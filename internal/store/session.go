package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// ErrSessionExists is returned when a child session would take the actor and
// session ID of a session that is already there.
var ErrSessionExists = errors.New("session exists")

// SessionPatch is a merge patch of a session. A set field with a nil value
// clears it.
type SessionPatch struct {
	Parent   Optional[model.SessionRef]
	Role     Optional[string]
	Issue    Optional[string]
	Location Optional[model.SessionLocation]
}

// ChildSessionInput is a child session to register under the session of the
// request. SessionID is the full ID of the child. A nil Role means subagent.
type ChildSessionInput struct {
	SessionID string
	Role      *string
	Issue     *string
}

// SessionFilter selects sessions. Project matches the last project of a
// session; Active true keeps the sessions that are not stale and false the
// stale ones; Named true keeps the sessions whose ID is not the CLI default.
type SessionFilter struct {
	Project string
	Actor   string
	Parent  *model.SessionRef
	Active  *bool
	Named   *bool
	Limit   int
	Offset  int
}

// sessionLastProject is the project of the newest record the session left:
// an issue activity, a resource activity or a document activity.
const sessionLastProject = `(select lp.key from (
		select p.key as key, a.at as at from activity a join issue ai on ai.id = a.issue_id join project p on p.id = ai.project_id
			where a.actor_id = s.actor_id and a.session_id = s.session_id
		union all
		select p.key, r.at from resource_activity r join project p on p.id = r.project_id
			where r.actor_id = s.actor_id and r.session_id = s.session_id
		union all
		select p.key, d.at from document_activity d join document dd on dd.id = d.document_id join project p on p.id = dd.project_id
			where d.actor_id = s.actor_id and d.session_id = s.session_id
	) lp order by lp.at desc limit 1)`

const sessionSelect = `select s.actor_id, s.session_id, s.first_seen_at, s.last_seen_at, s.last_host, s.parent_actor_id, s.parent_session_id,
	s.role, i.key as issue_key, s.location, s.updated_at, ` + sessionLastProject + ` as last_project
	from session s left join issue i on i.id = s.issue_id`

func scanSession(sc rowScanner, now model.Time) (*model.Session, error) {
	var x model.Session
	var first, last, updated string
	var parentActor, parentSession, role, issue, location, project sql.NullString
	if err := sc.Scan(&x.Actor, &x.SessionID, &first, &last, &x.LastHost, &parentActor, &parentSession,
		&role, &issue, &location, &updated, &project); err != nil {
		return nil, err
	}
	var err error
	if x.FirstSeenAt, err = model.ParseTime(first); err != nil {
		return nil, err
	}
	if x.LastSeenAt, err = model.ParseTime(last); err != nil {
		return nil, err
	}
	if x.UpdatedAt, err = model.ParseTime(updated); err != nil {
		return nil, err
	}
	if parentActor.Valid && parentSession.Valid {
		x.Parent = &model.SessionRef{Actor: parentActor.String, SessionID: parentSession.String}
	}
	if role.Valid {
		x.Role = &role.String
	}
	if issue.Valid {
		x.Issue = &issue.String
	}
	if project.Valid {
		x.LastProject = &project.String
	}
	if location.Valid {
		var loc model.SessionLocation
		if err := json.Unmarshal([]byte(location.String), &loc); err != nil {
			return nil, err
		}
		x.Location = &loc
	}
	x.Named = model.SessionNamed(x.SessionID)
	x.Stale = model.SessionStale(x.LastSeenAt, now)
	return &x, nil
}

// TouchSession records a call of the session of the trace at the given time:
// it writes the session when it is new and otherwise moves last_seen_at and
// last_host, unless the stored call is later. updated_at does not move.
func (s *Store) TouchSession(ctx context.Context, tr model.Trace, at time.Time) error {
	now := model.NewTime(at).String()
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			values (?, ?, ?, ?, ?, ?)
			on conflict(actor_id, session_id) do update set last_seen_at = excluded.last_seen_at, last_host = excluded.last_host
			where excluded.last_seen_at >= session.last_seen_at`, tr.Actor, tr.SessionID, now, now, tr.ClientHost, now)
		return err
	})
}

// GetSession returns a session with its children, oldest first. The
// children carry no children of their own.
func (s *Store) GetSession(ctx context.Context, actor, id string) (*model.Session, error) {
	now := s.stamp()
	x, err := scanSession(s.db.QueryRowContext(ctx, sessionSelect+` where s.actor_id = ? and s.session_id = ?`, actor, id), now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, sessionSelect+` where s.parent_actor_id = ? and s.parent_session_id = ?
		order by s.first_seen_at, s.actor_id, s.session_id`, actor, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	children := []model.Session{}
	for rows.Next() {
		c, err := scanSession(rows, now)
		if err != nil {
			return nil, err
		}
		children = append(children, *c)
	}
	x.Children = &children
	return x, rows.Err()
}

// ListSessions returns a page of sessions, the most recently seen first, and
// the number of sessions that match the filter.
func (s *Store) ListSessions(ctx context.Context, f SessionFilter) ([]model.Session, int, error) {
	if f.Project != "" {
		if _, err := getProject(ctx, s.db, f.Project); err != nil {
			return nil, 0, err
		}
	}
	now := s.stamp()
	var where []string
	var args []any
	if f.Project != "" {
		where = append(where, "x.last_project = ?")
		args = append(args, f.Project)
	}
	if f.Actor != "" {
		where = append(where, "x.actor_id = ?")
		args = append(args, f.Actor)
	}
	if f.Parent != nil {
		where = append(where, "x.parent_actor_id = ? and x.parent_session_id = ?")
		args = append(args, f.Parent.Actor, f.Parent.SessionID)
	}
	if f.Active != nil {
		cutoff := model.NewTime(now.Time().Add(-model.SessionStaleAfter)).String()
		if *f.Active {
			where = append(where, "x.last_seen_at >= ?")
		} else {
			where = append(where, "x.last_seen_at < ?")
		}
		args = append(args, cutoff)
	}
	if f.Named != nil {
		const cli = "x.session_id glob 'cli-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]'"
		if *f.Named {
			where = append(where, "not "+cli)
		} else {
			where = append(where, cli)
		}
	}
	from := ` from (` + sessionSelect + `) x`
	if len(where) > 0 {
		from += ` where ` + strings.Join(where, " and ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select x.*`+from+` order by x.last_seen_at desc, x.actor_id, x.session_id limit ? offset ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Session{}
	for rows.Next() {
		x, err := scanSession(rows, now)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *x)
	}
	return items, total, rows.Err()
}

// sessionRow is the part of a stored session a patch compares against.
type sessionRow struct {
	parentActor, parentSession, role, location sql.NullString
	issueID                                    sql.NullInt64
}

func getSessionRow(ctx context.Context, q querier, actor, id string) (*sessionRow, error) {
	var r sessionRow
	err := q.QueryRowContext(ctx, `select parent_actor_id, parent_session_id, role, issue_id, location from session where actor_id = ? and session_id = ?`,
		actor, id).Scan(&r.parentActor, &r.parentSession, &r.role, &r.issueID, &r.location)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// sessionIssueID resolves the issue key a session names, whatever the status
// of the issue. A key that names no issue is a violation.
func sessionIssueID(ctx context.Context, q querier, key string, verr *model.ValidationError) int64 {
	var id int64
	err := q.QueryRowContext(ctx, `select id from issue where key = ?`, key).Scan(&id)
	if err != nil {
		verr.Fields = append(verr.Fields, model.FieldError{Field: "issue", Reason: "unknown_issue", Message: "이슈 " + key + "가 없습니다."})
	}
	return id
}

// sessionRole checks a role name and adds a violation when it is none.
func sessionRole(v string, verr *model.ValidationError) {
	if _, ok := model.ParseSessionRole(v); !ok {
		verr.Fields = append(verr.Fields, model.FieldError{Field: "role", Message: "역할은 main, subagent, runner 중 하나여야 합니다."})
	}
}

// checkSessionParent reports whether ref may become the parent of self: it
// must be a session that is neither self nor a descendant of self.
func checkSessionParent(ctx context.Context, q querier, self, ref model.SessionRef, verr *model.ValidationError) bool {
	cycle := func() bool {
		verr.Fields = append(verr.Fields, model.FieldError{Field: "parent", Reason: "cycle", Message: "자기 자신이나 자기 자손 세션을 상위 세션으로 둘 수 없습니다."})
		return false
	}
	if ref == self {
		return cycle()
	}
	cur, seen := ref, map[model.SessionRef]bool{}
	for first := true; ; first = false {
		r, err := getSessionRow(ctx, q, cur.Actor, cur.SessionID)
		if errors.Is(err, ErrNotFound) && first {
			verr.Fields = append(verr.Fields, model.FieldError{Field: "parent", Reason: "unknown_session",
				Message: "세션 " + ref.Actor + "/" + ref.SessionID + "가 없습니다."})
			return false
		}
		if err != nil || !r.parentActor.Valid || seen[cur] {
			return err == nil || errors.Is(err, ErrNotFound)
		}
		seen[cur] = true
		cur = model.SessionRef{Actor: r.parentActor.String, SessionID: r.parentSession.String}
		if cur == self {
			return cycle()
		}
	}
}

// stripUserinfo removes the user name and password from a URL, such as the
// token of an https remote. Anything that is not such a URL stays as it is.
func stripUserinfo(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return raw
}

// UpdateSession applies a merge patch to a session. Only the fields whose
// value changes are written; updated_at moves when one does. A location is
// replaced as a whole and stamped with the time it is reported.
func (s *Store) UpdateSession(ctx context.Context, actor, id string, p SessionPatch) (*model.Session, error) {
	now := s.stamp()
	self := model.SessionRef{Actor: actor, SessionID: id}
	err := s.write(ctx, func(tx *sql.Tx) error {
		cur, err := getSessionRow(ctx, tx, actor, id)
		if err != nil {
			return err
		}
		verr := &model.ValidationError{}
		var sets []string
		var args []any
		set := func(col string, v any) {
			sets = append(sets, col+" = ?")
			args = append(args, v)
		}
		if p.Parent.Set {
			if p.Parent.Value == nil {
				if cur.parentActor.Valid {
					set("parent_actor_id", nil)
					set("parent_session_id", nil)
				}
			} else if ref := *p.Parent.Value; checkSessionParent(ctx, tx, self, ref, verr) &&
				(cur.parentActor.String != ref.Actor || cur.parentSession.String != ref.SessionID) {
				set("parent_actor_id", ref.Actor)
				set("parent_session_id", ref.SessionID)
			}
		}
		if p.Role.Set {
			if p.Role.Value == nil {
				if cur.role.Valid {
					set("role", nil)
				}
			} else if sessionRole(*p.Role.Value, verr); !cur.role.Valid || cur.role.String != *p.Role.Value {
				set("role", *p.Role.Value)
			}
		}
		if p.Issue.Set {
			if p.Issue.Value == nil {
				if cur.issueID.Valid {
					set("issue_id", nil)
				}
			} else if issueID := sessionIssueID(ctx, tx, *p.Issue.Value, verr); !cur.issueID.Valid || cur.issueID.Int64 != issueID {
				set("issue_id", issueID)
			}
		}
		if p.Location.Set {
			if p.Location.Value == nil {
				if cur.location.Valid {
					set("location", nil)
				}
			} else {
				loc := *p.Location.Value
				if loc.RepoRemote != nil {
					remote := stripUserinfo(*loc.RepoRemote)
					loc.RepoRemote = &remote
				}
				loc.ReportedAt = now
				raw, err := json.Marshal(loc)
				if err != nil {
					return err
				}
				set("location", string(raw))
			}
		}
		if len(verr.Fields) > 0 {
			return verr
		}
		if len(sets) == 0 {
			return nil
		}
		set("updated_at", now.String())
		_, err = tx.ExecContext(ctx, `update session set `+strings.Join(sets, ", ")+` where actor_id = ? and session_id = ?`,
			append(args, actor, id)...)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetSession(ctx, actor, id)
}

// CreateChildSession registers a child of the session of the trace, for the
// same actor. The parent is written too when no request has left it yet, and
// a parent without a role becomes main.
func (s *Store) CreateChildSession(ctx context.Context, tr model.Trace, in ChildSessionInput) (*model.Session, error) {
	now := s.stamp().String()
	role := string(model.RoleSubagent)
	if in.Role != nil {
		role = *in.Role
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		verr := &model.ValidationError{}
		if !model.ValidSessionID(in.SessionID) {
			verr.Fields = append(verr.Fields, model.FieldError{Field: "session_id", Reason: "invalid",
				Message: "세션 ID는 영문자나 숫자로 시작하고 영문자, 숫자, 점, 밑줄, 하이픈으로 된 128자 이하여야 합니다."})
		}
		sessionRole(role, verr)
		var issueID any
		if in.Issue != nil {
			issueID = sessionIssueID(ctx, tx, *in.Issue, verr)
		}
		if len(verr.Fields) > 0 {
			return verr
		}
		if _, err := getSessionRow(ctx, tx, tr.Actor, in.SessionID); err == nil {
			return ErrSessionExists
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			values (?, ?, ?, ?, ?, ?) on conflict(actor_id, session_id) do nothing`, tr.Actor, tr.SessionID, now, now, tr.ClientHost, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host,
			parent_actor_id, parent_session_id, role, issue_id, updated_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tr.Actor, in.SessionID, now, now, tr.ClientHost, tr.Actor, tr.SessionID, role, issueID, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `update session set role = 'main', updated_at = ? where actor_id = ? and session_id = ? and role is null`,
			now, tr.Actor, tr.SessionID)
		return err
	})
	if err != nil {
		return nil, err
	}
	x, err := s.GetSession(ctx, tr.Actor, in.SessionID)
	if err != nil {
		return nil, err
	}
	x.Children = nil
	return x, nil
}
