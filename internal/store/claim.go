package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// ErrIssueClaimed reports that an issue already has a valid open claim held by
// another session.
type ErrIssueClaimed struct {
	Actor     string
	SessionID string
}

func (e *ErrIssueClaimed) Error() string {
	return fmt.Sprintf("issue claimed by %s/%s", e.Actor, e.SessionID)
}

// ErrClaimForbidden is returned when an agent session tries to release a claim
// held by another session.
var ErrClaimForbidden = errors.New("claim forbidden")

// claimSessionValid reports whether a session with the given lastSeen, state,
// and stateAt keeps an open claim valid at now.
func claimSessionValid(lastSeen model.Time, state *string, stateAt *model.Time, now time.Time) bool {
	if now.Sub(lastSeen.Time()) <= model.SessionStaleAfter {
		return true
	}
	return state != nil && stateAt != nil && now.Sub(stateAt.Time()) <= model.SessionWaitingHold
}

// openClaimRow is an open issue_claim joined with its session's liveness columns.
type openClaimRow struct {
	id         int64
	actor      string
	sessionID  string
	claimedAt  model.Time
	hasSession bool
	lastSeenAt model.Time
	state      *string
	stateAt    *model.Time
}

func (c *openClaimRow) validAt(now time.Time) bool {
	if !c.hasSession {
		return false
	}
	return claimSessionValid(c.lastSeenAt, c.state, c.stateAt, now)
}

func getOpenClaim(ctx context.Context, q querier, issueID int64) (*openClaimRow, error) {
	var c openClaimRow
	var claimedAt string
	var lastSeen, state, stateAt sql.NullString
	err := q.QueryRowContext(ctx, `select ic.id, ic.actor_id, ic.session_id, ic.claimed_at,
		s.last_seen_at, s.state, s.state_at
		from issue_claim ic
		left join session s on s.actor_id = ic.actor_id and s.session_id = ic.session_id
		where ic.issue_id = ? and ic.released_at is null`, issueID).
		Scan(&c.id, &c.actor, &c.sessionID, &claimedAt, &lastSeen, &state, &stateAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.claimedAt, err = model.ParseTime(claimedAt); err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		c.hasSession = true
		if c.lastSeenAt, err = model.ParseTime(lastSeen.String); err != nil {
			return nil, err
		}
	}
	if state.Valid {
		s := state.String
		c.state = &s
	}
	if stateAt.Valid {
		t, err := model.ParseTime(stateAt.String)
		if err != nil {
			return nil, err
		}
		c.stateAt = &t
	}
	return &c, nil
}

func insertClaimTx(ctx context.Context, tx *sql.Tx, issueID int64, tr model.Trace, now model.Time) (int64, error) {
	res, err := tx.ExecContext(ctx, `insert into issue_claim(issue_id, actor_id, session_id, claimed_at)
		values (?, ?, ?, ?)`, issueID, tr.Actor, tr.SessionID, now.String())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func releaseClaimTx(ctx context.Context, tx *sql.Tx, claimID int64, now model.Time, outcome string, reason *string) error {
	var r any
	if reason != nil {
		r = *reason
	}
	_, err := tx.ExecContext(ctx, `update issue_claim set released_at = ?, outcome = ?, reason = ? where id = ?`,
		now.String(), outcome, r, claimID)
	return err
}

// syncClaimOnStatusChangeTx updates the issue's claim within the same write
// transaction as a status change.
func syncClaimOnStatusChangeTx(ctx context.Context, tx *sql.Tx, issueID int64, tr model.Trace, kind model.ActorKind, nextStatus model.IssueStatus, statusReason *string, now model.Time) error {
	open, err := getOpenClaim(ctx, tx, issueID)
	if err != nil {
		return err
	}
	if open == nil {
		if nextStatus == model.StatusDoing {
			if err := touchSessionTx(ctx, tx, tr, now.Time()); err != nil {
				return err
			}
			_, err = insertClaimTx(ctx, tx, issueID, tr, now)
			return err
		}
		return nil
	}
	valid := open.validAt(now.Time())
	sameSession := open.actor == tr.Actor && open.sessionID == tr.SessionID
	if sameSession {
		switch nextStatus {
		case model.StatusDone, model.StatusReview:
			return releaseClaimTx(ctx, tx, open.id, now, model.ClaimOutcomeDone, nil)
		case model.StatusBacklog, model.StatusCancelled, model.StatusDeleted:
			return releaseClaimTx(ctx, tx, open.id, now, model.ClaimOutcomeAbandoned, nil)
		case model.StatusDoing:
			if valid {
				return nil
			}
			if err := releaseClaimTx(ctx, tx, open.id, now, model.ClaimOutcomeExpired, nil); err != nil {
				return err
			}
			if err := touchSessionTx(ctx, tx, tr, now.Time()); err != nil {
				return err
			}
			_, err = insertClaimTx(ctx, tx, issueID, tr, now)
			return err
		}
		return nil
	}
	if valid {
		if kind != model.ActorUser {
			return &ErrIssueClaimed{Actor: open.actor, SessionID: open.sessionID}
		}
		if err := releaseClaimTx(ctx, tx, open.id, now, model.ClaimOutcomeTakenOver, statusReason); err != nil {
			return err
		}
	} else {
		if err := releaseClaimTx(ctx, tx, open.id, now, model.ClaimOutcomeExpired, nil); err != nil {
			return err
		}
	}
	if nextStatus == model.StatusDoing {
		if err := touchSessionTx(ctx, tx, tr, now.Time()); err != nil {
			return err
		}
		_, err = insertClaimTx(ctx, tx, issueID, tr, now)
		return err
	}
	return nil
}

func activeIssueIDForClaim(ctx context.Context, tx *sql.Tx, key string) (int64, error) {
	var issueID int64
	var projectStatus string
	err := tx.QueryRowContext(ctx, `select i.id, p.status from issue i join project p on p.id = i.project_id where i.key = ?`, key).
		Scan(&issueID, &projectStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if model.ProjectStatus(projectStatus) != model.ProjectActive {
		return 0, ErrProjectNotActive
	}
	return issueID, nil
}

// ClaimIssue atomically claims key for the session of tr. It returns the claim
// and whether a new claim was created (false when the same session already
// holds a valid open claim).
func (s *Store) ClaimIssue(ctx context.Context, tr model.Trace, key string) (*model.IssueClaim, bool, error) {
	var out *model.IssueClaim
	var created bool
	err := s.write(ctx, func(tx *sql.Tx) error {
		issueID, err := activeIssueIDForClaim(ctx, tx, key)
		if err != nil {
			return err
		}
		now := s.stamp()
		open, err := getOpenClaim(ctx, tx, issueID)
		if err != nil {
			return err
		}
		if open != nil {
			if open.validAt(now.Time()) {
				if open.actor == tr.Actor && open.sessionID == tr.SessionID {
					out = &model.IssueClaim{
						ID:        open.id,
						Issue:     key,
						Actor:     open.actor,
						SessionID: open.sessionID,
						ClaimedAt: open.claimedAt,
						Active:    true,
					}
					created = false
					return nil
				}
				return &ErrIssueClaimed{Actor: open.actor, SessionID: open.sessionID}
			}
			if err := releaseClaimTx(ctx, tx, open.id, now, model.ClaimOutcomeExpired, nil); err != nil {
				return err
			}
		}
		if err := touchSessionTx(ctx, tx, tr, now.Time()); err != nil {
			return err
		}
		id, err := insertClaimTx(ctx, tx, issueID, tr, now)
		if err != nil {
			return err
		}
		out = &model.IssueClaim{
			ID:        id,
			Issue:     key,
			Actor:     tr.Actor,
			SessionID: tr.SessionID,
			ClaimedAt: now,
			Active:    true,
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, created, nil
}

// ReleaseClaim releases the open claim on key. Only the holding session or a
// user actor may release it. outcome defaults to "abandoned" when empty and
// must be "done" or "abandoned".
func (s *Store) ReleaseClaim(ctx context.Context, tr model.Trace, key, outcome string, reason *string) (*model.IssueClaim, error) {
	if outcome == "" {
		outcome = model.ClaimOutcomeAbandoned
	}
	verr := &model.ValidationError{}
	if outcome != model.ClaimOutcomeDone && outcome != model.ClaimOutcomeAbandoned {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "outcome",
			Reason:  "invalid",
			Message: "done 또는 abandoned여야 합니다.",
		})
	}
	var cleanedReason *string
	if reason != nil {
		cleaned, msg, ok := model.CleanClaimReason(*reason)
		if !ok {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field:   "reason",
				Reason:  "invalid",
				Message: msg,
			})
		} else {
			cleanedReason = &cleaned
		}
	}
	if len(verr.Fields) > 0 {
		return nil, verr
	}
	kind, err := actorKind(ctx, s.db, tr.Actor)
	if err != nil {
		return nil, err
	}
	var out *model.IssueClaim
	err = s.write(ctx, func(tx *sql.Tx) error {
		issueID, err := activeIssueIDForClaim(ctx, tx, key)
		if err != nil {
			return err
		}
		open, err := getOpenClaim(ctx, tx, issueID)
		if err != nil {
			return err
		}
		if open == nil {
			return ErrNotFound
		}
		if (open.actor != tr.Actor || open.sessionID != tr.SessionID) && kind != model.ActorUser {
			return ErrClaimForbidden
		}
		now := s.stamp()
		if err := releaseClaimTx(ctx, tx, open.id, now, outcome, cleanedReason); err != nil {
			return err
		}
		oc := outcome
		out = &model.IssueClaim{
			ID:         open.id,
			Issue:      key,
			Actor:      open.actor,
			SessionID:  open.sessionID,
			ClaimedAt:  open.claimedAt,
			ReleasedAt: &now,
			Outcome:    &oc,
			Reason:     cleanedReason,
			Active:     false,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListIssueClaims returns the claim history of key, newest (highest id) first.
func (s *Store) ListIssueClaims(ctx context.Context, key string, limit, offset int) ([]model.IssueClaim, int, error) {
	var issueID int64
	err := s.db.QueryRowContext(ctx, `select id from issue where key = ?`, key).Scan(&issueID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from issue_claim where issue_id = ?`, issueID).Scan(&total); err != nil {
		return nil, 0, err
	}
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `select ic.id, ic.actor_id, ic.session_id, ic.claimed_at,
		ic.released_at, ic.outcome, ic.reason, s.last_seen_at, s.state, s.state_at
		from issue_claim ic
		left join session s on s.actor_id = ic.actor_id and s.session_id = ic.session_id
		where ic.issue_id = ?
		order by ic.id desc
		limit ? offset ?`, issueID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.IssueClaim{}
	for rows.Next() {
		var item model.IssueClaim
		item.Issue = key
		var claimedAt string
		var releasedAt, outcome, reason, lastSeen, state, stateAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Actor, &item.SessionID, &claimedAt,
			&releasedAt, &outcome, &reason, &lastSeen, &state, &stateAt); err != nil {
			return nil, 0, err
		}
		if item.ClaimedAt, err = model.ParseTime(claimedAt); err != nil {
			return nil, 0, err
		}
		if releasedAt.Valid {
			t, err := model.ParseTime(releasedAt.String)
			if err != nil {
				return nil, 0, err
			}
			item.ReleasedAt = &t
		}
		if outcome.Valid {
			o := outcome.String
			item.Outcome = &o
		}
		if reason.Valid {
			r := reason.String
			item.Reason = &r
		}
		if item.ReleasedAt == nil && lastSeen.Valid {
			ls, err := model.ParseTime(lastSeen.String)
			if err != nil {
				return nil, 0, err
			}
			var st *string
			if state.Valid {
				v := state.String
				st = &v
			}
			var sat *model.Time
			if stateAt.Valid {
				t, err := model.ParseTime(stateAt.String)
				if err != nil {
					return nil, 0, err
				}
				sat = &t
			}
			item.Active = claimSessionValid(ls, st, sat, now)
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}
