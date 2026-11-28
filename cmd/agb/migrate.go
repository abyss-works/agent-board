package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/wsk0715/agent-board/internal/migrate"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

const migrateUsage = `사용법: agb migrate --source <sqlite> [--target <pg-dsn>] [--plan] [--verify-only] [--json] [--no-backfill]

SQLite 데이터베이스를 읽기 전용으로 읽어 PostgreSQL 17로 1:1 이관하거나 사전 계획·사후 무결성을 검증합니다.

옵션:
  --source <경로>      원본 SQLite 파일 경로 (필수, 읽기 전용으로만 열림)
  --target <pg-dsn>    대상 PostgreSQL 연결 문자열 (미지정 시 AGB_DATABASE_URL 사용)
  --plan               PostgreSQL에 쓰지 않고 드라이런 분석만 출력
  --verify-only        이관 없이 원본 SQLite와 대상 PostgreSQL 무결성 검증만 실행
  --json               표 요약 대신 JSON으로 출력
  --no-backfill        이관 후 검색/임베딩 백필 작업(ingest_job) 적재를 생략
`

type migrateCombinedOutput struct {
	Migration    *migrate.RunResult    `json:"migration"`
	Verification *migrate.VerifyReport `json:"verification"`
}

func runMigrate(stdout, stderr io.Writer, args []string, getenv func(string) string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	var source, target string
	var planOnly, verifyOnly, jsonOut, noBackfill bool
	fs.StringVar(&source, "source", "", "")
	fs.StringVar(&target, "target", "", "")
	fs.BoolVar(&planOnly, "plan", false, "")
	fs.BoolVar(&verifyOnly, "verify-only", false, "")
	fs.BoolVar(&jsonOut, "json", false, "")
	fs.BoolVar(&noBackfill, "no-backfill", false, "")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(stdout, migrateUsage)
			return 0
		}
		fmt.Fprintf(stderr, "agb migrate: %v\n\n%s", err, migrateUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "agb migrate: 인자가 너무 많습니다\n\n%s", migrateUsage)
		return 2
	}

	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	if source == "" {
		fmt.Fprintf(stderr, "agb migrate: --source 는 필수입니다.\n\n%s", migrateUsage)
		return 2
	}
	if planOnly && verifyOnly {
		fmt.Fprint(stderr, "agb migrate: --plan 과 --verify-only 는 함께 쓸 수 없습니다.\n")
		return 2
	}
	if planOnly && noBackfill {
		fmt.Fprint(stderr, "agb migrate: --no-backfill 은 --plan 과 함께 쓸 수 없습니다.\n")
		return 2
	}
	if verifyOnly && noBackfill {
		fmt.Fprint(stderr, "agb migrate: --no-backfill 은 --verify-only 와 함께 쓸 수 없습니다.\n")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if planOnly {
		report, err := migrate.Plan(ctx, source)
		if err != nil {
			fmt.Fprintf(stderr, "agb migrate: %s\n", migrate.RedactDSN(err.Error(), target))
			return 1
		}
		if jsonOut {
			if err := writeMigrateJSON(stdout, report); err != nil {
				fmt.Fprintf(stderr, "agb migrate: %v\n", err)
				return 1
			}
			return 0
		}
		if err := report.FormatTable(stdout); err != nil {
			fmt.Fprintf(stderr, "agb migrate: %v\n", err)
			return 1
		}
		return 0
	}

	if target == "" {
		target = strings.TrimSpace(getenv("AGB_DATABASE_URL"))
	}
	if target == "" {
		fmt.Fprint(stderr, "agb migrate: --target 또는 AGB_DATABASE_URL 이 필요합니다.\n")
		return 2
	}

	pgStore, err := pg.Open(ctx, target, 8)
	if err != nil {
		fmt.Fprintf(stderr, "agb migrate: PostgreSQL 연결 실패: %s\n", migrate.RedactDSN(err.Error(), target))
		return 1
	}
	defer func() { _ = pgStore.Close() }()

	if verifyOnly {
		ver, err := migrate.Verify(ctx, source, pgStore.Pool())
		if err != nil {
			fmt.Fprintf(stderr, "agb migrate: 검증 실패: %s\n", migrate.RedactDSN(err.Error(), target))
			return 1
		}
		if jsonOut {
			if err := writeMigrateJSON(stdout, ver); err != nil {
				fmt.Fprintf(stderr, "agb migrate: %v\n", err)
				return 1
			}
		} else if err := ver.FormatTable(stdout); err != nil {
			fmt.Fprintf(stderr, "agb migrate: %v\n", err)
			return 1
		}
		if !ver.OK {
			return 1
		}
		return 0
	}

	opts := migrate.Options{
		SourcePath:      source,
		TargetDSN:       target,
		Pool:            pgStore.Pool(),
		EnqueueBackfill: migrate.Bool(!noBackfill),
	}
	res, err := migrate.Run(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "agb migrate: 이관 실패: %s\n", migrate.RedactDSN(err.Error(), target))
		return 1
	}
	ver, err := migrate.Verify(ctx, source, pgStore.Pool())
	if err != nil {
		fmt.Fprintf(stderr, "agb migrate: 사후 검증 실패: %s\n", migrate.RedactDSN(err.Error(), target))
		return 1
	}
	if jsonOut {
		if err := writeMigrateJSON(stdout, migrateCombinedOutput{
			Migration:    res,
			Verification: ver,
		}); err != nil {
			fmt.Fprintf(stderr, "agb migrate: %v\n", err)
			return 1
		}
	} else {
		if err := res.FormatTable(stdout); err != nil {
			fmt.Fprintf(stderr, "agb migrate: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout)
		if err := ver.FormatTable(stdout); err != nil {
			fmt.Fprintf(stderr, "agb migrate: %v\n", err)
			return 1
		}
	}
	if !ver.OK {
		return 1
	}
	return 0
}

func writeMigrateJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
