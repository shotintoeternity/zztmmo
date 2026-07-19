# The vanilla oracle (M16.2)

An independent ground-truth runner for ZZT 3.2 behavior: the **real ZZT.EXE**
executing under the [Zeta](https://github.com/asiekierka/zeta) 8086 emulator,
driven headless by a scripted keyboard schedule over a virtual clock, with
checkpoints read straight out of the emulated text VRAM (0xB8000) and every
PC-speaker transition logged. Nothing in a capture was produced by zztmmo — the
whole point is comparing this engine against state it did not generate.

## Pieces

| File | Role |
|---|---|
| `frontend_oracle.c` | The custom Zeta frontend: virtual clock, scenario interpreter, VRAM/speaker capture. Copied into the pinned Zeta checkout by `build.sh`. |
| `config_build.h` | Hand-pinned build config (normally meson-generated). |
| `build.sh` | Clones/checks out Zeta at the pinned commit into `reference/zeta` (gitignored), generates its font assets, compiles `reference/oracle/bin/zeta_oracle`. |
| `fetch_zzt.sh` | Downloads the Museum of ZZT's `zzt.zip` (ZZT v3.2, Epic's freeware release) and verifies zip, `ZZT.EXE`, and `ZZT.DAT` by sha256. Never committed. |
| `regen.sh` | `make oracle-regen`: runs both scripts, assembles a work dir, replays every `fixtures/oracle/*.scn`, rewrites the committed captures and `provenance.json`. |

## The committed fixtures (`fixtures/oracle/`)

- `ORCLROOM.zwd` / `ORCLROOM.ZZT` — the micro-world. Authored as ZWD and
  compiled **once**; the committed `.ZZT` bytes are the pinned input to *both*
  the oracle and the engine (the oracle never depends on zztmmo's correctness —
  identical world bytes feed two independent interpreters).
- `*.scn` — scenario scripts, the shared contract between
  `frontend_oracle.c` and `engine/oracle_parity_test.go` (directives documented
  in both).
- `*.capture.txt` — the oracle's checkpoints: `checkpoint LABEL` followed by
  25 rows × 80 cells of `chattr` hex, with `sound on FREQ @MS` / `sound off`
  lines interleaved between checkpoints.
- `provenance.json` — every hash that pins a capture: Zeta commit, frontend
  source, `zzt.zip`/`ZZT.EXE`/`ZZT.DAT`, world bytes, scenario scripts.

## Rules

- **Tests never run the oracle.** `go test` compares the engine against the
  committed captures offline; a clean clone needs no network, compiler, or
  emulator. Missing captures are a hard failure, not a skip.
- **Regeneration is an explicit maintainer command** (`make oracle-regen`),
  never part of a test run. Regenerating with unchanged inputs must reproduce
  the committed captures byte-for-byte (verified when this was landed).
- Expected stderr noise from ZZT under emulation: `creat: file not found: LPT1`
  and `open: file not found: ORCLROOM.HI`.

## Timing model

Vanilla paces one game cycle per `TickTimeDuration = TickSpeed*2` hundredths of
a second (`GAME.PAS:1511,1582`) — at the default speed 4 that is ~2 PIT ticks
(~110ms) per cycle. The oracle's `move` directive (keypress + 8 PIT ticks) is
therefore 4 cycles: one consuming the keypress, three idle. The Go adapter in
`engine/oracle_parity_test.go` documents the full mapping and the three
representation normalizations (pause blink, modal scrolls, the stubbed walk
click).
