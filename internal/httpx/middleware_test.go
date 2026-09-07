package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/reijiokito/jigsaw-backend/internal/metrics"
)

// newTestHandler wires the same middleware order the router uses.
func newTestHandler(mux *http.ServeMux) http.Handler {
	return Chain(mux, RequestID, Recover, Telemetry(mux))
}

// TestTelemetryUsesRouteTemplate is the guard against a metrics cardinality
// explosion: the route label must be the registered pattern, never the concrete
// path, or every dungeon id would create its own time series.
func TestTelemetryUsesRouteTemplate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/dungeons/{id}", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"id": r.PathValue("id")})
	})
	h := newTestHandler(mux)

	const route = "/api/v1/dungeons/{id}"
	before := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues("GET", route, "200"))

	// Two different ids must land on the same series.
	for _, id := range []string{"abc", "def"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/dungeons/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("id %s: got status %d, want 200", id, rec.Code)
		}
	}

	after := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues("GET", route, "200"))
	if got := after - before; got != 2 {
		t.Errorf("route %q counter increased by %v, want 2 (both ids must share one series)", route, got)
	}
}

// TestTelemetryUnmatchedRoute keeps scanner traffic on a single series.
func TestTelemetryUnmatchedRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {})
	h := newTestHandler(mux)

	before := testutil.ToFloat64(
		metrics.HTTPRequestsTotal.WithLabelValues("GET", metrics.RouteUnmatched, "404"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-login.php", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", rec.Code)
	}

	after := testutil.ToFloat64(
		metrics.HTTPRequestsTotal.WithLabelValues("GET", metrics.RouteUnmatched, "404"))
	if got := after - before; got != 1 {
		t.Errorf("unmatched counter increased by %v, want 1", got)
	}
}

// TestRequestIDEchoedAndGenerated covers both correlation paths: honour the
// caller's id, and mint one when absent.
func TestRequestIDEchoedAndGenerated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {})
	h := newTestHandler(mux)

	t.Run("generated", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
		if rec.Header().Get("X-Request-ID") == "" {
			t.Error("no X-Request-ID in response")
		}
	})

	t.Run("propagated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("X-Request-ID", "upstream-id-1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-ID"); got != "upstream-id-1" {
			t.Errorf("X-Request-ID = %q, want the upstream id", got)
		}
	})
}

// TestRecoverCountsPanic asserts a panicking handler yields a 500 (never a
// dropped connection) and is counted for alerting.
func TestRecoverCountsPanic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	h := newTestHandler(mux)

	before := testutil.ToFloat64(metrics.PanicsTotal.WithLabelValues("/boom"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", rec.Code)
	}
	if got := testutil.ToFloat64(metrics.PanicsTotal.WithLabelValues("/boom")) - before; got != 1 {
		t.Errorf("panic counter increased by %v, want 1", got)
	}
}

// TestRateLimiterRejectionIsCounted ensures 429s are attributed to the limiter
// that produced them.
func TestRateLimiterRejectionIsCounted(t *testing.T) {
	// rate 0, burst 1: the first request passes, the second is rejected.
	rl := NewRateLimiter("test-scope", 0, 1, false)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	before := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("test-scope"))

	var last int
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(rec, req)
		last = rec.Code
	}

	if last != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", last)
	}
	if got := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("test-scope")) - before; got != 1 {
		t.Errorf("rate-limit counter increased by %v, want 1", got)
	}
}
