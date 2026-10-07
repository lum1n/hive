MODULE := github.com/lum1n/hive
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

.PHONY: build install test vet tidy clean

build:
	go build -ldflags "$(LDFLAGS)" -o hive ./cmd/hive

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/hive

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -f hive
