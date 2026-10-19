//go:build e2e

package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	envURL          = "AGB_E2E_URL"
	envToken        = "AGB_E2E_TOKEN"
	envPersistKey   = "AGB_E2E_PERSIST_KEY"
	envPersistPhase = "AGB_E2E_PERSIST_PHASE"

	defaultHost = "e2e-host"
	jsonType    = "application/json; charset=utf-8"
)

var (
	baseURL   string
	token     string // the shared token; only user actors may use it
	runID     string // five characters of [A-Z0-9], unique per run
	sessionID string // X-Session-Id shared by the run
	httpc     = &http.Client{Timeout: 15 * time.Second}

	// actorTokens holds the actor token of each agent actor the server
	// declares. TestMain fills it before any test runs.
	actorTokens = map[string]string{}

	timeRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
)

func TestMain(m *testing.M) {
	baseURL = strings.TrimRight(os.Getenv(envURL), "/")
	token = os.Getenv(envToken)
	if baseURL == "" || token == "" {
		fmt.Fprintf(os.Stderr, "e2e: %s and %s must be set\n", envURL, envToken)
		os.Exit(2)
	}
	runID = randomID(5)
	sessionID = "e2e-" + runID
	owner = as("owner")
	if err := loginAgents(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// bearer is the token a caller authenticates with. An agent actor has no
// right to the shared token, so it uses the actor token its login produced.
// Everything else, the user actors and names the server does not declare,
// uses the shared token and meets the server's rules for it.
func bearer(actor string) string {
	if t, ok := actorTokens[actor]; ok {
		return t
	}
	return token
}

// loginAgents logs in every agent actor the server declares, as the CLI will:
// ask for a device authorization, approve it as owner with the shared token,
// exchange the device code. A run does this once, on defaultHost.
func loginAgents() error {
	r, err := sendRaw("GET", "/v1/actors", nil, owner.headers(false))
	if err != nil {
		return err
	}
	if r.status != 200 {
		return fmt.Errorf("GET /v1/actors: %s", r)
	}
	var actors struct {
		Items []actor `json:"items"`
	}
	if err := json.Unmarshal(r.body, &actors); err != nil {
		return fmt.Errorf("GET /v1/actors: %w (%s)", err, r)
	}
	for _, a := range actors.Items {
		if a.Kind != "agent" {
			continue
		}
		tok, err := deviceLogin(a.ID, defaultHost)
		if err != nil {
			return fmt.Errorf("log in %s: %w", a.ID, err)
		}
		actorTokens[a.ID] = tok.Token
	}
	return nil
}

func randomID(n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// projectKey builds a run-unique project key: "E" + runID + suffix, at most
// ten characters.
func projectKey(suffix string) string {
	k := "E" + runID + suffix
	if len(k) > 10 {
		panic("project key too long: " + k)
	}
	return k
}

// label builds a run-unique label.
func label(s string) string { return "e2e-" + strings.ToLower(runID) + "-" + s }

// tick guarantees that the next write gets a later millisecond timestamp.
func tick() { time.Sleep(2 * time.Millisecond) }

// caller is the identity sent in the trace headers.
type caller struct {
	actor, session, host string
}

func as(actor string) caller { return caller{actor: actor, session: sessionID, host: defaultHost} }

// owner is the default caller; TestMain fills it once the session ID exists.
var owner caller

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) String() string {
	b := string(r.body)
	if len(b) > 600 {
		b = b[:600] + "..."
	}
	return fmt.Sprintf("status %d body %s", r.status, b)
}

// sendRaw performs a request with exactly the given headers; nothing is
// added. TestMain uses it before there is a test to fail.
func sendRaw(method, path string, raw []byte, hdr http.Header) (response, error) {
	var rd io.Reader
	if raw != nil {
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		return response{}, fmt.Errorf("build request: %w", err)
	}
	for k, vs := range hdr {
		req.Header[k] = append([]string(nil), vs...)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: read body: %w", method, path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// send performs a request with exactly the given headers; nothing is added.
func send(t testing.TB, method, path string, raw []byte, hdr http.Header) response {
	t.Helper()
	r, err := sendRaw(method, path, raw, hdr)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// headers returns the full header set of an authenticated request.
func (c caller) headers(raw bool) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+bearer(c.actor))
	h.Set("X-Actor", c.actor)
	h.Set("X-Session-Id", c.session)
	h.Set("X-Client-Host", c.host)
	h.Set("Accept", "application/json")
	if raw {
		h.Set("Content-Type", jsonType)
	}
	return h
}

// do sends an authenticated request with body marshalled to JSON. extra
// headers override the defaults.
func (c caller) do(t testing.TB, method, path string, body any, extra ...http.Header) response {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	return c.doRaw(t, method, path, raw, extra...)
}

func (c caller) doRaw(t testing.TB, method, path string, raw []byte, extra ...http.Header) response {
	t.Helper()
	h := c.headers(raw != nil)
	for _, e := range extra {
		for k, vs := range e {
			h[k] = vs
		}
	}
	return send(t, method, path, raw, h)
}

// expect asserts the status and the JSON content type.
func expect(t testing.TB, r response, status int) response {
	t.Helper()
	if r.status != status {
		t.Fatalf("want status %d, got %s", status, r)
	}
	if ct := r.header.Get("Content-Type"); ct != jsonType {
		t.Fatalf("want Content-Type %q, got %q", jsonType, ct)
	}
	return r
}

func decode[T any](t testing.TB, r response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("decode %T: %v (%s)", v, err, r)
	}
	return v
}

func decodeAs[T any](t testing.TB, r response, status int) T {
	t.Helper()
	expect(t, r, status)
	return decode[T](t, r)
}

type detail struct {
	Field   string `json:"field"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type apiError struct {
	Error struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Details []detail `json:"details"`
	} `json:"error"`
}

// wantError asserts an error response with the given status and code.
func wantError(t testing.TB, r response, status int, code string) apiError {
	t.Helper()
	e := decodeAs[apiError](t, r, status)
	if e.Error.Code != code {
		t.Fatalf("want code %q, got %s", code, r)
	}
	if e.Error.Message == "" {
		t.Fatalf("error message is empty: %s", r)
	}
	return e
}

func (e apiError) fields() []string {
	out := make([]string, 0, len(e.Error.Details))
	for _, d := range e.Error.Details {
		out = append(out, d.Field)
	}
	sort.Strings(out)
	return out
}

func (e apiError) hasField(field string) bool {
	for _, d := range e.Error.Details {
		if d.Field == field {
			return true
		}
	}
	return false
}

func (e apiError) hasReason(reason string) bool {
	for _, d := range e.Error.Details {
		if d.Reason == reason {
			return true
		}
	}
	return false
}

// Resource shapes as the spec describes them. Unknown fields are ignored.

type project struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

// issue covers the full issue, the summary (Body nil), search items
// (Snippet) and queue items (Ready, BlockedBy).
type issue struct {
	Key              string   `json:"key"`
	Project          string   `json:"project"`
	Number           int64    `json:"number"`
	Title            string   `json:"title"`
	Body             *string  `json:"body"`
	Type             string   `json:"type"`
	Status           string   `json:"status"`
	Priority         string   `json:"priority"`
	Labels           []string `json:"labels"`
	Parent           *string  `json:"parent"`
	DependsOn        []string `json:"depends_on"`
	Refs             []string `json:"refs"`
	RecheckCondition *string  `json:"recheck_condition"`
	DecisionNeeded   *string  `json:"decision_needed"`
	Version          int64    `json:"version"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	ClosedAt         *string  `json:"closed_at"`
	Topic            *int64   `json:"topic"`
	TopicPath        *string  `json:"topic_path"`
	Milestone        *int64   `json:"milestone"`
	MilestoneName    *string  `json:"milestone_name"`
	StatusReason     *string  `json:"status_reason"`
	AcknowledgedAt   *string  `json:"acknowledged_at"`
	AcknowledgedBy   *string  `json:"acknowledged_by"`
	StatusChangedAt  string   `json:"status_changed_at"`
	StatusChangedBy  string   `json:"status_changed_by"`
	CreatedBy        string   `json:"created_by"`
	UpdatedBy        string   `json:"updated_by"`
	Snippet          *string  `json:"snippet"`
	Ready            *bool    `json:"ready"`
	BlockedBy        []string `json:"blocked_by"`
}

type change struct {
	From json.RawMessage `json:"from"`
	To   json.RawMessage `json:"to"`
}

type activity struct {
	ID         int64              `json:"id"`
	Issue      string             `json:"issue"`
	Project    string             `json:"project"`
	Kind       string             `json:"kind"`
	Actor      string             `json:"actor"`
	SessionID  string             `json:"session_id"`
	ClientHost string             `json:"client_host"`
	At         string             `json:"at"`
	Changes    *map[string]change `json:"changes"`
	Body       *string            `json:"body"`
}

type actor struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type deviceGrant struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
}

type deviceAuth struct {
	UserCode   string `json:"user_code"`
	Actor      string `json:"actor"`
	ClientHost string `json:"client_host"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
}

// tokenInfo covers a listed token and the exchange response, which alone
// carries Token.
type tokenInfo struct {
	ID         int64   `json:"id"`
	Actor      string  `json:"actor"`
	ClientHost string  `json:"client_host"`
	CreatedAt  string  `json:"created_at"`
	CreatedBy  string  `json:"created_by"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at"`
	RevokedBy  *string `json:"revoked_by"`
	Token      string  `json:"token"`
}

type tokenList struct {
	Items []tokenInfo `json:"items"`
}

type list[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type section[T any] struct {
	Total int `json:"total"`
	Items []T `json:"items"`
}

type overview struct {
	Project     project `json:"project"`
	GeneratedAt string  `json:"generated_at"`
	Counts      struct {
		Total      int            `json:"total"`
		Open       int            `json:"open"`
		ByStatus   map[string]int `json:"by_status"`
		ByType     map[string]int `json:"by_type"`
		ByPriority map[string]int `json:"by_priority"`
	} `json:"counts"`
	Doing          section[issue]    `json:"doing"`
	Review         section[issue]    `json:"review"`
	Blocked        section[issue]    `json:"blocked"`
	Decisions      section[issue]    `json:"decisions"`
	Next           section[issue]    `json:"next"`
	Unacknowledged section[issue]    `json:"unacknowledged"`
	RecentActivity section[activity] `json:"recent_activity"`
}

type boardColumn struct {
	Status string  `json:"status"`
	Total  int     `json:"total"`
	Issues []issue `json:"issues"`
}

type board struct {
	Project *string       `json:"project"`
	Columns []boardColumn `json:"columns"`
}

type labelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Fixtures.

// anonymousRaw sends a request with neither the token nor the trace headers,
// as the CLI does before it has a token.
func anonymousRaw(method, path string, raw []byte) (response, error) {
	h := http.Header{"Accept": {"application/json"}}
	if raw != nil {
		h.Set("Content-Type", jsonType)
	}
	return sendRaw(method, path, raw, h)
}

// anonymous is anonymousRaw for a test, with body marshalled to JSON.
func anonymous(t testing.TB, method, path string, body any) response {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	r, err := anonymousRaw(method, path, raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// deviceLogin gives an agent actor a token on a device the way the CLI will:
// ask for a device authorization, approve it as owner with the shared token,
// exchange the device code. It returns the token with its secret.
func deviceLogin(actorID, host string) (tokenInfo, error) {
	step := func(what string, r response, err error, want int) error {
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		if r.status != want {
			return fmt.Errorf("%s: want status %d, got %s", what, want, r)
		}
		return nil
	}
	body, _ := json.Marshal(map[string]any{"actor": actorID, "client_host": host})
	r, err := anonymousRaw("POST", "/v1/device-authorizations", body)
	if err := step("ask for a device authorization", r, err, 201); err != nil {
		return tokenInfo{}, err
	}
	var grant deviceGrant
	if err := json.Unmarshal(r.body, &grant); err != nil {
		return tokenInfo{}, fmt.Errorf("device authorization: %w (%s)", err, r)
	}
	body, _ = json.Marshal(map[string]any{"status": "approved"})
	r, err = sendRaw("PATCH", "/v1/device-authorizations/"+grant.UserCode, body, owner.headers(true))
	if err := step("approve the device authorization", r, err, 200); err != nil {
		return tokenInfo{}, err
	}
	body, _ = json.Marshal(map[string]any{"device_code": grant.DeviceCode})
	r, err = anonymousRaw("POST", "/v1/tokens", body)
	if err := step("exchange the device code", r, err, 201); err != nil {
		return tokenInfo{}, err
	}
	var tok tokenInfo
	if err := json.Unmarshal(r.body, &tok); err != nil || tok.Token == "" {
		return tokenInfo{}, fmt.Errorf("token: %v (%s)", err, r)
	}
	return tok, nil
}

func createProject(t testing.TB, suffix string) project {
	t.Helper()
	key := projectKey(suffix)
	r := owner.do(t, "POST", "/v1/projects", map[string]any{"key": key, "name": "e2e " + key})
	p := decodeAs[project](t, r, 201)
	if p.Key != key {
		t.Fatalf("created project key %q, want %q", p.Key, key)
	}
	return p
}

// createIssue posts an issue as the given caller. in must hold "project".
func createIssue(t testing.TB, c caller, in map[string]any) issue {
	t.Helper()
	r := c.do(t, "POST", "/v1/issues", in)
	return decodeAs[issue](t, r, 201)
}

// newIssue is createIssue as owner with the project filled in.
func newIssue(t testing.TB, projectKey, title string, fields map[string]any) issue {
	t.Helper()
	in := map[string]any{"project": projectKey, "title": title}
	for k, v := range fields {
		in[k] = v
	}
	return createIssue(t, owner, in)
}

func getIssue(t testing.TB, key string) issue {
	t.Helper()
	return decodeAs[issue](t, owner.do(t, "GET", "/v1/issues/"+key, nil), 200)
}

func patchIssue(t testing.TB, c caller, key string, patch map[string]any) issue {
	t.Helper()
	return decodeAs[issue](t, c.do(t, "PATCH", "/v1/issues/"+key, patch), 200)
}

// deleteIssue soft deletes an issue; the server requires a reason.
func deleteIssue(t testing.TB, c caller, key string) issue {
	t.Helper()
	return decodeAs[issue](t, c.do(t, "DELETE", deletePath(key), nil), 200)
}

// deletePath is the DELETE path of an issue with a reason.
func deletePath(key string) string {
	return "/v1/issues/" + key + "?reason=" + url.QueryEscape("e2e 정리")
}

// statusPatch is the PATCH body for a status change; cancelling and deleting
// carry the reason the server requires.
func statusPatch(status string) map[string]any {
	p := map[string]any{"status": status}
	if status == "cancelled" || status == "deleted" {
		p["status_reason"] = "e2e 시험"
	}
	return p
}

func keys(items []issue) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedCopy(a []string) []string {
	out := append([]string(nil), a...)
	sort.Strings(out)
	return out
}

// wantKeys asserts the exact order of issue keys.
func wantKeys(t testing.TB, items []issue, want ...string) {
	t.Helper()
	got := keys(items)
	if !equalStrings(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

func wantTime(t testing.TB, field, v string) {
	t.Helper()
	if !timeRE.MatchString(v) {
		t.Errorf("%s = %q, want RFC 3339 UTC with milliseconds", field, v)
	}
}

// strs builds a string slice for JSON bodies.
func strs(v ...string) []string { return v }
