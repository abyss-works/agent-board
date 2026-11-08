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
	name: "find", summary: "이슈·댓글·기록 문서 통합 검색 (키워드 + 의미 검색, 관련도 순)",
	usage: `사용법: agb find <검색어...> [옵션]

이슈(키, 제목, 본문), 댓글(요약, 본문), 기록 문서(이름, 제목, 태그, 원문)를 한 번에 찾습니다. 프로젝트를
주지 않으면 모든 프로젝트를 찾습니다. 지운 이슈와 그 댓글은 나오지 않습니다.

검색은 키워드 검색과 의미 검색을 합칩니다.
- 키워드 검색: 여러 단어는 모두 포함(AND)이고, 각 단어는 앞부분이 같으면 일치합니다(접두 일치). 단어 수에 상한이 있습니다.
  전체를 큰따옴표로 감싸면 구(phrase) 하나로 찾고, 이때는 접두 일치와 단어 수 상한이 적용되지 않습니다.
- 의미 검색: 서버가 임베딩 API 를 켜 둔 때만 합니다. 검색어와 뜻이 가까운 것을 단어가 일치하지 않아도 찾으므로, 검색어의
  단어를 모두 담지 않은 항목도 결과에 섞입니다. 올린 지 얼마 안 된 내용은 임베딩 전이라 의미 검색에는 아직 잡히지 않을 수
  있습니다. 의미 검색은 --project, --type, --comment-kind, --status, --state 와 옵션 없이도 걸리는 기본값(열린 이슈,
  유효한 문서)을 먼저 적용한 뒤 그 안에서 가까운 것을 고릅니다.
- 합치는 방식: 두 검색의 순위를 합쳐 정렬합니다. 두 검색에 모두 나온 항목이 앞서고, 결과 수에는 상한이 있습니다. 일치 부분을
  [ ] 로 표시하는 snippet 은 키워드 검색에 걸린 항목에만 있고, 의미 검색으로만 찾은 항목은 표시 없이 나옵니다.
- 유사도 하한: 의미 검색 점수가 서버가 정한 하한보다 낮은 항목은 결과에서 빠집니다. 하한을 넘었다는 것이 질문에 답이 된다는
  뜻은 아니므로 snippet 과 원문을 읽어 판단합니다. 의미 검색으로 찾은 항목의 점수는 JSON 의 semantic_score 에 있고,
  키워드 검색으로만 찾은 항목은 null 입니다.
- 서버가 의미 검색을 켜지 않았으면 키워드 검색 결과만 나옵니다. 검색어를 임베딩하지 못하면(한 번 다시 시도) 역시 키워드
  검색 결과만 나오고, 출력 끝에 그 알림이 한 줄 붙으며 JSON 의 semantic 이 failed 입니다(ok, disabled 도 이 필드에 있습니다).
  다만 단어 수가 키워드 검색의 상한을 넘는 검색어는, 의미 검색을 켠 서버에서 임베딩에 성공했을 때만 의미 검색으로 찾고
  그 밖에는 오류입니다.

지금 유효한 것을 찾는 명령이라 기본은 열린 이슈(backlog, doing, review)와 유효한 문서(current)입니다.
닫힌 이슈는 --status 로, 대체됨·아카이브 문서는 --state 로 넓힙니다. 결과의 STATE 열은 이슈와 댓글이면
이슈의 상태, 문서이면 문서의 상태이고, 문서가 다른 문서를 대체하거나 대체되었으면 표 아래에 이름을 보입니다.

결과는 합친 순위(관련도 순)이고 항목마다 TYPE(issue, comment, document), 출처 키(이슈 키나 문서 이름), 종류(이슈 종류,
댓글 종류, 문서 종류), 시각, 일치 부분이 있습니다. 이슈는 agb issue get <KEY>, 댓글은 agb issue activity
<KEY>, 문서는 agb doc get <PROJECT> <이름> --content 로 원문을 읽습니다.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR). 없으면 모든 프로젝트
  --type <T>              issue, comment, document. 쉼표 OR
  --comment-kind <K>      댓글 종류 decision, finding, lesson. 쉼표 OR. 댓글만 거릅니다
  --status <S>            이슈와 댓글이 달린 이슈의 상태. open(기본), closed, all 또는 backlog, doing, review,
                          done, cancelled. 쉼표 OR
  --state <S>             문서의 상태. current(기본, 유효), superseded(대체됨), archived(아카이브), all. 쉼표 OR
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
		fs.StringVar(&f.Status, "status", "open", "")
		fs.StringVar(&f.State, "state", "", "")
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
	l, err := decode[searchOf](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "검색 결과가 없습니다.")
		semanticNotice(w, l.Semantic)
		return nil
	}
	tb := &table{header: []string{"TYPE", "PROJECT", "KEY", "KIND", "STATE", "AT", "TITLE", "SNIPPET"}}
	var rel []supersession
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
		tb.add(string(h.Type), h.Project, key, deref(h.Kind, "-"), hitState(h), at, deref(h.Title, "-"), snippet)
		rel = append(rel, hitSupersession(h))
	}
	tb.write(w, a.TermWidth)
	printSupersession(w, rel)
	footer(w, len(l.Items), l.Total, l.Offset)
	semanticNotice(w, l.Semantic)
	return nil
}
