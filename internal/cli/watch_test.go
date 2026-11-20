package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/api"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func newWatchHarness(t *testing.T, terminal bool) (*store.Store, *harness) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "watch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	const tok = "test-token-0123456789abcdef0123456789"
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}}
	if err := st.EnsureActors(context.Background(), actors); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(context.Background(), model.Trace{Actor: "owner", SessionID: "s0", ClientHost: "h"}, "APP", "Example App"); err != nil {
		t.Fatal(err)
	}
	handler := api.New(api.Options{
		Token:           tok,
		Actors:          actors,
		Version:         "t",
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:           st,
		SSEPingInterval: 20 * time.Millisecond,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		if c, ok := handler.(io.Closer); ok {
			_ = c.Close()
		}
		srv.Close()
	})
	h := &harness{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		env: map[string]string{
			"AGB_URL":         srv.URL,
			"AGB_TOKEN":       tok,
			"AGB_ACTOR":       "owner",
			"AGB_SESSION_ID":  "watch-sess",
			"AGB_CLIENT_HOST": "desk",
		},
	}
	h.app = &App{
		Version:          "t",
		Getenv:           func(k string) string { return h.env[k] },
		Hostname:         func() (string, error) { return "desk", nil },
		Now:              func() time.Time { return noon },
		Location:         time.UTC,
		ConfigPath:       filepath.Join(t.TempDir(), "agb", "config.json"),
		HTTP:             &http.Client{Timeout: 30 * time.Second},
		Stdin:            strings.NewReader(""),
		Stdout:           h.stdout,
		Stderr:           h.stderr,
		StdoutIsTerminal: terminal,
	}
	return st, h
}

func TestWatchReceivesLiveEventImmediately(t *testing.T) {
	st, h := newWatchHarness(t, false)

	done := make(chan int, 1)
	go func() {
		done <- h.app.Run(context.Background(), []string{"watch", "-p", "APP", "--kind", "issue.", "--timeout", "5s"})
	}()

	// Give watch a brief moment to connect, then write an issue.
	time.Sleep(60 * time.Millisecond)
	if _, err := st.CreateIssue(context.Background(), model.Trace{Actor: "owner", SessionID: "writer", ClientHost: "desk"}, store.IssueInput{
		Project: "APP",
		Title:   "live event test",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, stderr=%q", code, h.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agb watch did not exit after event arrived")
	}

	var ev model.Event
	if err := json.Unmarshal(h.stdout.Bytes(), &ev); err != nil {
		t.Fatalf("stdout not JSON (%q): %v", h.stdout.String(), err)
	}
	if ev.Kind != "issue.created" || ev.Subject != "APP-1" {
		t.Errorf("event = %+v, want issue.created APP-1", ev)
	}
}

func TestWatchAfterAndOutputModes(t *testing.T) {
	st, h := newWatchHarness(t, false)
	if _, err := st.CreateIssue(context.Background(), model.Trace{Actor: "owner", SessionID: "writer", ClientHost: "desk"}, store.IssueInput{
		Project: "APP",
		Title:   "existing event",
	}); err != nil {
		t.Fatal(err)
	}

	// --after 0 receives existing matching event immediately.
	code := h.app.Run(context.Background(), []string{"watch", "--project", "APP", "--kind", "issue.", "--after", "0", "--timeout", "2s"})
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.stderr.String())
	}
	var ev model.Event
	if err := json.Unmarshal(h.stdout.Bytes(), &ev); err != nil || ev.Subject != "APP-1" {
		t.Fatalf("json out=%q err=%v", h.stdout.String(), err)
	}

	// --fields + --text mode.
	h.stdout.Reset()
	h.stderr.Reset()
	code = h.app.Run(context.Background(), []string{"watch", "--kind", "issue.", "--after", "0", "--fields", "kind,subject", "--text"})
	if code != 0 || h.stdout.String() != "issue.created\tAPP-1\n" {
		t.Errorf("fields/text code=%d out=%q err=%q", code, h.stdout.String(), h.stderr.String())
	}

	// --table mode.
	h.stdout.Reset()
	h.stderr.Reset()
	code = h.app.Run(context.Background(), []string{"watch", "--kind", "issue.", "--after", strconv.FormatInt(ev.ID-1, 10), "--table"})
	if code != 0 {
		t.Fatalf("table code=%d stderr=%q", code, h.stderr.String())
	}
	for _, want := range []string{"issue.created", "APP-1", "APP", "owner"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("table output missing %q:\n%s", want, h.stdout.String())
		}
	}
}

func TestWatchTimeoutExitsZeroWithEmptyStdout(t *testing.T) {
	_, h := newWatchHarness(t, false)
	code := h.app.Run(context.Background(), []string{"watch", "-p", "APP", "--kind", "issue.", "--timeout", "50ms"})
	if code != 0 {
		t.Errorf("timeout exit code = %d, want 0 (stderr=%q)", code, h.stderr.String())
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout on timeout = %q, want empty", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "시간 초과") {
		t.Errorf("stderr on timeout = %q, want timeout notice", h.stderr.String())
	}
}

func TestWatchUsageAndServerErrors(t *testing.T) {
	_, h := newWatchHarness(t, false)
	for _, args := range [][]string{
		{"watch", "--timeout", "0s"},
		{"watch", "--timeout", "-5s"},
		{"watch", "--timeout", "notaduration"},
		{"watch", "--after", "-1"},
		{"watch", "--after", "abc"},
		{"watch", "extra-arg"},
	} {
		h.stdout.Reset()
		h.stderr.Reset()
		if code := h.app.Run(context.Background(), args); code != 2 {
			t.Errorf("args %v: code=%d, want 2 (stderr=%q)", args, code, h.stderr.String())
		}
	}

	// Auth error -> exit code 1.
	h.env["AGB_TOKEN"] = "wrong-token"
	h.stdout.Reset()
	h.stderr.Reset()
	if code := h.app.Run(context.Background(), []string{"watch", "--timeout", "1s"}); code != 1 {
		t.Errorf("auth error code=%d, want 1 (stderr=%q)", code, h.stderr.String())
	}
	if h.stdout.Len() != 0 || h.stderr.Len() == 0 {
		t.Errorf("auth error out=%q err=%q", h.stdout.String(), h.stderr.String())
	}
}

func TestWatchCanceledContextExitsOne(t *testing.T) {
	_, h := newWatchHarness(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	code := h.app.Run(ctx, []string{"watch", "--timeout", "5s"})
	if code != 1 {
		t.Errorf("canceled exit code = %d, want 1 (stderr=%q)", code, h.stderr.String())
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout on cancel = %q, want empty", h.stdout.String())
	}
}

func TestUsageListsWatch(t *testing.T) {
	if !strings.Contains(Usage, "\n  watch ") {
		t.Errorf("Usage does not list watch")
	}
}
