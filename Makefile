# ==============================================================================
# DROP-RELAY — SYSTEMS MAKEFILE
# ==============================================================================

.PHONY: all build test test-race test-e2e lint clean dkim verify-dns scan scan-fs scan-image docker-build docker-up docker-down

BINARY=bin/relay
VERSION ?= 0.1.2

all: test build

build:
	@echo "==> Building relay binary..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION)" -o $(BINARY) ./cmd/relay

test:
	@echo "==> Executing unit test suite..."
	go test -v ./internal/...

test-race:
	@echo "==> Executing unit test suite with race detector..."
	go test -v -race ./internal/...

test-e2e: build
	@echo "==> Executing end-to-end integration tests..."
	@chmod +x scripts/*.sh
	@./scripts/test-e2e.sh

lint:
	@echo "==> Running static analysis..."
	go vet ./...

clean:
	@echo "==> Cleaning build artifacts..."
	rm -rf bin/ keys/

dkim: build
	@mkdir -p keys
	@$(BINARY) generate-dkim -out keys/relay.private

verify-dns:
	@chmod +x scripts/verify-dns.sh
	@./scripts/verify-dns.sh

scan: docker-build
	@chmod +x scripts/security-scan.sh
	@./scripts/security-scan.sh all

scan-fs:
	@chmod +x scripts/security-scan.sh
	@./scripts/security-scan.sh fs

scan-image:
	@chmod +x scripts/security-scan.sh
	@./scripts/security-scan.sh image

docker-build:
	@echo "==> Building container image..."
	docker build -f docker/Dockerfile -t ghcr.io/dropin-labs/drop-relay:latest .

docker-up:
	docker compose -f docker/docker-compose.yml up -d

docker-down:
	docker compose -f docker/docker-compose.yml down
