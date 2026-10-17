// Package backup takes periodic consistent snapshots of the database,
// compresses them and keeps a bounded window of them in an object store.
// The object store sits behind ObjectStore so tests run against Memory and
// the real S3-compatible adapter can be added without touching the runner.
package backup

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Object is one stored backup.
type Object struct {
	Name string
	Size int64
}

// ObjectStore is the minimal S3-like surface the runner needs.
type ObjectStore interface {
	Put(ctx context.Context, name string, r io.Reader, size int64) error
	List(ctx context.Context, prefix string) ([]Object, error)
	Delete(ctx context.Context, name string) error
}

// Source is the database side: a consistent snapshot and a fingerprint
// that changes whenever the database content changes.
type Source interface {
	Snapshot(ctx context.Context, dst string) error
	FingerprintNow(ctx context.Context) (string, error)
}

// Runner performs one backup cycle per interval.
type Runner struct {
	Store     ObjectStore
	Source    Source
	Prefix    string
	Interval  time.Duration
	Retention time.Duration
	TempDir   string
	Logger    *slog.Logger
	Now       func() time.Time

	lastFingerprint string
	ran             bool
}

// Result describes what one cycle did.
type Result struct {
	Uploaded bool
	Skipped  bool
	Object   string
	Deleted  []string
}

const nameLayout = "20060102T1504Z"

var nameRE = regexp.MustCompile(`^agb-(\d{8}T\d{4}Z)\.db\.gz$`)

// ObjectName is the object key for a snapshot taken at t.
func ObjectName(prefix string, t time.Time) string {
	return prefix + "/agb-" + t.UTC().Format(nameLayout) + ".db.gz"
}

// ParseObjectTime extracts the snapshot time from an object key produced by
// ObjectName under the same prefix. Other keys are not backups.
func ParseObjectTime(prefix, name string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(name, prefix+"/")
	if !ok {
		return time.Time{}, false
	}
	m := nameRE.FindStringSubmatch(rest)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(nameLayout, m[1])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// Run performs a cycle immediately and then every Interval until ctx ends.
// Failures are logged and retried on the next tick.
func (r *Runner) Run(ctx context.Context) {
	r.cycle(ctx)
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.cycle(ctx)
		}
	}
}

func (r *Runner) cycle(ctx context.Context) {
	res, err := r.RunOnce(ctx)
	if err != nil {
		r.logger().Error("backup failed", "err", err.Error())
		return
	}
	r.logger().Info("backup", "uploaded", res.Uploaded, "skipped", res.Skipped, "object", res.Object, "deleted", len(res.Deleted))
}

// RunOnce uploads a snapshot unless nothing changed since the last upload,
// then deletes objects older than Retention. The first cycle after start
// always uploads.
func (r *Runner) RunOnce(ctx context.Context) (Result, error) {
	var res Result
	fp, err := r.Source.FingerprintNow(ctx)
	if err != nil {
		return res, fmt.Errorf("fingerprint: %w", err)
	}
	if r.ran && fp == r.lastFingerprint {
		res.Skipped = true
	} else {
		name, err := r.upload(ctx)
		if err != nil {
			return res, err
		}
		res.Uploaded, res.Object = true, name
		r.lastFingerprint, r.ran = fp, true
	}
	deleted, err := r.prune(ctx)
	res.Deleted = deleted
	if err != nil {
		return res, fmt.Errorf("prune: %w", err)
	}
	return res, nil
}

func (r *Runner) upload(ctx context.Context) (string, error) {
	start := r.now()
	if err := os.MkdirAll(r.TempDir, 0o755); err != nil {
		return "", err
	}
	raw := filepath.Join(r.TempDir, "snapshot.db")
	gz := raw + ".gz"
	defer os.Remove(raw)
	defer os.Remove(gz)
	os.Remove(raw) // VACUUM INTO refuses to overwrite
	if err := r.Source.Snapshot(ctx, raw); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	if err := compress(raw, gz); err != nil {
		return "", fmt.Errorf("compress: %w", err)
	}
	f, err := os.Open(gz)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	name := ObjectName(r.Prefix, start)
	if err := r.Store.Put(ctx, name, f, info.Size()); err != nil {
		return "", fmt.Errorf("upload %s: %w", name, err)
	}
	return name, nil
}

func compress(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (r *Runner) prune(ctx context.Context) ([]string, error) {
	objs, err := r.Store.List(ctx, r.Prefix+"/")
	if err != nil {
		return nil, err
	}
	cutoff := r.now().Add(-r.Retention)
	var deleted []string
	for _, o := range objs {
		t, ok := ParseObjectTime(r.Prefix, o.Name)
		if !ok || !t.Before(cutoff) {
			continue
		}
		if err := r.Store.Delete(ctx, o.Name); err != nil {
			return deleted, err
		}
		deleted = append(deleted, o.Name)
	}
	sort.Strings(deleted)
	return deleted, nil
}
