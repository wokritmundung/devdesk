GO ?= go

.PHONY: build test lint fmt run-agent
build:
	$(GO) build ./...

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run || echo "golangci-lint not installed; go vet only"

fmt:
	$(GO)fmt -w ./cmd ./pkg 2>/dev/null || gofmt -w ./cmd ./pkg

run-agent:
	$(GO) run ./cmd/node-agent -data-dir /tmp/rewarm-snapshots -engine-url http://127.0.0.1:8000
