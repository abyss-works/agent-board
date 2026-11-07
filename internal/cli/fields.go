package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// pathTree is the set of --fields paths as a tree. A node that ends a path
// keeps the whole value, whatever longer paths say below it.
type pathTree struct {
	names []string
	kids  map[string]*pathTree
	whole bool
}

func newPathTree(paths [][]string) *pathTree {
	root := &pathTree{kids: map[string]*pathTree{}}
	for _, p := range paths {
		node := root
		for _, seg := range p {
			kid, ok := node.kids[seg]
			if !ok {
				kid = &pathTree{kids: map[string]*pathTree{}}
				node.kids[seg] = kid
				node.names = append(node.names, seg)
			}
			node = kid
		}
		node.whole = true
	}
	return root
}

// parsePaths splits the --fields value into dot paths.
func parsePaths(spec string) [][]string {
	var out [][]string
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, strings.Split(name, "."))
	}
	return out
}

// project keeps the parts of raw that tree names. Objects keep the named keys
// in the order of the tree, arrays project each element, and a path that
// does not exist is left out. ok is false when nothing of raw is kept.
func project(raw json.RawMessage, tree *pathTree) (out json.RawMessage, ok bool) {
	if tree.whole || len(tree.names) == 0 {
		return raw, true
	}
	switch firstByte(raw) {
	case '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return nil, false
		}
		var b bytes.Buffer
		b.WriteByte('{')
		n := 0
		for _, name := range tree.names {
			v, found := obj[name]
			if !found {
				continue
			}
			pv, kept := project(v, tree.kids[name])
			if !kept {
				continue
			}
			if n > 0 {
				b.WriteByte(',')
			}
			k, _ := json.Marshal(name)
			b.Write(k)
			b.WriteByte(':')
			b.Write(pv)
			n++
		}
		b.WriteByte('}')
		return b.Bytes(), true
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil {
			return nil, false
		}
		var b bytes.Buffer
		b.WriteByte('[')
		n := 0
		for _, el := range arr {
			pv, kept := project(el, tree)
			if !kept {
				continue
			}
			if n > 0 {
				b.WriteByte(',')
			}
			b.Write(pv)
			n++
		}
		b.WriteByte(']')
		return b.Bytes(), true
	}
	return nil, false
}

func firstByte(raw []byte) byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0
	}
	return raw[0]
}

// selectFields keeps only the named fields of a JSON object, or of each
// object in its items list for list responses, in the given order. A name may
// be a dot path into nested objects; arrays on the way are walked element by
// element.
func selectFields(body []byte, spec string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("--fields 는 JSON 객체 응답에만 씁니다: %w", err)
	}
	tree := newPathTree(parsePaths(spec))
	whole, _ := project(body, tree)
	raw, ok := obj["items"]
	if !ok {
		return whole, nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return whole, nil
	}
	picked := make([]json.RawMessage, len(items))
	for n := range items {
		el, _ := json.Marshal(items[n])
		picked[n], _ = project(el, tree)
	}
	obj["items"], _ = json.Marshal(picked)
	return json.Marshal(obj)
}

// textLines renders the response for --text: one line per row, the fields of
// a row separated by tabs.
//
// The rows are the elements of items for list responses. Otherwise, when
// every path starts with the same name and that name holds an array, the
// rows are its elements, and the paths are read inside them. Otherwise the
// response is one row.
//
// A field that reaches an array on its way holds the array of the values it
// found. An array or an object value is printed as compact JSON; null and
// a missing field are empty. Strings are escaped (\\, \t, \n, \r) so a row
// stays on one line. With one field and one row, the value is printed as it
// is: a string unescaped, an array one element per line.
func textLines(body []byte, spec string) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return "", fmt.Errorf("--fields 는 JSON 객체 응답에만 씁니다: %w", err)
	}
	paths := parsePaths(spec)
	var rows []json.RawMessage
	single := false
	if items, ok := arrayOf(obj["items"]); ok {
		rows = items
	} else if head, ok := commonHead(paths); ok {
		if els, ok := arrayOf(obj[head]); ok {
			rows = els
			stripped := make([][]string, len(paths))
			for i, p := range paths {
				stripped[i] = p[1:]
			}
			paths = stripped
		}
	}
	if rows == nil {
		rows, single = []json.RawMessage{body}, true
	}
	var b strings.Builder
	if single && len(paths) == 1 {
		for _, line := range rawLines(body, paths[0]) {
			b.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				b.WriteByte('\n')
			}
		}
		return b.String(), nil
	}
	for _, row := range rows {
		cells := make([]string, len(paths))
		for i, p := range paths {
			cells[i] = cellText(row, p)
		}
		b.WriteString(strings.Join(cells, "\t"))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func arrayOf(raw json.RawMessage) ([]json.RawMessage, bool) {
	if firstByte(raw) != '[' {
		return nil, false
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return nil, false
	}
	if arr == nil {
		arr = []json.RawMessage{}
	}
	return arr, true
}

// commonHead is the first name of the paths when every path has it and goes
// deeper.
func commonHead(paths [][]string) (string, bool) {
	if len(paths) == 0 {
		return "", false
	}
	for _, p := range paths {
		if len(p) < 2 || p[0] != paths[0][0] {
			return "", false
		}
	}
	return paths[0][0], true
}

// resolve follows path in raw. viaArray reports that an array was walked on
// the way, in which case vals holds one value per element that has the path.
func resolve(raw json.RawMessage, path []string) (vals []json.RawMessage, viaArray bool) {
	if len(path) == 0 {
		return []json.RawMessage{raw}, false
	}
	switch firstByte(raw) {
	case '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return nil, false
		}
		v, ok := obj[path[0]]
		if !ok {
			return nil, false
		}
		return resolve(v, path[1:])
	case '[':
		arr, _ := arrayOf(raw)
		for _, el := range arr {
			vs, _ := resolve(el, path)
			vals = append(vals, vs...)
		}
		return vals, true
	}
	return nil, false
}

// cellText is one field of a row on a line with other fields.
func cellText(row json.RawMessage, path []string) string {
	vals, viaArray := resolve(row, path)
	switch {
	case viaArray:
		joined := "[" + joinRaw(vals) + "]"
		return compact(joined)
	case len(vals) == 0:
		return ""
	}
	return scalarText(vals[0], true)
}

// rawLines is the single field of a single row: a string as it is, an array
// one element per line, a missing field nothing.
func rawLines(row json.RawMessage, path []string) []string {
	vals, viaArray := resolve(row, path)
	if !viaArray && len(vals) == 1 && firstByte(vals[0]) == '[' {
		vals, _ = arrayOf(vals[0])
	}
	lines := make([]string, len(vals))
	for i, v := range vals {
		lines[i] = scalarText(v, false)
	}
	return lines
}

func joinRaw(vals []json.RawMessage) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = string(v)
	}
	return strings.Join(parts, ",")
}

func compact(s string) string {
	var b bytes.Buffer
	if json.Compact(&b, []byte(s)) != nil {
		return s
	}
	return b.String()
}

var textEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

// scalarText prints a JSON value as text: a string as its content, null as
// nothing, and anything else as compact JSON.
func scalarText(raw json.RawMessage, escape bool) string {
	switch firstByte(raw) {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return compact(string(raw))
		}
		if escape {
			return textEscaper.Replace(s)
		}
		return s
	case 'n':
		return ""
	}
	return compact(string(raw))
}
