package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/reijiokito/jigsaw-backend/internal/auth"
	"github.com/reijiokito/jigsaw-backend/internal/config"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

// newOpsTestServer builds a server whose database is unreachable, which is
// exactly the state the readiness probe has to detect. The pool is created
// lazily so no connection is attempted until a request needs one.
func newOpsTestServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()

	// Port 1 is reserved and never listening, so Ping fails fast.
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return NewServer(cfg, repository.New(pool), nil, auth.NewJWTManager(strings.Repeat("k", 32), 0), nil)
}

func opsTestConfig() *config.Config {
	return &config.Config{
		AppEnv:          "test",
		CORSOrigins:     []string{"*"},
		MetricsEnabled:  true,
		MetricsPath:     "/metrics",
		RateLimitPerSec: 1000,
		RateLimitBurst:  1000,

		RegisterRatePerHour: 1000,
		RegisterRateBurst:   1000,
		LoginRatePerHour:    1000,
		LoginRateBurst:      1000,
	}
}

func TestHealthzIgnoresDependencies(t *testing.T) {
	// Liveness must stay green while the database is down; otherwise an
	// orchestrator would restart healthy pods during a database outage.
	h := newOpsTestServer(t, opsTestConfig()).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
}

func TestReadyzFailsWhenDatabaseUnreachable(t *testing.T) {
	h := newOpsTestServer(t, opsTestConfig()).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body readyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "not_ready" {
		t.Errorf("status = %q, want %q", body.Status, "not_ready")
	}
	if got := body.Checks["database"]; !strings.HasPrefix(got, "error:") {
		t.Errorf("database check = %q, want an error string", got)
	}
}

func TestVersionEndpoint(t *testing.T) {
	h := newOpsTestServer(t, opsTestConfig()).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Version == "" || body.GoVersion == "" {
		t.Errorf("incomplete version payload: %+v", body)
	}
}

func TestMetricsEndpointExposesInstrumentation(t *testing.T) {
	h := newOpsTestServer(t, opsTestConfig()).Handler()

	// A label-less metric family is omitted from the scrape until it has at
	// least one child, so publish build info the way main does.
	metrics.SetBuildInfo("test", "abc123", "go-test", "test")

	// Generate one request so the HTTP series exist.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"jigsaw_http_requests_total",
		"jigsaw_http_request_duration_seconds",
		"jigsaw_build_info",
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape output is missing %q", want)
		}
	}
}

func TestMetricsEndpointRequiresToken(t *testing.T) {
	cfg := opsTestConfig()
	cfg.MetricsToken = "s3cret"
	h := newOpsTestServer(t, cfg).Handler()

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"correct token", "Bearer s3cret", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestMetricsCanBeDisabled(t *testing.T) {
	cfg := opsTestConfig()
	cfg.MetricsEnabled = false
	h := newOpsTestServer(t, cfg).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when METRICS_ENABLED=false", rec.Code)
	}
}
