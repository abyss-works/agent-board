package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerMessages(mux *http.ServeMux) {
	route(mux, "/v1/projects/{key}/messages", methods{"GET": h.listMessages, "POST": h.createMessage})
	route(mux, "/v1/messages/{id}", methods{"GET": h.getMessage, "DELETE": h.deleteMessage})
	route(mux, "/v1/messages/{id}/ack", methods{"POST": h.ackMessage})
}

// messageID reads the {id} path value. An id that is not a positive integer
// names no message.
func messageID(r *http.Request) (int64, *apiError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, notFound("대상이 없습니다.")
	}
	return id, nil
}

func (h *handler) listMessages(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	tr := TraceFrom(r.Context())
	f := store.MessageFilter{
		Project:  r.PathValue("key"),
		To:       q.one("to"),
		UnreadBy: q.one("unread_by"),
		Viewer:   &model.SessionRef{Actor: tr.Actor, SessionID: tr.SessionID},
	}
	if v := q.one("to_session"); v != "" {
		if ref, ok := parseSessionRef(v); ok {
			f.ToSession = &ref
		} else {
			q.add("to_session", "<행위자>/<세션 ID> 형식이어야 합니다.")
		}
	}
	if v := q.one("to_role"); v != "" {
		if _, ok := model.ParseSessionRole(v); !ok {
			q.add("to_role", "main, subagent, runner 중 하나여야 합니다.")
		} else {
			f.ToRole = v
		}
	}
	if v := q.one("issue"); v != "" {
		if !model.ValidIssueKey(v) {
			q.add("issue", "이슈 키 형식(예: AGB-12)이어야 합니다.")
		} else {
			f.Issue = v
		}
	}
	if v := q.one("reply_to"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			q.add("reply_to", "1 이상의 정수여야 합니다.")
		} else {
			f.ReplyTo = &id
		}
	}
	if f.UnreadBy != "" && model.ValidateMessageReader(f.UnreadBy) != nil {
		q.add("unread_by", fmt.Sprintf("공백과 제어 문자 없이 1~%d자여야 합니다.", model.MaxMessageToRunes))
	}
	if all := parseBool(q, "all"); all != nil {
		f.All = *all
	}
	f.Limit, f.Offset = q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.ListMessages(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) createMessage(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{
		"to": true, "to_session": true, "to_role": true, "issue": true, "reply_to": true,
		"title": true, "body": true, "ttl": true,
	})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	// A null is left at the zero value: no addressee, and validation reports
	// an empty title.
	var in store.MessageInput
	for name, dst := range map[string]*string{
		"to": &in.To, "to_role": &in.ToRole, "issue": &in.Issue,
		"title": &in.Title, "body": &in.Body, "ttl": &in.TTL,
	} {
		if _, _, aerr := field(b, name, dst); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	var details []Detail
	if raw, ok := b["to_session"]; ok && !isNull(raw) {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil || m == nil {
			details = append(details, Detail{Field: "to_session", Reason: "invalid", Message: `{"actor","session_id"} 객체여야 합니다.`})
		} else if ref, ok := sessionRefObject(m); !ok {
			details = append(details, Detail{Field: "to_session", Reason: "invalid", Message: `{"actor","session_id"} 객체여야 합니다.`})
		} else {
			in.ToSession = &ref
		}
	}
	if raw, ok := b["reply_to"]; ok && !isNull(raw) {
		var id int64
		if json.Unmarshal(raw, &id) != nil || id < 1 {
			details = append(details, Detail{Field: "reply_to", Reason: "invalid", Message: "1 이상의 정수여야 합니다."})
		} else {
			in.ReplyTo = &id
		}
	}
	if len(details) > 0 {
		writeError(w, validationFailed(details))
		return
	}
	m, err := h.store.CreateMessage(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/messages/%d", m.ID))
	writeJSON(w, http.StatusCreated, m)
}

func (h *handler) getMessage(w http.ResponseWriter, r *http.Request) {
	id, aerr := messageID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.store.GetMessage(r.Context(), id)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) deleteMessage(w http.ResponseWriter, r *http.Request) {
	id, aerr := messageID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.store.DeleteMessage(r.Context(), TraceFrom(r.Context()), id)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// ackMessage records that the reader named in the body acknowledged a
// message. A null or missing reader is left empty for validation to report.
func (h *handler) ackMessage(w http.ResponseWriter, r *http.Request) {
	id, aerr := messageID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"reader": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var reader string
	if _, _, aerr := field(b, "reader", &reader); aerr != nil {
		writeError(w, aerr)
		return
	}
	read, err := h.store.AckMessage(r.Context(), TraceFrom(r.Context()), id, reader)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, read)
}
