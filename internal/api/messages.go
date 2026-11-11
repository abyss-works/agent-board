package api

import (
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
	f := store.MessageFilter{Project: r.PathValue("key"), To: q.one("to"), UnreadBy: q.one("unread_by")}
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
	b, aerr := readBody(w, r, map[string]bool{"to": true, "title": true, "body": true, "ttl": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	// A null is left at the zero value: no addressee, and validation reports
	// an empty title.
	var in store.MessageInput
	for name, dst := range map[string]*string{"to": &in.To, "title": &in.Title, "body": &in.Body, "ttl": &in.TTL} {
		if _, _, aerr := field(b, name, dst); aerr != nil {
			writeError(w, aerr)
			return
		}
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
