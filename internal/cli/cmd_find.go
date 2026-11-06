package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var findCmd = &command{
	name: "find", summary: "이슈·댓글·기록 문서 통합 검색 (관련도 순)",
	usage: `사용법: agb find <검색어...> [옵션]

이슈(키, 제목, 본문), 댓글(요약, 본문), 기록 문서(이름, 제목, 태그, 원문)를 한 번에 찾습니다. 프로젝트를
주지 않으면 모든 프로젝트를 찾습니다. 여러 단어는 모두 포함(AND)이고, 전체를 큰따옴표로 감싸면 구(phrase)
하나로 찾습니다. 지운 이슈와 그 댓글은 나오지 않습니다.

결과는 관련도 순이고 항목마다 TYPE(issue, comment, document), 출처 키(이슈 키나 문서 이름), 종류(이슈 종류,
댓글 종류, 문서 종류), 시각, 일치 부분이 있습니다. 이슈는 agb issue get <KEY>, 댓글은 agb issue activity
<KEY>, 문서는 agb doc get <PROJECT> <이름> --content 로 원문을 읽습니다.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR). 없으면 모든 프로젝트
  --type <T>              issue, comment, document. 쉼표 OR
  --comment-kind <K>      댓글 종류 decision, finding, lesson. 쉼표 OR. 댓글만 거릅니다
  --limit <N>             최대 200. 기본 20
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("find", g)
		var f client.SearchFilter
		fs.StringVar(&f.Project, "project", "", "")
		fs.StringVar(&f.Project, "p", "", "")
		fs.StringVar(&f.Type, "type", "", "")
		fs.StringVar(&f.CommentKind, "comment-kind", "", "")
		fs.IntVar(&f.Limit, "limit", 0, "")
		fs.IntVar(&f.Offset, "offset", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageErrorf("검색어가 필요합니다.")
		}
		f.Query = strings.Join(pos, " ")
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Search(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderSearch)
	},
}

func (a *App) renderSearch(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.SearchHit]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "검색 결과가 없습니다.")
		return nil
	}
	tb := &table{header: []string{"TYPE", "PROJECT", "KEY", "KIND", "AT", "TITLE", "SNIPPET"}}
	for _, h := range l.Items {
		key := h.Key
		if h.CommentID != nil {
			key += "#" + strconv.FormatInt(*h.CommentID, 10)
		}
		at := h.At
		if t, err := model.ParseTime(h.At); err == nil {
			at = formatTime(t, a.Location)
		}
		snippet := strings.Join(strings.Fields(h.Snippet), " ")
		tb.add(string(h.Type), h.Project, key, deref(h.Kind, "-"), at, deref(h.Title, "-"), snippet)
	}
	tb.write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}
