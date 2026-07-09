# zztgo Code Analysis — the Single-Player → Multi-Player Surgery Map

*2026-07-09. Code cloned to `reference/zztgo` (MIT) and `reference/reconstruction-of-zzt`
(the Pascal it was machine-converted from — our tiebreaker when Go intent is unclear).
File format reference: https://wiki.zzt.org/wiki/ZZT_file_format*

## 1. What we're working with

~7,300 lines of Go, files mapping 1:1 to the Pascal units:

| File | Lines | Role |
|---|---|---|
| `elements.go` | 1753 | all element tick/touch behaviors incl. the player |
| `game.go` | 1591 | game loop, stat ops (Add/Remove/MoveStat, Damage), board change, sidebar |
| `editor.go` | 889 | board editor (ignore for now) |
| `oop.go` | 818 | ZZT-OOP interpreter |
| `txtwind.go` | 535 | scroll/text-window UI (tcell) |
| `video.go` | 377 | screen writes (tcell) |
| `sounds.go` | 306 | sound queue + note parsing (playback stubbed) |
| `gamevars.go` | 255 | all types, constants, and **all mutable state as package globals** |
| `serialize.go` | 210 | board serialize/deserialize |
| `input.go` | 169 | keyboard → `InputDeltaX/Y`, `InputKeyPressed`, `InputShiftPressed` globals |
| `lib.go` | 154 | Pascal runtime shims (strings, `Random`, `Delay`) |

Repo ships `TOWN.ZZT` and a small `lib_test.go`. Note: **Go is not installed on this
machine yet** (`brew install go` before build work starts).

**The luckiest structural break:** the machine conversion preserved the DOS seams.
All rendering funnels through `video.go` (`VideoWriteText`), all input through
`input.go`, all delays through `lib.go`. The simulation code *calls* these but doesn't
*contain* them — so we can replace those three files wholesale (headless screen buffer,
injected input, no-op delay) **without touching the thousands of call sites** in the
sim. That's the difference between renovation and demolition.

## 2. Where the file format itself assumes one player

From the wiki (confirmed in `serialize.go` / `TWorldInfo`):
- World header offsets 4–28 are *the* player's ammo, gems, keys[7], health, torches,
  score — inventory is a **property of the world file**, not of a stat.
- "The first stat stored is always the player-controlled element" — stat 0 is the player.
- `TWorld` keeps every board **serialized** (`BoardData [MAX_BOARD+1][]byte`); exactly
  one board is live in the `Board` global at a time. `BoardChange` re-serializes the
  current board and deserializes the target. The engine is single-board by construction.

Implication: `.ZZT` stays the *content* format (boards, objects, scripts import
unchanged), but live multiplayer state (players, their inventories, board instances)
needs our own sidecar format. Vanilla saves can't represent it — expected.

## 3. Inventory of single-player assumptions (the surgery map)

### 3a. Everything is a package global — no instancing
`gamevars.go:137-189`: `Board`, `World`, plus ~50 loose globals (`CurrentTick`,
`CurrentStatTicked`, `GamePaused`, `PlayerDirX/Y`, one-time hint flags, …).
**Fix:** wrap the whole var block into an `Engine` struct; mechanical rename
(`Board.` → `e.Board.` etc.). Required anyway for one-engine-per-board rooms.

### 3b. Player inventory is global world state (~51 sites)
`World.Info.{Health,Ammo,Gems,Torches,Keys,Score,TorchTicks,EnergizerTicks}` —
26 refs in `game.go`, 19 in `elements.go`, 6 in `oop.go`. The good news: OOP's
`#give/#take` resolve through a single counter-pointer switch (`oop.go:620-630`) and
`#endgame` is just `World.Info.Health = 0` (`oop.go:653`) — the interpreter-side
conversion is localized. **Fix:** `PlayerState` record keyed by player id; every site
resolves via *triggering player* (touch/shot/give) or *nearest player* (ambient).

### 3c. `Stats[0]` hardcoded as THE player
- Seek/aim targeting: `game.go:1219-1223` (monsters chase stat 0)
- Dark-room torch radius: `game.go:278` (light centered on stat 0)
- Passage teleport: `game.go:1237-1268` (moves stat 0, stamps entry point)
- Pause blink + pause-move: `game.go:1437-1475`
- Player element stamp on loop enter/exit: `game.go:1417, 1513`
- Energizer-death blast, player restore: `game.go:1319-1321`
- `MoveStat(0, …)` inside `ElementPlayerTick`: `elements.go:1237`

**Fix:** `ElementPlayerTick` already receives `statId` — honor it. Replace literal
`Stats[0]` with (a) `statId` in player-tick paths, (b) `NearestPlayer(x,y)` in
seek/targeting, (c) the triggering stat in touch paths.

### 3d. Input is one global keyboard
`ElementPlayerTick` (`elements.go:1156-1330`) reads `InputDeltaX/Y`,
`InputShiftPressed`, `InputKeyPressed` globals; `PlayerDirX/Y` (shooting direction)
are globals too. **Trap:** the input globals are *also* reused as scratch `var`
parameters in touch-proc calls (`elements.go:866, 1083`) — a Pascal idiom the
converter preserved. Don't blindly rename those. **Fix:** per-player
`{dx, dy, shoot, keyPressed}` input record, routed by the player stat being ticked;
scratch call sites get real local variables.

### 3e. Modal UI blocks the entire simulation
- OOP text windows: `OopExecute` builds and **modally displays** scrolls inside the
  interpreter (`oop.go:798-800` → `TextWindowDrawOpen/Select`)
- Quit prompt inside the player tick: `elements.go:1148`
- Help viewer: `elements.go:1281`
- The whole `GamePaused` branch: `game.go:1432-1475`

**Fix:** the engine never blocks. Scroll content becomes a `ScrollEvent{playerId,
lines}` emitted to that one client; hyperlink selection comes back as next-tick input
(`!label` sends are already just OOP sends). Pause is deleted; spawn invulnerability
replaces pause-on-entry. This is the single biggest behavioral deviation from vanilla
and it's unavoidable — every doc we evaluated agreed.

### 3f. One live board; transitions swap it in place
`ElementBoardEdgeTouch` (`elements.go:1057`) and `ElementPassageTouch`
(`elements.go:957` → `BoardPassageTeleport game.go:1237`) mutate the global `Board`.
**Fix:** one `Engine` (Room) per board with ≥1 player; edge/passage touch becomes a
`TransferEvent{playerId, toBoard, entryXY}` handled by the server — despawn here,
spawn there. Boards keep simulating while occupied; empty rooms freeze/serialize.

### 3g. The game loop is blocking and ticks one stat per iteration
`GamePlayLoop` (`game.go:1431-1502`): ticks a single stat per loop pass with the
classic cycle stagger (`CurrentTick % stat.Cycle == statId % stat.Cycle`,
`game.go:1479`), sleeps (`game.go:1490` — the crude `time.Sleep` Hoyt flagged), then
advances `CurrentTick` (wraps at 420) and polls input. **Fix:** extract
`func (e *Engine) Step(inputs map[PlayerID]Input) []Event` = tick stats 0..StatCount
in order, advance CurrentTick, return events. The server owns pacing (110 ms).
Delete `Delay`/`time.Sleep` from engine paths.

### 3h. RNG is Go's global `math/rand`
`lib.go:129`. Not seedable per engine instance, not the Turbo Pascal LCG — breaks
both determinism (replay tests) and oracle parity (Zeta comparisons). **Fix:**
per-Engine seeded RNG implementing TP's generator
(`seed = seed*0x08088405 + 1` mod 2³²; `Random(n) = ((seed>>16) * n) >> 16`);
cross-check output against the Pascal semantics before anything else is built.

### 3i. Death, game-over, high scores
Health ≤ 0 exits the play loop into high-score entry (`game.go:1505-1511`).
**Fix:** death → `RespawnEvent` (board entry point, score penalty, brief
invulnerability); high-score list becomes a server-side leaderboard, not a modal.

### 3j. Sound
`Sound/NoSound` stubbed (`lib.go:119-125`) but the queue/priority/note logic in
`sounds.go` is intact. **Fix:** don't revive playback — drain the sound queue into
per-tick events; clients synthesize via WebAudio. (Also resolves "sound doesn't
work," one of zztgo's known gaps, for free.)

## 4. Design decisions the code surfaced (new since PLAN.md)

1. **World flags are shared world state** (`oop.go:275-295`, ten slots). Scripts use
   them as quest gates. v1: keep **shared per board-instance world** (true-MMO
   semantics: one player opening the gate opens it for everyone). Revisit per-player
   flags only if curated worlds need them.
2. **One-time hint messages** (`MessageAmmoNotShown` etc., `gamevars.go:149-159`)
   move into `PlayerState` — trivially per-player.
3. **Dark rooms** light from stat 0's position using *the world's* torch ticks
   (`game.go:278`). Server sims with darkness off; each client masks by its own
   player's torch state (accepted cheat surface for v1).
4. **Board time limits** (`Info.TimeLimitSec` + `World.Info.BoardTimeSec`) are
   per-player-visit in vanilla. v1: disable on multiplayer boards; flag as a curated-
   world design tool later.
5. **`SHOT_SOURCE_PLAYER/ENEMY`** (`gamevars.go:253-254`) is already a bullet
   ownership bit — extend to carry the owning player id for kill credit and the
   no-PvP-damage policy.

## 5. Revised build order (replaces IMPLEMENTATION.md milestones 0–2 detail)

1. **M0 — headless & deterministic (no behavior changes):** replace `video.go`
   (80×25 buffer + dirty list), `input.go` (injected), `lib.go` `Random` (seeded TP
   LCG per engine) and `Delay` (no-op); extract `Step()` from `GamePlayLoop`;
   build the replay/state-hash harness on TOWN.ZZT. *Exit: scripted-input replay of
   TOWN produces identical state hashes across runs; screen buffer matches Zeta at
   spot checkpoints.*
2. **M1 — instance & de-modal:** globals → `Engine` struct; text windows/prompts →
   events (3e); fix remaining known zztgo OOP bugs against the Pascal, adding a
   regression fixture per fix. *Exit: two `Engine` instances run different boards
   concurrently in one process; replay suite green.*
3. **M2 — multi-player:** `PlayerState` split (3b), N player stats + per-player input
   (3c, 3d), nearest-player targeting, owner-tagged bullets, respawn (3i), transfer
   events (3f). *Exit: bots + real clients co-roaming across boards, soak test clean.*
4. **M3+** as in IMPLEMENTATION.md (protocol/server/client were already specified
   for a dumb-terminal TS client and carry over unchanged).

## 6. Effort read

The surgery is large but *shallow* — mostly mechanical renames and seam swaps with a
running game at every step. The genuinely delicate parts, in order of risk:
(1) de-modalizing scrolls without breaking OOP control flow (`OopExecute` re-entry),
(2) per-player touch/give semantics through the interpreter, (3) nearest-player
targeting without disturbing stat iteration order. Everything else is bookkeeping.
