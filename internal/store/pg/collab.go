package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

// TouchSession upserts the last_seen_at and last_host of a session.
func (s *Store) TouchSession(ctx context.Context, tr Trace, at time.Time) error {
	if at.IsZero() {
		at = s.now()
	}
	at = at.UTC().Truncate(time.Millisecond)
	return s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO session (actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			 VALUES ($1, $2, $3, $3, $4, $3)
			 ON CONFLICT (actor_id, session_id) DO NOTHING`,
			tr.Actor, tr.SessionID, at, tr.ClientHost,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return emitEventTx(ctx, tx, events, at, nil, nil, model.EventSessionCreated, tr.Actor+"/"+tr.SessionID, tr.Actor, tr.SessionID, nil)
		}
		_, err = tx.Exec(ctx,
			`UPDATE session SET last_seen_at = GREATEST(last_seen_at, $1), last_host = $2
			 WHERE actor_id = $3 AND session_id = $4`,
			at, tr.ClientHost, tr.Actor, tr.SessionID,
		)
		return err
	})
}

// ClearSessionWaiting clears a waiting state on the session if set.
func (s *Store) ClearSessionWaiting(ctx context.Context, actor, sessionID string) (model.Time, bool, error) {
	now := s.now()
	var cleared bool
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		tag, err := tx.Exec(ctx,
			`UPDATE session SET state = NULL, state_reason = NULL, state_at = NULL, updated_at = $1
			 WHERE actor_id = $2 AND session_id = $3 AND state IS NOT NULL`,
			now, actor, sessionID)
		if err != nil {
			return err
		}
		cleared = tag.RowsAffected() > 0
		if cleared {
			return emitEventTx(ctx, tx, events, now, nil, nil, model.EventSessionUpdated, actor+"/"+sessionID, actor, sessionID, map[string]any{
				"fields": []string{"state", "state_at", "state_reason"},
			})
		}
		return nil
	})
	if err != nil {
		return model.Time{}, false, err
	}
	return model.NewTime(now), cleared, nil
}

const sessionSelectCols = `s.actor_id, s.session_id, s.first_seen_at, s.last_seen_at, s.last_host,
	s.parent_actor_id, s.parent_session_id, s.role, i.key, s.location,
	s.state, s.state_reason, s.state_at, s.updated_at`

func scanSession(row pgx.Row, nowStamp model.Time) (model.Session, error) {
	var sess model.Session
	var firstSeen, lastSeen, updatedAt time.Time
	var parentActor, parentSession *string
	var locRaw []byte
	var stateAt *time.Time
	if err := row.Scan(
		&sess.Actor, &sess.SessionID, &firstSeen, &lastSeen, &sess.LastHost,
		&parentActor, &parentSession, &sess.Role, &sess.Issue, &locRaw,
		&sess.State, &sess.StateReason, &stateAt, &updatedAt,
	); err != nil {
		return model.Session{}, err
	}
	sess.Named = model.SessionNamed(sess.SessionID)
	sess.FirstSeenAt = model.NewTime(firstSeen)
	sess.LastSeenAt = model.NewTime(lastSeen)
	sess.UpdatedAt = model.NewTime(updatedAt)
	sess.StateAt = toModelTimePtr(stateAt)
	sess.Stale = model.SessionStale(sess.LastSeenAt, nowStamp)
	sess.LocationConflicts = []model.SessionRef{}
	if parentActor != nil && parentSession != nil {
		sess.Parent = &model.SessionRef{Actor: *parentActor, SessionID: *parentSession}
	}
	if len(locRaw) > 0 {
		var loc model.SessionLocation
		if json.Unmarshal(locRaw, &loc) == nil {
			sess.Location = &loc
		}
	}
	return sess, nil
}

// GetSession returns a session by actor and session ID along with its direct children.
func (s *Store) GetSession(ctx context.Context, actor, sessionID string) (model.Session, error) {
	nowStamp := s.stamp()
	sess, err := scanSession(s.pool.QueryRow(ctx,
		`SELECT `+sessionSelectCols+`
		 FROM session s
		 LEFT JOIN issue i ON i.id = s.issue_id
		 WHERE s.actor_id = $1 AND s.session_id = $2`,
		actor, sessionID,
	), nowStamp)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Session{}, ErrNotFound
	}
	if err != nil {
		return model.Session{}, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+sessionSelectCols+`
		 FROM session s
		 LEFT JOIN issue i ON i.id = s.issue_id
		 WHERE s.parent_actor_id = $1 AND s.parent_session_id = $2
		 ORDER BY s.first_seen_at ASC, s.actor_id ASC, s.session_id ASC`,
		actor, sessionID,
	)
	if err != nil {
		return model.Session{}, err
	}
	defer rows.Close()
	children := []model.Session{}
	for rows.Next() {
		c, err := scanSession(rows, nowStamp)
		if err != nil {
			return model.Session{}, err
		}
		children = append(children, c)
	}
	if err := rows.Err(); err != nil {
		return model.Session{}, err
	}
	sess.Children = &children
	return sess, nil
}

// ListSessions returns sessions matching filter ordered by last_seen_at descending.
func (s *Store) ListSessions(ctx context.Context, filter SessionFilter) ([]model.Session, int, error) {
	now := s.now()
	nowStamp := model.NewTime(now)
	staleCutoff := now.Add(-model.SessionStaleAfter)

	var conds []string
	var args []any
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.Actor != "" {
		conds = append(conds, "s.actor_id = "+addArg(filter.Actor))
	}
	if filter.Parent != nil {
		conds = append(conds, "s.parent_actor_id = "+addArg(filter.Parent.Actor)+" AND s.parent_session_id = "+addArg(filter.Parent.SessionID))
	}
	if filter.Active != nil {
		if *filter.Active {
			conds = append(conds, "s.last_seen_at >= "+addArg(staleCutoff))
		} else {
			conds = append(conds, "s.last_seen_at < "+addArg(staleCutoff))
		}
	}
	if filter.Named != nil {
		if *filter.Named {
			conds = append(conds, "s.session_id !~ '^cli-[0-9]{8}$'")
		} else {
			conds = append(conds, "s.session_id ~ '^cli-[0-9]{8}$'")
		}
	}
	if filter.Waiting != nil {
		if *filter.Waiting {
			conds = append(conds, "s.state IS NOT NULL")
		} else {
			conds = append(conds, "s.state IS NULL")
		}
	}
	if filter.Project != "" {
		pk := model.NormalizeProjectKey(filter.Project)
		conds = append(conds, `EXISTS (
			SELECT 1 FROM activity a JOIN issue ai ON ai.id = a.issue_id JOIN project p ON p.id = ai.project_id
			WHERE a.actor_id = s.actor_id AND a.session_id = s.session_id AND p.key = `+addArg(pk)+`
		)`)
	}

	whereClause := ""
	if len(conds) > 0 {
		whereClause = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM session s`+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + sessionSelectCols + ` FROM session s LEFT JOIN issue i ON i.id = s.issue_id` +
		whereClause + ` ORDER BY s.last_seen_at DESC, s.actor_id ASC, s.session_id ASC`
	if filter.Limit > 0 {
		q += ` LIMIT ` + addArg(filter.Limit)
	}
	if filter.Offset > 0 {
		q += ` OFFSET ` + addArg(filter.Offset)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Session{}
	for rows.Next() {
		sess, err := scanSession(rows, nowStamp)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, sess)
	}
	return items, total, rows.Err()
}

func checkPGSessionParentTx(ctx context.Context, tx pgx.Tx, self, ref model.SessionRef, verr *model.ValidationError) (bool, error) {
	cycle := func() (bool, error) {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field: "parent", Reason: "cycle", Message: "자기 자신이나 자기 자손 세션을 상위 세션으로 둘 수 없습니다.",
		})
		return false, nil
	}
	if ref == self {
		return cycle()
	}
	cur, seen := ref, map[model.SessionRef]bool{}
	for first := true; ; first = false {
		var pActor, pSess *string
		err := tx.QueryRow(ctx,
			`SELECT parent_actor_id, parent_session_id FROM session WHERE actor_id = $1 AND session_id = $2`,
			cur.Actor, cur.SessionID,
		).Scan(&pActor, &pSess)
		if errors.Is(err, pgx.ErrNoRows) {
			if first {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "parent", Reason: "unknown_session",
					Message: "세션 " + ref.Actor + "/" + ref.SessionID + "가 없습니다.",
				})
				return false, nil
			}
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if pActor == nil || pSess == nil || seen[cur] {
			return true, nil
		}
		seen[cur] = true
		cur = model.SessionRef{Actor: *pActor, SessionID: *pSess}
		if cur == self {
			return cycle()
		}
	}
}

func resolvePGSessionIssueTx(ctx context.Context, tx pgx.Tx, key string, verr *model.ValidationError) (int64, bool, error) {
	if !model.ValidIssueKey(key) {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field: "issue", Reason: "invalid_value", Message: "이슈 키 형식이어야 합니다.",
		})
		return 0, false, nil
	}
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM issue WHERE key = $1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field: "issue", Reason: "unknown_issue", Message: "이슈 " + key + "가 없습니다.",
		})
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// UpdateSession applies a partial update to an existing session.
func (s *Store) UpdateSession(ctx context.Context, tr Trace, actor, sessionID string, p SessionPatch) (model.Session, error) {
	self := model.SessionRef{Actor: actor, SessionID: sessionID}
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var curParentActor, curParentSess, curRole, curState, curStateReason *string
		var curIssueID *int64
		var curLocRaw []byte
		var curStateAt *time.Time
		err := tx.QueryRow(ctx,
			`SELECT parent_actor_id, parent_session_id, role, issue_id, location, state, state_reason, state_at
			 FROM session WHERE actor_id = $1 AND session_id = $2 FOR UPDATE`,
			actor, sessionID,
		).Scan(&curParentActor, &curParentSess, &curRole, &curIssueID, &curLocRaw, &curState, &curStateReason, &curStateAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		now := s.now()
		verr := &model.ValidationError{}
		var sets []string
		var args []any
		fieldSet := map[string]bool{}
		setCol := func(col string, v any) {
			args = append(args, v)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
		mark := func(fields ...string) {
			for _, f := range fields {
				fieldSet[f] = true
			}
		}

		if p.Parent.Set {
			if p.Parent.Value == nil {
				if curParentActor != nil {
					setCol("parent_actor_id", nil)
					setCol("parent_session_id", nil)
					mark("parent")
				}
			} else {
				ref := *p.Parent.Value
				ok, err := checkPGSessionParentTx(ctx, tx, self, ref, verr)
				if err != nil {
					return err
				}
				if ok && (curParentActor == nil || *curParentActor != ref.Actor || curParentSess == nil || *curParentSess != ref.SessionID) {
					setCol("parent_actor_id", ref.Actor)
					setCol("parent_session_id", ref.SessionID)
					mark("parent")
				}
			}
		}
		if p.Role.Set {
			if p.Role.Value == nil {
				if curRole != nil {
					setCol("role", nil)
					mark("role")
				}
			} else {
				if _, ok := model.ParseSessionRole(*p.Role.Value); !ok {
					verr.Fields = append(verr.Fields, model.FieldError{
						Field: "role", Reason: "invalid_value", Message: "main, subagent, runner 중 하나여야 합니다.",
					})
				} else if curRole == nil || *curRole != *p.Role.Value {
					setCol("role", *p.Role.Value)
					mark("role")
				}
			}
		}
		if p.Issue.Set {
			if p.Issue.Value == nil {
				if curIssueID != nil {
					setCol("issue_id", nil)
					mark("issue")
				}
			} else {
				iid, ok, err := resolvePGSessionIssueTx(ctx, tx, *p.Issue.Value, verr)
				if err != nil {
					return err
				}
				if ok && (curIssueID == nil || *curIssueID != iid) {
					setCol("issue_id", iid)
					mark("issue")
				}
			}
		}
		if p.Location.Set {
			if p.Location.Value == nil {
				if len(curLocRaw) > 0 {
					setCol("location", nil)
					mark("location")
				}
			} else {
				loc := *p.Location.Value
				if loc.RepoRemote != nil {
					remote := model.StripRemoteUserinfo(*loc.RepoRemote)
					loc.RepoRemote = &remote
				}
				loc.ReportedAt = model.NewTime(now)
				raw, err := json.Marshal(loc)
				if err != nil {
					return err
				}
				setCol("location", string(raw))
				mark("location")
			}
		}
		if p.State.Set {
			if p.State.Value == nil {
				if p.StateReason.Set && p.StateReason.Value != nil {
					verr.Fields = append(verr.Fields, model.FieldError{
						Field: "state_reason", Reason: "requires_state", Message: "대기 상태가 없을 때는 대기 사유를 설정할 수 없습니다.",
					})
				} else if curState != nil || curStateReason != nil || curStateAt != nil {
					setCol("state", nil)
					setCol("state_reason", nil)
					setCol("state_at", nil)
					mark("state", "state_reason", "state_at")
				}
			} else {
				st := *p.State.Value
				stateOK := model.ValidSessionState(st)
				if !stateOK {
					verr.Fields = append(verr.Fields, model.FieldError{
						Field: "state", Reason: "invalid_value", Message: "waiting_approval, waiting_input, waiting_external 중 하나여야 합니다.",
					})
				}
				var reason *string
				reasonOK := true
				if p.StateReason.Set && p.StateReason.Value != nil {
					cleaned, msg, ok := model.CleanSessionStateReason(*p.StateReason.Value)
					if !ok {
						reasonOK = false
						verr.Fields = append(verr.Fields, model.FieldError{Field: "state_reason", Reason: "invalid_value", Message: msg})
					} else {
						reason = &cleaned
					}
				}
				if stateOK && reasonOK {
					setCol("state", st)
					setCol("state_reason", reason)
					setCol("state_at", now)
					mark("state", "state_reason", "state_at")
				}
			}
		} else if p.StateReason.Set {
			if p.StateReason.Value == nil {
				if curStateReason != nil {
					setCol("state_reason", nil)
					mark("state_reason")
				}
			} else if curState == nil {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "state_reason", Reason: "requires_state", Message: "대기 상태가 없을 때는 대기 사유를 설정할 수 없습니다.",
				})
			} else {
				cleaned, msg, ok := model.CleanSessionStateReason(*p.StateReason.Value)
				if !ok {
					verr.Fields = append(verr.Fields, model.FieldError{Field: "state_reason", Reason: "invalid_value", Message: msg})
				} else if curStateReason == nil || *curStateReason != cleaned {
					setCol("state_reason", cleaned)
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
		setCol("updated_at", now)
		args = append(args, actor, sessionID)
		q := fmt.Sprintf("UPDATE session SET %s WHERE actor_id = $%d AND session_id = $%d",
			strings.Join(sets, ", "), len(args)-1, len(args))
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return err
		}
		fields := make([]string, 0, len(fieldSet))
		for f := range fieldSet {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		return emitEventTx(ctx, tx, events, now, nil, nil, model.EventSessionUpdated, actor+"/"+sessionID, tr.Actor, tr.SessionID, map[string]any{
			"fields": fields,
		})
	})
	if err != nil {
		return model.Session{}, err
	}
	return s.GetSession(ctx, actor, sessionID)
}

// CreateChildSession registers a child session under the calling session for the same actor.
func (s *Store) CreateChildSession(ctx context.Context, tr Trace, in ChildSessionInput) (model.Session, error) {
	role := string(model.RoleSubagent)
	if in.Role != nil {
		role = *in.Role
	}
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		verr := &model.ValidationError{}
		if !model.ValidSessionID(in.SessionID) {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "session_id", Reason: "invalid_value",
				Message: "세션 ID는 영문자나 숫자로 시작하고 영문자, 숫자, 점, 밑줄, 하이픈으로 된 128자 이하여야 합니다.",
			})
		}
		if _, ok := model.ParseSessionRole(role); !ok {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "role", Reason: "invalid_value", Message: "main, subagent, runner 중 하나여야 합니다.",
			})
		}
		var issueID *int64
		if in.Issue != nil {
			iid, ok, err := resolvePGSessionIssueTx(ctx, tx, *in.Issue, verr)
			if err != nil {
				return err
			}
			if ok {
				issueID = &iid
			}
		}
		if len(verr.Fields) > 0 {
			return verr
		}
		if in.SessionID == tr.SessionID {
			return ErrSessionExists
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM session WHERE actor_id = $1 AND session_id = $2)`,
			tr.Actor, in.SessionID,
		).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrSessionExists
		}
		now := s.now()
		parentTag, err := tx.Exec(ctx,
			`INSERT INTO session (actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			 VALUES ($1, $2, $3, $3, $4, $3)
			 ON CONFLICT (actor_id, session_id) DO NOTHING`,
			tr.Actor, tr.SessionID, now, tr.ClientHost,
		)
		if err != nil {
			return err
		}
		if parentTag.RowsAffected() > 0 {
			if err := emitEventTx(ctx, tx, events, now, nil, nil, model.EventSessionCreated, tr.Actor+"/"+tr.SessionID, tr.Actor, tr.SessionID, nil); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO session (
				actor_id, session_id, first_seen_at, last_seen_at, last_host,
				parent_actor_id, parent_session_id, role, issue_id, updated_at
			) VALUES ($1, $2, $3, $3, $4, $5, $6, $7, $8, $3)`,
			tr.Actor, in.SessionID, now, tr.ClientHost, tr.Actor, tr.SessionID, role, issueID,
		); err != nil {
			if isUniqueViolation(err) {
				return ErrSessionExists
			}
			return err
		}
		if err := emitEventTx(ctx, tx, events, now, nil, nil, model.EventSessionCreated, tr.Actor+"/"+in.SessionID, tr.Actor, tr.SessionID, nil); err != nil {
			return err
		}
		roleTag, err := tx.Exec(ctx,
			`UPDATE session SET role = 'main', updated_at = $1
			 WHERE actor_id = $2 AND session_id = $3 AND role IS NULL`,
			now, tr.Actor, tr.SessionID,
		)
		if err != nil {
			return err
		}
		if roleTag.RowsAffected() > 0 {
			if err := emitEventTx(ctx, tx, events, now, nil, nil, model.EventSessionUpdated, tr.Actor+"/"+tr.SessionID, tr.Actor, tr.SessionID, map[string]any{
				"fields": []string{"role"},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return model.Session{}, err
	}
	return s.GetSession(ctx, tr.Actor, in.SessionID)
}

func claimSessionValid(lastSeen time.Time, state *string, stateAt *time.Time, now time.Time) bool {
	if now.Sub(lastSeen) <= model.SessionStaleAfter {
		return true
	}
	return state != nil && stateAt != nil && now.Sub(*stateAt) <= model.SessionWaitingHold
}

// ClaimIssue atomically acquires an active claim on an issue for the calling session.
func (s *Store) ClaimIssue(ctx context.Context, tr Trace, issueKey string, reason string) (model.IssueClaim, bool, error) {
	var reasonPtr *string
	if reason != "" {
		cleaned, msg, ok := model.CleanClaimReason(reason)
		if !ok {
			return model.IssueClaim{}, false, violation("reason", "invalid_value", msg)
		}
		reasonPtr = &cleaned
	}
	var out model.IssueClaim
	var created bool
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var issueID, projectID int64
		var projectKey, projectStatus string
		err := tx.QueryRow(ctx,
			`SELECT i.id, i.project_id, p.key, p.status
			 FROM issue i JOIN project p ON p.id = i.project_id
			 WHERE i.key = $1 FOR UPDATE OF i`, issueKey,
		).Scan(&issueID, &projectID, &projectKey, &projectStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.now()
		var openID int64
		var openActor, openSession string
		var claimedAt time.Time
		var openReason *string
		var lastSeen, stateAt *time.Time
		var state *string
		err = tx.QueryRow(ctx,
			`SELECT ic.id, ic.actor_id, ic.session_id, ic.claimed_at, ic.reason,
			        s.last_seen_at, s.state, s.state_at
			 FROM issue_claim ic
			 LEFT JOIN session s ON s.actor_id = ic.actor_id AND s.session_id = ic.session_id
			 WHERE ic.issue_id = $1 AND ic.released_at IS NULL
			 FOR UPDATE OF ic`, issueID,
		).Scan(&openID, &openActor, &openSession, &claimedAt, &openReason, &lastSeen, &state, &stateAt)
		if err == nil {
			valid := lastSeen != nil && claimSessionValid(*lastSeen, state, stateAt, now)
			if valid {
				if openActor == tr.Actor && openSession == tr.SessionID {
					out = model.IssueClaim{
						ID:        openID,
						Issue:     issueKey,
						Actor:     openActor,
						SessionID: openSession,
						ClaimedAt: model.NewTime(claimedAt),
						Reason:    openReason,
						Active:    true,
					}
					created = false
					return nil
				}
				return &ErrIssueClaimed{Actor: openActor, SessionID: openSession}
			}
			if _, err := tx.Exec(ctx,
				`UPDATE issue_claim SET released_at = $1, outcome = 'expired' WHERE id = $2`,
				now, openID,
			); err != nil {
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO session (actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			 VALUES ($1, $2, $3, $3, $4, $3)
			 ON CONFLICT (actor_id, session_id) DO UPDATE
			 SET last_seen_at = GREATEST(session.last_seen_at, EXCLUDED.last_seen_at),
			     last_host = EXCLUDED.last_host`,
			tr.Actor, tr.SessionID, now, tr.ClientHost,
		); err != nil {
			return err
		}

		var newID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO issue_claim (issue_id, actor_id, session_id, claimed_at, reason)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id`,
			issueID, tr.Actor, tr.SessionID, now, reasonPtr,
		).Scan(&newID); err != nil {
			return err
		}
		pk := projectKey
		if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventClaimAcquired, issueKey, tr.Actor, tr.SessionID, map[string]any{
			"claim_id": newID,
		}); err != nil {
			return err
		}
		out = model.IssueClaim{
			ID:        newID,
			Issue:     issueKey,
			Actor:     tr.Actor,
			SessionID: tr.SessionID,
			ClaimedAt: model.NewTime(now),
			Reason:    reasonPtr,
			Active:    true,
		}
		created = true
		return nil
	})
	return out, created, err
}

// ReleaseIssueClaim releases an open claim on an issue.
func (s *Store) ReleaseIssueClaim(ctx context.Context, tr Trace, issueKey string, outcome string, reason *string) (model.IssueClaim, error) {
	if outcome == "" {
		outcome = model.ClaimOutcomeAbandoned
	}
	if outcome != model.ClaimOutcomeDone && outcome != model.ClaimOutcomeAbandoned {
		return model.IssueClaim{}, violation("outcome", "invalid_value", "done 또는 abandoned여야 합니다.")
	}
	var cleanedReason *string
	if reason != nil {
		cleaned, msg, ok := model.CleanClaimReason(*reason)
		if !ok {
			return model.IssueClaim{}, violation("reason", "invalid_value", msg)
		}
		cleanedReason = &cleaned
	}
	var out model.IssueClaim
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var actorKind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM actor WHERE id = $1`, tr.Actor).Scan(&actorKind); err != nil {
			return err
		}
		var issueID, projectID int64
		var projectKey, projectStatus string
		err := tx.QueryRow(ctx,
			`SELECT i.id, i.project_id, p.key, p.status
			 FROM issue i JOIN project p ON p.id = i.project_id
			 WHERE i.key = $1`, issueKey,
		).Scan(&issueID, &projectID, &projectKey, &projectStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		var claimID int64
		var holderActor, holderSession string
		var claimedAt time.Time
		err = tx.QueryRow(ctx,
			`SELECT id, actor_id, session_id, claimed_at
			 FROM issue_claim
			 WHERE issue_id = $1 AND released_at IS NULL
			 FOR UPDATE`, issueID,
		).Scan(&claimID, &holderActor, &holderSession, &claimedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if (holderActor != tr.Actor || holderSession != tr.SessionID) && model.ActorKind(actorKind) != model.ActorUser {
			return ErrClaimForbidden
		}
		now := s.now()
		if _, err := tx.Exec(ctx,
			`UPDATE issue_claim SET released_at = $1, outcome = $2, reason = $3 WHERE id = $4`,
			now, outcome, cleanedReason, claimID,
		); err != nil {
			return err
		}
		pk := projectKey
		if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventClaimReleased, issueKey, tr.Actor, tr.SessionID, map[string]any{
			"claim_id": claimID,
			"outcome":  outcome,
		}); err != nil {
			return err
		}
		rel := model.NewTime(now)
		oc := outcome
		out = model.IssueClaim{
			ID:         claimID,
			Issue:      issueKey,
			Actor:      holderActor,
			SessionID:  holderSession,
			ClaimedAt:  model.NewTime(claimedAt),
			ReleasedAt: &rel,
			Outcome:    &oc,
			Reason:     cleanedReason,
			Active:     false,
		}
		return nil
	})
	return out, err
}

func messageAge(created, now time.Time) string {
	d := now.Sub(created)
	switch {
	case d < time.Hour:
		m := int(d / time.Minute)
		if m < 0 {
			m = 0
		}
		return fmt.Sprintf("%dm", m)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

const messageCols = `m.id, p.key, m.to_label, m.to_actor, m.to_session_id, m.to_role,
	i.key, m.reply_to_id,
	(SELECT COUNT(*) FROM message r WHERE r.reply_to_id = m.id),
	m.title, m.body, m.created_at, m.created_by, m.created_session, m.created_host,
	m.expires_at, m.deleted_at, m.deleted_by`

const messageFrom = ` FROM message m JOIN project p ON p.id = m.project_id LEFT JOIN issue i ON i.id = m.issue_id`

func scanMessage(row pgx.Row, now time.Time, withBody bool) (model.Message, error) {
	var m model.Message
	var toActor, toSession *string
	var body string
	var createdAt, expiresAt time.Time
	var deletedAt *time.Time
	if err := row.Scan(
		&m.ID, &m.Project, &m.To, &toActor, &toSession, &m.ToRole,
		&m.Issue, &m.ReplyTo, &m.ReplyCount,
		&m.Title, &body, &createdAt, &m.CreatedBy, &m.CreatedSession, &m.CreatedHost,
		&expiresAt, &deletedAt, &m.DeletedBy,
	); err != nil {
		return model.Message{}, err
	}
	if toActor != nil && toSession != nil {
		m.ToSession = &model.SessionRef{Actor: *toActor, SessionID: *toSession}
	}
	if withBody {
		m.Body = &body
	}
	m.CreatedAt = model.NewTime(createdAt)
	m.ExpiresAt = model.NewTime(expiresAt)
	m.DeletedAt = toModelTimePtr(deletedAt)
	m.Age = messageAge(createdAt, now)
	switch {
	case deletedAt != nil:
		m.State = model.MessageDeleted
	case !expiresAt.After(now):
		m.State = model.MessageExpired
	default:
		m.State = model.MessageActive
	}
	return m, nil
}

// CreateMessage inserts a message in an active project with full routing validation.
func (s *Store) CreateMessage(ctx context.Context, tr Trace, projectKey string, in CreateMessageInput) (model.Message, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	verr := &model.ValidationError{}
	if err := model.ValidateMessage(in.To, in.Title, in.Body, in.TTL); err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			verr.Fields = append(verr.Fields, ve.Fields...)
		} else {
			return model.Message{}, err
		}
	}
	ttl, _ := model.MessageTTL(in.TTL)
	if in.ToRole != "" {
		if _, ok := model.ParseSessionRole(in.ToRole); !ok {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field: "to_role", Reason: "invalid_value", Message: "역할은 main, subagent, runner 중 하나여야 합니다.",
			})
		}
	}
	if in.ReplyTo != nil && *in.ReplyTo < 1 {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field: "reply_to", Reason: "invalid_value", Message: "답장 대상 메시지 ID는 1 이상의 정수여야 합니다.",
		})
	}

	var out model.Message
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		proj, err := getProjectTx(ctx, tx, projectKey, false)
		if err != nil {
			return err
		}
		if proj.Status != model.ProjectActive {
			return ErrProjectNotActive
		}
		var toLabel, toActor, toSessionID, toRole *string
		if in.To != "" {
			v := in.To
			toLabel = &v
		}
		if in.ToSession != nil {
			if !model.ValidActorID(in.ToSession.Actor) || !model.ValidSessionID(in.ToSession.SessionID) {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "to_session", Reason: "invalid_value", Message: "행위자와 세션 ID 형식이 맞지 않습니다.",
				})
			} else {
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM actor WHERE id = $1)`, in.ToSession.Actor).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					verr.Fields = append(verr.Fields, model.FieldError{
						Field: "to_session", Reason: "unknown_actor", Message: "행위자 " + in.ToSession.Actor + "가 없습니다.",
					})
				} else {
					a, sid := in.ToSession.Actor, in.ToSession.SessionID
					toActor, toSessionID = &a, &sid
				}
			}
		}
		if in.ToRole != "" {
			r := in.ToRole
			toRole = &r
		}
		var issueID *int64
		var issueKeyPtr *string
		if in.Issue != "" {
			var iid, issProjID int64
			err := tx.QueryRow(ctx, `SELECT id, project_id FROM issue WHERE key = $1`, in.Issue).Scan(&iid, &issProjID)
			if errors.Is(err, pgx.ErrNoRows) {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "issue", Reason: "unknown_issue", Message: "이슈 " + in.Issue + "가 없습니다.",
				})
			} else if err != nil {
				return err
			} else if issProjID != proj.ID {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "issue", Reason: "project_mismatch", Message: "이슈 " + in.Issue + "는 이 프로젝트의 이슈가 아닙니다.",
				})
			} else {
				issueID = &iid
				ik := in.Issue
				issueKeyPtr = &ik
			}
		}
		if in.ReplyTo != nil && *in.ReplyTo >= 1 {
			var parentProjID int64
			err := tx.QueryRow(ctx, `SELECT project_id FROM message WHERE id = $1`, *in.ReplyTo).Scan(&parentProjID)
			if errors.Is(err, pgx.ErrNoRows) {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "reply_to", Reason: "unknown_message", Message: fmt.Sprintf("메시지 %d가 없습니다.", *in.ReplyTo),
				})
			} else if err != nil {
				return err
			} else if parentProjID != proj.ID {
				verr.Fields = append(verr.Fields, model.FieldError{
					Field: "reply_to", Reason: "project_mismatch", Message: fmt.Sprintf("메시지 %d는 이 프로젝트의 메시지가 아닙니다.", *in.ReplyTo),
				})
			}
		}
		if len(verr.Fields) > 0 {
			return verr
		}

		now := s.now()
		expiresAt := now.Add(ttl)
		var id int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO message (
				project_id, to_label, to_actor, to_session_id, to_role,
				issue_id, reply_to_id, title, body,
				created_at, created_by, created_session, created_host, expires_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			RETURNING id`,
			proj.ID, toLabel, toActor, toSessionID, toRole,
			issueID, in.ReplyTo, in.Title, in.Body,
			now, tr.Actor, tr.SessionID, tr.ClientHost, expiresAt,
		).Scan(&id); err != nil {
			return err
		}
		pk := proj.Key
		if err := emitEventTx(ctx, tx, events, now, &proj.ID, &pk, model.EventMessageCreated, fmt.Sprintf("%d", id), tr.Actor, tr.SessionID, nil); err != nil {
			return err
		}
		body := in.Body
		out = model.Message{
			ID:             id,
			Project:        proj.Key,
			To:             toLabel,
			ToSession:      in.ToSession,
			ToRole:         toRole,
			Issue:          issueKeyPtr,
			ReplyTo:        in.ReplyTo,
			Title:          in.Title,
			Body:           &body,
			State:          model.MessageActive,
			Age:            messageAge(now, now),
			CreatedAt:      model.NewTime(now),
			CreatedBy:      tr.Actor,
			CreatedSession: tr.SessionID,
			CreatedHost:    tr.ClientHost,
			ExpiresAt:      model.NewTime(expiresAt),
		}
		return nil
	})
	return out, err
}

// GetMessage returns a message by ID together with its reads and direct replies.
func (s *Store) GetMessage(ctx context.Context, messageID int64) (model.Message, error) {
	now := s.now()
	m, err := scanMessage(s.pool.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.id = $1`, messageID), now, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Message{}, ErrNotFound
	}
	if err != nil {
		return model.Message{}, err
	}
	readRows, err := s.pool.Query(ctx,
		`SELECT message_id, reader, read_at, read_by, read_session, read_host
		 FROM message_read WHERE message_id = $1 ORDER BY read_at ASC, reader ASC`, messageID)
	if err != nil {
		return model.Message{}, err
	}
	m.Reads = []model.MessageRead{}
	for readRows.Next() {
		var r model.MessageRead
		var readAt time.Time
		if err := readRows.Scan(&r.MessageID, &r.Reader, &readAt, &r.ReadBy, &r.ReadSession, &r.ReadHost); err != nil {
			readRows.Close()
			return model.Message{}, err
		}
		r.ReadAt = model.NewTime(readAt)
		m.Reads = append(m.Reads, r)
	}
	readRows.Close()
	if err := readRows.Err(); err != nil {
		return model.Message{}, err
	}

	replyRows, err := s.pool.Query(ctx,
		`SELECT `+messageCols+messageFrom+` WHERE m.reply_to_id = $1 ORDER BY m.created_at ASC, m.id ASC`, messageID)
	if err != nil {
		return model.Message{}, err
	}
	defer replyRows.Close()
	replies := []model.Message{}
	for replyRows.Next() {
		rep, err := scanMessage(replyRows, now, true)
		if err != nil {
			return model.Message{}, err
		}
		replies = append(replies, rep)
	}
	if err := replyRows.Err(); err != nil {
		return model.Message{}, err
	}
	m.Replies = &replies
	return m, nil
}

// ListMessages lists project messages matching filter and returns the total count before pagination.
func (s *Store) ListMessages(ctx context.Context, projectKey string, filter MessageFilter) ([]model.Message, int, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if _, err := s.GetProject(ctx, projectKey); err != nil {
		return nil, 0, err
	}
	now := s.now()
	args := []any{projectKey}
	conds := []string{"p.key = $1"}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.To != "" {
		conds = append(conds, "LOWER(m.to_label) = LOWER("+addArg(filter.To)+")")
	}
	if filter.ToSession != nil {
		conds = append(conds, "m.to_actor = "+addArg(filter.ToSession.Actor)+" AND m.to_session_id = "+addArg(filter.ToSession.SessionID))
	} else if !filter.All && filter.Viewer != nil {
		conds = append(conds, "(m.to_actor IS NULL OR (m.to_actor = "+addArg(filter.Viewer.Actor)+" AND m.to_session_id = "+addArg(filter.Viewer.SessionID)+"))")
	}
	if filter.ToRole != "" {
		conds = append(conds, "m.to_role = "+addArg(filter.ToRole))
	}
	if filter.Issue != "" {
		conds = append(conds, "i.key = "+addArg(filter.Issue))
	}
	if filter.ReplyTo != nil {
		conds = append(conds, "m.reply_to_id = "+addArg(*filter.ReplyTo))
	}
	if !filter.All {
		conds = append(conds, "m.deleted_at IS NULL", "m.expires_at > "+addArg(now))
	}
	if filter.UnreadBy != "" {
		conds = append(conds, "NOT EXISTS (SELECT 1 FROM message_read mr WHERE mr.message_id = m.id AND LOWER(mr.reader) = LOWER("+addArg(filter.UnreadBy)+"))")
	}
	whereClause := " WHERE " + strings.Join(conds, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)`+messageFrom+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + messageCols + messageFrom + whereClause + ` ORDER BY m.created_at DESC, m.id DESC`
	if filter.Limit > 0 {
		q += ` LIMIT ` + addArg(filter.Limit)
	}
	if filter.Offset > 0 {
		q += ` OFFSET ` + addArg(filter.Offset)
	}
	rows, err := s.pool.Query(ctx, q, args...)
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
		items = append(items, m)
	}
	return items, total, rows.Err()
}

// MarkMessageRead records that reader acknowledged messageID. Duplicate acks
// return the existing receipt without re-emitting a message.read event.
func (s *Store) MarkMessageRead(ctx context.Context, tr Trace, messageID int64, reader string) (model.MessageRead, error) {
	if err := model.ValidateMessageReader(reader); err != nil {
		return model.MessageRead{}, err
	}
	var out model.MessageRead
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var projectID int64
		var projectKey string
		err := tx.QueryRow(ctx,
			`SELECT m.project_id, p.key FROM message m JOIN project p ON p.id = m.project_id WHERE m.id = $1`, messageID,
		).Scan(&projectID, &projectKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.now()
		var readAt time.Time
		var readBy, readSess, readHost string
		err = tx.QueryRow(ctx,
			`INSERT INTO message_read (message_id, reader, read_at, read_by, read_session, read_host)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (message_id, reader) DO NOTHING
			 RETURNING read_at, read_by, read_session, read_host`,
			messageID, reader, now, tr.Actor, tr.SessionID, tr.ClientHost,
		).Scan(&readAt, &readBy, &readSess, &readHost)
		inserted := err == nil
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx,
				`SELECT read_at, read_by, read_session, read_host
				 FROM message_read WHERE message_id = $1 AND reader = $2`,
				messageID, reader,
			).Scan(&readAt, &readBy, &readSess, &readHost); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if inserted {
			pk := projectKey
			if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventMessageRead, fmt.Sprintf("%d", messageID), tr.Actor, tr.SessionID, map[string]any{
				"reader": reader,
			}); err != nil {
				return err
			}
		}
		out = model.MessageRead{
			MessageID:   messageID,
			Reader:      reader,
			ReadAt:      model.NewTime(readAt),
			ReadBy:      readBy,
			ReadSession: readSess,
			ReadHost:    readHost,
		}
		return nil
	})
	return out, err
}

// DeleteMessage soft-deletes a message.
func (s *Store) DeleteMessage(ctx context.Context, tr Trace, messageID int64) (model.Message, error) {
	var out model.Message
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var projectID int64
		var projectKey string
		var title, body, createdBy, createdSession, createdHost string
		var createdAt, expiresAt time.Time
		var deletedAt *time.Time
		var deletedBy *string
		err := tx.QueryRow(ctx,
			`SELECT m.project_id, p.key, m.title, m.body, m.created_at, m.created_by,
			        m.created_session, m.created_host, m.expires_at, m.deleted_at, m.deleted_by
			 FROM message m JOIN project p ON p.id = m.project_id
			 WHERE m.id = $1 FOR UPDATE OF m`, messageID,
		).Scan(
			&projectID, &projectKey, &title, &body, &createdAt, &createdBy,
			&createdSession, &createdHost, &expiresAt, &deletedAt, &deletedBy,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.now()
		if deletedAt == nil {
			if _, err := tx.Exec(ctx, `UPDATE message SET deleted_at = $1, deleted_by = $2 WHERE id = $3`, now, tr.Actor, messageID); err != nil {
				return err
			}
			deletedAt = &now
			a := tr.Actor
			deletedBy = &a
			pk := projectKey
			if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventMessageDeleted, fmt.Sprintf("%d", messageID), tr.Actor, tr.SessionID, nil); err != nil {
				return err
			}
		}
		out = model.Message{
			ID:             messageID,
			Project:        projectKey,
			Title:          title,
			Body:           &body,
			State:          model.MessageDeleted,
			Age:            messageAge(createdAt, now),
			CreatedAt:      model.NewTime(createdAt),
			CreatedBy:      createdBy,
			CreatedSession: createdSession,
			CreatedHost:    createdHost,
			ExpiresAt:      model.NewTime(expiresAt),
			DeletedAt:      toModelTimePtr(deletedAt),
			DeletedBy:      deletedBy,
		}
		return nil
	})
	return out, err
}

// LatestEventID returns the highest event ID or 0 if no events exist.
func (s *Store) LatestEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM event`).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// ListEvents returns events with id > filter.After in ascending ID order.
func (s *Store) ListEvents(ctx context.Context, filter EventFilter) ([]model.Event, int64, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	args := []any{filter.After}
	conds := []string{"e.id > $1"}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.Project != "" {
		conds = append(conds, "p.key = "+addArg(model.NormalizeProjectKey(filter.Project)))
	}
	if len(filter.Kinds) > 0 {
		conds = append(conds, "e.kind = ANY("+addArg(filter.Kinds)+")")
	}
	q := `SELECT e.id, e.at, p.key, e.kind, e.subject, e.actor, e.session_id, e.data
	      FROM event e
	      LEFT JOIN project p ON p.id = e.project_id
	      WHERE ` + strings.Join(conds, " AND ") + `
	      ORDER BY e.id ASC
	      LIMIT ` + addArg(limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, filter.After, err
	}
	defer rows.Close()
	items := []model.Event{}
	nextAfter := filter.After
	for rows.Next() {
		var ev model.Event
		var at time.Time
		var dataRaw []byte
		if err := rows.Scan(&ev.ID, &at, &ev.Project, &ev.Kind, &ev.Subject, &ev.Actor, &ev.SessionID, &dataRaw); err != nil {
			return nil, filter.After, err
		}
		ev.At = model.NewTime(at)
		if len(dataRaw) == 0 {
			ev.Data = json.RawMessage("{}")
		} else {
			ev.Data = json.RawMessage(append([]byte(nil), dataRaw...))
		}
		if ev.ID > nextAfter {
			nextAfter = ev.ID
		}
		items = append(items, ev)
	}
	return items, nextAfter, rows.Err()
}
