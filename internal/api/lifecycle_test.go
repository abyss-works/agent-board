package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func textPtr(s string) *string { return &s }

func etagOf(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// The todo status and the decision type are gone. A request that names either is
// refused exactly as one that names a value that never existed.
func TestTodoStatusAndDecisionTypeAreRejected(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"a"}`)

	post := func(body string) *httptest.ResponseRecorder { return e.postIssue(t, body, nil) }
	patch := func(body string) *httptest.ResponseRecorder { return e.do(t, "PATCH", "/v1/issues/AGB-1", body, nil) }
	for name, c := range map[string]struct {
		send         func(string) *httptest.ResponseRecorder
		removed, bad string
		field        string
	}{
		"create in todo":       {post, `{"project":"AGB","title":"x","status":"todo"}`, `{"project":"AGB","title":"x","status":"gone"}`, "status"},
		"create as a decision": {post, `{"project":"AGB","title":"x","type":"decision"}`, `{"project":"AGB","title":"x","type":"gone"}`, "type"},
		"update to todo":       {patch, `{"status":"todo"}`, `{"status":"gone"}`, "status"},
		"update to a decision": {patch, `{"type":"decision"}`, `{"type":"gone"}`, "type"},
	} {
		removed, bad := c.send(c.removed), c.send(c.bad)
		wantFieldError(t, removed, 422, c.field)
		if !reflect.DeepEqual(decodeErr(t, removed), decodeErr(t, bad)) {
			t.Errorf("%s: got %s, but an unknown value gets %s", name, removed.Body.String(), bad.Body.String())
		}
	}

	// Filters answer 400 for the removed values as they do for unknown ones.
	for target, field := range map[string]string{
		"/v1/issues?status=todo":           "status",
		"/v1/issues?status=backlog,todo":   "status",
		"/v1/issues?type=decision":         "type",
		"/v1/issues?type=feature,decision": "type",
		"/v1/queue?status=todo":            "status",
		"/v1/queue?status=backlog,todo":    "status",
		"/v1/board?status=todo":            "status",
	} {
		wantFieldError(t, e.do(t, "GET", target, "", nil), 400, field)
	}

	// Nothing was written: the issue is as it was and the next number is the next one.
	var i model.Issue
	decodeJSON(t, e.do(t, "GET", "/v1/issues/AGB-1", "", nil), &i)
	if i.Version != 1 || i.Status != model.StatusBacklog || i.Type != model.TypeFeature {
		t.Errorf("issue after the refusals = %+v", i)
	}
	if next := e.createIssue(t, `{"project":"AGB","title":"next"}`); next.Key != "AGB-2" {
		t.Errorf("key after the refusals = %s", next.Key)
	}
}

func TestDecisionNeededOnTheAPI(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")

	// A question can be given on creation; null means none.
	asked := e.createIssue(t, `{"project":"AGB","title":"asks","decision_needed":"SQLite로 둘지 정해 주세요."}`)
	if asked.DecisionNeeded == nil || *asked.DecisionNeeded != "SQLite로 둘지 정해 주세요." {
		t.Errorf("created with a question: %v", asked.DecisionNeeded)
	}
	rec := e.postIssue(t, `{"project":"AGB","title":"plain","decision_needed":null}`, nil)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"decision_needed":null`) {
		t.Errorf("created with null: %d %s", rec.Code, rec.Body.String())
	}

	// Merge patch: set, change and clear with null. Each change is a write of its own.
	patch := func(body string) (model.Issue, *httptest.ResponseRecorder) {
		t.Helper()
		rec := e.do(t, "PATCH", "/v1/issues/AGB-2", body, nil)
		var i model.Issue
		decodeJSON(t, rec, &i)
		return i, rec
	}
	for _, step := range []struct {
		name, body string
		want       *string
		version    int64
	}{
		{"set", `{"decision_needed":"두 안 중 어느 쪽인지"}`, textPtr("두 안 중 어느 쪽인지"), 2},
		{"change", `{"decision_needed":"다른 질문"}`, textPtr("다른 질문"), 3},
		{"repeat", `{"decision_needed":"다른 질문"}`, textPtr("다른 질문"), 3},
		{"clear", `{"decision_needed":null}`, nil, 4},
		{"clear again", `{"decision_needed":null}`, nil, 4},
	} {
		i, rec := patch(step.body)
		if rec.Code != 200 || !reflect.DeepEqual(i.DecisionNeeded, step.want) || i.Version != step.version || rec.Header().Get("ETag") != etagOf(step.version) {
			t.Errorf("%s: %d question %v version %d etag %s", step.name, rec.Code, i.DecisionNeeded, i.Version, rec.Header().Get("ETag"))
		}
		if step.want == nil && !strings.Contains(rec.Body.String(), `"decision_needed":null`) {
			t.Errorf("%s: a cleared question is null in the JSON: %s", step.name, rec.Body.String())
		}
	}

	// Each change is in the activity record of its write, like any other field.
	var acts listEnvelope[model.Activity]
	decodeJSON(t, e.do(t, "GET", "/v1/issues/AGB-2/activity?limit=10", "", nil), &acts)
	var got []string
	for _, a := range acts.Items {
		if c, ok := a.Changes["decision_needed"]; ok && a.Kind == model.ActivityUpdated {
			got = append(got, string(c.From)+">"+string(c.To))
		}
	}
	if want := []string{`"다른 질문">null`, `"두 안 중 어느 쪽인지">"다른 질문"`, `null>"두 안 중 어느 쪽인지"`}; !reflect.DeepEqual(got, want) {
		t.Errorf("decision_needed changes, newest first = %v, want %v", got, want)
	}
	var byField listEnvelope[model.Activity]
	rec = e.do(t, "GET", "/v1/activity?field=decision_needed&kind=updated", "", nil)
	decodeJSON(t, rec, &byField)
	if rec.Code != 200 || byField.Total != 3 {
		t.Errorf("activity filtered by decision_needed: %d %s", rec.Code, rec.Body.String())
	}

	// One to 500 characters; anything but a string or null is a type error.
	for name, c := range map[string]struct {
		body string
		code int
	}{
		"empty":          {`{"decision_needed":""}`, 422},
		"501 characters": {`{"decision_needed":"` + strings.Repeat("가", 501) + `"}`, 422},
		"a number":       {`{"decision_needed":5}`, 400},
		"a list":         {`{"decision_needed":["a"]}`, 400},
	} {
		t.Run("patch with "+name, func(t *testing.T) {
			wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-2", c.body, nil), c.code, "decision_needed")
		})
		t.Run("create with "+name, func(t *testing.T) {
			wantFieldError(t, e.postIssue(t, `{"project":"AGB","title":"x",`+strings.TrimPrefix(c.body, "{"), nil), c.code, "decision_needed")
		})
	}
	if i, rec := patch(`{"decision_needed":"` + strings.Repeat("가", 500) + `"}`); rec.Code != 200 || i.DecisionNeeded == nil || len([]rune(*i.DecisionNeeded)) != 500 {
		t.Errorf("500 characters: %d %s", rec.Code, rec.Body.String())
	}
	// The server fields stay read only next to it.
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-2", `{"decision_needed":"x","key":"AGB-9"}`, nil), 400, "key")

	// Every list that shows issues shows the question.
	e.createIssue(t, `{"project":"AGB","title":"doing","status":"doing","decision_needed":"묻는다"}`)
	var list listEnvelope[model.IssueSummary]
	rec = e.do(t, "GET", "/v1/issues?sort=key", "", nil)
	decodeJSON(t, rec, &list)
	if len(list.Items) != 3 || list.Items[0].DecisionNeeded == nil || *list.Items[0].DecisionNeeded != "SQLite로 둘지 정해 주세요." || list.Items[2].DecisionNeeded == nil {
		t.Errorf("list = %+v", list.Items)
	}
	if strings.Contains(rec.Body.String(), `"body"`) || strings.Count(rec.Body.String(), `"decision_needed"`) != 3 {
		t.Errorf("each summary has the field and none has a body: %s", rec.Body.String())
	}
	var board store.Board
	decodeJSON(t, e.do(t, "GET", "/v1/board?project=AGB", "", nil), &board)
	if board.Columns[1].Status != model.StatusDoing || board.Columns[1].Issues[0].DecisionNeeded == nil || *board.Columns[1].Issues[0].DecisionNeeded != "묻는다" {
		t.Errorf("board doing column = %+v", board.Columns[1])
	}
	var queue listEnvelope[model.QueueItem]
	decodeJSON(t, e.do(t, "GET", "/v1/queue?project=AGB", "", nil), &queue)
	if queue.Items[0].DecisionNeeded == nil || *queue.Items[0].DecisionNeeded != "SQLite로 둘지 정해 주세요." {
		t.Errorf("queue item = %+v", queue.Items[0])
	}
	var o store.Overview
	decodeJSON(t, e.do(t, "GET", "/v1/projects/AGB/overview", "", nil), &o)
	if o.Decisions.Total != 3 || o.Decisions.Items[0].DecisionNeeded == nil || o.Doing.Items[0].DecisionNeeded == nil {
		t.Errorf("overview decisions = %+v, doing = %+v", o.Decisions, o.Doing)
	}
}

// When the spec of an acknowledged issue changes, the server clears the
// acknowledgement and the activity record of the same write says so. Every
// other change leaves it.
func TestSpecChangeClearsTheAcknowledgementOnTheAPI(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	owner := map[string]string{"X-Actor": "owner"}
	feature, _ := model.TemplateFor(model.TypeFeature)
	longer, err := json.Marshal(feature.Body + "\n보충")
	if err != nil {
		t.Fatal(err)
	}

	// issue has the agent create an issue in the given status and the user acknowledge it.
	issue := func(status string) (key string, version int64) {
		t.Helper()
		i := e.createIssue(t, `{"project":"AGB","title":"agent work","status":"`+status+`"}`)
		rec := e.do(t, "POST", "/v1/issues/"+i.Key+"/acknowledge", "", owner)
		var acked model.Issue
		decodeJSON(t, rec, &acked)
		if rec.Code != 200 || acked.AcknowledgedAt == nil {
			t.Fatalf("acknowledge %s: %d %s", i.Key, rec.Code, rec.Body.String())
		}
		return i.Key, acked.Version
	}
	newest := func(key string) model.Activity {
		t.Helper()
		var acts listEnvelope[model.Activity]
		decodeJSON(t, e.do(t, "GET", "/v1/issues/"+key+"/activity?limit=1", "", nil), &acts)
		return acts.Items[0]
	}

	for name, c := range map[string]struct {
		status  string
		headers map[string]string
		body    string
	}{
		"title":           {"backlog", nil, `{"title":"renamed"}`},
		"body":            {"backlog", nil, `{"body":` + string(longer) + `}`},
		"type":            {"backlog", owner, `{"type":"design"}`},
		"review to doing": {"review", nil, `{"status":"doing"}`},
	} {
		key, version := issue(c.status)
		rec := e.do(t, "PATCH", "/v1/issues/"+key, c.body, c.headers)
		var got model.Issue
		decodeJSON(t, rec, &got)
		if rec.Code != 200 || got.AcknowledgedAt != nil || got.AcknowledgedBy != nil || got.Version != version+1 || rec.Header().Get("ETag") != etagOf(version+1) {
			t.Errorf("%s: %d acknowledged %v, version %d (was %d)", name, rec.Code, got.AcknowledgedAt, got.Version, version)
		}
		if !strings.Contains(rec.Body.String(), `"acknowledged_at":null,"acknowledged_by":null`) {
			t.Errorf("%s: the response shows the issue as unacknowledged: %s", name, rec.Body.String())
		}
		ack, has := newest(key).Changes["acknowledged"]
		if !has || string(ack.From) != "true" || string(ack.To) != "false" {
			t.Errorf("%s: the record lacks acknowledged true -> false: %+v", name, newest(key).Changes)
		}
		// The new spec can be acknowledged again.
		if rec := e.do(t, "POST", "/v1/issues/"+key+"/acknowledge", "", owner); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"acknowledged_by":"owner"`) {
			t.Errorf("%s: acknowledge again: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	for name, c := range map[string]struct {
		status string
		body   string
	}{
		"labels":             {"backlog", `{"labels":["api"]}`},
		"priority":           {"backlog", `{"priority":"P0"}`},
		"refs":               {"backlog", `{"refs":["doc:api.md"]}`},
		"decision_needed":    {"backlog", `{"decision_needed":"어느 쪽으로 할지"}`},
		"backlog to doing":   {"backlog", `{"status":"doing"}`},
		"doing to review":    {"doing", `{"status":"review"}`},
		"review to done":     {"review", `{"status":"done"}`},
		"the title repeated": {"backlog", `{"title":"agent work","labels":["api"]}`},
	} {
		key, version := issue(c.status)
		rec := e.do(t, "PATCH", "/v1/issues/"+key, c.body, nil)
		var got model.Issue
		decodeJSON(t, rec, &got)
		if rec.Code != 200 || got.AcknowledgedAt == nil || got.AcknowledgedBy == nil || *got.AcknowledgedBy != "owner" || got.Version != version+1 {
			t.Errorf("%s: %d acknowledged %v, version %d (was %d)", name, rec.Code, got.AcknowledgedAt, got.Version, version)
		}
		if _, has := newest(key).Changes["acknowledged"]; has {
			t.Errorf("%s: the record has an acknowledged change", name)
		}
	}

	// A comment, and a write that changes nothing, keep it and write nothing to the issue.
	key, version := issue("backlog")
	if rec := e.do(t, "POST", "/v1/issues/"+key+"/comments", `{"body":"착수한다."}`, nil); rec.Code != 201 {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.do(t, "PATCH", "/v1/issues/"+key, `{"title":"agent work","type":"feature","status":"backlog"}`, nil)
	var got model.Issue
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.AcknowledgedAt == nil || got.Version != version {
		t.Errorf("a write that changes nothing: %d acknowledged %v, version %d (was %d)", rec.Code, got.AcknowledgedAt, got.Version, version)
	}
}
