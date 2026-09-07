package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/auth"
	"github.com/reijiokito/jigsaw-backend/internal/logx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/models"
)

type ctxKey int

const (
	ctxUserID ctxKey = iota
	ctxRole
	ctxScope
)

// requestIDHeader is both read (to honour an upstream proxy's id) and written
// (so clients can quote it in bug reports).
const requestIDHeader = "X-Request-ID"

// requestScope carries per-request facts that are only discovered mid-chain
// (the authenticated user, the matched route) back out to the outer logging and
// metrics middleware. It is written and read on the request's own goroutine, so
// no synchronisation is required.
type requestScope struct {
	userID string
	role   string
	route  string
}

// Chain applies middlewares in order (outermost first).
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestID assigns every request a correlation id, echoes it back in the
// response, and puts a logger pre-tagged with it into the context. Every log
// line produced while handling the request can then be joined on request_id.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get(requestIDHeader))
		if id == "" || len(id) > 128 {
			id = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, id)

		ctx := logx.WithRequestID(r.Context(), id)
		ctx = logx.WithLogger(ctx, slog.Default().With(slog.String("request_id", id)))
		ctx = context.WithValue(ctx, ctxScope, &requestScope{})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Recover turns panics into 500 responses. The stack trace is logged (never
// returned to the client) and counted, because a non-zero panic rate always
// means a bug rather than bad input.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// A client that hung up mid-response is not a server bug.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				route := routeFrom(r.Context())
				metrics.PanicsTotal.WithLabelValues(route).Inc()
				logx.FromContext(r.Context()).Error("panic recovered",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("route", route),
					slog.String("stack", string(debug.Stack())),
				)
				ErrorCtx(r.Context(), w, http.StatusInternalServerError, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Telemetry emits one structured access-log line and the Prometheus HTTP series
// per request.
//
// mux is needed to resolve the matched route *template*
// ("/api/v1/dungeons/{id}") instead of the concrete path, which keeps metric
// cardinality bounded — a per-path label would create a new time series for
// every dungeon id.
//
// quiet lists path prefixes excluded from the access log (health checks and the
// metrics scrape, which would otherwise dominate log volume). They are still
// counted in metrics.
func Telemetry(mux *http.ServeMux, quiet ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := resolveRoute(mux, r)
			if sc := scopeFrom(r.Context()); sc != nil {
				sc.route = route
			}

			metrics.HTTPRequestsInFlight.Inc()
			defer metrics.HTTPRequestsInFlight.Dec()

			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start)

			metrics.ObserveRequest(r.Method, route, rec.status, elapsed.Seconds(), rec.written)

			if isQuiet(r.URL.Path, quiet) {
				return
			}

			attrs := []any{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("route", route),
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.written),
				slog.Float64("duration_ms", float64(elapsed.Microseconds())/1000),
				slog.String("ip", r.RemoteAddr),
			}
			if q := r.URL.RawQuery; q != "" {
				attrs = append(attrs, slog.String("query", q))
			}
			if ua := r.UserAgent(); ua != "" {
				attrs = append(attrs, slog.String("user_agent", ua))
			}
			if sc := scopeFrom(r.Context()); sc != nil && sc.userID != "" {
				attrs = append(attrs, slog.String("user_id", sc.userID), slog.String("role", sc.role))
			}

			l := logx.FromContext(r.Context())
			switch {
			case rec.status >= 500:
				l.Error("request", attrs...)
			case rec.status >= 400:
				l.Warn("request", attrs...)
			default:
				l.Info("request", attrs...)
			}
		})
	}
}

// resolveRoute returns the ServeMux pattern that will handle r, stripped of its
// method prefix, or metrics.RouteUnmatched when nothing matches.
func resolveRoute(mux *http.ServeMux, r *http.Request) string {
	if mux == nil {
		return metrics.RouteUnmatched
	}
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return metrics.RouteUnmatched
	}
	// Patterns are registered as "GET /api/v1/me"; keep only the path so the
	// method stays in its own label.
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return path
	}
	return pattern
}

func isQuiet(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// SecurityHeaders sets conservative security-related response headers on every
// response. These are cheap defence-in-depth headers appropriate for a JSON API.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// MaxBytes caps the request body size for every request passing through it,
// guarding against memory-exhaustion from oversized payloads. It is a generous
// global safety net; per-endpoint limits (JSON decode, uploads) stay tighter.
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS applies permissive CORS for the configured origins.
func CORS(origins []string) func(http.Handler) http.Handler {
	allowAll := len(origins) == 0
	set := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if o == "*" {
			allowAll = true
		}
		set[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allowAll {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if _, ok := set[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth validates the Bearer token and injects the user id/role.
func RequireAuth(jwt *auth.JWTManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				ErrorCtx(r.Context(), w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			claims, err := jwt.Parse(strings.TrimSpace(token))
			if err != nil {
				ErrorCtx(r.Context(), w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return
			}
			userID, err := claims.UserID()
			if err != nil {
				ErrorCtx(r.Context(), w, http.StatusUnauthorized, "unauthorized", "invalid subject")
				return
			}
			ctx := context.WithValue(r.Context(), ctxUserID, userID)
			ctx = context.WithValue(ctx, ctxRole, claims.Role)

			// Publish the identity outward (access log) and inward (handler
			// logs) so a single request_id search shows who did what.
			if sc := scopeFrom(ctx); sc != nil {
				sc.userID = userID.String()
				sc.role = claims.Role
			}
			ctx = logx.WithLogger(ctx, logx.FromContext(ctx).With(
				slog.String("user_id", userID.String()),
			))

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin ensures the authenticated user has the admin role. Must be
// chained after RequireAuth.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RoleFrom(r.Context()) != models.RoleAdmin {
			ErrorCtx(r.Context(), w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// UserIDFrom returns the authenticated user id from the context.
func UserIDFrom(ctx context.Context) uuid.UUID {
	if v, ok := ctx.Value(ctxUserID).(uuid.UUID); ok {
		return v
	}
	return uuid.Nil
}

// RoleFrom returns the authenticated user's role from the context.
func RoleFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRole).(string); ok {
		return v
	}
	return ""
}

func scopeFrom(ctx context.Context) *requestScope {
	if v, ok := ctx.Value(ctxScope).(*requestScope); ok {
		return v
	}
	return nil
}

// routeFrom returns the matched route template for the in-flight request, for
// use as a metric label outside Telemetry.
func routeFrom(ctx context.Context) string {
	if sc := scopeFrom(ctx); sc != nil && sc.route != "" {
		return sc.route
	}
	return metrics.RouteUnmatched
}

// responseRecorder captures the status code and body size of a response so they
// can be logged and measured after the handler returns.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	written     int64
	wroteHeader bool
}

func (w *responseRecorder) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseRecorder) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	return n, err
}

// Flush and Unwrap keep http.ResponseController features (flushing, deadlines)
// working through the wrapper.
func (w *responseRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
