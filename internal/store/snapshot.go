package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// Snapshot writes a consistent copy of the database to dst with VACUUM INTO.
// The copy is a complete single-file database without a WAL. dst must not
// exist. Readers and writers are not blocked beyond a normal read.
func (s *Store) Snapshot(ctx context.Context, dst string) error {
	path := filepath.ToSlash(dst)
	if strings.Contains(path, "'") {
		return fmt.Errorf("snapshot path must not contain a quote")
	}
	_, err := s.db.ExecContext(ctx, "vacuum into '"+path+"'")
	return err
}

// FingerprintNow returns a string that changes whenever the database
// content changes: every issue write appends an activity row, every
// project, topic, milestone or document write bumps updated_at there (topics,
// milestones and documents have no activity record; a stored document raises
// the number of documents), a change of the archive mark or the relations of
// a document appends a document activity record, a token write
// raises the highest token id or the number of revoked tokens, posting a message raises the highest message id and
// deleting one the number of deleted messages, and migrations
// bump user_version. A write of embeddings or similar documents changes a
// digest over the keys, hashes and scores of their rows. Recording the last use of a token changes none of these.
func (s *Store) FingerprintNow(ctx context.Context) (string, error) {
	var maxActivity, maxDocActivity, maxToken, maxMessage sql.NullInt64
	var maxProject, maxTopic, maxMilestone, maxDocument sql.NullString
	var projects, actors, topics, milestones, documents, revokedTokens, deletedMessages int
	var version int
	if err := s.db.QueryRowContext(ctx, `select (select max(id) from activity), (select max(updated_at) from project),
		(select count(*) from project), (select count(*) from actor), (select max(updated_at) from topic), (select count(*) from topic),
		(select max(updated_at) from milestone), (select count(*) from milestone),
		(select max(updated_at) from document), (select count(*) from document),
		(select max(id) from document_activity), (select max(id) from token), (select count(*) from token where revoked_at is not null),
		(select max(id) from message), (select count(deleted_at) from message)`).
		Scan(&maxActivity, &maxProject, &projects, &actors, &maxTopic, &topics, &maxMilestone, &milestones, &maxDocument, &documents, &maxDocActivity, &maxToken, &revokedTokens, &maxMessage, &deletedMessages); err != nil {
		return "", err
	}
	if err := s.db.QueryRowContext(ctx, `pragma user_version`).Scan(&version); err != nil {
		return "", err
	}
	var embeddingRows, similarRows sql.NullString
	if err := s.db.QueryRowContext(ctx, `select
		(select group_concat(source || ':' || source_id || ':' || chunk || ':' || model || ':' || text_sha256, ',') from (select * from embedding order by source, source_id, chunk)),
		(select group_concat(document_id || ':' || similar_id || ':' || score || ':' || model, ',') from (select * from similar_document order by document_id, similar_id))`).
		Scan(&embeddingRows, &similarRows); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(embeddingRows.String + "\n" + similarRows.String))
	return fmt.Sprintf("v%d:a%d:p%s:n%d:c%d:t%s:m%d:ms%s:mc%d:ds%s:dc%d:da%d:k%d:kr%d:g%d:gd%d:e%x",
		version, maxActivity.Int64, maxProject.String, projects, actors, maxTopic.String, topics, maxMilestone.String, milestones,
		maxDocument.String, documents, maxDocActivity.Int64, maxToken.Int64, revokedTokens, maxMessage.Int64, deletedMessages, digest[:8]), nil
}
