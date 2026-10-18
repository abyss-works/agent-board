package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeSource writes Content to the snapshot path and reports Fingerprint.
type fakeSource struct {
	Content     string
	Fingerprint string
	Snapshots   int
}

func (f *fakeSource) Snapshot(_ context.Context, dst string) error {
	f.Snapshots++
	return os.WriteFile(dst, []byte(f.Content), 0o600)
}

func (f *fakeSource) FingerprintNow(context.Context) (string, error) { return f.Fingerprint, nil }

type failingStore struct {
	*Memory
	fail bool
}

func (s *failingStore) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	if s.fail {
		return errors.New("upload failed")
	}
	return s.Memory.Put(ctx, name, r, size)
}

func newRunner(t *testing.T, store ObjectStore, src Source) (*Runner, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	r := &Runner{
		Store: store, Source: src, Prefix: "agb", Interval: time.Hour, Retention: 672 * time.Hour,
		TempDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now },
	}
	return r, &now
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestObjectNameRoundTrip(t *testing.T) {
	ts := time.Date(2026, 10, 17, 9, 5, 59, 0, time.UTC)
	name := ObjectName("agb", ts)
	if name != "agb/agb-20261017T0905Z.db.gz" {
		t.Fatalf("ObjectName = %q", name)
	}
	got, ok := ParseObjectTime("agb", name)
	if !ok || !got.Equal(ts.Truncate(time.Minute)) {
		t.Fatalf("ParseObjectTime = %v %v", got, ok)
	}
	for _, bad := range []string{"agb/other.db.gz", "agb/agb-2026.db.gz", "x/agb-20261017T0905Z.db.gz", "agb/agb-20261317T0905Z.db.gz"} {
		if _, ok := ParseObjectTime("agb", bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestRunOnceUploadsCompressedSnapshot(t *testing.T) {
	store := NewMemory()
	src := &fakeSource{Content: strings.Repeat("sqlite page data ", 1000), Fingerprint: "f1"}
	r, _ := newRunner(t, store, src)
	res, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Uploaded || res.Object != "agb/agb-20261017T0900Z.db.gz" {
		t.Fatalf("result = %+v", res)
	}
	objs, _ := store.List(context.Background(), "agb/")
	if len(objs) != 1 || objs[0].Name != res.Object {
		t.Fatalf("objects = %+v", objs)
	}
	data := store.Get(res.Object)
	if int64(len(data)) != objs[0].Size || len(data) >= len(src.Content) {
		t.Errorf("stored %d bytes for %d bytes of content (not compressed?)", len(data), len(src.Content))
	}
	if gunzip(t, data) != src.Content {
		t.Error("decompressed content differs from snapshot")
	}
	entries, _ := os.ReadDir(r.TempDir)
	if len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestSkipsWhenUnchanged(t *testing.T) {
	store := NewMemory()
	src := &fakeSource{Content: "db", Fingerprint: "f1"}
	r, now := newRunner(t, store, src)
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	res, err := r.RunOnce(context.Background())
	if err != nil || !res.Skipped || res.Uploaded {
		t.Fatalf("unchanged run = %+v %v", res, err)
	}
	if src.Snapshots != 1 {
		t.Errorf("snapshot taken on unchanged run: %d", src.Snapshots)
	}
	src.Fingerprint = "f2"
	*now = now.Add(time.Hour)
	res, err = r.RunOnce(context.Background())
	if err != nil || !res.Uploaded || res.Object != "agb/agb-20261017T1100Z.db.gz" {
		t.Fatalf("changed run = %+v %v", res, err)
	}
	objs, _ := store.List(context.Background(), "agb/")
	if len(objs) != 2 {
		t.Errorf("objects = %d", len(objs))
	}
}

func TestPrunesExpiredObjects(t *testing.T) {
	store := NewMemory()
	src := &fakeSource{Content: "db", Fingerprint: "f1"}
	r, now := newRunner(t, store, src)
	ctx := context.Background()
	old := ObjectName("agb", now.Add(-r.Retention-time.Minute))
	edge := ObjectName("agb", now.Add(-r.Retention+time.Minute))
	store.Put(ctx, old, strings.NewReader("x"), 1)
	store.Put(ctx, edge, strings.NewReader("x"), 1)
	store.Put(ctx, "agb/manual-copy.db.gz", strings.NewReader("x"), 1)
	store.Put(ctx, "other/agb-20200101T0000Z.db.gz", strings.NewReader("x"), 1)
	res, err := r.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != old {
		t.Errorf("deleted = %v, want [%s]", res.Deleted, old)
	}
	names := map[string]bool{}
	for _, o := range store.All() {
		names[o.Name] = true
	}
	if names[old] || !names[edge] || !names["agb/manual-copy.db.gz"] || !names["other/agb-20200101T0000Z.db.gz"] || !names[res.Object] {
		t.Errorf("remaining = %v", names)
	}
}

func TestUploadFailureIsRetriedNextRun(t *testing.T) {
	store := &failingStore{Memory: NewMemory(), fail: true}
	src := &fakeSource{Content: "db", Fingerprint: "f1"}
	r, now := newRunner(t, store, src)
	if _, err := r.RunOnce(context.Background()); err == nil {
		t.Fatal("expected upload error")
	}
	store.fail = false
	*now = now.Add(time.Hour)
	res, err := r.RunOnce(context.Background())
	if err != nil || !res.Uploaded {
		t.Fatalf("retry = %+v %v (fingerprint must not be recorded after a failure)", res, err)
	}
}

func TestRunLoopUploadsAndStops(t *testing.T) {
	store := NewMemory()
	src := &fakeSource{Content: "db", Fingerprint: "f1"}
	r, _ := newRunner(t, store, src)
	r.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(store.All()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	if len(store.All()) == 0 {
		t.Fatal("no upload happened")
	}
}
