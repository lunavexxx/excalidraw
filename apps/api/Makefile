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

# 需要本机安装 golang-migrate CLI(brew install golang-migrate);
# 不装 CLI 也可以直接用 psql 依次执行 migrations/*.up.sql(见 deploy/README.md)。
migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1
