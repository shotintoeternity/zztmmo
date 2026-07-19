#!/bin/sh
# oracle/build.sh — build the zeta_oracle harness against a pinned Zeta checkout.
#
# Produces reference/oracle/bin/zeta_oracle (reference/ is gitignored). Needs
# git, a C compiler, and python3 with Pillow (for Zeta's font asset generation).
# Maintainer tooling only: tests never run this — they read committed captures.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
ZETA="$ROOT/reference/zeta"
BIN="$ROOT/reference/oracle/bin"

ZETA_REPO="https://github.com/asiekierka/zeta.git"
ZETA_PIN="ad85bcf81971460c8d29951e489658855ced225c"

if [ ! -d "$ZETA/.git" ]; then
    git clone "$ZETA_REPO" "$ZETA"
fi
if [ "$(git -C "$ZETA" rev-parse HEAD)" != "$ZETA_PIN" ]; then
    git -C "$ZETA" fetch origin "$ZETA_PIN"
    git -C "$ZETA" checkout --quiet "$ZETA_PIN"
fi

# Font assets (zeta's meson custom targets, run by hand: font2raw + bin2c).
mkdir -p "$ZETA/gen"
gen_font() { # name png height field
    if [ ! -f "$ZETA/gen/$1.c" ]; then
        python3 "$ZETA/tools/font2raw.py" "$ZETA/fonts/$2" 8 "$3" a "$ZETA/gen/$1.bin"
        python3 "$ZETA/tools/bin2c.py" --field_name "$4" "$ZETA/gen/$1.c" "$ZETA/gen/$1.bin"
    fi
}
gen_font 8x8 pc_cga.png 8 res_8x8_bin
gen_font 8x14 pc_ega.png 14 res_8x14_bin
gen_font 8x8_cga pc_cga_orig.png 8 res_8x8_cga_bin
gen_font 8x12_window window_8x12.png 12 res_8x12_window_bin

cp "$ROOT/oracle/frontend_oracle.c" "$ZETA/src/frontend_oracle.c"
cp "$ROOT/oracle/config_build.h" "$ZETA/src/config_build.h"

mkdir -p "$BIN"
cd "$ZETA"
${CC:-cc} -O2 -DFRONTEND_POSIX_NO_AUDIO -I src -I gen -o "$BIN/zeta_oracle" \
    src/frontend_oracle.c src/zzt.c src/cpu.c src/posix_vfs.c \
    src/asset_loader.c src/util.c src/audio_shared.c src/audio_stream.c \
    src/zzt_ems.c src/ui.c \
    gen/8x14.c gen/8x8.c gen/8x8_cga.c gen/8x12_window.c

echo "built $BIN/zeta_oracle (zeta $ZETA_PIN)"
