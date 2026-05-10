.PHONY: build test lint fmt run docker-build clean

BINARY     := server
IMAGE_NAME := ai-microservice
REGISTRY   := registry.tst.carreira.cloud/tst
GO_VERSION := 1.24

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/$(BINARY) ./cmd/server

test:
	go test -v -count=1 -race ./internal/... ./tests/...

test-unit:
	go test -v -count=1 -race -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out | tail -5

test-acceptance:
	go test -v -count=1 ./tests/acceptance/...

lint:
	go vet ./...
	@which golangci-lint > /dev/null && golangci-lint run --timeout=5m || echo "[warn] golangci-lint not found"

fmt:
	go fmt ./...
	@which goimports > /dev/null && goimports -w . || true

run:
	GATEWAY_SECRET=dev-secret \
	GITHUB_COPILOT_OAUTH_TOKEN=dev-token \
	LOG_LEVEL=debug \
	go run ./cmd/server

docker-build:
	docker build -t $(REGISTRY)/$(IMAGE_NAME):dev .

clean:
	rm -rf bin/ coverage.out
