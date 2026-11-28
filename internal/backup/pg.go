package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5"
)

// stderrLimit bounds how much of a tool's error output is kept in an error.
const stderrLimit = 2048

// PGSource snapshots a PostgreSQL database with pg_dump in the custom archive
// format. The connection settings reach the tool through its environment, never
// through its arguments, so a password does not show in the process list.
type PGSource struct {
	DSN string
	// DumpBin is the pg_dump executable; empty means "pg_dump" from PATH.
	DumpBin string
}

// NewPGSource validates dsn and returns a source for it. It does not connect.
func NewPGSource(dsn string) (*PGSource, error) {
	if _, err := pgEnv(dsn); err != nil {
		return nil, err
	}
	return &PGSource{DSN: dsn}, nil
}

// Archive reports that the snapshot is already compressed.
func (s *PGSource) Archive() bool { return true }

// Snapshot writes a pg_dump custom-format archive of the database to dst.
func (s *PGSource) Snapshot(ctx context.Context, dst string) error {
	env, err := pgEnv(s.DSN)
	if err != nil {
		return err
	}
	bin := s.DumpBin
	if bin == "" {
		bin = "pg_dump"
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "--format=custom", "--no-owner", "--no-privileges")
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = out
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := out.Close()
	if runErr != nil {
		return fmt.Errorf("pg_dump: %w: %s", runErr, stderr.String())
	}
	return closeErr
}

// FingerprintNow returns a value that changes when the database is written to:
// the current WAL insert position. An idle database keeps the same value.
func (s *PGSource) FingerprintNow(ctx context.Context) (string, error) {
	conn, err := pgx.Connect(ctx, s.DSN)
	if err != nil {
		return "", errors.New("connect to PostgreSQL for the backup fingerprint failed")
	}
	defer conn.Close(ctx)
	var lsn string
	if err := conn.QueryRow(ctx, `SELECT pg_current_wal_insert_lsn()::text`).Scan(&lsn); err != nil {
		return "", fmt.Errorf("wal position: %w", err)
	}
	return lsn, nil
}

// RestorePG loads a pg_dump custom archive from dumpPath into the database dsn
// names, replacing objects that already exist. restoreBin is the pg_restore
// executable; empty means "pg_restore" from PATH. The archive is fed on standard
// input.
func RestorePG(ctx context.Context, dumpPath, dsn, restoreBin string) error {
	env, err := pgEnv(dsn)
	if err != nil {
		return err
	}
	u, _ := url.Parse(dsn)
	dbname := strings.TrimPrefix(u.Path, "/")
	if dbname == "" {
		return errors.New("restore target must name a database")
	}
	if restoreBin == "" {
		restoreBin = "pg_restore"
	}
	in, err := os.Open(dumpPath)
	if err != nil {
		return err
	}
	defer in.Close()
	cmd := exec.CommandContext(ctx, restoreBin, "--clean", "--if-exists", "--no-owner", "--no-privileges", "--exit-on-error", "--dbname="+dbname)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = in
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore: %w: %s", err, stderr.String())
	}
	return nil
}

// pgEnv turns a postgres:// URL into the libpq environment variables that
// carry its parts.
func pgEnv(dsn string) ([]string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" {
		return nil, errors.New("backup database URL must be a postgres:// URL with a host")
	}
	env := []string{"PGHOST=" + u.Hostname()}
	if p := u.Port(); p != "" {
		env = append(env, "PGPORT="+p)
	}
	if name := strings.TrimPrefix(u.Path, "/"); name != "" {
		env = append(env, "PGDATABASE="+name)
	}
	if u.User != nil {
		if n := u.User.Username(); n != "" {
			env = append(env, "PGUSER="+n)
		}
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
		}
	}
	if m := u.Query().Get("sslmode"); m != "" {
		env = append(env, "PGSSLMODE="+m)
	}
	return env, nil
}

// limitedBuffer keeps the first stderrLimit bytes written to it.
type limitedBuffer struct{ buf bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := stderrLimit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return strings.TrimSpace(b.buf.String()) }

var _ io.Writer = (*limitedBuffer)(nil)
