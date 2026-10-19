package api

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

var (
	errDeviceNotFound   = errors.New("device authorization not found")
	errDevicePending    = errors.New("device authorization is pending")
	errDeviceDenied     = errors.New("device authorization is denied")
	errDeviceNotPending = errors.New("device authorization is not pending")
	errTooManyDevices   = errors.New("too many pending device authorizations")
	errNoFreeCode       = errors.New("no free device code or user code")
)

// deviceRequest is one device authorization. It lives in memory only and is
// gone when it is exchanged, when its denial has been reported, when it
// expires, and when the server restarts.
type deviceRequest struct {
	deviceCode string
	userCode   string
	actor      string
	clientHost string
	status     model.DeviceStatus
	approvedBy string
	createdAt  time.Time
	expiresAt  time.Time
}

func (d deviceRequest) view() model.DeviceAuthorization {
	return model.DeviceAuthorization{
		UserCode: d.userCode, Actor: d.actor, ClientHost: d.clientHost, Status: d.status,
		CreatedAt: model.NewTime(d.createdAt), ExpiresAt: model.NewTime(d.expiresAt),
	}
}

// userCodeAttempts bounds the search for an unused user code.
const userCodeAttempts = 8

// deviceStore keeps the device authorizations that wait for a decision or for
// the exchange of their device code.
type deviceStore struct {
	mu     sync.Mutex
	now    func() time.Time
	rand   io.Reader
	byCode map[string]*deviceRequest // by device code
	byUser map[string]*deviceRequest // by user code in its canonical form
}

func newDeviceStore(now func() time.Time, rand io.Reader) *deviceStore {
	return &deviceStore{now: now, rand: rand, byCode: map[string]*deviceRequest{}, byUser: map[string]*deviceRequest{}}
}

func (s *deviceStore) remove(d *deviceRequest) {
	delete(s.byCode, d.deviceCode)
	delete(s.byUser, d.userCode)
}

// sweep drops the requests that have expired at now. The caller holds mu.
func (s *deviceStore) sweep(now time.Time) {
	for _, d := range s.byCode {
		if !now.Before(d.expiresAt) {
			s.remove(d)
		}
	}
}

// newDeviceCode reads 32 bytes: the secret the client trades for a token.
func (s *deviceStore) newDeviceCode() (string, error) {
	var b [32]byte
	if _, err := io.ReadFull(s.rand, b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// newUserCode reads the letters of a user code one byte at a time. A byte
// from 240 up is skipped, because 240 is the largest multiple of the 20 letters
// in a byte and a plain remainder would favor the first letters.
func (s *deviceStore) newUserCode() (string, error) {
	alphabet := model.UserCodeAlphabet
	limit := 256 - 256%len(alphabet)
	var chars [model.UserCodeLength]byte
	var b [1]byte
	for n := 0; n < len(chars); {
		if _, err := io.ReadFull(s.rand, b[:]); err != nil {
			return "", err
		}
		if int(b[0]) >= limit {
			continue
		}
		chars[n] = alphabet[int(b[0])%len(alphabet)]
		n++
	}
	return model.FormatUserCode(string(chars[:])), nil
}

// create adds a pending request for an agent actor on a device. It fails with
// errTooManyDevices when MaxPendingDevices requests already wait for a decision.
func (s *deviceStore) create(actor, clientHost string) (deviceRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweep(now)
	pending := 0
	for _, d := range s.byCode {
		if d.status == model.DevicePending {
			pending++
		}
	}
	if pending >= model.MaxPendingDevices {
		return deviceRequest{}, errTooManyDevices
	}
	deviceCode, err := s.newDeviceCode()
	if err != nil {
		return deviceRequest{}, err
	}
	if _, taken := s.byCode[deviceCode]; taken {
		return deviceRequest{}, errNoFreeCode
	}
	var userCode string
	for attempt := 0; ; attempt++ {
		if attempt == userCodeAttempts {
			return deviceRequest{}, errNoFreeCode
		}
		if userCode, err = s.newUserCode(); err != nil {
			return deviceRequest{}, err
		}
		if _, taken := s.byUser[userCode]; !taken {
			break
		}
	}
	d := &deviceRequest{
		deviceCode: deviceCode, userCode: userCode, actor: actor, clientHost: clientHost, status: model.DevicePending,
		createdAt: now, expiresAt: now.Add(model.DeviceExpiresIn * time.Second),
	}
	s.byCode[deviceCode], s.byUser[userCode] = d, d
	return *d, nil
}

// get returns the live request with the user code, which is in its canonical
// form.
func (s *deviceStore) get(userCode string) (deviceRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.now())
	d, ok := s.byUser[userCode]
	if !ok {
		return deviceRequest{}, errDeviceNotFound
	}
	return *d, nil
}

// decide approves or denies a pending request in the name of the user.
func (s *deviceStore) decide(userCode string, status model.DeviceStatus, by string) (deviceRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.now())
	d, ok := s.byUser[userCode]
	if !ok {
		return deviceRequest{}, errDeviceNotFound
	}
	if d.status != model.DevicePending {
		return deviceRequest{}, errDeviceNotPending
	}
	d.status = status
	if status == model.DeviceApproved {
		d.approvedBy = by
	}
	return *d, nil
}

// take settles the exchange of a device code. It returns the request when it
// is approved and removes it. A pending request stays and gives
// errDevicePending. A denied request is removed and gives errDeviceDenied once.
// A code that is unknown, expired or already exchanged gives errDeviceNotFound.
func (s *deviceStore) take(deviceCode string) (deviceRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.now())
	d, ok := s.byCode[deviceCode]
	if !ok {
		return deviceRequest{}, errDeviceNotFound
	}
	switch d.status {
	case model.DevicePending:
		return deviceRequest{}, errDevicePending
	case model.DeviceDenied:
		s.remove(d)
		return deviceRequest{}, errDeviceDenied
	}
	s.remove(d)
	return *d, nil
}

func (h *handler) registerDevices(mux *http.ServeMux) {
	route(mux, "/v1/device-authorizations", methods{"POST": h.createDeviceAuthorization})
	route(mux, "/v1/device-authorizations/{user_code}", methods{
		"GET":   h.userOnly(h.getDeviceAuthorization),
		"PATCH": h.userOnly(h.patchDeviceAuthorization),
	})
}

// deviceError maps the results of the device store to API errors.
func (h *handler) deviceError(r *http.Request, err error) *apiError {
	switch {
	case errors.Is(err, errDeviceNotFound):
		return notFound("대상이 없습니다.")
	case errors.Is(err, errDeviceNotPending):
		return conflict("not_pending", "이미 승인되거나 거부된 요청입니다.")
	case errors.Is(err, errTooManyDevices):
		return tooManyRequests("승인을 기다리는 요청이 너무 많습니다. 잠시 뒤에 다시 시도하세요.")
	}
	h.opts.Logger.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	return errInternal
}

func (h *handler) createDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"actor": true, "client_host": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	// A null or missing field stays empty, and validation names it.
	var actor, host string
	for _, f := range []struct {
		name string
		dst  *string
	}{{"actor", &actor}, {"client_host", &host}} {
		if _, _, aerr := field(b, f.name, f.dst); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	if err := model.ValidateDeviceRequest(actor, host, h.actors); err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	d, err := h.devices.create(actor, host)
	if err != nil {
		writeError(w, h.deviceError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/device-authorizations/"+d.userCode)
	writeJSON(w, http.StatusCreated, model.DeviceGrant{
		DeviceCode: d.deviceCode, UserCode: d.userCode, ExpiresIn: model.DeviceExpiresIn, Interval: model.DeviceInterval,
	})
}

// userCodeParam reads the {user_code} path value. A value that is not a user
// code names no request.
func userCodeParam(r *http.Request) (string, *apiError) {
	code, ok := model.NormalizeUserCode(r.PathValue("user_code"))
	if !ok {
		return "", notFound("대상이 없습니다.")
	}
	return code, nil
}

func (h *handler) getDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	code, aerr := userCodeParam(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	d, err := h.devices.get(code)
	if err != nil {
		writeError(w, h.deviceError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, d.view())
}

func (h *handler) patchDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	code, aerr := userCodeParam(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var status model.DeviceStatus
	if _, _, aerr := field(b, "status", &status); aerr != nil {
		writeError(w, aerr)
		return
	}
	if status != model.DeviceApproved && status != model.DeviceDenied {
		writeError(w, validationFailed([]Detail{{Field: "status", Message: "approved, denied 중 하나여야 합니다."}}))
		return
	}
	d, err := h.devices.decide(code, status, TraceFrom(r.Context()).Actor)
	if err != nil {
		writeError(w, h.deviceError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, d.view())
}
