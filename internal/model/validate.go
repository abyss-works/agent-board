package model

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits on field sizes.
const (
	MaxTitleRunes       = 200
	MaxNameRunes        = 100
	MaxBodyBytes        = 64 * 1024
	MaxLabels           = 20
	MaxDependsOn        = 20
	MaxRefs             = 50
	MaxRecheckRunes     = 500
	MaxSearchTerms      = 10
	DefaultIssueType    = TypeFeature
	DefaultIssueStatus  = StatusBacklog
	DefaultPriority     = P2
	DefaultProjectState = ProjectActive
)

// FieldError is one rule violation located by field path.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError collects every violation found in one object so the
// client can fix them all at once.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Message
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) add(field, format string, args ...any) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

func joinEnum[T ~string](all []T) string {
	parts := make([]string, len(all))
	for i, a := range all {
		parts[i] = string(a)
	}
	return strings.Join(parts, ", ")
}

// NormalizeIssue removes duplicates from and sorts the array fields, and
// replaces nil arrays with empty ones so JSON shows [] rather than null.
func NormalizeIssue(i *Issue) {
	i.Labels = uniqueSorted(i.Labels)
	i.DependsOn = uniqueSorted(i.DependsOn)
	i.Refs = uniqueSorted(i.Refs)
}

func uniqueSorted(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// ValidateIssue checks every field rule of an issue after defaults and the
// merge patch have been applied. Referential rules (existence, cycles,
// project state) are the store's job.
func ValidateIssue(i *Issue) error {
	var e ValidationError
	if n := utf8.RuneCountInString(i.Title); n < 1 || n > MaxTitleRunes {
		e.add("title", "1~%d자여야 합니다.", MaxTitleRunes)
	}
	if len(i.Body) > MaxBodyBytes {
		e.add("body", "%d바이트를 넘을 수 없습니다.", MaxBodyBytes)
	}
	if _, ok := ParseIssueType(string(i.Type)); !ok {
		e.add("type", "%s 중 하나여야 합니다.", joinEnum(IssueTypes))
	}
	if _, ok := ParseIssueStatus(string(i.Status)); !ok {
		e.add("status", "%s 중 하나여야 합니다.", joinEnum(IssueStatuses))
	}
	if _, ok := ParsePriority(string(i.Priority)); !ok {
		e.add("priority", "%s 중 하나여야 합니다.", joinEnum(Priorities))
	}
	if len(i.Labels) > MaxLabels {
		e.add("labels", "최대 %d개입니다.", MaxLabels)
	}
	for n, l := range i.Labels {
		if !ValidLabel(l) {
			e.add(fmt.Sprintf("labels[%d]", n), "소문자, 숫자, '.', '_', '-'로 된 1~32자여야 하고 첫 글자는 소문자나 숫자여야 합니다.")
		}
	}
	if i.Parent != nil {
		if _, _, ok := ParseIssueKey(*i.Parent); !ok {
			e.add("parent", "이슈 키 형식(예: AGB-12)이어야 합니다.")
		}
	}
	if len(i.DependsOn) > MaxDependsOn {
		e.add("depends_on", "최대 %d개입니다.", MaxDependsOn)
	}
	for n, k := range i.DependsOn {
		if _, _, ok := ParseIssueKey(k); !ok {
			e.add(fmt.Sprintf("depends_on[%d]", n), "이슈 키 형식(예: AGB-12)이어야 합니다.")
		}
	}
	if len(i.Refs) > MaxRefs {
		e.add("refs", "최대 %d개입니다.", MaxRefs)
	}
	for n, r := range i.Refs {
		if _, _, ok := ParseRef(r); !ok {
			e.add(fmt.Sprintf("refs[%d]", n), "종류:값 형식이어야 하고 종류는 %s 중 하나, 값은 1~500자여야 합니다.", strings.Join(RefKinds, ", "))
		}
	}
	if i.Type == TypeObservation {
		if i.RecheckCondition == nil || utf8.RuneCountInString(*i.RecheckCondition) < 1 || utf8.RuneCountInString(*i.RecheckCondition) > MaxRecheckRunes {
			e.add("recheck_condition", "종류가 observation이면 1~%d자의 재검토 조건이 필요합니다.", MaxRecheckRunes)
		}
	} else if i.RecheckCondition != nil {
		e.add("recheck_condition", "종류가 observation일 때만 쓸 수 있습니다.")
	}
	return e.orNil()
}

// ValidateProject checks the key and name of a project.
func ValidateProject(key, name string) error {
	var e ValidationError
	if !ValidProjectKey(key) {
		e.add("key", "대문자로 시작하는 대문자·숫자 2~10자여야 합니다.")
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameRunes {
		e.add("name", "1~%d자여야 합니다.", MaxNameRunes)
	}
	return e.orNil()
}

// ValidateProjectName checks only the name, for patches.
func ValidateProjectName(name string) error {
	var e ValidationError
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameRunes {
		e.add("name", "1~%d자여야 합니다.", MaxNameRunes)
	}
	return e.orNil()
}

// ValidateCommentBody checks a comment body.
func ValidateCommentBody(body string) error {
	var e ValidationError
	if len(body) < 1 || len(body) > MaxBodyBytes {
		e.add("body", "1바이트 이상 %d바이트 이하여야 합니다.", MaxBodyBytes)
	}
	return e.orNil()
}
