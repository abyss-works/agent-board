package pg

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationAdvisoryLockKey serializes schema migrations across concurrent
// server startups against the same PostgreSQL database.
const migrationAdvisoryLockKey int64 = 741852963

// Compile-time check that Store implements DomainStore.
var _ DomainStore = (*Store)(nil)

// Store is the PostgreSQL 17 domain store.
type Store struct {
	pool      *pgxpool.Pool
	clockMu   sync.RWMutex
	nowFn     func() time.Time
	hookMu    sync.RWMutex
	eventHook func([]model.Event)
}

// Open connects to PostgreSQL, verifies connectivity, and applies any pending
// SQL migrations under an advisory lock.
func Open(ctx context.Context, dsn string, maxConns int) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("parse database configuration failed")
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	s := &Store{
		pool:  pool,
		nowFn: time.Now,
	}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

// Pool exposes the underlying pgxpool for search and graph layers.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close shuts down the connection pool.
func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// SetClock replaces the time source used for timestamps.
func (s *Store) SetClock(now func() time.Time) {
	s.clockMu.Lock()
	if now == nil {
		s.nowFn = time.Now
	} else {
		s.nowFn = now
	}
	s.clockMu.Unlock()
}

func (s *Store) now() time.Time {
	s.clockMu.RLock()
	fn := s.nowFn
	s.clockMu.RUnlock()
	return fn().UTC().Truncate(time.Millisecond)
}

func (s *Store) stamp() model.Time { return model.NewTime(s.now()) }

// SetEventHook registers a callback invoked with committed events after each
// write transaction commits.
func (s *Store) SetEventHook(fn func([]model.Event)) {
	s.hookMu.Lock()
	s.eventHook = fn
	s.hookMu.Unlock()
}

func (s *Store) notifyEvents(events []model.Event) {
	if len(events) == 0 {
		return
	}
	s.hookMu.RLock()
	hook := s.eventHook
	s.hookMu.RUnlock()
	if hook != nil {
		hook(events)
	}
}

// DBSize returns the current PostgreSQL database size in bytes.
func (s *Store) DBSize(ctx context.Context) (int64, error) {
	var size int64
	if err := s.pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&size); err != nil {
		return 0, err
	}
	return size, nil
}

func (s *Store) migrate(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := tx.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range names {
		ver, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("parse migration version %q: %w", name, err)
		}
		if applied[ver] {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, ver, name); err != nil {
			return fmt.Errorf("record %s: %w", name, err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) withTx(ctx context.Context, fn func(tx pgx.Tx, events *[]model.Event) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var events []model.Event
	if err := fn(tx, &events); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.notifyEvents(events)
	return nil
}

func emitEventTx(ctx context.Context, tx pgx.Tx, events *[]model.Event, at time.Time, projectID *int64, projectKey *string, kind, subject, actor, sessionID string, data map[string]any) error {
	payload := []byte("{}")
	if len(data) > 0 {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		payload = raw
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO event (at, project_id, kind, subject, actor, session_id, data)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		 RETURNING id`,
		at, projectID, kind, subject, actor, sessionID, string(payload),
	).Scan(&id); err != nil {
		return err
	}
	*events = append(*events, model.Event{
		ID:        id,
		At:        model.NewTime(at),
		Project:   projectKey,
		Kind:      kind,
		Subject:   subject,
		Actor:     actor,
		SessionID: sessionID,
		Data:      json.RawMessage(payload),
	})
	return nil
}

func enqueueIngestJobTx(ctx context.Context, tx pgx.Tx, sourceType model.IngestSourceType, sourceID int64, sourceSHA256 string) (int64, error) {
	var jobID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO ingest_job (source_type, source_id, source_sha256, stage, status)
		 VALUES ($1, $2, $3, 'all', 'pending')
		 RETURNING id`,
		string(sourceType), sourceID, sourceSHA256,
	).Scan(&jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, IngestNotifyChannel, strconv.FormatInt(jobID, 10)); err != nil {
		return 0, err
	}
	return jobID, nil
}

func insertResourceActivityTx(ctx context.Context, tx pgx.Tx, projectID int64, resource model.ResourceKind, resourceID int64, kind model.ResourceActivityKind, tr Trace, at time.Time, changes map[string]model.Change) (int64, error) {
	raw, err := json.Marshal(changes)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO resource_activity (project_id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
		 RETURNING id`,
		projectID, string(resource), resourceID, string(kind), tr.Actor, tr.SessionID, tr.ClientHost, at, string(raw),
	).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func insertDocumentActivityTx(ctx context.Context, tx pgx.Tx, targetType string, targetID int64, tr Trace, at time.Time, changes map[string]model.Change) (int64, error) {
	raw, err := json.Marshal(changes)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO document_activity (target_type, target_id, actor_id, session_id, client_host, at, changes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		 RETURNING id`,
		targetType, targetID, tr.Actor, tr.SessionID, tr.ClientHost, at, string(raw),
	).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func jsonRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

func makeChange(from, to any) model.Change {
	var c model.Change
	if from != nil {
		c.From = jsonRaw(from)
	}
	c.To = jsonRaw(to)
	return c
}

func violation(field, reason, msg string) error {
	return &model.ValidationError{Fields: []model.FieldError{{Field: field, Reason: reason, Message: msg}}}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func toModelTimePtr(t *time.Time) *model.Time {
	if t == nil {
		return nil
	}
	mt := model.NewTime(*t)
	return &mt
}

func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func marshalStringArray(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(items)
	return string(b)
}

func unmarshalStringArray(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return []string{}
	}
	return out
}
