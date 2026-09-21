.PHONY: build test install

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/ttopias/glci/internal/version.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o glci ./cmd/glci

test:
	go test ./... -count=1 -timeout 15m

install: build
	install -m 755 glci "$(or $(GOBIN),$(HOME)/go/bin)/glci"
