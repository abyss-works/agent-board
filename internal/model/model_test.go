package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIssueStatusClosed(t *testing.T) {
	closed := map[IssueStatus]bool{
		StatusBacklog: false, StatusTodo: false, StatusDoing: false, StatusReview: false,
		StatusDone: true, StatusCancelled: true, StatusDeleted: true,
	}
	if len(IssueStatuses) != 7 {
		t.Fatalf("IssueStatuses = %d, want 7", len(IssueStatuses))
	}
	for s, want := range closed {
		if got := s.Closed(); got != want {
			t.Errorf("%s.Closed() = %v, want %v", s, got, want)
		}
	}
}

func TestParseEnums(t *testing.T) {
	if _, ok := ParseIssueStatus("deleted"); !ok {
		t.Error("deleted must be a valid issue status")
	}
	if _, ok := ParseIssueStatus("open"); ok {
		t.Error("open is an alias, not a status")
	}
	if _, ok := ParseIssueType("observation"); !ok {
		t.Error("observation must be a valid type")
	}
	if _, ok := ParsePriority("P4"); ok {
		t.Error("P4 must be invalid")
	}
	if _, ok := ParseProjectStatus("archived"); !ok {
		t.Error("archived must be a valid project status")
	}
	if _, ok := ParseActorKind("bot"); ok {
		t.Error("bot must be an invalid actor kind")
	}
}

func TestExpandStatusAlias(t *testing.T) {
	got, err := ExpandIssueStatuses("open")
	if err != nil || len(got) != 4 {
		t.Fatalf("open = %v, %v", got, err)
	}
	got, err = ExpandIssueStatuses("closed")
	if err != nil || len(got) != 3 {
		t.Fatalf("closed = %v, %v", got, err)
	}
	got, err = ExpandIssueStatuses("all")
	if err != nil || len(got) != 7 {
		t.Fatalf("all = %v, %v", got, err)
	}
	got, err = ExpandIssueStatuses("todo,doing,todo")
	if err != nil || len(got) != 2 {
		t.Fatalf("todo,doing,todo = %v, %v (dedup)", got, err)
	}
	if _, err = ExpandIssueStatuses("todo,nope"); err == nil {
		t.Fatal("nope must be rejected")
	}
}

func TestValidators(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) bool
		ok   []string
		bad  []string
	}{
		{"project key", ValidProjectKey, []string{"AGB", "A1", "ABCDEFGHIJ"}, []string{"", "A", "agb", "1AB", "ABCDEFGHIJK", "AG-B"}},
		{"actor id", ValidActorID, []string{"claude", "owner1", "a"}, []string{"", "Claude", "a_b", "a-b", "abcdefghijklmnopqrstuvwxyz0123456"}},
		{"label", ValidLabel, []string{"api", "cli-v2", "a.b_c", "0x"}, []string{"", "API", "-api", "a b", "abcdefghijklmnopqrstuvwxyz0123456"}},
		{"session id", ValidSessionID, []string{"c1d2e3f4", "550e8400-e29b-41d4-a716-446655440000", "s.1_2-3", "A"}, []string{"", "-abc", ".abc", "a b", "a/b", "한글", string(make([]byte, 129))}},
		{"client host", ValidClientHost, []string{"desk-win10", "ab", "host.example.com", "A1"}, []string{"", "-host", "host-", "ho st", "host..x", ".host", "host.", "a_b", string(make([]byte, 65)), "한글"}},
	}
	for _, c := range cases {
		for _, v := range c.ok {
			if !c.fn(v) {
				t.Errorf("%s: %q should be valid", c.name, v)
			}
		}
		for _, v := range c.bad {
			if c.fn(v) {
				t.Errorf("%s: %q should be invalid", c.name, v)
			}
		}
	}
	if !ValidSessionID(string(make([]byte, 0)) + "a" + repeat("b", 127)) {
		t.Error("128-char session id should be valid")
	}
	if !ValidClientHost(repeat("a", 63)) {
		t.Error("63-char host label should be valid")
	}
	if ValidClientHost(repeat("a", 64)) {
		t.Error("64-char single label should be invalid (label limit 63)")
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestValidateComment(t *testing.T) {
	text := func(s string) *string { return &s }
	cases := []struct {
		name    string
		body    string
		summary *string
		want    []string // field of each violation, in order
	}{
		{"body without summary", "x", nil, nil},
		{"one character summary", "x", text("a"), nil},
		{"30 runes of 3 bytes each", "x", text(strings.Repeat("가", MaxSummaryRunes)), nil},
		{"31 runes", "x", text(strings.Repeat("가", MaxSummaryRunes+1)), []string{"summary"}},
		{"empty summary", "x", text(""), []string{"summary"}},
		{"line feed", "x", text("서버\n랭킹"), []string{"summary"}},
		{"carriage return", "x", text("서버\r랭킹"), []string{"summary"}},
		{"too long and multi-line reports both", "x", text(strings.Repeat("a", MaxSummaryRunes) + "\n"), []string{"summary", "summary"}},
		{"empty body", "", nil, []string{"body"}},
		{"body at the byte limit", strings.Repeat("x", MaxBodyBytes), nil, nil},
		{"body over the byte limit", strings.Repeat("x", MaxBodyBytes+1), nil, []string{"body"}},
		{"body and summary both invalid", "", text(""), []string{"body", "summary"}},
	}
	for _, c := range cases {
		err := ValidateComment(c.body, c.summary)
		if len(c.want) == 0 {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want *ValidationError", c.name, err)
			continue
		}
		got := make([]string, len(ve.Fields))
		for i, f := range ve.Fields {
			got[i] = f.Field
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: fields = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseRef(t *testing.T) {
	kind, val, ok := ParseRef("repo:wsk0715/agent-board")
	if !ok || kind != "repo" || val != "wsk0715/agent-board" {
		t.Fatalf("ParseRef = %q %q %v", kind, val, ok)
	}
	if _, _, ok := ParseRef("url:https://example.com/a:b"); !ok {
		t.Error("value may contain colons")
	}
	for _, bad := range []string{"", "repo", "repo:", ":x", "git:abc", "REPO:x", "repo:" + repeat("x", 501)} {
		if _, _, ok := ParseRef(bad); ok {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestIssueKey(t *testing.T) {
	p, n, ok := ParseIssueKey("AGB-12")
	if !ok || p != "AGB" || n != 12 {
		t.Fatalf("ParseIssueKey = %q %d %v", p, n, ok)
	}
	for _, bad := range []string{"AGB", "AGB-0", "AGB-01", "agb-1", "AGB-1-2", "AGB-x", "-1"} {
		if _, _, ok := ParseIssueKey(bad); ok {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if IssueKey("AGB", 7) != "AGB-7" {
		t.Error("IssueKey format")
	}
}

func TestTimeJSON(t *testing.T) {
	ts := NewTime(time.Date(2026, 10, 17, 5, 46, 6, 123_456_789, time.FixedZone("KST", 9*3600)))
	b, err := json.Marshal(ts)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2026-10-16T20:46:06.123Z"` {
		t.Fatalf("Marshal = %s", b)
	}
	zero := NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if b, _ = json.Marshal(zero); string(b) != `"2026-01-02T03:04:05.000Z"` {
		t.Fatalf("Marshal zero millis = %s", b)
	}
	var back Time
	if err := json.Unmarshal([]byte(`"2026-10-16T20:46:06.123Z"`), &back); err != nil {
		t.Fatal(err)
	}
	if !back.Time().Equal(ts.Time()) {
		t.Fatalf("round trip = %v, want %v", back.Time(), ts.Time())
	}
	if ts.String() != "2026-10-16T20:46:06.123Z" {
		t.Fatalf("String = %s", ts.String())
	}
	parsed, err := ParseTime("2026-10-16T20:46:06.123Z")
	if err != nil || !parsed.Time().Equal(ts.Time()) {
		t.Fatalf("ParseTime = %v %v", parsed, err)
	}
	var np *Time
	if b, _ = json.Marshal(np); string(b) != "null" {
		t.Fatalf("nil *Time = %s", b)
	}
}
