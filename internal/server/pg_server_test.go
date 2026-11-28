package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

var srvTestDBSeq atomic.Int64

func TestPostgresServerStartsServesStatsAndStops(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	ctx := context.Background()
	dbName := fmt.Sprintf("agb_t_srv_%d_%d", os.Getpid(), srvTestDBSeq.Add(1))
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
	dsn := u.String()

	cfg := &config.Config{
		Addr:        "127.0.0.1:0",
		DatabaseURL: dsn,
		DBMaxConns:  8,
		Token:       testToken,
		Actors:      []model.Actor{{ID: "owner", Kind: model.ActorUser}},
	}
	srv, err := New(cfg, Options{Version: "t-pg", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("New PG server: %v", err)
	}
	addr, err := srv.Start()
	if err != nil {
		t.Fatalf("Start PG server: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/v1/health = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest("GET", "http://"+addr+"/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "pg-srv")
	req.Header.Set("X-Client-Host", "devbox")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("/v1/stats = %d", resp2.StatusCode)
	}
	var stats struct {
		DBBytes int64 `json:"db_bytes"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&stats); err != nil || stats.DBBytes <= 0 {
		t.Fatalf("/v1/stats db_bytes = %d, err = %v", stats.DBBytes, err)
	}

	// Verify background ingest worker + ?wait=true + /v1/context/query end-to-end.
	doJSON := func(method, path, payload string) (*http.Response, []byte) {
		t.Helper()
		var body io.Reader
		if payload != "" {
			body = bytes.NewBufferString(payload)
		}
		r, err := http.NewRequest(method, "http://"+addr+path, body)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("X-Actor", "owner")
		r.Header.Set("X-Session-Id", "pg-srv")
		r.Header.Set("X-Client-Host", "devbox")
		if payload != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return res, raw
	}

	if res, raw := doJSON("POST", "/v1/projects", `{"key":"SRV","name":"Server E2E"}`); res.StatusCode != 201 {
		t.Fatalf("create project = %d %s", res.StatusCode, raw)
	}
	resRec, rawRec := doJSON("POST", "/v1/projects/SRV/records?wait=true&wait_timeout_ms=5000",
		`{"name":"2026-11-01-1000-decision-shutdown.md","kind":"decision","title":"우아한 종료 절차","content":"# 우아한 종료\n서버 종료 시 백그라운드 워커와 큐 리스너를 먼저 정리한다."}`)
	if resRec.StatusCode != 201 || !strings.Contains(string(rawRec), `"ingest_status":"done"`) {
		t.Fatalf("create record wait=true = %d %s", resRec.StatusCode, rawRec)
	}
	resCtx, rawCtx := doJSON("POST", "/v1/context/query", `{"query":"백그라운드 워커를","project":"SRV"}`)
	if resCtx.StatusCode != 200 {
		t.Fatalf("context query = %d %s", resCtx.StatusCode, rawCtx)
	}
	var ctxBody pgstore.ContextQueryResponse
	if err := json.Unmarshal(rawCtx, &ctxBody); err != nil || len(ctxBody.Items) == 0 {
		t.Fatalf("context query body = %s, err = %v", rawCtx, err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
