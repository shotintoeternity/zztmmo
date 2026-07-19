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

- `*.zwd` / `*.ZZT` — the micro-worlds. Each is authored as ZWD and compiled
  **once**; the committed `.ZZT` bytes are the pinned input to *both* the
  oracle and the engine (the oracle never depends on zztmmo's correctness —
  identical world bytes feed two independent interpreters).
  `engine/oracle_worlds_test.go` locks each `.ZZT` to its `.zwd` source;
  re-pinning after an edit is `ZZT_PARITY_REGEN=1` plus `make oracle-regen`.
  ORCLROOM is the M16.2 seam world; the M16.3 sweep added ORCLMOVE (terrain,
  walls, text, pushing, board-edge refusal), ORCLITEM (ammo/gem/torch/keys/
  doors), ORCLDARK (darkness and torches), ORCLNRG (energizer), ORCLSHOT
  (shooting, breakable, ricochet, max-shots), ORCLPASS (passages, post-passage
  unpause, board-edge transfer), and ORCLTIME (per-board time limit).
- `*.scn` — scenario scripts, the shared contract between
  `frontend_oracle.c` and `engine/oracle_parity_test.go` (directives documented
  in both). Each names its world in a `world NAME` directive that `regen.sh`
  reads.
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
(~110ms) per cycle. The oracle's `move`/`shoot`/`key` directives (keypress + 8
PIT ticks) are therefore 4 cycles: one consuming the keypress, three idle. The
per-board time limit runs on the BIOS day clock (`SOUNDS.PAS`
`UseSystemTimeForElapsed`), which Zeta derives from the same PIT at
65536000/1193181.66 ms per tick — one board second every 19 ticks at speed 4;
`Engine.BoardTimeElapsed` ports that arithmetic over the engine's virtual
`TimerTicks`. The Go adapter in `engine/oracle_parity_test.go` documents the
full mapping, the representation normalizations (pause blink, modal scrolls,
the stubbed walk click), and the ISR-preemption-aware sound matching (a queued
melody replaces whatever is still sounding; drum bursts match by onset count
because several drum tables are seeded from the oracle's boot RNG).
