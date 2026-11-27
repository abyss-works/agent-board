package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	defaultEventLimit = 100
	maxEventLimit     = 500
)

// EventFilter selects events strictly after After, ordered by id ascending.
// Kinds are OR-combined prefix matches against event.kind.
type EventFilter struct {
	After   int64
	Project string
	Kinds   []string
	Limit   int
}

func marshalEventData(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(b) == 0 || string(b) == "null" {
		return []byte("{}"), nil
	}
	return b, nil
}

func changeFields(changes map[string]model.Change) []string {
	fields := make([]string, 0, len(changes))
	for k := range changes {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields
}

// emitEventTx inserts an event row in the active write transaction and stages
// it in s.pending so Store.write delivers it to the event hook only after
// commit and lock release.
func (s *Store) emitEventTx(ctx context.Context, tx *sql.Tx, at model.Time, projectID *int64, projectKey *string, kind, subject, actor, sessionID string, data any) error {
	raw, err := marshalEventData(data)
	if err != nil {
		return err
	}
	var projIDVal any
	var projKey *string
	if projectID != nil {
		projIDVal = *projectID
		if projectKey != nil {
			k := *projectKey
			projKey = &k
		} else {
			var k string
			if err := tx.QueryRowContext(ctx, `select key from project where id = ?`, *projectID).Scan(&k); err != nil {
				return err
			}
			projKey = &k
		}
	}
	res, err := tx.ExecContext(ctx,
		`insert into event(at, project_id, kind, subject, actor, session_id, data) values (?, ?, ?, ?, ?, ?, ?)`,
		at.String(), projIDVal, kind, subject, actor, sessionID, string(raw))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	s.pending = append(s.pending, model.Event{
		ID:        id,
		At:        at,
		Project:   projKey,
		Kind:      kind,
		Subject:   subject,
		Actor:     actor,
		SessionID: sessionID,
		Data:      json.RawMessage(raw),
	})
	return nil
}

// LatestEventID returns the highest stored event id, or 0 when no events exist.
func (s *Store) LatestEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(id), 0) from event`).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// ListEvents returns events with id > f.After in ascending id order along with
// next_after (the last returned id, or f.After when empty).
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]model.Event, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultEventLimit
	}
	if limit > maxEventLimit {
		limit = maxEventLimit
	}
	where := []string{"e.id > ?"}
	args := []any{f.After}
	if f.Project != "" {
		where = append(where, "p.key = ?")
		args = append(args, f.Project)
	}
	var kinds []string
	for _, k := range f.Kinds {
		if k != "" {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) > 0 {
		parts := make([]string, len(kinds))
		for i, k := range kinds {
			parts[i] = "substr(e.kind, 1, length(?)) = ?"
			args = append(args, k, k)
		}
		where = append(where, "("+strings.Join(parts, " or ")+")")
	}
	query := `select e.id, e.at, p.key, e.kind, e.subject, e.actor, e.session_id, e.data
		from event e left join project p on p.id = e.project_id
		where ` + strings.Join(where, " and ") + ` order by e.id asc limit ?`
	rows, err := s.db.QueryContext(ctx, query, append(args, limit)...)
	if err != nil {
		return nil, f.After, err
	}
	defer rows.Close()
	items := []model.Event{}
	nextAfter := f.After
	for rows.Next() {
		var ev model.Event
		var at, data string
		var proj sql.NullString
		if err := rows.Scan(&ev.ID, &at, &proj, &ev.Kind, &ev.Subject, &ev.Actor, &ev.SessionID, &data); err != nil {
			return nil, f.After, err
		}
		if ev.At, err = model.ParseTime(at); err != nil {
			return nil, f.After, err
		}
		if proj.Valid {
			k := proj.String
			ev.Project = &k
		}
		ev.Data = json.RawMessage(data)
		items = append(items, ev)
		nextAfter = ev.ID
	}
	if err := rows.Err(); err != nil {
		return nil, f.After, err
	}
	return items, nextAfter, nil
}
