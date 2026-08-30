#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"; PKG="$ROOT/package"; OUT="$ROOT/dist"
mkdir -p "$OUT"; "$ROOT/audit.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
COPYFILE_DISABLE=1 tar --sort=name --owner=0 --group=0 --numeric-owner -czf "$TMP/app.tgz" -C "$PKG/app" .
SUM="$(md5sum "$TMP/app.tgz" | awk '{print $1}')"
mkdir -p "$TMP/outer"
cp "$TMP/app.tgz" "$TMP/outer/app.tgz"
cp -a "$PKG/cmd" "$PKG/config" "$PKG/wizard" "$TMP/outer/"
cp "$PKG/ICON.PNG" "$PKG/ICON_256.PNG" "$TMP/outer/"
awk -v s="$SUM" 'BEGIN{done=0} /^checksum=/{print "checksum="s;done=1;next} {print} END{if(!done)print "checksum="s}' "$PKG/manifest" > "$TMP/outer/manifest"
tar --sort=name --owner=0 --group=0 --numeric-owner -czf "$OUT/fnos-ups-monitor_0.1.4_x86_candidate.fpk" -C "$TMP/outer" .
sha256sum "$OUT/fnos-ups-monitor_0.1.4_x86_candidate.fpk" > "$OUT/fnos-ups-monitor_0.1.4_x86_candidate.fpk.sha256"
echo "$OUT/fnos-ups-monitor_0.1.4_x86_candidate.fpk"
