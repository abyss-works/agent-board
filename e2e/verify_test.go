//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestIssueVerificationGate(t *testing.T) {
	p := createProject(t, "VG")
	plain := createIssue(t, as("claude"), map[string]any{"project": p.Key, "title": "plain work", "body": templateBody(t, "feature")})
	pending := createIssue(t, as("claude"), map[string]any{
		"project":            p.Key,
		"title":              "ui polish",
		"body":               templateBody(t, "feature"),
		"verification_state": "pending",
		"verification_note":  "Check mobile layout",
	})

	t.Run("initial verification state", func(t *testing.T) {
		if plain.VerificationState != "none" || plain.VerificationNote != nil || plain.VerifiedAt != nil || plain.VerifiedBy != nil {
			t.Errorf("plain issue = %+v", plain)
		}
		if pending.VerificationState != "pending" || pending.VerificationNote == nil || *pending.VerificationNote != "Check mobile layout" || pending.VerifiedAt != nil || pending.VerifiedBy != nil {
			t.Errorf("pending issue = %+v", pending)
		}
		wantError(t, owner.do(t, "POST", "/v1/issues/"+plain.Key+"/verify", nil), 422, "validation_failed")
	})

	path := "/v1/issues/" + pending.Key + "/verify"
	t.Run("verify and unverify", func(t *testing.T) {
		r := owner.do(t, "POST", path, nil)
		got := decodeAs[issue](t, r, 200)
		if got.VerificationState != "verified" || got.VerifiedAt == nil || got.VerifiedBy == nil || *got.VerifiedBy != "owner" || got.Version != pending.Version+1 {
			t.Fatalf("verified = %+v", got)
		}
		if r.header.Get("ETag") != fmt.Sprintf(`"%d"`, got.Version) {
			t.Errorf("ETag = %q", r.header.Get("ETag"))
		}
		again := decodeAs[issue](t, owner.do(t, "POST", path, nil), 200)
		if again.Version != got.Version {
			t.Errorf("repeated verify changed version: %d -> %d", got.Version, again.Version)
		}
		acts := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+pending.Key+"/activity", nil), 200)
		ch := (*acts.Items[0].Changes)["verification_state"]
		if acts.Total != 2 || acts.Items[0].Kind != "updated" || string(ch.From) != `"pending"` || string(ch.To) != `"verified"` {
			t.Errorf("activity = total %d %+v", acts.Total, acts.Items[0])
		}
		reverted := decodeAs[issue](t, owner.do(t, "DELETE", path, nil), 200)
		if reverted.VerificationState != "pending" || reverted.VerifiedAt != nil || reverted.VerifiedBy != nil || reverted.VerificationNote == nil || *reverted.VerificationNote != "Check mobile layout" || reverted.Version != got.Version+1 {
			t.Errorf("unverified = %+v", reverted)
		}
	})

	t.Run("If-Match", func(t *testing.T) {
		cur := getIssue(t, pending.Key)
		wantError(t, owner.do(t, "POST", path, nil, http.Header{"If-Match": {fmt.Sprintf(`"%d"`, cur.Version-1)}}), 412, "precondition_failed")
		decodeAs[issue](t, owner.do(t, "POST", path, nil, http.Header{"If-Match": {fmt.Sprintf(`"%d"`, cur.Version)}}), 200)
	})

	t.Run("edits keep verification state", func(t *testing.T) {
		edited := patchIssue(t, as("claude"), pending.Key, map[string]any{"title": "ui polish updated", "status": "doing"})
		if edited.VerificationState != "verified" || edited.VerifiedAt == nil || edited.VerifiedBy == nil || *edited.VerifiedBy != "owner" {
			t.Errorf("edit reverted verification: %+v", edited)
		}
	})

	t.Run("list filter, overview, and agenda", func(t *testing.T) {
		stillPending := createIssue(t, as("claude"), map[string]any{
			"project":            p.Key,
			"title":              "dialog check",
			"body":               templateBody(t, "feature"),
			"verification_state": "pending",
		})
		q := "/v1/issues?status=all&sort=key&project=" + p.Key
		noneList := decodeAs[list[issue]](t, owner.do(t, "GET", q+"&verification=none", nil), 200)
		pendingList := decodeAs[list[issue]](t, owner.do(t, "GET", q+"&verification=pending", nil), 200)
		verifiedList := decodeAs[list[issue]](t, owner.do(t, "GET", q+"&verification=verified", nil), 200)
		if noneList.Total != 1 || pendingList.Total != 1 || verifiedList.Total != 1 {
			t.Errorf("verification counts = %d/%d/%d, want 1/1/1", noneList.Total, pendingList.Total, verifiedList.Total)
		}
		wantError(t, owner.do(t, "GET", q+"&verification=maybe", nil), 400, "bad_request")
		o := decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview", nil), 200)
		if o.Unverified.Total != 1 || len(o.Unverified.Items) != 1 || o.Unverified.Items[0].Key != stillPending.Key {
			t.Errorf("unverified section = %+v", o.Unverified)
		}
		var ag struct {
			Projects []struct {
				UserTurn struct {
					Unverified section[issue] `json:"unverified"`
				} `json:"user_turn"`
			} `json:"projects"`
		}
		ag = decodeAs[struct {
			Projects []struct {
				UserTurn struct {
					Unverified section[issue] `json:"unverified"`
				} `json:"user_turn"`
			} `json:"projects"`
		}](t, owner.do(t, "GET", "/v1/agenda?project="+p.Key, nil), 200)
		if len(ag.Projects) != 1 || ag.Projects[0].UserTurn.Unverified.Total != 1 || ag.Projects[0].UserTurn.Unverified.Items[0].Key != stillPending.Key {
			t.Errorf("agenda unverified = %+v", ag)
		}
	})

	t.Run("milestone close blocked until verified", func(t *testing.T) {
		var m struct {
			ID int64 `json:"id"`
		}
		m = decodeAs[struct {
			ID int64 `json:"id"`
		}](t, owner.do(t, "POST", "/v1/projects/"+p.Key+"/milestones", map[string]any{"name": "v1.0"}), 201)
		msPath := "/v1/milestones/" + strconv.FormatInt(m.ID, 10)
		blocker := createIssue(t, as("claude"), map[string]any{
			"project":            p.Key,
			"title":              "release blocker",
			"status":             "done",
			"body":               templateBody(t, "feature"),
			"milestone":          m.ID,
			"verification_state": "pending",
			"verification_note":  "Check responsive header",
		})
		wantError(t, owner.do(t, "PATCH", msPath, map[string]any{"status": "closed"}), 409, "unverified_issues")
		decodeAs[issue](t, owner.do(t, "POST", "/v1/issues/"+blocker.Key+"/verify", nil), 200)
		decodeAs[map[string]any](t, owner.do(t, "PATCH", msPath, map[string]any{"status": "closed"}), 200)
	})

	t.Run("unknown issue is 404", func(t *testing.T) {
		wantError(t, owner.do(t, "POST", "/v1/issues/"+p.Key+"-999/verify", nil), 404, "not_found")
	})
}
