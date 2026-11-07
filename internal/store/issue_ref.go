package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// refTarget is the issue or record document an issue: or doc: reference of an
// issue resolved to. A reference of another kind, or one written before the
// targets were resolved, has none.
type refTarget struct {
	issue, doc sql.NullInt64
}

// storedRefTargets reads the targets of the references an issue has.
func storedRefTargets(ctx context.Context, q querier, issueID int64) (map[string]refTarget, error) {
	rows, err := q.QueryContext(ctx, `select ref, target_issue_id, target_document_id from issue_ref where issue_id = ?`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]refTarget{}
	for rows.Next() {
		var ref string
		var t refTarget
		if err := rows.Scan(&ref, &t.issue, &t.doc); err != nil {
			return nil, err
		}
		out[ref] = t
	}
	return out, rows.Err()
}

// resolveRefs finds the target of each issue: and doc: reference: an issue by
// key, whatever its status, or a record document of the project by name. A
// reference in keep is not resolved again and keeps the target it has. A
// reference that is new and resolves to nothing is a violation (reason
// unresolved), reported for all of them at once.
func resolveRefs(ctx context.Context, q querier, projectID int64, refs []string, keep map[string]refTarget) (map[string]refTarget, error) {
	out := make(map[string]refTarget, len(refs))
	var errs []model.FieldError
	for i, ref := range refs {
		if t, ok := keep[ref]; ok {
			out[ref] = t
			continue
		}
		kind, value, _ := strings.Cut(ref, ":")
		var t refTarget
		var err error
		switch kind {
		case "issue":
			err = q.QueryRowContext(ctx, `select id from issue where key = ?`, value).Scan(&t.issue.Int64)
			t.issue.Valid = err == nil
		case "doc":
			err = q.QueryRowContext(ctx, `select id from document where project_id = ? and name = ?`, projectID, value).Scan(&t.doc.Int64)
			t.doc.Valid = err == nil
		default:
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if !t.issue.Valid && !t.doc.Valid {
			what := "이슈 " + value
			if kind == "doc" {
				what = "같은 프로젝트의 기록 문서 " + value
			}
			errs = append(errs, model.FieldError{Field: fmt.Sprintf("refs[%d]", i), Reason: "unresolved", Message: what + "가 없습니다. 이슈와 문서를 먼저 만들어야 참조를 걸 수 있습니다."})
			continue
		}
		out[ref] = t
	}
	return out, ptrValidation(errs)
}
