//go:build e2e

package e2e

import "testing"

func TestTemplateCheck(t *testing.T) {
	p := createProject(t, "TC")
	t.Run("an agent's body must follow the template of its type", func(t *testing.T) {
		agent := as("claude")
		for _, in := range []map[string]any{
			{"project": p.Key, "title": "본문 없음"},
			{"project": p.Key, "title": "자유 본문", "body": "메모입니다."},
			{"project": p.Key, "title": "다른 종류", "type": "bug", "body": templateBody(t, "feature")},
		} {
			e := wantError(t, agent.do(t, "POST", "/v1/issues", in), 422, "validation_failed")
			if !e.hasField("body") {
				t.Errorf("%v: details = %+v", in, e.Error.Details)
			}
		}
		for _, typ := range []string{"feature", "bug", "design", "idea", "observation"} {
			in := map[string]any{"project": p.Key, "title": "템플릿 " + typ, "type": typ, "body": templateBody(t, typ)}
			if typ == "observation" {
				in["recheck_condition"] = "다음 릴리즈 뒤"
			}
			createIssue(t, agent, in)
		}
	})
	t.Run("the user is not checked", func(t *testing.T) {
		iss := newIssue(t, p.Key, "빠른 메모", map[string]any{"body": "그냥 적는다."})
		if iss.Body == nil || *iss.Body != "그냥 적는다." {
			t.Errorf("body = %v", iss.Body)
		}
		newIssue(t, p.Key, "본문 없는 메모", nil)
	})
	t.Run("only a changed body or type is checked on update", func(t *testing.T) {
		legacy := newIssue(t, p.Key, "legacy", map[string]any{"body": "자유 본문"})
		agent := as("claude")
		patchIssue(t, agent, legacy.Key, map[string]any{"title": "제목만 바꿈", "status": "doing"})
		for _, patch := range []map[string]any{{"body": "새 메모"}, {"type": "bug"}} {
			e := wantError(t, agent.do(t, "PATCH", "/v1/issues/"+legacy.Key, patch), 422, "validation_failed")
			if !e.hasField("body") {
				t.Errorf("%v: details = %+v", patch, e.Error.Details)
			}
		}
		got := patchIssue(t, agent, legacy.Key, map[string]any{"type": "bug", "body": templateBody(t, "bug")})
		if got.Type != "bug" {
			t.Errorf("patched = %+v", got)
		}
		patchIssue(t, owner, legacy.Key, map[string]any{"body": "다시 자유롭게"})
	})
}
