#!/usr/bin/env bash
# Prepares the release commit of a new version (https://cicord.dev/docs/project/releasing/):
#
#   make release VERSION=<x.y.z>     (or scripts/release.sh <x.y.z>)
#
# 1. sets <x.y.z> wherever the version is written (engine version, Makefile, Dockerfile,
#    the packages' package.json);
# 2. turns the CHANGELOG's [Unreleased] section into "## [<x.y.z>] - <today>" (it must not be
#    empty), with a fresh empty [Unreleased] above it and the compare links at the bottom;
# 3. commits "chore(release): v<x.y.z>" and lists the other files that still mention the
#    previous version (install examples…) for review.
#
# It never pushes nor tags: push the commit, wait for CI, then tag v<x.y.z> as the release
# page describes.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

new="${1:?usage: scripts/release.sh <x.y.z>}"
new="${new#v}"
[[ "$new" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "release: '$new' is not a version x.y.z" >&2; exit 1; }

[[ "$(git branch --show-current)" == main ]] || { echo "release: run it on main" >&2; exit 1; }
[[ -z "$(git status --porcelain)" ]] || { echo "release: the working tree is not clean" >&2; exit 1; }

old="$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/version/version.go)"
[[ -n "$old" ]] || { echo "release: no Version in internal/version/version.go" >&2; exit 1; }
newest="$(printf '%s\n%s\n' "$old" "$new" | sort -V | tail -1)"
[[ "$new" != "$old" && "$newest" == "$new" ]] || { echo "release: $new is not greater than $old" >&2; exit 1; }
if git rev-parse -q --verify "refs/tags/v$new" >/dev/null; then
  echo "release: the tag v$new already exists" >&2; exit 1
fi

# The [Unreleased] section: everything between its heading and the next "## [".
unreleased="$(awk '/^## \[Unreleased\]/{f=1;next} /^## \[/{f=0} f' CHANGELOG.md | grep -v '^[[:space:]]*$' || true)"
[[ -n "$unreleased" ]] || { echo "release: CHANGELOG.md has nothing under [Unreleased]" >&2; exit 1; }

# Portable in-place edit (GNU and BSD sed differ on -i).
edit() { local f="$1"; shift; sed "$@" "$f" >"$f.tmp" && mv "$f.tmp" "$f"; }

edit internal/version/version.go -e "s/^var Version = \"$old\"$/var Version = \"$new\"/"
edit Makefile -e "s/^VERSION ?= $old$/VERSION ?= $new/"
edit Dockerfile -e "s/^ARG VERSION=$old$/ARG VERSION=$new/"
for p in sdk client react ui admin create-cicord-portal; do
  f="packages/$p/package.json"
  # Only the package's own version (the first "version" key), not a dependency's.
  awk -v old="\"version\": \"$old\"" -v new="\"version\": \"$new\"" \
    '!done && index($0, old) { sub(old, new); done = 1 } { print }' "$f" >"$f.tmp" && mv "$f.tmp" "$f"
done

today="$(date -u +%Y-%m-%d)"
awk -v v="$new" -v d="$today" -v old="$old" '
  /^## \[Unreleased\]/ { print; print ""; print "## [" v "] - " d; next }
  /^\[Unreleased\]: / {
    print "[Unreleased]: https://github.com/cicord/cicord/compare/v" v "...HEAD"
    print "[" v "]: https://github.com/cicord/cicord/compare/v" old "...v" v
    next
  }
  { print }
' CHANGELOG.md >CHANGELOG.md.tmp && mv CHANGELOG.md.tmp CHANGELOG.md

# Every spot must have changed: a mismatch means the file layout moved.
for f in internal/version/version.go Makefile Dockerfile; do
  grep -q "$new" "$f" || { echo "release: $f was not updated, check it" >&2; exit 1; }
done
for p in sdk client react ui admin create-cicord-portal; do
  grep -q "\"version\": \"$new\"" "packages/$p/package.json" || { echo "release: packages/$p/package.json was not updated" >&2; exit 1; }
done

git add internal/version/version.go Makefile Dockerfile CHANGELOG.md packages/*/package.json
git commit -q -m "chore(release): v$new"
echo "Committed chore(release): v$new ($old → $new)."

leftovers="$(git grep -l -F "$old" -- ':!CHANGELOG.md' ':!internal/catalog/**' ':!**/testdata/**' ':!**/*_test.go' ':!pnpm-lock.yaml' ':!examples/**' || true)"
if [[ -n "$leftovers" ]]; then
  echo
  echo "These files still mention $old (install examples, docs): check whether they should say $new:"
  while IFS= read -r f; do echo "  $f"; done <<<"$leftovers"
fi
cat <<EOF

Next:
  1. review the commit (git show), amend if needed, then git push origin main and wait for CI;
  2. git tag -a v$new -m "Cicord $new" && git push origin v$new
  3. approve the npm environment once the release run is green.
EOF
