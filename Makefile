# Targets the README and CI rely on. run-example is the upstream from issue #3.
BIN_DIR := bin

.PHONY: build test lint run run-example

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -o $(BIN_DIR)/mcp-gateway ./cmd/mcp-gateway
	go build -trimpath -o $(BIN_DIR)/echo-server ./examples/echo-server

test:
	go test -race ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint v2 is required."; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4"; \
		exit 1; \
	}
	golangci-lint run ./...
	golangci-lint fmt --diff ./...

run:
	go run ./cmd/mcp-gateway

run-example:
	go run ./examples/echo-server
