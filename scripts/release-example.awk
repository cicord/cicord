# Convert exactly the expected repository CI pins into tag installs at release preparation.
BEGIN {
  prefix = "      - run: go install "
  path = "github.com/cicord/cicord"
  location = path "/cmd/cicord@"
}
index($0, prefix) == 1 {
  split($0, parts, "@")
  ref = parts[2]
  tag = prefix location "v" old
  snapshot = prefix "-ldflags=\"-X " path "/internal/version.Version=" old " -X " path "/internal/version.SourceRef=" ref "\" " location ref
  if ($0 != tag && ($0 != snapshot || length(ref) != 40 || ref ~ /[^0-9a-f]/)) bad = 1
  $0 = prefix location "v" new
  count++
}
{ print }
END { if (count != 3 || bad) exit 1 }
