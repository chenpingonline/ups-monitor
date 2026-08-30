#!/bin/bash
set -euo pipefail
export LC_ALL=C

ROOT="$(cd "$(dirname "$0")" && pwd)"
VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
OUT="$ROOT/dist"
mkdir -p "$OUT"

build_package() {
  platform="$1"
  goarch="$2"
  temporary="$(mktemp -d)"
  stage="$temporary/package"
  trap 'rm -rf "$temporary"' RETURN

  cp -R "$ROOT/package" "$stage"
  cp "$ROOT/src/static/index.html" "$stage/app/static/index.html"
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build \
    -C "$ROOT/src" -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$stage/app/ups-monitor" .
  chmod 755 "$stage/app/ups-monitor"

  awk -F= -v version="$VERSION" -v platform="$platform" '
    $1=="version" {print "version=" version; next}
    $1=="platform" {print "platform=" platform; next}
    $1=="changelog" {print "changelog=" version " 施耐德 APC 识别、功率与市电质量、续航趋势、NUT 能力检测和 BK650M2-CH 防误报。"; next}
    {print}
  ' "$ROOT/package/manifest" > "$stage/manifest"

  "$ROOT/audit.sh" "$stage"
  inner="$temporary/app.tgz"
  COPYFILE_DISABLE=1 tar -czf "$inner" -C "$stage/app" .
  checksum="$(md5sum "$inner" | awk '{print $1}')"
  outer="$temporary/outer"
  mkdir -p "$outer"
  cp "$inner" "$outer/app.tgz"
  cp -R "$stage/cmd" "$stage/config" "$stage/wizard" "$outer/"
  cp "$stage/ICON.PNG" "$stage/ICON_256.PNG" "$outer/"
  awk -v checksum="$checksum" '
    BEGIN {done=0}
    /^checksum=/ {print "checksum=" checksum; done=1; next}
    {print}
    END {if (!done) print "checksum=" checksum}
  ' "$stage/manifest" > "$outer/manifest"

  artifact="$OUT/fnos-ups-monitor_${VERSION}_${platform}.fpk"
  COPYFILE_DISABLE=1 tar -czf "$artifact" -C "$outer" .
  sha256sum "$artifact" > "$artifact.sha256"
  printf '%s\n' "$artifact"
}

case "${1:-all}" in
  all)
    build_package x86 amd64
    build_package arm arm64
    ;;
  x86) build_package x86 amd64 ;;
  arm|arm64) build_package arm arm64 ;;
  *) echo "Usage: $0 [all|x86|arm]" >&2; exit 2 ;;
esac
