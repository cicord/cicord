# CODI developer tasks.
GO ?= go
VERSION ?= 0.1.0
LDFLAGS := -s -w -X github.com/npjonath/codi/internal/version.Version=$(VERSION) -X github.com/npjonath/codi/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

.PHONY: all build ui test test-go test-js vet check clean

all: ui build

## build: the single `codi` binary (embeds the admin UI and the default portal)
build:
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/codi ./cmd/codi

## ui: build the JS workspace and copy the admin UI / portal into the Go embed dirs
ui:
	pnpm install --frozen-lockfile
	pnpm -r --filter "./packages/*" build
	./scripts/sync-ui.sh

test: test-go test-js

test-go:
	$(GO) test -race ./...

test-js:
	pnpm -r test

vet:
	$(GO) vet ./...

check: vet test

clean:
	rm -rf bin
