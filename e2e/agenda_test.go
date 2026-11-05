//go:build e2e

package e2e

import "testing"

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
				Review         section[issue] `json:"review"`
				Decisions      section[issue] `json:"decisions"`
				Unacknowledged section[issue] `json:"unacknowledged"`
				Tasks          section[issue] `json:"tasks"`
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
}
