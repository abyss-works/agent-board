package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// Sentinel errors of the topic rules. HTTP maps them to 409.
var (
	ErrDuplicateName    = errors.New("duplicate topic name")
	ErrTopicHasChildren = errors.New("topic has active children")
)

// TopicInput is a create request. A nil Parent makes a top-level topic.
type TopicInput struct {
	Name   string
	Parent *int64
}

// TopicPatch is a merge patch for a topic. Nil fields are unchanged.
type TopicPatch struct {
	Name   *string
	Parent Optional[int64]
	Status *model.TopicStatus
}

// topicRow is a topic with the internal ids needed inside transactions.
type topicRow struct {
	model.Topic
	projectID     int64
	projectStatus model.ProjectStatus
}

const topicCols = `t.id, t.project_id, p.key, p.status, t.name, t.parent_id, par.name, t.status, t.created_at, t.updated_at, t.created_by, t.updated_by`

const topicFrom = ` from topic t join project p on p.id = t.project_id left join topic par on par.id = t.parent_id`

func scanTopic(sc rowScanner) (*topicRow, error) {
	var r topicRow
	var parentID sql.NullInt64
	var parentName sql.NullString
	var created, updated string
	if err := sc.Scan(&r.ID, &r.projectID, &r.Project, &r.projectStatus, &r.Name, &parentID, &parentName, &r.Status, &created, &updated, &r.CreatedBy, &r.UpdatedBy); err != nil {
		return nil, err
	}
	r.Path = r.Name
	if parentID.Valid {
		r.Parent = &parentID.Int64
		r.Path = parentName.String + "/" + r.Name
	}
	var err error
	if r.CreatedAt, err = model.ParseTime(created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = model.ParseTime(updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func getTopicRow(ctx context.Context, q querier, id int64) (*topicRow, error) {
	r, err := scanTopic(q.QueryRowContext(ctx, `select `+topicCols+topicFrom+` where t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// GetTopic returns a topic by id regardless of its status.
func (s *Store) GetTopic(ctx context.Context, id int64) (*model.Topic, error) {
	r, err := getTopicRow(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return &r.Topic, nil
}

// ListTopics returns a project's topics with the given statuses (all when
// empty): top-level topics by name, each followed by its topics by name.
func (s *Store) ListTopics(ctx context.Context, project string, statuses []model.TopicStatus) ([]model.Topic, error) {
	if _, err := getProject(ctx, s.db, project); err != nil {
		return nil, err
	}
	cond := ` where p.key = ?`
	args := []any{project}
	if len(statuses) > 0 {
		cond += ` and t.status in (` + placeholders(len(statuses)) + `)`
		for _, st := range statuses {
			args = append(args, string(st))
		}
	}
	rows, err := s.db.QueryContext(ctx, `select `+topicCols+topicFrom+cond+
		` order by coalesce(par.name, t.name), coalesce(t.parent_id, t.id), (t.parent_id is not null), t.name, t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Topic{}
	for rows.Next() {
		r, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, r.Topic)
	}
	return items, rows.Err()
}

func violation(field, message string) error {
	return &model.ValidationError{Fields: []model.FieldError{{Field: field, Message: message}}}
}

// checkParent verifies that parent can hold a topic of the project: an active
// top-level topic of the same project that is not the topic itself (selfID, 0
// for a new topic).
func checkParent(ctx context.Context, q querier, projectID, selfID, parent int64) error {
	var projectOf int64
	var parentOf sql.NullInt64
	var status string
	err := q.QueryRowContext(ctx, `select project_id, parent_id, status from topic where id = ?`, parent).Scan(&projectOf, &parentOf, &status)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && projectOf != projectID):
		return violation("parent", "같은 프로젝트의 대주제여야 합니다.")
	case err != nil:
		return err
	case parent == selfID:
		return violation("parent", "자기 자신을 상위 주제로 둘 수 없습니다.")
	case parentOf.Valid:
		return violation("parent", "주제 아래에 주제를 둘 수 없습니다. 대주제를 고르세요.")
	case model.TopicStatus(status) != model.TopicActive:
		return violation("parent", "삭제된 주제 아래에는 둘 수 없습니다.")
	}
	return nil
}

// checkSiblingName rejects a name another active topic with the same parent
// already uses. exceptID is the topic being written, 0 for a new one.
func checkSiblingName(ctx context.Context, q querier, projectID int64, parent *int64, name string, exceptID int64) error {
	var n int
	if err := q.QueryRowContext(ctx, `select count(*) from topic where project_id = ? and coalesce(parent_id, 0) = coalesce(?, 0)
		and name = ? and status = 'active' and id != ?`, projectID, parent, name, exceptID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicateName
	}
	return nil
}

// CreateTopic adds an active topic to a project. A topic has no activity
// record; created_by and updated_by name the actor.
func (s *Store) CreateTopic(ctx context.Context, tr model.Trace, project string, in TopicInput) (*model.Topic, error) {
	var nameErr error
	if err := model.ValidateTopicName(in.Name); err != nil {
		nameErr = err
	}
	now := s.stamp().String()
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
		var parentErr error
		if in.Parent != nil {
			parentErr = checkParent(ctx, tx, projectID, 0, *in.Parent)
		}
		if err := model.MergeValidation(nameErr, parentErr); err != nil {
			return err
		}
		if err := checkSiblingName(ctx, tx, projectID, in.Parent, in.Name, 0); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `insert into topic(project_id, name, parent_id, status, created_at, updated_at, created_by, updated_by)
			values (?, ?, ?, 'active', ?, ?, ?, ?)`, projectID, in.Name, in.Parent, now, now, tr.Actor, tr.Actor)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetTopic(ctx, id)
}

func sameParent(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// UpdateTopic applies a merge patch. A patch that changes nothing leaves
// updated_at and updated_by untouched.
func (s *Store) UpdateTopic(ctx context.Context, tr model.Trace, id int64, patch TopicPatch) (*model.Topic, error) {
	var fieldErrs []error
	if patch.Name != nil {
		fieldErrs = append(fieldErrs, model.ValidateTopicName(*patch.Name))
	}
	if patch.Status != nil {
		if _, ok := model.ParseTopicStatus(string(*patch.Status)); !ok {
			fieldErrs = append(fieldErrs, violation("status", "active, deleted 중 하나여야 합니다."))
		}
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		cur, err := getTopicRow(ctx, tx, id)
		if err != nil {
			return err
		}
		next := cur.Topic
		if patch.Name != nil {
			next.Name = *patch.Name
		}
		if patch.Parent.Set {
			next.Parent = patch.Parent.Value
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		parentMoves := !sameParent(next.Parent, cur.Parent)
		if next.Name == cur.Name && next.Status == cur.Status && !parentMoves {
			return nil
		}
		if err := model.MergeValidation(fieldErrs...); err != nil {
			return err
		}
		if cur.projectStatus != model.ProjectActive {
			return ErrProjectNotActive
		}

		if parentMoves && next.Parent != nil {
			if err := checkParent(ctx, tx, cur.projectID, cur.ID, *next.Parent); err != nil {
				return err
			}
			if cur.Parent == nil {
				var n int
				if err := tx.QueryRowContext(ctx, `select count(*) from topic where parent_id = ?`, cur.ID).Scan(&n); err != nil {
					return err
				}
				if n > 0 {
					return violation("parent", "하위 주제가 있는 대주제에는 상위 주제를 붙일 수 없습니다.")
				}
			}
		}
		if cur.Status == model.TopicDeleted && next.Status == model.TopicActive && !parentMoves && next.Parent != nil {
			var parentStatus string
			if err := tx.QueryRowContext(ctx, `select status from topic where id = ?`, *next.Parent).Scan(&parentStatus); err != nil {
				return err
			}
			if model.TopicStatus(parentStatus) != model.TopicActive {
				return violation("status", "삭제된 대주제 아래의 주제는 대주제를 먼저 되살려야 합니다.")
			}
		}
		if cur.Status == model.TopicActive && next.Status == model.TopicDeleted {
			var n int
			if err := tx.QueryRowContext(ctx, `select count(*) from topic where parent_id = ? and status = 'active'`, cur.ID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return ErrTopicHasChildren
			}
		}
		if next.Status == model.TopicActive {
			if err := checkSiblingName(ctx, tx, cur.projectID, next.Parent, next.Name, cur.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `update topic set name = ?, parent_id = ?, status = ?, updated_at = ?, updated_by = ? where id = ?`,
			next.Name, next.Parent, string(next.Status), s.stamp().String(), tr.Actor, cur.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetTopic(ctx, id)
}

// checkIssueTopic verifies that an issue of the project may carry the topic:
// it must exist in the same project and be active.
func checkIssueTopic(ctx context.Context, q querier, projectID, topic int64) error {
	var projectOf int64
	var status string
	err := q.QueryRowContext(ctx, `select project_id, status from topic where id = ?`, topic).Scan(&projectOf, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && projectOf != projectID) {
		return violation("topic", "같은 프로젝트의 주제여야 합니다.")
	}
	if err != nil {
		return err
	}
	if model.TopicStatus(status) != model.TopicActive {
		return violation("topic", "삭제된 주제는 붙일 수 없습니다.")
	}
	return nil
}

// topicFilterClause builds the condition of the issue topic filter: any of
// the topics, a top-level topic matching the topics under it too, and none
// matching issues without a topic.
func topicFilterClause(topics []int64, none bool) (string, []any) {
	var parts []string
	var args []any
	if len(topics) > 0 {
		ph := placeholders(len(topics))
		parts = append(parts, "i.topic_id in ("+ph+") or tp.parent_id in ("+ph+")")
		for pass := 0; pass < 2; pass++ {
			for _, id := range topics {
				args = append(args, id)
			}
		}
	}
	if none {
		parts = append(parts, "i.topic_id is null")
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " or ") + ")", args
}
