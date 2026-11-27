package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var watchCmd = &command{
	name: "watch", summary: "이벤트 스트림 구독 (첫 일치 이벤트에서 종료)",
	usage: `사용법: agb watch [옵션]

GET /v1/events/stream 을 구독하고 조건에 맞는 첫 이벤트가 오면 출력한 뒤 종료 코드 0으로 끝납니다.
시간 제한 안에 이벤트가 없으면 표준 출력에 아무것도 쓰지 않고 표준 오류에 알린 뒤 종료 코드 0으로 끝납니다.

옵션:
  --project, -p <KEY>     프로젝트 키. 생략하면 전체 프로젝트(세션 이벤트 포함)
  --kind <접두사>         이벤트 종류 접두사. 쉼표 구분이나 반복 지정 가능 (예: issue.,message.created)
  --after <id>            이 ID 다음 이벤트부터 받음 (0 이상). 생략하면 연결 시점 이후 이벤트만 받음
  --timeout <기간>        대기 제한 시간 (Go duration 형식, 예: 30s, 10m). 기본 10m
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("watch", g)
		var project string
		var kindFlags stringList
		var after int64
		var timeoutStr string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.Var(&kindFlags, "kind", "")
		fs.Int64Var(&after, "after", 0, "")
		fs.StringVar(&timeoutStr, "timeout", "10m", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}

		timeout, err := time.ParseDuration(timeoutStr)
		if err != nil || timeout <= 0 {
			return usageErrorf("--timeout은 양수 기간이어야 합니다 (예: 30s, 10m).")
		}

		var afterPtr *int64
		if isSet(fs, "after") {
			if after < 0 {
				return usageErrorf("--after는 0 이상의 정수여야 합니다.")
			}
			v := after
			afterPtr = &v
		}

		var kinds []string
		for _, entry := range kindFlags {
			for _, part := range strings.Split(entry, ",") {
				if p := strings.TrimSpace(part); p != "" {
					kinds = append(kinds, p)
				}
			}
		}
		if isSet(fs, "kind") && len(kinds) == 0 {
			return usageErrorf("--kind 값이 비어 있습니다.")
		}

		c, err := a.client(g, true)
		if err != nil {
			return err
		}

		watchCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		resp, err := c.EventStream(watchCtx, client.WatchParams{
			Project: project,
			Kinds:   kinds,
			After:   afterPtr,
		})
		if err != nil {
			if ctx.Err() == nil && errors.Is(watchCtx.Err(), context.DeadlineExceeded) {
				fmt.Fprintf(a.Stderr, "시간 초과(%s): 조건에 맞는 이벤트가 없었습니다.\n", timeoutStr)
				return nil
			}
			return err
		}
		defer resp.Body.Close()

		payload, err := readFirstSSEEvent(resp.Body)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(watchCtx.Err(), context.DeadlineExceeded) {
				fmt.Fprintf(a.Stderr, "시간 초과(%s): 조건에 맞는 이벤트가 없었습니다.\n", timeoutStr)
				return nil
			}
			if errors.Is(err, io.EOF) {
				return errors.New("이벤트 스트림 연결이 종료되었습니다")
			}
			return err
		}

		eventResp := &client.Response{
			Status: http.StatusOK,
			Header: resp.Header,
			Body:   payload,
		}
		return a.emit(g, eventResp, a.renderEvent)
	},
}

// readFirstSSEEvent reads from an SSE stream until the first complete event
// (terminated by a blank line with at least one data: field) and returns its
// data payload. Comment lines (starting with ':') such as keepalive pings are
// ignored.
func readFirstSSEEvent(r io.Reader) ([]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var dataLines []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if len(dataLines) > 0 {
				return []byte(strings.Join(dataLines, "\n")), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			rest = strings.TrimPrefix(rest, " ")
			dataLines = append(dataLines, rest)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (a *App) renderEvent(w io.Writer, resp *client.Response) error {
	ev, err := decode[model.Event](resp)
	if err != nil {
		return err
	}
	pairs := [][2]string{
		{"id", strconv.FormatInt(ev.ID, 10)},
		{"at", formatTime(ev.At, a.Location)},
		{"project", deref(ev.Project, "-")},
		{"kind", ev.Kind},
		{"subject", ev.Subject},
		{"actor", ev.Actor},
		{"session_id", ev.SessionID},
		{"data", string(ev.Data)},
	}
	fields(w, pairs)
	return nil
}
