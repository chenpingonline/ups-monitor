#!/bin/bash
set -euo pipefail
export LC_ALL=C

ROOT="$(cd "$(dirname "$0")" && pwd)"
current="$(tr -d '[:space:]' < "$ROOT/VERSION")"
IFS=. read -r major minor patch extra <<< "$current"
if [ -n "${extra:-}" ] || ! [[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ && "$patch" =~ ^[0-9]+$ ]]; then
  echo "Invalid VERSION: $current (expected major.minor.patch)" >&2
  exit 1
fi

next="$major.$minor.$((patch + 1))"
printf '%s\n' "$next" > "$ROOT/VERSION"

temporary="$(mktemp)"
trap 'rm -f "$temporary"' EXIT
awk -F= -v version="$next" '
  $1=="version" {print "version=" version; next}
  $1=="changelog" {
    text=substr($0, index($0, "=") + 1)
    sub(/^[^[:space:]]+[[:space:]]+/, "", text)
    print "changelog=" version " " text
    next
  }
  {print}
' "$ROOT/package/manifest" > "$temporary"
mv "$temporary" "$ROOT/package/manifest"
trap - EXIT

printf '%s\n' "$next"
