#!/usr/bin/env bash
# Prepares the release commit of a new version (https://cicord.dev/docs/project/releasing/):
#
#   make release VERSION=<version>     (or scripts/release.sh <version>)
#
# <version> is x.y.z, or x.y.z-rc.N for a pre-release (GitHub prerelease, npm dist-tag next).
#
# 1. sets <version> wherever the version is written (engine version, Makefile, Dockerfile,
#    the packages' package.json, @cicord/cli included);
# 2. for x.y.z, turns the CHANGELOG's [Unreleased] section into "## [x.y.z] - <today>" (it
#    must not be empty), with a fresh empty [Unreleased] above it and the compare links at
#    the bottom (from the previous release of the CHANGELOG, never from a pre-release). A
#    pre-release only checks that [Unreleased] is not empty and leaves it as is: its entries
#    go into the x.y.z section when x.y.z is released;
# 3. commits "chore(release): v<version>" and lists the other files that still mention the
#    previous version (install examples…) for review.
#
# It never pushes nor tags: push the commit, wait for CI, then tag v<version> as the release
# page describes.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# x.y.z or x.y.z-rc.N, numbers without leading zeros (semver; and no octal in $(( ))).
version_re='^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(-rc\.(0|[1-9][0-9]{0,8}))?$'

# version_gt A B: whether A > B by semver precedence (A and B match version_re). A release
# follows its pre-releases (x.y.z-rc.N < x.y.z) and rc numbers compare as numbers (rc.10 >
# rc.2); `sort -V` gets the first one wrong.
version_gt() {
  local a b i
  [[ "$1" =~ $version_re ]] || return 1
  a=("${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[5]:-1000000000}")
  [[ "$2" =~ $version_re ]] || return 1
  b=("${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[5]:-1000000000}")
  for i in 0 1 2 3; do
    if ((a[i] != b[i])); then
      ((a[i] > b[i]))
      return
    fi
  done
  return 1
}

new="${1:?usage: scripts/release.sh <x.y.z|x.y.z-rc.N>}"
new="${new#v}"
[[ "$new" =~ $version_re ]] || { echo "release: '$new' is not a version x.y.z or x.y.z-rc.N" >&2; exit 1; }
pre=false
if [[ "$new" == *-* ]]; then pre=true; fi

[[ "$(git branch --show-current)" == main ]] || { echo "release: run it on main" >&2; exit 1; }
[[ -z "$(git status --porcelain)" ]] || { echo "release: the working tree is not clean" >&2; exit 1; }

old="$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/version/version.go)"
[[ -n "$old" ]] || { echo "release: no Version in internal/version/version.go" >&2; exit 1; }
[[ "$old" =~ $version_re ]] || { echo "release: the current version '$old' is not x.y.z or x.y.z-rc.N" >&2; exit 1; }
version_gt "$new" "$old" || { echo "release: $new is not greater than $old" >&2; exit 1; }
if git rev-parse -q --verify "refs/tags/v$new" >/dev/null; then
  echo "release: the tag v$new already exists" >&2; exit 1
fi

# The [Unreleased] section: everything between its heading and the next "## [".
unreleased="$(awk '/^## \[Unreleased\]/{f=1;next} /^## \[/{f=0} f' CHANGELOG.md | grep -v '^[[:space:]]*$' || true)"
[[ -n "$unreleased" ]] || { echo "release: CHANGELOG.md has nothing under [Unreleased]" >&2; exit 1; }
# The previous release of the CHANGELOG (pre-releases have no section): the compare base.
prev="$(sed -n 's|^\[Unreleased\]: https://github.com/cicord/cicord/compare/v\(.*\)\.\.\.HEAD$|\1|p' CHANGELOG.md)"
[[ -n "$prev" ]] || { echo "release: no [Unreleased] compare link at the bottom of CHANGELOG.md" >&2; exit 1; }

# Portable in-place edit (GNU and BSD sed differ on -i).
edit() { local f="$1"; shift; sed "$@" "$f" >"$f.tmp" && mv "$f.tmp" "$f"; }

edit internal/version/version.go -e "s/^var Version = \"$old\"$/var Version = \"$new\"/"
edit Makefile -e "s/^VERSION ?= $old$/VERSION ?= $new/"
edit Dockerfile -e "s/^ARG VERSION=$old$/ARG VERSION=$new/"
# The workflow of `cicord init` pins the release that generated it; the example ships the
# same file (TestInitWorkflow compares them).
edit examples/basic/.github/workflows/cicord.yml -e "s|cicord/cmd/cicord@v$old$|cicord/cmd/cicord@v$new|"
for p in sdk client react ui cli admin create-cicord-portal; do
  f="packages/$p/package.json"
  # Only the package's own version (the first "version" key), not a dependency's.
  awk -v old="\"version\": \"$old\"" -v new="\"version\": \"$new\"" \
    '!done && index($0, old) { sub(old, new); done = 1 } { print }' "$f" >"$f.tmp" && mv "$f.tmp" "$f"
done

if [[ "$pre" == false ]]; then
  today="$(date -u +%Y-%m-%d)"
  awk -v v="$new" -v d="$today" -v prev="$prev" '
    /^## \[Unreleased\]/ { print; print ""; print "## [" v "] - " d; next }
    /^\[Unreleased\]: / {
      print "[Unreleased]: https://github.com/cicord/cicord/compare/v" v "...HEAD"
      print "[" v "]: https://github.com/cicord/cicord/compare/v" prev "...v" v
      next
    }
    { print }
  ' CHANGELOG.md >CHANGELOG.md.tmp && mv CHANGELOG.md.tmp CHANGELOG.md
fi

# Every spot must have changed: a mismatch means the file layout moved.
for f in internal/version/version.go Makefile Dockerfile examples/basic/.github/workflows/cicord.yml; do
  grep -qF "$new" "$f" || { echo "release: $f was not updated, check it" >&2; exit 1; }
done
for p in sdk client react ui cli admin create-cicord-portal; do
  grep -qF "\"version\": \"$new\"" "packages/$p/package.json" || { echo "release: packages/$p/package.json was not updated" >&2; exit 1; }
done

git add internal/version/version.go Makefile Dockerfile CHANGELOG.md packages/*/package.json examples/basic/.github/workflows/cicord.yml
git commit -q -m "chore(release): v$new"
echo "Committed chore(release): v$new ($old → $new)."

# Install examples and docs name the latest release ($prev): a release replaces it (and a
# pre-release $old it followed), a pre-release does not.
if [[ "$pre" == false ]]; then
  olds=("$prev")
  if [[ "$old" != "$prev" ]]; then olds+=("$old"); fi
  pats=()
  for o in "${olds[@]}"; do pats+=(-e "$o"); done
  leftovers="$(git grep -l -F "${pats[@]}" -- ':!CHANGELOG.md' ':!internal/catalog/**' ':!**/testdata/**' ':!**/*_test.go' ':!pnpm-lock.yaml' ':!examples/**' || true)"
  if [[ -n "$leftovers" ]]; then
    echo
    echo "These files still mention ${olds[*]} (install examples, docs): check whether they should say $new:"
    while IFS= read -r f; do echo "  $f"; done <<<"$leftovers"
  fi
fi
cat <<EOF

Next:
  1. review the commit (git show), amend if needed, then git push origin main and wait for CI;
  2. git tag -a v$new -m "Cicord $new" && git push origin v$new
  3. approve the npm environment once the release run is green.
EOF
if [[ "$pre" == true ]]; then
  cat <<EOF

v$new is a pre-release: a GitHub prerelease (not "Latest"), the npm dist-tag next, and
only the image tags :$new and :sha-<commit>. CHANGELOG.md is unchanged: its [Unreleased]
entries go into the release of ${new%%-*}. Install examples and docs stay on $prev.
EOF
fi
