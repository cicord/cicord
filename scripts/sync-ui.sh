#!/usr/bin/env bash
# Copies the built web UIs and the portal kit into internal/webui so that `go build`
# embeds them:
#   packages/admin/dist              → internal/webui/admin
#   packages/create-cicord-portal/dist → internal/webui/portal
#   pnpm pack of @cicord/sdk, @cicord/client, @cicord/react, @cicord/ui → internal/webui/kit/packages/*.tgz
#     (@cicord/sdk for the unit tests of `cicord plugin new`, the others for `cicord portal init`)
#   the reference portal template    → internal/webui/kit/template (cicord portal init)
# A UI that has not been built is left as is (placeholder index.html); a missing kit only
# disables `cicord portal init` (it explains how to build it).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

sync_one() {
  local src="$root/$1" dst="$root/internal/webui/$2"
  if [[ ! -f "$src/index.html" ]]; then
    echo "skip $2: $1/index.html not found (build it first)" >&2
    return 0
  fi
  rm -rf "$dst"
  mkdir -p "$dst"
  cp -R "$src/." "$dst/"
  echo "synced $1 → internal/webui/$2"
}

sync_kit() {
  local kit="$root/internal/webui/kit"
  for p in sdk client react ui; do
    if [[ ! -f "$root/packages/$p/dist/index.mjs" ]]; then
      echo "skip portal kit: packages/$p is not built (pnpm -r build)" >&2
      return 0
    fi
  done
  if ! command -v pnpm >/dev/null || ! command -v node >/dev/null; then
    echo "skip portal kit: pnpm and node are required" >&2
    return 0
  fi
  rm -rf "$kit/packages" "$kit/template"
  mkdir -p "$kit/packages"
  for p in sdk client react ui; do
    # pnpm pack rewrites workspace:/catalog: specifiers to real versions.
    (cd "$root/packages/$p" && pnpm pack --pack-destination "$kit/packages" >/dev/null)
  done
  node "$root/packages/create-cicord-portal/bin/create-cicord-portal.mjs" "$kit/template" --force >/dev/null
  echo "synced portal kit → internal/webui/kit ($(cd "$kit/packages" && ls *.tgz | tr '\n' ' '))"
}

sync_one packages/admin/dist admin
sync_one packages/create-cicord-portal/dist portal
sync_kit
