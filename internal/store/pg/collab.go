package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	return s.withTx(ctx, func(tx pgx.Tx, _ *[]model.Event) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session (actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			 VALUES ($1, $2, $3, $3, $4, $3)
			 ON CONFLICT (actor_id, session_id) DO UPDATE
			 SET last_seen_at = GREATEST(session.last_seen_at, EXCLUDED.last_seen_at),
			     last_host = EXCLUDED.last_host`,
			tr.Actor, tr.SessionID, at, tr.ClientHost,
		)
		return err
	})
}

// ClearSessionWaiting clears a waiting state on the session if set.
func (s *Store) ClearSessionWaiting(ctx context.Context, actor, sessionID string) (model.Time, bool, error) {
	now := s.now()
	tag, err := s.pool.Exec(ctx,
		`UPDATE session SET state = NULL, state_reason = NULL, state_at = NULL, updated_at = $1
		 WHERE actor_id = $2 AND session_id = $3 AND state IS NOT NULL`,
		now, actor, sessionID)
	if err != nil {
		return model.Time{}, false, err
	}
	return model.NewTime(now), tag.RowsAffected() > 0, nil
}

// GetSession returns a session by actor and session ID.
func (s *Store) GetSession(ctx context.Context, actor, sessionID string) (model.Session, error) {
	var sess model.Session
	var firstSeen, lastSeen, updatedAt time.Time
	var parentActor, parentSession *string
	var locRaw []byte
	var stateAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT s.actor_id, s.session_id, s.first_seen_at, s.last_seen_at, s.last_host,
		        s.parent_actor_id, s.parent_session_id, s.role, i.key, s.location,
		        s.state, s.state_reason, s.state_at, s.updated_at
		 FROM session s
		 LEFT JOIN issue i ON i.id = s.issue_id
		 WHERE s.actor_id = $1 AND s.session_id = $2`,
		actor, sessionID,
	).Scan(
		&sess.Actor, &sess.SessionID, &firstSeen, &lastSeen, &sess.LastHost,
		&parentActor, &parentSession, &sess.Role, &sess.Issue, &locRaw,
		&sess.State, &sess.StateReason, &stateAt, &updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Session{}, ErrNotFound
	}
	if err != nil {
		return model.Session{}, err
	}
	sess.Named = model.SessionNamed(sess.SessionID)
	sess.FirstSeenAt = model.NewTime(firstSeen)
	sess.LastSeenAt = model.NewTime(lastSeen)
	sess.UpdatedAt = model.NewTime(updatedAt)
	sess.StateAt = toModelTimePtr(stateAt)
	sess.Stale = model.SessionStale(sess.LastSeenAt, s.stamp())
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

// ListSessions returns sessions ordered by last_seen_at descending.
func (s *Store) ListSessions(ctx context.Context, limit, offset int) ([]model.Session, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM session`).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT s.actor_id, s.session_id, s.first_seen_at, s.last_seen_at, s.last_host,
		        s.parent_actor_id, s.parent_session_id, s.role, i.key, s.location,
		        s.state, s.state_reason, s.state_at, s.updated_at
		 FROM session s
		 LEFT JOIN issue i ON i.id = s.issue_id
		 ORDER BY s.last_seen_at DESC
		 LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	nowStamp := s.stamp()
	items := []model.Session{}
	for rows.Next() {
		var sess model.Session
		var firstSeen, lastSeen, updatedAt time.Time
		var parentActor, parentSession *string
		var locRaw []byte
		var stateAt *time.Time
		if err := rows.Scan(
			&sess.Actor, &sess.SessionID, &firstSeen, &lastSeen, &sess.LastHost,
			&parentActor, &parentSession, &sess.Role, &sess.Issue, &locRaw,
			&sess.State, &sess.StateReason, &stateAt, &updatedAt,
		); err != nil {
			return nil, 0, err
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
		items = append(items, sess)
	}
	return items, total, rows.Err()
}

func claimSessionValid(lastSeen time.Time, state *string, stateAt *time.Time, now time.Time) bool {
	if now.Sub(lastSeen) <= model.SessionStaleAfter {
		return true
	}
	return state != nil && stateAt != nil && now.Sub(*stateAt) <= model.SessionWaitingHold
}

// ClaimIssue atomically acquires an active claim on an issue for the calling session.
func (s *Store) ClaimIssue(ctx context.Context, tr Trace, issueKey string, reason string) (model.IssueClaim, bool, error) {
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
		var lastSeen, stateAt *time.Time
		var state *string
		err = tx.QueryRow(ctx,
			`SELECT ic.id, ic.actor_id, ic.session_id, ic.claimed_at,
			        s.last_seen_at, s.state, s.state_at
			 FROM issue_claim ic
			 LEFT JOIN session s ON s.actor_id = ic.actor_id AND s.session_id = ic.session_id
			 WHERE ic.issue_id = $1 AND ic.released_at IS NULL
			 FOR UPDATE OF ic`, issueID,
		).Scan(&openID, &openActor, &openSession, &claimedAt, &lastSeen, &state, &stateAt)
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

		var reasonPtr *string
		if strings.TrimSpace(reason) != "" {
			r := strings.TrimSpace(reason)
			reasonPtr = &r
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

// CreateMessage inserts a message in an active project.
func (s *Store) CreateMessage(ctx context.Context, tr Trace, projectKey string, in CreateMessageInput) (model.Message, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if err := model.ValidateMessage(in.To, in.Title, in.Body, in.TTL); err != nil {
		return model.Message{}, err
	}
	ttl, err := model.MessageTTL(in.TTL)
	if err != nil {
		return model.Message{}, violation("ttl", "invalid_value", err.Error())
	}
	var out model.Message
	err = s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
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
			a, sid := in.ToSession.Actor, in.ToSession.SessionID
			toActor, toSessionID = &a, &sid
		}
		if in.ToRole != "" {
			r := in.ToRole
			toRole = &r
		}
		var issueID *int64
		var issueKeyPtr *string
		if in.Issue != "" {
			var iid int64
			err := tx.QueryRow(ctx, `SELECT id FROM issue WHERE key = $1`, in.Issue).Scan(&iid)
			if errors.Is(err, pgx.ErrNoRows) {
				return &RefError{Field: "issue", Key: in.Issue}
			}
			if err != nil {
				return err
			}
			issueID = &iid
			ik := in.Issue
			issueKeyPtr = &ik
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

// ListMessages lists project messages matching filter.
func (s *Store) ListMessages(ctx context.Context, projectKey string, filter MessageFilter) ([]model.Message, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if _, err := s.GetProject(ctx, projectKey); err != nil {
		return nil, err
	}
	now := s.now()
	args := []any{projectKey}
	conds := []string{"p.key = $1"}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if !filter.All {
		conds = append(conds, "m.deleted_at IS NULL", "m.expires_at > "+addArg(now))
	}
	if filter.To != "" {
		conds = append(conds, "m.to_label = "+addArg(filter.To))
	}
	if filter.ToRole != "" {
		conds = append(conds, "m.to_role = "+addArg(filter.ToRole))
	}
	if filter.UnreadBy != "" {
		conds = append(conds, "NOT EXISTS (SELECT 1 FROM message_read mr WHERE mr.message_id = m.id AND mr.reader = "+addArg(filter.UnreadBy)+")")
	}
	q := `SELECT m.id, p.key, m.to_label, m.to_actor, m.to_session_id, m.to_role,
	             i.key, m.reply_to_id, m.title, m.created_at, m.created_by,
	             m.created_session, m.created_host, m.expires_at, m.deleted_at, m.deleted_by
	      FROM message m
	      JOIN project p ON p.id = m.project_id
	      LEFT JOIN issue i ON i.id = m.issue_id
	      WHERE ` + strings.Join(conds, " AND ") + `
	      ORDER BY m.id DESC`
	if filter.Limit > 0 {
		q += ` LIMIT ` + addArg(filter.Limit)
	}
	if filter.Offset > 0 {
		q += ` OFFSET ` + addArg(filter.Offset)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Message{}
	for rows.Next() {
		var m model.Message
		var toActor, toSession *string
		var createdAt, expiresAt time.Time
		var deletedAt *time.Time
		if err := rows.Scan(
			&m.ID, &m.Project, &m.To, &toActor, &toSession, &m.ToRole,
			&m.Issue, &m.ReplyTo, &m.Title, &createdAt, &m.CreatedBy,
			&m.CreatedSession, &m.CreatedHost, &expiresAt, &deletedAt, &m.DeletedBy,
		); err != nil {
			return nil, err
		}
		if toActor != nil && toSession != nil {
			m.ToSession = &model.SessionRef{Actor: *toActor, SessionID: *toSession}
		}
		m.CreatedAt = model.NewTime(createdAt)
		m.ExpiresAt = model.NewTime(expiresAt)
		m.DeletedAt = toModelTimePtr(deletedAt)
		switch {
		case deletedAt != nil:
			m.State = model.MessageDeleted
		case !expiresAt.After(now):
			m.State = model.MessageExpired
		default:
			m.State = model.MessageActive
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// MarkMessageRead records that reader acknowledged messageID.
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
			 ON CONFLICT (message_id, reader) DO UPDATE SET reader = EXCLUDED.reader
			 RETURNING read_at, read_by, read_session, read_host`,
			messageID, reader, now, tr.Actor, tr.SessionID, tr.ClientHost,
		).Scan(&readAt, &readBy, &readSess, &readHost)
		if err != nil {
			return err
		}
		pk := projectKey
		if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventMessageRead, fmt.Sprintf("%d", messageID), tr.Actor, tr.SessionID, map[string]any{
			"reader": reader,
		}); err != nil {
			return err
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
