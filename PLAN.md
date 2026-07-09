# ZZTMMO — Evaluation of Prior Proposals & Implementation Plan

*2026-07-09*

> **SUPERSEDED IN PART:** Part 2's language choice (TypeScript transliteration)
> was reversed the same day after verifying benhoyt/zztgo is a runnable ~parity
> baseline: **the engine is now a fork of zztgo (Go)**; TypeScript remains for
> the browser client only. See ANALYSIS.md (code analysis + rationale) and
> IMPLEMENTATION.md (current milestones). Parts 1 and 3 remain accurate.

## Part 1: Evaluation of the three documents

### 1. `ruzzt.txt` (build on the RUZZT Rust engine)

**What it gets right** — the network architecture. Server-authoritative headless
simulation, fixed ~9 Hz tick, board-as-room sharding, seeded server-side RNG,
snapshot-on-join + RLE tile diffs + stat-field deltas per tick, WebSocket
transport. This is the correct model and this plan adopts it nearly verbatim.

**What it gets wrong or underestimates:**

- *"Small, surgical extension to ruzzt_engine for N players"* is wishful. The
  single-player assumption isn't one `Player` stat — it's pervasive: health,
  ammo, gems, torches, keys, score, and flags are **global world-header state**,
  not stat state; the scroll/message UI and game-start pause **freeze the whole
  simulation**; board transitions replace the entire live board. Multiplayer
  surgery touches all of it, in someone else's unfamiliar Rust codebase.
- RUZZT is dormant (17 commits, no releases, effectively unmaintained since
  ~2019) and known-incomplete (no speed control, missing edge-case bugs).
  The prior WASM build attempt already hit dependency rot (`dirs` crate patch
  failure) — that was the canary.

**Verdict: adopt the architecture, reject the engine choice.** Keep RUZZT as a
readable reference implementation of ZZT logic in a memory-safe language.

### 2. `zeta.txt` (use the Zeta emulator)

**This document's core insight is the best idea across all three docs:** Zeta +
Reconstruction of ZZT is the *golden reference and compatibility oracle*, not
the multiplayer core. Retrofitting netcode into an emulated DOS process is
correctly identified as a dead end (memory hooking, global modal UI, desync).

Verified current: Zeta is **actively maintained** (v1.1.4, July 2025, MIT).

**One caveat:** the doc's suggestion of an automated harness that snapshots
Zeta's internal state every tick and bit-compares against our engine is harder
than it sounds (instrumenting emulated DOS memory, aligning tick semantics).
Plan for *replay-based* comparison — scripted inputs, compare board outcomes at
checkpoints — plus human side-by-side play, not bit-exact per-tick diffs.

**Verdict: adopt wholesale, with realistic testing expectations.** Zeta also
gives us a free "classic single-player mode" for the site later.

### 3. `conversion-approach.txt` (deep research on porting the Pascal source)

**Right conclusion, verbose path:** rewrite/port guided by the Reconstruction
source rather than patching Turbo Pascal or emulating; server-authoritative;
modular separation of engine/net/render. Its component-by-component analysis of
what breaks under multiplayer (global counters, modal scrolls, input, timing)
is accurate and useful — Part 3 below turns it into concrete decisions.

**Claims verified:** Ben Hoyt's Pascal→Go conversion is real
(`benhoyt/pas2go`, `benhoyt/zztgo`) — an important proof that the
Reconstruction source is small and regular enough to *transliterate
mechanically* into a modern language. libzoo (asiekierka's portable C engine)
also exists (MIT), though it's sparsely documented and not obviously complete.

**Where the transcript went off the rails:** it decayed into build-system
firefighting (RUZZT wasm patches, Emscripten installs via apt) — symptoms of
choosing a foundation that fights the toolchain. The lesson: pick a stack with
zero exotic build steps.

**Verdict: adopt the conclusion, skip the toolchain detours.**

### Prior art warning

`github.com/Marak/zztmmo` already exists — a Node.js/jQuery ZZT MMO attempt
(~2010) that shipped single-player only and was abandoned. Expect the name
collision; consider a distinct release name later. Its existence is also a
useful cautionary tale: it tried to build the MMO before achieving engine
parity, and died there.

---

## Part 2: The decision — faithful TypeScript transliteration + purpose-built netcode

**Build the engine by transliterating Reconstruction of ZZT (MIT) into
TypeScript, function-for-function, then refactor three specific seams for
multiplayer.** Run the same engine code headless on the server (authoritative)
and in the browser (rendering + optional prediction).

Why this beats the alternatives:

| Option | Why not |
|---|---|
| Patch Turbo Pascal / Free Pascal | Dead toolchain, single-player assumptions baked into a language nobody debugs comfortably; still needs a browser story |
| Zeta emulator as core | Single-player DOS process; netcode retrofit = memory hooking + desync (zeta.txt is right) |
| Fork RUZZT (Rust) | Dormant upstream, invasive multiplayer surgery in unfamiliar Rust, WASM build rot already encountered |
| libzoo (C) + Emscripten | Plausible fallback, but sparse docs, unclear completeness, and Emscripten adds a toolchain layer; keep as reference |
| **Transliterate Pascal → TypeScript** | **Proven path (pas2go did exactly this to Go); one language across server/client/protocol; zero exotic toolchain; full control over the multiplayer seams; MIT source** |

Transliteration ≠ rewrite-from-spec. The Pascal is the spec *and* the source:
port `GAMEVARS.PAS`, `GAME.PAS`, `ELEMENTS.PAS`, `OOP.PAS`, `SOUNDS.PAS`
(data) line-by-line, preserving the weird stuff (stat iteration order, RNG
usage, signed-byte quirks, the deliberate bugs). Discard `VIDEO.PAS`,
`INPUT.PAS`, `TXTWIND.PAS`, `EDITOR.PAS` — those are the DOS I/O layer we
replace with canvas/WebSocket/DOM. This is exactly the kind of systematic,
granular porting work that AI-assisted development is good at, and the codebase
is ~15–20k lines of very regular Pascal.

---

## Part 3: The hard problems, decided

These are "the serious issues" — every place classic ZZT assumes one player,
with the chosen resolution. Deviations from vanilla are deliberate and
documented; vanilla worlds will *run*, but true co-op needs curated/purpose-built
worlds (all three docs agree on this).

1. **Global counters → per-player.** `World.Info` (health, ammo, gems,
   torches, torch ticks, energizer ticks, score, 7 keys, flags) becomes a
   `PlayerState` record keyed by player id. Board-owned state (tiles, stats,
   timers) stays global.
2. **No global pause, ever.** The simulation never blocks. Scroll windows,
   "press any key", game-start pause become **per-player client UI**; the world
   keeps ticking while you read. Spawn protection (brief invulnerability)
   replaces pause-on-entry.
3. **Board = room; transitions are per-player.** Every board with ≥1 player
   simulates concurrently. Touching a passage or board edge moves *that player*
   between live board instances (no leader election — the ruzzt.txt "majority
   near edge" idea dies here). Empty boards freeze and serialize.
4. **ZZT-OOP "player" targeting:** *seek/aim* → nearest player on board;
   *touch/shot triggers* → the triggering player; `#give`/`#take`/`#endgame`
   → the triggering player (or nearest if untriggered); bottom-line messages →
   broadcast to players on that board; scrolls → the triggering player only.
5. **Stat list:** raise the 150-stat cap (players consume slots), but keep
   **stable iteration order** — object messaging Heisenbugs live there.
   Player stats get reserved low indices in join order.
6. **Bullets carry owner ids.** Default policy: player bullets don't damage
   players (no friendly fire) in v1; creatures' bullets hurt everyone.
7. **Death → respawn** at the board's player-entry point with score/inventory
   penalty, replacing save-restore/game-over.
8. **Dark rooms & torches per-player:** server sends full board state; client
   masks by its own torch radius (v1; acceptable cheat surface, revisit later).
9. **Tick rate:** fixed server tick at ~9.1 Hz (ZZT default speed: one game
   cycle per 2 PIT ticks @ 18.2 Hz). Element `Cycle` semantics unchanged.
10. **Determinism:** one seeded RNG stream per board instance, server-only.
    Inputs applied in (tick, seq) order before stepping. `{seed, tick}` in
    every snapshot; input logs make any board replayable — this is also the
    test harness.

---

## Part 4: Architecture

```
apps/
  server/          Node/Bun WebSocket server; Room = one live board instance
  client/          Vite + canvas renderer (CP437 8×14), input, WebAudio beeper
packages/
  engine/          transliterated ZZT core — pure, headless, deterministic
                   (no timers, no I/O; step(inputs) → events)
  zztformat/       .ZZT world/board reader-writer (RLE), save serialization
  protocol/        message types + tile-RLE/stat-delta codecs (shared)
reference/         git submodules or copies: reconstruction-of-zzt, zztgo,
                   ruzzt, libzoo — read-only oracles
```

- **Engine purity is the load-bearing wall:** `engine` never sleeps, reads
  keyboards, or renders. Server drives it with `setInterval`-style ticks;
  tests drive it with loops; the client can run it read-only for prediction
  later. (All three docs converge on this; it's non-negotiable.)
- **Protocol** (start JSON, move hot paths to binary later):
  - C→S: `join{name}`, `input{seq, keymask}`, `chat{text}`
  - S→C: `snapshot{boardId, tick, seed, tilesRLE, stats, you}`,
    `diff{tick, tileOps, statOps, hud, sounds, message?}`,
    `boardChange{snapshot}`, `scroll{lines}` (per-player), `chat`
- **Persistence:** boards serialize through `zztformat` + a sidecar for
  per-player state. SQLite on the server in v1.
- **Deployment:** plain Node process first (100 players ≈ trivial load: a
  board diff is a few hundred bytes at 9 Hz). The Room abstraction is
  transport-agnostic on purpose — a Cloudflare Durable Object per board
  (WebSocket hibernation + per-object SQLite) is a clean Phase-5 mapping if
  hosting there is desired.

## Part 5: Phased plan with exit criteria

**Phase 0 — Skeleton (small).** Monorepo scaffolding; `zztformat` parses
TOWN.ZZT; client renders a static board pixel-faithfully (CP437 font, 16
colors, blink). *Exit: TOWN's title screen in a browser, side-by-side
indistinguishable from Zeta.*

**Phase 1 — Engine parity (the long pole).** Transliterate GAMEVARS → GAME →
ELEMENTS → OOP → SOUNDS into `engine`, single-player, driven locally in the
browser. Build the replay harness: seeded engine + scripted input →
state-hash checkpoints; compare behavior against Zeta-run Reconstruction on
TOWN.ZZT and a battery of Museum of ZZT worlds; consult zztgo/RUZZT/libzoo
source when Pascal intent is unclear. *Exit: TOWN.ZZT completable in-browser;
replay tests green and deterministic across runs.*

**Phase 2 — Multiplayer core.** Apply the Part-3 seams (per-player state,
no-pause UI, owner-tagged bullets). Server Room loop, join/leave, snapshot +
diff protocol, per-player HUD. *Exit: several browsers on one board — moving,
shooting, picking up items, triggering scripts with nearest-player semantics —
with a bot-driven soak test for state drift.*

**Phase 3 — World scale.** Concurrent board instances, per-player passage/edge
transitions, board freeze/thaw with persistence, respawn rules, chat.
*Exit: a full world where players roam boards independently and meet each
other; server restart restores state.*

**Phase 4 — MMO layer.** Accounts/auth, persistence of player progress,
moderation basics, curated co-op world set (vanilla worlds playable but
labeled), stitched multi-world "overworld" registry, instancing for crowded
boards. Optional: Durable-Objects deployment, classic mode via embedded Zeta.

**Phase 5 — Creation.** Browser editor (reuse renderer + `zztformat`),
ZZT-OOP multiplayer extensions (`#give <player>`, player-count conditionals),
world publishing pipeline.

## Part 6: Risks

- **Parity is the schedule risk.** Mitigation: transliterate rather than
  reinterpret; four cross-references (Pascal, Go, Rust, C); replay harness from
  Phase 1, not after.
- **Vanilla worlds under co-op semantics** can soft-lock or trivialize.
  Mitigation: this is a content problem, not an engine problem — label vanilla
  worlds "compatibility mode", curate co-op sets (every doc agreed).
- **JS number semantics vs Pascal integers** (16-bit wraparound, signed bytes,
  integer division) cause subtle drift. Mitigation: a `pas.ts` helper module
  (`int16()`, `div()`, RNG matching ZZT's LCG) used everywhere from day one.
- **Scope creep toward "MMO features" before parity** — what killed
  Marak/zztmmo. Mitigation: Phase 1's exit criterion is contractual.
