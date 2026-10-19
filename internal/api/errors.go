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

func forbidden(msg string, d Detail) *apiError {
	return &apiError{Status: 403, Code: "forbidden", Message: msg, Details: []Detail{d}}
}

func tooManyRequests(msg string) *apiError {
	return &apiError{Status: 429, Code: "too_many_requests", Message: msg}
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

	errActorMismatch = forbidden("토큰과 X-Actor가 맞지 않습니다.",
		Detail{Field: headerActor, Reason: "actor_mismatch", Message: "공용 토큰은 사용자 행위자만, 행위자 토큰은 그 토큰의 행위자만 쓸 수 있습니다."})
	errUserOnly = forbidden("사용자 행위자만 호출할 수 있습니다.",
		Detail{Reason: "user_only", Message: "행위자 토큰으로는 호출할 수 없는 경로입니다."})
	errSharedToken = forbidden("공용 토큰으로는 자기 토큰을 폐기할 수 없습니다.",
		Detail{Reason: "shared_token", Message: "행위자 토큰으로 호출하거나 DELETE /v1/tokens/{id}를 쓰세요."})

	// The three answers of a token exchange that has no token to give.
	errAuthorizationPending = &apiError{Status: 400, Code: "authorization_pending", Message: "아직 승인되지 않았습니다. 승인을 기다려 주세요."}
	errAccessDenied         = &apiError{Status: 400, Code: "access_denied", Message: "승인이 거부되었습니다."}
	errExpiredToken         = &apiError{Status: 400, Code: "expired_token", Message: "기기 승인 요청이 없거나 만료됐거나 이미 교환됐습니다."}
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
