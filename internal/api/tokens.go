package api

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerTokens(mux *http.ServeMux) {
	route(mux, "/v1/tokens", methods{"GET": h.userOnly(h.listTokens), "POST": h.exchangeToken})
	// The literal path wins over the {id} pattern, so "current" is never an id.
	route(mux, "/v1/tokens/current", methods{"DELETE": h.revokeCurrentToken})
	route(mux, "/v1/tokens/{id}", methods{"DELETE": h.userOnly(h.revokeToken)})
}

// newTokenSecret reads 32 bytes and writes them as an actor token: the prefix
// and the bytes in base64url without padding.
func (h *handler) newTokenSecret() (string, error) {
	var b [32]byte
	if _, err := io.ReadFull(h.opts.Rand, b[:]); err != nil {
		return "", err
	}
	return model.TokenPrefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// exchangeToken trades the device code of an approved request for an actor
// token, which this response shows once.
func (h *handler) exchangeToken(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"device_code": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var code string
	if _, _, aerr := field(b, "device_code", &code); aerr != nil {
		writeError(w, aerr)
		return
	}
	if code == "" {
		writeError(w, validationFailed([]Detail{{Field: "device_code", Message: "필수입니다."}}))
		return
	}
	d, err := h.devices.take(code)
	switch {
	case errors.Is(err, errDevicePending):
		writeError(w, errAuthorizationPending)
		return
	case errors.Is(err, errDeviceDenied):
		writeError(w, errAccessDenied)
		return
	case errors.Is(err, errDeviceNotFound):
		writeError(w, errExpiredToken)
		return
	case err != nil:
		writeError(w, h.deviceError(r, err))
		return
	}
	secret, err := h.newTokenSecret()
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	tok, err := h.store.CreateToken(r.Context(), store.TokenInput{Actor: d.actor, ClientHost: d.clientHost, Secret: secret, CreatedBy: d.approvedBy})
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusCreated, model.IssuedToken{Token: *tok, Secret: secret})
}

func (h *handler) listTokens(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	filter := model.TokensActive
	if v := q.one("status"); v != "" {
		f, ok := model.ParseTokenFilter(v)
		if !ok {
			q.add("status", "active, revoked, all 중 하나여야 합니다.")
		}
		filter = f
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.store.ListTokens(r.Context(), filter)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// tokenID reads the {id} path value. An id that is not a positive integer
// names no token.
func tokenID(r *http.Request) (int64, *apiError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, notFound("대상이 없습니다.")
	}
	return id, nil
}

func (h *handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, aerr := tokenID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	tok, err := h.store.RevokeToken(r.Context(), id, TraceFrom(r.Context()).Actor)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, tok)
}

// revokeCurrentToken revokes the actor token the request carries, in the name
// of its own actor.
func (h *handler) revokeCurrentToken(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred.shared {
		writeError(w, errSharedToken)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	tok, err := h.store.RevokeToken(r.Context(), cred.tokenID, cred.actor)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, tok)
}
