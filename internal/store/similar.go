package store

import (
	"context"
	"database/sql"
)

// SimilarPair says the document with id Similar is close in meaning to the
// document with id Document.
type SimilarPair struct {
	Document int64
	Similar  int64
	Score    float64
}

// ReplaceSimilarDocuments replaces every stored pair with pairs, found with
// model. Pairs naming a document that no longer exists are skipped.
func (s *Store) ReplaceSimilarDocuments(ctx context.Context, model string, pairs []SimilarPair) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from similar_document`); err != nil {
			return err
		}
		for _, p := range pairs {
			if _, err := tx.ExecContext(ctx, `insert into similar_document(document_id, similar_id, score, model)
				select ?, ?, ?, ? where exists (select 1 from document where id = ?) and exists (select 1 from document where id = ?)`,
				p.Document, p.Similar, p.Score, model, p.Document, p.Similar); err != nil {
				return err
			}
		}
		return nil
	})
}
