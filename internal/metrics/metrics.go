// Package metrics owns the Prometheus registry and every collector the service
// exposes on /metrics.
//
// Design rules:
//
//   - One private registry, not the global default one, so what we publish is
//     explicit and testable.
//   - Every label value is drawn from a bounded set. Route labels come from the
//     ServeMux pattern ("/api/v1/dungeons/{id}") rather than the raw URL path,
//     otherwise each dungeon id would create a new time series.
//   - This package imports nothing from the rest of the app, so any layer can
//     record a metric without creating an import cycle.
package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "jigsaw"

// registry holds every collector this service exposes.
var registry = prometheus.NewRegistry()

// RouteUnmatched is the route label used when no mux pattern matched, keeping
// scanner traffic and typos from exploding label cardinality.
const RouteUnmatched = "unmatched"

var (
	// --- HTTP ---------------------------------------------------------------

	// HTTPRequestsTotal counts finished requests. Combined with the duration
	// histogram this is enough for RED dashboards (rate, errors, duration).
	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total HTTP requests served, by method, route template and status code.",
	}, []string{"method", "route", "status"})

	// HTTPRequestDuration measures end-to-end handler latency. Buckets are
	// tuned for a mobile game API: most calls are single-digit milliseconds,
	// with image uploads in the multi-second tail.
	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request latency in seconds.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"method", "route", "status"})

	// HTTPRequestsInFlight exposes concurrency, which is what saturates the
	// database connection pool long before CPU becomes the limit.
	HTTPRequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "requests_in_flight",
		Help:      "Number of HTTP requests currently being served.",
	})

	// HTTPResponseSize tracks payload sizes; useful for spotting an endpoint
	// that started returning unbounded lists.
	HTTPResponseSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "response_size_bytes",
		Help:      "HTTP response body size in bytes.",
		Buckets:   prometheus.ExponentialBuckets(256, 4, 8), // 256B .. 4MB
	}, []string{"route"})

	// PanicsTotal counts recovered panics. Any non-zero rate deserves an alert.
	PanicsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "panics_total",
		Help:      "Recovered handler panics, by route template.",
	}, []string{"route"})

	// RateLimitedTotal counts requests rejected with 429, split by which
	// limiter rejected them (global, or the stricter per-hour register and
	// login limiters).
	RateLimitedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "rate_limited_total",
		Help:      "Requests rejected by the in-process rate limiter, by limiter scope.",
	}, []string{"scope"})

	// --- Domain -------------------------------------------------------------

	// AuthEventsTotal tracks register/login outcomes. A spike of failures on
	// login is the earliest signal of a credential-stuffing attempt.
	AuthEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "auth",
		Name:      "events_total",
		Help:      "Authentication events, by event (register|login) and result (success|failure).",
	}, []string{"event", "result"})

	// PurchaseVerificationsTotal tracks in-app-purchase receipt verification
	// against Apple/Google. Failures here mean players paid without being
	// credited, so this is a revenue-critical series.
	PurchaseVerificationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "purchase",
		Name:      "verifications_total",
		Help:      "In-app purchase verifications, by platform and result (success|failure|unsupported).",
	}, []string{"platform", "result"})

	// PurchaseVerifyDuration measures the round-trip to the store's
	// verification API, an external dependency that can stall a checkout.
	PurchaseVerifyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "purchase",
		Name:      "verify_duration_seconds",
		Help:      "Latency of store receipt verification calls, by platform.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"platform"})

	// GameEventsTotal tracks gameplay progression, giving product-level
	// funnels (starts vs completes vs fails) per game mode.
	GameEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "game",
		Name:      "events_total",
		Help:      "Gameplay events, by mode (expedition|story|dungeon|challenge|checkin) and event (start|complete|fail|move|retreat|monster_defeated).",
	}, []string{"mode", "event"})

	// PointsTotal tracks the in-game currency flow. Grants and spends should
	// stay in a plausible ratio; a sudden divergence means an exploit.
	PointsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "game",
		Name:      "points_total",
		Help:      "In-game points moved, by direction (granted|spent) and source.",
	}, []string{"direction", "source"})

	// --- Infrastructure -----------------------------------------------------

	// UploadsTotal tracks Cloudinary uploads, an external dependency whose
	// failures are invisible in HTTP status codes alone when retried.
	UploadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "storage",
		Name:      "uploads_total",
		Help:      "Image uploads to object storage, by kind and result (success|failure).",
	}, []string{"kind", "result"})

	// UploadDuration measures upload latency to the storage provider.
	UploadDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "storage",
		Name:      "upload_duration_seconds",
		Help:      "Object-storage upload latency in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"kind"})

	// buildInfo is a constant-1 gauge whose labels carry release metadata, the
	// conventional way to correlate a metrics change with a deploy.
	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build metadata as labels; the value is always 1.",
	}, []string{"version", "commit", "go_version", "env"})
)

func init() {
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),

		HTTPRequestsTotal,
		HTTPRequestDuration,
		HTTPRequestsInFlight,
		HTTPResponseSize,
		PanicsTotal,
		RateLimitedTotal,

		AuthEventsTotal,
		PurchaseVerificationsTotal,
		PurchaseVerifyDuration,
		GameEventsTotal,
		PointsTotal,

		UploadsTotal,
		UploadDuration,
		buildInfo,
	)
}

// SetBuildInfo publishes release metadata. Call once at startup.
func SetBuildInfo(version, commit, goVersion, env string) {
	buildInfo.WithLabelValues(version, commit, goVersion, env).Set(1)
}

// Register adds an extra collector (e.g. the database pool collector) to the
// service registry.
func Register(c prometheus.Collector) error { return registry.Register(c) }

// Handler returns the /metrics HTTP handler for the service registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		// Report scrape-time collector errors as 500s rather than silently
		// serving partial data.
		ErrorHandling:       promhttp.HTTPErrorOnError,
		EnableOpenMetrics:   true,
		MaxRequestsInFlight: 4,
	})
}

// ObserveRequest records the standard HTTP series for one finished request.
func ObserveRequest(method, route string, status int, seconds float64, responseBytes int64) {
	code := strconv.Itoa(status)
	HTTPRequestsTotal.WithLabelValues(method, route, code).Inc()
	HTTPRequestDuration.WithLabelValues(method, route, code).Observe(seconds)
	if responseBytes >= 0 {
		HTTPResponseSize.WithLabelValues(route).Observe(float64(responseBytes))
	}
}

// Result maps an error to the "success"/"failure" label value, so call sites
// stay one-liners.
func Result(err error) string {
	if err != nil {
		return "failure"
	}
	return "success"
}
