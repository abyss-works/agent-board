package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/api"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func newRealServerHarness(t *testing.T, project string) (*store.Store, *harness) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	const tok = "test-token-0123456789abcdef0123456789"
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}}
	if err := st.EnsureActors(ctx, actors); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(ctx, model.Trace{Actor: "owner", SessionID: "s", ClientHost: "h"}, project, "sample"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(api.Options{
		Token:   tok,
		Actors:  actors,
		Version: "t",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:   st,
	}))
	t.Cleanup(srv.Close)
	h := &harness{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		env: map[string]string{
			"AGB_URL":         srv.URL,
			"AGB_TOKEN":       tok,
			"AGB_ACTOR":       "owner",
			"AGB_SESSION_ID":  "sess-1",
			"AGB_CLIENT_HOST": "desk",
		},
	}
	h.app = &App{
		Version:    "t",
		Getenv:     func(k string) string { return h.env[k] },
		Hostname:   func() (string, error) { return "test-host", nil },
		Now:        func() time.Time { return noon },
		Location:   time.FixedZone("KST", 9*3600),
		ConfigPath: filepath.Join(t.TempDir(), "agb", "config.json"),
		Stdin:      strings.NewReader(""),
		Stdout:     h.stdout,
		Stderr:     h.stderr,
	}
	return st, h
}

func TestDocSyncBatchMutualRelatedAndComponentIsolation(t *testing.T) {
	const (
		nA   = "2026-10-10-1000-decision-alpha.md"
		nB   = "2026-10-10-1001-decision-beta.md"
		nBad = "2026-10-10-1002-decision-broken.md"
	)
	st, h := newRealServerHarness(t, "CLI")
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":     syncConfig,
		"notes/" + nA:   "---\nkind: decision\ntitle: alpha\nrelated: [" + nB + "]\n---\n\n본문 A\n\n## 후속\n\n- 항목 A\n",
		"notes/" + nB:   "---\nkind: decision\ntitle: beta\nrelated: [" + nA + "]\n---\n\n본문 B\n",
		"notes/" + nBad: "---\nkind: decision\ntitle: broken\nrelated: [2026-10-10-9999-decision-missing.md]\n---\n\n본문 C\n",
	})
	commitAll(t, root)

	code := h.run("doc", "sync", "--root", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (rejected C); stderr = %s, stdout = %s", code, h.stderr, h.stdout)
	}
	var got docSyncResult
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal stdout: %v (%s)", err, h.stdout)
	}
	if want := []string{nA, nB}; !reflect.DeepEqual(got.Uploaded, want) {
		t.Fatalf("uploaded = %v, want %v (rejected = %+v)", got.Uploaded, want, got.Rejected)
	}
	if len(got.Rejected) != 1 || got.Rejected[0].Name != nBad || !strings.Contains(got.Rejected[0].Reason, "2026-10-10-9999-decision-missing.md") {
		t.Errorf("rejected = %+v", got.Rejected)
	}
	wantFU := []docFollowUps{{Name: nA, Sections: []followUp{{Section: "후속", Items: []string{"항목 A"}}}}}
	if !reflect.DeepEqual(got.FollowUps, wantFU) {
		t.Errorf("follow_ups = %+v, want %+v", got.FollowUps, wantFU)
	}

	ctx := context.Background()
	docA, err := st.GetDocument(ctx, "CLI", nA)
	if err != nil || !reflect.DeepEqual(docA.Related, []string{nB}) || docA.State != model.StateCurrent {
		t.Errorf("stored A = %+v, %v", docA, err)
	}
	docB, err := st.GetDocument(ctx, "CLI", nB)
	if err != nil || !reflect.DeepEqual(docB.Related, []string{nA}) || docB.State != model.StateCurrent {
		t.Errorf("stored B = %+v, %v", docB, err)
	}
}

func TestDocSyncBatchThreeCycleAndSupersedes(t *testing.T) {
	const (
		n1 = "2026-10-10-1000-decision-one.md"
		n2 = "2026-10-10-1001-decision-two.md"
		n3 = "2026-10-10-1002-decision-three.md"
		s1 = "2026-10-10-1003-decision-old.md"
		s2 = "2026-10-10-1004-decision-new.md"
	)
	st, h := newRealServerHarness(t, "CLI")
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":   syncConfig,
		"notes/" + n1: "---\nkind: decision\ntitle: one\nrelated: [" + n2 + "]\n---\n\n1\n",
		"notes/" + n2: "---\nkind: decision\ntitle: two\nrelated: [" + n3 + "]\n---\n\n2\n",
		"notes/" + n3: "---\nkind: decision\ntitle: three\nrelated: [" + n1 + "]\n---\n\n3\n",
		"notes/" + s1: "---\nkind: decision\ntitle: old\n---\n\nold\n",
		"notes/" + s2: "---\nkind: decision\ntitle: new\nsupersedes: [" + s1 + "]\n---\n\nnew\n",
	})
	commitAll(t, root)

	if code := h.run("doc", "sync", "--root", root); code != 0 {
		t.Fatalf("code = %d stderr = %s stdout = %s", code, h.stderr, h.stdout)
	}
	var got docSyncResult
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := []string{n1, n2, n3, s1, s2}; !reflect.DeepEqual(got.Uploaded, want) {
		t.Errorf("uploaded = %v, want %v", got.Uploaded, want)
	}
	oldDoc, err := st.GetDocument(context.Background(), "CLI", s1)
	if err != nil || oldDoc.State != model.StateSuperseded || !reflect.DeepEqual(oldDoc.SupersededBy, []string{s2}) {
		t.Errorf("oldDoc = %+v, %v", oldDoc, err)
	}
}

func TestDocSyncBatchFallsBackOnOlderServer(t *testing.T) {
	const (
		nA = "2026-10-10-1000-decision-alpha.md"
		nB = "2026-10-10-1001-decision-beta.md"
	)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":   syncConfig,
		"notes/" + nA: "---\nkind: decision\ntitle: alpha\nrelated: [" + nB + "]\n---\n\nA\n",
		"notes/" + nB: "---\nkind: decision\ntitle: beta\nrelated: [" + nA + "]\n---\n\nB\n",
	})
	commitAll(t, root)

	// docSyncServer returns 404 for POST /v1/projects/CLI/documents:batch, simulating an older server.
	srv := docSyncServer(t, nil, map[string]canned{
		putPath(nA): {status: 422, body: `{"error":{"code":"validation_failed","message":"x","details":[{"field":"related[0]","reason":"unresolved","message":"대상 문서가 없습니다."}]}}`},
		putPath(nB): {status: 422, body: `{"error":{"code":"validation_failed","message":"x","details":[{"field":"related[0]","reason":"unresolved","message":"대상 문서가 없습니다."}]}}`},
	})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 1 {
		t.Fatalf("code = %d stderr = %s", code, h.stderr)
	}
	var got docSyncResult
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rejected) != 2 || got.Rejected[0].Name != nA || got.Rejected[1].Name != nB {
		t.Errorf("fallback rejected = %+v", got.Rejected)
	}
}
