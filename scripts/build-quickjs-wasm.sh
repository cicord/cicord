#!/usr/bin/env bash
# Builds internal/plugin/qjs/quickjs.wasm: QuickJS-ng + Cicord's shim (csrc/cicord.c) for
# wasm32-wasip1 (reactor), with a pinned wasi-sdk image. The output is reproducible: running
# this script twice gives the same bytes, and the sha256 is checked by a Go test
# (internal/plugin/qjs, TestWasmChecksum) against quickjs.wasm.sha256.
#
#   scripts/build-quickjs-wasm.sh          # build and update quickjs.wasm(.sha256)
#   scripts/build-quickjs-wasm.sh --check  # build in a temp dir and compare with the committed file
#
# Requires Docker. Bumping QuickJS-ng: change QJS_VERSION and QJS_SHA256, run the script,
# run `go test ./internal/plugin/...`, commit the three files.
set -euo pipefail

QJS_VERSION=0.18.0
QJS_SHA256=28bcd89aa3683fd2a946537e5ce3f11cc46b5cbf3383c7561aaef3df5ff9f1dd
# ghcr.io/webassembly/wasi-sdk:wasi-sdk-34 (manifest list), built for linux/amd64.
WASI_SDK_IMAGE=ghcr.io/webassembly/wasi-sdk@sha256:52595085a0ffbaf574d8d678a9aa81305f0b3af81c639291f15bc7713e42848e
PLATFORM=linux/amd64

# Linear memory layout: the shadow stack comes first (an overflow traps instead of
# corrupting the heap); internal/plugin/qjs sets the JS stack limit below it.
STACK_SIZE=1048576

root=$(cd "$(dirname "$0")/.." && pwd)
pkg="$root/internal/plugin/qjs"
check=false
[[ "${1:-}" == "--check" ]] && check=true

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

curl -fsSL -o "$work/qjs.tar.gz" "https://github.com/quickjs-ng/quickjs/archive/refs/tags/v${QJS_VERSION}.tar.gz"
echo "${QJS_SHA256}  $work/qjs.tar.gz" | shasum -a 256 -c - >/dev/null
mkdir -p "$work/src"
tar xzf "$work/qjs.tar.gz" -C "$work/src" --strip-components=1
cp "$pkg/csrc/cicord.c" "$work/src/cicord.c"

docker run --rm --platform "$PLATFORM" --network none \
  -v "$work/src:/src:ro" -v "$work:/out" -w /src "$WASI_SDK_IMAGE" \
  /opt/wasi-sdk/bin/clang --target=wasm32-wasip1 -mexec-model=reactor \
  -O2 -std=gnu11 -ffile-prefix-map=/src=quickjs -fno-ident \
  -finstrument-function-entry-bare \
  -D_GNU_SOURCE -DCONFIG_VERSION="\"${QJS_VERSION}\"" \
  -Wno-unused-parameter -Wno-sign-compare -Wno-implicit-fallthrough \
  -Wl,--stack-first -Wl,-z,stack-size=${STACK_SIZE} \
  -Wl,--strip-all -Wl,--no-entry \
  -o /out/quickjs.wasm \
  cicord.c quickjs.c libregexp.c libunicode.c dtoa.c

sum=$(shasum -a 256 "$work/quickjs.wasm" | cut -d' ' -f1)
if $check; then
  want=$(cut -d' ' -f1 "$pkg/quickjs.wasm.sha256")
  if [[ "$sum" != "$want" ]]; then
    echo "quickjs.wasm is not reproducible: built $sum, committed $want" >&2
    exit 1
  fi
  echo "quickjs.wasm reproduced ($sum)"
  exit 0
fi
cp "$work/quickjs.wasm" "$pkg/quickjs.wasm"
echo "$sum  quickjs.wasm" >"$pkg/quickjs.wasm.sha256"
echo "built quickjs.wasm ($(wc -c <"$pkg/quickjs.wasm" | tr -d ' ') bytes, sha256 $sum)"
