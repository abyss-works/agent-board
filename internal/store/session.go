package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
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
	Parent      Optional[model.SessionRef]
	Role        Optional[string]
	Issue       Optional[string]
	Location    Optional[model.SessionLocation]
	State       Optional[string]
	StateReason Optional[string]
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
// stale ones; Named true keeps the sessions whose ID is not the CLI default;
// Waiting true keeps sessions with a waiting state and false those without.
type SessionFilter struct {
	Project string
	Actor   string
	Parent  *model.SessionRef
	Active  *bool
	Named   *bool
	Waiting *bool
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

func sessionCutoffs(now model.Time) (staleCutoff, waitCutoff string) {
	t := now.Time()
	return model.NewTime(t.Add(-model.SessionStaleAfter)).String(),
		model.NewTime(t.Add(-model.SessionWaitingHold)).String()
}

// sessionEffectiveIssueExpr resolves a session's issue_id: the most recently
// claimed issue among its valid open claims, falling back to session.issue_id.
const sessionEffectiveIssueExpr = `coalesce(
	case when s.last_seen_at >= ? or (s.state is not null and s.state_at is not null and s.state_at >= ?)
		then (select ic.issue_id from issue_claim ic
			where ic.actor_id = s.actor_id and ic.session_id = s.session_id and ic.released_at is null
			order by ic.claimed_at desc, ic.id desc limit 1)
	end,
	s.issue_id
)`

const sessionSelect = `select s.actor_id, s.session_id, s.first_seen_at, s.last_seen_at, s.last_host, s.parent_actor_id, s.parent_session_id,
	s.role, i.key as issue_key, s.location, s.state, s.state_reason, s.state_at, s.updated_at, ` + sessionLastProject + ` as last_project
	from (select s.*, ` + sessionEffectiveIssueExpr + ` as effective_issue_id from session s) s
	left join issue i on i.id = s.effective_issue_id`

func scanSession(sc rowScanner, now model.Time) (*model.Session, error) {
	var x model.Session
	var first, last, updated string
	var parentActor, parentSession, role, issue, location, state, stateReason, stateAt, project sql.NullString
	if err := sc.Scan(&x.Actor, &x.SessionID, &first, &last, &x.LastHost, &parentActor, &parentSession,
		&role, &issue, &location, &state, &stateReason, &stateAt, &updated, &project); err != nil {
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
	if state.Valid {
		x.State = &state.String
	}
	if stateReason.Valid {
		x.StateReason = &stateReason.String
	}
	if stateAt.Valid {
		t, err := model.ParseTime(stateAt.String)
		if err != nil {
			return nil, err
		}
		x.StateAt = &t
	}
	x.Named = model.SessionNamed(x.SessionID)
	x.Stale = model.SessionStale(x.LastSeenAt, now)
	return &x, nil
}

// touchSessionTx records a call of the session of tr inside an existing write
// transaction. Only inserting a brand new session row emits session.created.
func (s *Store) touchSessionTx(ctx context.Context, tx *sql.Tx, tr model.Trace, at time.Time) error {
	stamp := model.NewTime(at)
	now := stamp.String()
	res, err := tx.ExecContext(ctx, `insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
		values (?, ?, ?, ?, ?, ?) on conflict(actor_id, session_id) do nothing`,
		tr.Actor, tr.SessionID, now, now, tr.ClientHost, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return s.emitEventTx(ctx, tx, stamp, nil, nil, model.EventSessionCreated, tr.Actor+"/"+tr.SessionID, tr.Actor, tr.SessionID, nil)
	}
	_, err = tx.ExecContext(ctx, `update session set last_seen_at = ?, last_host = ?
		where actor_id = ? and session_id = ? and ? >= last_seen_at`,
		now, tr.ClientHost, tr.Actor, tr.SessionID, now)
	return err
}

// TouchSession records a call of the session of the trace at the given time:
// it writes the session when it is new and otherwise moves last_seen_at and
// last_host, unless the stored call is later. updated_at does not move.
func (s *Store) TouchSession(ctx context.Context, tr model.Trace, at time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		return s.touchSessionTx(ctx, tx, tr, at)
	})
}

func (s *Store) readSession(ctx context.Context, actor, id string) (*model.Session, model.Time, error) {
	now := s.stamp()
	staleCutoff, waitCutoff := sessionCutoffs(now)
	x, err := scanSession(s.db.QueryRowContext(ctx, sessionSelect+` where s.actor_id = ? and s.session_id = ?`, staleCutoff, waitCutoff, actor, id), now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, model.Time{}, err
	}
	return x, now, nil
}

// GetSession returns a session with its children, oldest first. The
// children carry no children of their own.
func (s *Store) GetSession(ctx context.Context, actor, id string) (*model.Session, error) {
	x, now, err := s.readSession(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	staleCutoff, waitCutoff := sessionCutoffs(now)
	rows, err := s.db.QueryContext(ctx, sessionSelect+` where s.parent_actor_id = ? and s.parent_session_id = ?
		order by s.first_seen_at, s.actor_id, s.session_id`, staleCutoff, waitCutoff, actor, id)
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
	staleCutoff, waitCutoff := sessionCutoffs(now)
	var where []string
	args := []any{staleCutoff, waitCutoff}
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
	if f.Waiting != nil {
		if *f.Waiting {
			where = append(where, "x.state is not null")
		} else {
			where = append(where, "x.state is null")
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
	parentActor, parentSession, role, location, state, stateReason, stateAt sql.NullString
	issueID                                                                 sql.NullInt64
}

func getSessionRow(ctx context.Context, q querier, actor, id string) (*sessionRow, error) {
	var r sessionRow
	err := q.QueryRowContext(ctx, `select parent_actor_id, parent_session_id, role, issue_id, location, state, state_reason, state_at from session where actor_id = ? and session_id = ?`,
		actor, id).Scan(&r.parentActor, &r.parentSession, &r.role, &r.issueID, &r.location, &r.state, &r.stateReason, &r.stateAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// sessionIssueID resolves the issue key a session names, whatever the status
// of the issue. A key that names no issue is a violation.
func sessionIssueID(ctx context.Context, q querier, key string, verr *model.ValidationError) (int64, bool, error) {
	var id int64
	err := q.QueryRowContext(ctx, `select id from issue where key = ?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		verr.Fields = append(verr.Fields, model.FieldError{Field: "issue", Reason: "unknown_issue", Message: "이슈 " + key + "가 없습니다."})
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// sessionRole checks a role name and adds a violation when it is none.
func sessionRole(v string, verr *model.ValidationError) {
	if _, ok := model.ParseSessionRole(v); !ok {
		verr.Fields = append(verr.Fields, model.FieldError{Field: "role", Message: "역할은 main, subagent, runner 중 하나여야 합니다."})
	}
}

// checkSessionParent reports whether ref may become the parent of self: it
// must be a session that is neither self nor a descendant of self.
func checkSessionParent(ctx context.Context, q querier, self, ref model.SessionRef, verr *model.ValidationError) (bool, error) {
	cycle := func() (bool, error) {
		verr.Fields = append(verr.Fields, model.FieldError{Field: "parent", Reason: "cycle", Message: "자기 자신이나 자기 자손 세션을 상위 세션으로 둘 수 없습니다."})
		return false, nil
	}
	if ref == self {
		return cycle()
	}
	cur, seen := ref, map[model.SessionRef]bool{}
	for first := true; ; first = false {
		r, err := getSessionRow(ctx, q, cur.Actor, cur.SessionID)
		if errors.Is(err, ErrNotFound) {
			if first {
				verr.Fields = append(verr.Fields, model.FieldError{Field: "parent", Reason: "unknown_session",
					Message: "세션 " + ref.Actor + "/" + ref.SessionID + "가 없습니다."})
				return false, nil
			}
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if !r.parentActor.Valid || seen[cur] {
			return true, nil
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
func (s *Store) UpdateSession(ctx context.Context, tr model.Trace, actor, id string, p SessionPatch) (*model.Session, error) {
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
		fieldSet := map[string]bool{}
		set := func(col string, v any) {
			sets = append(sets, col+" = ?")
			args = append(args, v)
		}
		mark := func(fields ...string) {
			for _, f := range fields {
				fieldSet[f] = true
			}
		}
		if p.Parent.Set {
			if p.Parent.Value == nil {
				if cur.parentActor.Valid {
					set("parent_actor_id", nil)
					set("parent_session_id", nil)
					mark("parent")
				}
			} else {
				ref := *p.Parent.Value
				ok, err := checkSessionParent(ctx, tx, self, ref, verr)
				if err != nil {
					return err
				}
				if ok && (cur.parentActor.String != ref.Actor || cur.parentSession.String != ref.SessionID) {
					set("parent_actor_id", ref.Actor)
					set("parent_session_id", ref.SessionID)
					mark("parent")
				}
			}
		}
		if p.Role.Set {
			if p.Role.Value == nil {
				if cur.role.Valid {
					set("role", nil)
					mark("role")
				}
			} else if sessionRole(*p.Role.Value, verr); !cur.role.Valid || cur.role.String != *p.Role.Value {
				set("role", *p.Role.Value)
				mark("role")
			}
		}
		if p.Issue.Set {
			if p.Issue.Value == nil {
				if cur.issueID.Valid {
					set("issue_id", nil)
					mark("issue")
				}
			} else {
				issueID, ok, err := sessionIssueID(ctx, tx, *p.Issue.Value, verr)
				if err != nil {
					return err
				}
				if ok && (!cur.issueID.Valid || cur.issueID.Int64 != issueID) {
					set("issue_id", issueID)
					mark("issue")
				}
			}
		}
		if p.Location.Set {
			if p.Location.Value == nil {
				if cur.location.Valid {
					set("location", nil)
					mark("location")
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
				mark("location")
			}
		}
		if p.State.Set {
			if p.State.Value == nil {
				if p.StateReason.Set && p.StateReason.Value != nil {
					verr.Fields = append(verr.Fields, model.FieldError{
						Field: "state_reason", Reason: "requires_state",
						Message: "대기 상태가 없을 때는 대기 사유를 설정할 수 없습니다.",
					})
				} else if cur.state.Valid || cur.stateReason.Valid || cur.stateAt.Valid {
					set("state", nil)
					set("state_reason", nil)
					set("state_at", nil)
					mark("state", "state_reason", "state_at")
				}
			} else {
				st := *p.State.Value
				stateOK := model.ValidSessionState(st)
				if !stateOK {
					verr.Fields = append(verr.Fields, model.FieldError{
						Field: "state", Reason: "invalid",
						Message: "waiting_approval, waiting_input, waiting_external 중 하나여야 합니다.",
					})
				}
				var reason any
				reasonOK := true
				if p.StateReason.Set && p.StateReason.Value != nil {
					cleaned, msg, ok := model.CleanSessionStateReason(*p.StateReason.Value)
					if !ok {
						reasonOK = false
						verr.Fields = append(verr.Fields, model.FieldError{Field: "state_reason", Reason: "invalid", Message: msg})
					} else {
						reason = cleaned
					}
				}
				if stateOK && reasonOK {
					set("state", st)
					set("state_reason", reason)
					set("state_at", now.String())
					mark("state", "state_reason", "state_at")
				}
			}
		} else if p.StateReason.Set {
			if p.StateReason.Value == nil {
				if cur.stateReason.Valid {
					set("state_reason", nil)
					mark("state_reason")
				}
			} else if !cur.state.Valid {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "state_reason", Reason: "requires_state",
					Message: "대기 상태가 없을 때는 대기 사유를 설정할 수 없습니다.",
				})
			} else {
				cleaned, msg, ok := model.CleanSessionStateReason(*p.StateReason.Value)
				if !ok {
					verr.Fields = append(verr.Fields, model.FieldError{Field: "state_reason", Reason: "invalid", Message: msg})
				} else if !cur.stateReason.Valid || cur.stateReason.String != cleaned {
					set("state_reason", cleaned)
					mark("state_reason")
				}
			}
		}
		if len(verr.Fields) > 0 {
			return verr
		}
		if len(sets) == 0 {
			return nil
		}
		set("updated_at", now.String())
		if _, err := tx.ExecContext(ctx, `update session set `+strings.Join(sets, ", ")+` where actor_id = ? and session_id = ?`,
			append(args, actor, id)...); err != nil {
			return err
		}
		fields := make([]string, 0, len(fieldSet))
		for f := range fieldSet {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		return s.emitEventTx(ctx, tx, now, nil, nil, model.EventSessionUpdated, actor+"/"+id, tr.Actor, tr.SessionID, map[string]any{
			"fields": fields,
		})
	})
	if err != nil {
		return nil, err
	}
	x, _, err := s.readSession(ctx, actor, id)
	return x, err
}

// ClearSessionWaiting clears a waiting session's state, state_reason, and
// state_at and bumps updated_at. Sessions not currently waiting are untouched
// without acquiring the write lock. Returns the new updated_at and whether a
// waiting state was cleared.
func (s *Store) ClearSessionWaiting(ctx context.Context, actor, id string) (model.Time, bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `select 1 from session where actor_id = ? and session_id = ? and state is not null`, actor, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Time{}, false, nil
	}
	if err != nil {
		return model.Time{}, false, err
	}
	now := s.stamp()
	var cleared bool
	err = s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `update session set state = null, state_reason = null, state_at = null, updated_at = ?
			where actor_id = ? and session_id = ? and state is not null`, now.String(), actor, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		cleared = n > 0
		if cleared {
			return s.emitEventTx(ctx, tx, now, nil, nil, model.EventSessionUpdated, actor+"/"+id, actor, id, map[string]any{
				"fields": []string{"state", "state_at", "state_reason"},
			})
		}
		return nil
	})
	if err != nil {
		return model.Time{}, false, err
	}
	return now, cleared, nil
}

// CreateChildSession registers a child of the session of the trace, for the
// same actor. The parent is written too when no request has left it yet, and
// a parent without a role becomes main.
func (s *Store) CreateChildSession(ctx context.Context, tr model.Trace, in ChildSessionInput) (*model.Session, error) {
	at := s.stamp()
	now := at.String()
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
			id, ok, err := sessionIssueID(ctx, tx, *in.Issue, verr)
			if err != nil {
				return err
			}
			if ok {
				issueID = id
			}
		}
		if len(verr.Fields) > 0 {
			return verr
		}
		if _, err := getSessionRow(ctx, tx, tr.Actor, in.SessionID); err == nil {
			return ErrSessionExists
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		parentRes, err := tx.ExecContext(ctx, `insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			values (?, ?, ?, ?, ?, ?) on conflict(actor_id, session_id) do nothing`, tr.Actor, tr.SessionID, now, now, tr.ClientHost, now)
		if err != nil {
			return err
		}
		if pn, err := parentRes.RowsAffected(); err != nil {
			return err
		} else if pn > 0 {
			if err := s.emitEventTx(ctx, tx, at, nil, nil, model.EventSessionCreated, tr.Actor+"/"+tr.SessionID, tr.Actor, tr.SessionID, nil); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host,
			parent_actor_id, parent_session_id, role, issue_id, updated_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tr.Actor, in.SessionID, now, now, tr.ClientHost, tr.Actor, tr.SessionID, role, issueID, now); err != nil {
			return err
		}
		if err := s.emitEventTx(ctx, tx, at, nil, nil, model.EventSessionCreated, tr.Actor+"/"+in.SessionID, tr.Actor, tr.SessionID, nil); err != nil {
			return err
		}
		roleRes, err := tx.ExecContext(ctx, `update session set role = 'main', updated_at = ? where actor_id = ? and session_id = ? and role is null`,
			now, tr.Actor, tr.SessionID)
		if err != nil {
			return err
		}
		if rn, err := roleRes.RowsAffected(); err != nil {
			return err
		} else if rn > 0 {
			if err := s.emitEventTx(ctx, tx, at, nil, nil, model.EventSessionUpdated, tr.Actor+"/"+tr.SessionID, tr.Actor, tr.SessionID, map[string]any{
				"fields": []string{"role"},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	x, _, err := s.readSession(ctx, tr.Actor, in.SessionID)
	return x, err
}
