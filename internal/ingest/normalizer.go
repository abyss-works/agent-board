package ingest

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

// DefaultPipelineVersion tags normalized artifacts produced by DefaultNormalizer.
const DefaultPipelineVersion = "v1"

// Compile-time check that DefaultNormalizer implements pg.Normalizer.
var _ pg.Normalizer = (*DefaultNormalizer)(nil)

// DefaultNormalizer strips YAML frontmatter, cleans titles, builds concise
// summaries, and extracts headings and keywords without ever altering source
// domain rows.
type DefaultNormalizer struct {
	version string
}

// NewDefaultNormalizer returns a DefaultNormalizer tagged with pipelineVersion
// (defaults to DefaultPipelineVersion when empty).
func NewDefaultNormalizer(pipelineVersion string) *DefaultNormalizer {
	v := strings.TrimSpace(pipelineVersion)
	if v == "" {
		v = DefaultPipelineVersion
	}
	return &DefaultNormalizer{version: v}
}

// PipelineVersion returns the pipeline version recorded in normalized_artifact.
func (n *DefaultNormalizer) PipelineVersion() string { return n.version }

var (
	mdLinkRE        = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdInlineCodeRE  = regexp.MustCompile("`([^`]+)`")
	mdHeadingLineRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	multiBlankRE    = regexp.MustCompile(`\n{3,}`)
)

// Normalize turns heterogeneous raw source content into a normalized title,
// concise summary, structured metadata, and clean markdown body.
func (n *DefaultNormalizer) Normalize(_ context.Context, in pg.NormalizeInput) (pg.NormalizeOutput, error) {
	raw := strings.TrimPrefix(in.Content, "\xef\xbb\xbf")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	fmEntries, hasFM := model.FrontMatterEntries(raw)
	fmParsed, _ := model.ParseFrontMatter(raw)
	body := raw
	if hasFM {
		body = stripYAMLFrontMatter(raw)
	}
	body = strings.TrimSpace(multiBlankRE.ReplaceAllString(body, "\n\n"))

	headings := extractMarkdownHeadings(body)
	title := strings.TrimSpace(in.Title)
	if title == "" && fmParsed.Title != nil {
		title = strings.TrimSpace(*fmParsed.Title)
	}
	if title == "" && len(headings) > 0 {
		title = headings[0]
	}
	if title == "" {
		title = strings.TrimSpace(in.KeyOrName)
	}
	if title == "" {
		title = string(in.SourceType)
	}

	summary := buildSummary(body, title, 320)
	keywords := extractKeywords(title+" "+strings.Join(headings, " ")+" "+body, 12)

	meta := map[string]any{
		"source_type": string(in.SourceType),
		"project":     in.ProjectKey,
		"key_or_name": in.KeyOrName,
		"kind":        in.Kind,
		"headings":    headings,
		"keywords":    keywords,
	}
	if len(fmParsed.Tags) > 0 {
		meta["tags"] = fmParsed.Tags
	}
	if hasFM && len(fmEntries) > 0 {
		fmMap := make(map[string]string, len(fmEntries))
		for _, en := range fmEntries {
			fmMap[en.Key] = en.Value
		}
		meta["frontmatter"] = fmMap
	}

	return pg.NormalizeOutput{
		NormalizedTitle: title,
		Summary:         summary,
		Metadata:        meta,
		NormalizedBody:  body,
	}, nil
}

// ArtifactQuerier is the minimal query interface needed to upsert a normalized
// artifact (satisfied by *pgxpool.Pool and pgx.Tx).
type ArtifactQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// UpsertNormalizedArtifact persists a NormalizeOutput into normalized_artifact
// keyed by (source_type, source_id, pipeline_version) and returns its ID.
func UpsertNormalizedArtifact(
	ctx context.Context,
	q ArtifactQuerier,
	sourceType model.IngestSourceType,
	sourceID int64,
	sourceSHA256 string,
	pipelineVersion string,
	out pg.NormalizeOutput,
) (int64, error) {
	metaJSON := []byte("{}")
	if len(out.Metadata) > 0 {
		raw, err := json.Marshal(out.Metadata)
		if err != nil {
			return 0, err
		}
		metaJSON = raw
	}
	var artifactID int64
	err := q.QueryRow(ctx, `
		INSERT INTO normalized_artifact (
			source_type, source_id, source_sha256, pipeline_version,
			normalized_title, summary, metadata, normalized_body
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
		ON CONFLICT (source_type, source_id, pipeline_version)
		DO UPDATE SET
			source_sha256    = EXCLUDED.source_sha256,
			normalized_title = EXCLUDED.normalized_title,
			summary          = EXCLUDED.summary,
			metadata         = EXCLUDED.metadata,
			normalized_body  = EXCLUDED.normalized_body
		RETURNING id`,
		string(sourceType), sourceID, sourceSHA256, pipelineVersion,
		out.NormalizedTitle, out.Summary, string(metaJSON), out.NormalizedBody,
	).Scan(&artifactID)
	return artifactID, err
}

func stripYAMLFrontMatter(content string) string {
	first, rest, ok := strings.Cut(content, "\n")
	if !ok || strings.TrimRight(first, " \t") != "---" {
		return content
	}
	for {
		line, next, more := strings.Cut(rest, "\n")
		if strings.TrimRight(line, " \t") == "---" {
			return strings.TrimLeft(next, "\n")
		}
		if !more {
			return content
		}
		rest = next
	}
}

func extractMarkdownHeadings(body string) []string {
	var headings []string
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if ch, n, ok := detectFence(trimmed); ok {
			if !inFence {
				inFence = true
				fenceChar = ch
				fenceLen = n
			} else if ch == fenceChar && n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		if m := mdHeadingLineRE.FindStringSubmatch(trimmed); m != nil {
			h := cleanInlineMarkdown(m[2])
			if h != "" {
				headings = append(headings, h)
			}
		}
	}
	if headings == nil {
		return []string{}
	}
	return headings
}

func buildSummary(body, fallbackTitle string, maxRunes int) string {
	var lines []string
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0

	for _, rawLine := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if ch, n, ok := detectFence(trimmed); ok {
			if !inFence {
				inFence = true
				fenceChar = ch
				fenceLen = n
			} else if ch == fenceChar && n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		if trimmed == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		// Skip headings, horizontal rules, and markdown table separators for summary prose.
		if mdHeadingLineRE.MatchString(trimmed) || trimmed == "---" || strings.HasPrefix(trimmed, "|") {
			continue
		}
		cleaned := cleanInlineMarkdown(strings.TrimLeft(trimmed, "-*> "))
		if cleaned != "" {
			lines = append(lines, cleaned)
		}
		if len([]rune(strings.Join(lines, " "))) >= maxRunes {
			break
		}
	}
	summary := strings.TrimSpace(strings.Join(lines, " "))
	if summary == "" {
		summary = fallbackTitle
	}
	r := []rune(summary)
	if len(r) > maxRunes {
		summary = strings.TrimSpace(string(r[:maxRunes])) + "..."
	}
	return summary
}

func cleanInlineMarkdown(s string) string {
	s = mdLinkRE.ReplaceAllString(s, "$1")
	s = mdInlineCodeRE.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

var keywordStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "only": true, "when": true, "will": true, "have": true,
	"were": true, "been": true, "then": true, "than": true, "also": true, "about": true,
	"using": true, "after": true, "before": true, "should": true, "must": true, "true": true,
	"false": true, "null": true, "none": true, "http": true, "https": true, "json": true,
	"한다": true, "있다": true, "위해": true, "통해": true, "대한": true, "관련": true,
	"경우": true, "기준": true, "사용": true, "적용": true, "있는": true, "없는": true,
}

func extractKeywords(text string, limit int) []string {
	freq := map[string]int{}
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		tok := strings.ToLower(cur.String())
		cur.Reset()
		if len([]rune(tok)) < 2 || keywordStopwords[tok] {
			return
		}
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
		freq[tok]++
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()

	type kv struct {
		tok   string
		count int
	}
	pairs := make([]kv, 0, len(freq))
	for k, c := range freq {
		pairs = append(pairs, kv{tok: k, count: c})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].tok < pairs[j].tok
		}
		return pairs[i].count > pairs[j].count
	})
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.tok
	}
	return out
}

func detectFence(trimmed string) (byte, int, bool) {
	if len(trimmed) < 3 {
		return 0, 0, false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	return ch, n, true
}
