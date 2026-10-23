package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

const maxBodyBytes = 1 << 20

// body is a decoded JSON object whose keys were checked against the
// allowed set. Values are decoded lazily so type errors name the field.
type body map[string]json.RawMessage

// readBody decodes the request body as a JSON object and rejects unknown
// fields with 400. allowed maps field names to true.
func readBody(w http.ResponseWriter, r *http.Request, allowed map[string]bool) (body, *apiError) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, errPayloadTooLarge
		}
		return nil, badRequest("요청 본문을 읽지 못했습니다.")
	}
	var b body
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, badRequest("요청 본문이 JSON 객체가 아닙니다.", Detail{Message: err.Error()})
	}
	if b == nil {
		return nil, badRequest("요청 본문이 JSON 객체가 아닙니다.")
	}
	var unknown []string
	for k := range b {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		details := make([]Detail, len(unknown))
		for i, k := range unknown {
			details[i] = Detail{Field: k, Reason: "unknown_field", Message: "모르는 필드입니다."}
		}
		return nil, badRequest("요청 본문에 모르는 필드가 있습니다.", details...)
	}
	return b, nil
}

func isNull(raw json.RawMessage) bool { return string(raw) == "null" }

// field decodes b[name] into dst when present and not null. It reports
// whether the key was present (null counts as present) and a 400 on type
// mismatch. A null value for a non-nullable field is left to validation by
// the caller, which sees present=true and an untouched dst.
func field[T any](b body, name string, dst *T) (present, null bool, err *apiError) {
	raw, ok := b[name]
	if !ok {
		return false, false, nil
	}
	if isNull(raw) {
		return true, true, nil
	}
	if e := json.Unmarshal(raw, dst); e != nil {
		return true, false, badRequest("필드 타입이 맞지 않습니다.", Detail{Field: name, Reason: "type_mismatch", Message: e.Error()})
	}
	return true, false, nil
}

// query parses and validates URL query parameters, collecting problems.
type query struct {
	values   map[string][]string
	repeatOK map[string]bool
	seen     map[string]bool
	details  []Detail
}

func newQuery(r *http.Request, repeatOK ...string) *query {
	q := &query{values: r.URL.Query(), repeatOK: map[string]bool{}, seen: map[string]bool{}}
	for _, k := range repeatOK {
		q.repeatOK[k] = true
	}
	return q
}

func (q *query) add(name, msg string) {
	q.details = append(q.details, Detail{Field: name, Reason: "invalid_query", Message: msg})
}

// one returns a single-valued parameter, or "" when absent.
func (q *query) one(name string) string {
	q.seen[name] = true
	vals := q.values[name]
	if len(vals) > 1 && !q.repeatOK[name] {
		q.add(name, "이 매개변수는 반복할 수 없습니다.")
	}
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// csv returns a comma separated parameter split into values, or nil.
func (q *query) csv(name string) []string {
	v := q.one(name)
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// repeated returns every value of a parameter that may repeat.
func (q *query) repeated(name string) []string {
	q.seen[name] = true
	return q.values[name]
}

func (q *query) intRange(name string, def, lo, hi int) int {
	v := q.one(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		q.add(name, fmt.Sprintf("%d~%d 사이의 정수여야 합니다.", lo, hi))
		return def
	}
	return n
}

func (q *query) limit(def, max int) int { return q.intRange("limit", def, 1, max) }
func (q *query) offset() int            { return q.intRange("offset", 0, 0, 1<<31-1) }

// sorts parses "field,-field" against the allowed fields.
func (q *query) sorts(allowed ...string) []store.Sort {
	var out []store.Sort
	for _, s := range q.csv("sort") {
		desc := strings.HasPrefix(s, "-")
		f := strings.TrimPrefix(s, "-")
		ok := false
		for _, a := range allowed {
			if a == f {
				ok = true
			}
		}
		if !ok {
			q.add("sort", "정렬 필드는 "+strings.Join(allowed, ", ")+" 중 하나여야 합니다.")
			continue
		}
		out = append(out, store.Sort{Field: f, Desc: desc})
	}
	return out
}

func (q *query) time(name string) *model.Time {
	v := q.one(name)
	if v == "" {
		return nil
	}
	t, err := model.ParseTime(v)
	if err != nil {
		q.add(name, "RFC 3339 시각이어야 합니다.")
		return nil
	}
	return &t
}

// finish rejects unknown parameters and returns the collected problems.
func (q *query) finish() *apiError {
	var unknown []string
	for k := range q.values {
		if !q.seen[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		q.add(k, "모르는 질의 매개변수입니다.")
	}
	if len(q.details) == 0 {
		return nil
	}
	return badRequest("질의 매개변수가 규칙에 맞지 않습니다.", q.details...)
}

func parseEnumList[T ~string](q *query, name string, parse func(string) (T, bool)) []T {
	var out []T
	for _, v := range q.csv(name) {
		e, ok := parse(v)
		if !ok {
			q.add(name, fmt.Sprintf("%q는 허용되지 않는 값입니다.", v))
			continue
		}
		out = append(out, e)
	}
	return out
}

// ifMatch parses an optional If-Match header of the form "<version>".
func ifMatch(r *http.Request) (*int64, *apiError) {
	v := r.Header.Get("If-Match")
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(strings.Trim(v, `"`), 10, 64)
	if err != nil || !strings.HasPrefix(v, `"`) || !strings.HasSuffix(v, `"`) {
		return nil, badRequest("If-Match는 \"<version>\" 형식이어야 합니다.", Detail{Field: "If-Match", Reason: "invalid_header"})
	}
	return &n, nil
}

func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// storeError maps store and model errors to API errors.
func (h *handler) storeError(r *http.Request, err error) *apiError {
	var verr *model.ValidationError
	var rerr *store.RefError
	var ierr *store.ImmutableError
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("대상이 없습니다.")
	case errors.Is(err, store.ErrDuplicateKey):
		return conflict("duplicate_key", "같은 키가 이미 있습니다.")
	case errors.Is(err, store.ErrProjectNotActive):
		return conflict("project_not_active", "활성 상태가 아닌 프로젝트의 이슈, 주제, 마일스톤, 기록 문서는 쓸 수 없습니다.")
	case errors.As(err, &ierr):
		return &apiError{Status: 409, Code: "conflict", Message: "같은 이름의 기록 문서가 다른 내용으로 이미 있습니다. 기록 문서는 고칠 수 없습니다.",
			Details: []Detail{{Field: "content", Reason: "immutable", Message: "서버에 저장된 내용의 sha256은 " + ierr.SHA256 + "입니다.", SHA256: ierr.SHA256}}}
	case errors.Is(err, store.ErrDuplicateName):
		return &apiError{Status: 409, Code: "conflict", Message: "같은 상위 주제 아래에 같은 이름의 활성 주제가 이미 있습니다.",
			Details: []Detail{{Field: "name", Reason: "duplicate"}}}
	case errors.Is(err, store.ErrDuplicateMilestoneName):
		return &apiError{Status: 409, Code: "conflict", Message: "같은 이름의 삭제되지 않은 마일스톤이 이미 있습니다.",
			Details: []Detail{{Field: "name", Reason: "duplicate"}}}
	case errors.Is(err, store.ErrTopicHasChildren):
		return conflict("topic_has_children", "활성 하위 주제가 있는 대주제는 삭제할 수 없습니다.")
	case errors.Is(err, store.ErrCycle):
		return conflict("cycle", "상위 이슈나 선행 이슈가 순환합니다.")
	case errors.Is(err, store.ErrVersionMismatch):
		return errPreconditionFailed
	case errors.As(err, &rerr):
		return validationFailed([]Detail{{Field: rerr.Field, Reason: "not_found", Message: "이슈 " + rerr.Key + "가 없습니다."}})
	case errors.As(err, &verr):
		details := make([]Detail, len(verr.Fields))
		for i, f := range verr.Fields {
			details[i] = Detail{Field: f.Field, Message: f.Message}
		}
		return validationFailed(details)
	}
	h.opts.Logger.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	return errInternal
}

func envelope(items any, total, limit, offset int) map[string]any {
	return map[string]any{"items": items, "total": total, "limit": limit, "offset": offset}
}
