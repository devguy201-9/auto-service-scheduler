.PHONY: db down run test test-integration lint tidy

db:
	docker compose up -d

down:
	docker compose down -v

run: db
	DATABASE_URL=postgres://scheduler:scheduler@localhost:5432/scheduler?sslmode=disable \
	go run ./cmd/scheduler

test:
	go test ./... -race -count=1

test-integration: db
	DATABASE_URL=postgres://scheduler:scheduler@localhost:5432/scheduler?sslmode=disable \
	go test ./test/... -tags=integration -race -count=1 -v

lint:
	go vet ./...

tidy:
	go mod tidy