#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"; PKG="$ROOT/package"
required=(main install_init install_callback upgrade_init upgrade_callback uninstall_init uninstall_callback config_init config_callback)
for f in "${required[@]}"; do test -x "$PKG/cmd/$f" || { echo "ERROR missing/executable cmd/$f"; exit 1; }; done
python3 - <<PY
import json,pathlib
p=pathlib.Path('$PKG')
for f in [p/'config/privilege',p/'config/resource',p/'app/ui/config',*sorted((p/'wizard').iterdir())]:
    json.load(open(f,encoding='utf-8'))
print('JSON OK')
PY
bash -n "$PKG/cmd/"*
file "$PKG/app/ups-monitor" | grep -q 'x86-64.*statically linked'
grep -q '^platform=x86$' "$PKG/manifest"
! grep -q '^install_dep_apps=' "$PKG/manifest"
echo "Project audit OK"
