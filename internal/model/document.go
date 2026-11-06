package model

import (
	"regexp"
	"strings"
)

// DocumentKind is the kind of a record document. The file name and the front
// matter both name it.
type DocumentKind string

const (
	DocumentDecision       DocumentKind = "decision"
	DocumentIncident       DocumentKind = "incident"
	DocumentReport         DocumentKind = "report"
	DocumentImplementation DocumentKind = "implementation"
	// DocumentOther is a legacy document whose kind neither its folder nor its
	// front matter tells. A record document name never carries it.
	DocumentOther DocumentKind = "other"
)

// recordKinds are the kinds a record document file name may carry.
var recordKinds = []DocumentKind{DocumentDecision, DocumentIncident, DocumentReport, DocumentImplementation}

// DocumentKinds lists every document kind.
var DocumentKinds = append(append([]DocumentKind{}, recordKinds...), DocumentOther)

// DocumentLocation is where a document lies in the management repository.
type DocumentLocation string

const (
	LocationHistory DocumentLocation = "history"
	LocationArchive DocumentLocation = "archive"
	// LocationLegacy holds the documents of the earlier record system: the
	// decisions, reports and incidents folders and the subfolders of archive.
	// Their name is the path relative to the repository root.
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

// MaxDocumentBytes is the largest content of a record document.
const MaxDocumentBytes = 256 * 1024

// Document is a record document: the original markdown of a file of the
// management repository, kept as it was committed. Content is set only when a
// single document is read and Snippet only by a search with a query.
type Document struct {
	Project   string           `json:"project"`
	Name      string           `json:"name"`
	Location  DocumentLocation `json:"location"`
	Kind      DocumentKind     `json:"kind"`
	Date      *string          `json:"date"`
	Title     *string          `json:"title"`
	Tags      []string         `json:"tags"`
	SHA256    string           `json:"sha256"`
	Size      int              `json:"size"`
	Content   *string          `json:"content,omitempty"`
	CreatedAt Time             `json:"created_at"`
	CreatedBy string           `json:"created_by"`
	UpdatedAt Time             `json:"updated_at"`
	UpdatedBy string           `json:"updated_by"`
	Snippet   *string          `json:"snippet,omitempty"`
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

// legacyFolderKinds maps the folders of the earlier record system to the kind
// of their documents. The archive subfolders take the kind from the front
// matter instead.
var legacyFolderKinds = map[string]DocumentKind{
	"decisions": DocumentDecision,
	"reports":   DocumentReport,
	"incidents": DocumentIncident,
}

// ParseLegacyName reports whether name is the path of a legacy document:
// decisions/, reports/ or incidents/ followed by a file, or archive/ followed
// by at least one subfolder and a file. The file ends in .md and is not a
// template: its name neither starts with _ nor ends in .example.md. Segments
// are not empty, ".", ".." or hidden, and hold no backslash or control
// character. folder is the first segment.
func ParseLegacyName(name string) (folder string, ok bool) {
	segs := strings.Split(name, "/")
	file := segs[len(segs)-1]
	if !strings.HasSuffix(file, ".md") || strings.HasPrefix(file, "_") || strings.HasSuffix(file, ".example.md") {
		return "", false
	}
	for _, s := range segs {
		if s == "" || s[0] == '.' || strings.ContainsAny(s, "\\") || strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return "", false
		}
	}
	folder = segs[0]
	if _, ok := legacyFolderKinds[folder]; ok && len(segs) == 2 {
		return folder, true
	}
	if folder == string(LocationArchive) && len(segs) >= 3 {
		return folder, true
	}
	return "", false
}

// legacyNameDate is the date at the start of a legacy file name: yyyy-mm-dd,
// optionally followed by the time as HH-MM or HHMM.
var legacyNameDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:-(\d{2})-?(\d{2})-)?`)

// legacyMeta fills what a legacy document lacks. The kind comes from the
// folder, or for an archive subfolder from a known front matter kind, and is
// otherwise other. The date falls back to the one in the file name and the
// title to the first "# " heading.
func legacyMeta(folder, name, content string, meta DocumentMeta) DocumentMeta {
	if k, ok := legacyFolderKinds[folder]; ok {
		meta.Kind = string(k)
	} else if _, ok := ParseDocumentKind(meta.Kind); !ok || meta.Kind == string(DocumentOther) {
		meta.Kind = string(DocumentOther)
	}
	if meta.Date == nil {
		file := name[strings.LastIndex(name, "/")+1:]
		if m := legacyNameDate.FindStringSubmatch(file); m != nil {
			d := m[1]
			if m[2] != "" {
				d += " " + m[2] + ":" + m[3]
			}
			meta.Date = &d
		}
	}
	if meta.Title == nil {
		for _, line := range strings.Split(strings.TrimPrefix(content, "\xef\xbb\xbf"), "\n") {
			if t, ok := strings.CutPrefix(strings.TrimRight(line, " \t\r"), "# "); ok {
				meta.Title = nonEmpty(strings.TrimSpace(t))
				break
			}
		}
	}
	return meta
}

// DocumentMeta is what the server reads from the front matter of a record
// document. Date and Title are nil when the front matter holds no such value;
// Tags is empty, never nil.
type DocumentMeta struct {
	Kind  string
	Date  *string
	Title *string
	Tags  []string
}

// ParseFrontMatter reads the YAML front matter at the start of content: a
// "---" line, "key: value" lines and a closing "---" line. It understands the
// keys kind, date, title and tags, with plain or quoted single-line values and
// tags written as a flow list ("[a, b]"). Comments, other keys, indented lines
// and tags in any other form are skipped. ok is false when content does not
// start with a closed block.
func ParseFrontMatter(content string) (meta DocumentMeta, ok bool) {
	meta.Tags = []string{}
	first, rest, found := strings.Cut(content, "\n")
	if !found || !isFence(first) {
		return meta, false
	}
	for more := true; more; {
		var line string
		line, rest, more = strings.Cut(rest, "\n")
		if isFence(line) {
			return meta, true
		}
		key, value, hasValue := strings.Cut(line, ":")
		if !hasValue || key == "" || key[0] == ' ' || key[0] == '\t' || key[0] == '#' {
			continue
		}
		switch strings.TrimRight(key, " \t") {
		case "kind":
			meta.Kind = yamlScalar(value)
		case "date":
			meta.Date = nonEmpty(yamlScalar(value))
		case "title":
			meta.Title = nonEmpty(yamlScalar(value))
		case "tags":
			meta.Tags = flowList(value)
		}
	}
	return meta, false
}

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

// ValidateDocument checks the file name, the location and the content of a
// record document and returns what the front matter says. Every violation is
// reported at once. The front matter kind must equal the kind in the name.
// A legacy document is checked by validateLegacy instead.
func ValidateDocument(name, location, content string) (DocumentMeta, error) {
	if location == string(LocationLegacy) {
		return validateLegacy(name, content)
	}
	var e ValidationError
	kind, nameOK := ParseDocumentName(name)
	if !nameOK {
		e.add("name", "yyyy-mm-dd-HHMM-<종류>-<slug>.md 형식이어야 합니다. 종류는 %s 중 하나이고 slug는 영문 소문자, 숫자, 하이픈입니다.", joinEnum(DocumentKinds))
	}
	if _, ok := ParseDocumentLocation(location); !ok {
		e.add("location", "%s 중 하나여야 합니다.", joinEnum(DocumentLocations))
	}
	var meta DocumentMeta
	if len(content) > MaxDocumentBytes {
		e.add("content", "%d바이트를 넘을 수 없습니다.", MaxDocumentBytes)
	} else if m, ok := ParseFrontMatter(content); !ok {
		e.add("content", "맨 앞에 ---로 시작해 ---로 닫는 YAML 프런트매터가 있어야 합니다.")
	} else {
		meta = m
		if nameOK && m.Kind != string(kind) {
			e.add("content", "프런트매터의 kind(%q)가 파일명의 종류(%s)와 같아야 합니다.", m.Kind, kind)
		}
	}
	return meta, e.orNil()
}

// validateLegacy checks a legacy document: its name is a legacy path and its
// content fits the size limit. The front matter is optional because the
// earlier record system did not require it.
func validateLegacy(name, content string) (DocumentMeta, error) {
	var e ValidationError
	folder, ok := ParseLegacyName(name)
	if !ok {
		e.add("name", "이전 체계 문서는 decisions/, reports/, incidents/ 바로 아래나 archive/ 하위 폴더에 있는 *.md 의 상대 경로여야 합니다.")
	}
	if len(content) > MaxDocumentBytes {
		e.add("content", "%d바이트를 넘을 수 없습니다.", MaxDocumentBytes)
	}
	if err := e.orNil(); err != nil {
		return DocumentMeta{}, err
	}
	meta, ok := ParseFrontMatter(content)
	if !ok {
		meta = DocumentMeta{Tags: []string{}}
	}
	return legacyMeta(folder, name, content, meta), nil
}
