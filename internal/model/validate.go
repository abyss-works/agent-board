package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
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
	MaxSummaryRunes     = 30
	MaxReasonRunes      = 500
	MaxTopicNameRunes   = 50
	MaxSearchTerms      = 10
	DefaultIssueType    = TypeFeature
	DefaultIssueStatus  = StatusBacklog
	DefaultPriority     = P2
	DefaultProjectState = ProjectActive
)

// Limits on the fields of a milestone.
const (
	MaxMilestoneNameRunes        = 50
	MaxMilestoneDescriptionRunes = 2000
)

// DateLayout is the format of a calendar date, as in a milestone's target date.
const DateLayout = "2006-01-02"

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

// MergeValidation joins the violations of several validation results into
// one error so a client sees every violation at once. Nil results are
// skipped. An error that is not a *ValidationError is returned as it is.
func MergeValidation(errs ...error) error {
	var merged ValidationError
	for _, err := range errs {
		if err == nil {
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			return err
		}
		merged.Fields = append(merged.Fields, ve.Fields...)
	}
	return merged.orNil()
}

// ResolveStatusReason decides the status_reason of an update that moves an
// issue from one status to another. reason is the one the request carried, or
// nil. A change to a reason-required status needs a reason; any other change
// without one gets "{from} → {to}" in Korean status names. A reason on a
// request that does not change the status is a violation. The result is nil
// when the status does not change, and the caller keeps the stored reason.
func ResolveStatusReason(from, to IssueStatus, reason *string) (*string, error) {
	var e ValidationError
	if from == to {
		if reason != nil {
			e.add("status_reason", "상태를 바꾸는 요청에서만 쓸 수 있습니다.")
		}
		return nil, e.orNil()
	}
	if _, ok := ParseIssueStatus(string(to)); !ok {
		return nil, nil // the status check reports the unknown status
	}
	if reason == nil {
		if to.ReasonRequired() {
			e.add("status_reason", "%s로 바꿀 때는 사유가 필요합니다.", to.Label())
			return nil, e.orNil()
		}
		def := from.Label() + " → " + to.Label()
		return &def, nil
	}
	if n := utf8.RuneCountInString(*reason); n < 1 || n > MaxReasonRunes {
		e.add("status_reason", "1~%d자여야 합니다.", MaxReasonRunes)
		return nil, e.orNil()
	}
	return reason, nil
}

// ValidateTopicName checks the name of a topic: 1 to MaxTopicNameRunes
// characters without a slash, which separates the levels of a topic path.
func ValidateTopicName(name string) error {
	var e ValidationError
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxTopicNameRunes {
		e.add("name", "1~%d자여야 합니다.", MaxTopicNameRunes)
	}
	if strings.Contains(name, "/") {
		e.add("name", "'/'를 넣을 수 없습니다.")
	}
	return e.orNil()
}

// ValidDate reports whether v is a calendar date written YYYY-MM-DD.
func ValidDate(v string) bool {
	t, err := time.Parse(DateLayout, v)
	return err == nil && t.Format(DateLayout) == v
}

// ValidateMilestone checks the fields of a milestone: a name of 1 to
// MaxMilestoneNameRunes characters, a description of at most
// MaxMilestoneDescriptionRunes characters and, when present, a target date.
func ValidateMilestone(name, description string, targetDate *string) error {
	var e ValidationError
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxMilestoneNameRunes {
		e.add("name", "1~%d자여야 합니다.", MaxMilestoneNameRunes)
	}
	if utf8.RuneCountInString(description) > MaxMilestoneDescriptionRunes {
		e.add("description", "%d자를 넘을 수 없습니다.", MaxMilestoneDescriptionRunes)
	}
	if targetDate != nil && !ValidDate(*targetDate) {
		e.add("target_date", "YYYY-MM-DD 형식의 날짜여야 합니다.")
	}
	return e.orNil()
}

// ValidateDeviceRequest checks a request for a device authorization: the
// actor must be a declared agent and the client host must follow the rule of
// the X-Client-Host header.
func ValidateDeviceRequest(actor, clientHost string, actors map[string]Actor) error {
	var e ValidationError
	if a, ok := actors[actor]; !ok || a.Kind != ActorAgent {
		e.add("actor", "선언된 agent 종류 행위자여야 합니다.")
	}
	if !ValidClientHost(clientHost) {
		e.add("client_host", "X-Client-Host와 같은 규칙의 기기 이름(1~64자)이어야 합니다.")
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

// ValidateComment checks a comment body and its optional one-line summary.
// A nil summary is valid; a present one must be 1 to MaxSummaryRunes runes
// without a line break.
func ValidateComment(body string, summary *string) error {
	var e ValidationError
	if len(body) < 1 || len(body) > MaxBodyBytes {
		e.add("body", "1바이트 이상 %d바이트 이하여야 합니다.", MaxBodyBytes)
	}
	if summary != nil {
		if n := utf8.RuneCountInString(*summary); n < 1 || n > MaxSummaryRunes {
			e.add("summary", "1~%d자여야 합니다.", MaxSummaryRunes)
		}
		if strings.ContainsAny(*summary, "\r\n") {
			e.add("summary", "줄바꿈을 넣을 수 없습니다.")
		}
	}
	return e.orNil()
}
