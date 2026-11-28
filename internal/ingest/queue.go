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
	"sync"
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

	mu            sync.Mutex
	waiters       map[int64]map[chan struct{}]struct{}
	activeWaiters int
	dispCancel    context.CancelFunc
	dispDone      chan struct{}
	stoppingDone  chan struct{}
	closed        bool
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
		waiters:            make(map[int64]map[chan struct{}]struct{}),
	}
}

// Close shuts down the shared completion LISTEN dispatcher and ensures UNLISTEN *
// runs before returning its PostgreSQL connection to the pool.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	cancel := q.dispCancel
	done := q.dispDone
	stopping := q.stoppingDone
	q.dispCancel = nil
	q.dispDone = nil
	q.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	} else if stopping != nil {
		<-stopping
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
// expires. It multiplexes notifications over a single shared LISTEN connection
// on IngestDoneNotifyChannel with a short polling fallback and returns any
// candidate supersedes edges attached to the source.
func (q *Queue) Wait(ctx context.Context, jobID int64, timeout time.Duration) (WaitResult, error) {
	waitCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	checkNow := func() (WaitResult, bool, bool, error) {
		res, terminal, err := q.checkTerminalJob(waitCtx, jobID)
		if err != nil {
			if ctx.Err() != nil {
				return WaitResult{}, false, false, ctx.Err()
			}
			if waitCtx.Err() != nil {
				finalRes, finalErr := q.finalWaitResult(ctx, jobID)
				return finalRes, true, true, finalErr
			}
			return WaitResult{}, false, false, err
		}
		return res, terminal, false, nil
	}

	// Fast path check before registering on the shared LISTEN dispatcher.
	if res, done, _, err := checkNow(); err != nil {
		return WaitResult{}, err
	} else if done {
		return res, nil
	}

	notifyCh, unsub := q.subscribeDone(jobID)
	defer unsub()

	// Re-check immediately after subscription to close the race window between
	// initial check and listener registration.
	if res, done, _, err := checkNow(); err != nil {
		return WaitResult{}, err
	} else if done {
		return res, nil
	}

	ticker := time.NewTicker(q.waitPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-notifyCh:
			if res, done, _, err := checkNow(); err != nil {
				return WaitResult{}, err
			} else if done {
				return res, nil
			}
		case <-ticker.C:
			if res, done, _, err := checkNow(); err != nil {
				return WaitResult{}, err
			} else if done {
				return res, nil
			}
		case <-waitCtx.Done():
			return q.finalWaitResult(ctx, jobID)
		}
	}
}

func (q *Queue) finalWaitResult(ctx context.Context, jobID int64) (WaitResult, error) {
	// Distinguish caller cancellation from wait timeout.
	if ctx.Err() != nil {
		return WaitResult{}, ctx.Err()
	}
	finalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	job, gErr := q.GetJob(finalCtx, jobID)
	if gErr != nil {
		if errors.Is(gErr, pg.ErrNotFound) {
			return WaitResult{}, gErr
		}
		return WaitResult{
			JobResult:            pg.JobResult{JobID: jobID, Status: model.IngestStatusPending, TimedOut: true},
			SupersedesCandidates: []pg.SupersedesCandidate{},
		}, nil
	}
	terminal := job.Status == model.IngestStatusDone || job.Status == model.IngestStatusDeadLetter
	res := WaitResult{
		JobResult: pg.JobResult{
			JobID:    job.ID,
			Status:   job.Status,
			TimedOut: !terminal,
		},
		SourceType:           job.SourceType,
		SourceID:             job.SourceID,
		SupersedesCandidates: []pg.SupersedesCandidate{},
	}
	if job.LastError != nil {
		res.LastError = *job.LastError
	}
	if job.Status == model.IngestStatusDone {
		if nodeType, ok := ingestSourceToGraphNode(job.SourceType); ok {
			if candidates, cErr := q.graphStore.ListCandidatesForSource(finalCtx, nodeType, job.SourceID); cErr == nil && candidates != nil {
				res.SupersedesCandidates = candidates
			}
		}
	}
	return res, nil
}

func (q *Queue) subscribeDone(jobID int64) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	q.mu.Lock()
	if q.waiters == nil {
		q.waiters = make(map[int64]map[chan struct{}]struct{})
	}
	if q.waiters[jobID] == nil {
		q.waiters[jobID] = make(map[chan struct{}]struct{})
	}
	q.waiters[jobID][ch] = struct{}{}
	q.activeWaiters++
	if !q.closed && q.dispCancel == nil {
		dispCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		prevDone := q.stoppingDone
		q.dispCancel = cancel
		q.dispDone = done
		go q.runDoneListener(dispCtx, prevDone, done)
	}
	q.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			q.mu.Lock()
			if set := q.waiters[jobID]; set != nil {
				delete(set, ch)
				if len(set) == 0 {
					delete(q.waiters, jobID)
				}
			}
			q.activeWaiters--
			var cancel context.CancelFunc
			var done chan struct{}
			if q.activeWaiters == 0 && q.dispCancel != nil {
				cancel = q.dispCancel
				done = q.dispDone
				q.dispCancel = nil
				q.dispDone = nil
				q.stoppingDone = done
			}
			q.mu.Unlock()

			if cancel != nil {
				cancel()
				<-done
			}
		})
	}
	return ch, unsub
}

func (q *Queue) runDoneListener(ctx context.Context, prevDone, done chan struct{}) {
	defer close(done)
	if prevDone != nil {
		select {
		case <-ctx.Done():
			return
		case <-prevDone:
		}
	}

	retryDelay := min(q.waitPollInterval, 100*time.Millisecond)
	if retryDelay <= 0 {
		retryDelay = 25 * time.Millisecond
	}

	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := q.pool.Acquire(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}
		if _, err := conn.Exec(ctx, `LISTEN `+IngestDoneNotifyChannel); err != nil {
			releaseListenConn(conn)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}

		// Wake any waiters registered while acquiring/reconnecting so no
		// completion signal emitted during the window is missed.
		q.broadcastWaiters()

		for {
			n, nErr := conn.Conn().WaitForNotification(ctx)
			if nErr != nil {
				break
			}
			q.dispatchDonePayload(n.Payload)
		}
		releaseListenConn(conn)
	}
}

func (q *Queue) dispatchDonePayload(payload string) {
	id, err := strconv.ParseInt(strings.TrimSpace(payload), 10, 64)
	if err != nil {
		q.broadcastWaiters()
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for ch := range q.waiters[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (q *Queue) broadcastWaiters() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, set := range q.waiters {
		for ch := range set {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func releaseListenConn(conn *pgxpool.Conn) {
	if conn == nil {
		return
	}
	unlistenCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := conn.Exec(unlistenCtx, `UNLISTEN *`); err != nil {
		_ = conn.Conn().Close(unlistenCtx)
	}
	conn.Release()
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
