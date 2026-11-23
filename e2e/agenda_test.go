//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestAgenda(t *testing.T) {
	p := createProject(t, "AG")
	other := createProject(t, "AG2")
	wait := newIssue(t, p.Key, "MR 대기", map[string]any{"labels": strs("external")})
	tick()
	blocked := newIssue(t, p.Key, "MR 뒤 작업", map[string]any{"depends_on": strs(wait.Key)})
	tick()
	ready := newIssue(t, p.Key, "바로 할 일", nil)
	tick()
	review := newIssue(t, p.Key, "화면 확인", map[string]any{"status": "review"})
	tick()
	task := newIssue(t, p.Key, "메일 수신 확인", map[string]any{"labels": strs("user-task")})
	newIssue(t, other.Key, "다른 프로젝트 일", nil)

	type agenda struct {
		GeneratedAt string `json:"generated_at"`
		Projects    []struct {
			Project  project `json:"project"`
			UserTurn struct {
				Review          section[issue]   `json:"review"`
				Decisions       section[issue]   `json:"decisions"`
				Unverified      section[issue]   `json:"unverified"`
				Tasks           section[issue]   `json:"tasks"`
				WaitingSessions section[session] `json:"waiting_sessions"`
			} `json:"user_turn"`
			Agent struct {
				Doing section[issue] `json:"doing"`
				Next  section[issue] `json:"next"`
			} `json:"agent"`
			Waiting struct {
				Blocked  section[issue] `json:"blocked"`
				External section[issue] `json:"external"`
			} `json:"waiting"`
		} `json:"projects"`
	}
	a := decodeAs[agenda](t, owner.do(t, "GET", "/v1/agenda?project="+p.Key+","+other.Key, nil), 200)
	wantTime(t, "generated_at", a.GeneratedAt)
	if len(a.Projects) != 2 || a.Projects[0].Project.Key != p.Key || a.Projects[1].Project.Key != other.Key {
		t.Fatalf("projects = %+v", a.Projects)
	}
	ag := a.Projects[0]
	if ag.UserTurn.Review.Total != 1 || ag.UserTurn.Review.Items[0].Key != review.Key {
		t.Errorf("review = %+v", ag.UserTurn.Review)
	}
	if ag.UserTurn.Tasks.Total != 1 || ag.UserTurn.Tasks.Items[0].Key != task.Key {
		t.Errorf("tasks = %+v", ag.UserTurn.Tasks)
	}
	if ag.Agent.Next.Total != 1 || ag.Agent.Next.Items[0].Key != ready.Key {
		t.Errorf("next = %+v", ag.Agent.Next)
	}
	if ag.Waiting.Blocked.Total != 1 || ag.Waiting.Blocked.Items[0].Key != blocked.Key {
		t.Errorf("blocked = %+v", ag.Waiting.Blocked)
	}
	if ag.Waiting.External.Total != 1 || ag.Waiting.External.Items[0].Key != wait.Key {
		t.Errorf("external = %+v", ag.Waiting.External)
	}
	if a.Projects[1].Agent.Next.Total != 1 {
		t.Errorf("other next = %+v", a.Projects[1].Agent.Next)
	}
	wantError(t, owner.do(t, "GET", "/v1/agenda?project="+projectKey("NOPE"), nil), 404, "not_found")

	t.Run("decision waiting_sessions", func(t *testing.T) {
		dp := createProject(t, "AGW")
		dNoWait := newIssue(t, dp.Key, "D1 no waiting", map[string]any{"priority": "P0", "decision_needed": "안건 A 선택"})
		dWithWait := newIssue(t, dp.Key, "D2 with waiting", map[string]any{"priority": "P1", "decision_needed": "안건 B 확인"})

		w := caller{actor: "claude", session: "e2e-agw-" + runID, host: defaultHost}
		expect(t, w.do(t, "GET", "/v1/actors/me", nil), 200)
		expect(t, w.do(t, "PATCH", "/v1/sessions/claude/"+w.session, map[string]any{
			"issue":        dWithWait.Key,
			"state":        "waiting_input",
			"state_reason": "옵션 확인 필요",
		}), 200)
		t.Cleanup(func() {
			w.do(t, "PATCH", "/v1/sessions/claude/"+w.session, map[string]any{"state": nil, "issue": nil})
		})

		res := expect(t, owner.do(t, "GET", "/v1/agenda?project="+dp.Key, nil), 200)
		raw := string(res.body)
		if strings.Contains(raw, `"waiting_sessions":null`) || strings.Contains(raw, `"location_conflicts":null`) {
			t.Errorf("agenda JSON contains null array: %s", raw)
		}
		got := decode[agenda](t, res)
		if len(got.Projects) != 1 {
			t.Fatalf("projects = %+v", got.Projects)
		}
		ut := got.Projects[0].UserTurn
		if ut.Decisions.Total != 2 || len(ut.Decisions.Items) != 2 {
			t.Fatalf("decisions = %+v", ut.Decisions)
		}
		if ut.Decisions.Items[0].Key != dNoWait.Key || ut.Decisions.Items[0].WaitingSessions == nil || len(ut.Decisions.Items[0].WaitingSessions) != 0 {
			t.Errorf("dNoWait waiting_sessions = %#v, want non-nil empty", ut.Decisions.Items[0].WaitingSessions)
		}
		ws := ut.Decisions.Items[1].WaitingSessions
		if ut.Decisions.Items[1].Key != dWithWait.Key || len(ws) != 1 {
			t.Fatalf("dWithWait waiting_sessions = %+v", ws)
		}
		if ws[0].Actor != "claude" || ws[0].SessionID != w.session || ws[0].State == nil || *ws[0].State != "waiting_input" || ws[0].StateReason == nil || *ws[0].StateReason != "옵션 확인 필요" {
			t.Errorf("decision waiting session = %+v", ws[0])
		}
		if ut.WaitingSessions.Total != 1 || len(ut.WaitingSessions.Items) != 1 || ut.WaitingSessions.Items[0].SessionID != w.session {
			t.Errorf("user_turn waiting_sessions = %+v", ut.WaitingSessions)
		}
	})
}
