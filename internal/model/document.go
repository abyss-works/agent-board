package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// DocumentKind is the kind of a record document. The file name and the front
// matter both name it.
type DocumentKind string

const (
	DocumentDecision       DocumentKind = "decision"
	DocumentIncident       DocumentKind = "incident"
	DocumentReport         DocumentKind = "report"
	DocumentImplementation DocumentKind = "implementation"
	// DocumentOther is the kind of an imported document that names no other
	// kind. A record document name never carries it.
	DocumentOther DocumentKind = "other"
)

// recordKinds are the kinds a record document file name may carry.
var recordKinds = []DocumentKind{DocumentDecision, DocumentIncident, DocumentReport, DocumentImplementation}

// DocumentKinds lists every document kind.
var DocumentKinds = append(append([]DocumentKind{}, recordKinds...), DocumentOther)

// DocumentLocation is where a document lay in the management repository. The
// server no longer stores it: a document response carries the value computed
// from archived and imported, and a put request may still name it (see
// DocumentArgs.ApplyLocation). The compatibility ends with the release after
// v0.9.x.
type DocumentLocation string

const (
	LocationHistory DocumentLocation = "history"
	LocationArchive DocumentLocation = "archive"
	// LocationLegacy is a document that was imported.
	LocationLegacy DocumentLocation = "legacy"
)

// DocumentLocations lists every location.
var DocumentLocations = []DocumentLocation{LocationHistory, LocationArchive, LocationLegacy}

// ParseDocumentKind returns the document kind named v.
func ParseDocumentKind(v string) (DocumentKind, bool) { return parseEnum(v, DocumentKinds) }

// ParseDocumentLocation returns the document location named v.
func ParseDocumentLocation(v string) (DocumentLocation, bool) {
	return parseEnum(v, DocumentLocations)
}

// DocumentState is what a document is worth for a decision now. It is computed
// from the archive mark and the relations, never stored.
type DocumentState string

const (
	StateCurrent    DocumentState = "current"
	StateSuperseded DocumentState = "superseded"
	StateArchived   DocumentState = "archived"
)

// DocumentStates lists every state.
var DocumentStates = []DocumentState{StateCurrent, StateSuperseded, StateArchived}

// ParseDocumentState returns the document state named v.
func ParseDocumentState(v string) (DocumentState, bool) { return parseEnum(v, DocumentStates) }

// RelationKind is the kind of a document relation.
type RelationKind string

const (
	// RelSupersedes replaces the target document as a whole.
	RelSupersedes RelationKind = "supersedes"
	// RelRelated points at a document that is related, or partly replaced.
	RelRelated RelationKind = "related"
	// RelAbout points at an issue the document is about.
	RelAbout RelationKind = "about"
)

// RelationKinds lists every relation kind.
var RelationKinds = []RelationKind{RelSupersedes, RelRelated, RelAbout}

// ParseRelationKind returns the relation kind named v.
func ParseRelationKind(v string) (RelationKind, bool) { return parseEnum(v, RelationKinds) }

// MaxDocumentBytes is the largest content of a record document.
const MaxDocumentBytes = 256 * 1024

// MaxDocumentPathRunes is the longest path of a record document.
const MaxDocumentPathRunes = 500

// UnresolvedRef is a relation whose target is not a document or an issue that
// exists. Target is the text as it was given.
type UnresolvedRef struct {
	Kind   RelationKind `json:"kind"`
	Target string       `json:"target"`
}

// DocumentWarning is a problem of a write that does not refuse it.
type DocumentWarning struct {
	Field   string `json:"field"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// Document is a record document: the original markdown of a file, kept as it
// was uploaded. Content is set only when a single document is read and Snippet
// only by a search with a query.
//
// State is computed. Supersedes and Related name documents of the same project
// by their name and documents of another project as "PROJECT/name"; Issues are
// issue keys. A relation that does not resolve is in UnresolvedRefs only.
type Document struct {
	Project        string          `json:"project"`
	Name           string          `json:"name"`
	Kind           DocumentKind    `json:"kind"`
	Date           *string         `json:"date"`
	Title          *string         `json:"title"`
	Tags           []string        `json:"tags"`
	State          DocumentState   `json:"state"`
	Archived       bool            `json:"archived"`
	Imported       bool            `json:"imported"`
	Path           *string         `json:"path"`
	Supersedes     []string        `json:"supersedes"`
	SupersededBy   []string        `json:"superseded_by"`
	Related        []string        `json:"related"`
	Issues         []string        `json:"issues"`
	UnresolvedRefs []UnresolvedRef `json:"unresolved_refs"`
	SHA256         string          `json:"sha256"`
	Size           int             `json:"size"`
	Content        *string         `json:"content,omitempty"`
	CreatedAt      Time            `json:"created_at"`
	CreatedBy      string          `json:"created_by"`
	UpdatedAt      Time            `json:"updated_at"`
	UpdatedBy      string          `json:"updated_by"`
	// Location is computed for clients of v0.8.x; see DocumentLocation.
	Location DocumentLocation `json:"location"`
	Snippet  *string          `json:"snippet,omitempty"`
}

// DocumentResult is the answer to a write: the document without its content and
// the warnings the write raised.
type DocumentResult struct {
	Document
	Warnings []DocumentWarning `json:"warnings"`
}

// DocumentActivity is one change of the archive mark or the relations of a
// document. Changes maps a field to its {from, to}.
type DocumentActivity struct {
	ID         int64             `json:"id"`
	Project    string            `json:"project"`
	Document   string            `json:"document"`
	Actor      string            `json:"actor"`
	SessionID  string            `json:"session_id"`
	ClientHost string            `json:"client_host"`
	At         Time              `json:"at"`
	Changes    map[string]Change `json:"changes"`
}

// documentNameRE is the file name yyyy-mm-dd-HHMM-<kind>-<slug>.md. The kind is
// checked against DocumentKinds.
var documentNameRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-\d{4}-([a-z]+)-[a-z0-9-]+\.md$`)

// ParseDocumentName returns the kind a record document file name carries, or
// false when the name does not follow the pattern.
func ParseDocumentName(name string) (DocumentKind, bool) {
	m := documentNameRE.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return parseEnum(m[1], recordKinds)
}

// ValidDocumentPath reports whether every "/" separated segment of p is
// non-empty, does not start with "." and holds no backslash or control
// character.
func ValidDocumentPath(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == "" || s[0] == '.' || strings.ContainsAny(s, "\\") || strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return false
		}
	}
	return true
}

// ValidImportedName reports whether name can name an imported document: a
// valid path of a markdown file that is no template, so its file name neither
// starts with _ nor ends in .example.md.
func ValidImportedName(name string) bool {
	file := name[strings.LastIndex(name, "/")+1:]
	return ValidDocumentPath(name) && strings.HasSuffix(file, ".md") && !strings.HasPrefix(file, "_") && !strings.HasSuffix(file, ".example.md")
}

// nameDate is the date at the start of a file name: yyyy-mm-dd, optionally
// followed by the time as HH-MM or HHMM.
var nameDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:-(\d{2})-?(\d{2})-)?`)

// dateFromName reads the date, and when the name carries one the time, from the
// start of the file name of name. It is nil when the name starts with no date.
func dateFromName(name string) *string {
	file := name[strings.LastIndex(name, "/")+1:]
	m := nameDate.FindStringSubmatch(file)
	if m == nil {
		return nil
	}
	d := m[1]
	if m[2] != "" {
		d += " " + m[2] + ":" + m[3]
	}
	return &d
}

// firstHeading is the text of the first "# " line of content.
func firstHeading(content string) *string {
	for _, line := range strings.Split(strings.TrimPrefix(content, "\xef\xbb\xbf"), "\n") {
		if t, ok := strings.CutPrefix(strings.TrimRight(line, " \t\r"), "# "); ok {
			return nonEmpty(strings.TrimSpace(t))
		}
	}
	return nil
}

// FrontMatter is what the server reads from the front matter of a record
// document. What the front matter does not hold is empty (Kind) or nil. A key
// with an empty value gives an empty, non-nil list.
type FrontMatter struct {
	Kind       string
	Date       *string
	Title      *string
	Tags       []string
	Supersedes []string
	Related    []string
	Issues     []string
}

// ParseFrontMatter reads the YAML front matter at the start of content: a
// "---" line, "key: value" lines and a closing "---" line. It understands the
// keys kind, date, title, tags, supersedes, related, issue and issues, with
// plain or quoted single-line values. Tags are a flow list ("[a, b]"); the
// relations are one value or a flow list. Comments, other keys and indented
// lines are skipped. ok is false when content does not start with a closed
// block.
func ParseFrontMatter(content string) (meta FrontMatter, ok bool) {
	entries, ok := FrontMatterEntries(content)
	for _, en := range entries {
		switch en.Key {
		case "kind":
			meta.Kind = en.Value
		case "date":
			meta.Date = nonEmpty(en.Value)
		case "title":
			meta.Title = nonEmpty(en.Value)
		case "tags":
			meta.Tags = flowList(en.Value)
		case "supersedes":
			meta.Supersedes = valueList(en.Value)
		case "related":
			meta.Related = valueList(en.Value)
		case "issue", "issues":
			if meta.Issues == nil {
				meta.Issues = []string{}
			}
			meta.Issues = append(meta.Issues, valueList(en.Value)...)
		}
	}
	return meta, ok
}

// valueList reads a front matter value that is one value or a flow list. An
// empty value is an empty list.
func valueList(value string) []string {
	v := strings.TrimSpace(value)
	switch {
	case len(v) >= 2 && v[0] == '[' && v[len(v)-1] == ']':
		return flowList(v)
	case v == "":
		return []string{}
	}
	return []string{v}
}

// FrontMatterEntry is one top-level "key: value" line of a front matter block.
// Value is read like a scalar: unquoted, without a trailing comment.
type FrontMatterEntry struct {
	Key, Value string
}

// FrontMatterEntries lists the top-level "key: value" lines of the front matter
// at the start of content, in order, whatever their keys. Comments, indented
// lines and lines without a colon are skipped. ok is false when content does
// not start with a closed block.
func FrontMatterEntries(content string) (entries []FrontMatterEntry, ok bool) {
	return frontMatterEntries(content, yamlScalar)
}

// FrontMatterRawEntries lists the lines of FrontMatterEntries with the value as
// written: quotes stay and so does any text after a closing quote, and only the
// space around the value and a trailing comment (" #") are dropped. A reader
// that has to tell a name from a sentence about it needs this form, since
// yamlScalar keeps just the quoted part.
func FrontMatterRawEntries(content string) (entries []FrontMatterEntry, ok bool) {
	return frontMatterEntries(content, rawScalar)
}

func frontMatterEntries(content string, read func(string) string) (entries []FrontMatterEntry, ok bool) {
	first, rest, found := strings.Cut(content, "\n")
	if !found || !isFence(first) {
		return nil, false
	}
	for more := true; more; {
		var line string
		line, rest, more = strings.Cut(rest, "\n")
		if isFence(line) {
			return entries, true
		}
		key, value, hasValue := strings.Cut(line, ":")
		if !hasValue || key == "" || key[0] == ' ' || key[0] == '\t' || key[0] == '#' {
			continue
		}
		entries = append(entries, FrontMatterEntry{strings.TrimRight(key, " \t"), read(value)})
	}
	return nil, false
}

// rawScalar is the text of a single-line value without the space around it and
// without the comment (" #") that ends it.
func rawScalar(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "#") {
		return ""
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// IsPlaceholder reports whether a front matter value is a template
// placeholder: text that starts with "<" and ends with ">", or a flow list
// with such an item.
func IsPlaceholder(value string) bool {
	v := yamlScalar(value)
	if len(v) >= 2 && v[0] == '[' && v[len(v)-1] == ']' {
		for _, item := range strings.Split(v[1:len(v)-1], ",") {
			if isPlaceholderScalar(yamlScalar(item)) {
				return true
			}
		}
		return false
	}
	return isPlaceholderScalar(v)
}

func isPlaceholderScalar(s string) bool {
	return len(s) >= 2 && s[0] == '<' && s[len(s)-1] == '>'
}

// htmlComment matches an HTML comment, which may span lines.
var htmlComment = regexp.MustCompile(`(?s)<!--(.*?)-->`)

func isFence(line string) bool { return strings.TrimRight(line, " \t\r") == "---" }

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// yamlScalar reads a single-line scalar: a quoted string, or a plain string
// that a comment (" #") ends. In a double quoted string only a backslash before
// a double quote or a backslash is an escape; in a single quoted string only a
// doubled single quote is.
func yamlScalar(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		if s, ok := unquote(v); ok {
			return s
		}
	}
	if strings.HasPrefix(v, "#") {
		return ""
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// unquote reads the quoted string at the start of v. Whatever follows the
// closing quote is ignored.
func unquote(v string) (string, bool) {
	q := v[0]
	var b strings.Builder
	for i := 1; i < len(v); i++ {
		c := v[i]
		switch {
		case q == '"' && c == '\\' && i+1 < len(v) && (v[i+1] == '"' || v[i+1] == '\\'):
			i++
			b.WriteByte(v[i])
		case q == '\'' && c == '\'' && i+1 < len(v) && v[i+1] == '\'':
			i++
			b.WriteByte('\'')
		case c == q:
			return b.String(), true
		default:
			b.WriteByte(c)
		}
	}
	return "", false
}

// flowList reads a "[a, b]" list of scalars. Any other form yields no items.
func flowList(value string) []string {
	v := strings.TrimSpace(value)
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	items := []string{}
	if len(v) < 2 || v[0] != '[' || v[len(v)-1] != ']' {
		return items
	}
	for _, item := range strings.Split(v[1:len(v)-1], ",") {
		if s := yamlScalar(item); s != "" {
			items = append(items, s)
		}
	}
	return items
}

// DocumentArgs is the metadata a put request carries. A nil field, and a nil
// list, was not sent and leaves the value to the front matter and the
// fallbacks. An empty list was sent and counts as a value.
type DocumentArgs struct {
	Kind       *string
	Date       *string
	Title      *string
	Path       *string
	Tags       []string
	Supersedes []string
	Related    []string
	Issues     []string
	Archived   *bool
	Imported   *bool
}

// HasMetadata reports whether the request carries any metadata.
func (a DocumentArgs) HasMetadata() bool {
	return a.Kind != nil || a.Date != nil || a.Title != nil || a.Path != nil || a.Tags != nil || a.Supersedes != nil ||
		a.Related != nil || a.Issues != nil || a.Archived != nil || a.Imported != nil
}

// ApplyLocation reads the location of a v0.8.x client as the metadata it stands
// for: archive as archived, legacy as imported. A legacy name that starts with
// archive/ is archived too. It sets only what the request left unset, and only
// when neither archived nor imported was sent.
func (a *DocumentArgs) ApplyLocation(name string, loc DocumentLocation) {
	if a.Archived != nil || a.Imported != nil {
		return
	}
	t := true
	switch loc {
	case LocationArchive:
		a.Archived = &t
	case LocationLegacy:
		a.Imported = &t
		if strings.HasPrefix(name, "archive/") {
			a.Archived = &t
		}
	}
}

// DocumentMeta is the metadata of a record document after the arguments, the
// front matter and the fallbacks have decided each field.
type DocumentMeta struct {
	Kind       DocumentKind
	Date       *string
	Title      *string
	Path       *string
	Tags       []string
	Supersedes []string
	Related    []string
	Issues     []string
	Archived   bool
	Imported   bool
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}( \d{2}:\d{2})?$`)

func validDocumentDate(s string) bool {
	layout := "2006-01-02"
	if len(s) > len(layout) {
		layout += " 15:04"
	}
	_, err := time.Parse(layout, s)
	return dateRE.MatchString(s) && err == nil
}

// IsDocumentName reports whether s is a document name: one word that ends
// in .md.
func IsDocumentName(s string) bool {
	return strings.HasSuffix(s, ".md") && !strings.ContainsAny(s, " \t\r\n")
}

// ValidateDocument decides the metadata of a record document and checks it.
// Each field takes the argument, else the front matter, else the fallback of
// the kind of document; archived, imported and path never come from the front
// matter. A document is imported when the arguments say so. Every violation is
// reported at once. Whether the relation targets exist is for the store to
// check.
func ValidateDocument(name, content string, args DocumentArgs) (DocumentMeta, error) {
	var e ValidationError
	meta := DocumentMeta{Tags: []string{}, Supersedes: []string{}, Related: []string{}, Issues: []string{}}
	meta.Imported = args.Imported != nil && *args.Imported
	meta.Archived = args.Archived != nil && *args.Archived
	imported := meta.Imported

	if len(content) > MaxDocumentBytes {
		e.add("content", "%d바이트를 넘을 수 없습니다.", MaxDocumentBytes)
	}
	fm, _ := ParseFrontMatter(content)

	nameKind, nameOK := ParseDocumentName(name)
	switch {
	case imported && !ValidImportedName(name):
		e.add("name", "/로 나눈 조각이 비지 않고 .으로 시작하지 않는 .md 파일의 상대 경로여야 합니다. _로 시작하거나 .example.md로 끝나는 서식은 받지 않습니다.")
	case !imported && !nameOK:
		e.add("name", "yyyy-mm-dd-HHMM-<종류>-<slug>.md 형식이어야 합니다. 종류는 %s 중 하나이고 slug는 영문 소문자, 숫자, 하이픈입니다. 이 형식이 아닌 문서는 imported로 올립니다.", joinEnum(recordKinds))
	}

	if args.Path != nil {
		if n := utf8.RuneCountInString(*args.Path); n < 1 || n > MaxDocumentPathRunes || !ValidDocumentPath(*args.Path) {
			e.add("path", "1~%d자이고 /로 나눈 조각이 비지 않고 .으로 시작하지 않아야 하며 역슬래시와 제어 문자를 담을 수 없습니다.", MaxDocumentPathRunes)
		} else {
			meta.Path = args.Path
		}
	}

	// kind
	kindText := ""
	switch {
	case args.Kind != nil:
		kindText = *args.Kind
	case fm.Kind != "":
		kindText = fm.Kind
	case imported:
		kindText = string(DocumentOther)
	case nameOK:
		kindText = string(nameKind)
	}
	switch kind, ok := ParseDocumentKind(kindText); {
	case !imported && IsPlaceholder(kindText):
		e.addReason("kind", "placeholder", "값이 서식의 자리표시자(<...>)입니다. 실제 값으로 채워야 합니다.")
	case kindText == "":
		// Only a name that is no record name leaves the kind open; the name is already reported.
	case !ok:
		e.add("kind", "%s 중 하나여야 합니다.", joinEnum(DocumentKinds))
	case !imported && nameOK && kind != nameKind:
		e.add("kind", "이름의 종류(%s)와 같아야 합니다. 받은 값은 %q입니다.", nameKind, kindText)
	case !imported && kind == DocumentOther:
		e.add("kind", "other는 imported 문서에만 쓸 수 있습니다.")
	default:
		meta.Kind = kind
	}

	// date and title
	date := firstNonNil(args.Date, fm.Date)
	title := firstNonNil(args.Title, fm.Title)
	if date == nil {
		if d := dateFromName(name); d != nil && validDocumentDate(*d) {
			date = d
		}
	} else {
		switch {
		case !imported && IsPlaceholder(*date):
			e.addReason("date", "placeholder", "값이 서식의 자리표시자(<...>)입니다. 실제 값으로 채워야 합니다.")
		case !validDocumentDate(*date):
			e.add("date", "yyyy-mm-dd 또는 yyyy-mm-dd HH:MM 형식이어야 합니다. 받은 값은 %q입니다.", *date)
		}
	}
	if title == nil && imported {
		title = firstHeading(content)
	}
	switch {
	case title == nil || strings.TrimSpace(*title) == "":
		title = nil
		if !imported {
			e.addReason("title", "required", "title이 필요합니다. 인자로 넘기거나 프런트매터에 적습니다.")
		}
	case !imported && IsPlaceholder(*title):
		e.addReason("title", "placeholder", "값이 서식의 자리표시자(<...>)입니다. 실제 값으로 채워야 합니다.")
	}
	meta.Date, meta.Title = date, title

	// tags and relations
	if tags := firstList(args.Tags, fm.Tags); tags != nil {
		meta.Tags = tags
	}
	for i, t := range meta.Tags {
		if !imported && isPlaceholderScalar(strings.TrimSpace(t)) {
			e.addReason(fmt.Sprintf("tags[%d]", i), "placeholder", "값이 서식의 자리표시자(<...>)입니다. 실제 값으로 채워야 합니다.")
		}
	}
	for _, rel := range []struct {
		field string
		args  []string
		fm    []string
		dst   *[]string
	}{
		{"supersedes", args.Supersedes, fm.Supersedes, &meta.Supersedes},
		{"related", args.Related, fm.Related, &meta.Related},
		{"issues", args.Issues, fm.Issues, &meta.Issues},
	} {
		if v := firstList(rel.args, rel.fm); v != nil {
			if rel.field != "issues" {
				v = withoutNoRelation(v)
			}
			*rel.dst = v
		}
		for i, t := range *rel.dst {
			field := fmt.Sprintf("%s[%d]", rel.field, i)
			switch {
			case imported:
			case isPlaceholderScalar(strings.TrimSpace(t)):
				e.addReason(field, "placeholder", "값이 서식의 자리표시자(<...>)입니다. 실제 값으로 채워야 합니다.")
			case rel.field != "issues" && !IsDocumentName(t):
				hint := ""
				if rel.field == "supersedes" {
					hint = " 문서의 일부만 대체하면 related로 씁니다."
				}
				e.addReason(field, "not_a_name", "문서 이름(.md로 끝나는 한 단어)이어야 합니다. 받은 값은 %q입니다.%s", t, hint)
			}
		}
	}
	return meta, e.orNil()
}

// isNoRelation reports whether a supersedes or related value says there is no
// relation: an empty value, or none in lower case.
func isNoRelation(s string) bool {
	v := strings.TrimSpace(s)
	return v == "" || v == "none"
}

// withoutNoRelation drops the values that say there is no relation.
func withoutNoRelation(items []string) []string {
	out := make([]string, 0, len(items))
	for _, t := range items {
		if !isNoRelation(t) {
			out = append(out, t)
		}
	}
	return out
}

func firstNonNil(vals ...*string) *string {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// firstList returns the first list that is not nil.
func firstList(lists ...[]string) []string {
	for _, l := range lists {
		if l != nil {
			return l
		}
	}
	return nil
}
