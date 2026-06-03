.PHONY: up down test test-local run-local lint tidy

# One-command run (interactive: server + DB)
up:
	docker compose up --build

# Stop and clean all containers + volumes
down:
	docker compose down -v

# One-command test (the reviewer command)
test:
	docker compose --profile test up --build --abort-on-container-exit --exit-code-from tests

# Run tests against an already-running compose Postgres, without rebuilding container
test-local:
	DATABASE_URL=postgres://scheduler:scheduler@localhost:5432/scheduler?sslmode=disable \
	go test ./internal/... -count=1 && \
	go test ./test/... -tags=integration -count=1 -v

# Run the server locally (no container) for development iteration
run-local:
	docker compose up -d postgres
	DATABASE_URL=postgres://scheduler:scheduler@localhost:5432/scheduler?sslmode=disable \
	go run ./cmd/scheduler

lint:
	go vet ./...

tidy:
	go mod tidy