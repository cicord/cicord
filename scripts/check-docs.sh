#!/usr/bin/env bash
# The documentation lives in one place, the docs site (sites/docs, https://cicord.dev/docs/).
# This check fails when a Markdown file appears anywhere else, except the files GitHub and
# npm display (README, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, CHANGELOG, the packages'
# READMEs), short pointer READMEs of folders, and generated templates.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

allowed='^(README|CHANGELOG|CONTRIBUTING|CODE_OF_CONDUCT|SECURITY)\.md$'
allowed+='|^\.github/'
allowed+='|^sites/'
allowed+='|^packages/[^/]+/README\.md$'
allowed+='|^(deploy/vps|spec|internal/webui/kit)/README\.md$'
allowed+='|^scripts/official-registry/template/'

stray="$(git ls-files --cached --others --exclude-standard -- '*.md' '*.mdx' | grep -v -E "$allowed" || true)"
if [[ -n "$stray" ]]; then
  echo "Documentation goes in the docs site (sites/docs/src/content/docs), not in:"
  while IFS= read -r f; do echo "  $f"; done <<<"$stray"
  echo "If a file must stay (shown by GitHub or npm), add it to scripts/check-docs.sh."
  exit 1
fi

# Folder READMEs stay pointers: anything longer belongs on the site.
for f in deploy/vps/README.md spec/README.md internal/webui/kit/README.md; do
  [[ ! -f "$f" ]] || (( $(wc -l <"$f") <= 20 )) || { echo "$f is longer than 20 lines: move the content to the site"; exit 1; }
done
echo "docs: one source (sites/docs)"
