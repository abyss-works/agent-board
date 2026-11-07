package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var docCmd = &command{name: "doc", summary: "기록 문서 작성·검사·등록·동기화·상태 수정·목록·검색·조회", note: docHelpNote, subs: []*command{
	docNewCmd, docCheckCmd, docPutCmd, docSyncCmd, docArchiveCmd, docUnarchiveCmd, docLinkCmd, docUnlinkCmd, docActivityCmd,
	docListCmd, docSearchCmd, docGetCmd,
}}

// docHelpNote points the help of agb doc to the pages that hold the details of
// the documents and of the .agb.json that decides which files are uploaded.
const docHelpNote = `
기록 문서의 형식, 메타데이터, 상태, 참조 표기, 오류와 대처는 agb help doc-format 으로 봅니다.
.agb.json 의 키, 규칙, 예시는 agb help repo-config 로 봅니다.
`

// errNotUTF8 reports a record document file that is not text the server could
// keep as it is.
var errNotUTF8 = errors.New("UTF-8이 아닌 파일입니다.")

// readDocContent reads a record document file. The server stores text, so the
// bytes must be UTF-8; they are sent as they are.
func readDocContent(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return "", errNotUTF8
	}
	return string(raw), nil
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (a *App) renderDocument(w io.Writer, resp *client.Response) error {
	d, err := decode[model.Document](resp)
	if err != nil {
		return err
	}
	fields(w, [][2]string{
		{"project", d.Project},
		{"name", d.Name},
		{"state", string(d.State)},
		{"kind", string(d.Kind)},
		{"date", deref(d.Date, "-")},
		{"title", deref(d.Title, "-")},
		{"tags", joinOr(d.Tags, "-")},
		{"archived", yesNo(d.Archived)},
		{"imported", yesNo(d.Imported)},
		{"path", deref(d.Path, "-")},
		{"supersedes", joinOr(d.Supersedes, "-")},
		{"superseded_by", joinOr(d.SupersededBy, "-")},
		{"related", joinOr(d.Related, "-")},
		{"issues", joinOr(d.Issues, "-")},
		{"unresolved", unresolvedText(d.UnresolvedRefs)},
		{"sha256", d.SHA256},
		{"size", strconv.Itoa(d.Size)},
		{"created", formatTime(d.CreatedAt, a.Location) + " (" + d.CreatedBy + ")"},
		{"updated", formatTime(d.UpdatedAt, a.Location) + " (" + d.UpdatedBy + ")"},
	})
	return nil
}

// apiReason summarizes a refusal of the server in one line.
func apiReason(e *client.Error) string {
	var parts []string
	for _, d := range e.Details {
		if d.Field != "" {
			parts = append(parts, d.Field+": "+d.Message)
		} else if d.Message != "" {
			parts = append(parts, d.Message)
		}
	}
	if len(parts) == 0 {
		return e.Message
	}
	return strings.Join(parts, "; ")
}

func (a *App) renderDocumentSearch(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.Document]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "검색 결과가 없습니다.")
		return nil
	}
	tb := &table{header: []string{"PROJECT", "NAME", "STATE", "KIND", "DATE", "TITLE", "SNIPPET"}}
	var rel []supersession
	for _, d := range l.Items {
		// A snippet cuts the original, so it carries its line breaks.
		snippet := strings.Join(strings.Fields(deref(d.Snippet, "")), " ")
		tb.add(d.Project, d.Name, string(d.State), string(d.Kind), deref(d.Date, "-"), deref(d.Title, "-"), snippet)
		rel = append(rel, docSupersession(d))
	}
	tb.write(w, a.TermWidth)
	printSupersession(w, rel)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

var docSearchCmd = &command{
	name: "search", summary: "기록 문서 전문 검색 (관련도 순)",
	usage: `사용법: agb doc search <검색어...> [옵션]

기록 문서의 이름, 제목, 태그, 원문을 검색합니다. 프로젝트를 주지 않으면 모든 프로젝트를 찾습니다.
여러 단어는 모두 포함(AND)이고, 전체를 큰따옴표로 감싸면 구(phrase) 하나로 찾습니다. 관련도 순이고
결과에 snippet 이 붙습니다. 원문은 agb doc get <KEY> <파일명> --content 로 읽습니다.

기본은 유효한(current) 문서만 찾습니다. 대체됨·아카이브 문서는 --state 로 넓힙니다. 결과의 STATE 열은
문서의 상태이고, 다른 문서를 대체하거나 대체되었으면 표 아래에 이름을 보입니다.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR). 없으면 모든 프로젝트
  --kind <K>              문서 종류. 쉼표 OR. 종류의 형식은 agb help doc-format
  --state <S>             current(기본, 유효), superseded(대체됨), archived(아카이브), all. 쉼표 OR
  --imported <true|false> 가져온 문서만(true), 일반 문서만(false). 없으면 둘 다
  --limit <N>             최대 200. 기본 50
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc search", g)
		var f client.DocumentFilter
		fs.StringVar(&f.Project, "project", "", "")
		fs.StringVar(&f.Project, "p", "", "")
		fs.StringVar(&f.Kind, "kind", "", "")
		fs.StringVar(&f.State, "state", "", "")
		fs.StringVar(&f.Imported, "imported", "", "")
		fs.IntVar(&f.Limit, "limit", 0, "")
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
		resp, err := c.SearchDocuments(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderDocumentSearch)
	},
}

func (a *App) renderDocumentList(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.Document]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "기록 문서가 없습니다.")
		return nil
	}
	tb := &table{header: []string{"PROJECT", "NAME", "STATE", "KIND", "DATE", "TITLE"}}
	var rel []supersession
	for _, d := range l.Items {
		tb.add(d.Project, d.Name, string(d.State), string(d.Kind), deref(d.Date, "-"), deref(d.Title, "-"))
		rel = append(rel, docSupersession(d))
	}
	tb.write(w, a.TermWidth)
	printSupersession(w, rel)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

var docListCmd = &command{
	name: "list", summary: "서버의 기록 문서 목록 (날짜 최신 순)",
	usage: `사용법: agb doc list [옵션]

서버에 등록된 기록 문서를 날짜 최신 순으로 보입니다. 프로젝트를 주지 않으면 모든 프로젝트를 봅니다.
기본은 유효한(current) 문서만 보입니다. 대체됨·아카이브 문서는 --state 로 넓힙니다.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR). 없으면 모든 프로젝트
  --kind <K>              문서 종류. 쉼표 OR. 종류의 형식은 agb help doc-format
  --state <S>             current(기본, 유효), superseded(대체됨), archived(아카이브), all. 쉼표 OR
  --imported <true|false> 가져온 문서만(true), 일반 문서만(false). 없으면 둘 다
  --limit <N>             최대 200. 기본 50
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc list", g)
		var f client.DocumentFilter
		fs.StringVar(&f.Project, "project", "", "")
		fs.StringVar(&f.Project, "p", "", "")
		fs.StringVar(&f.Kind, "kind", "", "")
		fs.StringVar(&f.State, "state", "", "")
		fs.StringVar(&f.Imported, "imported", "", "")
		fs.IntVar(&f.Limit, "limit", 0, "")
		fs.IntVar(&f.Offset, "offset", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.SearchDocuments(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderDocumentList)
	},
}

var docGetCmd = &command{
	name: "get", summary: "기록 문서 단건 (--content 는 원문만)",
	usage: `사용법: agb doc get <KEY> <파일명> [--content]

기록 문서 하나의 속성을 보입니다. 터미널이 아니면 원문을 포함한 JSON입니다.

옵션:
  --content               원문만 그대로 출력합니다. --json, --table 과 무관합니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc get", g)
		var content bool
		fs.BoolVar(&content, "content", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		switch {
		case len(pos) < 2:
			return usageErrorf("<KEY>와 <파일명> 인자가 필요합니다.")
		case len(pos) > 2:
			return usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos[2:], " "))
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.GetDocument(ctx, pos[0], pos[1])
		if err != nil {
			return err
		}
		if !content {
			return a.emit(g, resp, a.renderDocument)
		}
		d, err := decode[model.Document](resp)
		if err != nil {
			return err
		}
		if d.Content == nil {
			return errors.New("응답에 원문이 없습니다.")
		}
		_, err = io.WriteString(a.Stdout, *d.Content)
		return err
	},
}
