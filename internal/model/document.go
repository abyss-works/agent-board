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
)

// DocumentKinds lists every record document kind.
var DocumentKinds = []DocumentKind{DocumentDecision, DocumentIncident, DocumentReport, DocumentImplementation}

// DocumentLocation is the folder of the management repository that holds a
// record document.
type DocumentLocation string

const (
	LocationHistory DocumentLocation = "history"
	LocationArchive DocumentLocation = "archive"
)

// DocumentLocations lists every folder a record document may lie in.
var DocumentLocations = []DocumentLocation{LocationHistory, LocationArchive}

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
	return ParseDocumentKind(m[1])
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
func ValidateDocument(name, location, content string) (DocumentMeta, error) {
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
