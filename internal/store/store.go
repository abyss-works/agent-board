// Package store persists projects, issues and activity in a single SQLite
// file. It owns every transactional business rule: key issuance, cycle
// checks, version bumps and the activity record written with each change.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wsk0715/agent-board/internal/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// SchemaVersion is the user_version the store expects after migrating.
const SchemaVersion = 6

// Sentinel errors returned by the store. HTTP maps them to status codes.
var (
	ErrNotFound         = errors.New("not found")
	ErrDuplicateKey     = errors.New("duplicate key")
	ErrProjectNotActive = errors.New("project not active")
	ErrCycle            = errors.New("cycle")
	ErrVersionMismatch  = errors.New("version mismatch")
)

// RefError reports an issue key referenced by a field that does not exist.
type RefError struct {
	Field string // "parent" or "depends_on[i]"
	Key   string
}

func (e *RefError) Error() string { return fmt.Sprintf("%s: issue %s not found", e.Field, e.Key) }

// Store is the SQLite-backed persistence layer. One Store per database file.
type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
	now     func() time.Time
}

// DSN builds the connection string used for path: WAL, busy timeout,
// foreign keys and immediate write transactions.
func DSN(path string) string {
	return "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
}

// Open opens or creates the database at path and applies pending migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for maintenance such as VACUUM INTO.
func (s *Store) DB() *sql.DB { return s.db }

// SetClock replaces the time source. Tests use it for deterministic stamps.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

func (s *Store) stamp() model.Time { return model.NewTime(s.now()) }

func (s *Store) migrate(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var current int
	if err := s.db.QueryRowContext(ctx, "pragma user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for _, name := range names {
		n, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration name %q: %w", name, err)
		}
		if n <= current {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current = n
	}
	if current != SchemaVersion {
		return fmt.Errorf("schema version %d, expected %d", current, SchemaVersion)
	}
	return nil
}

// EnsureActors inserts or updates the declared actors. Rows for actors no
// longer declared are kept so activity records stay valid.
func (s *Store) EnsureActors(ctx context.Context, actors []model.Actor) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		now := s.stamp().String()
		for _, a := range actors {
			if _, err := tx.ExecContext(ctx,
				`insert into actor(id, kind, created_at) values (?, ?, ?)
				 on conflict(id) do update set kind = excluded.kind`, a.ID, string(a.Kind), now); err != nil {
				return err
			}
		}
		return nil
	})
}

// write runs fn in a serialized write transaction.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
