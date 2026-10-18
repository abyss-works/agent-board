package cli

import (
	"context"
	"flag"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var issueCmd = &command{name: "issue", summary: "이슈 관리", subs: []*command{
	issueListCmd, issueGetCmd, issueCreateCmd, issueUpdateCmd, issueDeleteCmd, issueAckCmd, issueUnackCmd, issueCommentCmd, issueActivityCmd,
}}

// issueFilterFlags registers the shared list filters.
func issueFilterFlags(fs *flag.FlagSet, f *client.IssueFilter, labels *stringList) {
	fs.StringVar(&f.Project, "project", "", "")
	fs.StringVar(&f.Project, "p", "", "")
	fs.StringVar(&f.Status, "status", "", "")
	fs.StringVar(&f.Type, "type", "", "")
	fs.StringVar(&f.Priority, "priority", "", "")
	fs.Var(labels, "label", "")
	fs.StringVar(&f.Parent, "parent", "", "")
	fs.StringVar(&f.Acknowledged, "acknowledged", "", "")
	fs.StringVar(&f.CreatedBy, "created-by", "", "")
	fs.StringVar(&f.UpdatedBy, "updated-by", "", "")
	fs.StringVar(&f.StatusChangedBy, "status-changed-by", "", "")
	fs.StringVar(&f.Sort, "sort", "", "")
	fs.IntVar(&f.Limit, "limit", 0, "")
	fs.IntVar(&f.Offset, "offset", 0, "")
}

const issueFilterUsage = `  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR)
  --status <S>            backlog, todo, doing, review, done, cancelled, deleted 를 쉼표로 잇거나
                          open(열린 넷), closed, all. 기본 open
  --type <T>              feature, bug, design, decision, idea, observation. 쉼표 OR
  --priority <P>          P0, P1, P2, P3. 쉼표 OR
  --label <L>             라벨. 반복하면 AND
  --parent <KEY|none>     상위 이슈. none 이면 상위가 없는 이슈
  --acknowledged <true|false>  사용자가 확인한 이슈(true)나 아직 확인하지 않은 이슈(false)만
  --created-by, --updated-by, --status-changed-by <행위자>   쉼표 OR
  --sort <F>              priority, created_at, updated_at, key, relevance. 쉼표로 여러 개, - 는 내림차순
  --limit <N>             최대 200. 기본 50
  --offset <N>
`

var issueListCmd = &command{
	name: "list", summary: "이슈 목록 (필터, 정렬, 검색)",
	usage: `사용법: agb issue list [옵션]

옵션:
` + issueFilterUsage + `  -q <검색어>             전문 검색. 결과에 snippet 이 붙습니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("issue list", g)
		var f client.IssueFilter
		var labels stringList
		issueFilterFlags(fs, &f, &labels)
		fs.StringVar(&f.Query, "q", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		f.Labels = labels
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.ListIssues(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderIssueList)
	},
}

var searchCmd = &command{
	name: "search", summary: "전문 검색",
	usage: `사용법: agb search <검색어...> [옵션]

이슈의 키, 제목, 본문을 검색합니다. 여러 단어는 모두 포함(AND)이고, 전체를 큰따옴표로
감싸면 구(phrase) 하나로 찾습니다. agb issue list -q 와 같습니다. 상태 기본값은 open 이므로
닫힌 이슈까지 찾으려면 --status all 을 줍니다.

옵션:
` + issueFilterUsage,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("search", g)
		var f client.IssueFilter
		var labels stringList
		issueFilterFlags(fs, &f, &labels)
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageErrorf("검색어가 필요합니다.")
		}
		f.Query = strings.Join(pos, " ")
		f.Labels = labels
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.ListIssues(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderIssueList)
	},
}

var issueGetCmd = &command{
	name: "get", summary: "이슈 단건 (본문 포함)",
	usage: `사용법: agb issue get <KEY>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("issue get", g), args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.GetIssue(ctx, key)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderIssue)
	},
}

const bodyUsage = `  --body <텍스트>         본문(마크다운). 짧은 본문용
  --body-file <경로|->    본문을 파일에서 읽습니다. - 는 표준 입력. 긴 본문과 에이전트용
`

var issueCreateCmd = &command{
	name: "create", summary: "이슈 생성",
	usage: `사용법: agb issue create --project <P> --title <제목> [옵션]

옵션:
  --project, -p <P>       프로젝트 키 (필수)
  --title, -t <제목>      1~200자 (필수)
  --type <T>              feature(기본), bug, design, decision, idea, observation
  --priority <P>          P0, P1, P2(기본), P3
  --status <S>            기본 backlog
` + bodyUsage + `  --label <L>             라벨. 반복 가능
  --parent <KEY>          상위 이슈
  --depends-on <KEY>      선행 이슈. 반복 가능
  --ref <종류:값>         참조. 종류는 repo, file, url, pr, issue, doc. 반복 가능
  --recheck <조건>        observation 의 재검토 조건 (observation 이면 필수)
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("issue create", g)
		var project, title, typ, priority, status, body, bodyFile, parent, recheck string
		var labels, deps, refs stringList
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&title, "t", "", "")
		fs.StringVar(&typ, "type", "", "")
		fs.StringVar(&priority, "priority", "", "")
		fs.StringVar(&status, "status", "", "")
		fs.StringVar(&body, "body", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
		fs.Var(&labels, "label", "")
		fs.StringVar(&parent, "parent", "", "")
		fs.Var(&deps, "depends-on", "")
		fs.Var(&refs, "ref", "")
		fs.StringVar(&recheck, "recheck", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if project == "" {
			return usageErrorf("--project 가 필요합니다.")
		}
		if title == "" {
			return usageErrorf("--title 이 필요합니다.")
		}
		in := map[string]any{"project": project, "title": title}
		for name, v := range map[string]string{"type": typ, "priority": priority, "status": status, "parent": parent, "recheck_condition": recheck} {
			if v != "" {
				in[name] = v
			}
		}
		text, present, err := a.readBody(fs, body, bodyFile)
		if err != nil {
			return err
		}
		if present {
			in["body"] = text
		}
		for name, list := range map[string]stringList{"labels": labels, "depends_on": deps, "refs": refs} {
			if len(list) > 0 {
				in[name] = []string(list)
			}
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.CreateIssue(ctx, in)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderIssue)
	},
}

// arrayEdit is the replace / add / remove triple of one array field.
type arrayEdit struct {
	field   string
	flag    string
	replace stringList
	add     stringList
	remove  stringList
}

func (e *arrayEdit) register(fs *flag.FlagSet) {
	fs.Var(&e.replace, e.flag, "")
	fs.Var(&e.add, "add-"+e.flag, "")
	fs.Var(&e.remove, "remove-"+e.flag, "")
}

func (e *arrayEdit) edits() bool { return len(e.add) > 0 || len(e.remove) > 0 }

// apply merges the edit into the current array, keeping order and
// dropping duplicates.
func (e *arrayEdit) apply(current []string) []string {
	drop := map[string]bool{}
	for _, v := range e.remove {
		drop[v] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range append(append([]string{}, current...), e.add...) {
		if drop[v] || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

var issueUpdateCmd = &command{
	name: "update", summary: "이슈 수정 (JSON Merge Patch, 상태 변경 포함)",
	usage: `사용법: agb issue update <KEY> [옵션]

준 옵션만 바꿉니다. 배열(--label, --depends-on, --ref)은 통째로 바뀌고, --add-*/--remove-* 는
현재 값을 읽은 뒤 ETag 를 If-Match 로 보내 동시 수정을 막습니다. 그 사이 바뀌었으면 412 로 실패합니다.

옵션:
  --title <제목>
  --type <T>              observation 으로 바꾸면 --recheck 를 함께 줍니다
  --priority <P>
  --status <S>            backlog, todo, doing, review, done, cancelled, deleted
  --reason <사유>         상태 변경의 사유. 1~500자. cancelled, deleted 로 바꿀 때는 필수이고,
                          다른 상태로 바꿀 때 생략하면 서버가 "이전 상태 → 새 상태"를 적습니다.
                          상태를 바꾸지 않는 요청에는 쓸 수 없습니다
` + bodyUsage + `  --label <L>             라벨 전체를 이 값들로 바꿈. 반복 가능
  --add-label <L>, --remove-label <L>
  --parent <KEY>          상위 이슈 설정
  --no-parent             상위 이슈 제거
  --depends-on <KEY>      선행 이슈 전체 교체. 반복 가능
  --add-depends-on <KEY>, --remove-depends-on <KEY>
  --ref <종류:값>         참조 전체 교체. 반복 가능
  --add-ref <R>, --remove-ref <R>
  --recheck <조건>        재검토 조건 설정
  --no-recheck            재검토 조건 제거 (type 을 observation 에서 바꿀 때)
  --if-match <버전>       이 버전일 때만 수정. 숫자만 주어도 됩니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("issue update", g)
		var title, typ, priority, status, reason, body, bodyFile, parent, recheck, ifMatch string
		var noParent, noRecheck bool
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&typ, "type", "", "")
		fs.StringVar(&priority, "priority", "", "")
		fs.StringVar(&status, "status", "", "")
		fs.StringVar(&reason, "reason", "", "")
		fs.StringVar(&body, "body", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
		fs.StringVar(&parent, "parent", "", "")
		fs.BoolVar(&noParent, "no-parent", false, "")
		fs.StringVar(&recheck, "recheck", "", "")
		fs.BoolVar(&noRecheck, "no-recheck", false, "")
		fs.StringVar(&ifMatch, "if-match", "", "")
		arrays := []*arrayEdit{{field: "labels", flag: "label"}, {field: "depends_on", flag: "depends-on"}, {field: "refs", flag: "ref"}}
		for _, e := range arrays {
			e.register(fs)
		}
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}

		patch := map[string]any{}
		for name, v := range map[string]*string{"title": &title, "type": &typ, "priority": &priority, "status": &status} {
			if isSet(fs, name) {
				patch[name] = *v
			}
		}
		if isSet(fs, "reason") {
			patch["status_reason"] = reason
		}
		text, present, err := a.readBody(fs, body, bodyFile)
		if err != nil {
			return err
		}
		if present {
			patch["body"] = text
		}
		switch {
		case isSet(fs, "parent") && noParent:
			return usageErrorf("--parent 와 --no-parent 는 함께 쓸 수 없습니다.")
		case isSet(fs, "parent"):
			patch["parent"] = parent
		case noParent:
			patch["parent"] = nil
		}
		switch {
		case isSet(fs, "recheck") && noRecheck:
			return usageErrorf("--recheck 와 --no-recheck 는 함께 쓸 수 없습니다.")
		case isSet(fs, "recheck"):
			patch["recheck_condition"] = recheck
		case noRecheck:
			patch["recheck_condition"] = nil
		}
		needRead := false
		for _, e := range arrays {
			if len(e.replace) > 0 && e.edits() {
				return usageErrorf("--%s 와 --add-%s/--remove-%s 는 함께 쓸 수 없습니다.", e.flag, e.flag, e.flag)
			}
			if len(e.replace) > 0 {
				patch[e.field] = []string(e.replace)
			}
			if e.edits() {
				needRead = true
			}
		}
		if len(patch) == 0 && !needRead {
			return usageErrorf("바꿀 필드가 없습니다.")
		}

		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		version := quoteETag(ifMatch)
		if needRead {
			resp, err := c.GetIssue(ctx, key)
			if err != nil {
				return err
			}
			cur, err := decode[model.Issue](resp)
			if err != nil {
				return err
			}
			current := map[string][]string{"labels": cur.Labels, "depends_on": cur.DependsOn, "refs": cur.Refs}
			for _, e := range arrays {
				if e.edits() {
					patch[e.field] = e.apply(current[e.field])
				}
			}
			if version == "" {
				version = resp.ETag()
			}
		}
		resp, err := c.PatchIssue(ctx, key, patch, version)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderIssue)
	},
}

var issueDeleteCmd = &command{
	name: "delete", summary: "이슈 소프트 삭제",
	usage: `사용법: agb issue delete <KEY> --reason <사유> [--if-match <버전>]

상태를 deleted 로 바꿉니다. 되살리려면 agb issue update <KEY> --status <상태>.

옵션:
  --reason <사유>         삭제 사유. 1~500자 (필수)
  --if-match <버전>       이 버전일 때만 삭제. 숫자만 주어도 됩니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("issue delete", g)
		var reason, ifMatch string
		fs.StringVar(&reason, "reason", "", "")
		fs.StringVar(&ifMatch, "if-match", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		if !isSet(fs, "reason") {
			return usageErrorf("--reason 이 필요합니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.DeleteIssue(ctx, key, reason, quoteETag(ifMatch))
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderIssue)
	},
}

var issueAckCmd = &command{
	name: "ack", summary: "사용자 확인 표시 (여러 개 가능)",
	usage: `사용법: agb issue ack <KEY>...

사용자가 이슈를 확인했다고 표시합니다. 작업 흐름(상태, 큐, 막힘)에는 영향이 없습니다.
이미 확인된 이슈는 그대로 둡니다. 여러 키를 주면 차례로 처리하고, 처음 실패하는 키에서 멈춥니다.
JSON 출력은 키마다 서버 응답을 한 줄씩 냅니다. 확인은 사용자의 몫이므로 에이전트는 쓰지 않습니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("issue ack", g), args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageErrorf("<KEY> 인자가 필요합니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		for _, key := range pos {
			resp, err := c.Acknowledge(ctx, key)
			if err != nil {
				return err
			}
			if err := a.emit(g, resp, a.renderIssue); err != nil {
				return err
			}
		}
		return nil
	},
}

var issueUnackCmd = &command{
	name: "unack", summary: "사용자 확인 표시 해제",
	usage: `사용법: agb issue unack <KEY>

이슈의 확인 표시를 지워 미확인으로 되돌립니다. 이미 미확인이면 그대로 둡니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("issue unack", g), args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Unacknowledge(ctx, key)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderIssue)
	},
}

var issueCommentCmd = &command{
	name: "comment", summary: "댓글 추가",
	usage: `사용법: agb issue comment <KEY> [--summary <요약>] (--body <텍스트> | --body-file <경로|->)

옵션:
  --summary <요약>        한 줄 요약. 1~30자, 줄바꿈 없음. 목록에 보이는 색인
` + bodyUsage,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("issue comment", g)
		var summary, body, bodyFile string
		fs.StringVar(&summary, "summary", "", "")
		fs.StringVar(&body, "body", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		text, present, err := a.readBody(fs, body, bodyFile)
		if err != nil {
			return err
		}
		if !present {
			return usageErrorf("--body 또는 --body-file 이 필요합니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.AddComment(ctx, key, text, summary)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderActivity)
	},
}

var issueActivityCmd = &command{
	name: "activity", summary: "이슈 활동 기록 (최신 먼저)",
	usage: `사용법: agb issue activity <KEY> [--limit <N>] [--offset <N>]
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("issue activity", g)
		var p client.Page
		fs.IntVar(&p.Limit, "limit", 0, "")
		fs.IntVar(&p.Offset, "offset", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.IssueActivity(ctx, key, p)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderActivityList)
	},
}
