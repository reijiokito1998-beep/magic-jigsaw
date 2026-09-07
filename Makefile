.PHONY: run build tidy test fmt vet swagger docker-up docker-down obs-up obs-down obs-logs metrics

# Version stamped into the binary, /version and the jigsaw_build_info metric.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -X github.com/reijiokito/jigsaw-backend/internal/buildinfo.Version=$(VERSION) \
           -X github.com/reijiokito/jigsaw-backend/internal/buildinfo.Commit=$(COMMIT)

# Regenerate OpenAPI/Swagger docs from handler annotations.
# Requires: go install github.com/swaggo/swag/cmd/swag@latest
swagger:
	swag init -g cmd/server/main.go -o docs --parseInternal

run:
	go run ./cmd/server

build:
	go build -ldflags "$(LDFLAGS)" -o bin/server ./cmd/server

tidy:
	go mod tidy

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

docker-up:
	docker compose up --build

docker-down:
	docker compose down

# --- Observability -----------------------------------------------------------

# Bring up Prometheus (:9090) and Grafana (:3000, dashboard "Jigsaw").
# The API itself is expected to run on the host via `make run`.
obs-up:
	docker compose up -d prometheus grafana
	@echo "Prometheus  http://localhost:9090/targets"
	@echo "Grafana     http://localhost:3000  (admin/admin)"

obs-down:
	docker compose stop prometheus grafana

obs-logs:
	docker compose logs -f prometheus grafana

# Print the current scrape output — the quickest check that instrumentation works.
metrics:
	@curl -sf $${METRICS_TOKEN:+-H "Authorization: Bearer $$METRICS_TOKEN"} \
		http://localhost:$${PORT:-8080}/metrics | grep '^jigsaw_' | grep -v '^#'
