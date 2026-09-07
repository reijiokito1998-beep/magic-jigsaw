# Observability

Logs, metrics and operational endpoints for `jigsaw-backend`, plus the local
Prometheus + Grafana stack.

## Quick start

```bash
# 1. Run the API on the host
make run

# 2. Bring up Prometheus + Grafana (already provisioned, nothing to click)
make obs-up

#    Prometheus  http://localhost:9090/targets
#    Grafana     http://localhost:3000  (admin/admin) → folder "Jigsaw"

# 3. Sanity check the instrumentation directly
make metrics
```

`make obs-down` stops them; the TSDB and Grafana state survive in named volumes.

---

## Operational endpoints

| Endpoint   | Purpose | Notes |
|------------|---------|-------|
| `GET /healthz` | Liveness | Checks **nothing**. Deliberate: a liveness probe that fails during a database outage would make the orchestrator restart healthy pods and turn a partial failure into a total one. |
| `GET /readyz`  | Readiness | Pings the database with a 2s timeout. `200 {"status":"ready"}` or `503 {"status":"not_ready", "checks":{...}}`. Wire load balancers and Kubernetes `readinessProbe` here. |
| `GET /version` | Build info | Version, commit, Go version, env, uptime. Confirms which build is live without shell access. |
| `GET /metrics` | Prometheus scrape | OpenMetrics/Prometheus text format. Bearer-token protected when `METRICS_TOKEN` is set. |

Kubernetes example:

```yaml
livenessProbe:
  httpGet: { path: /healthz, port: 8080 }
  periodSeconds: 10
readinessProbe:
  httpGet: { path: /readyz, port: 8080 }
  periodSeconds: 5
  failureThreshold: 3
```

---

## Logging

Structured `log/slog` output — JSON when `APP_ENV=production`, human-readable
text otherwise. Configure with `LOG_LEVEL` (`debug|info|warn|error`) and
`LOG_FORMAT` (`json|text`).

The stdlib `log` package is redirected onto the same handler, so pre-existing
`log.Printf` call sites keep working and still land in the structured stream.

### Request correlation

Every request gets an id (`X-Request-ID`, honoured if the caller/proxy already
set one, generated otherwise) which is:

- echoed in the response header,
- attached to every log line produced while handling the request,
- included in error response bodies as `error.request_id`.

So a user reporting a failure quotes one id, and it retrieves the full server-side
story:

```bash
# All lines for one request
jq 'select(.request_id == "5f3c…")' app.log

# Slow requests
jq 'select(.msg == "request" and .duration_ms > 1000)' app.log

# Everything a user did
jq 'select(.user_id == "0f9a…")' app.log
```

### Access log fields

One `"request"` record per request, at `INFO` for 2xx/3xx, `WARN` for 4xx and
`ERROR` for 5xx:

| Field | Meaning |
|-------|---------|
| `request_id` | Correlation id |
| `method`, `path` | As received |
| `route` | Matched route **template** (`/api/v1/dungeons/{id}`) |
| `status`, `bytes`, `duration_ms` | Response outcome |
| `ip`, `user_agent`, `query` | Caller |
| `user_id`, `role` | Present once authenticated |

`/healthz`, `/readyz` and `/metrics` are excluded from the access log (they would
dominate log volume) but are still counted in metrics.

Panics are logged at `ERROR` with the full stack trace and counted in
`jigsaw_http_panics_total`; the client only ever sees a generic 500.

---

## Metrics

Namespace `jigsaw`. Go runtime and process collectors are included.

### HTTP (RED)

| Metric | Type | Labels |
|--------|------|--------|
| `jigsaw_http_requests_total` | counter | `method`, `route`, `status` |
| `jigsaw_http_request_duration_seconds` | histogram | `method`, `route`, `status` |
| `jigsaw_http_requests_in_flight` | gauge | — |
| `jigsaw_http_response_size_bytes` | histogram | `route` |
| `jigsaw_http_panics_total` | counter | `route` |
| `jigsaw_http_rate_limited_total` | counter | `scope` (`global`, `register`, `login`) |

**`route` is always the registered pattern, never the concrete path.** Labelling
by path would create a new time series per dungeon/story/challenge id and melt
the TSDB. Unmatched paths (scanners, typos) collapse into `route="unmatched"`.
`internal/httpx/middleware_test.go` guards this.

### Database (pgx pool)

`jigsaw_db_connections_{acquired,idle,total,max,constructing}`,
`jigsaw_db_acquires_total`, `jigsaw_db_acquires_empty_total`,
`jigsaw_db_acquires_canceled_total`, `jigsaw_db_acquire_duration_seconds_total`,
`jigsaw_db_connections_new_total`,
`jigsaw_db_connections_closed_max_{lifetime,idle}_total`.

Read at scrape time from `pool.Stat()`, so the values are never stale.

**This is the first thing to check on a latency complaint.** Pool exhaustion —
`connections_acquired` pinned at `connections_max` with `acquires_empty_total`
climbing — means requests are queueing on the database, not on CPU. Raise
`DB_MAX_CONNS`, add PgBouncer, or fix the queries holding connections.

### Product & revenue

| Metric | Labels | Why it matters |
|--------|--------|----------------|
| `jigsaw_auth_events_total` | `event` (register\|login), `result` | Failures climbing while successes stay flat = credential stuffing |
| `jigsaw_purchase_verifications_total` | `platform`, `result` (success\|failure\|unsupported) | Non-success means players paid and were **not** credited |
| `jigsaw_purchase_verify_duration_seconds` | `platform` | Apple/Google round-trip; slow verification stalls checkout |
| `jigsaw_game_events_total` | `mode`, `event` | Progression funnel per game mode |
| `jigsaw_game_points_total` | `direction`, `source` | Currency flow; granted/spent divergence suggests an exploit |
| `jigsaw_storage_uploads_total` | `kind`, `result` | Cloudinary failures invisible in HTTP status alone |
| `jigsaw_storage_upload_duration_seconds` | `kind` | Slowest operation in the service |
| `jigsaw_build_info` | `version`, `commit`, `go_version`, `env` | Always 1; correlates a metrics change with a deploy |

### Useful queries

```promql
# Error rate (%)
100 * sum(rate(jigsaw_http_requests_total{status=~"5.."}[5m]))
    / sum(rate(jigsaw_http_requests_total[5m]))

# p95 latency, excluding probes
histogram_quantile(0.95, sum by (le) (
  rate(jigsaw_http_request_duration_seconds_bucket{route!~"/metrics|/healthz|/readyz"}[5m])))

# Slowest endpoints
topk(5, histogram_quantile(0.95, sum by (le, route) (
  rate(jigsaw_http_request_duration_seconds_bucket[5m]))))

# Pool saturation (%)
100 * sum(jigsaw_db_connections_acquired) / sum(jigsaw_db_connections_max)

# Failed logins per second
sum(rate(jigsaw_auth_events_total{event="login",result="failure"}[5m]))
```

---

## Dashboard & alerts

Everything under `deploy/` is provisioned from files — dashboards are code, and
UI edits are overwritten on reload (`allowUiUpdates: false`).

```
deploy/
├── prometheus/
│   ├── prometheus.yml   # scrape config
│   └── alerts.yml       # alert rules
└── grafana/
    ├── provisioning/    # datasource + dashboard providers
    └── dashboards/
        └── jigsaw-backend.json
```

Dashboard sections: **Overview** (rate / errors / p95 / in-flight / pool / version),
**Traffic & latency**, **Database**, **Go runtime**, **Product & revenue**.

Alerts: `ApiDown`, `NotReady`, `HighServerErrorRate` (>2%), `HandlerPanics`,
`HighLatencyP95` (>1s), `DatabasePoolExhausted`, `RateLimitSurge`,
`PurchaseVerificationFailing`, `LoginFailureSpike`. Thresholds are starting
points — tune them against a week of real traffic.

Prometheus evaluates the rules and shows firing alerts at
<http://localhost:9090/alerts>. Routing them to Slack/PagerDuty needs an
Alertmanager, which is not part of the local stack.

`TestDeployedQueriesReferenceRealMetrics` (in `internal/metrics`) fails the build
if a dashboard panel or alert rule queries a metric name that no longer exists —
otherwise a rename would silently render as "No data".

---

## Configuration

| Variable | Default | Notes |
|----------|---------|-------|
| `LOG_LEVEL` | `info` | `debug` is verbose: every 4xx gets a line |
| `LOG_FORMAT` | `json` in production, else `text` | |
| `METRICS_ENABLED` | `true` | `false` removes the route entirely (404) |
| `METRICS_PATH` | `/metrics` | |
| `METRICS_TOKEN` | *(empty)* | Required as `Authorization: Bearer <token>` when set |
| `GRAFANA_USER` / `GRAFANA_PASSWORD` | `admin` / `admin` | Local stack only |

### Securing `/metrics` in production

The scrape output reveals route names, error rates and pool sizes. Pick one:

1. **Network isolation** (preferred) — only the scraper can reach the port.
2. **Bearer token** — set `METRICS_TOKEN` and add it to the scrape config:

   ```yaml
   authorization:
     type: Bearer
     credentials: <METRICS_TOKEN>
   ```

Leaving it open on a publicly reachable service logs a warning at startup.

---

## Adding a metric

1. Declare it in `internal/metrics/metrics.go` and add it to the `init()`
   registration list.
2. Keep labels bounded — never a user id, email, path with an id in it, or any
   unbounded string. Each label-value combination is a separate time series.
3. Record it at the decision point in the handler or service.
4. Add a panel and, if it needs action, an alert rule.

## Adding a log line

Use the request-scoped logger so the line carries `request_id` and `user_id`:

```go
logx.FromContext(ctx).Info("dungeon completed",
    slog.String("dungeon_id", id.String()),
    slog.Int("energy_left", energy),
)
```

Never log secrets: tokens, passwords, receipts, or full store credentials.
`config` already reports secret-like values as `set`/`missing` rather than
printing them.
