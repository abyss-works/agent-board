package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

// UpsertSpec creates a mutable specification document or appends a new
// spec_revision, records document_activity, emits an event, and enqueues an
// ingest job.
func (s *Store) UpsertSpec(ctx context.Context, tr Trace, projectKey, slug string, in UpsertSpecInput) (SpecWriteResult, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if err := model.ValidateUTF8TextString(in.Content); err != nil {
		return SpecWriteResult{}, err
	}
	var verr model.ValidationError
	if !model.ValidSpecSlug(slug) {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "slug",
			Reason:  "invalid_value",
			Message: "소문자, 숫자, '.', '_', '-'로 된 1~128자여야 합니다.",
		})
	}
	title := strings.TrimSpace(in.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > model.MaxTitleRunes {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "title",
			Reason:  "invalid_value",
			Message: fmt.Sprintf("1~%d자여야 합니다.", model.MaxTitleRunes),
		})
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "spec"
	}
	if len(verr.Fields) > 0 {
		return SpecWriteResult{}, &verr
	}

	tags := normalizeTags(in.Tags)
	tagsJSON := marshalStringArray(tags)
	hash := sha256Hex(in.Content)
	sizeBytes := len(in.Content)

	var res SpecWriteResult
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		proj, err := getProjectTx(ctx, tx, projectKey, false)
		if err != nil {
			return err
		}
		if proj.Status != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.now()
		var docID int64
		var createdAt time.Time
		var createdBy string
		var archived bool
		var created bool
		var revNo int

		err = tx.QueryRow(ctx,
			`SELECT id, created_at, created_by, archived
			 FROM spec_document
			 WHERE project_id = $1 AND slug = $2
			 FOR UPDATE`,
			proj.ID, slug,
		).Scan(&docID, &createdAt, &createdBy, &archived)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			created = true
			createdAt = now
			createdBy = tr.Actor
			revNo = 1
			if err := tx.QueryRow(ctx,
				`INSERT INTO spec_document (project_id, slug, kind, title, tags, archived, created_at, created_by, updated_at, updated_by)
				 VALUES ($1, $2, $3, $4, $5::jsonb, FALSE, $6, $7, $6, $7)
				 RETURNING id`,
				proj.ID, slug, kind, title, tagsJSON, now, tr.Actor,
			).Scan(&docID); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			var maxRev int
			if err := tx.QueryRow(ctx,
				`SELECT COALESCE(MAX(revision_no), 0) FROM spec_revision WHERE spec_document_id = $1`, docID,
			).Scan(&maxRev); err != nil {
				return err
			}
			revNo = maxRev + 1
		}

		var revID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO spec_revision (
				spec_document_id, revision_no, title, content, sha256, size_bytes,
				change_summary, created_at, created_by, session_id, client_host
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id`,
			docID, revNo, title, in.Content, hash, sizeBytes,
			in.ChangeSummary, now, tr.Actor, tr.SessionID, tr.ClientHost,
		).Scan(&revID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE spec_document
			 SET kind = $1, title = $2, tags = $3::jsonb, current_revision_id = $4, updated_at = $5, updated_by = $6
			 WHERE id = $7`,
			kind, title, tagsJSON, revID, now, tr.Actor, docID,
		); err != nil {
			return err
		}

		changes := map[string]model.Change{
			"revision_no": makeChange(nil, revNo),
			"sha256":      makeChange(nil, hash),
			"title":       makeChange(nil, title),
		}
		actID, err := insertDocumentActivityTx(ctx, tx, "spec", docID, tr, now, changes)
		if err != nil {
			return err
		}
		evKind := model.EventSpecUpdated
		if created {
			evKind = model.EventSpecCreated
		}
		pk := proj.Key
		if err := emitEventTx(ctx, tx, events, now, &proj.ID, &pk, evKind, proj.Key+"/"+slug, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
			"revision_no": revNo,
		}); err != nil {
			return err
		}
		jobID, err := enqueueIngestJobTx(ctx, tx, model.IngestSourceSpec, docID, hash)
		if err != nil {
			return err
		}
		contentCopy := in.Content
		res = SpecWriteResult{
			Spec: model.SpecDocument{
				ID:                docID,
				Project:           proj.Key,
				Slug:              slug,
				Kind:              kind,
				Title:             title,
				Tags:              tags,
				CurrentRevisionID: &revID,
				CurrentRevisionNo: revNo,
				SHA256:            hash,
				SizeBytes:         sizeBytes,
				Content:           &contentCopy,
				Archived:          archived,
				CreatedAt:         model.NewTime(createdAt),
				CreatedBy:         createdBy,
				UpdatedAt:         model.NewTime(now),
				UpdatedBy:         tr.Actor,
			},
			Created:      created,
			IngestJobID:  jobID,
			IngestStatus: model.IngestStatusPending,
		}
		return nil
	})
	return res, err
}

// GetSpec returns a spec document together with its current revision content.
func (s *Store) GetSpec(ctx context.Context, projectKey, slug string) (model.SpecDocument, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	var d model.SpecDocument
	var tagsRaw []byte
	var createdAt, updatedAt time.Time
	var content string
	err := s.pool.QueryRow(ctx,
		`SELECT d.id, p.key, d.slug, d.kind, d.title, d.tags,
		        d.current_revision_id, r.revision_no, r.sha256, r.size_bytes, r.content,
		        d.archived, d.created_at, d.created_by, d.updated_at, d.updated_by
		 FROM spec_document d
		 JOIN project p ON p.id = d.project_id
		 JOIN spec_revision r ON r.id = d.current_revision_id
		 WHERE p.key = $1 AND d.slug = $2`,
		projectKey, slug,
	).Scan(
		&d.ID, &d.Project, &d.Slug, &d.Kind, &d.Title, &tagsRaw,
		&d.CurrentRevisionID, &d.CurrentRevisionNo, &d.SHA256, &d.SizeBytes, &content,
		&d.Archived, &createdAt, &d.CreatedBy, &updatedAt, &d.UpdatedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SpecDocument{}, ErrNotFound
	}
	if err != nil {
		return model.SpecDocument{}, err
	}
	d.Tags = unmarshalStringArray(tagsRaw)
	d.Content = &content
	d.CreatedAt = model.NewTime(createdAt)
	d.UpdatedAt = model.NewTime(updatedAt)
	return d, nil
}

// ListSpecs returns spec summaries of a project matching filter.
func (s *Store) ListSpecs(ctx context.Context, projectKey string, filter SpecFilter) ([]model.SpecSummary, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if _, err := s.GetProject(ctx, projectKey); err != nil {
		return nil, err
	}
	args := []any{projectKey}
	cond := ` WHERE p.key = $1`
	if !filter.IncludeArchived {
		cond += ` AND NOT d.archived`
	}
	if len(filter.Kinds) > 0 {
		args = append(args, filter.Kinds)
		cond += fmt.Sprintf(` AND d.kind = ANY($%d)`, len(args))
	}
	q := `SELECT d.id, p.key, d.slug, d.kind, d.title, d.tags,
	             r.revision_no, r.sha256, r.size_bytes,
	             d.archived, d.created_at, d.created_by, d.updated_at, d.updated_by
	      FROM spec_document d
	      JOIN project p ON p.id = d.project_id
	      JOIN spec_revision r ON r.id = d.current_revision_id` + cond + ` ORDER BY d.updated_at DESC, d.id DESC`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		q += fmt.Sprintf(` LIMIT $%d`, len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		q += fmt.Sprintf(` OFFSET $%d`, len(args))
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.SpecSummary{}
	for rows.Next() {
		var d model.SpecSummary
		var tagsRaw []byte
		var createdAt, updatedAt time.Time
		if err := rows.Scan(
			&d.ID, &d.Project, &d.Slug, &d.Kind, &d.Title, &tagsRaw,
			&d.CurrentRevisionNo, &d.SHA256, &d.SizeBytes,
			&d.Archived, &createdAt, &d.CreatedBy, &updatedAt, &d.UpdatedBy,
		); err != nil {
			return nil, err
		}
		d.Tags = unmarshalStringArray(tagsRaw)
		d.CreatedAt = model.NewTime(createdAt)
		d.UpdatedAt = model.NewTime(updatedAt)
		items = append(items, d)
	}
	return items, rows.Err()
}

// ListSpecRevisions returns all append-only revisions of a spec newest first.
func (s *Store) ListSpecRevisions(ctx context.Context, projectKey, slug string) ([]model.SpecRevision, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	var docID int64
	err := s.pool.QueryRow(ctx,
		`SELECT d.id FROM spec_document d JOIN project p ON p.id = d.project_id WHERE p.key = $1 AND d.slug = $2`,
		projectKey, slug,
	).Scan(&docID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, spec_document_id, revision_no, title, content, sha256, size_bytes,
		        change_summary, created_at, created_by, session_id, client_host
		 FROM spec_revision
		 WHERE spec_document_id = $1
		 ORDER BY revision_no DESC`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.SpecRevision{}
	for rows.Next() {
		var r model.SpecRevision
		var createdAt time.Time
		if err := rows.Scan(
			&r.ID, &r.SpecDocumentID, &r.RevisionNo, &r.Title, &r.Content,
			&r.SHA256, &r.SizeBytes, &r.ChangeSummary,
			&createdAt, &r.CreatedBy, &r.SessionID, &r.ClientHost,
		); err != nil {
			return nil, err
		}
		r.CreatedAt = model.NewTime(createdAt)
		items = append(items, r)
	}
	return items, rows.Err()
}

// SetSpecArchived updates the archive flag of a spec document.
func (s *Store) SetSpecArchived(ctx context.Context, tr Trace, projectKey, slug string, archived bool) (model.SpecDocument, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var docID, projectID int64
		var projectStatus string
		var curArchived bool
		err := tx.QueryRow(ctx,
			`SELECT d.id, d.project_id, p.status, d.archived
			 FROM spec_document d JOIN project p ON p.id = d.project_id
			 WHERE p.key = $1 AND d.slug = $2 FOR UPDATE OF d`,
			projectKey, slug,
		).Scan(&docID, &projectID, &projectStatus, &curArchived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		if curArchived == archived {
			return nil
		}
		now := s.now()
		if _, err := tx.Exec(ctx,
			`UPDATE spec_document SET archived = $1, updated_at = $2, updated_by = $3 WHERE id = $4`,
			archived, now, tr.Actor, docID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE rag_chunk SET archived = $1 WHERE source_type = 'spec' AND source_id = $2`,
			archived, docID,
		); err != nil {
			return err
		}
		actID, err := insertDocumentActivityTx(ctx, tx, "spec", docID, tr, now, map[string]model.Change{
			"archived": makeChange(curArchived, archived),
		})
		if err != nil {
			return err
		}
		pk := projectKey
		return emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventSpecUpdated, projectKey+"/"+slug, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		})
	})
	if err != nil {
		return model.SpecDocument{}, err
	}
	return s.GetSpec(ctx, projectKey, slug)
}
