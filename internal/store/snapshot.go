package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// Snapshot writes a consistent copy of the database to dst with VACUUM INTO.
// The copy is a complete single-file database without a WAL. dst must not
// exist. Readers and writers are not blocked beyond a normal read.
func (s *Store) Snapshot(ctx context.Context, dst string) error {
	path := filepath.ToSlash(dst)
	if strings.Contains(path, "'") {
		return fmt.Errorf("snapshot path must not contain a quote")
	}
	_, err := s.db.ExecContext(ctx, "vacuum into '"+path+"'")
	return err
}

// FingerprintNow returns a string that changes whenever the database
// content changes: every issue write appends an activity row, every
// project write bumps project.updated_at, and migrations bump user_version.
func (s *Store) FingerprintNow(ctx context.Context) (string, error) {
	var maxActivity sql.NullInt64
	var maxProject sql.NullString
	var projects, actors int
	var version int
	if err := s.db.QueryRowContext(ctx, `select (select max(id) from activity), (select max(updated_at) from project),
		(select count(*) from project), (select count(*) from actor)`).Scan(&maxActivity, &maxProject, &projects, &actors); err != nil {
		return "", err
	}
	if err := s.db.QueryRowContext(ctx, `pragma user_version`).Scan(&version); err != nil {
		return "", err
	}
	return fmt.Sprintf("v%d:a%d:p%s:n%d:c%d", version, maxActivity.Int64, maxProject.String, projects, actors), nil
}
