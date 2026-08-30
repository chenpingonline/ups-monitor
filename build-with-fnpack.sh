#!/bin/bash
set -euo pipefail
export LC_ALL=C

ROOT="$(cd "$(dirname "$0")" && pwd)"
PLATFORM="${1:-x86}"
case "$PLATFORM" in
  x86) GOARCH=amd64 ;;
  arm|arm64) PLATFORM=arm; GOARCH=arm64 ;;
  *) echo "Usage: $0 [x86|arm]" >&2; exit 2 ;;
esac

FNPACK="${FNPACK:-$ROOT/.tools/fnpack-1.2.3-linux-amd64}"
URL="https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64"
SHA256="54b97fa7b70968c4d05c79840f5daeff508957d0bb2062fdb0376d00d9615c93"
mkdir -p "$ROOT/.tools" "$ROOT/dist"
if [ ! -x "$FNPACK" ]; then
  echo "Downloading official fnpack 1.2.3..."
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error "$URL" -o "$FNPACK"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$FNPACK" "$URL"
  else
    echo "curl/wget not found" >&2; exit 1
  fi
fi
actual="$(sha256sum "$FNPACK" | awk '{print $1}')"
[ "$actual" = "$SHA256" ] || { echo "fnpack SHA-256 mismatch: $actual" >&2; exit 1; }
chmod 755 "$FNPACK"

VERSION="$("$ROOT/bump-version.sh")"
printf 'Release version: %s\n' "$VERSION"

temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
stage="$temporary/package"
cp -R "$ROOT/package" "$stage"
cp "$ROOT/src/static/index.html" "$ROOT/src/static/ups-device.png" "$ROOT/src/static/ups-device-dark.png" "$ROOT/src/static/tabler-icons.svg" "$stage/app/static/"
CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build \
  -C "$ROOT/src" -trimpath -ldflags "-s -w -X main.version=$VERSION" \
  -o "$stage/app/ups-monitor" .
chmod 755 "$stage/app/ups-monitor"
awk -F= -v version="$VERSION" -v platform="$PLATFORM" '
  $1=="version" {print "version=" version; next}
  $1=="platform" {print "platform=" platform; next}
  {print}
' "$ROOT/package/manifest" > "$stage/manifest"
"$ROOT/audit.sh" "$stage"

cd "$temporary"
"$FNPACK" build --directory "$stage"
found=""
for path in "$temporary/fnos-ups-monitor.fpk" "$stage/fnos-ups-monitor.fpk"; do
  [ -f "$path" ] && found="$path" && break
done
[ -n "$found" ] || { echo "fnpack completed but fnos-ups-monitor.fpk was not found" >&2; exit 1; }
artifact="$ROOT/dist/fnos-ups-monitor_${VERSION}_${PLATFORM}.fpk"
mv -f "$found" "$artifact"
sha256sum "$artifact" > "$artifact.sha256"
echo "Built: $artifact"
