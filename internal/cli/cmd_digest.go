package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var digestCmd = &command{
	name: "digest", summary: "시점 이후 프로젝트별 변경 요약",
	usage: `사용법: agb digest --since <시점> [--until <시점>] [--project <P>] [--limit <N>]

지정한 시점부터 지금(또는 --until)까지 프로젝트마다 바뀐 것을 오래된 순으로 보입니다.
새 이슈, 상태 변경, 결정 요청(결정 필요 지정), 등록된 기록 문서, 활동·댓글 수입니다.

옵션:
  --since <시점>          필수. RFC 3339 시각, 날짜(YYYY-MM-DD, 그날 0시 현지 시각), 또는 지금부터 거슬러 갈
                          기간(예: 12h, 3d)
  --until <시점>          끝 시점. 형식은 --since 와 같음. 기본 지금
  --project, -p <P>       프로젝트 키. 쉼표로 여럿. 생략하면 활성 프로젝트 중 바뀐 것이 있는 프로젝트
  --limit <N>             목록마다 최대 개수. 기본 50, 최대 200
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("digest", g)
		var since, until, project string
		var limit int
		fs.StringVar(&since, "since", "", "")
		fs.StringVar(&until, "until", "", "")
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.IntVar(&limit, "limit", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if since == "" {
			return usageErrorf("--since 가 필요합니다.")
		}
		from, err := a.parseMoment(since)
		if err != nil {
			return err
		}
		var to string
		if until != "" {
			if to, err = a.parseMoment(until); err != nil {
				return err
			}
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Digest(ctx, project, from, to, limit)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderDigest)
	},
}

// parseMoment reads an RFC 3339 time, a local date, or a duration back from
// now (h for hours, d for days) and returns it as RFC 3339 in UTC.
func (a *App) parseMoment(v string) (string, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, a.Location); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && strings.HasSuffix(v, "d") && n >= 0 {
		return a.Now().Add(-time.Duration(n) * 24 * time.Hour).UTC().Format(time.RFC3339), nil
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return a.Now().Add(-d).UTC().Format(time.RFC3339), nil
	}
	return "", usageErrorf("시점 %q를 읽지 못했습니다. RFC 3339 시각, YYYY-MM-DD, 또는 12h·3d 같은 기간을 주세요.", v)
}

type digestEvent struct {
	Issue    string            `json:"issue"`
	Title    string            `json:"title"`
	Status   model.IssueStatus `json:"status"`
	From     *string           `json:"from"`
	To       *string           `json:"to"`
	Question *string           `json:"question"`
	Actor    string            `json:"actor"`
	At       model.Time        `json:"at"`
}

type digest struct {
	Since    model.Time `json:"since"`
	Until    model.Time `json:"until"`
	Projects []struct {
		Project   model.Project        `json:"project"`
		Activity  int                  `json:"activity"`
		Comments  int                  `json:"comments"`
		Created   section[digestEvent] `json:"created"`
		Status    section[digestEvent] `json:"status_changes"`
		Decisions section[digestEvent] `json:"decisions_asked"`
		Documents section[struct {
			Name      string     `json:"name"`
			Kind      string     `json:"kind"`
			Title     *string    `json:"title"`
			CreatedBy string     `json:"created_by"`
			CreatedAt model.Time `json:"created_at"`
		}] `json:"documents"`
	} `json:"projects"`
}

func (a *App) renderDigest(w io.Writer, resp *client.Response) error {
	d, err := decode[digest](resp)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "기간: %s ~ %s\n", formatTime(d.Since, a.Location), formatTime(d.Until, a.Location))
	if len(d.Projects) == 0 {
		fmt.Fprintln(w, "\n바뀐 것이 없습니다.")
		return nil
	}
	more := func(s int, n int) string {
		if s > n {
			return fmt.Sprintf(" (%d개 중 %d개)", s, n)
		}
		return ""
	}
	for _, p := range d.Projects {
		fmt.Fprintf(w, "\n### %s (%s): 활동 %d, 댓글 %d, 새 이슈 %d, 상태 변경 %d, 결정 요청 %d, 기록 문서 %d\n",
			p.Project.Key, p.Project.Name, p.Activity, p.Comments, p.Created.Total, p.Status.Total, p.Decisions.Total, p.Documents.Total)
		if len(p.Created.Items) > 0 {
			fmt.Fprintf(w, "\n== 새 이슈%s ==\n", more(p.Created.Total, len(p.Created.Items)))
			tb := &table{header: []string{"AT", "KEY", "STATUS", "ACTOR", "TITLE"}}
			for _, e := range p.Created.Items {
				tb.add(formatTime(e.At, a.Location), e.Issue, string(e.Status), e.Actor, e.Title)
			}
			tb.write(w, a.TermWidth)
		}
		if len(p.Status.Items) > 0 {
			fmt.Fprintf(w, "\n== 상태 변경%s ==\n", more(p.Status.Total, len(p.Status.Items)))
			tb := &table{header: []string{"AT", "KEY", "CHANGE", "ACTOR", "TITLE"}}
			for _, e := range p.Status.Items {
				tb.add(formatTime(e.At, a.Location), e.Issue, deref(e.From, "-")+" → "+deref(e.To, "-"), e.Actor, e.Title)
			}
			tb.write(w, a.TermWidth)
		}
		if len(p.Decisions.Items) > 0 {
			fmt.Fprintf(w, "\n== 결정 요청%s ==\n", more(p.Decisions.Total, len(p.Decisions.Items)))
			tb := &table{header: []string{"AT", "KEY", "ACTOR", "QUESTION"}}
			for _, e := range p.Decisions.Items {
				tb.add(formatTime(e.At, a.Location), e.Issue, e.Actor, deref(e.Question, "-"))
			}
			tb.write(w, a.TermWidth)
		}
		if len(p.Documents.Items) > 0 {
			fmt.Fprintf(w, "\n== 기록 문서%s ==\n", more(p.Documents.Total, len(p.Documents.Items)))
			tb := &table{header: []string{"AT", "KIND", "NAME", "TITLE"}}
			for _, doc := range p.Documents.Items {
				tb.add(formatTime(doc.CreatedAt, a.Location), doc.Kind, doc.Name, deref(doc.Title, "-"))
			}
			tb.write(w, a.TermWidth)
		}
	}
	return nil
}
