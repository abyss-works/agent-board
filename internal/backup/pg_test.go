package backup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

func TestPGEnvKeepsSecretsOutOfArguments(t *testing.T) {
	env, err := pgEnv("postgres://agb:s3cr3t@db.internal:5433/agbdb?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"PGHOST=db.internal": true, "PGPORT=5433": true, "PGDATABASE=agbdb": true, "PGUSER=agb": true, "PGPASSWORD=s3cr3t": true, "PGSSLMODE=require": true}
	for _, e := range env {
		delete(want, e)
	}
	if len(want) != 0 {
		t.Errorf("missing %v in %v", want, env)
	}
	for _, bad := range []string{"", "mysql://h/db", "postgres:///db", "not a url"} {
		if _, err := pgEnv(bad); err == nil {
			t.Errorf("pgEnv(%q) accepted", bad)
		}
	}
	if _, err := NewPGSource("http://x/y"); err == nil {
		t.Error("NewPGSource accepted a non-postgres URL")
	}
}

// writeScript writes an executable shell script that stands in for a tool.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPGSourceSnapshotUsesEnvironmentOnly(t *testing.T) {
	dump := writeScript(t, `printf 'ARGS:%s|PW:%s|HOST:%s' "$*" "$PGPASSWORD" "$PGHOST"`)
	src, err := NewPGSource("postgres://agb:topsecret@h1:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	src.DumpBin = dump
	dst := filepath.Join(t.TempDir(), "out.pgdump")
	if err := src.Snapshot(context.Background(), dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	got := string(b)
	if strings.Contains(strings.SplitN(got, "|PW:", 2)[0], "topsecret") {
		t.Errorf("the password reached the arguments: %s", got)
	}
	if !strings.Contains(got, "|PW:topsecret|HOST:h1") || !strings.Contains(got, "--format=custom") {
		t.Errorf("tool saw %q", got)
	}
	if info, _ := os.Stat(dst); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode())
	}
	if err := src.Snapshot(context.Background(), dst); err == nil {
		t.Error("an existing snapshot file must not be overwritten")
	}
}

func TestPGSourceSnapshotFailureIsBounded(t *testing.T) {
	src, _ := NewPGSource("postgres://agb:pw@h/db")
	src.DumpBin = writeScript(t, `i=0; while [ $i -lt 400 ]; do printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' >&2; i=$((i+1)); done; exit 3`)
	err := src.Snapshot(context.Background(), filepath.Join(t.TempDir(), "o"))
	if err == nil || len(err.Error()) > stderrLimit+100 {
		t.Fatalf("err = %v (%d bytes)", err, len(fmt.Sprint(err)))
	}
}

type archiveFake struct{ fakeSource }

func (archiveFake) Archive() bool { return true }

func TestRunnerUploadsArchiveSnapshotUncompressed(t *testing.T) {
	store := NewMemory()
	src := &archiveFake{fakeSource{Content: "PGDMP-bytes", Fingerprint: "0/1"}}
	r, _ := newRunner(t, store, src)
	res, err := r.RunOnce(context.Background())
	if err != nil || !res.Uploaded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Object != "agb/agb-20261017T0900Z.pgdump" {
		t.Errorf("object = %s", res.Object)
	}
	if string(store.Get(res.Object)) != "PGDMP-bytes" {
		t.Errorf("an archive must be stored as it is, got %q", store.Get(res.Object))
	}
	// retention covers both kinds of key
	for _, name := range []string{"agb/agb-20200101T0000Z.pgdump", "agb/agb-20200101T0000Z.db.gz"} {
		_ = store.Put(context.Background(), name, strings.NewReader("x"), 1)
	}
	res, err = r.RunOnce(context.Background())
	if err != nil || len(res.Deleted) != 2 {
		t.Errorf("prune deleted %v err=%v", res.Deleted, err)
	}
}

var pgTestDBSeq atomic.Int64

// TestPGBackupRestoreRehearsal dumps a migrated database through the runner and
// restores the archive into an empty database. It needs AGB_TEST_PG_DSN (a
// superuser, because the archive creates extensions) and pg_dump and
// pg_restore, found on PATH or named by AGB_TEST_PG_DUMP and AGB_TEST_PG_RESTORE.
func TestPGBackupRestoreRehearsal(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	dumpBin, restoreBin := os.Getenv("AGB_TEST_PG_DUMP"), os.Getenv("AGB_TEST_PG_RESTORE")
	if dumpBin == "" {
		dumpBin = "pg_dump"
	}
	if restoreBin == "" {
		restoreBin = "pg_restore"
	}
	for _, b := range []string{dumpBin, restoreBin} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
	ctx := context.Background()
	newDB := func() string {
		u, err := url.Parse(baseDSN)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("agb_t_bk_%d_%d", os.Getpid(), pgTestDBSeq.Add(1))
		admin, err := pgxpool.New(ctx, baseDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			p, err := pgxpool.New(ctx, baseDSN)
			if err != nil {
				return
			}
			defer p.Close()
			_, _ = p.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		})
		u.Path = "/" + name
		return u.String()
	}

	srcDSN := newDB()
	st, err := pgstore.Open(ctx, srcDSN, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureActors(ctx, []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "worker", Kind: model.ActorAgent}}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	src, err := NewPGSource(srcDSN)
	if err != nil {
		t.Fatal(err)
	}
	src.DumpBin = dumpBin
	fp1, err := src.FingerprintNow(ctx)
	if err != nil || fp1 == "" {
		t.Fatalf("fingerprint %q %v", fp1, err)
	}
	store := NewMemory()
	r := &Runner{Store: store, Source: src, Prefix: "agb", Interval: time.Hour, Retention: time.Hour * 24,
		TempDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	res, err := r.RunOnce(ctx)
	if err != nil || !res.Uploaded || !strings.HasSuffix(res.Object, ".pgdump") {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	archive := filepath.Join(t.TempDir(), "restore.pgdump")
	if err := os.WriteFile(archive, store.Get(res.Object), 0o600); err != nil {
		t.Fatal(err)
	}
	dstDSN := newDB()
	if err := RestorePG(ctx, archive, dstDSN, restoreBin); err != nil {
		t.Fatal(err)
	}

	want, got := tableCounts(t, srcDSN), tableCounts(t, dstDSN)
	if len(want) < 10 {
		t.Fatalf("expected the migrated schema, got %d tables", len(want))
	}
	if want["actor"] < 2 {
		t.Fatalf("source lost its actors: %v", want)
	}
	for tbl, n := range want {
		if got[tbl] != n {
			t.Errorf("table %s: restored %d rows, source has %d", tbl, got[tbl], n)
		}
	}
	if a, b := indexCount(t, srcDSN), indexCount(t, dstDSN); a == 0 || a != b {
		t.Errorf("pgroonga/hnsw indexes: source %d, restored %d", a, b)
	}
	// the restored database accepts the store again (schema, extensions, sequences intact)
	st2, err := pgstore.Open(ctx, dstDSN, 4)
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	defer st2.Close()
}

func tableCounts(t *testing.T, dsn string) map[string]int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	out := map[string]int{}
	for _, n := range names {
		var c int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.`+pgx.Identifier{n}.Sanitize()).Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[n] = c
	}
	return out
}

func indexCount(t *testing.T, dsn string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND (indexdef ILIKE '%pgroonga%' OR indexdef ILIKE '%hnsw%')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
