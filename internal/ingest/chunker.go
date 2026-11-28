package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

// Default semantic chunk sizing constants.
const (
	DefaultMaxChunkRunes = 1200
)

// ChunkerConfig configures SemanticChunker behavior.
type ChunkerConfig struct {
	MaxChunkRunes int
}

// ChunkInput describes a normalized source document to split into semantic chunks.
type ChunkInput struct {
	Title      string
	ProjectKey string
	Kind       string
	Body       string
	Summary    string
}

// SemanticChunk is one heading-aware chunk ready for rag_chunk insertion.
type SemanticChunk struct {
	ChunkIndex    int
	SectionPath   string
	HeaderPrefix  string
	ChunkText     string
	SearchText    string
	ContentSHA256 string
}

// ChunkTarget identifies the persisted domain entity owning a set of chunks.
type ChunkTarget struct {
	ProjectID  int64
	SourceType model.IngestSourceType
	SourceID   int64
	ArtifactID *int64
	Archived   bool
}

// StoredChunk is a persisted rag_chunk row returned after ReplaceChunksTx.
type StoredChunk struct {
	ID            int64
	ChunkIndex    int
	SearchText    string
	ContentSHA256 string
}

// SemanticChunker splits markdown documents along heading boundaries while
// keeping fenced code blocks and markdown tables intact and prepending a
// structured breadcrumb header to every chunk's search_text.
type SemanticChunker struct {
	maxRunes int
}

// NewSemanticChunker returns a SemanticChunker with safe defaults.
func NewSemanticChunker(cfg ChunkerConfig) *SemanticChunker {
	maxRunes := cfg.MaxChunkRunes
	if maxRunes <= 0 {
		maxRunes = DefaultMaxChunkRunes
	}
	return &SemanticChunker{maxRunes: maxRunes}
}

type mdSection struct {
	sectionPath string
	headingLine string
	blocks      []mdBlock
}

type blockKind int

const (
	blockProse blockKind = iota
	blockCode
	blockTable
)

type mdBlock struct {
	kind blockKind
	text string
}

// Chunk produces deterministic semantic chunks for in.
func (c *SemanticChunker) Chunk(in ChunkInput) []SemanticChunk {
	body := strings.ReplaceAll(strings.TrimSpace(in.Body), "\r\n", "\n")
	if body == "" {
		fallback := strings.TrimSpace(in.Summary)
		if fallback == "" {
			fallback = strings.TrimSpace(in.Title)
		}
		if fallback == "" {
			return nil
		}
		return []SemanticChunk{buildChunk(0, "", buildBreadcrumb(in.Title, in.ProjectKey, in.Kind, ""), fallback)}
	}

	sections := parseMarkdownSections(body)
	var out []SemanticChunk

	for _, sec := range sections {
		headerPrefix := buildBreadcrumb(in.Title, in.ProjectKey, in.Kind, sec.sectionPath)
		pieces := packBlocks(sec.headingLine, sec.blocks, c.maxRunes)
		for _, text := range pieces {
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			out = append(out, buildChunk(len(out), sec.sectionPath, headerPrefix, text))
		}
	}

	if len(out) == 0 {
		fallback := strings.TrimSpace(in.Title)
		if fallback != "" {
			out = append(out, buildChunk(0, "", buildBreadcrumb(in.Title, in.ProjectKey, in.Kind, ""), fallback))
		}
	}
	return out
}

// ReplaceChunksTx atomically replaces every rag_chunk for
// (target.SourceType, target.SourceID) inside tx. Deleting old chunks cascades
// to rag_embedding so stale chunk/embedding pairs can never remain.
func ReplaceChunksTx(ctx context.Context, tx pgx.Tx, target ChunkTarget, chunks []SemanticChunk) ([]StoredChunk, error) {
	if _, err := tx.Exec(ctx,
		`DELETE FROM rag_chunk WHERE source_type = $1 AND source_id = $2`,
		string(target.SourceType), target.SourceID,
	); err != nil {
		return nil, err
	}

	stored := make([]StoredChunk, 0, len(chunks))
	for _, ch := range chunks {
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO rag_chunk (
				project_id, source_type, source_id, artifact_id, chunk_index,
				section_path, header_prefix, chunk_text, search_text, content_sha256, archived
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id`,
			target.ProjectID, string(target.SourceType), target.SourceID, target.ArtifactID,
			ch.ChunkIndex, ch.SectionPath, ch.HeaderPrefix, ch.ChunkText, ch.SearchText,
			ch.ContentSHA256, target.Archived,
		).Scan(&id); err != nil {
			return nil, err
		}
		stored = append(stored, StoredChunk{
			ID:            id,
			ChunkIndex:    ch.ChunkIndex,
			SearchText:    ch.SearchText,
			ContentSHA256: ch.ContentSHA256,
		})
	}
	return stored, nil
}

func buildChunk(index int, sectionPath, headerPrefix, chunkText string) SemanticChunk {
	searchText := chunkText
	if headerPrefix != "" {
		searchText = headerPrefix + "\n\n" + chunkText
	}
	sum := sha256.Sum256([]byte(searchText))
	return SemanticChunk{
		ChunkIndex:    index,
		SectionPath:   sectionPath,
		HeaderPrefix:  headerPrefix,
		ChunkText:     chunkText,
		SearchText:    searchText,
		ContentSHA256: hex.EncodeToString(sum[:]),
	}
}

func buildBreadcrumb(title, projectKey, kind, sectionPath string) string {
	parts := make([]string, 0, 4)
	for _, p := range []string{title, projectKey, kind, sectionPath} {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

type headingFrame struct {
	level int
	text  string
}

func parseMarkdownSections(body string) []mdSection {
	lines := strings.Split(body, "\n")
	var (
		stack      []headingFrame
		sections   []mdSection
		curSection = &mdSection{}
	)

	pushSection := func() {
		if len(curSection.blocks) > 0 || curSection.headingLine != "" {
			sections = append(sections, *curSection)
		}
	}

	sectionPathFromStack := func() string {
		if len(stack) == 0 {
			return ""
		}
		names := make([]string, len(stack))
		for i, f := range stack {
			names[i] = f.text
		}
		return strings.Join(names, " > ")
	}

	var proseBuf []string
	flushProse := func() {
		if len(proseBuf) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(proseBuf, "\n"))
		proseBuf = proseBuf[:0]
		if text != "" {
			curSection.blocks = append(curSection.blocks, mdBlock{kind: blockProse, text: text})
		}
	}

	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Fenced code block: consume atomically until matching closing fence.
		if ch, n, ok := detectFence(trimmed); ok {
			flushProse()
			start := i
			i++
			for i < len(lines) {
				t := strings.TrimSpace(lines[i])
				if c2, n2, ok2 := detectFence(t); ok2 && c2 == ch && n2 >= n && strings.TrimSpace(t[n2:]) == "" {
					i++
					break
				}
				i++
			}
			curSection.blocks = append(curSection.blocks, mdBlock{
				kind: blockCode,
				text: strings.Join(lines[start:i], "\n"),
			})
			continue
		}

		// Heading line starts a new semantic section.
		if m := mdHeadingLineRE.FindStringSubmatch(trimmed); m != nil {
			flushProse()
			pushSection()
			level := len(m[1])
			title := cleanInlineMarkdown(m[2])
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}
			if title != "" {
				stack = append(stack, headingFrame{level: level, text: title})
			}
			curSection = &mdSection{
				sectionPath: sectionPathFromStack(),
				headingLine: trimmed,
			}
			i++
			continue
		}

		// Markdown table block: header line + delimiter/rows starting with '|'.
		if isTableStart(lines, i) {
			flushProse()
			start := i
			i++
			for i < len(lines) && isTableRow(lines[i]) {
				i++
			}
			curSection.blocks = append(curSection.blocks, mdBlock{
				kind: blockTable,
				text: strings.Join(lines[start:i], "\n"),
			})
			continue
		}

		// Blank line splits prose paragraphs cleanly.
		if trimmed == "" {
			flushProse()
			i++
			continue
		}

		proseBuf = append(proseBuf, line)
		i++
	}

	flushProse()
	pushSection()
	return sections
}

func isTableStart(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	if !isTableRow(lines[i]) {
		return false
	}
	return isTableDelimiterRow(lines[i+1])
}

func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.Contains(t[1:], "|")
}

func isTableDelimiterRow(line string) bool {
	t := strings.TrimSpace(line)
	if !isTableRow(t) {
		return false
	}
	inner := strings.Trim(t, "|")
	hasDash := false
	for _, r := range inner {
		switch r {
		case '-', ':', '|', ' ', '\t':
			if r == '-' {
				hasDash = true
			}
		default:
			return false
		}
	}
	return hasDash
}

func packBlocks(headingLine string, blocks []mdBlock, maxRunes int) []string {
	if len(blocks) == 0 {
		if headingLine != "" {
			return []string{headingLine}
		}
		return nil
	}

	var out []string
	var cur []string
	curRunes := 0

	startChunk := func() {
		cur = cur[:0]
		curRunes = 0
		if headingLine != "" {
			cur = append(cur, headingLine)
			curRunes = len([]rune(headingLine))
		}
	}
	flushChunk := func() {
		if len(cur) == 0 {
			return
		}
		// Avoid emitting a heading-only chunk when followed by real content.
		if len(cur) == 1 && headingLine != "" && cur[0] == headingLine && len(out) > 0 {
			cur = cur[:0]
			curRunes = 0
			return
		}
		out = append(out, strings.Join(cur, "\n\n"))
		cur = cur[:0]
		curRunes = 0
	}

	startChunk()
	for _, blk := range blocks {
		blkRunes := len([]rune(blk.text))

		// If this block fits into the current chunk, append it.
		extra := blkRunes
		if len(cur) > 0 {
			extra += 2
		}
		if curRunes+extra <= maxRunes {
			cur = append(cur, blk.text)
			curRunes += extra
			continue
		}

		// Flush existing accumulated blocks if any content beyond heading exists.
		if len(cur) > 1 || (len(cur) == 1 && (headingLine == "" || cur[0] != headingLine)) {
			flushChunk()
			startChunk()
		}

		// If the single block fits in a fresh chunk (with heading), emit or keep accumulating.
		if curRunes+blkRunes+2 <= maxRunes {
			cur = append(cur, blk.text)
			curRunes += blkRunes + 2
			continue
		}

		// Oversized block handling while preserving table headers or code fences.
		subPieces := splitOversizedBlock(blk, maxRunes-curRunes-2)
		for _, sub := range subPieces {
			if len(cur) > 1 || (len(cur) == 1 && headingLine == "") {
				flushChunk()
				startChunk()
			}
			cur = append(cur, sub)
			flushChunk()
			startChunk()
		}
	}

	if len(cur) > 1 || (len(cur) == 1 && (headingLine == "" || len(out) == 0)) {
		flushChunk()
	}
	return out
}

func splitOversizedBlock(blk mdBlock, budget int) []string {
	if budget < 200 {
		budget = 200
	}
	switch blk.kind {
	case blockTable:
		return splitTableByRows(blk.text, budget)
	case blockCode:
		return splitCodeByLines(blk.text, budget)
	default:
		return splitProseByBudget(blk.text, budget)
	}
}

func splitTableByRows(table string, budget int) []string {
	lines := strings.Split(table, "\n")
	if len(lines) <= 2 {
		return []string{table}
	}
	header := lines[0] + "\n" + lines[1]
	headerRunes := len([]rune(header))

	var pieces []string
	cur := []string{header}
	curRunes := headerRunes

	for _, row := range lines[2:] {
		rowRunes := len([]rune(row)) + 1
		if curRunes+rowRunes > budget && len(cur) > 1 {
			pieces = append(pieces, strings.Join(cur, "\n"))
			cur = []string{header, row}
			curRunes = headerRunes + rowRunes
		} else {
			cur = append(cur, row)
			curRunes += rowRunes
		}
	}
	if len(cur) > 1 {
		pieces = append(pieces, strings.Join(cur, "\n"))
	}
	return pieces
}

func splitCodeByLines(code string, budget int) []string {
	lines := strings.Split(code, "\n")
	if len(lines) <= 2 {
		return []string{code}
	}
	openFence := lines[0]
	closeFence := lines[len(lines)-1]
	bodyLines := lines[1 : len(lines)-1]
	fenceOverhead := len([]rune(openFence)) + len([]rune(closeFence)) + 2

	var pieces []string
	var cur []string
	curRunes := fenceOverhead
	for _, line := range bodyLines {
		lr := len([]rune(line)) + 1
		if curRunes+lr > budget && len(cur) > 0 {
			pieces = append(pieces, openFence+"\n"+strings.Join(cur, "\n")+"\n"+closeFence)
			cur = []string{line}
			curRunes = fenceOverhead + lr
		} else {
			cur = append(cur, line)
			curRunes += lr
		}
	}
	if len(cur) > 0 {
		pieces = append(pieces, openFence+"\n"+strings.Join(cur, "\n")+"\n"+closeFence)
	}
	return pieces
}

func splitProseByBudget(text string, budget int) []string {
	lines := strings.Split(text, "\n")
	var pieces []string
	var cur strings.Builder
	curRunes := 0

	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			pieces = append(pieces, s)
		}
		cur.Reset()
		curRunes = 0
	}

	for _, line := range lines {
		r := []rune(line)
		if len(r) > budget {
			flush()
			for len(r) > 0 {
				n := min(len(r), budget)
				if s := strings.TrimSpace(string(r[:n])); s != "" {
					pieces = append(pieces, s)
				}
				r = r[n:]
			}
			continue
		}
		if curRunes+len(r)+1 > budget && curRunes > 0 {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
			curRunes++
		}
		cur.WriteString(line)
		curRunes += len(r)
	}
	flush()
	return pieces
}
