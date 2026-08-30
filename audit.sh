#!/bin/bash
set -euo pipefail
export LC_ALL=C
ROOT="$(cd "$(dirname "$0")" && pwd)"; PKG="${1:-$ROOT/package}"
required=(main install_init install_callback upgrade_init upgrade_callback uninstall_init uninstall_callback config_init config_callback)
for f in "${required[@]}"; do test -x "$PKG/cmd/$f" || { echo "ERROR missing/executable cmd/$f"; exit 1; }; done
for f in index.html ups-device.png ups-device-dark.png tabler-icons.svg; do test -s "$PKG/app/static/$f" || { echo "ERROR missing app/static/$f"; exit 1; }; done
python3 - <<PY
import json,pathlib
p=pathlib.Path('$PKG')
for f in [p/'config/privilege',p/'config/resource',p/'app/ui/config',*sorted((p/'wizard').iterdir())]:
    json.load(open(f,encoding='utf-8'))
print('JSON OK')
PY
bash -n "$PKG/cmd/"*
platform="$(awk -F= '$1=="platform"{print $2}' "$PKG/manifest")"
case "$platform" in
  x86) file "$PKG/app/ups-monitor" | grep -q 'x86-64.*statically linked' ;;
  arm) file "$PKG/app/ups-monitor" | grep -Eq '(ARM aarch64|ARM64).*statically linked' ;;
  *) echo "ERROR unsupported platform=$platform" >&2; exit 1 ;;
esac
version="$(tr -d '[:space:]' < "$ROOT/VERSION")"
grep -q "^version=$version$" "$PKG/manifest"
! grep -q '^install_dep_apps=' "$PKG/manifest"
echo "Project audit OK"
