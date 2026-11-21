package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wsk0715/agent-board/internal/model"
)

// actorKind returns the declared kind of an actor.
func actorKind(ctx context.Context, q querier, id string) (model.ActorKind, error) {
	var kind string
	if err := q.QueryRowContext(ctx, `select kind from actor where id = ?`, id).Scan(&kind); err != nil {
		return "", fmt.Errorf("actor %q: %w", id, err)
	}
	return model.ActorKind(kind), nil
}

// VerifyIssue marks a pending issue as verified by the actor of tr. Calling it
// on an already verified issue writes nothing and returns changed=false.
// Calling it on an issue with verification_state "none" returns a validation
// error. ifMatch, when non-nil, must equal the current version.
func (s *Store) VerifyIssue(ctx context.Context, tr model.Trace, key string, ifMatch *int64) (*model.Issue, bool, error) {
	return s.setVerificationState(ctx, tr, key, model.VerificationVerified, ifMatch)
}

// UnverifyIssue reverts a verified issue back to pending, clearing verified_at
// and verified_by while preserving verification_note. Calling it on an already
// pending issue writes nothing and returns changed=false. Calling it on an
// issue with verification_state "none" returns a validation error. ifMatch,
// when non-nil, must equal the current version.
func (s *Store) UnverifyIssue(ctx context.Context, tr model.Trace, key string, ifMatch *int64) (*model.Issue, bool, error) {
	return s.setVerificationState(ctx, tr, key, model.VerificationPending, ifMatch)
}

func (s *Store) setVerificationState(ctx context.Context, tr model.Trace, key string, target model.VerificationState, ifMatch *int64) (*model.Issue, bool, error) {
	changed := false
	err := s.write(ctx, func(tx *sql.Tx) error {
		cur, err := getIssueRow(ctx, tx, key)
		if err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersionMismatch
		}
		if cur.VerificationState == model.VerificationNone {
			return violation("verification_state", "검증이 필요하지 않은 이슈입니다.")
		}
		if cur.VerificationState == target {
			return nil
		}
		if cur.projectStatus != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.stamp()
		var at, by any
		if target == model.VerificationVerified {
			at, by = now.String(), tr.Actor
		}
		if _, err := tx.ExecContext(ctx, `update issue set verification_state = ?, verified_at = ?, verified_by = ?, version = version + 1, updated_at = ?, updated_by = ? where id = ?`,
			string(target), at, by, now.String(), tr.Actor, cur.id); err != nil {
			return err
		}
		changes := map[string]model.Change{
			"verification_state": {From: rawJSON(cur.VerificationState), To: rawJSON(target)},
		}
		actID, err := insertActivity(ctx, tx, cur.id, model.ActivityUpdated, tr, now, changes, nil, nil, nil)
		if err != nil {
			return err
		}
		if err := s.emitEventTx(ctx, tx, now, &cur.projectID, &cur.Project, model.EventIssueUpdated, key, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
			"fields":      changeFields(changes),
		}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	issue, err := s.GetIssue(ctx, key)
	return issue, changed, err
}
