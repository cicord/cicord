# Cicord developer tasks.
GO ?= go
VERSION ?= 0.1.0
LDFLAGS := -s -w -X github.com/npjonath/cicord/internal/version.Version=$(VERSION) -X github.com/npjonath/cicord/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

.PHONY: all build ui test test-go test-js vet check clean

all: ui build

## build: the single `cicord` binary (embeds the admin UI, the default portal and the portal kit of `cicord portal init`)
build:
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/cicord ./cmd/cicord

## ui: build the JS workspace; copy the admin UI, the portal and the portal kit (@cicord/* tarballs + template) into the Go embed dirs
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
