package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

const (
	// sessionWriteEvery is the precision of last_seen_at: a call within this
	// long of the last write of the same session by this process is not
	// written.
	sessionWriteEvery = time.Minute
	// maxSeenSessions is how many sessions the process remembers before it
	// forgets the ones written longer than sessionWriteEvery ago.
	maxSeenSessions = 10000
	// childSuffixLength is the number of random characters after the parent
	// session ID in a child session ID the server makes.
	childSuffixLength = 6
	childSuffixChars  = "abcdefghijklmnopqrstuvwxyz0123456789"
	maxSessionIDLen   = 128
)

func (h *handler) registerSessions(mux *http.ServeMux) {
	route(mux, "/v1/sessions", methods{"GET": h.listSessions, "POST": h.startSession})
	route(mux, "/v1/sessions/{actor}/{session}", methods{"GET": h.getSession, "PATCH": h.patchSession})
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// touchSession records the call of the session of a request that passed
// authentication and the trace headers, before the route handles it. After a
// successful (2xx) write request, any waiting state on the calling session is
// cleared unless the route opted to keep it.
func (h *handler) touchSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr := TraceFrom(r.Context())
		h.recordCall(r.Context(), tr)
		sw, ok := w.(*statusWriter)
		if !ok {
			sw = &statusWriter{ResponseWriter: w}
			w = sw
		}
		wc := &waitingControl{}
		r = r.WithContext(context.WithValue(r.Context(), ctxKeepWaiting, wc))
		next.ServeHTTP(w, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		if isWriteMethod(r.Method) && status >= 200 && status < 300 && !wc.keep {
			h.clearWaiting(r.Context(), tr)
		}
	})
}

// clearWaiting clears the waiting state of the session of tr. A failed write
// is logged and does not affect the HTTP response.
func (h *handler) clearWaiting(ctx context.Context, tr model.Trace) (model.Time, bool) {
	at, cleared, err := h.store.ClearSessionWaiting(ctx, tr.Actor, tr.SessionID)
	if err != nil {
		h.opts.Logger.Warn("session waiting clear failed", "actor", tr.Actor, "session_id", truncate(tr.SessionID), "err", err.Error())
		return model.Time{}, false
	}
	return at, cleared
}

// recordCall writes the call of a session unless this process completed a
// write of that session less than sessionWriteEvery ago. A failed write is
// logged and does not fail the request; the next call tries again.
func (h *handler) recordCall(ctx context.Context, tr model.Trace) {
	now := h.opts.Now()
	key := tr.Actor + "/" + tr.SessionID
	h.seenMu.Lock()
	if last, ok := h.seen[key]; ok && now.Sub(last) < sessionWriteEvery {
		h.seenMu.Unlock()
		return
	}
	h.seenMu.Unlock()
	if err := h.store.TouchSession(ctx, tr, now); err != nil {
		h.opts.Logger.Warn("session write failed", "actor", tr.Actor, "session_id", truncate(tr.SessionID), "err", err.Error())
		return
	}
	h.seenMu.Lock()
	if len(h.seen) >= maxSeenSessions {
		for k, t := range h.seen {
			if now.Sub(t) >= sessionWriteEvery {
				delete(h.seen, k)
			}
		}
	}
	if last, ok := h.seen[key]; !ok || now.After(last) {
		h.seen[key] = now
	}
	h.seenMu.Unlock()
}

// parseSessionRef reads "<actor>/<session ID>".
func parseSessionRef(v string) (model.SessionRef, bool) {
	actor, id, ok := strings.Cut(v, "/")
	if !ok || !model.ValidActorID(actor) || !model.ValidSessionID(id) {
		return model.SessionRef{}, false
	}
	return model.SessionRef{Actor: actor, SessionID: id}, true
}

// sessionPath reads the {actor} and {session} path values. Values that are
// not well formed name no session.
func sessionPath(r *http.Request) (model.SessionRef, *apiError) {
	ref := model.SessionRef{Actor: r.PathValue("actor"), SessionID: r.PathValue("session")}
	if !model.ValidActorID(ref.Actor) || !model.ValidSessionID(ref.SessionID) {
		return ref, notFound("대상이 없습니다.")
	}
	return ref, nil
}

func (h *handler) listSessions(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.SessionFilter{Project: q.one("project"), Actor: q.one("actor")}
	if f.Project != "" && !model.ValidProjectKey(f.Project) {
		q.add("project", "프로젝트 키 형식이 아닙니다.")
	}
	if f.Actor != "" && !model.ValidActorID(f.Actor) {
		q.add("actor", "행위자 ID 형식이 아닙니다.")
	}
	if v := q.one("parent"); v != "" {
		if ref, ok := parseSessionRef(v); ok {
			f.Parent = &ref
		} else {
			q.add("parent", "<행위자>/<세션 ID> 형식이어야 합니다.")
		}
	}
	f.Active, f.Named, f.Waiting = parseBool(q, "active"), parseBool(q, "named"), parseBool(q, "waiting")
	f.Limit, f.Offset = q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.ListSessions(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) getSession(w http.ResponseWriter, r *http.Request) {
	ref, aerr := sessionPath(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	s, err := h.store.GetSession(r.Context(), ref.Actor, ref.SessionID)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, s)
}

var errNotSessionActor = forbidden("다른 행위자의 세션은 고칠 수 없습니다.",
	Detail{Field: headerActor, Reason: "not_session_actor", Message: "세션의 행위자나 사용자 행위자만 세션을 고칠 수 있습니다."})

// patchSession applies a JSON merge patch of parent, role, issue, location,
// state and state_reason. The last write wins; there is no If-Match.
func (h *handler) patchSession(w http.ResponseWriter, r *http.Request) {
	ref, aerr := sessionPath(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	tr := TraceFrom(r.Context())
	if tr.Actor != ref.Actor && h.actors[tr.Actor].Kind != model.ActorUser {
		writeError(w, errNotSessionActor)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"parent": true, "role": true, "issue": true, "location": true, "state": true, "state_reason": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch store.SessionPatch
	var details []Detail
	var parent, location map[string]json.RawMessage
	var role, issue, state, stateReason string
	steps := []func() *apiError{
		func() *apiError {
			present, null, e := field(b, "parent", &parent)
			if e != nil || !present {
				return e
			}
			patch.Parent.Set = true
			if !null {
				p, ok := sessionRefObject(parent)
				if !ok {
					details = append(details, Detail{Field: "parent", Reason: "invalid", Message: `{"actor","session_id"} 객체여야 합니다.`})
				}
				patch.Parent.Value = &p
			}
			return nil
		},
		func() *apiError {
			present, null, e := field(b, "role", &role)
			patch.Role.Set = present
			if present && !null {
				patch.Role.Value = &role
			}
			return e
		},
		func() *apiError {
			present, null, e := field(b, "issue", &issue)
			patch.Issue.Set = present
			if present && !null {
				patch.Issue.Value = &issue
			}
			return e
		},
		func() *apiError {
			present, null, e := field(b, "location", &location)
			if e != nil || !present {
				return e
			}
			patch.Location.Set = true
			if !null {
				loc, problems := parseSessionLocation(location)
				details = append(details, problems...)
				patch.Location.Value = &loc
			}
			return nil
		},
		func() *apiError {
			present, null, e := field(b, "state", &state)
			patch.State.Set = present
			if present && !null {
				patch.State.Value = &state
			}
			return e
		},
		func() *apiError {
			present, null, e := field(b, "state_reason", &stateReason)
			patch.StateReason.Set = present
			if present && !null {
				patch.StateReason.Value = &stateReason
			}
			return e
		},
	}
	for _, step := range steps {
		if aerr := step(); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	if len(details) > 0 {
		writeError(w, validationFailed(details))
		return
	}
	s, err := h.store.UpdateSession(r.Context(), tr, ref.Actor, ref.SessionID, patch)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	if ref.Actor == tr.Actor && ref.SessionID == tr.SessionID {
		keepWaiting(r)
		if !patch.State.Set && !patch.StateReason.Set {
			if at, cleared := h.clearWaiting(r.Context(), tr); cleared {
				s.State = nil
				s.StateReason = nil
				s.StateAt = nil
				s.UpdatedAt = at
			}
		}
	}
	writeJSON(w, http.StatusOK, s)
}

// sessionRefObject reads {"actor","session_id"} with nothing else in it.
func sessionRefObject(m map[string]json.RawMessage) (model.SessionRef, bool) {
	var ref model.SessionRef
	if len(m) != 2 || json.Unmarshal(m["actor"], &ref.Actor) != nil || json.Unmarshal(m["session_id"], &ref.SessionID) != nil {
		return ref, false
	}
	return ref, model.ValidActorID(ref.Actor) && model.ValidSessionID(ref.SessionID)
}

// parseSessionLocation reads a location object. Every key is optional and a
// null value leaves the key unreported. Each unknown key and each value of the
// wrong type or out of range is a problem on location.<key>.
func parseSessionLocation(m map[string]json.RawMessage) (model.SessionLocation, []Detail) {
	var loc model.SessionLocation
	var details []Detail
	text := map[string]struct {
		max int
		dst **string
	}{
		"repo_remote": {model.MaxLocationRemoteRunes, &loc.RepoRemote},
		"branch":      {model.MaxLocationBranchRunes, &loc.Branch},
		"worktree":    {model.MaxLocationWorktreeRunes, &loc.Worktree},
		"head":        {model.MaxLocationHeadRunes, &loc.Head},
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		raw, name := m[k], "location."+k
		if isNull(raw) {
			if _, ok := text[k]; ok || k == "dirty_count" {
				continue
			}
		}
		if t, ok := text[k]; ok {
			var s string
			switch {
			case json.Unmarshal(raw, &s) != nil:
				details = append(details, Detail{Field: name, Reason: "invalid", Message: "문자열이어야 합니다."})
			case utf8.RuneCountInString(s) > t.max:
				details = append(details, Detail{Field: name, Reason: "too_long", Message: fmt.Sprintf("%d자 이하여야 합니다.", t.max)})
			default:
				*t.dst = &s
			}
			continue
		}
		if k == "dirty_count" {
			var n int
			if json.Unmarshal(raw, &n) != nil || n < 0 {
				details = append(details, Detail{Field: name, Reason: "invalid", Message: "0 이상의 정수여야 합니다."})
			} else {
				loc.DirtyCount = &n
			}
			continue
		}
		details = append(details, Detail{Field: name, Reason: "unknown_field", Message: "모르는 키입니다. repo_remote, branch, worktree, head, dirty_count만 씁니다."})
	}
	return loc, details
}

// startSession registers a child of the session of the request for the same
// actor. Without a session ID the server makes one from the parent's.
func (h *handler) startSession(w http.ResponseWriter, r *http.Request) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"session_id": true, "role": true, "issue": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in store.ChildSessionInput
	var role, issue string
	if _, _, aerr := field(b, "session_id", &in.SessionID); aerr != nil {
		writeError(w, aerr)
		return
	}
	for name, dst := range map[string]struct {
		v   *string
		out **string
	}{"role": {&role, &in.Role}, "issue": {&issue, &in.Issue}} {
		present, null, aerr := field(b, name, dst.v)
		if aerr != nil {
			writeError(w, aerr)
			return
		}
		if present && !null {
			*dst.out = dst.v
		}
	}
	tr := TraceFrom(r.Context())
	if in.SessionID == "" {
		suffix, err := randomChars(h.opts.Rand, childSuffixChars, childSuffixLength)
		if err != nil {
			writeError(w, h.storeError(r, err))
			return
		}
		in.SessionID = tr.SessionID + "." + suffix
		if len(in.SessionID) > maxSessionIDLen {
			writeError(w, validationFailed([]Detail{{Field: "session_id", Reason: "too_long",
				Message: fmt.Sprintf("부모 세션 ID로 만든 세션 ID가 %d자를 넘습니다. session_id를 직접 주세요.", maxSessionIDLen)}}))
			return
		}
	}
	s, err := h.store.CreateChildSession(r.Context(), tr, in)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/sessions/"+url.PathEscape(s.Actor)+"/"+url.PathEscape(s.SessionID))
	writeJSON(w, http.StatusCreated, s)
}

// randomChars draws n characters of alphabet from rand without favoring any.
func randomChars(rand io.Reader, alphabet string, n int) (string, error) {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	var b [1]byte
	for len(out) < n {
		if _, err := io.ReadFull(rand, b[:]); err != nil {
			return "", errors.Join(errors.New("random session suffix"), err)
		}
		if int(b[0]) >= limit {
			continue
		}
		out = append(out, alphabet[int(b[0])%len(alphabet)])
	}
	return string(out), nil
}
