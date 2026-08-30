#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
PKG="$ROOT/package"
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
ACTUAL="$(sha256sum "$FNPACK" | awk '{print $1}')"
[ "$ACTUAL" = "$SHA256" ] || { echo "fnpack SHA-256 mismatch: $ACTUAL" >&2; rm -f "$FNPACK"; exit 1; }
chmod 755 "$FNPACK"
# Validate files before fnpack.
for f in main install_init install_callback upgrade_init upgrade_callback uninstall_init uninstall_callback config_init config_callback; do
  [ -x "$PKG/cmd/$f" ] || { echo "missing executable cmd/$f" >&2; exit 1; }
done
python3 - <<PY
import json, pathlib
p=pathlib.Path('$PKG')
for f in [p/'config/privilege',p/'config/resource',p/'app/ui/config',*p.joinpath('wizard').iterdir()]:
    json.load(open(f,encoding='utf-8'))
PY
rm -f "$ROOT/fnos-ups-monitor.fpk" "$ROOT/dist/fnos-ups-monitor_0.1.4_x86.fpk"
cd "$ROOT"
"$FNPACK" build --directory "$PKG"
# fnpack normally writes <appname>.fpk in cwd; tolerate output in package dir too.
FOUND=""
for p in "$ROOT/fnos-ups-monitor.fpk" "$PKG/fnos-ups-monitor.fpk"; do [ -f "$p" ] && FOUND="$p" && break; done
[ -n "$FOUND" ] || { echo "fnpack completed but fnos-ups-monitor.fpk was not found" >&2; exit 1; }
mv "$FOUND" "$ROOT/dist/fnos-ups-monitor_0.1.4_x86.fpk"
sha256sum "$ROOT/dist/fnos-ups-monitor_0.1.4_x86.fpk" > "$ROOT/dist/fnos-ups-monitor_0.1.4_x86.fpk.sha256"
echo "Built: $ROOT/dist/fnos-ups-monitor_0.1.4_x86.fpk"
