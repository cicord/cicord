# syntax=docker/dockerfile:1
#
# Cicord runtime image: one static `cicord` binary with the admin UI and the default portal
# embedded, on distroless/static (no shell, non-root uid 65532).
#
#   docker build -t cicord --build-arg VERSION=0.1.0 --build-arg COMMIT="$(git rev-parse --short HEAD)" .
#   docker run -p 7420:7420 -v cicord-data:/data -v "$PWD:/repo:ro" --env-file .env cicord
#
# Stages mirror `make all`: `ui` = `make ui` (pnpm build + scripts/sync-ui.sh: admin UI,
# portal and portal kit), `build` = `make build` (CGO_ENABLED=0). Both run on the build
# platform: the UIs are platform independent and Go cross-compiles to the target platform,
# so a multi-arch build (`--platform linux/amd64,linux/arm64`) needs no emulation. The
# release workflow (.github/workflows/release.yml) publishes ghcr.io/cicord/cicord this way.
# See https://cicord.dev/docs/self-hosting/deploy/.

ARG NODE_VERSION=26
ARG GO_VERSION=1.27

# ───────────────────────────────────────────────────────────── web UIs
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-trixie-slim AS ui
WORKDIR /src
# Vite+ (vp) refuses to start without system CA certificates.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# pnpm version comes from package.json "packageManager" (Node 25+ no longer ships corepack).
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN npm install -g "$(node -p 'require("./package.json").packageManager')"
COPY vite.config.ts .node-version ./
COPY packages ./packages
# @cicord/sdk copies its normative types from spec/sdk/cicord-sdk.d.ts; the admin UI's mock
# runtime imports the example plugins' config schemas.
COPY spec ./spec
COPY examples ./examples
RUN --mount=type=cache,id=cicord-pnpm-store,target=/pnpm-store \
    pnpm install --frozen-lockfile --store-dir /pnpm-store
RUN pnpm -r --filter "./packages/*" build
# Admin UI, default portal and portal kit (pnpm pack of @cicord/*, needs node) into the
# Go embed dirs.
COPY scripts/sync-ui.sh ./scripts/
COPY internal/webui ./internal/webui
RUN ./scripts/sync-ui.sh \
 && ls internal/webui/kit/packages/*.tgz internal/webui/kit/template/package.json >/dev/null

# ───────────────────────────────────────────────────────────── cicord binary
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie AS build
WORKDIR /src
# The official image pins GOTOOLCHAIN=local; allow the exact toolchain of go.mod.
ENV GOTOOLCHAIN=auto CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,id=cicord-gomod,target=/go/pkg/mod go mod download
COPY . .
COPY --from=ui /src/internal/webui ./internal/webui
ARG TARGETOS TARGETARCH
ARG VERSION=0.1.7
ARG COMMIT=dev
RUN --mount=type=cache,id=cicord-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=cicord-gobuild,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -X github.com/cicord/cicord/internal/version.Version=${VERSION} -X github.com/cicord/cicord/internal/version.Commit=${COMMIT}" \
      -o /out/cicord ./cmd/cicord \
 && mkdir -p /out/data /out/repo && chmod 0700 /out/data /out/repo

# ───────────────────────────────────────────────────────────── runtime
FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=0.1.7
ARG COMMIT=dev
LABEL org.opencontainers.image.title="cicord" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.description="GitOps for Discord: declarative servers and bots, TypeScript plugins" \
      org.opencontainers.image.source="https://github.com/cicord/cicord" \
      org.opencontainers.image.url="https://cicord.dev" \
      org.opencontainers.image.documentation="https://cicord.dev/docs/" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/cicord /usr/local/bin/cicord
# Empty, nonroot-owned directories: a named volume mounted on /data inherits the ownership.
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/repo /repo
# Plugin download cache on the volume (default would be $HOME/.cache, lost on restart).
ENV CICORD_CACHE_DIR=/data/cache
USER 65532:65532
WORKDIR /repo
VOLUME ["/data"]
EXPOSE 7420
# GET http://127.0.0.1:7420/healthz (answered in setup mode too); with another --listen
# port, override it (`cicord healthcheck --url …`).
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["/usr/local/bin/cicord", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/cicord"]
# /repo holds at least cicord.yaml (spec.source tells the runtime what to follow); with no
# spec.source it is the watched local repository.
CMD ["serve", "/repo", "--data-dir", "/data", "--listen", ":7420"]
