package api

import (
	"context"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

// pgDocuments answers the record document endpoints from the PostgreSQL store.
type pgDocuments struct{ s *pgstore.Store }

func (d pgDocuments) PutDocument(ctx context.Context, tr model.Trace, project, name string, in store.DocumentInput) (*model.DocumentResult, bool, error) {
	return d.s.CompatPutDocument(ctx, tr, project, name, in)
}

func (d pgDocuments) PutDocumentsBatch(ctx context.Context, tr model.Trace, project string, items []store.BatchDocumentInput) ([]model.DocumentResult, error) {
	return d.s.CompatPutDocumentsBatch(ctx, tr, project, items)
}

func (d pgDocuments) GetDocument(ctx context.Context, project, name string) (*model.Document, error) {
	return d.s.CompatGetDocument(ctx, project, name)
}

func (d pgDocuments) ListDocuments(ctx context.Context, project string, limit, offset int) ([]model.Document, int, error) {
	return d.s.CompatListDocuments(ctx, project, limit, offset)
}

func (d pgDocuments) SearchDocuments(ctx context.Context, f store.DocumentFilter) ([]model.Document, int, error) {
	return d.s.CompatSearchDocuments(ctx, f)
}

func (d pgDocuments) SetDocumentArchived(ctx context.Context, tr model.Trace, project, name string, archived bool) (*model.DocumentResult, error) {
	return d.s.CompatSetDocumentArchived(ctx, tr, project, name, archived)
}

func (d pgDocuments) ChangeDocumentRelation(ctx context.Context, tr model.Trace, project, name string, kind model.RelationKind, target string, add bool) (*model.DocumentResult, error) {
	return d.s.CompatChangeDocumentRelation(ctx, tr, project, name, kind, target, add)
}

func (d pgDocuments) ListDocumentActivity(ctx context.Context, project, name string, limit, offset int) ([]model.DocumentActivity, int, error) {
	return d.s.CompatListDocumentActivity(ctx, project, name, limit, offset)
}
