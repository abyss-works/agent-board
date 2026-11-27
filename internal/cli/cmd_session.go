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

var sessionCmd = &command{name: "session", summary: "에이전트 세션 목록·단건·수정·자식 세션 시작", subs: []*command{
	sessionListCmd, sessionGetCmd, sessionSetCmd, sessionStartCmd,
}, note: `세션은 agb 를 부르기만 하면 서버에 자동으로 등록되고, 부를 때마다 마지막 호출 시각이 갱신됩니다(1분 단위).
마지막 호출에서 30분이 지난 세션은 멈춤(stale)으로 보입니다. 서브에이전트에 일을 맡길 때는
agb session start 로 자식 세션을 만들고, 서브에이전트가 모든 agb 명령에 --session-id <자식 세션 ID> 를 붙이게 합니다.
`}

// sessionList is the response of the session list endpoint.
type sessionList struct {
	Items  []model.Session `json:"items"`
	Total  int             `json:"total"`
	Offset int             `json:"offset"`
}

// parseSessionArg reads "<actor>/<session ID>" and validates both segments so
// path segments like ".." or extra slashes are rejected before reaching URLs.
func parseSessionArg(v, what string) (model.SessionRef, error) {
	actor, id, ok := strings.Cut(v, "/")
	if !ok || actor == "" || id == "" {
		return model.SessionRef{}, usageErrorf("%s는 <행위자>/<세션 ID> 형식이어야 합니다: %q", what, v)
	}
	if !model.ValidActorID(actor) || !model.ValidSessionID(id) {
		return model.SessionRef{}, usageErrorf("%s의 행위자 또는 세션 ID 형식이 맞지 않습니다: %q", what, v)
	}
	return model.SessionRef{Actor: actor, SessionID: id}, nil
}

// sessionTarget is the session a command acts on: the <actor/session>
// argument, or the actor and session ID of this run.
func sessionTarget(pos []string, c *client.Client) (model.SessionRef, error) {
	switch len(pos) {
	case 0:
		return model.SessionRef{Actor: c.Actor, SessionID: c.SessionID}, nil
	case 1:
		return parseSessionArg(pos[0], "<행위자/세션>")
	}
	return model.SessionRef{}, usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos[1:], " "))
}

func sessionRefText(r *model.SessionRef) string {
	if r == nil {
		return "-"
	}
	return r.Actor + "/" + r.SessionID
}

func staleText(stale bool) string {
	if stale {
		return "yes"
	}
	return "-"
}

func conflictCountText(conflicts []model.SessionRef) string {
	if len(conflicts) == 0 {
		return "-"
	}
	return strconv.Itoa(len(conflicts))
}

func formatSessionRefs(refs []model.SessionRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.Actor + "/" + r.SessionID
	}
	return strings.Join(parts, ", ")
}

func (a *App) warnLocationConflicts(resp *client.Response) {
	var sess model.Session
	if err := resp.Decode(&sess); err != nil || len(sess.LocationConflicts) == 0 {
		return
	}
	fmt.Fprintf(a.Stderr, "agb: 경고: 같은 저장소·브랜치를 쓰는 활성 세션이 있습니다: %s\n", formatSessionRefs(sess.LocationConflicts))
}

func (a *App) renderSessionList(w io.Writer, resp *client.Response) error {
	l, err := decode[sessionList](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "세션이 없습니다.")
		return nil
	}
	tb := &table{header: []string{"ACTOR", "SESSION", "ROLE", "STATE", "PARENT", "ISSUE", "HOST", "PROJECT", "LAST_SEEN", "STALE", "CONFLICTS"}}
	for _, s := range l.Items {
		tb.add(s.Actor, s.SessionID, deref(s.Role, "-"), deref(s.State, "-"), sessionRefText(s.Parent), deref(s.Issue, "-"), s.LastHost,
			deref(s.LastProject, "-"), formatTime(s.LastSeenAt, a.Location), staleText(s.Stale), conflictCountText(s.LocationConflicts))
	}
	tb.write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

func (a *App) renderSession(w io.Writer, resp *client.Response) error {
	s, err := decode[model.Session](resp)
	if err != nil {
		return err
	}
	pairs := [][2]string{
		{"actor", s.Actor},
		{"session", s.SessionID},
		{"named", strconv.FormatBool(s.Named)},
		{"role", deref(s.Role, "-")},
		{"parent", sessionRefText(s.Parent)},
		{"issue", deref(s.Issue, "-")},
		{"state", deref(s.State, "-")},
		{"state_reason", deref(s.StateReason, "-")},
		{"state_at", formatTimePtr(s.StateAt, a.Location)},
		{"host", s.LastHost},
		{"project", deref(s.LastProject, "-")},
		{"first_seen", formatTime(s.FirstSeenAt, a.Location)},
		{"last_seen", formatTime(s.LastSeenAt, a.Location)},
		{"stale", strconv.FormatBool(s.Stale)},
	}
	if l := s.Location; l != nil {
		dirty := "-"
		if l.DirtyCount != nil {
			dirty = strconv.Itoa(*l.DirtyCount)
		}
		pairs = append(pairs,
			[2]string{"repo", deref(l.RepoRemote, "-")},
			[2]string{"branch", deref(l.Branch, "-") + " (HEAD " + deref(l.Head, "-") + ")"},
			[2]string{"worktree", deref(l.Worktree, "-")},
			[2]string{"dirty", dirty},
			[2]string{"reported", formatTime(l.ReportedAt, a.Location)})
	}
	if len(s.LocationConflicts) > 0 {
		pairs = append(pairs, [2]string{"location_conflicts", formatSessionRefs(s.LocationConflicts)})
	}
	pairs = append(pairs, [2]string{"updated", formatTime(s.UpdatedAt, a.Location)})
	fields(w, pairs)
	if s.Children == nil {
		return nil
	}
	fmt.Fprintln(w, "\n== 자식 세션 ==")
	if len(*s.Children) == 0 {
		fmt.Fprintln(w, "자식 세션이 없습니다.")
		return nil
	}
	tb := &table{header: []string{"ACTOR", "SESSION", "ROLE", "STATE", "ISSUE", "HOST", "LAST_SEEN", "STALE", "CONFLICTS"}}
	for _, c := range *s.Children {
		tb.add(c.Actor, c.SessionID, deref(c.Role, "-"), deref(c.State, "-"), deref(c.Issue, "-"), c.LastHost, formatTime(c.LastSeenAt, a.Location), staleText(c.Stale), conflictCountText(c.LocationConflicts))
	}
	tb.write(w, a.TermWidth)
	return nil
}

var sessionListCmd = &command{
	name: "list", summary: "세션 목록 (마지막 호출이 최근인 순)",
	usage: `사용법: agb session list [--project <KEY>] [--actor <ID>] [--parent <행위자/세션>] [--active | --stale] [--named true|false] [--waiting]

서버가 본 에이전트 세션을 마지막 호출이 최근인 순으로 보입니다. LAST_SEEN 은 마지막 호출 시각(1분 단위)이고,
STALE 은 마지막 호출에서 30분이 지났는지입니다. STATE 는 대기 상태(waiting_approval, waiting_input, waiting_external)입니다.
PROJECT 는 그 세션이 마지막으로 기록을 남긴 프로젝트입니다.
예: agb session list --active --fields actor,session_id,role,issue --text

옵션:
  --project, -p <KEY>     마지막으로 기록을 남긴 프로젝트가 이 키인 세션만
  --actor <ID>            이 행위자의 세션만
  --parent <행위자/세션>  이 세션의 자식 세션만
  --active                멈추지 않은 세션만 (마지막 호출에서 30분 이내)
  --stale                 멈춘 세션만. --active 와 함께 쓸 수 없음
  --named true|false      false 면 CLI 기본 세션 ID(cli-YYYYMMDD)인 세션만, true 면 그 밖의 세션만
  --waiting               대기 상태인 세션만
  --limit <N>             최대 200. 기본 50
  --offset <N>

주의: --actor 는 이 명령 뒤에서는 필터입니다. 요청의 행위자는 명령 앞 --actor, AGB_ACTOR 또는 설정 파일로 정합니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		// --actor filters here, so skip it from globals.
		fs := leafFlags("session list", g, "actor")
		var f client.SessionFilter
		var active, stale, waiting bool
		var named string
		fs.StringVar(&f.Project, "project", "", "")
		fs.StringVar(&f.Project, "p", "", "")
		fs.StringVar(&f.Actor, "actor", "", "")
		fs.StringVar(&f.Parent, "parent", "", "")
		fs.BoolVar(&active, "active", false, "")
		fs.BoolVar(&stale, "stale", false, "")
		fs.StringVar(&named, "named", "", "")
		fs.BoolVar(&waiting, "waiting", false, "")
		fs.IntVar(&f.Page.Limit, "limit", 0, "")
		fs.IntVar(&f.Page.Offset, "offset", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if active && stale {
			return usageErrorf("--active 와 --stale 은 함께 쓸 수 없습니다.")
		}
		if active || stale {
			f.Active = &active
		}
		if waiting {
			f.Waiting = &waiting
		}
		switch named {
		case "":
		case "true", "false":
			v := named == "true"
			f.Named = &v
		default:
			return usageErrorf("--named 는 true 또는 false 여야 합니다: %q", named)
		}
		if f.Parent != "" {
			if _, err := parseSessionArg(f.Parent, "--parent"); err != nil {
				return err
			}
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.ListSessions(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderSessionList)
	},
}

var sessionGetCmd = &command{
	name: "get", summary: "세션 단건과 자식 세션",
	usage: `사용법: agb session get [<행위자/세션>]

세션 하나와 그 자식 세션 목록을 보입니다. 인자를 생략하면 이 명령의 행위자와 세션 ID
(--actor, --session-id, AGB_ACTOR, AGB_SESSION_ID, 설정 파일 순)로 정해진 세션입니다.
예: agb session get claude/claude-abc
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("session get", g), args)
		if err != nil {
			return err
		}
		if len(pos) > 1 {
			return usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos[1:], " "))
		}
		if len(pos) == 1 {
			if _, err := parseSessionArg(pos[0], "<행위자/세션>"); err != nil {
				return err
			}
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		ref, err := sessionTarget(pos, c)
		if err != nil {
			return err
		}
		resp, err := c.GetSession(ctx, ref.Actor, ref.SessionID)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderSession)
	},
}

var sessionSetCmd = &command{
	name: "set", summary: "세션의 부모·역할·이슈·대기 상태·작업 위치 수정",
	usage: `사용법: agb session set [<행위자/세션>] [--parent <행위자/세션>|none] [--role <역할>|none] [--issue <KEY>|none] [--state <상태>|none] [--reason <사유>|none] [--location [--repo <폴더>] | --clear-location]

세션의 부모 세션, 역할, 맡은 이슈, 대기 상태, 작업 위치를 고칩니다. 준 옵션만 바꾸고 none 은 그 값을 지웁니다.
인자를 생략하면 이 명령의 행위자와 세션 ID로 정해진 세션입니다. 다른 행위자의 세션은 사용자 행위자만 고칩니다.

옵션:
  --parent <행위자/세션>|none  부모 세션. 자기 자신이나 자기 자손은 안 됨
  --role <역할>|none           main, subagent, runner 중 하나
  --issue <KEY>|none           맡은 이슈 키
  --state <상태>|none          대기 상태: waiting_approval, waiting_input, waiting_external, none(해제)
  --reason <사유>|none         대기 사유 (1~200자, 제어 문자 불가, none 이면 사유만 지움)
  --location                   --repo 폴더의 git 정보(원격 주소, 브랜치, 저장소 최상위 경로, HEAD,
                               커밋하지 않은 파일 수)로 작업 위치를 통째로 바꿈. 파일 내용은 읽지 않음
  --repo <폴더>                --location 이 읽을 저장소. 생략하면 현재 폴더
  --clear-location             작업 위치를 지움. --location 과 함께 쓸 수 없음
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("session set", g)
		var parent, role, issue, state, reason, repo string
		var location, clearLocation bool
		fs.StringVar(&parent, "parent", "", "")
		fs.StringVar(&role, "role", "", "")
		fs.StringVar(&issue, "issue", "", "")
		fs.StringVar(&state, "state", "", "")
		fs.StringVar(&reason, "reason", "", "")
		fs.BoolVar(&location, "location", false, "")
		fs.StringVar(&repo, "repo", "", "")
		fs.BoolVar(&clearLocation, "clear-location", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) > 1 {
			return usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos[1:], " "))
		}
		if len(pos) == 1 {
			if _, err := parseSessionArg(pos[0], "<행위자/세션>"); err != nil {
				return err
			}
		}
		if location && clearLocation {
			return usageErrorf("--location 과 --clear-location 은 함께 쓸 수 없습니다.")
		}
		if isSet(fs, "repo") && !location {
			return usageErrorf("--repo 는 --location 과 함께 씁니다.")
		}
		patch := map[string]any{}
		if isSet(fs, "parent") {
			if parent == "none" {
				patch["parent"] = nil
			} else {
				ref, err := parseSessionArg(parent, "--parent")
				if err != nil {
					return err
				}
				patch["parent"] = map[string]any{"actor": ref.Actor, "session_id": ref.SessionID}
			}
		}
		for _, opt := range []struct {
			flag, field, val string
		}{
			{"role", "role", role},
			{"issue", "issue", issue},
			{"state", "state", state},
			{"reason", "state_reason", reason},
		} {
			if !isSet(fs, opt.flag) {
				continue
			}
			if opt.val == "none" {
				patch[opt.field] = nil
			} else {
				patch[opt.field] = opt.val
			}
		}
		if clearLocation {
			patch["location"] = nil
		}
		if len(patch) == 0 && !location {
			return usageErrorf("바꿀 옵션이 필요합니다: --parent, --role, --issue, --state, --reason, --location, --clear-location")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		ref, err := sessionTarget(pos, c)
		if err != nil {
			return err
		}
		if location {
			if repo == "" {
				repo = "."
			}
			loc, err := sessionLocation(repo)
			if err != nil {
				return err
			}
			patch["location"] = loc
		}
		resp, err := c.UpdateSession(ctx, ref.Actor, ref.SessionID, patch)
		if err != nil {
			return err
		}
		if location {
			a.warnLocationConflicts(resp)
		}
		return a.emit(g, resp, a.renderSession)
	},
}

var sessionStartCmd = &command{
	name: "start", summary: "이 세션 아래에 자식 세션 만들기 (서브에이전트용)",
	usage: `사용법: agb session start [--role <역할>] [--issue <KEY>] [--id <자식 세션 ID>]

이 명령의 세션을 부모로 하는 자식 세션을 만들고 그 세션 ID를 출력합니다. 서브에이전트에 일을 맡길 때
출력된 ID를 맡기는 지시에 넣고, 서브에이전트는 모든 agb 명령에 --session-id <그 ID> 를 붙입니다.
부모 세션에 역할이 없으면 main 이 됩니다.

옵션:
  --role <역할>           main, subagent, runner 중 하나. 생략하면 subagent
  --issue <KEY>           자식 세션이 맡을 이슈 키
  --id <자식 세션 ID>     생략하면 서버가 <부모 세션 ID>.<영숫자 6자> 로 만듦
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("session start", g)
		var role, issue, id string
		fs.StringVar(&role, "role", "", "")
		fs.StringVar(&issue, "issue", "", "")
		fs.StringVar(&id, "id", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		in := map[string]any{}
		for name, v := range map[string]string{"role": role, "issue": issue, "session_id": id} {
			if v != "" {
				in[name] = v
			}
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.StartSession(ctx, in)
		if err != nil {
			return err
		}
		return a.emit(g, resp, func(w io.Writer, resp *client.Response) error {
			s, err := decode[model.Session](resp)
			if err != nil {
				return err
			}
			fmt.Fprintln(w, s.SessionID)
			fmt.Fprintf(w, "이 세션으로 기록하려면: agb --session-id %s <명령>\n", s.SessionID)
			return nil
		})
	},
}
