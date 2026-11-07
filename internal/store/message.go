package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// MessageInput is a create request. An empty To leaves the message without
// an addressee and an empty TTL means model.DefaultMessageTTL.
type MessageInput struct {
	To    string
	Title string
	Body  string
	TTL   string
}

// MessageFilter selects the messages of a project. Without All, messages that
// are deleted or expired are left out. An empty To matches every addressee.
type MessageFilter struct {
	Project string
	To      string
	All     bool
	Limit   int
	Offset  int
}

const messageCols = `m.id, p.key, m.to_label, m.title, m.body, m.created_at, m.created_by, m.created_session, m.created_host,
	m.expires_at, m.deleted_at, m.deleted_by`

const messageFrom = ` from message m join project p on p.id = m.project_id`

// messageAge is how long ago a message was posted, in whole minutes below an
// hour, whole hours below a day and whole days after that.
func messageAge(created, now model.Time) string {
	d := now.Time().Sub(created.Time())
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d/time.Minute), 0))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

func scanMessage(sc rowScanner, now model.Time, withBody bool) (*model.Message, error) {
	var m model.Message
	var to, deletedAt, deletedBy sql.NullString
	var body, created, expires string
	if err := sc.Scan(&m.ID, &m.Project, &to, &m.Title, &body, &created, &m.CreatedBy, &m.CreatedSession, &m.CreatedHost,
		&expires, &deletedAt, &deletedBy); err != nil {
		return nil, err
	}
	var err error
	if m.CreatedAt, err = model.ParseTime(created); err != nil {
		return nil, err
	}
	if m.ExpiresAt, err = model.ParseTime(expires); err != nil {
		return nil, err
	}
	if to.Valid {
		m.To = &to.String
	}
	if withBody {
		m.Body = &body
	}
	m.State = model.MessageActive
	if !now.Before(m.ExpiresAt) {
		m.State = model.MessageExpired
	}
	if deletedAt.Valid {
		t, err := model.ParseTime(deletedAt.String)
		if err != nil {
			return nil, err
		}
		m.DeletedAt = &t
		m.DeletedBy = &deletedBy.String
		m.State = model.MessageDeleted
	}
	m.Age = messageAge(m.CreatedAt, now)
	return &m, nil
}

// GetMessage returns a message by id, deleted or expired ones included.
func (s *Store) GetMessage(ctx context.Context, id int64) (*model.Message, error) {
	return getMessage(ctx, s.db, id, s.stamp())
}

func getMessage(ctx context.Context, q querier, id int64, now model.Time) (*model.Message, error) {
	m, err := scanMessage(q.QueryRowContext(ctx, `select `+messageCols+messageFrom+` where m.id = ?`, id), now, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// ListMessages returns a page of the messages of a project, newest first,
// without their bodies, and the number of messages that match the filter.
func (s *Store) ListMessages(ctx context.Context, f MessageFilter) ([]model.Message, int, error) {
	if _, err := getProject(ctx, s.db, f.Project); err != nil {
		return nil, 0, err
	}
	now := s.stamp()
	cond := ` where p.key = ?`
	args := []any{f.Project}
	if f.To != "" {
		cond += ` and m.to_label = ?`
		args = append(args, f.To)
	}
	if !f.All {
		cond += ` and m.deleted_at is null and m.expires_at > ?`
		args = append(args, now.String())
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)`+messageFrom+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select `+messageCols+messageFrom+cond+` order by m.created_at desc, m.id desc limit ? offset ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Message{}
	for rows.Next() {
		m, err := scanMessage(rows, now, false)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *m)
	}
	return items, total, rows.Err()
}

// CreateMessage posts a message to a project. The actor, session and host of
// the trace are kept with it.
func (s *Store) CreateMessage(ctx context.Context, tr model.Trace, project string, in MessageInput) (*model.Message, error) {
	invalid := model.ValidateMessage(in.To, in.Title, in.Body, in.TTL)
	now := s.stamp()
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		var projectID int64
		var status string
		err := tx.QueryRowContext(ctx, `select id, status from project where key = ?`, project).Scan(&projectID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		if invalid != nil {
			return invalid
		}
		ttl, err := model.MessageTTL(in.TTL)
		if err != nil {
			return err
		}
		var to any
		if in.To != "" {
			to = in.To
		}
		expires := model.NewTime(now.Time().Add(ttl))
		res, err := tx.ExecContext(ctx, `insert into message(project_id, to_label, title, body, created_at, created_by, created_session, created_host, expires_at)
			values (?, ?, ?, ?, ?, ?, ?, ?, ?)`, projectID, to, in.Title, in.Body, now.String(), tr.Actor, tr.SessionID, tr.ClientHost, expires.String())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetMessage(ctx, id)
}

// DeleteMessage marks a message deleted. The row stays. Deleting a message
// that is already deleted changes nothing. A project that is not active
// takes no deletion, like any other write.
func (s *Store) DeleteMessage(ctx context.Context, tr model.Trace, id int64) (*model.Message, error) {
	now := s.stamp()
	err := s.write(ctx, func(tx *sql.Tx) error {
		if _, err := getMessage(ctx, tx, id, now); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRowContext(ctx, `select p.status from message m join project p on p.id = m.project_id where m.id = ?`, id).Scan(&status); err != nil {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		_, err := tx.ExecContext(ctx, `update message set deleted_at = ?, deleted_by = ? where id = ? and deleted_at is null`, now.String(), tr.Actor, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetMessage(ctx, id)
}
