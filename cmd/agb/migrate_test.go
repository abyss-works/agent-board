package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/migrate"
	"github.com/wsk0715/agent-board/internal/store"
)

var cliTestPGSeq atomic.Int64

func makeMinimalSQLiteForCLI(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cli_src.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	db := st.DB()
	ts := "2026-10-01T10:00:00.000Z"
	for _, q := range []string{
		`INSERT INTO actor(id, kind, created_at) VALUES ('owner', 'user', '` + ts + `')`,
		`INSERT INTO project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 VALUES (1, 'DEMO', 'Demo', 'active', 3, '` + ts + `', '` + ts + `', 'owner', 'owner')`,
		`INSERT INTO issue(id, project_id, number, key, title, body, type, status, priority, parent_id, version, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by) VALUES
		 (1, 1, 1, 'DEMO-1', 'Parent', 'Body', 'design', 'doing', 'P1', NULL, 1, '` + ts + `', '` + ts + `', '` + ts + `', 'owner', 'owner', 'owner'),
		 (2, 1, 2, 'DEMO-2', 'Child', 'Sub', 'feature', 'backlog', 'P2', 1, 1, '` + ts + `', '` + ts + `', '` + ts + `', 'owner', 'owner', 'owner')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	_ = st.Close()
	return dbPath
}

func TestMigrateCLIHelpAndFlagValidation(t *testing.T) {
	if code, out, errb := run2("migrate", "--help"); code != 0 || !strings.Contains(out, "--plan") || errb != "" {
		t.Fatalf("migrate --help: code=%d out=%q err=%q", code, out, errb)
	}
	if code, _, errb := run2("migrate"); code != 2 || !strings.Contains(errb, "--source") {
		t.Fatalf("migrate without --source: code=%d err=%q", code, errb)
	}
	if code, _, errb := run2("migrate", "--source", "/tmp/x.db", "--plan", "--verify-only"); code != 2 || !strings.Contains(errb, "--plan") {
		t.Fatalf("migrate --plan --verify-only: code=%d err=%q", code, errb)
	}
	if code, _, errb := run2("migrate", "--source", "/tmp/x.db"); code != 2 || !strings.Contains(errb, "AGB_DATABASE_URL") {
		t.Fatalf("migrate without target or env: code=%d err=%q", code, errb)
	}
}

func TestMigrateCLIPlanTableAndJSON(t *testing.T) {
	src := makeMinimalSQLiteForCLI(t)

	code, out, errb := run2("migrate", "--source", src, "--plan")
	if code != 0 || errb != "" {
		t.Fatalf("migrate --plan: code=%d err=%q out=%q", code, errb, out)
	}
	if !strings.Contains(out, "DEMO-1") || !strings.Contains(out, "Promoted Epics: 1") {
		t.Errorf("unexpected --plan table output:\n%s", out)
	}

	code, out, errb = run2("migrate", "--source", src, "--plan", "--json")
	if code != 0 || errb != "" {
		t.Fatalf("migrate --plan --json: code=%d err=%q", code, errb)
	}
	var rep migrate.PlanReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal plan JSON: %v\n%s", err, out)
	}
	if rep.TotalPromotedEpics != 1 || rep.TotalAssignedChildIssues != 1 {
		t.Errorf("plan JSON promotions=(%d,%d), want (1,1)", rep.TotalPromotedEpics, rep.TotalAssignedChildIssues)
	}
}

func TestMigrateCLIRedactsDSNCredentialsOnError(t *testing.T) {
	src := makeMinimalSQLiteForCLI(t)
	secretDSN := "postgres://miguser:UltraSecretPass99!@127.0.0.1:1/nonexistent?connect_timeout=1"

	code, out, errb := run2("migrate", "--source", src, "--target", secretDSN)
	if code != 1 {
		t.Fatalf("expected exit 1 on unreachable PG, got code=%d out=%q err=%q", code, out, errb)
	}
	combined := out + "\n" + errb
	for _, secret := range []string{"UltraSecretPass99", "miguser:UltraSecretPass99"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("CLI output leaked DSN credential %q:\n%s", secret, combined)
		}
	}
}

func TestMigrateCLILivePG17EndToEnd(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}

	ctx := context.Background()
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	dbName := fmt.Sprintf("agb_t_climig_%d_%d", os.Getpid(), cliTestPGSeq.Add(1))
	adminPool, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		adminPool.Close()
		t.Fatalf("CREATE DATABASE %s: %v", dbName, err)
	}
	adminPool.Close()
	t.Cleanup(func() {
		p, err := pgxpool.New(context.Background(), baseDSN)
		if err != nil {
			return
		}
		defer p.Close()
		_, _ = p.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})

	u.Path = "/" + dbName
	targetDSN := u.String()
	src := makeMinimalSQLiteForCLI(t)

	// 1. Run migration via --target with --json and --no-backfill.
	code, out, errb := run2("migrate", "--source", src, "--target", targetDSN, "--no-backfill", "--json")
	if code != 0 || errb != "" {
		t.Fatalf("migrate run failed: code=%d err=%q out=%q", code, errb, out)
	}
	var combined migrateCombinedOutput
	if err := json.Unmarshal([]byte(out), &combined); err != nil {
		t.Fatalf("unmarshal migrate output: %v\n%s", err, out)
	}
	if combined.Migration == nil || combined.Verification == nil || !combined.Verification.OK {
		t.Fatalf("unexpected migrate output: %+v", combined)
	}
	if combined.Migration.EnqueuedBackfillJobs != 0 {
		t.Errorf("EnqueuedBackfillJobs = %d, want 0 with --no-backfill", combined.Migration.EnqueuedBackfillJobs)
	}

	// 2. Run --verify-only using AGB_DATABASE_URL fallback.
	var vOut, vErr bytes.Buffer
	vCode := run(
		[]string{"migrate", "--source", src, "--verify-only", "--json"},
		&vOut, &vErr,
		func(k string) string {
			if k == "AGB_DATABASE_URL" {
				return targetDSN
			}
			return ""
		},
	)
	if vCode != 0 || vErr.Len() > 0 {
		t.Fatalf("migrate --verify-only failed: code=%d err=%q out=%q", vCode, vErr.String(), vOut.String())
	}
	var ver migrate.VerifyReport
	if err := json.Unmarshal(vOut.Bytes(), &ver); err != nil {
		t.Fatalf("unmarshal verify-only JSON: %v\n%s", err, vOut.String())
	}
	if !ver.OK || !ver.GlobalSeedOK {
		t.Errorf("verify-only report not OK: %+v", ver)
	}
}
