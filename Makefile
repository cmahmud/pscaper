.PHONY: build test fmt
build:
	go build -trimpath -o bin/pscaper ./cmd/pscaper
test:
	go test ./...
fmt:
	gofmt -w ./cmd ./internal
