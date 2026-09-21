.PHONY: build test install

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/ttopias/glci/internal/version.Version=$(VERSION)
ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BINDIR := $(or $(GOBIN),$(shell go env GOBIN),$(shell go env GOPATH)/bin)

build:
	go build -ldflags "$(LDFLAGS)" -o glci ./cmd/glci

test:
	go test ./... -count=1 -timeout 15m

install: build
	mkdir -p "$(BINDIR)"
	install -m 755 glci "$(BINDIR)/glci"
	chmod +x "$(ROOT)scripts/ensure-path.sh"
	"$(ROOT)scripts/ensure-path.sh" "$(BINDIR)"
