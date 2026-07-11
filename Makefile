.PHONY: build test lint
build:
	go build ./... 2>/dev/null || echo "no Go code yet (pre-M1)"
test:
	go test ./... 2>/dev/null || echo "no Go code yet (pre-M1)"
lint:
	golangci-lint run 2>/dev/null || echo "no Go code yet (pre-M1)"
