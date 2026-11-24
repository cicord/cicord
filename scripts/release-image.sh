#!/usr/bin/env bash
# Builds the multi-arch Cicord container image (linux/amd64 and linux/arm64) from the root
# Dockerfile with buildx, with an SBOM and max-mode provenance attached, and pushes it when
# PUSH=true. Used by .github/workflows/release.yml (https://cicord.dev/docs/project/releasing/).
#
#   VERSION=0.1.0 scripts/release-image.sh             # build only (result stays in the builder)
#   VERSION=0.1.0 PUSH=true scripts/release-image.sh   # build and push (docker login ghcr.io first)
#
# Needs a builder that can export multi-platform images with attestations, e.g.
#   docker buildx create --use --driver docker-container
# Environment: VERSION (required, without the leading "v"), IMAGE (ghcr.io/cicord/cicord),
# PLATFORMS (linux/amd64,linux/arm64), PUSH (false).
#
# Tags: <version> and sha-<short commit>; a version without a pre-release suffix also gets
# <major>.<minor> and latest. Prints the image digest, and writes digest=<sha256:…> to
# $GITHUB_OUTPUT in GitHub Actions.
set -euo pipefail

: "${VERSION:?VERSION is required (e.g. VERSION=0.1.0)}"
IMAGE="${IMAGE:-ghcr.io/cicord/cicord}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
PUSH="${PUSH:-false}"

if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "release-image: VERSION=$VERSION is not a semantic version (x.y.z or x.y.z-suffix)" >&2
  exit 1
fi

cd "$(dirname "${BASH_SOURCE[0]}")/.."
commit="$(git rev-parse --short HEAD)"
revision="$(git rev-parse HEAD)"
# Reproducible image timestamps: the commit time.
epoch="$(git log -1 --format=%ct)"

tags=("$IMAGE:$VERSION" "$IMAGE:sha-$commit")
if [[ "$VERSION" != *-* ]]; then
  tags+=("$IMAGE:${VERSION%.*}" "$IMAGE:latest")
fi

args=()
for t in "${tags[@]}"; do args+=(--tag "$t"); done
# The Dockerfile LABELs end up in each platform's image config; GHCR reads the index
# annotations of a multi-arch image (description, source, license on the package page).
annotations=(
  "org.opencontainers.image.title=cicord"
  "org.opencontainers.image.description=GitOps for Discord: declarative servers and bots, TypeScript plugins"
  "org.opencontainers.image.source=https://github.com/cicord/cicord"
  "org.opencontainers.image.url=https://cicord.dev"
  "org.opencontainers.image.documentation=https://cicord.dev/docs/"
  "org.opencontainers.image.licenses=Apache-2.0"
  "org.opencontainers.image.version=$VERSION"
  "org.opencontainers.image.revision=$revision"
)
for a in "${annotations[@]}"; do args+=(--annotation "index:$a"); done

meta="$(mktemp)"
trap 'rm -f "$meta"' EXIT

echo "release-image: building ${tags[*]} for $PLATFORMS (push=$PUSH)" >&2
docker buildx build \
  --platform "$PLATFORMS" \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$commit" \
  --build-arg SOURCE_DATE_EPOCH="$epoch" \
  --sbom=true \
  --provenance=mode=max \
  --output "type=image,push=$PUSH" \
  --metadata-file "$meta" \
  "${args[@]}" \
  .

digest="$(sed -n 's/.*"containerimage\.digest": *"\(sha256:[0-9a-f]*\)".*/\1/p' "$meta" | head -n 1)"
if [[ -z "$digest" ]]; then
  echo "release-image: no containerimage.digest in the build metadata" >&2
  cat "$meta" >&2
  exit 1
fi
echo "$digest"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  echo "digest=$digest" >>"$GITHUB_OUTPUT"
fi
