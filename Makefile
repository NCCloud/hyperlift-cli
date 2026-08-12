BINARY      := hyperlift
PKG         := github.com/nccloud/hyperlift-cli
CMD         := ./cmd/hyperlift
MOCK_CMD    := ./cmd/mock
BUILD_PKG   := $(PKG)/internal/build

# Default listen address for the mock server (override: make mock MOCK_ADDR=:9000).
MOCK_ADDR   ?= :8080

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X '$(BUILD_PKG).Version=$(VERSION)' \
	-X '$(BUILD_PKG).Commit=$(COMMIT)' \
	-X '$(BUILD_PKG).Date=$(DATE)'


.PHONY: build install lint test refresh-spec mock run-mock fmt tidy clean

build: ## Build the hyperlift binary into ./bin
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

install: ## Install hyperlift into GOBIN
	go install -trimpath -ldflags "$(LDFLAGS)" $(CMD)

lint: ## Run golangci-lint
	golangci-lint run ./...

test: ## Run the test suite (same flags as CI)
	go test -race -cover ./...

refresh-spec: ## Re-pin the contract oracle's OpenAPI document from the published docs
	curl -fsSL https://docs.spaceship.dev/ | python3 scripts/refresh_spec.py

fmt: ## Format the codebase
	golangci-lint fmt


tidy: ## Tidy go.mod / go.sum
	go mod tidy

mock: ## Run the contract-accurate mock API server (foreground)
	go run $(MOCK_CMD) -addr $(MOCK_ADDR)

run-mock: build ## Run the CLI against a running `make mock`, e.g. ARGS='apps list'
	HYPERLIFT_BASE_URL=$${HYPERLIFT_BASE_URL:-http://localhost:8080} \
		HYPERLIFT_API_KEY=$${HYPERLIFT_API_KEY:-demo} \
		HYPERLIFT_API_SECRET=$${HYPERLIFT_API_SECRET:-demo} \
		./bin/$(BINARY) $(ARGS)

clean: ## Remove build artifacts
	rm -rf bin
