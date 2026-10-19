package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// Sort is one ordering term of a list query.
type Sort struct {
	Field string
	Desc  bool
}

// ProjectFilter selects and orders projects.
type ProjectFilter struct {
	Statuses []model.ProjectStatus
	Sort     []Sort // key, created_at, updated_at
	Limit    int
	Offset   int
}

// ProjectPatch is a merge patch for a project. Nil fields are unchanged.
type ProjectPatch struct {
	Name   *string
	Status *model.ProjectStatus
}

const projectCols = `p.key, p.name, p.status, p.created_at, p.updated_at, p.created_by, p.updated_by`

func scanProject(sc interface{ Scan(...any) error }) (*model.Project, error) {
	var p model.Project
	var created, updated string
	if err := sc.Scan(&p.Key, &p.Name, &p.Status, &created, &updated, &p.CreatedBy, &p.UpdatedBy); err != nil {
		return nil, err
	}
	var err error
	if p.CreatedAt, err = model.ParseTime(created); err != nil {
		return nil, err
	}
	if p.UpdatedAt, err = model.ParseTime(updated); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateProject inserts a project in active state.
func (s *Store) CreateProject(ctx context.Context, tr model.Trace, key, name string) (*model.Project, error) {
	if err := model.ValidateProject(key, name); err != nil {
		return nil, err
	}
	now := s.stamp().String()
	err := s.write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `select count(*) from project where key = ?`, key).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrDuplicateKey
		}
		_, err := tx.ExecContext(ctx,
			`insert into project(key, name, status, created_at, updated_at, created_by, updated_by) values (?, ?, ?, ?, ?, ?, ?)`,
			key, name, string(model.ProjectActive), now, now, tr.Actor, tr.Actor)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, key)
}

// GetProject returns a project by key regardless of its status.
func (s *Store) GetProject(ctx context.Context, key string) (*model.Project, error) {
	return getProject(ctx, s.db, key)
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func getProject(ctx context.Context, q querier, key string) (*model.Project, error) {
	p, err := scanProject(q.QueryRowContext(ctx, `select `+projectCols+` from project p where p.key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListProjects returns a page of projects and the total matching count.
func (s *Store) ListProjects(ctx context.Context, f ProjectFilter) ([]model.Project, int, error) {
	var where []string
	var args []any
	if len(f.Statuses) > 0 {
		where = append(where, "p.status in ("+placeholders(len(f.Statuses))+")")
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	cond := ""
	if len(where) > 0 {
		cond = " where " + strings.Join(where, " and ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from project p`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order, err := orderBy(f.Sort, map[string][]string{"key": {"p.key"}, "created_at": {"p.created_at"}, "updated_at": {"p.updated_at"}}, "p.key", "p.id")
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select `+projectCols+` from project p`+cond+order+` limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *p)
	}
	return items, total, rows.Err()
}

// UpdateProject applies a merge patch. A patch that changes nothing leaves
// updated_at and updated_by untouched.
func (s *Store) UpdateProject(ctx context.Context, tr model.Trace, key string, patch ProjectPatch) (*model.Project, error) {
	if patch.Name != nil {
		if err := model.ValidateProjectName(*patch.Name); err != nil {
			return nil, err
		}
	}
	if patch.Status != nil {
		if _, ok := model.ParseProjectStatus(string(*patch.Status)); !ok {
			return nil, &model.ValidationError{Fields: []model.FieldError{{Field: "status", Message: "active, archived, deleted 중 하나여야 합니다."}}}
		}
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		cur, err := getProject(ctx, tx, key)
		if err != nil {
			return err
		}
		name, status := cur.Name, cur.Status
		if patch.Name != nil {
			name = *patch.Name
		}
		if patch.Status != nil {
			status = *patch.Status
		}
		if name == cur.Name && status == cur.Status {
			return nil
		}
		_, err = tx.ExecContext(ctx, `update project set name = ?, status = ?, updated_at = ?, updated_by = ? where key = ?`,
			name, string(status), s.stamp().String(), tr.Actor, key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, key)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// orderBy builds an ORDER BY clause from API sort terms using the given
// column map, falling back to def, and always ending with the tiebreaker.
func orderBy(sorts []Sort, cols map[string][]string, def, tiebreak string) (string, error) {
	var terms []string
	for _, s := range sorts {
		list, ok := cols[s.Field]
		if !ok {
			return "", fmt.Errorf("unknown sort field %q", s.Field)
		}
		for _, col := range list {
			if s.Desc {
				col += " desc"
			}
			terms = append(terms, col)
		}
	}
	if len(terms) == 0 {
		terms = append(terms, def)
	}
	// Ties follow the first sort, so a newest-first list stays newest first.
	if len(sorts) > 0 && sorts[0].Desc {
		tiebreak += " desc"
	}
	terms = append(terms, tiebreak)
	return " order by " + strings.Join(terms, ", "), nil
}
