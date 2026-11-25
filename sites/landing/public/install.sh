#!/bin/sh
# Installs the cicord binary from a GitHub release of cicord/cicord:
#
#   curl -fsSL https://cicord.dev/install.sh | sh
#
# Linux and macOS, amd64 and arm64. It downloads the release archive and checksums.txt,
# checks the SHA-256 of the archive, verifies its build provenance attestation when the
# GitHub CLI (gh) is installed and logged in, then installs `cicord`.
#
# Environment:
#   CICORD_VERSION       version to install (0.1.9 or v0.1.9); default: the latest release
#   CICORD_INSTALL_DIR   where to put cicord; default: /usr/local/bin when it is writable or
#                        sudo is available, else ~/.local/bin
#   CICORD_RELEASES_URL  releases base URL (a mirror, or tests);
#                        default: https://github.com/cicord/cicord/releases
#
# Docs: https://cicord.dev/docs/getting-started/installation/
#
# Everything is inside main(), called on the last line: a truncated download runs nothing.

set -eu

REPO=cicord/cicord
DOCS=https://cicord.dev/docs/getting-started/installation/

say() { printf 'cicord-install: %s\n' "$*" >&2; }
fail() {
  printf 'cicord-install: error: %s\n' "$*" >&2
  exit 1
}
has() { command -v "$1" >/dev/null 2>&1; }

# fetch URL FILE: downloads URL into FILE (fails on HTTP errors).
fetch() {
  if has curl; then
    curl -fsSL --proto "$proto" --proto-redir "$proto" --retry 3 -o "$2" "$1"
  else
    wget -q -O "$2" "$1"
  fi
}

# latest_version BASE: the version of BASE/latest (the latest release, never a
# pre-release), from the tag the redirect lands on (.../releases/tag/v0.1.9).
latest_version() {
  if has curl; then
    url="$(curl -fsSL --proto "$proto" --proto-redir "$proto" -o /dev/null -w '%{url_effective}' "$1/latest")" || return 1
  else
    url="$(wget -q -S -O /dev/null "$1/latest" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r')" || return 1
  fi
  tag="${url##*/}"
  case "$tag" in
    v[0-9]*) printf '%s\n' "${tag#v}" ;;
    *) return 1 ;;
  esac
}

# sha256 FILE: the SHA-256 of FILE, in hex.
sha256() {
  if has sha256sum; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif has shasum; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  elif has openssl; then
    openssl dgst -sha256 -r "$1" | cut -d ' ' -f 1
  else
    fail "no sha256sum, shasum or openssl to verify the download"
  fi
}

detect_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      fail "Windows: download cicord_<version>_windows_amd64.zip from https://github.com/$REPO/releases (see $DOCS)"
      ;;
    *) fail "unsupported operating system $(uname -s): cicord is released for Linux and macOS (see $DOCS)" ;;
  esac
}

detect_arch() {
  m="$(uname -m)"
  case "$m" in
    x86_64 | amd64)
      # An x86_64 shell under Rosetta on Apple silicon: install the native binary.
      if [ "$1" = darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
        echo arm64
      else
        echo amd64
      fi
      ;;
    arm64 | aarch64) echo arm64 ;;
    *) fail "unsupported architecture $m: cicord is released for amd64 and arm64 (see $DOCS)" ;;
  esac
}

# verify_attestation FILE: the build provenance check, with gh when it can.
verify_attestation() {
  how="download $(basename "$1") from the release, then gh attestation verify $(basename "$1") --repo $REPO"
  if ! has gh; then
    say "build provenance not verified: the GitHub CLI (gh) is not installed. To check it: $how"
    return 0
  fi
  if ! gh attestation verify --help >/dev/null 2>&1; then
    say "build provenance not verified: this gh has no 'attestation' command (update gh). To check it: $how"
    return 0
  fi
  if ! gh auth status --hostname github.com >/dev/null 2>&1; then
    say "build provenance not verified: gh is not logged in (gh auth login --hostname github.com). To check it: $how"
    return 0
  fi
  say "verifying the build provenance with gh"
  gh attestation verify "$1" --repo "$REPO" \
    --signer-workflow "$REPO/.github/workflows/release.yml" >/dev/null ||
    fail "the build provenance attestation of $(basename "$1") does not verify: not installing it"
  say "build provenance verified (built by $REPO's release workflow)"
}

# install_dir: CICORD_INSTALL_DIR, or /usr/local/bin (writable, or with sudo), or
# ~/.local/bin. Sets $dir and $sudo.
pick_install_dir() {
  sudo=""
  if [ -n "${CICORD_INSTALL_DIR:-}" ]; then
    dir="$CICORD_INSTALL_DIR"
    if mkdir -p "$dir" 2>/dev/null && [ -w "$dir" ]; then
      return 0
    fi
    has sudo || fail "$dir is not writable and sudo is not available"
    sudo=sudo
    return 0
  fi
  dir=/usr/local/bin
  if { [ -d "$dir" ] && [ -w "$dir" ]; } || { [ ! -e "$dir" ] && mkdir -p "$dir" 2>/dev/null; }; then
    return 0
  fi
  if has sudo; then
    sudo=sudo
    return 0
  fi
  [ -n "${HOME:-}" ] || fail "no HOME to install into ~/.local/bin: set CICORD_INSTALL_DIR"
  dir="$HOME/.local/bin"
  mkdir -p "$dir" || fail "cannot create $dir"
}

main() {
  base="${CICORD_RELEASES_URL:-https://github.com/$REPO/releases}"
  base="${base%/}"
  # HTTPS only, redirects included, unless the releases URL itself is plain HTTP (tests).
  proto='=https'
  case "$base" in http://*) proto='=https,http' ;; esac
  has curl || has wget || fail "curl or wget is required"
  has tar || fail "tar is required"

  os="$(detect_os)"
  arch="$(detect_arch "$os")"

  version="${CICORD_VERSION:-}"
  if [ -z "$version" ]; then
    version="$(latest_version "$base")" || fail "could not find the latest release at $base/latest: set CICORD_VERSION"
  fi
  version="${version#v}"
  if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
    fail "'$version' is not a version (x.y.z or x.y.z-rc.N)"
  fi

  archive="cicord_${version}_${os}_${arch}.tar.gz"
  url="$base/download/v$version"
  tmp="$(mktemp -d 2>/dev/null || mktemp -d -t cicord-install)"
  # shellcheck disable=SC2064 # $tmp is expanded now, on purpose
  trap "rm -rf '$tmp'" EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  say "downloading cicord $version for $os/$arch"
  fetch "$url/checksums.txt" "$tmp/checksums.txt" ||
    fail "cicord $version not found: no $url/checksums.txt (versions: https://github.com/$REPO/releases)"
  fetch "$url/$archive" "$tmp/$archive" ||
    fail "could not download $url/$archive"

  want="$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/checksums.txt")"
  [ -n "$want" ] || fail "$archive is not listed in checksums.txt"
  got="$(sha256 "$tmp/$archive")"
  [ "$got" = "$want" ] || fail "checksum mismatch for $archive: expected $want, got $got (not installing it)"
  say "checksum verified (SHA-256 $got)"

  verify_attestation "$tmp/$archive"

  tar -xzf "$tmp/$archive" -C "$tmp" cicord || fail "could not extract cicord from $archive"
  [ -f "$tmp/cicord" ] || fail "$archive has no cicord binary"

  pick_install_dir
  if [ -n "$sudo" ]; then
    say "installing to $dir with sudo (set CICORD_INSTALL_DIR to choose another directory)"
    sudo mkdir -p "$dir"
  fi
  $sudo install -m 0755 "$tmp/cicord" "$dir/cicord" || fail "could not install cicord into $dir"

  installed="$("$dir/cicord" version)" || fail "$dir/cicord was installed but does not run"
  printf 'Installed %s at %s\n' "$installed" "$dir/cicord"
  case ":${PATH:-}:" in
    *":$dir:"*) ;;
    *)
      say "$dir is not on your PATH: add it, e.g. in ~/.profile:"
      say "  export PATH=\"$dir:\$PATH\""
      ;;
  esac
}

main "$@"
