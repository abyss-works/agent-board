package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventStreamReusesTransportWithoutTimeoutAndDecodesErrors(t *testing.T) {
	var gotAccept, gotAuth, gotActor, gotSession, gotHost, gotLastID, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotAuth = r.Header.Get("Authorization")
		gotActor = r.Header.Get("X-Actor")
		gotSession = r.Header.Get("X-Session-Id")
		gotHost = r.Header.Get("X-Client-Host")
		gotLastID = r.Header.Get("Last-Event-ID")
		gotQuery = r.URL.RawQuery

		if r.URL.Query().Get("project") == "ERR" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":"unauthorized","message":"bad token"}}`)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fl.Flush()
		// Wait longer than c.HTTP.Timeout (40ms) to prove Stream sets Timeout: 0.
		time.Sleep(80 * time.Millisecond)
		fmt.Fprint(w, "id: 1\nevent: issue.created\ndata: {\"id\":1}\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	c := &Client{
		BaseURL:    srv.URL,
		Token:      "tok-secret",
		Actor:      "owner",
		SessionID:  "s1",
		ClientHost: "desk",
		Version:    "test",
		HTTP:       &http.Client{Transport: srv.Client().Transport, Timeout: 40 * time.Millisecond},
	}

	after := int64(5)
	resp, err := c.EventStream(context.Background(), WatchParams{
		Project:     "APP",
		Kinds:       []string{"issue.", "message.created"},
		After:       &after,
		LastEventID: "7",
	})
	if err != nil {
		t.Fatalf("EventStream: %v", err)
	}
	defer resp.Body.Close()
	if gotAccept != "text/event-stream" || gotAuth != "Bearer tok-secret" || gotActor != "owner" || gotSession != "s1" || gotHost != "desk" || gotLastID != "7" {
		t.Errorf("headers: accept=%q auth=%q actor=%q session=%q host=%q last=%q", gotAccept, gotAuth, gotActor, gotSession, gotHost, gotLastID)
	}
	if !strings.Contains(gotQuery, "project=APP") || !strings.Contains(gotQuery, "after=5") || !strings.Contains(gotQuery, "kind=issue.%2Cmessage.created") {
		t.Errorf("query = %q", gotQuery)
	}
	sc := bufio.NewScanner(resp.Body)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan stream: %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), `data: {"id":1}`) {
		t.Errorf("stream lines = %v", lines)
	}

	// Non-2xx returns decoded *Error.
	_, err = c.EventStream(context.Background(), WatchParams{Project: "ERR"})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Code != "unauthorized" {
		t.Errorf("error = %v, want 401 unauthorized", err)
	}
}
