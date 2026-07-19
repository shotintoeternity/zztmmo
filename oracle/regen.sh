#!/bin/sh
# oracle/regen.sh — regenerate the committed oracle captures (M16.2, M16.3).
#
# EXPLICIT MAINTAINER COMMAND (make oracle-regen). Tests never run the oracle;
# they compare the engine against the captures this script commits. Every input
# is pinned: the Zeta emulator revision (oracle/build.sh), the vanilla ZZT.EXE
# bytes (oracle/fetch_zzt.sh), the world files, and the scenario scripts. The
# captures come from the real ZZT.EXE under emulation — never from zztmmo.
#
# Each fixtures/oracle/*.scn is replayed against the world named by its
# `world NAME` directive (the committed NAME.ZZT bytes, authored as NAME.zwd
# and compiled once — see engine/oracle_worlds_test.go).
#
# Expected stderr noise from ZZT under emulation: "creat: file not found: LPT1"
# (printer port) and "open: file not found: <WORLD>.HI" (no high-score file).
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
FIX="$ROOT/fixtures/oracle"
REF="$ROOT/reference/oracle"
WORK="$REF/work"

sh "$ROOT/oracle/fetch_zzt.sh"
sh "$ROOT/oracle/build.sh"

sha() { shasum -a 256 "$1" | cut -d' ' -f1; }

scenario_entries=""
world_entries=""

for scn in "$FIX"/*.scn; do
    name=$(basename "$scn" .scn)
    world=$(awk '$1 == "world" { print $2; exit }' "$scn")
    if [ -z "$world" ]; then
        echo "oracle: $scn has no world directive" >&2
        exit 1
    fi
    if [ ! -f "$FIX/$world.ZZT" ]; then
        echo "oracle: $scn names missing world $world.ZZT" >&2
        exit 1
    fi

    echo "oracle: running $name.scn (world $world)"
    rm -rf "$WORK"
    mkdir -p "$WORK"
    cp "$REF/zzt/ZZT.EXE" "$REF/zzt/ZZT.DAT" "$WORK/"
    printf '%s\r\nREGISTERED\r\n' "$world" > "$WORK/ZZT.CFG"
    cp "$FIX/$world.ZZT" "$WORK/"
    (cd "$WORK" && ZZT_ORACLE_SCN="$scn" ZZT_ORACLE_OUT="$FIX/$name.capture.txt" \
        "$REF/bin/zeta_oracle" -t "$world.ZZT")

    scenario_entries="$scenario_entries    \"$name.scn\": \"$(sha "$scn")\",\n"
    case "$world_entries" in
        *"\"$world.ZZT\""*) ;;
        *) world_entries="$world_entries    \"$world.ZZT\": \"$(sha "$FIX/$world.ZZT")\",\n" ;;
    esac
done

scenario_entries=$(printf "$scenario_entries" | sed '$s/,$//')
world_entries=$(printf "$world_entries" | sed '$s/,$//')

cat > "$FIX/provenance.json" <<EOF
{
  "generated_by": "oracle/regen.sh (make oracle-regen)",
  "runner": {
    "emulator": "zeta",
    "repo": "https://github.com/asiekierka/zeta.git",
    "commit": "ad85bcf81971460c8d29951e489658855ced225c",
    "frontend": "oracle/frontend_oracle.c",
    "frontend_sha256": "$(sha "$ROOT/oracle/frontend_oracle.c")"
  },
  "program": {
    "name": "ZZT v3.2 (registered, Epic freeware release)",
    "source": "https://museumofzzt.com/zgames/z/zzt.zip",
    "zip_sha256": "$(sha "$REF/zzt.zip")",
    "zzt_exe_sha256": "$(sha "$REF/zzt/ZZT.EXE")",
    "zzt_dat_sha256": "$(sha "$REF/zzt/ZZT.DAT")",
    "zzt_cfg": "<WORLD>\\r\\nREGISTERED\\r\\n"
  },
  "worlds": {
$world_entries
  },
  "determinism": {
    "timer_offset": 0,
    "virtual_clock_start_ms": 0,
    "note": "Randomize() seeds from the fixed virtual clock; the compared path is RNG-free."
  },
  "scenarios": {
$scenario_entries
  }
}
EOF

echo "regenerated captures for $(ls "$FIX"/*.scn | wc -l | tr -d ' ') scenarios + $FIX/provenance.json"
