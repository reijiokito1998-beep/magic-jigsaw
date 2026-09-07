package metrics

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// exposedNames returns every jigsaw_* metric family this service can publish.
//
// Prometheus omits a labelled family until it has at least one child, so each
// one is seeded with a zero-valued series first.
func exposedNames(t *testing.T) map[string]bool {
	t.Helper()

	HTTPRequestsTotal.WithLabelValues("GET", "/x", "200").Add(0)
	HTTPRequestDuration.WithLabelValues("GET", "/x", "200").Observe(0)
	HTTPResponseSize.WithLabelValues("/x").Observe(0)
	PanicsTotal.WithLabelValues("/x").Add(0)
	RateLimitedTotal.WithLabelValues("global").Add(0)
	AuthEventsTotal.WithLabelValues("login", "success").Add(0)
	PurchaseVerificationsTotal.WithLabelValues("app_store", "success").Add(0)
	PurchaseVerifyDuration.WithLabelValues("app_store").Observe(0)
	GameEventsTotal.WithLabelValues("expedition", "start").Add(0)
	PointsTotal.WithLabelValues("granted", "purchase").Add(0)
	UploadsTotal.WithLabelValues("image", "success").Add(0)
	UploadDuration.WithLabelValues("image").Observe(0)
	SetBuildInfo("test", "commit", "go", "test")

	// The pool collector is registered by main, not by init.
	pool, err := pgxpool.New(t.Context(), "postgres://u@127.0.0.1:1/d?sslmode=disable")
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	defer pool.Close()
	_ = Register(NewPoolCollector(pool)) // ignore "already registered" on re-run

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	names := make(map[string]bool, len(families))
	for _, f := range families {
		if strings.HasPrefix(f.GetName(), "jigsaw_") {
			names[f.GetName()] = true
		}
	}
	return names
}

// anyWithPrefix reports whether any known metric starts with prefix.
func anyWithPrefix(known map[string]bool, prefix string) bool {
	for name := range known {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// metricRef matches a metric identifier in PromQL or a dashboard query.
var metricRef = regexp.MustCompile(`jigsaw_[a-z0-9_]+`)

// histogramSuffixes are appended by the exposition format, not declared by us.
var histogramSuffixes = []string{"_bucket", "_sum", "_count"}

// TestDeployedQueriesReferenceRealMetrics is the contract between the code and
// the checked-in dashboards/alerts: renaming a metric here must not silently
// leave a Grafana panel or an alert rule querying a series that no longer
// exists (which renders as "No data" rather than as an error).
func TestDeployedQueriesReferenceRealMetrics(t *testing.T) {
	known := exposedNames(t)
	if len(known) == 0 {
		t.Fatal("no jigsaw_* metrics exposed; the registry is empty")
	}

	files := []string{
		filepath.Join("..", "..", "deploy", "prometheus", "alerts.yml"),
		filepath.Join("..", "..", "deploy", "grafana", "dashboards", "jigsaw-backend.json"),
	}

	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		seen := map[string]bool{}
		for _, ref := range metricRef.FindAllString(string(content), -1) {
			if seen[ref] {
				continue
			}
			seen[ref] = true

			// Prose in annotations refers to families by prefix
			// ("check jigsaw_db_* metrics"); require the prefix to match
			// something rather than treating it as a metric name.
			if strings.HasSuffix(ref, "_") {
				if !anyWithPrefix(known, ref) {
					t.Errorf("%s references prefix %q, which matches no metric", filepath.Base(path), ref)
				}
				continue
			}

			name := ref
			for _, suffix := range histogramSuffixes {
				if base := strings.TrimSuffix(name, suffix); base != name && known[base] {
					name = base
					break
				}
			}
			if !known[name] {
				t.Errorf("%s references unknown metric %q", filepath.Base(path), ref)
			}
		}
	}
}
