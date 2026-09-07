// Package httpx provides small HTTP helpers: JSON responses, a standard error
// envelope, and middleware.
package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/reijiokito/jigsaw-backend/internal/logx"
)

// ErrorBody is the standard error envelope: {"error": {"code", "message"}}.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the error payload.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response failed", slog.String("component", "httpx"), slog.Any("error", err))
	}
}

// Error writes a standard error envelope.
//
// Severity follows the status class: 5xx is a server fault and logged at ERROR;
// 4xx is caller error and logged at DEBUG, because the access-log line already
// records it at WARN and duplicating it at WARN would double log volume for
// ordinary 401s.
func Error(w http.ResponseWriter, status int, code, message string) {
	ErrorCtx(context.Background(), w, status, code, message)
}

// ErrorCtx is Error with the request context, so the log line carries the
// request id and the response body echoes it back for support tickets.
func ErrorCtx(ctx context.Context, w http.ResponseWriter, status int, code, message string) {
	l := logx.FromContext(ctx)
	attrs := []any{
		slog.Int("status", status),
		slog.String("code", code),
		slog.String("message", message),
	}
	if status >= 500 {
		l.Error("api error", attrs...)
	} else {
		l.Debug("api error", attrs...)
	}
	JSON(w, status, ErrorBody{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		RequestID: logx.RequestIDFrom(ctx),
	}})
}
