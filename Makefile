BINARY := syscheckr
PKG := ./cmd/syscheckr
VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/Krakaw/syscheckr/internal/version.Version=$(VERSION) \
           -X github.com/Krakaw/syscheckr/internal/version.Commit=$(COMMIT) \
           -X github.com/Krakaw/syscheckr/internal/version.Date=$(DATE)

.PHONY: build test vet tidy run clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

run: build
	./$(BINARY) run -c config.example.yaml

clean:
	rm -f $(BINARY)
