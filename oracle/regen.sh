#!/bin/sh
# oracle/regen.sh — regenerate the committed M16.2 oracle captures.
#
# EXPLICIT MAINTAINER COMMAND (make oracle-regen). Tests never run the oracle;
# they compare the engine against the captures this script commits. Every input
# is pinned: the Zeta emulator revision (oracle/build.sh), the vanilla ZZT.EXE
# bytes (oracle/fetch_zzt.sh), the world file, and the scenario scripts. The
# captures come from the real ZZT.EXE under emulation — never from zztmmo.
#
# Expected stderr noise from ZZT under emulation: "creat: file not found: LPT1"
# (printer port) and "open: file not found: ORCLROOM.HI" (no high-score file).
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
FIX="$ROOT/fixtures/oracle"
REF="$ROOT/reference/oracle"
WORK="$REF/work"

sh "$ROOT/oracle/fetch_zzt.sh"
sh "$ROOT/oracle/build.sh"

rm -rf "$WORK"
mkdir -p "$WORK"
cp "$REF/zzt/ZZT.EXE" "$REF/zzt/ZZT.DAT" "$WORK/"
printf 'ORCLROOM\r\nREGISTERED\r\n' > "$WORK/ZZT.CFG"
cp "$FIX/ORCLROOM.ZZT" "$WORK/"

for name in main scroll; do
    echo "oracle: running $name.scn"
    (cd "$WORK" && ZZT_ORACLE_SCN="$FIX/$name.scn" ZZT_ORACLE_OUT="$FIX/$name.capture.txt" \
        "$REF/bin/zeta_oracle" -t ORCLROOM.ZZT)
done

sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
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
    "zzt_cfg": "ORCLROOM\\r\\nREGISTERED\\r\\n"
  },
  "world": {
    "file": "ORCLROOM.ZZT",
    "sha256": "$(sha "$FIX/ORCLROOM.ZZT")",
    "authored_as": "ORCLROOM.zwd (compiled once by zwd; identical bytes are the input to both engines)"
  },
  "determinism": {
    "timer_offset": 0,
    "virtual_clock_start_ms": 0,
    "note": "Randomize() seeds from the fixed virtual clock; the compared path is RNG-free."
  },
  "scenarios": {
    "main.scn": "$(sha "$FIX/main.scn")",
    "scroll.scn": "$(sha "$FIX/scroll.scn")"
  }
}
EOF

echo "regenerated: $FIX/main.capture.txt $FIX/scroll.capture.txt $FIX/provenance.json"
