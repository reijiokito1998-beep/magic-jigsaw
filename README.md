# Jigsaw Backend

Golang backend for the Jigsaw Puzzle app. Provides Google authentication, a
per-user image collection, admin-managed **daily challenges** (images stored on
Cloudinary), and per-day **reputation** ("Danh vọng") tracking.

## Stack

- Go 1.25 (standard-library `net/http` router with method+pattern routing)
- PostgreSQL via `pgx/v5`
- Email/password auth (bcrypt) with HMAC JWT sessions (`golang-jwt/v5`)
- Image hosting on Cloudinary (`cloudinary-go/v2`)
- OpenAPI/Swagger docs (`swaggo/swag` + `http-swagger`) at `/swagger/index.html`

## Layout

```
backend/
├── cmd/server/main.go            # entrypoint (config, db, migrate, http)
├── internal/
│   ├── config/                   # env-based config
│   ├── database/                 # pgx pool + embedded SQL migrations
│   │   └── migrations/0001_init.sql
│   ├── models/                   # domain structs
│   ├── repository/               # Postgres data access (Store)
│   ├── auth/                     # Google verify + JWT
│   ├── storage/                  # Cloudinary uploader
│   ├── httpx/                    # JSON/error helpers + middleware
│   └── api/                      # handlers + router
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

## Running locally

1. Copy `.env.example` to `.env` and fill in values.
2. Start Postgres + API:

```bash
docker compose up --build          # runs db + api
# or run the API against your own Postgres:
make run
```

Migrations run automatically on startup.

## Configuration (env)

| Var | Required | Notes |
|-----|----------|-------|
| `PORT` | no (8080) | HTTP port |
| `DATABASE_URL` | yes | Postgres DSN |
| `JWT_SECRET` | yes | long random string |
| `JWT_TTL` | no (720h) | session lifetime |
| `ADMIN_EMAILS` | no | comma-separated emails granted admin role on register/login |
| `CLOUDINARY_URL` | yes* | or `CLOUDINARY_CLOUD_NAME`/`API_KEY`/`API_SECRET` |
| `CLOUDINARY_FOLDER` | no (jigsaw) | upload folder |
| `CORS_ORIGINS` | no (*) | comma-separated allowed origins |
| `LOG_LEVEL` | no (info) | `debug\|info\|warn\|error` |
| `LOG_FORMAT` | no | `json` in production, `text` otherwise |
| `METRICS_ENABLED` | no (true) | exposes the Prometheus scrape endpoint |
| `METRICS_TOKEN` | no | when set, `/metrics` requires this bearer token |

## API docs (Swagger)

Interactive docs are served by the running app:

- Swagger UI: `http://localhost:8080/swagger/index.html`
- OpenAPI JSON: `http://localhost:8080/swagger/doc.json`

Use the **Authorize** button in Swagger UI and enter `Bearer <token>` (the token
from `POST /api/v1/auth/google`) to call protected endpoints.

The spec is generated from handler annotations into `docs/`. Regenerate after
changing annotations:

```bash
go install github.com/swaggo/swag/cmd/swag@latest   # once
make swagger
```

## API

All responses are JSON. Errors use `{"error":{"code","message"}}`.
Authenticated routes require `Authorization: Bearer <token>`.

### Auth
- `POST /api/v1/auth/register` — body `{"email","password","confirmPassword"}` → `{token, user}`
- `POST /api/v1/auth/login` — body `{"email","password"}` → `{token, user}`

### Me
- `GET /api/v1/me` → user profile
- `GET /api/v1/me/notifications` → `{hasNew, challenge?}` (today's challenge if unplayed)
- `GET /api/v1/me/results` → `{results:[...]}` (reputation history — for the future Danh vọng screen)

### Collection
- `GET /api/v1/collection` → `{items:[{id, addedAt, image}]}`
- `POST /api/v1/collection/upload` — multipart `file`, `title` → uploaded image (added to collection)
- `POST /api/v1/collection` — body `{"imageId":"..."}` → add existing image (e.g. a completed challenge)
- `DELETE /api/v1/collection/{imageId}` → remove

### Challenges
- `GET /api/v1/challenges/today` → today's challenge + user result (`404` if none)
- `GET /api/v1/challenges` → recent challenges
- `POST /api/v1/challenges/{id}/start` → mark in_progress (idempotent; never resets a finished result)
- `POST /api/v1/challenges/{id}/complete` — body `{"elapsedSeconds":n}` → mark completed
- `POST /api/v1/challenges/{id}/fail` → mark failed (quit/lose → reputation = Failed)

### Admin (role=admin)
- `POST /api/v1/admin/challenges` — multipart `file`, `date` (YYYY-MM-DD, default today), `playSeconds`, `title`
- `GET /api/v1/admin/challenges` → recent challenges

### Ops
- `GET /healthz` → liveness (checks nothing by design)
- `GET /readyz` → readiness (pings the database; 503 when it is unreachable)
- `GET /version` → build version, commit, uptime
- `GET /metrics` → Prometheus metrics (bearer token when `METRICS_TOKEN` is set)

## Observability

Structured JSON logs with per-request correlation ids, Prometheus metrics, and a
provisioned Grafana dashboard:

```bash
make run       # API on :8080
make obs-up    # Prometheus :9090 + Grafana :3000 (admin/admin)
make metrics   # print the current scrape output
```

See [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md) for the metric catalogue,
alert rules and log-querying recipes.

## Reputation ("Danh vọng") rules

Per user, per daily challenge there is exactly one result:

- `start` creates `in_progress`.
- `complete` (only from `in_progress`) → `completed`.
- `fail` (only from `in_progress`) → `failed`. The client calls this when the
  user quits mid-challenge or loses (time out).
- Once `completed`/`failed`, the result is immutable for that day.

Đã bổ sung endpoint POST /api/v1/me/points/purchase mà Flutter client (points_api.dart) đã sẵn sàng gọi, verify receipt server-to-server trước khi cộng điểm — không tin productId từ client.

Files mới:

internal/database/migrations/0016_purchases.sql — bảng purchases ghi log mọi giao dịch, UNIQUE (platform, transaction_id) để chống double-credit
internal/purchases/{purchases,apple,google,catalog}.go — verifier cho App Store (verifyReceipt) và Google Play (Play Developer API + service-account JWT), catalog unlock_points_50 → 50 điểm
internal/repository/purchases.go — CreditPurchase cộng điểm idempotent trong 1 transaction
Files sửa: config.go (4 env var mới, không bắt buộc), points.go (handler mới), router.go, server.go, main.go, go.mod

Env vars cần set khi lên production:

Var	Ghi chú
APPLE_SHARED_SECRET	Shared secret của App Store Connect
APPLE_BUNDLE_ID	Tùy chọn — chặn receipt từ app khác
GOOGLE_PACKAGE_NAME	applicationId Android
GOOGLE_SERVICE_ACCOUNT_JSON	JSON key hoặc đường dẫn file, cần quyền "View financial data" trên Play Console
Thiếu var nào thì nền tảng đó chỉ log WARNING và trả 503 khi có purchase, không crash server.

Đã tự chạy lại go build ./... và go vet ./... để xác nhận độc lập — pass. Agent cũng đã chạy go test ./... -race (26 test case cho verifiers) và golangci-lint — pass.

Lưu ý còn lại:

Migration 0016_purchases.sql chưa apply lên DB thật (máy này không có Postgres để test) — cần chạy migration theo cách hiện tại của bạn đang dùng (lưu ý database.Migrate trong main.go đang bị comment sẵn từ trước, không phải do task này).
Chỉ có unlock_points_50 trong catalog — sản phẩm mới sau này phải thêm vào internal/purchases/catalog.go.
Dùng verifyReceipt (legacy) cho Apple thay vì App Store Server API mới — vì chỉ cần shared secret, không cần signing key riêng.