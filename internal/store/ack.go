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

// SetAcknowledged marks an issue as acknowledged by the actor of tr, or clears
// the mark. It returns the issue and whether anything changed. Setting the
// state an issue already has writes nothing. A change bumps the version and
// appends an updated record whose only change is "acknowledged". ifMatch, when
// non-nil, must equal the current version.
//
// The mark is only a note for the user. Nothing about status, queue order or
// blocking depends on it.
func (s *Store) SetAcknowledged(ctx context.Context, tr model.Trace, key string, acknowledged bool, ifMatch *int64) (*model.Issue, bool, error) {
	changed := false
	err := s.write(ctx, func(tx *sql.Tx) error {
		cur, err := getIssueRow(ctx, tx, key)
		if err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersionMismatch
		}
		if (cur.AcknowledgedAt != nil) == acknowledged {
			return nil
		}
		if cur.projectStatus != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.stamp()
		var at, by any
		if acknowledged {
			at, by = now.String(), tr.Actor
		}
		if _, err := tx.ExecContext(ctx, `update issue set acknowledged_at = ?, acknowledged_by = ?, version = version + 1, updated_at = ?, updated_by = ? where id = ?`,
			at, by, now.String(), tr.Actor, cur.id); err != nil {
			return err
		}
		changes := map[string]model.Change{"acknowledged": {From: rawJSON(!acknowledged), To: rawJSON(acknowledged)}}
		if _, err := insertActivity(ctx, tx, cur.id, model.ActivityUpdated, tr, now, changes, nil, nil); err != nil {
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
