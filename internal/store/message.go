package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// MessageInput is a create request. An empty To leaves the message without
// an addressee and an empty TTL means model.DefaultMessageTTL.
type MessageInput struct {
	To        string
	ToSession *model.SessionRef
	ToRole    string
	Issue     string
	ReplyTo   *int64
	Title     string
	Body      string
	TTL       string
}

// MessageFilter selects the messages of a project. Without All, messages that
// are deleted or expired are left out, and when ToSession is nil and Viewer is
// set, messages addressed to another session are hidden. An empty To matches
// every addressee. A UnreadBy leaves out the messages that reader acknowledged.
type MessageFilter struct {
	Project   string
	To        string
	ToSession *model.SessionRef
	ToRole    string
	Issue     string
	ReplyTo   *int64
	Viewer    *model.SessionRef
	UnreadBy  string
	All       bool
	Limit     int
	Offset    int
}

const messageCols = `m.id, p.key, m.to_label, m.to_actor, m.to_session_id, m.to_role, i.key, m.reply_to_id,
	(select count(*) from message r where r.reply_to_id = m.id),
	m.title, m.body, m.created_at, m.created_by, m.created_session, m.created_host,
	m.expires_at, m.deleted_at, m.deleted_by`

const messageFrom = ` from message m join project p on p.id = m.project_id left join issue i on i.id = m.issue_id`

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
	var to, toActor, toSessionID, toRole, issue, deletedAt, deletedBy sql.NullString
	var replyTo sql.NullInt64
	var body, created, expires string
	if err := sc.Scan(&m.ID, &m.Project, &to, &toActor, &toSessionID, &toRole, &issue, &replyTo, &m.ReplyCount,
		&m.Title, &body, &created, &m.CreatedBy, &m.CreatedSession, &m.CreatedHost,
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
	if toActor.Valid && toSessionID.Valid {
		m.ToSession = &model.SessionRef{Actor: toActor.String, SessionID: toSessionID.String}
	}
	if toRole.Valid {
		m.ToRole = &toRole.String
	}
	if issue.Valid {
		m.Issue = &issue.String
	}
	if replyTo.Valid {
		m.ReplyTo = &replyTo.Int64
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

// GetMessage returns a message by id with its reads and direct replies,
// deleted or expired ones included.
func (s *Store) GetMessage(ctx context.Context, id int64) (*model.Message, error) {
	now := s.stamp()
	m, err := getMessage(ctx, s.db, id, now)
	if err != nil {
		return nil, err
	}
	if err := s.loadMessageReads(ctx, m); err != nil {
		return nil, err
	}
	if err := s.loadMessageReplies(ctx, m, now); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) loadMessageReads(ctx context.Context, m *model.Message) error {
	rows, err := s.db.QueryContext(ctx, `select `+messageReadCols+` from message_read where message_id = ? order by read_at, reader`, m.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	m.Reads = []model.MessageRead{}
	for rows.Next() {
		r, err := scanMessageRead(rows)
		if err != nil {
			return err
		}
		m.Reads = append(m.Reads, *r)
	}
	return rows.Err()
}

func (s *Store) loadMessageReplies(ctx context.Context, m *model.Message, now model.Time) error {
	rows, err := s.db.QueryContext(ctx, `select `+messageCols+messageFrom+` where m.reply_to_id = ? order by m.created_at asc, m.id asc`, m.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	replies := []model.Message{}
	for rows.Next() {
		r, err := scanMessage(rows, now, true)
		if err != nil {
			return err
		}
		replies = append(replies, *r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	m.Replies = &replies
	return nil
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
	if f.ToSession != nil {
		cond += ` and m.to_actor = ? and m.to_session_id = ?`
		args = append(args, f.ToSession.Actor, f.ToSession.SessionID)
	} else if !f.All && f.Viewer != nil {
		cond += ` and (m.to_actor is null or (m.to_actor = ? and m.to_session_id = ?))`
		args = append(args, f.Viewer.Actor, f.Viewer.SessionID)
	}
	if f.ToRole != "" {
		cond += ` and m.to_role = ?`
		args = append(args, f.ToRole)
	}
	if f.Issue != "" {
		cond += ` and i.key = ?`
		args = append(args, f.Issue)
	}
	if f.ReplyTo != nil {
		cond += ` and m.reply_to_id = ?`
		args = append(args, *f.ReplyTo)
	}
	if !f.All {
		cond += ` and m.deleted_at is null and m.expires_at > ?`
		args = append(args, now.String())
	}
	if f.UnreadBy != "" {
		cond += ` and not exists (select 1 from message_read r where r.message_id = m.id and r.reader = ?)`
		args = append(args, f.UnreadBy)
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

type messageRouting struct {
	toActor     any
	toSessionID any
	toRole      any
	issueID     any
	replyToID   any
}

func validateMessageRouting(ctx context.Context, tx *sql.Tx, projectID int64, in MessageInput, verr *model.ValidationError) (messageRouting, error) {
	var rt messageRouting
	if in.ToSession != nil {
		if !model.ValidActorID(in.ToSession.Actor) || !model.ValidSessionID(in.ToSession.SessionID) {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "to_session", Reason: "invalid",
				Message: "행위자와 세션 ID 형식이 맞지 않습니다.",
			})
		} else {
			var one int
			err := tx.QueryRowContext(ctx, `select 1 from actor where id = ?`, in.ToSession.Actor).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "to_session", Reason: "unknown_actor",
					Message: "행위자 " + in.ToSession.Actor + "가 없습니다.",
				})
			} else if err != nil {
				return rt, err
			} else {
				rt.toActor = in.ToSession.Actor
				rt.toSessionID = in.ToSession.SessionID
			}
		}
	}
	if in.ToRole != "" {
		if _, ok := model.ParseSessionRole(in.ToRole); !ok {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "to_role", Reason: "invalid",
				Message: "역할은 main, subagent, runner 중 하나여야 합니다.",
			})
		} else {
			rt.toRole = in.ToRole
		}
	}
	if in.Issue != "" {
		var issID, issProjID int64
		err := tx.QueryRowContext(ctx, `select id, project_id from issue where key = ?`, in.Issue).Scan(&issID, &issProjID)
		if errors.Is(err, sql.ErrNoRows) {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "issue", Reason: "unknown_issue",
				Message: "이슈 " + in.Issue + "가 없습니다.",
			})
		} else if err != nil {
			return rt, err
		} else if issProjID != projectID {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "issue", Reason: "project_mismatch",
				Message: "이슈 " + in.Issue + "는 이 프로젝트의 이슈가 아닙니다.",
			})
		} else {
			rt.issueID = issID
		}
	}
	if in.ReplyTo != nil {
		if *in.ReplyTo < 1 {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "reply_to", Reason: "invalid",
				Message: "답장 대상 메시지 ID는 1 이상의 정수여야 합니다.",
			})
		} else {
			var parentProjID int64
			err := tx.QueryRowContext(ctx, `select project_id from message where id = ?`, *in.ReplyTo).Scan(&parentProjID)
			if errors.Is(err, sql.ErrNoRows) {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "reply_to", Reason: "unknown_message",
					Message: fmt.Sprintf("메시지 %d가 없습니다.", *in.ReplyTo),
				})
			} else if err != nil {
				return rt, err
			} else if parentProjID != projectID {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "reply_to", Reason: "project_mismatch",
					Message: fmt.Sprintf("메시지 %d는 이 프로젝트의 메시지가 아닙니다.", *in.ReplyTo),
				})
			} else {
				rt.replyToID = *in.ReplyTo
			}
		}
	}
	return rt, nil
}

// CreateMessage posts a message to a project. The actor, session and host of
// the trace are kept with it.
func (s *Store) CreateMessage(ctx context.Context, tr model.Trace, project string, in MessageInput) (*model.Message, error) {
	verr := &model.ValidationError{}
	if err := model.ValidateMessage(in.To, in.Title, in.Body, in.TTL); err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			verr.Fields = append(verr.Fields, ve.Fields...)
		} else {
			return nil, err
		}
	}
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
		rt, err := validateMessageRouting(ctx, tx, projectID, in, verr)
		if err != nil {
			return err
		}
		if len(verr.Fields) > 0 {
			return verr
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
		res, err := tx.ExecContext(ctx, `insert into message(project_id, to_label, to_actor, to_session_id, to_role, issue_id, reply_to_id, title, body, created_at, created_by, created_session, created_host, expires_at)
			values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, to, rt.toActor, rt.toSessionID, rt.toRole, rt.issueID, rt.replyToID, in.Title, in.Body, now.String(), tr.Actor, tr.SessionID, tr.ClientHost, expires.String())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		data := map[string]any{}
		if in.To != "" {
			data["to"] = in.To
		}
		if in.ToSession != nil {
			data["to_session"] = in.ToSession.Actor + "/" + in.ToSession.SessionID
		}
		if in.ToRole != "" {
			data["to_role"] = in.ToRole
		}
		if in.Issue != "" {
			data["issue"] = in.Issue
		}
		if in.ReplyTo != nil {
			data["reply_to"] = *in.ReplyTo
		}
		return s.emitEventTx(ctx, tx, now, &projectID, &project, model.EventMessageCreated, strconv.FormatInt(id, 10), tr.Actor, tr.SessionID, data)
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
		var projectID int64
		var projectKey, status string
		if err := tx.QueryRowContext(ctx, `select m.project_id, p.key, p.status from message m join project p on p.id = m.project_id where m.id = ?`, id).Scan(&projectID, &projectKey, &status); err != nil {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		res, err := tx.ExecContext(ctx, `update message set deleted_at = ?, deleted_by = ? where id = ? and deleted_at is null`, now.String(), tr.Actor, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			return s.emitEventTx(ctx, tx, now, &projectID, &projectKey, model.EventMessageDeleted, strconv.FormatInt(id, 10), tr.Actor, tr.SessionID, nil)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetMessage(ctx, id)
}

const messageReadCols = `message_id, reader, read_at, read_by, read_session, read_host`

func scanMessageRead(sc rowScanner) (*model.MessageRead, error) {
	var r model.MessageRead
	var at string
	if err := sc.Scan(&r.MessageID, &r.Reader, &at, &r.ReadBy, &r.ReadSession, &r.ReadHost); err != nil {
		return nil, err
	}
	var err error
	r.ReadAt, err = model.ParseTime(at)
	return &r, err
}

// AckMessage records that reader acknowledged a message, whatever the state
// of the message, and returns the record. The actor, session and host of the
// trace are kept with it. Acknowledging again writes nothing and returns the
// first record. A project that is not active takes no new record, like any
// other write.
func (s *Store) AckMessage(ctx context.Context, tr model.Trace, id int64, reader string) (*model.MessageRead, error) {
	const get = `select ` + messageReadCols + ` from message_read where message_id = ? and reader = ?`
	now := s.stamp()
	err := s.write(ctx, func(tx *sql.Tx) error {
		var projectID int64
		var projectKey, status string
		err := tx.QueryRowContext(ctx, `select m.project_id, p.key, p.status from message m join project p on p.id = m.project_id where m.id = ?`, id).Scan(&projectID, &projectKey, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := model.ValidateMessageReader(reader); err != nil {
			return err
		}
		if _, err := scanMessageRead(tx.QueryRowContext(ctx, get, id, reader)); !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		if _, err := tx.ExecContext(ctx, `insert into message_read(`+messageReadCols+`) values (?, ?, ?, ?, ?, ?)`,
			id, reader, now.String(), tr.Actor, tr.SessionID, tr.ClientHost); err != nil {
			return err
		}
		return s.emitEventTx(ctx, tx, now, &projectID, &projectKey, model.EventMessageRead, strconv.FormatInt(id, 10), tr.Actor, tr.SessionID, map[string]any{
			"reader": reader,
		})
	})
	if err != nil {
		return nil, err
	}
	return scanMessageRead(s.db.QueryRowContext(ctx, get, id, reader))
}
