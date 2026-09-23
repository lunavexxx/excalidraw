DATABASE_URL ?= postgres://postgres:postgres@localhost:5432/excalidraw

.PHONY: run build test vet migrate-up migrate-down

run:
	go run ./cmd/api

build:
	CGO_ENABLED=0 go build -o bin/api ./cmd/api

test:
	go test ./...

vet:
	go vet ./...

migrate-up:
	migrate -path internal/migrate/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path internal/migrate/migrations -database "$(DATABASE_URL)" down 1
