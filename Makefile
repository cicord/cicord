# Cicord developer tasks.
GO ?= go
VERSION ?= 0.1.6
LDFLAGS := -s -w -X github.com/cicord/cicord/internal/version.Version=$(VERSION) -X github.com/cicord/cicord/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

.PHONY: all build ui test test-go test-js vet lint check clean hooks release

all: ui build

## build: the single `cicord` binary (embeds the admin UI, the default portal and the portal kit of `cicord portal init`)
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/cicord ./cmd/cicord

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

## lint: static analysis run by CI (golangci-lint, govulncheck, oxlint, knip, jscpd, semgrep, gitleaks)
lint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...
	pnpm run check
	pnpm exec knip
	npx -y jscpd@5.4.3 --config .jscpd.json
	semgrep scan --metrics=off --error --config p/golang --config p/react --config p/typescript --config p/javascript --config p/security-audit
	gitleaks git --redact --no-banner .

check: vet test

clean:
	rm -rf bin

## hooks: use the repository git hooks (.githooks: gitleaks on staged changes before each commit)
hooks:
	git config core.hooksPath .githooks

## release: prepare the release commit of VERSION (versions + CHANGELOG), e.g. make release VERSION=0.1.6 (never pushes nor tags)
release:
	scripts/release.sh $(VERSION)
