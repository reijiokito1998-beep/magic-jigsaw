package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"time"

	"github.com/reijiokito/jigsaw-backend/internal/buildinfo"
	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/logx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
)

// readinessTimeout bounds the dependency check so a hung database makes the
// probe fail fast instead of piling up requests on the load balancer.
const readinessTimeout = 2 * time.Second

// healthResponse is the payload of GET /healthz.
type healthResponse struct {
	Status string `json:"status" example:"ok"`
}

// readyResponse is the payload of GET /readyz.
type readyResponse struct {
	Status string            `json:"status" example:"ready"`
	Checks map[string]string `json:"checks"`
}

// versionResponse is the payload of GET /version.
type versionResponse struct {
	Version   string `json:"version" example:"1.0.0"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	GoVersion string `json:"go_version" example:"go1.25.0"`
	Env       string `json:"env" example:"production"`
	UptimeSec int64  `json:"uptime_seconds" example:"3600"`
}

// startedAt is the process start time, used to report uptime.
var startedAt = time.Now()

// registerOpsRoutes wires the operational endpoints used by orchestrators and
// the monitoring stack. They are deliberately outside /api/v1: they are not part
// of the product API and are not versioned with it.
func (s *Server) registerOpsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /version", s.handleVersion)

	if s.cfg.MetricsEnabled {
		mux.Handle("GET "+s.cfg.MetricsPath, s.metricsHandler())
	}
}

// handleHealthz reports process liveness.
//
// It intentionally checks nothing: a liveness probe that fails when the
// database is down would make Kubernetes restart healthy pods during a database
// outage, turning a partial failure into a total one. Dependency checks belong
// in /readyz.
//
//	@Summary		Liveness probe
//	@Description	Returns 200 while the process is running. Checks no dependencies.
//	@Tags			ops
//	@Produce		json
//	@Success		200	{object}	healthResponse
//	@Router			/healthz [get]
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// handleReadyz reports whether the service can serve traffic, by verifying
// every hard dependency.
//
//	@Summary		Readiness probe
//	@Description	Verifies dependencies (database). Returns 503 when any check fails.
//	@Tags			ops
//	@Produce		json
//	@Success		200	{object}	readyResponse
//	@Failure		503	{object}	readyResponse
//	@Router			/readyz [get]
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	checks := map[string]string{}
	ready := true

	if err := s.store.Ping(ctx); err != nil {
		ready = false
		checks["database"] = "error: " + err.Error()
		logx.FromContext(r.Context()).Error("readiness check failed",
			slog.String("check", "database"), slog.Any("error", err))
	} else {
		checks["database"] = "ok"
	}

	status, body := http.StatusOK, "ready"
	if !ready {
		status, body = http.StatusServiceUnavailable, "not_ready"
	}
	httpx.JSON(w, status, readyResponse{Status: body, Checks: checks})
}

// handleVersion reports build metadata, so a deploy can be confirmed without
// shell access and Grafana can annotate releases.
//
//	@Summary		Build and runtime information
//	@Tags			ops
//	@Produce		json
//	@Success		200	{object}	versionResponse
//	@Router			/version [get]
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, versionResponse{
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		BuildDate: buildinfo.Date,
		GoVersion: buildinfo.GoVersion(),
		Env:       s.cfg.AppEnv,
		UptimeSec: int64(time.Since(startedAt).Seconds()),
	})
}

// metricsHandler serves the Prometheus exposition format.
//
// When METRICS_TOKEN is set the scrape must present it as a bearer token.
// /metrics leaks operational detail (route names, error rates, pool size), so on
// a publicly reachable service it should be either token-protected or bound to a
// private network.
//
//	@Summary		Prometheus metrics
//	@Description	Prometheus/OpenMetrics exposition. Requires a bearer token when METRICS_TOKEN is configured.
//	@Tags			ops
//	@Produce		plain
//	@Success		200	{string}	string	"metrics in Prometheus text format"
//	@Failure		401	{object}	httpx.ErrorBody
//	@Router			/metrics [get]
func (s *Server) metricsHandler() http.Handler {
	h := metrics.Handler()
	token := s.cfg.MetricsToken
	if token == "" {
		if s.cfg.IsProduction() {
			slog.Warn("metrics endpoint is unauthenticated in production; set METRICS_TOKEN or restrict it at the network level",
				slog.String("path", s.cfg.MetricsPath))
		}
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Constant-time compare so the token cannot be recovered by timing.
		provided, _ := bearerToken(r)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			httpx.ErrorCtx(r.Context(), w, http.StatusUnauthorized, "unauthorized", "invalid metrics token")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// bearerToken extracts a bearer token from the Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return "", false
	}
	return h[len(prefix):], true
}
