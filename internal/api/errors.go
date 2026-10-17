package api

import (
	"encoding/json"
	"net/http"
)

// Detail locates one problem inside an error response.
type Detail struct {
	Field   string `json:"field,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type errorBody struct {
	Error struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Details []Detail `json:"details,omitempty"`
	} `json:"error"`
}

// apiError is an error that maps directly to an HTTP response.
type apiError struct {
	Status  int
	Code    string
	Message string
	Details []Detail
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func badRequest(msg string, details ...Detail) *apiError {
	return &apiError{Status: 400, Code: "bad_request", Message: msg, Details: details}
}

func notFound(msg string) *apiError {
	return &apiError{Status: 404, Code: "not_found", Message: msg}
}

func conflict(reason, msg string) *apiError {
	return &apiError{Status: 409, Code: "conflict", Message: msg, Details: []Detail{{Reason: reason}}}
}

func validationFailed(details []Detail) *apiError {
	return &apiError{Status: 422, Code: "validation_failed", Message: "요청 값이 규칙에 맞지 않습니다.", Details: details}
}

var (
	errUnauthorized       = &apiError{Status: 401, Code: "unauthorized", Message: "유효한 Bearer 토큰이 필요합니다."}
	errPreconditionFailed = &apiError{Status: 412, Code: "precondition_failed", Message: "If-Match의 버전이 현재 버전과 다릅니다."}
	errPayloadTooLarge    = &apiError{Status: 413, Code: "payload_too_large", Message: "요청 본문은 1MiB를 넘을 수 없습니다."}
	errInternal           = &apiError{Status: 500, Code: "internal", Message: "서버 오류입니다."}
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, e *apiError) {
	var body errorBody
	body.Error.Code = e.Code
	body.Error.Message = e.Message
	body.Error.Details = e.Details
	writeJSON(w, e.Status, body)
}
