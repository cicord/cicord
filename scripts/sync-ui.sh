#!/usr/bin/env bash
# Copies the built web UIs into internal/webui so that `go build` embeds them:
#   packages/admin/dist              → internal/webui/admin
#   packages/create-codi-portal/dist → internal/webui/portal
# A UI that has not been built is left as is (placeholder index.html).
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

sync_one packages/admin/dist admin
sync_one packages/create-codi-portal/dist portal
