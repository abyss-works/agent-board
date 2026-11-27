package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

type eventsResponse struct {
	Items     []model.Event `json:"items"`
	NextAfter int64         `json:"next_after"`
}

func TestListEventsEndpoint(t *testing.T) {
	e := newEnv(t)

	// Auth and trace checks.
	if rec := e.do(t, "GET", "/v1/events", "", map[string]string{"Authorization": "Bearer wrong"}); rec.Code != 401 {
		t.Errorf("wrong token = %d, want 401", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/events", "", map[string]string{"X-Actor": "-"}); rec.Code != 400 {
		t.Errorf("missing actor = %d, want 400", rec.Code)
	}
	if rec := e.do(t, "POST", "/v1/events", "", nil); rec.Code != 405 {
		t.Errorf("POST = %d, want 405", rec.Code)
	}

	// Empty project filter returns [] (never null).
	rec := e.do(t, "GET", "/v1/events?project=NOPE&after=10", "", nil)
	if rec.Code != 200 {
		t.Fatalf("empty list status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("expected empty array, got %s", rec.Body.String())
	}
	var empty eventsResponse
	decodeJSON(t, rec, &empty)
	if empty.NextAfter != 10 {
		t.Errorf("empty next_after = %d, want 10", empty.NextAfter)
	}

	// Query validation errors -> 400 invalid_query.
	for _, q := range []string{
		"?after=-1",
		"?after=abc",
		"?limit=0",
		"?limit=501",
		"?limit=abc",
		"?project=bad-key",
		"?unknown=1",
	} {
		wantErr(t, e.do(t, "GET", "/v1/events"+q, "", nil), 400, "bad_request", "invalid_query")
	}

	// Create projects and issues via API.
	owner := map[string]string{"X-Actor": "owner"}
	if r := e.do(t, "POST", "/v1/projects", `{"key":"AAA","name":"App A"}`, owner); r.Code != 201 {
		t.Fatalf("create AAA: %d %s", r.Code, r.Body.String())
	}
	if r := e.do(t, "POST", "/v1/projects", `{"key":"BBB","name":"App B"}`, owner); r.Code != 201 {
		t.Fatalf("create BBB: %d %s", r.Code, r.Body.String())
	}
	if r := e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"first"}`, owner); r.Code != 201 {
		t.Fatalf("create issue: %d %s", r.Code, r.Body.String())
	}
	if r := e.do(t, "POST", "/v1/projects/AAA/messages", `{"title":"hi","body":"hello"}`, owner); r.Code != 201 {
		t.Fatalf("create msg: %d %s", r.Code, r.Body.String())
	}

	// Filter by project and kind prefixes.
	rec = e.do(t, "GET", "/v1/events?project=AAA&kind=issue.,message.created&limit=10", "", nil)
	if rec.Code != 200 {
		t.Fatalf("filtered status = %d: %s", rec.Code, rec.Body.String())
	}
	var got eventsResponse
	decodeJSON(t, rec, &got)
	if len(got.Items) != 2 {
		t.Fatalf("items = %d (%+v), want 2", len(got.Items), got.Items)
	}
	if got.Items[0].Kind != "issue.created" || got.Items[1].Kind != "message.created" {
		t.Errorf("kinds = %s, %s", got.Items[0].Kind, got.Items[1].Kind)
	}
	if got.NextAfter != got.Items[1].ID {
		t.Errorf("next_after = %d, want %d", got.NextAfter, got.Items[1].ID)
	}

	// Paginate using after.
	rec = e.do(t, "GET", "/v1/events?project=AAA&kind=issue.,message.created&after="+strconv.FormatInt(got.Items[0].ID, 10), "", nil)
	var page2 eventsResponse
	decodeJSON(t, rec, &page2)
	if len(page2.Items) != 1 || page2.Items[0].ID != got.Items[1].ID {
		t.Errorf("page2 = %+v, want only second item", page2)
	}
}

type sseFrame struct {
	ID    string
	Event string
	Data  string
}

func readSSELine(t *testing.T, r *bufio.Reader, timeout time.Duration) (string, error) {
	t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		l, err := r.ReadString('\n')
		ch <- res{l, err}
	}()
	select {
	case out := <-ch:
		return out.line, out.err
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for SSE line")
		return "", nil
	}
}

func readSSEEvent(t *testing.T, r *bufio.Reader, timeout time.Duration) sseFrame {
	t.Helper()
	var f sseFrame
	for {
		line, err := readSSELine(t, r, timeout)
		if err != nil {
			t.Fatalf("SSE read error: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if f.Data != "" {
				return f
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "id:"):
			f.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			f.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(line, "data:")
			d = strings.TrimPrefix(d, " ")
			f.Data = d
		}
	}
}

func openStream(t *testing.T, baseURL, path string, headers map[string]string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+path, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "stream-sess")
	req.Header.Set("X-Client-Host", "test-host")
	for k, v := range headers {
		if v == "-" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return resp, cancel
}

func TestEventStreamValidationAndAuth(t *testing.T) {
	e := newEnv(t)
	if rec := e.do(t, "GET", "/v1/events/stream", "", map[string]string{"Authorization": "Bearer wrong"}); rec.Code != 401 {
		t.Errorf("wrong token = %d, want 401", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/events/stream", "", map[string]string{"X-Actor": "-"}); rec.Code != 400 {
		t.Errorf("missing actor = %d, want 400", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/events/stream", "", map[string]string{"X-Session-Id": "-"}); rec.Code != 400 {
		t.Errorf("missing session = %d, want 400", rec.Code)
	}
	wantErr(t, e.do(t, "GET", "/v1/events/stream", "", map[string]string{"Last-Event-ID": "-1"}), 400, "bad_request", "invalid_header")
	wantErr(t, e.do(t, "GET", "/v1/events/stream", "", map[string]string{"Last-Event-ID": "notanint"}), 400, "bad_request", "invalid_header")
	wantErr(t, e.do(t, "GET", "/v1/events/stream?after=-5", "", nil), 400, "bad_request", "invalid_query")
	wantErr(t, e.do(t, "GET", "/v1/events/stream?after=abc", "", nil), 400, "bad_request", "invalid_query")
}

func TestEventStreamReplayPrecedenceAndLive(t *testing.T) {
	e := newEnvWith(t, testActors, func(o *Options) {
		o.SSEPingInterval = 30 * time.Millisecond
	})
	srv := httptest.NewServer(e.h)
	t.Cleanup(func() {
		if c, ok := e.h.(io.Closer); ok {
			_ = c.Close()
		}
		srv.Close()
	})

	// Seed 2 events: project AAA + issue AAA-1 + issue AAA-2.
	owner := map[string]string{"X-Actor": "owner"}
	if r := e.do(t, "POST", "/v1/projects", `{"key":"AAA","name":"App A"}`, owner); r.Code != 201 {
		t.Fatalf("create project: %d", r.Code)
	}
	if r := e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"first"}`, owner); r.Code != 201 {
		t.Fatalf("create issue 1: %d", r.Code)
	}
	if r := e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"second"}`, owner); r.Code != 201 {
		t.Fatalf("create issue 2: %d", r.Code)
	}
	var all eventsResponse
	decodeJSON(t, e.do(t, "GET", "/v1/events?project=AAA&kind=issue.", "", nil), &all)
	if len(all.Items) != 2 {
		t.Fatalf("seeded issue events = %d, want 2", len(all.Items))
	}
	ev1ID, ev2ID := all.Items[0].ID, all.Items[1].ID

	// 1. Last-Event-ID takes precedence over after (after=0, Last-Event-ID=ev1ID -> receives ev2, NOT ev1).
	resp, cancel := openStream(t, srv.URL, "/v1/events/stream?after=0&project=AAA&kind=issue.", map[string]string{
		"Last-Event-ID": strconv.FormatInt(ev1ID, 10),
	})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if xab := resp.Header.Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("X-Accel-Buffering = %q", xab)
	}
	rd := bufio.NewReader(resp.Body)
	frame := readSSEEvent(t, rd, 2*time.Second)
	if frame.ID != strconv.FormatInt(ev2ID, 10) || frame.Event != "issue.created" {
		t.Errorf("replayed frame = %+v, want id=%d event=issue.created", frame, ev2ID)
	}
	var ev model.Event
	if err := json.Unmarshal([]byte(frame.Data), &ev); err != nil || ev.Subject != "AAA-2" {
		t.Errorf("replayed data = %q err=%v", frame.Data, err)
	}
	// Wait for keepalive : ping comment.
	gotPing := false
	for range 5 {
		line, err := readSSELine(t, rd, 2*time.Second)
		if err != nil {
			t.Fatalf("waiting for ping: %v", err)
		}
		if strings.TrimRight(line, "\r\n") == ": ping" {
			gotPing = true
			break
		}
	}
	if !gotPing {
		t.Errorf("did not receive : ping keepalive")
	}
	cancel()

	// 2. No cursor (no after, no Last-Event-ID) receives only events created AFTER connecting.
	resp2, cancel2 := openStream(t, srv.URL, "/v1/events/stream?project=AAA&kind=issue.", nil)
	defer func() {
		cancel2()
		resp2.Body.Close()
	}()
	if resp2.StatusCode != 200 {
		t.Fatalf("live stream status = %d", resp2.StatusCode)
	}
	rd2 := bufio.NewReader(resp2.Body)
	// Create AAA-3 after connecting.
	if r := e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"third"}`, owner); r.Code != 201 {
		t.Fatalf("create issue 3: %d", r.Code)
	}
	frame3 := readSSEEvent(t, rd2, 2*time.Second)
	var ev3 model.Event
	if err := json.Unmarshal([]byte(frame3.Data), &ev3); err != nil {
		t.Fatalf("unmarshal live event: %v", err)
	}
	if ev3.Subject != "AAA-3" || ev3.ID <= ev2ID {
		t.Errorf("live stream received %+v, want AAA-3 (id > %d)", ev3, ev2ID)
	}
	cancel2()
	resp2.Body.Close()

	// 3. Stats separates /v1/events/stream from routes into streams.
	// Wait briefly for handler defer to finish recording stream completion.
	time.Sleep(30 * time.Millisecond)
	statsRec := e.do(t, "GET", "/v1/stats", "", nil)
	if statsRec.Code != 200 {
		t.Fatalf("stats status = %d", statsRec.Code)
	}
	var st struct {
		Routes  []RouteStats  `json:"routes"`
		Streams []StreamStats `json:"streams"`
	}
	decodeJSON(t, statsRec, &st)
	for _, r := range st.Routes {
		if r.Route == "/v1/events/stream" {
			t.Errorf("/v1/events/stream leaked into routes: %+v", r)
		}
	}
	foundStream := false
	for _, s := range st.Streams {
		if s.Route == "/v1/events/stream" && s.Method == "GET" {
			foundStream = true
			if s.Requests < 2 {
				t.Errorf("stream requests = %d, want >= 2", s.Requests)
			}
		}
	}
	if !foundStream {
		t.Errorf("/v1/events/stream missing from streams: %+v", st.Streams)
	}
}

func TestEventStreamInitialReadFailureReturnsJSONError(t *testing.T) {
	e := newEnv(t)
	if _, err := e.st.DB().Exec(`drop table event`); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/events/stream?after=0", "", nil)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if eb := decodeErr(t, rec); eb.Error.Code != "internal" {
		t.Errorf("error code = %q, want internal", eb.Error.Code)
	}
	var st struct {
		Streams []StreamStats `json:"streams"`
	}
	decodeJSON(t, e.do(t, "GET", "/v1/stats", "", nil), &st)
	if len(st.Streams) != 1 || st.Streams[0].Requests != 1 || st.Streams[0].Errors != 1 || st.Streams[0].Active != 0 {
		t.Errorf("failed stream stats = %+v, want Requests=1 Errors=1 Active=0", st.Streams)
	}
}

func waitStreamEOF(t *testing.T, r io.Reader, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, r)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream ended with error %v, want clean EOF", err)
		}
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for SSE stream to close")
	}
}

func TestEventStreamConnectionLimits(t *testing.T) {
	if b := newEventBroker(0, 0); b.maxTotal != defaultMaxSSEConnections || b.maxPerActor != defaultMaxSSEConnectionsPerActor {
		t.Fatalf("default limits = (%d, %d), want (%d, %d)", b.maxTotal, b.maxPerActor, defaultMaxSSEConnections, defaultMaxSSEConnectionsPerActor)
	}

	e := newEnvWith(t, loginActors, func(o *Options) {
		o.MaxSSEConnections = 3
		o.MaxSSEConnectionsPerActor = 2
	})
	srv := httptest.NewServer(e.h)
	t.Cleanup(func() {
		if c, ok := e.h.(io.Closer); ok {
			_ = c.Close()
		}
		srv.Close()
	})

	claude := e.login(t, "claude", "desk", "owner")

	// Open 2 streams as owner (reaches per-actor cap = 2).
	r1, c1 := openStream(t, srv.URL, "/v1/events/stream", map[string]string{"X-Actor": "owner"})
	defer func() { c1(); r1.Body.Close() }()
	if r1.StatusCode != 200 {
		t.Fatalf("owner stream 1 = %d", r1.StatusCode)
	}
	r2, c2 := openStream(t, srv.URL, "/v1/events/stream", map[string]string{"X-Actor": "owner"})
	defer func() { c2(); r2.Body.Close() }()
	if r2.StatusCode != 200 {
		t.Fatalf("owner stream 2 = %d", r2.StatusCode)
	}

	// 3rd stream for owner is rejected with 429 too_many_requests.
	rOverActor, cOverActor := openStream(t, srv.URL, "/v1/events/stream", map[string]string{"X-Actor": "owner"})
	defer func() { cOverActor(); rOverActor.Body.Close() }()
	if rOverActor.StatusCode != 429 {
		t.Fatalf("owner stream 3 status = %d, want 429", rOverActor.StatusCode)
	}
	var ebActor errorBody
	if err := json.NewDecoder(rOverActor.Body).Decode(&ebActor); err != nil || ebActor.Error.Code != "too_many_requests" {
		t.Fatalf("owner stream 3 error = %+v (err=%v), want too_many_requests", ebActor, err)
	}

	// Another actor can still open a stream up to the global cap = 3.
	r3, c3 := openStream(t, srv.URL, "/v1/events/stream", withToken(claude.Secret, "claude"))
	defer func() { c3(); r3.Body.Close() }()
	if r3.StatusCode != 200 {
		t.Fatalf("claude stream 1 = %d", r3.StatusCode)
	}

	// 4th global stream is rejected with 429 too_many_requests even though claude only has 1.
	rOverGlobal, cOverGlobal := openStream(t, srv.URL, "/v1/events/stream", withToken(claude.Secret, "claude"))
	defer func() { cOverGlobal(); rOverGlobal.Body.Close() }()
	if rOverGlobal.StatusCode != 429 {
		t.Fatalf("claude stream 2 status = %d, want 429", rOverGlobal.StatusCode)
	}
	var ebGlobal errorBody
	if err := json.NewDecoder(rOverGlobal.Body).Decode(&ebGlobal); err != nil || ebGlobal.Error.Code != "too_many_requests" {
		t.Fatalf("claude stream 2 error = %+v (err=%v), want too_many_requests", ebGlobal, err)
	}

	// Closing one owner stream frees a slot so claude can connect.
	c1()
	r1.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r4, c4 := openStream(t, srv.URL, "/v1/events/stream", withToken(claude.Secret, "claude"))
		if r4.StatusCode == 200 {
			c4()
			r4.Body.Close()
			break
		}
		c4()
		r4.Body.Close()
		if time.Now().After(deadline) {
			t.Fatalf("slot was not freed after closing stream (last status=%d)", r4.StatusCode)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func TestEventStreamDisconnectedOnTokenRevocation(t *testing.T) {
	e := newEnvWith(t, loginActors, func(o *Options) {
		o.SSEPingInterval = time.Hour
	})
	srv := httptest.NewServer(e.h)
	t.Cleanup(func() {
		if c, ok := e.h.(io.Closer); ok {
			_ = c.Close()
		}
		srv.Close()
	})

	owner := map[string]string{"X-Actor": "owner"}
	if r := e.do(t, "POST", "/v1/projects", `{"key":"AAA","name":"App A"}`, owner); r.Code != 201 {
		t.Fatalf("create project: %d", r.Code)
	}

	// Keep a shared-token stream open throughout to verify it is never disconnected.
	sharedResp, sharedCancel := openStream(t, srv.URL, "/v1/events/stream?project=AAA&kind=issue.", owner)
	defer func() { sharedCancel(); sharedResp.Body.Close() }()
	if sharedResp.StatusCode != 200 {
		t.Fatalf("shared stream = %d", sharedResp.StatusCode)
	}
	sharedRd := bufio.NewReader(sharedResp.Body)

	// 1. DELETE /v1/tokens/{id} closes the stream authenticated with that token.
	tokByID := e.login(t, "claude", "desk", "owner")
	otherActor := e.login(t, "codex", "desk", "owner")

	respByID, cancelByID := openStream(t, srv.URL, "/v1/events/stream", withToken(tokByID.Secret, "claude"))
	defer func() { cancelByID(); respByID.Body.Close() }()
	if respByID.StatusCode != 200 {
		t.Fatalf("tokByID stream = %d", respByID.StatusCode)
	}
	otherResp, otherCancel := openStream(t, srv.URL, "/v1/events/stream?project=AAA&kind=issue.", withToken(otherActor.Secret, "codex"))
	defer func() { otherCancel(); otherResp.Body.Close() }()
	if otherResp.StatusCode != 200 {
		t.Fatalf("otherActor stream = %d", otherResp.StatusCode)
	}
	otherRd := bufio.NewReader(otherResp.Body)

	if rec := e.do(t, "DELETE", "/v1/tokens/"+strconv.FormatInt(tokByID.ID, 10), "", owner); rec.Code != 200 {
		t.Fatalf("revoke by id: %d %s", rec.Code, rec.Body.String())
	}
	waitStreamEOF(t, respByID.Body, 2*time.Second)

	// 2. DELETE /v1/tokens/current closes the stream authenticated with that token.
	tokCurrent := e.login(t, "claude", "laptop", "owner")
	respCurrent, cancelCurrent := openStream(t, srv.URL, "/v1/events/stream", withToken(tokCurrent.Secret, "claude"))
	defer func() { cancelCurrent(); respCurrent.Body.Close() }()
	if respCurrent.StatusCode != 200 {
		t.Fatalf("tokCurrent stream = %d", respCurrent.StatusCode)
	}
	if rec := e.do(t, "DELETE", "/v1/tokens/current", "", withToken(tokCurrent.Secret, "claude")); rec.Code != 200 {
		t.Fatalf("revoke current: %d %s", rec.Code, rec.Body.String())
	}
	waitStreamEOF(t, respCurrent.Body, 2*time.Second)

	// 3. Re-issuing a token for the same (actor, client_host) closes the replaced token's stream,
	// while keeping the stream for another device open.
	tokOld := e.login(t, "claude", "host-a", "owner")
	tokOtherHost := e.login(t, "claude", "host-b", "owner")
	respOld, cancelOld := openStream(t, srv.URL, "/v1/events/stream", withToken(tokOld.Secret, "claude"))
	defer func() { cancelOld(); respOld.Body.Close() }()
	if respOld.StatusCode != 200 {
		t.Fatalf("tokOld stream = %d", respOld.StatusCode)
	}
	respOtherHost, cancelOtherHost := openStream(t, srv.URL, "/v1/events/stream?project=AAA&kind=issue.", withToken(tokOtherHost.Secret, "claude"))
	defer func() { cancelOtherHost(); respOtherHost.Body.Close() }()
	if respOtherHost.StatusCode != 200 {
		t.Fatalf("tokOtherHost stream = %d", respOtherHost.StatusCode)
	}
	otherHostRd := bufio.NewReader(respOtherHost.Body)

	_ = e.login(t, "claude", "host-a", "owner")
	waitStreamEOF(t, respOld.Body, 2*time.Second)

	// Unrelated streams (shared token, other actor, other device) still receive live events.
	if r := e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"still alive"}`, owner); r.Code != 201 {
		t.Fatalf("create issue: %d", r.Code)
	}
	for name, rd := range map[string]*bufio.Reader{
		"shared":     sharedRd,
		"otherActor": otherRd,
		"otherHost":  otherHostRd,
	} {
		f := readSSEEvent(t, rd, 2*time.Second)
		if f.Event != "issue.created" {
			t.Errorf("%s stream got event %q, want issue.created", name, f.Event)
		}
	}
}
