.PHONY: build test run lint migrate

build:
	go build -o bin/agent-service ./cmd/agent-service

test:
	go test ./...

run:
	go run ./cmd/agent-service

lint:
	go vet ./...

migrate:
	psql "$(DATABASE_URL)" -f migrations/001_init.sql
