// Package ingest implements the asynchronous search projection pipeline:
// job queue with SKIP LOCKED claiming and dead-letter isolation, content
// normalization, heading-aware semantic chunking, blue-green embedding
// generation management, and the background worker loop.
package ingest

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

// PostgreSQL LISTEN/NOTIFY channels for ingest coordination.
const (
	IngestNotifyChannel     = pg.IngestNotifyChannel
	IngestDoneNotifyChannel = "agb_ingest_job_done"

	DefaultMaxAttempts      = 5
	DefaultBaseBackoff      = 1 * time.Second
	DefaultMaxBackoff       = 5 * time.Minute
	DefaultWaitPollInterval = 50 * time.Millisecond
	maxLastErrorRunes       = 2000
)

// Compile-time check that Queue implements pg.IngestQueue.
var _ pg.IngestQueue = (*Queue)(nil)

// QueueConfig tunes retry backoff, attempt limits, and wait polling.
type QueueConfig struct {
	DefaultMaxAttempts int
	BaseBackoff        time.Duration
	MaxBackoff         time.Duration
	WaitPollInterval   time.Duration
}

// Queue manages ingest_job persistence, claiming, retries, dead-lettering,
// and completion waiting over PostgreSQL.
type Queue struct {
	pool               *pgxpool.Pool
	graphStore         *graph.Store
	defaultMaxAttempts int
	baseBackoff        time.Duration
	maxBackoff         time.Duration
	waitPollInterval   time.Duration
}

// NewQueue constructs a Queue backed by pool.
func NewQueue(pool *pgxpool.Pool, cfg QueueConfig) *Queue {
	maxAttempts := cfg.DefaultMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	baseBackoff := cfg.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = DefaultBaseBackoff
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = DefaultMaxBackoff
	}
	if maxBackoff < baseBackoff {
		maxBackoff = baseBackoff
	}
	pollInterval := cfg.WaitPollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultWaitPollInterval
	}
	return &Queue{
		pool:               pool,
		graphStore:         graph.NewStore(pool),
		defaultMaxAttempts: maxAttempts,
		baseBackoff:        baseBackoff,
		maxBackoff:         maxBackoff,
		waitPollInterval:   pollInterval,
	}
}

// WaitResult extends pg.JobResult with source coordinates and extracted
// candidate supersedes edges for synchronous wait callers.
type WaitResult struct {
	pg.JobResult
	SourceType           model.IngestSourceType   `json:"source_type"`
	SourceID             int64                    `json:"source_id"`
	SupersedesCandidates []pg.SupersedesCandidate `json:"supersedes_candidates"`
}

// Enqueue inserts a new pending ingest job in its own transaction and emits a
// pg_notify wake-up signal.
func (q *Queue) Enqueue(ctx context.Context, job pg.EnqueueInput) (int64, error) {
	return q.EnqueueWithMaxAttempts(ctx, job, q.defaultMaxAttempts)
}

// EnqueueWithMaxAttempts inserts a new pending ingest job with an explicit
// max_attempts limit.
func (q *Queue) EnqueueWithMaxAttempts(ctx context.Context, job pg.EnqueueInput, maxAttempts int) (int64, error) {
	if maxAttempts <= 0 {
		maxAttempts = q.defaultMaxAttempts
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	id, err := q.enqueueTx(ctx, tx, job, maxAttempts)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// EnqueueTx inserts a pending ingest job inside an existing write transaction
// and emits a pg_notify on IngestNotifyChannel.
func (q *Queue) EnqueueTx(ctx context.Context, tx pgx.Tx, job pg.EnqueueInput) (int64, error) {
	return q.enqueueTx(ctx, tx, job, q.defaultMaxAttempts)
}

func (q *Queue) enqueueTx(ctx context.Context, tx pgx.Tx, job pg.EnqueueInput, maxAttempts int) (int64, error) {
	stage := job.Stage
	if stage == "" {
		stage = model.IngestStageAll
	}
	var jobID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO ingest_job (source_type, source_id, source_sha256, stage, status, max_attempts)
		VALUES ($1, $2, $3, $4, 'pending', $5)
		RETURNING id`,
		string(job.SourceType), job.SourceID, job.SourceSHA256, string(stage), maxAttempts,
	).Scan(&jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, IngestNotifyChannel, strconv.FormatInt(jobID, 10)); err != nil {
		return 0, err
	}
	return jobID, nil
}

// ClaimNext atomically claims the next due pending job using
// SELECT ... FOR UPDATE SKIP LOCKED so concurrent workers never contend on the
// same row. Returns (nil, nil) when no job is ready.
func (q *Queue) ClaimNext(ctx context.Context, _ string) (*model.IngestJob, error) {
	row := q.pool.QueryRow(ctx, `
		WITH next_job AS (
			SELECT id
			FROM ingest_job
			WHERE status = 'pending'
			  AND next_attempt_at <= now()
			ORDER BY next_attempt_at ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE ingest_job j
		SET status = 'running',
		    attempts = j.attempts + 1,
		    started_at = now()
		FROM next_job
		WHERE j.id = next_job.id
		RETURNING j.id, j.source_type, j.source_id, j.source_sha256, j.stage,
		          j.status, j.attempts, j.max_attempts, j.next_attempt_at,
		          j.last_error, j.created_at, j.started_at, j.finished_at`)
	job, err := scanIngestJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// Complete marks a job as done and notifies wait subscribers.
func (q *Queue) Complete(ctx context.Context, jobID int64) error {
	return q.MarkDone(ctx, jobID)
}

// MarkDone transitions jobID to status='done' and emits pg_notify on
// IngestDoneNotifyChannel.
func (q *Queue) MarkDone(ctx context.Context, jobID int64) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE ingest_job
		SET status = 'done',
		    finished_at = now(),
		    last_error = NULL
		WHERE id = $1`,
		jobID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pg.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, IngestDoneNotifyChannel, strconv.FormatInt(jobID, 10)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fail records a job failure with exponential backoff based on attempts, or
// isolates the job into dead_letter once attempts >= max_attempts so subsequent
// jobs are never blocked.
func (q *Queue) Fail(ctx context.Context, jobID int64, cause error) (bool, error) {
	var attempts int
	err := q.pool.QueryRow(ctx, `SELECT attempts FROM ingest_job WHERE id = $1`, jobID).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, pg.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	backoff := q.backoffForAttempt(attempts)
	return q.MarkRetryOrDeadLetter(ctx, jobID, cause, backoff)
}

// MarkRetryOrDeadLetter transitions jobID to dead_letter when
// attempts >= max_attempts, otherwise reschedules it as pending after backoff.
func (q *Queue) MarkRetryOrDeadLetter(ctx context.Context, jobID int64, cause error, backoff time.Duration) (bool, error) {
	if backoff < 0 {
		backoff = 0
	}
	errMsg := formatJobError(cause)

	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var newStatus string
	err = tx.QueryRow(ctx, `
		UPDATE ingest_job
		SET status = CASE WHEN attempts >= max_attempts THEN 'dead_letter' ELSE 'pending' END,
		    last_error = $2,
		    next_attempt_at = CASE WHEN attempts >= max_attempts THEN next_attempt_at ELSE now() + ($3 * interval '1 millisecond') END,
		    finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END
		WHERE id = $1
		RETURNING status`,
		jobID, errMsg, float64(backoff.Microseconds())/1000.0,
	).Scan(&newStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, pg.ErrNotFound
	}
	if err != nil {
		return false, err
	}

	deadLettered := newStatus == string(model.IngestStatusDeadLetter)
	if deadLettered {
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, IngestDoneNotifyChannel, strconv.FormatInt(jobID, 10)); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return deadLettered, nil
}

// DeadLetter immediately isolates jobID into dead_letter regardless of remaining
// attempts and notifies wait subscribers.
func (q *Queue) DeadLetter(ctx context.Context, jobID int64, cause error) error {
	errMsg := formatJobError(cause)
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE ingest_job
		SET status = 'dead_letter',
		    last_error = $2,
		    finished_at = now()
		WHERE id = $1`,
		jobID, errMsg,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pg.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, IngestDoneNotifyChannel, strconv.FormatInt(jobID, 10)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListDeadLetters returns dead-lettered jobs ordered newest first.
func (q *Queue) ListDeadLetters(ctx context.Context, limit int) ([]model.IngestJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := q.pool.Query(ctx, `
		SELECT id, source_type, source_id, source_sha256, stage,
		       status, attempts, max_attempts, next_attempt_at,
		       last_error, created_at, started_at, finished_at
		FROM ingest_job
		WHERE status = 'dead_letter'
		ORDER BY created_at DESC, id DESC
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.IngestJob, 0)
	for rows.Next() {
		job, err := scanIngestJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// RequeueDeadLetter resets a dead-lettered job back to pending with attempts=0
// and emits a pg_notify wake-up signal.
func (q *Queue) RequeueDeadLetter(ctx context.Context, jobID int64) (model.IngestJob, error) {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return model.IngestJob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		UPDATE ingest_job
		SET status = 'pending',
		    attempts = 0,
		    next_attempt_at = now(),
		    last_error = NULL,
		    started_at = NULL,
		    finished_at = NULL
		WHERE id = $1 AND status = 'dead_letter'
		RETURNING id, source_type, source_id, source_sha256, stage,
		          status, attempts, max_attempts, next_attempt_at,
		          last_error, created_at, started_at, finished_at`,
		jobID,
	)
	job, err := scanIngestJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.IngestJob{}, pg.ErrNotFound
	}
	if err != nil {
		return model.IngestJob{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, IngestNotifyChannel, strconv.FormatInt(jobID, 10)); err != nil {
		return model.IngestJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.IngestJob{}, err
	}
	return job, nil
}

// GetJob fetches a single ingest job by ID.
func (q *Queue) GetJob(ctx context.Context, jobID int64) (model.IngestJob, error) {
	row := q.pool.QueryRow(ctx, `
		SELECT id, source_type, source_id, source_sha256, stage,
		       status, attempts, max_attempts, next_attempt_at,
		       last_error, created_at, started_at, finished_at
		FROM ingest_job
		WHERE id = $1`,
		jobID,
	)
	job, err := scanIngestJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.IngestJob{}, pg.ErrNotFound
	}
	return job, err
}

// WaitForJob implements pg.IngestQueue by delegating to Wait.
func (q *Queue) WaitForJob(ctx context.Context, jobID int64, timeout time.Duration) (pg.JobResult, error) {
	res, err := q.Wait(ctx, jobID, timeout)
	return res.JobResult, err
}

// Wait blocks until jobID reaches done or dead_letter, or until timeout
// expires. It listens on PostgreSQL LISTEN/NOTIFY with a short polling
// fallback and returns any candidate supersedes edges attached to the source.
func (q *Queue) Wait(ctx context.Context, jobID int64, timeout time.Duration) (WaitResult, error) {
	waitCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Fast path check before acquiring a dedicated LISTEN connection.
	if res, terminal, err := q.checkTerminalJob(waitCtx, jobID); err != nil {
		return WaitResult{}, err
	} else if terminal {
		return res, nil
	}

	// Acquire a dedicated connection for LISTEN. If acquiring fails, fall back
	// gracefully to polling so Wait stays resilient under pool pressure.
	notifyCh := make(chan string, 16)
	var stopListen func()
	if conn, err := q.pool.Acquire(waitCtx); err == nil {
		if _, err1 := conn.Exec(waitCtx, `LISTEN `+IngestDoneNotifyChannel); err1 == nil {
			_, _ = conn.Exec(waitCtx, `LISTEN `+IngestNotifyChannel)
			listenCtx, listenCancel := context.WithCancel(waitCtx)
			doneCh := make(chan struct{})
			go func() {
				defer close(doneCh)
				for {
					n, nErr := conn.Conn().WaitForNotification(listenCtx)
					if nErr != nil {
						return
					}
					select {
					case notifyCh <- n.Payload:
					default:
					}
				}
			}()
			stopListen = func() {
				listenCancel()
				<-doneCh
				conn.Release()
			}
		} else {
			conn.Release()
		}
	}
	if stopListen != nil {
		defer stopListen()
	}

	// Re-check immediately after LISTEN to close the race window between initial
	// check and subscription registration.
	if res, terminal, err := q.checkTerminalJob(waitCtx, jobID); err != nil {
		return WaitResult{}, err
	} else if terminal {
		return res, nil
	}

	ticker := time.NewTicker(q.waitPollInterval)
	defer ticker.Stop()
	targetPayload := strconv.FormatInt(jobID, 10)

	for {
		select {
		case payload := <-notifyCh:
			if payload != "" && payload != targetPayload {
				continue
			}
			if res, terminal, err := q.checkTerminalJob(waitCtx, jobID); err != nil {
				return WaitResult{}, err
			} else if terminal {
				return res, nil
			}
		case <-ticker.C:
			if res, terminal, err := q.checkTerminalJob(waitCtx, jobID); err != nil {
				return WaitResult{}, err
			} else if terminal {
				return res, nil
			}
		case <-waitCtx.Done():
			// Distinguish caller cancellation from wait timeout.
			if ctx.Err() != nil {
				return WaitResult{}, ctx.Err()
			}
			job, gErr := q.GetJob(context.Background(), jobID)
			if gErr != nil {
				return WaitResult{
					JobResult:            pg.JobResult{JobID: jobID, Status: model.IngestStatusPending, TimedOut: true},
					SupersedesCandidates: []pg.SupersedesCandidate{},
				}, nil
			}
			res := WaitResult{
				JobResult: pg.JobResult{
					JobID:    job.ID,
					Status:   job.Status,
					TimedOut: job.Status != model.IngestStatusDone && job.Status != model.IngestStatusDeadLetter,
				},
				SourceType:           job.SourceType,
				SourceID:             job.SourceID,
				SupersedesCandidates: []pg.SupersedesCandidate{},
			}
			if job.LastError != nil {
				res.LastError = *job.LastError
			}
			return res, nil
		}
	}
}

func (q *Queue) checkTerminalJob(ctx context.Context, jobID int64) (WaitResult, bool, error) {
	job, err := q.GetJob(ctx, jobID)
	if err != nil {
		return WaitResult{}, false, err
	}
	if job.Status != model.IngestStatusDone && job.Status != model.IngestStatusDeadLetter {
		return WaitResult{}, false, nil
	}
	res := WaitResult{
		JobResult: pg.JobResult{
			JobID:    job.ID,
			Status:   job.Status,
			TimedOut: false,
		},
		SourceType:           job.SourceType,
		SourceID:             job.SourceID,
		SupersedesCandidates: []pg.SupersedesCandidate{},
	}
	if job.LastError != nil {
		res.LastError = *job.LastError
	}
	if nodeType, ok := ingestSourceToGraphNode(job.SourceType); ok {
		candidates, cErr := q.graphStore.ListCandidatesForSource(ctx, nodeType, job.SourceID)
		if cErr != nil {
			return WaitResult{}, false, cErr
		}
		res.SupersedesCandidates = candidates
	}
	return res, true, nil
}

func (q *Queue) backoffForAttempt(attempts int) time.Duration {
	if attempts <= 1 {
		return q.baseBackoff
	}
	shift := attempts - 1
	if shift > 20 {
		shift = 20
	}
	delay := q.baseBackoff << shift
	if delay <= 0 || delay > q.maxBackoff {
		return q.maxBackoff
	}
	return delay
}

func ingestSourceToGraphNode(st model.IngestSourceType) (model.GraphNodeType, bool) {
	switch st {
	case model.IngestSourceIssue:
		return model.GraphNodeIssue, true
	case model.IngestSourceSpec:
		return model.GraphNodeSpec, true
	case model.IngestSourceRecord:
		return model.GraphNodeRecord, true
	default:
		return "", false
	}
}

func formatJobError(cause error) string {
	if cause == nil {
		return "unknown ingest error"
	}
	msg := strings.TrimSpace(cause.Error())
	if msg == "" {
		return "unknown ingest error"
	}
	r := []rune(msg)
	if len(r) > maxLastErrorRunes {
		return string(r[:maxLastErrorRunes]) + "..."
	}
	return msg
}

type jobScanner interface {
	Scan(dest ...any) error
}

func scanIngestJob(row jobScanner) (model.IngestJob, error) {
	var (
		j             model.IngestJob
		sourceType    string
		stage         string
		status        string
		nextAttemptAt time.Time
		createdAt     time.Time
		startedAt     *time.Time
		finishedAt    *time.Time
	)
	if err := row.Scan(
		&j.ID, &sourceType, &j.SourceID, &j.SourceSHA256, &stage,
		&status, &j.Attempts, &j.MaxAttempts, &nextAttemptAt,
		&j.LastError, &createdAt, &startedAt, &finishedAt,
	); err != nil {
		return model.IngestJob{}, err
	}
	j.SourceType = model.IngestSourceType(sourceType)
	j.Stage = model.IngestStage(stage)
	j.Status = model.IngestStatus(status)
	j.NextAttemptAt = model.NewTime(nextAttemptAt)
	j.CreatedAt = model.NewTime(createdAt)
	if startedAt != nil {
		t := model.NewTime(*startedAt)
		j.StartedAt = &t
	}
	if finishedAt != nil {
		t := model.NewTime(*finishedAt)
		j.FinishedAt = &t
	}
	return j, nil
}
