// Package logx provides structured logging built on log/slog.
//
// It is the single place where log output is configured. Two things matter:
//
//  1. Output is machine-parsable JSON in production (so Loki/CloudWatch can
//     index fields) and human-readable text in development.
//  2. The standard library's `log` package is redirected through the same
//     handler, so the many existing log.Printf call sites keep working and
//     still land in the structured stream instead of bypassing it.
package logx

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strings"
)

// contextKey is the private key type for values logx stores in a context.
type contextKey int

const (
	ctxLogger contextKey = iota
	ctxRequestID
)

// Init configures the global slog default logger and bridges the stdlib logger
// onto it. level is one of debug/info/warn/error; format is json or text.
// service and version are attached to every record so logs from multiple
// deployments stay distinguishable.
func Init(level, format, service, version string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	handler = handler.WithAttrs([]slog.Attr{
		slog.String("service", service),
		slog.String("version", version),
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Route everything written via the stdlib `log` package through the same
	// handler at INFO. Flags are cleared because slog adds its own timestamp.
	log.SetFlags(0)
	log.SetOutput(slog.NewLogLogger(handler, slog.LevelInfo).Writer())

	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithLogger returns a context carrying a request-scoped logger.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxLogger, l)
}

// FromContext returns the request-scoped logger, falling back to the default
// logger so callers never have to nil-check.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxLogger).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithRequestID returns a context carrying the request correlation id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxRequestID, id)
}

// RequestIDFrom returns the request correlation id, or "" when absent.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRequestID).(string); ok {
		return v
	}
	return ""
}
