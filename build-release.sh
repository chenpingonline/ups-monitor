#!/bin/bash
set -euo pipefail
export LC_ALL=C

ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT="$ROOT/dist"
mkdir -p "$OUT"

build_package() {
  platform="$1"
  goarch="$2"
  temporary="$(mktemp -d)"
  stage="$temporary/package"
  trap 'rm -rf "$temporary"' RETURN

  cp -R "$ROOT/package" "$stage"
  cp "$ROOT/src/static/index.html" "$ROOT/src/static/ups-device.png" "$ROOT/src/static/ups-device-dark.png" "$ROOT/src/static/tabler-icons.svg" "$stage/app/static/"
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build \
    -C "$ROOT/src" -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$stage/app/ups-monitor" .
  chmod 755 "$stage/app/ups-monitor"

  awk -F= -v version="$VERSION" -v platform="$platform" '
    $1=="version" {print "version=" version; next}
    $1=="platform" {print "platform=" platform; next}
    $1=="changelog" {print "changelog=" version " 新增状态、趋势、事件、设备和设置五个功能页，优化 fnOS 窗口布局与主题体验。"; next}
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

TARGET="${1:-all}"
case "$TARGET" in
  all|x86|arm|arm64) ;;
  *) echo "Usage: $0 [all|x86|arm]" >&2; exit 2 ;;
esac

VERSION="$("$ROOT/bump-version.sh")"
printf 'Release version: %s\n' "$VERSION"

case "$TARGET" in
  all)
    build_package x86 amd64
    build_package arm arm64
    ;;
  x86) build_package x86 amd64 ;;
  arm|arm64) build_package arm arm64 ;;
esac
