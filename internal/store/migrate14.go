package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path"
	"regexp"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

var (
	// backfillFileRE finds the file names in a supersedes or related value.
	backfillFileRE = regexp.MustCompile(`[^\s\[\],'"` + "`" + `]+\.md`)
	// backfillKeyRE finds the issue keys in an issue or issues value.
	backfillKeyRE = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-[1-9][0-9]*\b`)
	// backfillFiller is what a list value may hold besides file names without
	// being a sentence.
	backfillFiller = strings.NewReplacer("[", "", "]", "", ",", "", "'", "", `"`, "", "`", "")
)

// backfillCounts is what the relation backfill of schema version 14 did.
type backfillCounts struct {
	Documents  int // documents read
	Resolved   int // relations to a document or an issue
	Unresolved int // relations kept as text
	Downgraded int // supersedes made related: a sentence, or a loop
	SelfDrops  int // relations to the document itself, which cannot be stored
	Skipped    int // values without a file name or issue key
	RefsTotal  int // issue references of the kind issue: or doc:
	RefsFound  int // of which resolved
}

// backfillDocumentState is the Go step of schema version 14. It reads the
// relations out of the front matter of every document and resolves the
// issue: and doc: references of the issues. It refuses nothing: what does not
// resolve stays as text or null.
func backfillDocumentState(ctx context.Context, tx *sql.Tx) error {
	var c backfillCounts
	if err := backfillRelations(ctx, tx, &c); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update issue_ref set target_issue_id = (select t.id from issue t where 'issue:' || t.key = issue_ref.ref)
		where ref like 'issue:%'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update issue_ref set target_document_id = (select d.id from document d join issue i on i.id = issue_ref.issue_id
		where d.project_id = i.project_id and 'doc:' || d.name = issue_ref.ref) where ref like 'doc:%'`); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `select count(*), coalesce(sum(target_issue_id is not null or target_document_id is not null), 0) from issue_ref
		where ref like 'issue:%' or ref like 'doc:%'`).Scan(&c.RefsTotal, &c.RefsFound); err != nil {
		return err
	}
	if c.Documents == 0 && c.RefsTotal == 0 {
		return nil
	}
	slog.Info("schema 14 backfill", "documents", c.Documents, "relations_resolved", c.Resolved, "relations_unresolved", c.Unresolved,
		"supersedes_to_related", c.Downgraded, "self_references_dropped", c.SelfDrops, "values_skipped", c.Skipped,
		"issue_refs", c.RefsTotal, "issue_refs_resolved", c.RefsFound)
	return nil
}

func backfillRelations(ctx context.Context, tx *sql.Tx, c *backfillCounts) error {
	type doc struct {
		id, project int64
		content     string
	}
	var docs []doc
	byName := map[int64]map[string]int64{}
	rows, err := tx.QueryContext(ctx, `select id, project_id, name, content from document order by id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d doc
		var name string
		if err := rows.Scan(&d.id, &d.project, &name, &d.content); err != nil {
			rows.Close()
			return err
		}
		docs = append(docs, d)
		if byName[d.project] == nil {
			byName[d.project] = map[string]int64{}
		}
		byName[d.project][name] = d.id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	issues := map[string]int64{}
	rows, err = tx.QueryContext(ctx, `select key, id from issue`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var id int64
		if err := rows.Scan(&key, &id); err != nil {
			rows.Close()
			return err
		}
		issues[key] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// supersedes edges written so far, to find the one that closes a loop.
	edges := map[int64][]int64{}
	reaches := func(from, to int64) bool {
		seen := map[int64]bool{}
		stack := []int64{from}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == to {
				return true
			}
			if !seen[n] {
				seen[n] = true
				stack = append(stack, edges[n]...)
			}
		}
		return false
	}
	insert := func(docID int64, kind model.RelationKind, targetDoc, targetIssue any, text any) error {
		_, err := tx.ExecContext(ctx, `insert or ignore into document_relation(document_id, kind, target_document_id, target_issue_id, target_text)
			values (?, ?, ?, ?, ?)`, docID, string(kind), targetDoc, targetIssue, text)
		return err
	}
	// documentOf finds a document by the name the value cites: the name itself,
	// or the file name of a path, in the project of the document.
	documentOf := func(project int64, name string) (int64, bool) {
		if id, ok := byName[project][name]; ok {
			return id, true
		}
		id, ok := byName[project][path.Base(name)]
		return id, ok
	}

	for _, d := range docs {
		c.Documents++
		entries, ok := model.FrontMatterEntries(d.content)
		if !ok {
			continue
		}
		for _, en := range entries {
			switch en.Key {
			case "supersedes", "related":
				names := backfillFileRE.FindAllString(en.Value, -1)
				if len(names) == 0 || model.IsPlaceholder(en.Value) {
					if strings.TrimSpace(en.Value) != "" {
						c.Skipped++
					}
					continue
				}
				kind := model.RelationKind(en.Key)
				// A value that holds more than file names says something about
				// the file: the document replaces only a part of it.
				if kind == model.RelSupersedes && strings.TrimSpace(backfillFiller.Replace(backfillFileRE.ReplaceAllString(en.Value, ""))) != "" {
					kind = model.RelRelated
					c.Downgraded++
				}
				for _, name := range names {
					target, found := documentOf(d.project, name)
					switch {
					case !found:
						c.Unresolved++
						if err := insert(d.id, kind, nil, nil, name); err != nil {
							return err
						}
					case target == d.id:
						c.SelfDrops++
					default:
						k := kind
						if k == model.RelSupersedes && reaches(target, d.id) {
							k = model.RelRelated
							c.Downgraded++
						}
						if k == model.RelSupersedes {
							edges[d.id] = append(edges[d.id], target)
						}
						c.Resolved++
						if err := insert(d.id, k, target, nil, nil); err != nil {
							return err
						}
					}
				}
			case "issue", "issues":
				keys := backfillKeyRE.FindAllString(en.Value, -1)
				if len(keys) == 0 {
					if strings.TrimSpace(en.Value) != "" {
						c.Skipped++
					}
					continue
				}
				for _, key := range keys {
					if id, found := issues[key]; found {
						c.Resolved++
						err = insert(d.id, model.RelAbout, nil, id, nil)
					} else {
						c.Unresolved++
						err = insert(d.id, model.RelAbout, nil, nil, key)
					}
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
