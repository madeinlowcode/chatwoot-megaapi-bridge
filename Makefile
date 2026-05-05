.PHONY: build test lint fmt sqlc migrate run-api run-worker tidy clean

BIN_DIR := bin
MOD := github.com/madeinlowcode/chatwoot-megaapi-bridge

build:
	go build -ldflags="-s -w" -o $(BIN_DIR)/bridge ./cmd/bridge-api
	go build -ldflags="-s -w" -o $(BIN_DIR)/bridge-worker ./cmd/bridge-worker

test:
	go test ./... -race -count=1

test-cover:
	go test ./... -race -coverprofile=coverage.out
	go tool cover -func=coverage.out

lint:
	golangci-lint run

fmt:
	gofmt -s -w .
	go mod tidy

sqlc:
	sqlc generate

migrate:
	go run ./cmd/bridge-api migrate up

run-api:
	go run ./cmd/bridge-api serve

run-worker:
	go run ./cmd/bridge-worker

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR) coverage.out
