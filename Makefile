GO := $(shell if [ -x .toolchain/go/bin/go ]; then echo .toolchain/go/bin/go; else echo go; fi)
export GOCACHE := $(CURDIR)/.build-cache/go-build
export GOMODCACHE := $(CURDIR)/.build-cache/gomod
export GOPATH := $(CURDIR)/.build-cache/gopath
export GOENV := off
export GOTOOLCHAIN := local
.PHONY: build test fmt vet
build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o bin/pscaper ./cmd/pscaper
test:
	$(GO) test ./...
fmt:
	$(GO) fmt ./...
vet:
	$(GO) vet ./...
