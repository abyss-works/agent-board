package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	headerActor      = "X-Actor"
	headerSessionID  = "X-Session-Id"
	headerClientHost = "X-Client-Host"
	logValueLimit    = 128
)

type ctxKey int

const (
	ctxTrace ctxKey = iota
	ctxReqInfo
)

// reqInfo is shared between the logging middleware and the checks that run
// inside it, so a rejection reason can be logged.
type reqInfo struct {
	reject string
}

func setReject(r *http.Request, reason string) {
	if info, ok := r.Context().Value(ctxReqInfo).(*reqInfo); ok {
		info.reject = reason
	}
}

// TraceFrom returns the validated trace headers of the request.
func TraceFrom(ctx context.Context) model.Trace {
	t, _ := ctx.Value(ctxTrace).(model.Trace)
	return t
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func truncate(s string) string {
	if len(s) > logValueLimit {
		return s[:logValueLimit]
	}
	return s
}

// logging writes one JSON line per request, rejected or not. The
// Authorization header is never logged.
func logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		info := &reqInfo{}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), ctxReqInfo, info)))
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("query", r.URL.RawQuery),
			slog.Int("status", sw.status),
			slog.String("actor", truncate(r.Header.Get(headerActor))),
			slog.String("session_id", truncate(r.Header.Get(headerSessionID))),
			slog.String("client_host", truncate(r.Header.Get(headerClientHost))),
			slog.String("remote", r.RemoteAddr),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		}
		if info.reject != "" {
			attrs = append(attrs, slog.String("reject", info.reject))
		}
		logger.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
	})
}

// auth rejects requests whose bearer token does not match.
func auth(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			setReject(r, "unauthorized")
			writeError(w, errUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// trace requires the three tracking headers, each exactly once and well
// formed, and the actor to be declared. Any violation is a 400.
func trace(actors map[string]model.Actor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var t model.Trace
		fields := []struct {
			name  string
			dst   *string
			valid func(string) bool
		}{
			{headerActor, &t.Actor, model.ValidActorID},
			{headerSessionID, &t.SessionID, model.ValidSessionID},
			{headerClientHost, &t.ClientHost, model.ValidClientHost},
		}
		for _, f := range fields {
			values := r.Header.Values(f.name)
			switch {
			case len(values) == 0 || (len(values) == 1 && values[0] == ""):
				reject(w, r, "missing_header", f.name, f.name+" 헤더가 필요합니다.")
				return
			case len(values) > 1:
				reject(w, r, "duplicate_header", f.name, f.name+" 헤더는 하나여야 합니다.")
				return
			case !f.valid(values[0]):
				reject(w, r, "invalid_header", f.name, f.name+" 헤더 값이 규칙에 맞지 않습니다.")
				return
			}
			*f.dst = values[0]
		}
		if _, ok := actors[t.Actor]; !ok {
			setReject(r, "unknown_actor")
			writeError(w, badRequest("선언되지 않은 행위자입니다.", Detail{Field: headerActor, Reason: "unknown_actor", Message: "X-Actor 값이 서버에 선언된 행위자가 아닙니다."}))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxTrace, t)))
	})
}

func reject(w http.ResponseWriter, r *http.Request, reason, field, msg string) {
	setReject(r, reason+":"+field)
	writeError(w, badRequest("추적 헤더가 규칙에 맞지 않습니다.", Detail{Field: field, Reason: reason, Message: msg}))
}
