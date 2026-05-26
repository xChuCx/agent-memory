# Convenience targets for local development.
#
# Requires GNU make. On Windows: `choco install make`, or run inside git-bash,
# or invoke the underlying go commands directly (see README).

GOEXE := $(shell go env GOEXE)
BIN   := agent-memory$(GOEXE)

.PHONY: help build test test-race lint tidy clean

help: ## show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## compile the agent-memory binary into the project root
	go build -o $(BIN) ./cmd/agent-memory

test: ## run all tests
	go test ./...

test-race: ## run tests with the race detector (linux/macos most reliable)
	go test -race ./...

lint: ## run golangci-lint (requires golangci-lint installed)
	golangci-lint run ./...

tidy: ## refresh go.mod and go.sum
	go mod tidy

clean: ## remove the built binary
	go clean
	-rm -f $(BIN)
