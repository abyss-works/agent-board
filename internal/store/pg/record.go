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

// CreateRecord inserts an immutable record document. Re-submitting the same
// (project, name) with the same sha256 is idempotent (Created=false); a
// different sha256 returns *ImmutableError.
func (s *Store) CreateRecord(ctx context.Context, tr Trace, projectKey string, in CreateRecordInput) (RecordWriteResult, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if err := model.ValidateUTF8TextString(in.Content); err != nil {
		return RecordWriteResult{}, err
	}
	name := strings.TrimSpace(in.Name)
	kind := strings.TrimSpace(in.Kind)
	if kind == "" && in.Imported {
		kind = string(model.DocumentOther)
	}
	var verr model.ValidationError
	if n := utf8.RuneCountInString(name); n < 1 || n > model.MaxRecordNameLen || strings.ContainsAny(name, "\r\n\t") {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "name",
			Reason:  "invalid_value",
			Message: fmt.Sprintf("1~%d자의 유효한 이름이어야 합니다.", model.MaxRecordNameLen),
		})
	}
	if kind == "" || len(kind) > model.MaxDocumentKindLen {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "kind",
			Reason:  "required",
			Message: "문서 종류(kind)가 필요합니다.",
		})
	}
	if len(verr.Fields) > 0 {
		return RecordWriteResult{}, &verr
	}

	tags := normalizeTags(in.Tags)
	tagsJSON := marshalStringArray(tags)
	hash := sha256Hex(in.Content)
	sizeBytes := len(in.Content)
	var recordedAt *time.Time
	if in.RecordedAt != nil {
		t := in.RecordedAt.Time()
		recordedAt = &t
	}

	var res RecordWriteResult
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		proj, err := getProjectTx(ctx, tx, projectKey, false)
		if err != nil {
			return err
		}
		if proj.Status != model.ProjectActive {
			return ErrProjectNotActive
		}

		// Check if a record with the same (project_id, name) already exists.
		var existing model.RecordDocument
		var exTagsRaw []byte
		var exRecordedAt *time.Time
		var exCreatedAt, exUpdatedAt time.Time
		var exContent string
		err = tx.QueryRow(ctx,
			`SELECT id, name, kind, recorded_at, title, tags, archived, imported, source_path,
			        sha256, size_bytes, content, created_at, created_by, updated_at, updated_by
			 FROM record_document
			 WHERE project_id = $1 AND name = $2
			 FOR UPDATE`,
			proj.ID, name,
		).Scan(
			&existing.ID, &existing.Name, &existing.Kind, &exRecordedAt, &existing.Title,
			&exTagsRaw, &existing.Archived, &existing.Imported, &existing.SourcePath,
			&existing.SHA256, &existing.SizeBytes, &exContent,
			&exCreatedAt, &existing.CreatedBy, &exUpdatedAt, &existing.UpdatedBy,
		)
		if err == nil {
			if existing.SHA256 != hash {
				return &ImmutableError{Name: name, Field: "content", SHA256: existing.SHA256}
			}
			existing.Project = proj.Key
			existing.RecordedAt = toModelTimePtr(exRecordedAt)
			existing.Tags = unmarshalStringArray(exTagsRaw)
			existing.Content = &exContent
			existing.CreatedAt = model.NewTime(exCreatedAt)
			existing.UpdatedAt = model.NewTime(exUpdatedAt)

			var latestJobID int64
			var latestStatus string
			jErr := tx.QueryRow(ctx,
				`SELECT id, status FROM ingest_job WHERE source_type = 'record' AND source_id = $1 ORDER BY id DESC LIMIT 1`,
				existing.ID,
			).Scan(&latestJobID, &latestStatus)
			if jErr != nil && !errors.Is(jErr, pgx.ErrNoRows) {
				return jErr
			}
			st := model.IngestStatusPending
			if latestStatus != "" {
				st = model.IngestStatus(latestStatus)
			}
			res = RecordWriteResult{
				Record:               existing,
				Created:              false,
				IngestJobID:          latestJobID,
				IngestStatus:         st,
				SupersedesCandidates: []SupersedesCandidate{},
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		now := s.now()
		var id int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO record_document (
				project_id, name, kind, recorded_at, title, tags, archived, imported,
				source_path, sha256, size_bytes, content, created_at, created_by, updated_at, updated_by
			) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13, $14, $13, $14)
			RETURNING id`,
			proj.ID, name, kind, recordedAt, in.Title, tagsJSON, in.Archived, in.Imported,
			in.SourcePath, hash, sizeBytes, in.Content, now, tr.Actor,
		).Scan(&id); err != nil {
			return err
		}
		actID, err := insertDocumentActivityTx(ctx, tx, "record", id, tr, now, map[string]model.Change{
			"name":   makeChange(nil, name),
			"sha256": makeChange(nil, hash),
		})
		if err != nil {
			return err
		}
		pk := proj.Key
		if err := emitEventTx(ctx, tx, events, now, &proj.ID, &pk, model.EventRecordCreated, proj.Key+"/"+name, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		jobID, err := enqueueIngestJobTx(ctx, tx, model.IngestSourceRecord, id, hash)
		if err != nil {
			return err
		}
		contentCopy := in.Content
		res = RecordWriteResult{
			Record: model.RecordDocument{
				ID:         id,
				Project:    proj.Key,
				Name:       name,
				Kind:       kind,
				RecordedAt: in.RecordedAt,
				Title:      in.Title,
				Tags:       tags,
				Archived:   in.Archived,
				Imported:   in.Imported,
				SourcePath: in.SourcePath,
				SHA256:     hash,
				SizeBytes:  sizeBytes,
				Content:    &contentCopy,
				CreatedAt:  model.NewTime(now),
				CreatedBy:  tr.Actor,
				UpdatedAt:  model.NewTime(now),
				UpdatedBy:  tr.Actor,
			},
			Created:              true,
			IngestJobID:          jobID,
			IngestStatus:         model.IngestStatusPending,
			SupersedesCandidates: []SupersedesCandidate{},
		}
		return nil
	})
	return res, err
}

// GetRecord returns an immutable record document with its content.
func (s *Store) GetRecord(ctx context.Context, projectKey, name string) (model.RecordDocument, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	var d model.RecordDocument
	var tagsRaw []byte
	var recordedAt *time.Time
	var createdAt, updatedAt time.Time
	var content string
	err := s.pool.QueryRow(ctx,
		`SELECT r.id, p.key, r.name, r.kind, r.recorded_at, r.title, r.tags,
		        r.archived, r.imported, r.source_path, r.sha256, r.size_bytes, r.content,
		        r.created_at, r.created_by, r.updated_at, r.updated_by
		 FROM record_document r
		 JOIN project p ON p.id = r.project_id
		 WHERE p.key = $1 AND r.name = $2`,
		projectKey, name,
	).Scan(
		&d.ID, &d.Project, &d.Name, &d.Kind, &recordedAt, &d.Title, &tagsRaw,
		&d.Archived, &d.Imported, &d.SourcePath, &d.SHA256, &d.SizeBytes, &content,
		&createdAt, &d.CreatedBy, &updatedAt, &d.UpdatedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.RecordDocument{}, ErrNotFound
	}
	if err != nil {
		return model.RecordDocument{}, err
	}
	d.RecordedAt = toModelTimePtr(recordedAt)
	d.Tags = unmarshalStringArray(tagsRaw)
	d.Content = &content
	d.CreatedAt = model.NewTime(createdAt)
	d.UpdatedAt = model.NewTime(updatedAt)
	return d, nil
}

// ListRecords returns record summaries of a project matching filter.
func (s *Store) ListRecords(ctx context.Context, projectKey string, filter RecordFilter) ([]model.RecordSummary, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if _, err := s.GetProject(ctx, projectKey); err != nil {
		return nil, err
	}
	args := []any{projectKey}
	cond := ` WHERE p.key = $1`
	if !filter.IncludeArchived {
		cond += ` AND NOT r.archived`
	}
	if len(filter.Kinds) > 0 {
		args = append(args, filter.Kinds)
		cond += fmt.Sprintf(` AND r.kind = ANY($%d)`, len(args))
	}
	q := `SELECT r.id, p.key, r.name, r.kind, r.recorded_at, r.title, r.tags,
	             r.archived, r.imported, r.source_path, r.sha256, r.size_bytes,
	             r.created_at, r.created_by, r.updated_at, r.updated_by
	      FROM record_document r
	      JOIN project p ON p.id = r.project_id` + cond + ` ORDER BY r.created_at DESC, r.id DESC`
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
	items := []model.RecordSummary{}
	for rows.Next() {
		var d model.RecordSummary
		var tagsRaw []byte
		var recordedAt *time.Time
		var createdAt, updatedAt time.Time
		if err := rows.Scan(
			&d.ID, &d.Project, &d.Name, &d.Kind, &recordedAt, &d.Title, &tagsRaw,
			&d.Archived, &d.Imported, &d.SourcePath, &d.SHA256, &d.SizeBytes,
			&createdAt, &d.CreatedBy, &updatedAt, &d.UpdatedBy,
		); err != nil {
			return nil, err
		}
		d.RecordedAt = toModelTimePtr(recordedAt)
		d.Tags = unmarshalStringArray(tagsRaw)
		d.CreatedAt = model.NewTime(createdAt)
		d.UpdatedAt = model.NewTime(updatedAt)
		items = append(items, d)
	}
	return items, rows.Err()
}

// SetRecordArchived toggles the archive flag of an immutable record document.
func (s *Store) SetRecordArchived(ctx context.Context, tr Trace, projectKey, name string, archived bool) (model.RecordDocument, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var recID, projectID int64
		var projectStatus string
		var curArchived bool
		err := tx.QueryRow(ctx,
			`SELECT r.id, r.project_id, p.status, r.archived
			 FROM record_document r JOIN project p ON p.id = r.project_id
			 WHERE p.key = $1 AND r.name = $2 FOR UPDATE OF r`,
			projectKey, name,
		).Scan(&recID, &projectID, &projectStatus, &curArchived)
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
			`UPDATE record_document SET archived = $1, updated_at = $2, updated_by = $3 WHERE id = $4`,
			archived, now, tr.Actor, recID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE rag_chunk SET archived = $1 WHERE source_type = 'record' AND source_id = $2`,
			archived, recID,
		); err != nil {
			return err
		}
		actID, err := insertDocumentActivityTx(ctx, tx, "record", recID, tr, now, map[string]model.Change{
			"archived": makeChange(curArchived, archived),
		})
		if err != nil {
			return err
		}
		pk := projectKey
		return emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventRecordUpdated, projectKey+"/"+name, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		})
	})
	if err != nil {
		return model.RecordDocument{}, err
	}
	return s.GetRecord(ctx, projectKey, name)
}
