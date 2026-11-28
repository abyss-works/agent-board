package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

var apiTestDBSeq atomic.Int64

func newPGTestHandler(t *testing.T) http.Handler {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	ctx := context.Background()
	dbName := fmt.Sprintf("agb_t_api_%d_%d", os.Getpid(), apiTestDBSeq.Add(1))
	pool, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		pool.Close()
		t.Fatalf("CREATE DATABASE %s: %v", dbName, err)
	}
	pool.Close()
	t.Cleanup(func() {
		p, err := pgxpool.New(context.Background(), baseDSN)
		if err != nil {
			return
		}
		defer p.Close()
		_, _ = p.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})
	u.Path = "/" + dbName

	pgs, err := pgstore.Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = pgs.Close() })

	actors := []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "worker", Kind: model.ActorAgent},
	}
	if err := pgs.EnsureActors(ctx, actors); err != nil {
		t.Fatalf("EnsureActors: %v", err)
	}
	h := New(Options{
		Token:   testToken,
		Actors:  actors,
		Version: "test",
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PGStore: pgs,
	})
	t.Cleanup(func() {
		if c, ok := h.(io.Closer); ok {
			_ = c.Close()
		}
	})
	return h
}

func pgDo(h http.Handler, method, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "pg-api-test")
	req.Header.Set("X-Client-Host", "devbox")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPGAPIHealthStatsAndDomainContracts(t *testing.T) {
	h := newPGTestHandler(t)

	// Health & Stats (db_bytes > 0).
	rec := pgDo(h, "GET", "/v1/health", nil, "")
	if rec.Code != 200 {
		t.Fatalf("health = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "GET", "/v1/stats", nil, "")
	if rec.Code != 200 {
		t.Fatalf("stats = %d %s", rec.Code, rec.Body.String())
	}
	var statsBody struct {
		DBBytes int64 `json:"db_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &statsBody); err != nil || statsBody.DBBytes <= 0 {
		t.Fatalf("stats body = %s, want db_bytes > 0", rec.Body.String())
	}

	// GLOBAL delete rejected with 409.
	rec = pgDo(h, "DELETE", "/v1/projects/GLOBAL", nil, "")
	if rec.Code != 409 {
		t.Fatalf("DELETE GLOBAL = %d %s, want 409", rec.Code, rec.Body.String())
	}

	// Create project & epic.
	rec = pgDo(h, "POST", "/v1/projects", []byte(`{"key":"CORE","name":"Core Project"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create project = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "POST", "/v1/projects/CORE/epics", []byte(`{"title":"Storage Engine"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create epic = %d %s", rec.Code, rec.Body.String())
	}
	var epic model.Epic
	if err := json.Unmarshal(rec.Body.Bytes(), &epic); err != nil || epic.Key != "CORE-EPIC-1" {
		t.Fatalf("epic = %s", rec.Body.String())
	}

	// Create milestone (planned) -> transition to active.
	rec = pgDo(h, "POST", "/v1/projects/CORE/milestones", []byte(`{"name":"v1.0"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create milestone = %d %s", rec.Code, rec.Body.String())
	}
	var ms model.Milestone
	if err := json.Unmarshal(rec.Body.Bytes(), &ms); err != nil || ms.Status != model.MilestonePlanned {
		t.Fatalf("milestone = %s", rec.Body.String())
	}
	rec = pgDo(h, "PATCH", fmt.Sprintf("/v1/projects/CORE/milestones/%d", ms.ID), []byte(`{"status":"active"}`), "")
	if rec.Code != 200 {
		t.Fatalf("activate milestone = %d %s", rec.Code, rec.Body.String())
	}

	// Non-P0 non-bug into active milestone -> 422 milestone_scope_locked.
	rec = pgDo(h, "POST", "/v1/projects/CORE/issues", []byte(fmt.Sprintf(
		`{"title":"Feature A","type":"feature","priority":"P2","epic_id":%d,"milestone_id":%d}`, epic.ID, ms.ID,
	)), "")
	if rec.Code != 422 {
		t.Fatalf("scope locked = %d %s, want 422", rec.Code, rec.Body.String())
	}
	errBody := decodeErr(t, rec)
	if errBody.Error.Code != "milestone_scope_locked" {
		t.Fatalf("code = %q, want milestone_scope_locked", errBody.Error.Code)
	}

	// force=true without reason -> 422 validation_failed.
	rec = pgDo(h, "POST", "/v1/projects/CORE/issues", []byte(fmt.Sprintf(
		`{"title":"Feature A","type":"feature","priority":"P2","milestone_id":%d,"force":true}`, ms.ID,
	)), "")
	if rec.Code != 422 || decodeErr(t, rec).Error.Code != "validation_failed" {
		t.Fatalf("force without reason = %d %s, want 422 validation_failed", rec.Code, rec.Body.String())
	}

	// force=true + reason -> 201 and activity records override.
	rec = pgDo(h, "POST", "/v1/projects/CORE/issues", []byte(fmt.Sprintf(
		`{"title":"Feature A","type":"feature","priority":"P2","milestone_id":%d,"force":true,"reason":"release blocker"}`, ms.ID,
	)), "")
	if rec.Code != 201 {
		t.Fatalf("force with reason = %d %s", rec.Code, rec.Body.String())
	}
	var createdIssue struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &createdIssue)
	rec = pgDo(h, "GET", "/v1/issues/"+createdIssue.Key+"/activities", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "milestone_scope_override") || !strings.Contains(rec.Body.String(), "release blocker") {
		t.Fatalf("activities missing scope override: %d %s", rec.Code, rec.Body.String())
	}

	// Pending verification issue blocks milestone close with 409 unverified_issues.
	rec = pgDo(h, "POST", "/v1/projects/CORE/issues", []byte(fmt.Sprintf(
		`{"title":"Bug B","type":"bug","priority":"P1","milestone_id":%d,"verification_state":"pending","verification_note":"check logs"}`, ms.ID,
	)), "")
	if rec.Code != 201 {
		t.Fatalf("create bug = %d %s", rec.Code, rec.Body.String())
	}
	var bugIssue struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bugIssue)

	rec = pgDo(h, "PATCH", fmt.Sprintf("/v1/projects/CORE/milestones/%d", ms.ID), []byte(`{"status":"closed"}`), "")
	if rec.Code != 409 || decodeErr(t, rec).Error.Code != "unverified_issues" {
		t.Fatalf("close with pending = %d %s, want 409 unverified_issues", rec.Code, rec.Body.String())
	}

	// Verify bug and close milestone -> 200.
	rec = pgDo(h, "POST", "/v1/issues/"+bugIssue.Key+"/verify", nil, "")
	if rec.Code != 200 {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "PATCH", fmt.Sprintf("/v1/projects/CORE/milestones/%d", ms.ID), []byte(`{"status":"closed"}`), "")
	if rec.Code != 200 {
		t.Fatalf("close after verify = %d %s", rec.Code, rec.Body.String())
	}

	// Specs: 413 > 2 MiB, 415 invalid UTF-8, 415 NUL byte, 201 create, 200 revision update, GET revisions.
	bigPayload, _ := json.Marshal(map[string]any{
		"title":   "Too Big",
		"content": strings.Repeat("x", model.MaxDocumentBytes+1),
	})
	rec = pgDo(h, "PUT", "/v1/projects/CORE/specs/oversize", bigPayload, "application/json")
	if rec.Code != 413 || decodeErr(t, rec).Error.Code != "payload_too_large" {
		t.Fatalf("spec > 2MiB = %d %s, want 413 payload_too_large", rec.Code, rec.Body.String())
	}

	badUTF8 := append([]byte(`{"title":"Bad","content":"`), 0xff, 0xfe, '"', '}')
	rec = pgDo(h, "PUT", "/v1/projects/CORE/specs/bad-utf8", badUTF8, "application/json")
	if rec.Code != 415 || decodeErr(t, rec).Error.Code != "unsupported_media_type" {
		t.Fatalf("spec invalid utf8 = %d %s, want 415 unsupported_media_type", rec.Code, rec.Body.String())
	}

	nulPayload := []byte(`{"title":"Bin","content":"abc\u0000def"}`)
	rec = pgDo(h, "PUT", "/v1/projects/CORE/specs/bin", nulPayload, "application/json")
	if rec.Code != 415 || decodeErr(t, rec).Error.Code != "unsupported_media_type" {
		t.Fatalf("spec NUL = %d %s, want 415 unsupported_media_type", rec.Code, rec.Body.String())
	}

	rec = pgDo(h, "PUT", "/v1/projects/CORE/specs/schema", []byte(`{"title":"Schema","content":"# v1"}`), "")
	if rec.Code != 201 {
		t.Fatalf("upsert spec v1 = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "PUT", "/v1/projects/CORE/specs/schema", []byte(`{"title":"Schema v2","content":"# v2","change_summary":"update"}`), "")
	if rec.Code != 200 {
		t.Fatalf("upsert spec v2 = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "GET", "/v1/projects/CORE/specs/schema/revisions", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"revision_no":2`) {
		t.Fatalf("revisions = %d %s", rec.Code, rec.Body.String())
	}

	// Records: 201 create, 200 idempotent, 409 immutable, 200 archive.
	rec = pgDo(h, "POST", "/v1/projects/CORE/records", []byte(`{"name":"2026-11-28-decision-1.md","kind":"decision","title":"D1","content":"# D1"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create record = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "POST", "/v1/projects/CORE/records", []byte(`{"name":"2026-11-28-decision-1.md","kind":"decision","title":"D1","content":"# D1"}`), "")
	if rec.Code != 200 {
		t.Fatalf("idempotent record = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "POST", "/v1/projects/CORE/records", []byte(`{"name":"2026-11-28-decision-1.md","kind":"decision","title":"D1","content":"# D1 modified"}`), "")
	if rec.Code != 409 {
		t.Fatalf("immutable record = %d %s, want 409", rec.Code, rec.Body.String())
	}
	var immErr errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &immErr); err != nil {
		t.Fatalf("decode immutable error: %v", err)
	}
	if len(immErr.Error.Details) == 0 || immErr.Error.Details[0].Reason != "immutable" || immErr.Error.Details[0].SHA256 == "" {
		t.Fatalf("immutable details = %+v", immErr.Error.Details)
	}
	rec = pgDo(h, "POST", "/v1/projects/CORE/records/2026-11-28-decision-1.md/archive", nil, "")
	if rec.Code != 200 {
		t.Fatalf("archive record = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPGAPIRegressionGuardsSessionsMessagesAndSSE(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	ctx := context.Background()
	dbName := fmt.Sprintf("agb_t_api_%d_%d", os.Getpid(), apiTestDBSeq.Add(1))
	pool, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		pool.Close()
		t.Fatalf("CREATE DATABASE %s: %v", dbName, err)
	}
	pool.Close()
	t.Cleanup(func() {
		p, err := pgxpool.New(context.Background(), baseDSN)
		if err != nil {
			return
		}
		defer p.Close()
		_, _ = p.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})
	u.Path = "/" + dbName
	pgs, err := pgstore.Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = pgs.Close() })

	actors := []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "worker", Kind: model.ActorAgent},
	}
	if err := pgs.EnsureActors(ctx, actors); err != nil {
		t.Fatal(err)
	}
	h := New(Options{
		Token:           testToken,
		Actors:          actors,
		Version:         "test",
		Logger:          slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PGStore:         pgs,
		SSEPingInterval: 25 * time.Millisecond,
	})
	t.Cleanup(func() {
		if c, ok := h.(io.Closer); ok {
			_ = c.Close()
		}
	})

	// Setup project and issues.
	if rec := pgDo(h, "POST", "/v1/projects", []byte(`{"key":"SEC","name":"Security"}`), ""); rec.Code != 201 {
		t.Fatalf("create project = %d %s", rec.Code, rec.Body.String())
	}
	rec := pgDo(h, "POST", "/v1/projects/SEC/issues", []byte(`{"title":"Item 1"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create issue = %d %s", rec.Code, rec.Body.String())
	}
	var iss struct {
		Key     string `json:"key"`
		Version int64  `json:"version"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &iss)

	// 1. pgPatchIssue rejects non-nullable null fields with 422.
	rec = pgDo(h, "PATCH", "/v1/issues/"+iss.Key, []byte(`{"title":null,"status":null}`), "")
	if rec.Code != 422 || decodeErr(t, rec).Error.Code != "validation_failed" {
		t.Fatalf("patch null fields = %d %s, want 422 validation_failed", rec.Code, rec.Body.String())
	}

	// 2. pgVerifyIssue rejects verification_state=none with 422.
	rec = pgDo(h, "POST", "/v1/issues/"+iss.Key+"/verify", nil, "")
	if rec.Code != 422 || decodeErr(t, rec).Error.Code != "validation_failed" {
		t.Fatalf("verify none issue = %d %s, want 422 validation_failed", rec.Code, rec.Body.String())
	}

	// 3. pgDeleteIssue honors If-Match (wrong version -> 412).
	req := httptest.NewRequest("DELETE", "/v1/issues/"+iss.Key+"?reason=cleanup", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "pg-api-test")
	req.Header.Set("X-Client-Host", "devbox")
	req.Header.Set("If-Match", `"999"`)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, req)
	if delRec.Code != 412 {
		t.Fatalf("delete with wrong If-Match = %d %s, want 412", delRec.Code, delRec.Body.String())
	}

	// 4. Document body size bypass via oversized metadata (4 MiB JSON body -> 413), NUL in title -> 415.
	metaBypass := []byte(`{"title":"` + strings.Repeat("a", 3*1024*1024+100) + `","content":"ok"}`)
	rec = pgDo(h, "PUT", "/v1/projects/SEC/specs/meta-bypass", metaBypass, "application/json")
	if rec.Code != 413 {
		t.Fatalf("oversized metadata spec = %d %s, want 413", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "PUT", "/v1/projects/SEC/specs/nul-title", []byte(`{"title":"bad\u0000title","content":"ok"}`), "application/json")
	if rec.Code != 415 {
		t.Fatalf("NUL in spec title = %d %s, want 415", rec.Code, rec.Body.String())
	}

	// 5. Record name rejects ../ traversal and /archive suffix with 422.
	for _, badName := range []string{"../escape.md", "sub/archive", "archive"} {
		payload, _ := json.Marshal(map[string]any{"name": badName, "kind": "decision", "content": "ok"})
		rec = pgDo(h, "POST", "/v1/projects/SEC/records", payload, "application/json")
		if rec.Code != 422 {
			t.Fatalf("bad record name %q = %d %s, want 422", badName, rec.Code, rec.Body.String())
		}
	}

	// 6. Sessions POST /v1/sessions and PATCH /v1/sessions/{actor}/{session}.
	rec = pgDo(h, "POST", "/v1/sessions", []byte(`{"session_id":"child-1","role":"subagent"}`), "")
	if rec.Code != 201 {
		t.Fatalf("POST /v1/sessions = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "PATCH", "/v1/sessions/owner/child-1", []byte(`{"state":"waiting_input","state_reason":"need review"}`), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"waiting_input"`) {
		t.Fatalf("PATCH /v1/sessions = %d %s", rec.Code, rec.Body.String())
	}

	// 7. Messages: to_session, missing reply_to -> 422, GET /v1/messages/{id}, ListMessages filters + total.
	rec = pgDo(h, "POST", "/v1/projects/SEC/messages", []byte(`{"title":"Bad reply","body":"x","reply_to":99999}`), "")
	if rec.Code != 422 {
		t.Fatalf("missing reply_to = %d %s, want 422", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "POST", "/v1/projects/SEC/messages", []byte(fmt.Sprintf(
		`{"title":"Targeted","body":"hi","to_session":{"actor":"worker","session_id":"w-1"},"issue":%q}`, iss.Key,
	)), "")
	if rec.Code != 201 {
		t.Fatalf("create targeted message = %d %s", rec.Code, rec.Body.String())
	}
	var msg model.Message
	_ = json.Unmarshal(rec.Body.Bytes(), &msg)

	rec = pgDo(h, "GET", fmt.Sprintf("/v1/messages/%d", msg.ID), nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"Targeted"`) {
		t.Fatalf("GET /v1/messages/{id} = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "POST", "/v1/projects/SEC/messages", []byte(fmt.Sprintf(`{"title":"Reply","body":"ack","reply_to":%d}`, msg.ID)), "")
	if rec.Code != 201 {
		t.Fatalf("create reply = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "GET", fmt.Sprintf("/v1/projects/SEC/messages?to_session=worker/w-1&issue=%s", iss.Key), nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("filtered messages = %d %s, want total=1", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "GET", fmt.Sprintf("/v1/projects/SEC/messages?reply_to=%d", msg.ID), nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("reply_to filtered messages = %d %s, want total=1", rec.Code, rec.Body.String())
	}

	// 8. SSE keepalive ping and immediate disconnect upon token revocation.
	ts := httptest.NewServer(h)
	defer ts.Close()

	tok, err := pgs.CreateToken(ctx, pgstore.Trace{Actor: "owner"}, pgstore.CreateTokenInput{
		Actor: "worker", ClientHost: "devbox", Secret: "agb_test_sse_token_secret_123456", CreatedBy: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	sseCtx, sseCancel := context.WithTimeout(ctx, 2*time.Second)
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, "GET", ts.URL+"/v1/events/stream", nil)
	sseReq.Header.Set("Authorization", "Bearer agb_test_sse_token_secret_123456")
	sseReq.Header.Set("X-Actor", "worker")
	sseReq.Header.Set("X-Session-Id", "w-sse")
	sseReq.Header.Set("X-Client-Host", "devbox")
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != 200 {
		t.Fatalf("SSE status = %d", sseResp.StatusCode)
	}

	// Read keepalive ping, then revoke token and assert stream closes promptly.
	buf := make([]byte, 64)
	n, err := sseResp.Body.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), ": ping") {
		t.Fatalf("expected : ping keepalive, got %q err=%v", string(buf[:n]), err)
	}
	if rec := pgDo(h, "DELETE", fmt.Sprintf("/v1/tokens/%d", tok.ID), nil, ""); rec.Code != 200 {
		t.Fatalf("revoke token = %d %s", rec.Code, rec.Body.String())
	}
	eofDone := make(chan error, 1)
	go func() {
		_, rerr := io.ReadAll(sseResp.Body)
		eofDone <- rerr
	}()
	select {
	case rerr := <-eofDone:
		if rerr != nil {
			t.Fatalf("SSE stream read after revoke = %v, want clean EOF", rerr)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("SSE stream stayed open after token revocation")
	}

	// Broker closed -> 503 shutting_down.
	if c, ok := h.(io.Closer); ok {
		_ = c.Close()
	}
	rec = pgDo(h, "GET", "/v1/events/stream", nil, "")
	if rec.Code != 503 || decodeErr(t, rec).Error.Code != "shutting_down" {
		t.Fatalf("stream after broker close = %d %s, want 503 shutting_down", rec.Code, rec.Body.String())
	}
}
