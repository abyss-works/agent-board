package graph

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

// ExtractSource supplies read-only source metadata and content for graph edge
// extraction. The extractor never modifies source domain tables.
type ExtractSource struct {
	ProjectID  int64
	ProjectKey string
	SourceType model.GraphNodeType
	SourceID   int64
	KeyOrName  string
	Kind       string
	Title      string
	Body       string
	Tags       []string
	Refs       []string
}

// ExtractionReport summarizes edges persisted for one source entity.
type ExtractionReport struct {
	Explicit []model.Edge
	Inferred []model.Edge
}

// Extractor discovers explicit references (issue:KEY, doc:name, spec:slug,
// frontmatter relations) and inferred semantic edges (about, related, and
// candidate supersedes) for a source document or issue.
type Extractor struct {
	pool  *pgxpool.Pool
	store *Store
}

// NewExtractor returns an Extractor wired to pool and store.
func NewExtractor(pool *pgxpool.Pool, store *Store) *Extractor {
	if store == nil {
		store = NewStore(pool)
	}
	return &Extractor{pool: pool, store: store}
}

const maxExtractedRefsPerDoc = 128

var (
	typedIssueRefRE = regexp.MustCompile(`\bissue:([A-Z][A-Z0-9]{1,9}-[0-9]+)\b`)
	typedDocRefRE   = regexp.MustCompile(`\bdoc:([A-Za-z0-9._/-]+\.md|[A-Za-z0-9._/-]+)\b`)
	typedSpecRefRE  = regexp.MustCompile(`\bspec:([A-Z][A-Z0-9]{1,9}/[a-z0-9][a-z0-9._-]{0,127}|[a-z0-9][a-z0-9._-]{0,127})\b`)
	bareIssueKeyRE  = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9}-[0-9]+)\b`)
	recordFileRE    = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}-\d{4}-[a-z][a-z0-9_]*-[a-z0-9-]+\.md)\b`)
)

var supersedeCueTerms = []string{
	"supersedes", "supersede", "superseded", "replaces", "replace",
	"obsoletes", "deprecates", "revising", "replaced by",
	"대체한다", "대체함", "대체합니다", "대체하는", "대체",
	"폐기한다", "폐기하고", "기존 결정", "전환 확정", "개정",
}

// ExtractAndStore extracts explicit and inferred edges from src and persists
// them via Store. Source domain tables are strictly read-only.
func (e *Extractor) ExtractAndStore(ctx context.Context, src ExtractSource) (ExtractionReport, error) {
	var report ExtractionReport
	if _, ok := model.ParseGraphNodeType(string(src.SourceType)); !ok || src.SourceID <= 0 {
		return report, nil
	}

	sourceRef := pg.NodeRef{Type: src.SourceType, ID: src.SourceID}
	projectKey := model.NormalizeProjectKey(src.ProjectKey)
	seenExplicit := map[string]bool{}
	seenInferred := map[string]bool{}

	recordExplicit := func(target *pg.NodeRef, unresolved *string, kind model.EdgeKind, evidence string) error {
		if target != nil && target.Type == sourceRef.Type && target.ID == sourceRef.ID {
			return nil
		}
		dedup := edgeDedupKey(target, unresolved, kind)
		if seenExplicit[dedup] {
			return nil
		}
		seenExplicit[dedup] = true
		ev := nonEmptyPtr(evidence)
		edge, err := e.store.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
			ProjectID:            src.ProjectID,
			Source:               sourceRef,
			Target:               target,
			UnresolvedTargetText: unresolved,
			Kind:                 kind,
			Evidence:             ev,
		})
		if errors.Is(err, ErrCycle) {
			return nil
		}
		if err != nil {
			return err
		}
		report.Explicit = append(report.Explicit, edge)
		return nil
	}

	recordInferred := func(target pg.NodeRef, kind model.EdgeKind, confidence float64, evidence string) error {
		if target.Type == sourceRef.Type && target.ID == sourceRef.ID {
			return nil
		}
		dedup := edgeDedupKey(&target, nil, kind)
		if seenExplicit[dedup] || seenInferred[dedup] {
			return nil
		}
		seenInferred[dedup] = true
		c := clampConfidence(confidence)
		ev := nonEmptyPtr(evidence)
		edge, err := e.store.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
			ProjectID:  src.ProjectID,
			Source:     sourceRef,
			Target:     &target,
			Kind:       kind,
			Confidence: &c,
			Evidence:   ev,
		})
		if errors.Is(err, ErrCycle) {
			return nil
		}
		if err != nil {
			return err
		}
		report.Inferred = append(report.Inferred, edge)
		return nil
	}

	type cachedRef struct {
		ref pg.NodeRef
		ok  bool
	}
	issueCache := map[string]cachedRef{}
	recordCache := map[string]cachedRef{}
	specCache := map[string]cachedRef{}
	lookupsUsed := 0

	resolveIssueCached := func(key string) (pg.NodeRef, bool, bool, error) {
		if c, hit := issueCache[key]; hit {
			return c.ref, c.ok, true, nil
		}
		if lookupsUsed >= maxExtractedRefsPerDoc {
			return pg.NodeRef{}, false, false, nil
		}
		lookupsUsed++
		ref, ok, err := e.resolveIssue(ctx, key)
		if err != nil {
			return pg.NodeRef{}, false, false, err
		}
		issueCache[key] = cachedRef{ref: ref, ok: ok}
		return ref, ok, true, nil
	}

	resolveRecordCached := func(refText string) (pg.NodeRef, bool, bool, error) {
		if c, hit := recordCache[refText]; hit {
			return c.ref, c.ok, true, nil
		}
		if lookupsUsed >= maxExtractedRefsPerDoc {
			return pg.NodeRef{}, false, false, nil
		}
		lookupsUsed++
		ref, ok, err := e.resolveRecord(ctx, src.ProjectID, projectKey, refText)
		if err != nil {
			return pg.NodeRef{}, false, false, err
		}
		recordCache[refText] = cachedRef{ref: ref, ok: ok}
		return ref, ok, true, nil
	}

	resolveSpecCached := func(refText string) (pg.NodeRef, bool, bool, error) {
		if c, hit := specCache[refText]; hit {
			return c.ref, c.ok, true, nil
		}
		if lookupsUsed >= maxExtractedRefsPerDoc {
			return pg.NodeRef{}, false, false, nil
		}
		lookupsUsed++
		ref, ok, err := e.resolveSpec(ctx, src.ProjectID, projectKey, refText)
		if err != nil {
			return pg.NodeRef{}, false, false, err
		}
		specCache[refText] = cachedRef{ref: ref, ok: ok}
		return ref, ok, true, nil
	}

	resolveDocOrSpecCached := func(refText string) (pg.NodeRef, bool, bool, error) {
		if strings.HasPrefix(refText, "spec:") {
			return resolveSpecCached(strings.TrimPrefix(refText, "spec:"))
		}
		if strings.HasPrefix(refText, "doc:") {
			return resolveRecordCached(strings.TrimPrefix(refText, "doc:"))
		}
		ref, ok, allowed, err := resolveRecordCached(refText)
		if err != nil || ok || !allowed {
			return ref, ok, allowed, err
		}
		return resolveSpecCached(refText)
	}

	// 1. Parse explicit YAML frontmatter relations if present.
	bodyText := src.Body
	if fm, ok := model.ParseFrontMatter(src.Body); ok {
		bodyText = stripFrontMatterBody(src.Body)
		for _, issueKey := range dedupStrings(fm.Issues, maxExtractedRefsPerDoc) {
			ref, found, allowed, err := resolveIssueCached(issueKey)
			if err != nil {
				return report, err
			}
			if !allowed {
				break
			}
			if found {
				if err := recordExplicit(&ref, nil, model.EdgeKindAbout, "frontmatter issue: "+issueKey); err != nil {
					return report, err
				}
			} else {
				u := "issue:" + issueKey
				if err := recordExplicit(nil, &u, model.EdgeKindAbout, "frontmatter issue: "+issueKey); err != nil {
					return report, err
				}
			}
		}
		for _, relDoc := range dedupStrings(fm.Related, maxExtractedRefsPerDoc) {
			ref, found, allowed, err := resolveDocOrSpecCached(relDoc)
			if err != nil {
				return report, err
			}
			if !allowed {
				break
			}
			if found {
				if err := recordExplicit(&ref, nil, model.EdgeKindRelated, "frontmatter related: "+relDoc); err != nil {
					return report, err
				}
			} else {
				u := relDoc
				if err := recordExplicit(nil, &u, model.EdgeKindRelated, "frontmatter related: "+relDoc); err != nil {
					return report, err
				}
			}
		}
		for _, supDoc := range dedupStrings(fm.Supersedes, maxExtractedRefsPerDoc) {
			ref, found, allowed, err := resolveDocOrSpecCached(supDoc)
			if err != nil {
				return report, err
			}
			if !allowed {
				break
			}
			if found {
				if err := recordExplicit(&ref, nil, model.EdgeKindSupersedes, "frontmatter supersedes: "+supDoc); err != nil {
					return report, err
				}
			} else {
				u := supDoc
				if err := recordExplicit(nil, &u, model.EdgeKindSupersedes, "frontmatter supersedes: "+supDoc); err != nil {
					return report, err
				}
			}
		}
	}

	// 2. Parse explicit typed references from Refs and body (issue:KEY, doc:name, spec:slug).
	refSources := make([]string, 0, len(src.Refs)+1)
	refSources = append(refSources, src.Refs...)
	refSources = append(refSources, bodyText)
	combinedRefsText := strings.Join(refSources, "\n")

	for _, key := range extractUniqueRefs(typedIssueRefRE, combinedRefsText, "", maxExtractedRefsPerDoc) {
		ref, found, allowed, err := resolveIssueCached(key)
		if err != nil {
			return report, err
		}
		if !allowed {
			break
		}
		if !found {
			continue
		}
		kind := model.EdgeKindAbout
		if src.SourceType == model.GraphNodeIssue {
			kind = model.EdgeKindRelated
		}
		if err := recordExplicit(&ref, nil, kind, "explicit ref issue:"+key); err != nil {
			return report, err
		}
	}

	for _, docRef := range extractUniqueRefs(typedDocRefRE, combinedRefsText, ".,;:)", maxExtractedRefsPerDoc) {
		ref, found, allowed, err := resolveRecordCached(docRef)
		if err != nil {
			return report, err
		}
		if !allowed {
			break
		}
		if !found {
			continue
		}
		if err := recordExplicit(&ref, nil, model.EdgeKindRelated, "explicit ref doc:"+docRef); err != nil {
			return report, err
		}
	}

	for _, specRef := range extractUniqueRefs(typedSpecRefRE, combinedRefsText, ".,;:)", maxExtractedRefsPerDoc) {
		ref, found, allowed, err := resolveSpecCached(specRef)
		if err != nil {
			return report, err
		}
		if !allowed {
			break
		}
		if !found {
			continue
		}
		kind := model.EdgeKindAbout
		if src.SourceType == model.GraphNodeSpec {
			kind = model.EdgeKindRelated
		}
		if err := recordExplicit(&ref, nil, kind, "explicit ref spec:"+specRef); err != nil {
			return report, err
		}
	}

	// 3. Inferred issue mentions (bare keys in body -> about/related confirmed).
	for _, key := range extractUniqueRefs(bareIssueKeyRE, bodyText, "", maxExtractedRefsPerDoc) {
		ref, found, allowed, err := resolveIssueCached(key)
		if err != nil {
			return report, err
		}
		if !allowed {
			break
		}
		if !found {
			continue
		}
		kind := model.EdgeKindAbout
		if src.SourceType == model.GraphNodeIssue {
			kind = model.EdgeKindRelated
		}
		if err := recordInferred(ref, kind, 0.85, "mentioned issue "+key); err != nil {
			return report, err
		}
	}

	// 4. Inferred record/spec mentions and semantic overlap for related & supersedes candidates.
	hasSupersedeCue, matchedCue := detectSupersedeCue(src.Title + "\n" + bodyText)

	// 4a. Direct record filename mentions in body text.
	for _, docName := range extractUniqueRefs(recordFileRE, bodyText, "", maxExtractedRefsPerDoc) {
		if docName == src.KeyOrName {
			continue
		}
		ref, found, allowed, err := resolveRecordCached(docName)
		if err != nil {
			return report, err
		}
		if !allowed {
			break
		}
		if !found {
			continue
		}
		if err := recordInferred(ref, model.EdgeKindRelated, 0.82, "referenced record "+docName); err != nil {
			return report, err
		}
		if hasSupersedeCue && (src.SourceType == model.GraphNodeRecord || src.SourceType == model.GraphNodeSpec) {
			ev := fmt.Sprintf("supersedes cue (%s) referencing %s", matchedCue, docName)
			if err := recordInferred(ref, model.EdgeKindSupersedes, 0.86, ev); err != nil {
				return report, err
			}
		}
	}

	// 4b. Lexical/topic overlap against peer specs and records in the same project
	// (plus GLOBAL scope) to infer related edges and supersedes candidates.
	if err := e.inferPeerDocumentEdges(ctx, src, bodyText, hasSupersedeCue, matchedCue, recordInferred); err != nil {
		return report, err
	}

	return report, nil
}

type peerDoc struct {
	ref     pg.NodeRef
	name    string
	kind    string
	title   string
	tokens  map[string]struct{}
	isOlder bool
}

func (e *Extractor) inferPeerDocumentEdges(
	ctx context.Context,
	src ExtractSource,
	bodyText string,
	hasSupersedeCue bool,
	matchedCue string,
	recordInferred func(target pg.NodeRef, kind model.EdgeKind, confidence float64, evidence string) error,
) error {
	srcTokens := tokenizeForOverlap(src.Title + " " + strings.Join(src.Tags, " ") + " " + truncateRunes(bodyText, 1200))
	if len(srcTokens) < 2 {
		return nil
	}

	peers, err := e.loadPeerDocuments(ctx, src)
	if err != nil {
		return err
	}

	type scoredPeer struct {
		peer  peerDoc
		score float64
		terms []string
	}
	var matches []scoredPeer
	for _, p := range peers {
		sim, shared := tokenOverlap(srcTokens, p.tokens)
		if len(shared) < 2 || sim < 0.22 {
			continue
		}
		matches = append(matches, scoredPeer{peer: p, score: sim, terms: shared})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].peer.ref.ID < matches[j].peer.ref.ID
		}
		return matches[i].score > matches[j].score
	})
	if len(matches) > 5 {
		matches = matches[:5]
	}

	for _, m := range matches {
		ev := fmt.Sprintf("shared topic terms (%s) with %s", strings.Join(m.terms[:min(len(m.terms), 4)], ", "), m.peer.name)
		relConf := math.Min(0.95, 0.45+m.score*0.6)
		if err := recordInferred(m.peer.ref, model.EdgeKindRelated, relConf, ev); err != nil {
			return err
		}

		// Supersedes candidates are ONLY inferred as candidate (never confirmed)
		// when a newer record/spec has strong overlap with an older peer of the
		// same kind (or explicit supersede phrasing is present).
		sameKind := src.Kind != "" && m.peer.kind == src.Kind
		isCandidateType := src.SourceType == model.GraphNodeRecord || src.SourceType == model.GraphNodeSpec
		if isCandidateType && m.peer.isOlder && (hasSupersedeCue || (sameKind && m.score >= 0.40)) {
			supConf := math.Min(0.92, 0.50+m.score*0.5)
			if hasSupersedeCue {
				supConf = math.Min(0.95, supConf+0.12)
			}
			supEv := ev
			if hasSupersedeCue {
				supEv = fmt.Sprintf("supersedes cue (%s); %s", matchedCue, ev)
			}
			if err := recordInferred(m.peer.ref, model.EdgeKindSupersedes, supConf, supEv); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Extractor) loadPeerDocuments(ctx context.Context, src ExtractSource) ([]peerDoc, error) {
	var peers []peerDoc

	// Load non-archived record documents in the same project (or GLOBAL).
	recRows, err := e.pool.Query(ctx, `
		SELECT id, name, kind, title, left(content, 1200)
		FROM record_document
		WHERE project_id IN ($1, 0)
		  AND NOT archived
		ORDER BY id DESC
		LIMIT 100`,
		src.ProjectID,
	)
	if err != nil {
		return nil, err
	}
	for recRows.Next() {
		var id int64
		var name, kind, title, excerpt string
		if err := recRows.Scan(&id, &name, &kind, &title, &excerpt); err != nil {
			recRows.Close()
			return nil, err
		}
		if src.SourceType == model.GraphNodeRecord && id == src.SourceID {
			continue
		}
		isOlder := true
		if src.SourceType == model.GraphNodeRecord {
			isOlder = id < src.SourceID
		}
		peers = append(peers, peerDoc{
			ref:     pg.NodeRef{Type: model.GraphNodeRecord, ID: id},
			name:    name,
			kind:    kind,
			title:   title,
			tokens:  tokenizeForOverlap(title + " " + name + " " + excerpt),
			isOlder: isOlder,
		})
	}
	recRows.Close()
	if err := recRows.Err(); err != nil {
		return nil, err
	}

	// Load non-archived spec documents in the same project (or GLOBAL).
	specRows, err := e.pool.Query(ctx, `
		SELECT sd.id, sd.slug, sd.kind, sd.title, COALESCE(left(sr.content, 1200), '')
		FROM spec_document sd
		LEFT JOIN spec_revision sr ON sr.id = sd.current_revision_id
		WHERE sd.project_id IN ($1, 0)
		  AND NOT sd.archived
		ORDER BY sd.id DESC
		LIMIT 100`,
		src.ProjectID,
	)
	if err != nil {
		return nil, err
	}
	for specRows.Next() {
		var id int64
		var slug, kind, title, excerpt string
		if err := specRows.Scan(&id, &slug, &kind, &title, &excerpt); err != nil {
			specRows.Close()
			return nil, err
		}
		if src.SourceType == model.GraphNodeSpec && id == src.SourceID {
			continue
		}
		isOlder := true
		if src.SourceType == model.GraphNodeSpec {
			isOlder = id < src.SourceID
		}
		peers = append(peers, peerDoc{
			ref:     pg.NodeRef{Type: model.GraphNodeSpec, ID: id},
			name:    slug,
			kind:    kind,
			title:   title,
			tokens:  tokenizeForOverlap(title + " " + slug + " " + excerpt),
			isOlder: isOlder,
		})
	}
	specRows.Close()
	if err := specRows.Err(); err != nil {
		return nil, err
	}

	return peers, nil
}

func (e *Extractor) resolveIssue(ctx context.Context, key string) (pg.NodeRef, bool, error) {
	var id int64
	err := e.pool.QueryRow(ctx, `SELECT id FROM issue WHERE key = $1 AND status <> 'deleted'`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return pg.NodeRef{}, false, nil
	}
	if err != nil {
		return pg.NodeRef{}, false, err
	}
	return pg.NodeRef{Type: model.GraphNodeIssue, ID: id}, true, nil
}

func (e *Extractor) resolveRecord(ctx context.Context, projectID int64, projectKey, refText string) (pg.NodeRef, bool, error) {
	projKey, name := splitProjectRef(projectKey, refText)
	var id int64
	var err error
	if projKey != "" && projKey != projectKey {
		err = e.pool.QueryRow(ctx, `
			SELECT rd.id
			FROM record_document rd
			JOIN project p ON p.id = rd.project_id
			WHERE p.key = $1 AND rd.name = $2`,
			projKey, name,
		).Scan(&id)
	} else {
		err = e.pool.QueryRow(ctx, `
			SELECT id FROM record_document WHERE project_id = $1 AND name = $2`,
			projectID, name,
		).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return pg.NodeRef{}, false, nil
	}
	if err != nil {
		return pg.NodeRef{}, false, err
	}
	return pg.NodeRef{Type: model.GraphNodeRecord, ID: id}, true, nil
}

func (e *Extractor) resolveSpec(ctx context.Context, projectID int64, projectKey, refText string) (pg.NodeRef, bool, error) {
	projKey, slug := splitProjectRef(projectKey, refText)
	var id int64
	var err error
	if projKey != "" && projKey != projectKey {
		err = e.pool.QueryRow(ctx, `
			SELECT sd.id
			FROM spec_document sd
			JOIN project p ON p.id = sd.project_id
			WHERE p.key = $1 AND sd.slug = $2`,
			projKey, slug,
		).Scan(&id)
	} else {
		err = e.pool.QueryRow(ctx, `
			SELECT id FROM spec_document WHERE project_id = $1 AND slug = $2`,
			projectID, slug,
		).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return pg.NodeRef{}, false, nil
	}
	if err != nil {
		return pg.NodeRef{}, false, err
	}
	return pg.NodeRef{Type: model.GraphNodeSpec, ID: id}, true, nil
}

func splitProjectRef(defaultProject, ref string) (string, string) {
	if p, rest, ok := strings.Cut(ref, "/"); ok && model.ValidProjectKey(p) && rest != "" {
		return p, rest
	}
	return defaultProject, ref
}

func detectSupersedeCue(text string) (bool, string) {
	lower := strings.ToLower(text)
	for _, cue := range supersedeCueTerms {
		if strings.Contains(lower, cue) {
			return true, cue
		}
	}
	return false, ""
}

var overlapStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "only": true, "when": true, "will": true, "have": true,
	"were": true, "been": true, "then": true, "than": true, "also": true, "about": true,
	"using": true, "after": true, "before": true, "should": true, "must": true, "true": true,
	"false": true, "null": true, "none": true, "http": true, "https": true, "json": true,
	"markdown": true, "md": true, "title": true, "kind": true, "date": true, "tags": true,
	"한다": true, "있다": true, "한다.": true, "위해": true, "통해": true, "대한": true, "관련": true,
	"경우": true, "기준": true, "사용": true, "적용": true, "기존": true, "변경": true, "문서": true,
}

func tokenizeForOverlap(text string) map[string]struct{} {
	out := map[string]struct{}{}
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		tok := strings.ToLower(cur.String())
		cur.Reset()
		if len([]rune(tok)) < 2 || overlapStopwords[tok] {
			return
		}
		// Skip pure numbers (e.g. timestamps/dates).
		allDigits := true
		for _, r := range tok {
			if !unicode.IsDigit(r) {
				allDigits = false
				break
			}
		}
		if allDigits {
			return
		}
		out[tok] = struct{}{}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func tokenOverlap(a, b map[string]struct{}) (float64, []string) {
	if len(a) == 0 || len(b) == 0 {
		return 0, nil
	}
	small, large := a, b
	if len(a) > len(b) {
		small, large = b, a
	}
	shared := make([]string, 0)
	for tok := range small {
		if _, ok := large[tok]; ok {
			shared = append(shared, tok)
		}
	}
	if len(shared) == 0 {
		return 0, nil
	}
	sort.Strings(shared)
	// Sorensen-Dice coefficient gives intuitive overlap for asymmetric lengths.
	score := (2.0 * float64(len(shared))) / float64(len(a)+len(b))
	return score, shared
}

func stripFrontMatterBody(content string) string {
	trimmed := strings.TrimPrefix(content, "\xef\xbb\xbf")
	first, rest, ok := strings.Cut(trimmed, "\n")
	if !ok || strings.TrimRight(first, " \t\r") != "---" {
		return content
	}
	for {
		line, next, hasMore := strings.Cut(rest, "\n")
		if strings.TrimRight(line, " \t\r") == "---" {
			return strings.TrimLeft(next, "\r\n")
		}
		if !hasMore {
			return content
		}
		rest = next
	}
}

func edgeDedupKey(target *pg.NodeRef, unresolved *string, kind model.EdgeKind) string {
	if target != nil {
		return fmt.Sprintf("r:%s:%d:%s", target.Type, target.ID, kind)
	}
	if unresolved != nil {
		return fmt.Sprintf("u:%s:%s", strings.TrimSpace(*unresolved), kind)
	}
	return string(kind)
}

func clampConfidence(c float64) float64 {
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return math.Round(c*1000) / 1000
}

func nonEmptyPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func extractUniqueRefs(re *regexp.Regexp, text, trimChars string, limit int) []string {
	if limit <= 0 || text == "" {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for offset := 0; offset < len(text); {
		loc := re.FindStringSubmatchIndex(text[offset:])
		if loc == nil {
			break
		}
		if len(loc) >= 4 && loc[2] >= 0 && loc[3] >= 0 {
			val := text[offset+loc[2] : offset+loc[3]]
			if trimChars != "" {
				val = strings.TrimRight(val, trimChars)
			}
			if val != "" {
				if _, ok := seen[val]; !ok {
					seen[val] = struct{}{}
					out = append(out, val)
					if len(out) >= limit {
						break
					}
				}
			}
		}
		if loc[1] <= 0 {
			offset++
		} else {
			offset += loc[1]
		}
	}
	return out
}

func dedupStrings(items []string, limit int) []string {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, min(len(items), limit))
	out := make([]string, 0, min(len(items), limit))
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}
