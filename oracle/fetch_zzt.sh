#!/bin/sh
# oracle/fetch_zzt.sh — fetch the pinned vanilla ZZT v3.2 program the oracle
# runs. Downloads the Museum of ZZT's registered release (freeware since Epic's
# 1997 release; redistributed by the Museum) and verifies every byte by sha256.
# The program is never committed; reference/ is gitignored.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEST="$ROOT/reference/oracle"

ZIP_URL="https://museumofzzt.com/zgames/z/zzt.zip"
ZIP_SHA256="fdf5a0f9f45c32447199aaad3d3caa5dbabcb6ec6a5f55cd954d060515488d5e"
EXE_SHA256="6a7f8d7f60f33f43ca4b008c02ae436cb4025da259b5c73947580f6ddf06fadb"
DAT_SHA256="94c371c09d252046416505704d610d189fbe9e542b709d1008f325f65ca904b8"

sha() { shasum -a 256 "$1" | cut -d' ' -f1; }

mkdir -p "$DEST"
if [ ! -f "$DEST/zzt.zip" ] || [ "$(sha "$DEST/zzt.zip")" != "$ZIP_SHA256" ]; then
    curl -fsSL -A 'zztmmo-oracle-fetch/1.0 (github.com/shotintoeternity/zztmmo)' \
        -o "$DEST/zzt.zip" "$ZIP_URL"
fi
[ "$(sha "$DEST/zzt.zip")" = "$ZIP_SHA256" ] || { echo "zzt.zip sha256 mismatch" >&2; exit 1; }

mkdir -p "$DEST/zzt"
unzip -o -q "$DEST/zzt.zip" ZZT.EXE ZZT.DAT -d "$DEST/zzt"
[ "$(sha "$DEST/zzt/ZZT.EXE")" = "$EXE_SHA256" ] || { echo "ZZT.EXE sha256 mismatch" >&2; exit 1; }
[ "$(sha "$DEST/zzt/ZZT.DAT")" = "$DAT_SHA256" ] || { echo "ZZT.DAT sha256 mismatch" >&2; exit 1; }

echo "fetched ZZT v3.2 into $DEST/zzt (ZZT.EXE $EXE_SHA256)"
