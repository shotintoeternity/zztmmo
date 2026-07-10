# TASKS — executor protocol and task list

## Protocol (every session)

1. Find the first unchecked task below. Read its spec here, plus the ANALYSIS.md
   sections it cites. Read CLAUDE.md's hard rules if you haven't this session.
2. Do only that task. Tasks marked `[ADVISOR]` — consult the advisor with your
   approach *before* editing.
3. Verify: `cd engine && go build ./... && go test ./...` — all green.
4. Commit `M<n>.<n>: <summary>` including the checked box in this file. Stop.
5. Blocked after two honest attempts → append findings to NOTES.md, revert to
   clean tree, stop. Do not push past a red replay test by editing fixtures.

Baseline verified 2026-07-09: `engine/` builds and its tests pass on go1.26.5.

---

## M0 — Headless & deterministic (NO behavior changes)

Goal: the game simulates identically, but all I/O goes through replaceable seams
and every run is reproducible. ANALYSIS.md §3g, §3h, §5.

- [x] **M0.1 — Seeded RNG.** In `engine/lib.go`, replace `Random(end)`
  (currently Go's global `math/rand`, line ~129) with the Turbo Pascal generator:
  package-level `var RandSeed uint32`; `RandSeed = RandSeed*0x08088405 + 1`;
  `Random(end) = int16((uint32(RandSeed>>16) * uint32(end)) >> 16)`. Add
  `RandomSeed(s uint32)`. Cross-check the formula against
  `reference/reconstruction-of-zzt` docs/code comments for TP's `Random`; write a
  table-driven test asserting the first 10 values of `Random(100)` from seed 1
  (generate the table from your implementation, then verify by hand-computing the
  first 3 values from the formula). Grep for any other `rand.` use and remove it.

- [x] **M0.2 — Screen buffer.** Rewrite `engine/video.go` so all Video* functions
  write into `var Screen [80][25]struct{ Ch, Color byte }` plus a dirty-cell list,
  instead of tcell. Move tcell to a new `engine/present_tcell.go` that *reads*
  `Screen` and draws it (called from the main loop only, behind a
  `var Headless bool` guard). The sim's thousands of VideoWriteText/BoardDrawTile
  call sites must not change (ANALYSIS.md §1 "luckiest structural break").
  DoD: game still playable via `go run .`; with `Headless = true` nothing
  touches tcell (unit test: run `BoardDrawTile` headless, assert Screen contents).

- [x] **M0.3 — Injected input.** In `engine/input.go`, put keyboard polling behind
  `type InputSource interface { Poll() (dx, dy int16, shift bool, key byte) }`
  with two impls: the existing tcell keyboard, and `ScriptedInput` (a slice of
  per-tick inputs). `InputUpdate()` reads from the active source into the existing
  globals. Do NOT rename `InputDeltaX/Y` (CLAUDE.md rule 6). DoD: a test drives 20
  scripted ticks without a terminal.

- [x] **M0.4 — No sleeps in sim.** Remove `time.Sleep` from `GamePlayLoop`
  (`engine/game.go:1490`) and make `Delay()` (`engine/lib.go`) a no-op when
  `Headless`. Pacing belongs to the caller. DoD: interactive game speed unchanged
  (the `SoundHasTimeElapsed` check still paces interactive mode); headless test
  runs 1000 ticks in <1s.

- [x] **M0.5 [ADVISOR] — Extract Step().** Restructure `GamePlayLoop`
  (`engine/game.go:1431-1502`) so its body calls a new
  `func GameStep()` = "tick stats 0..StatCount with the cycle stagger at
  game.go:1479, advance CurrentTick (wrap >420 → 1), call InputUpdate". The
  interactive loop becomes: pace, then `GameStep()`. Keep the GamePaused branch
  inside the interactive wrapper, NOT inside GameStep. No behavior change
  interactively. DoD: play TOWN.ZZT manually — feels identical; headless test can
  call GameStep in a loop.

- [x] **M0.6 — Replay harness + fixtures.** New `engine/replay_test.go`:
  `StateHash()` = FNV-1a over Board.Tiles, all Stats fields, World.Info, and
  RandSeed. Test: `RandomSeed(42)`, load `TOWN.ZZT`, start play (GameStateElement
  = E_PLAYER, unpaused), drive 600 GameSteps with a fixed ScriptedInput sequence,
  record StateHash every 100 steps into `fixtures/town.replay.json` (write file if
  absent, compare if present). Run twice to prove determinism; commit the fixture.
  This test is the project's safety net — from now on it gates every commit.

## M1 — Instance & de-modal

- [x] **M1.1 — Globals → Engine struct, part 1 (state).** Move `Board`, `World`,
  and the ~50 mutable globals in `engine/gamevars.go:137-189` into
  `type Engine struct`, with a package-level `var E *Engine` and mechanical
  `Board.` → `E.Board.` rewrites (ANALYSIS.md §3a). ElementDefs (immutable after
  init) may stay global. Screen/InputSource/RandSeed move into Engine too. Keep
  `package main` working via `E = NewEngine()` at startup. DoD: builds, replay
  fixture hash UNCHANGED (this proves the refactor was pure).

- [x] **M1.2 — Two engines, one process.** Replace remaining direct uses of `E`
  inside sim functions with methods on `*Engine` (mechanical; editor.go may keep
  using `E`). DoD: test constructs two Engines on different boards of TOWN.ZZT,
  interleaves 100 GameSteps each, both replay-deterministic, no cross-talk (their
  StateHashes match single-engine runs with same seeds).

- [x] **M1.3 [ADVISOR] — De-modal scrolls.** `OopExecute` displays text windows
  modally (`engine/oop.go:798-800`); `ElementScrollTouch` similar (ANALYSIS.md
  §3e). Convert: engine gains `Events []Event` drained by the caller each step;
  scroll display becomes `ScrollEvent{Lines []string}` (single-player: the
  interactive wrapper shows the same TextWindow — behavior preserved; headless:
  event captured). Hyperlink selection (`!label;text`) re-enters as an OOP send —
  route the selection through a new `PendingScrollReply` input field. DoD: TOWN
  scroll objects readable interactively as before; headless test captures a
  ScrollEvent and sends a reply label that triggers the right OOP branch.
  DEVIATION allowed: the sim no longer freezes while a scroll is open — replay
  fixture will change; regenerate it and say so in the commit.

- [x] **M1.4 — De-modal remaining prompts.** Quit confirm inside player tick
  (`engine/elements.go:1148`), help viewer (`elements.go:1281`), high-score entry
  (`game.go:1505-1511`), `GamePromptEndPlay` — all become events or move to the
  interactive wrapper. Sim code must contain zero TextWindow/SidebarPrompt/
  InputReadWaitKey calls (grep proves it). DoD: grep clean + replay green.

- [x] **M1.5 — Sound to events.** Wire `sounds.go`'s queue into
  `SoundEvent{Notes string, Priority int16}` emitted per step instead of the
  stubbed `Sound()/NoSound()` (ANALYSIS.md §3j). DoD: headless test sees the
  gem-pickup sound event when the player steps on a gem.

## M2 — Multiple players

- [x] **M2.1 — PlayerState split.** New `type PlayerState struct` holding Health,
  Ammo, Gems, Torches, TorchTicks, EnergizerTicks, Score, Keys[7], and the
  MessageXxxNotShown hint flags (ANALYSIS.md §3b, §4.2). Engine gets
  `Players map[int16]*PlayerState` (key = stat index) + `PlayerFor(statId)`.
  Single-player: one entry at stat 0; serialize.go maps it to/from World.Info on
  load/save. Convert all ~51 `World.Info.<counter>` sim sites; OOP's counterPtr
  switch is at `oop.go:620-630`; `#endgame` (oop.go:653) sets the *triggering*
  player's health. World.Info.Flags stay SHARED (ANALYSIS.md §4.1). DoD: replay
  fixture unchanged (single player, same numbers, same order).

- [x] **M2.2 [ADVISOR] — N player stats.** `SpawnPlayer() int16` /
  `RemovePlayer(statId)`: spawn adds an E_PLAYER stat (AddStat) at
  Board.Info.StartPlayerX/Y, despawn removes it. Replace hardcoded `Stats[0]` /
  `MoveStat(0,…)` in player paths with the ticked statId
  (`elements.go:1156-1330`), and seek/targeting (`game.go:1219-1223`) plus
  dark-room lighting (`game.go:278`) with `NearestPlayer(x,y)` (ANALYSIS.md §3c).
  Stat-order rule: extra players append at the END of the stat list to keep
  vanilla stat numbering; player stats never participate in Follower/Leader.
  DoD: single-player replay unchanged; test with 3 spawned players shows a tiger
  chasing the nearest one.

- [x] **M2.3 — Per-player input.** `Step(inputs map[int16]PlayerInput)`:
  before ticking a player stat, load that player's input into the existing
  globals; zero them otherwise (ANALYSIS.md §3d — remember the scratch-var trap).
  PlayerDirX/Y become PlayerState fields. DoD: test moves two players
  independently in one step.

- [x] **M2.4 — Ownership, death, respawn.** Bullets/stars carry owner statId
  (extend SHOT_SOURCE, gamevars.go:253) — player bullets don't damage players;
  DamageStat on a player emits `DeathEvent`, respawns at entry point after N
  ticks with score penalty + 50-tick invulnerability (reuse EnergizerTicks
  mechanics), replacing game-over (ANALYSIS.md §3i). Per-player touch semantics:
  keys/doors/gems/give/take act on the triggering player (verify each TouchProc
  uses sourceStatId, not stat 0). DoD: two-player test — one dies, respawns;
  the other's inventory untouched.

- [x] **M2.5 — Transfer events.** Passage/edge touches (`elements.go:957,1057`,
  `game.go:1237-1268`) emit `TransferEvent{statId, toBoard, entry}` instead of
  swapping the global board when >1 player present, or when Engine.MultiRoom is
  set (ANALYSIS.md §3f). A `RoomManager` test type owns two Engines and moves a
  player between them. DoD: player walks through a passage from board A to
  board B while another player keeps playing board A undisturbed.

## M3 — Network

- [x] **M3.0 — Importable engine package.** Convert `engine/` from terminal-only
  `package main` into an importable engine package. Move the terminal runner under
  `cmd/zztgo`, and add a headless smoke command that can be run locally without
  opening tcell. DoD: `go build ./...`, `go test ./...`, replay green, and
  `go run ./cmd/zzt-smoke` loads TOWN and steps the sim.

- [x] **M3.1 — Production RoomManager.** Lift the M2.5 test RoomManager into real
  server-side code: one room per board, join/leave, spawn/despawn, transfer
  routing, stable tick order, occupied rooms simulate, empty rooms freeze. DoD:
  integration test with two rooms and two players crossing boards.

- [x] **M3.2 — Snapshot protocol.** Define JSON protocol structs for `join`,
  `input`, `snapshot`, `diff`, `event`, and `boardChange`. Snapshot includes board
  ID, tick, seed/hash, 80x25 screen cells, player ID/stat ID, HUD state, and
  visible events. DoD: round-trip tests and a snapshot generated from TOWN.

- [x] **M3.3 — Dirty diffs.** Expose screen dirty cells and event drains cleanly
  from the engine/room boundary. DoD: one tick produces only changed cells plus
  events, with full snapshot fallback.

- [x] **M3.4 — WebSocket server.** Add a fixed-110ms tick server that accepts
  browser clients, maps incoming keymasks to `PlayerInput`, and broadcasts
  snapshots/diffs over JSON WebSockets. DoD: a test WebSocket client can join,
  move, and receive deterministic diffs.

- [x] **M3.5 — Browser terminal client.** Add a Vite/TS client that renders
  CP437-style 80x25 cells on canvas, sends inputs, and handles snapshots, diffs,
  scroll/sound/message events. DoD: one browser can play against the server.

- [x] **M3.6 — Multiplayer browser smoke.** Two browsers or bot clients on one
  board move independently, pick up items, transfer rooms, and receive
  per-player events/HUD. DoD: scripted multi-client test passes.

- [x] **M3.7 — Soak/drift test.** Run 20 bot clients for a shorter CI duration
  first, then the 1-hour target manually/nightly. DoD: no drift, no panic, no
  runaway memory.

- [x] **M3.8 — Authentic ZZT sidebar.** Replace the temporary web HUD with a
  ZZT-styled 20x25 sidebar rendered from protocol HUD data, using CP437 glyphs,
  DOS colors, key slots, torch meter, and sound toggle styling without letting
  legacy engine sidebar writes corrupt the playable board. DoD: browser layout
  visually matches the original board+sidebar split while remaining protocol
  driven.

- [x] **M3.9 — Browser debug/help windows.** Restore browser access to modal
  keyboard flows such as the `?` debug window, help screens, and other legacy
  text-window prompts through protocol events and client-side CP437 windows.
  DoD: pressing `?` in the browser opens an interactive debug/help-style window
  without disconnecting or corrupting gameplay input.

- [x] **M3.10 — ZZT scroll message windows.** Render all `scroll` events from
  object interactions and other gameplay text prompts as in-world ZZT text
  windows instead of sidebar text, including selectable `!label;text` choices
  and replies back to the engine. Include vendor-style dialogue as one required
  fixture:
  `Vendor`
  `"Hello, you must be new to town! ..."`
  `!ba;Ammunition, 3 shots.........1 gem`
  `!bt;Torch.......................1 gem`
  `!bx;Advice......................Free`
  DoD: touching any object that opens a scroll produces a CP437-style modal
  window over the board, and selecting an option sends the expected reply
  without corrupting movement input.

- [x] **M3.11 — De-modal save; per-player pause and sound.** Three play-mode
  keys are unsafe or wrong on a server. All three block M4.2. Verified against
  the code 2026-07-09:
  * **`S` (save) would hang the whole server.** `ElementPlayerTick` case 'S'
    (`elements.go:1401`) calls `GameWorldSave` → `SidebarPromptString` →
    `PromptString` → `InputReadWaitKey`, which blocks on a key channel nothing
    feeds when headless — identical to the `?` bug fixed in M3.9, and dormant
    only because the client does not send 'S' yet. Fix it the same way: the sim
    emits `SavePromptEvent{StatId}` and returns; the caller replies via
    `Engine.SubmitSaveFilename(statId, name)`, applied at the top of the next
    step; the terminal keeps its modal prompt in the GamePlayLoop event drain.
    Decide and write down the multiplayer policy for saving a *shared* world.
  * **`P` (pause) is a silent no-op headless.** `GamePaused` is set
    (`elements.go:1404`, and `game.go:1243` on ReenterWhenZapped damage) but
    `GameStepWithInputs` never reads it. Do NOT pause the room — that freezes
    every other player (no global pause, ANALYSIS.md §3i). Make it per-player: a
    `PlayerState.Paused` flag that skips only that player's stat tick and input,
    plus a `PauseEvent` so the client can draw vanilla's blinking "Pausing..."
    at (64,5) (`game.go:1701`, `GAME.PAS:1533`).
  * **`B` (sound toggle) is process-global.** `SoundEnabled` (`sounds.go:9`) is
    a package var, so one player muting silences everyone in every room, and it
    feeds `HUDSnapshot.SoundEnabled` for all of them. Move it onto
    `PlayerState` (or make it purely client-side) so the sidebar's "Be
    quiet"/"Be noisy" line tracks only the acting player.
  DoD: a grep proves no `PromptString` / `SidebarPrompt*` / `InputReadWaitKey`
  is reachable from simulation code; a headless test has one of two players
  press 'S', 'P' and 'B' while the other keeps stepping normally; replay green.

## M4 — Browser ZZT UI and playability parity

Goal: the browser client should feel like complete ZZT, not just a multiplayer
terminal viewport. Preserve original CP437/DOS presentation and interaction
semantics while keeping the server authoritative.

- [x] **M4.0 — ZZT screen shell parity.** Render the board and sidebar as the
  original 60x25 board plus 20x25 sidebar, using CP437 cells, DOS colors, blink
  behavior where relevant, and fixed text-mode geometry. DoD: TOWN in the
  browser visually matches the original board/sidebar split with no temporary
  web-dashboard panels.

- [x] **M4.1 — Modal text-window system.** Implement the reusable browser
  CP437 text-window layer used by scrolls, help, prompts, high scores, debug
  windows, save/load confirmations, and editor dialogs. DoD: a single renderer
  and input router handles read-only text, selectable links, yes/no prompts,
  text entry, and paged help without gameplay input leaking through.

- [x] **M4.2 — Full keyboard/control parity.** Requires M3.11. Today the browser
  sends only movement, Shift/Space, Enter, Escape, `?` and `H`; everything else
  in `ElementPlayerTick`'s key switch (`elements.go:1376-1419`) is unreachable
  from a browser. Route the rest through the protocol:

  | Key | Effect | Status |
  |---|---|---|
  | arrows / numpad 8·4·6·2 | move (keymask) | done |
  | Shift+dir | shoot | done |
  | Space | shoot last direction | done |
  | `T` | light torch | done |
  | `P` | pause | done (per-player + "Pausing..." overlay) |
  | `B` | sound toggle | done (per-player) |
  | `S` | save game | done (emits `SavePromptEvent`; the server saves — M4.3a) |
  | `Q` / Esc | quit prompt | done (client answers locally; routing is M4.3) |
  | `H` | help window | done (M3.9) |
  | `?` | debug prompt | done (M3.9) |
  | ↑↓ PgUp PgDn Enter Esc | text-window navigation | done (M3.9/M3.10) |

  DoD: browser key behavior matches the terminal client for common TOWN flows,
  and each row above has a protocol-level test.
  DEVIATION: WASD movement (a M3.5 client invention) was removed — it made `S`
  ambiguous between "move down" and ZZT's save key, which arrive as the same
  `InputKeyPressed` byte. The client now sends the original's vocabulary only:
  arrows plus numpad `8/4/6/2` (`INPUT.PAS:217-234`). See NOTES.md.

- [x] **M4.3 — Title, world, save, and high-score flows.** Restore the original
  non-gameplay UI paths in the browser, including title screen/start flow,
  save/load prompts, quit confirmation, and high-score entry/display. Note
  `QuitPromptEvent` carries no `StatId` and `GamePromptEndPlay`
  (`elements.go:1239`) still reads `PlayerFor(0)`, so a quit prompt currently
  belongs to nobody and reads the wrong player's health — fix both here, the way
  M3.9 did for `HelpEvent`. `HighScoreEntryEvent` and `HighScoresAdd`
  (`game.go:1803` passes `PlayerFor(0).Score`) likewise assume a single player. DoD: a player can start, save,
  load, quit, die, enter a score, and restart without falling back to
  terminal-only UI, and one player quitting does not disturb the others.

  Landed: all three ownership fixes; `SubmitQuitReply` → `QuitEvent` →
  `RoomManager` removes only that player; the high-score list moved off `Engine`
  (one per board) onto `RoomManager` (one per world); browser title screen with
  start/restart, quit confirmation, and high-score entry + display.
  DEVIATION: a score is entered on **quit**, not on death — M2.4 already made
  death a respawn, so death is no longer an ending. The title board is a static
  render, and the monitor menu omits `S` (game speed: the server owns the tick)
  and `E` (editor: M5). See NOTES.md.
  Deferred by design to **M4.3a**, which owns them: `R` Restore game and save
  persistence — "player B loads that snapshot by name and joins it" is M4.3a's
  own DoD, and needs its sanitized `-saves` directory.
  Deferred to a future task: real world select (`W` lists the one hosted world;
  multi-world needs server-scoped client ids, because each `RoomManager` mints
  `PlayerID`s from 1 and two would collide in `WebSocketServer.clients`).

- [x] **M4.3a — Savable, rejoinable room snapshots.** Requires M3.11 (which
  builds the `SavePromptEvent`/`SubmitSaveFilename` seam but has the server
  refuse saves). Make a save snapshot the whole room and let other players load
  it and join later. `RoomManager.world` (`room_manager.go:10`) is already the
  authoritative `TWorld`, synced at `room_manager.go:417-419`, and `WorldSave`
  (`game.go:684`) already writes the vanilla format; loading one back is
  `NewRoomManager(loadedWorld)`. Three things this task must not hand-wave:
  * **The filename comes from the client.** Sanitize it — reject path
    separators, `..`, absolute paths, and anything outside a configured
    snapshot directory. A `-saves` flag like `-web`/`-help`. This is the reason
    the work is not folded into M3.11.
  * **A snapshot captures other players**, whose stats live in `World.Info` and
    whose stat entries sit on the board. Decide explicitly whether a reloaded
    snapshot respawns them at `Board.Info.StartPlayerX/Y`, drops them, or
    freezes them; write the choice in NOTES.md before implementing.
  * **World flags are global** (see the 2026-07-09 NOTES entry), so a snapshot
    also freezes shared puzzle progress. That is probably correct for a co-op
    save, but state it rather than inherit it by accident.
  DoD: player A saves a room under a name; the process restarts; player B loads
  that snapshot by name and joins it; board contents, flags, and puzzle progress
  survive the round trip; a filename containing `../` is rejected with a test
  proving it; replay green.

  Landed: `-saves` flag; `SanitizeSaveName` (a whitelist of vanilla's
  PROMPT_ALPHANUM charset, so traversal fails by construction);
  `RoomManager.SaveSnapshot` serializes every live room out of a copy, so a save
  never disturbs the game it is a save of; `RestoreSnapshot` swaps the world in
  place and is refused while anyone is in a room; browser `S` → `saveFilename` →
  `saveResult`, and the title screen's `R` lists and restores snapshots.
  The three decisions the spec demanded, all in NOTES.md: players are **dropped**
  from a snapshot (World.Info holds one player's stats, so N cannot round-trip;
  a joiner arrives fresh, as they already do on any running world); flags are
  **unioned** across live rooms (each room engine holds its own `World.Info`, so
  no single copy is authoritative); and a co-op save therefore freezes shared
  puzzle progress, which is the only coherent reading of a shared save.
  DEVIATION (both restore fidelity to the Pascal, neither touches simulation):
  `StoreWorldInfo` never wrote `World.Info.Flags` at all, though `LoadWorldInfo`
  has always read them — every world this fork saved lost its puzzle progress.
  And `WorldSave` zeroed its 512-byte header one byte at a time (`ptr[0]` in a
  512-iteration loop), leaking `BoardClose`'s scratch buffer into every file.
  See NOTES.md.
  Deferred: `WorldSave`/`WorldLoad` still write player 0's stats for the
  terminal, and the server ignores them on join — a snapshot restores the world,
  not anybody's keys.

- [x] **M4.3b — Player-on-player collision and push-out.** Two players can end up
  on the same square, and the board holds only one tile per square: the second
  player's `E_PLAYER` tile overwrites the first's, and thereafter one stat is
  standing on a tile that does not describe it. Because `GameStepWithInputs`
  dispatches tick procs **by tile element**, whichever stat loses the square can
  stop ticking entirely — the same class of failure as the ReenterWhenZapped bug
  (NOTES.md 2026-07-09). Detect the overlap and push one player to a free
  adjacent square. Known ways to produce it today:
  * Two players re-entering or respawning onto the same entry square.
  * A re-entering player landing on a square a monster or player already holds:
    `DamageStat` writes `E_PLAYER` over it, saving only the *tile* in `stat.Under`,
    so the displaced stat's element is lost.
  * `roomSpawn` has `isSpawnOpen`/`isSpawnUnoccupied` checks; re-enter and
    respawn have none. Reuse that logic rather than inventing a second policy.
  DoD: a headless test puts two players on one square by each route above; both
  keep their own tile, both keep ticking, and neither is silently deleted;
  the pushed player lands on a walkable adjacent square (or stays put if the
  board is full, never overlapping); replay green.

  Landed: `engine/placement.go` — one placement policy (`StatAt`,
  `PlacementUnoccupied`, `PlacementOpen`, `FindPlacement`) with `roomSpawn`'s ring
  search lifted into it; `isSpawnOpen`/`isSpawnUnoccupied` became room-scoped
  wrappers, so join, re-enter (`DamageStat`) and respawn (`ElementPlayerTick`) now
  pick a landing square the same way. The arriving player is pushed, never the
  incumbent. Because the arriving stat's tile is cleared before the search, its own
  square is open ground, so "nowhere to go" means re-entering in place — never an
  overlap.
  DEVIATION (the push triggers on a *stat* holding the square, not on the square
  being non-empty): the spec's "reuse `isSpawnOpen`" reads as "require `E_EMPTY`",
  but that makes M3.11's `stat.Under` save on re-enter dead code and breaks
  `TestReenterWhenZappedPreservesUnder` — landing on terrain and stashing the tile
  is deliberate M3.11 behavior, and all three DoD routes involve a stat. The landing
  square is still chosen by the shared `PlacementOpen` search. See NOTES.md.
  DEVIATION (both are fork-only paths; vanilla has no respawn and never writes the
  re-enter destination tile): respawn never set `stat.Under`, stamping the stale
  pre-death tile onto the next square walked off of; re-enter set it even when the
  player never moved, discarding their real `Under`. Both now write on an actual
  move only.
  `TestReenterUsesPlayerEntrySquareNotStaleBoardValue` (M3.11) had to move its entry
  square off (5,24), where TOWN board 19's own stat 0 stands: it was manufacturing
  the overlap this task fixes and asserting the overlapping outcome. Its stale-wall
  assertions are unchanged.

- [x] **M4.4 — Browser sound synthesis.** Convert `SoundEvent` notes into
  WebAudio playback with priority/queue behavior close to ZZT, plus a visible
  sound toggle in the authentic sidebar. DoD: pickups, shots, doors, damage,
  and object sounds are audible and can be toggled.

- [x] **M4.5 — Rendering compatibility pass.** Audit CP437 glyphs, colors,
  dark-room/torch behavior, player blinking, text elements, transition effects,
  and dirty-cell updates against terminal output. DoD: a scripted visual smoke
  covers TOWN landmarks, dark rooms, torches, gems, passages, text signs, and
  player damage/respawn without stale cells or missing glyphs.

- [x] **M4.6 — Full TOWN playthrough smoke.** Add a scripted or semi-scripted
  browser/protocol playthrough that exercises the core original game loop:
  collecting keys, buying/using items, crossing boards, opening scroll windows,
  taking damage, and reaching the Palace path. DoD: TOWN is playable end to end
  in the browser without terminal UI.

## M7 — Live-game quality (deliberately placed before M5/M6)

Goal: fix everything that makes the *current* multiplayer game feel broken
before building new feature surface. The executor protocol is positional —
"first unchecked task below" — so this section sits between M4 and the
remaining feature milestones on purpose. Ranked by player impact
(2026-07-10 planning session; see NOTES.md).

- [ ] **M7.1 [URGENT] — Spawn at the vanilla start point, not the nearest blank
  square.**
  A player joining a board whose floor is carpeted in fake walls spawns in
  whatever stray empty square the search finds, far from where the world author
  put them. `roomSpawn` (`engine/room_manager.go:296-326`) resolves the vanilla
  point (`Board.Info.StartPlayerX/Y`, or the claimable player stat), then gates
  it on `isSpawnOpen` → `PlacementOpen` (`engine/placement.go:47-53`), which
  demands `Element == E_EMPTY`. `E_FAKE` is not `E_EMPTY`, so the gate fails and
  the code falls through to `FindPlacement`, which walks outward to the nearest
  truly empty tile. Every walkable non-empty tile (fake wall, floor art,
  invisible wall) has the same effect.
  **Wanted:** the vanilla spawn point is used unconditionally *unless another
  player already stands there* — the tile's element is irrelevant, since ZZT
  simply writes the player over whatever was on the start square. Fall back to
  `FindPlacement` only on player-occupied. Note `PlacementUnoccupied`
  (`placement.go:34-39`) tests only `Element != E_PLAYER`; a square can read
  empty and still be held by a stat whose tile was clobbered, so the occupancy
  test must also consult `StatAt` (see `PlacementOpen`'s own comment).
  Watch the `requested` branch: transfers/passages pass an explicit spawn and
  must keep their current meaning.
  DoD: a test board with `E_FAKE` on the start square spawns the player on the
  start square; a second player joining the same square still gets displaced;
  `go test ./...` green, replay fixture unchanged.

- [ ] **M7.2 — Torch light must arrive with the player in dark rooms.**
  Reported as "torches only illuminate the player's path". The full lit circle
  is painted only by `DrawPlayerSurroundings` (`engine/elements.go:1202`),
  which runs when a torch is lit (`elements.go:1409-1414`), when it expires
  (`elements.go:1463-1466`), on respawn/re-enter (`elements.go:1303,1326`),
  and on a *non-adjacent* move (`game.go:1136-1139`). An adjacent move
  repaints only the ring whose lit-state changed (`game.go:1123-1135`) —
  correct in vanilla only because the full circle is already on screen. Both
  M4.5 torch tests pass (`m4_5_test.go:142,189`), so lighting-in-place works;
  the unpainted-circle paths are the *arrival* ones: `roomSpawn`
  (`room_manager.go:296`) and `transferPlayer` (`room_manager.go:600`) put a
  stat on a board with no surroundings draw. So a player who lights a torch
  and then takes the passage into a dark room (the normal TOWN vault flow)
  sees only the moving delta-ring trail — the reported symptom.
  **Write the failing test first**: via `RoomManager`, a torch-lit player
  (`PlayerState.TorchTicks > 0`) transfers into TOWN's dark board (find it as
  `m4_5_test.go:144` does), and separately joins it fresh; assert every board
  tile within `TORCH_DIST_SQR` of the player renders lit — not the dark shade
  `0x07`/`'\xb0'` (`game.go:320`) — in that room's Screen/snapshot. Then fix:
  on arrival into a dark board, call `DrawPlayerSurroundings(x, y, 0)` for the
  arriving stat, mirroring the respawn paths. While there, note in NOTES.md
  (do not fix here) the multiplayer wrinkle: `TileToColorAndChar`
  (`game.go:302-304`) lights each tile from the single *nearest* player, so
  with two players in one dark room only the nearer one's torch counts.
  DoD: new tests fail before the fix, pass after; `go test ./...` green;
  replay fixture unchanged (the 600-step fixture never leaves Room One).

- [ ] **M7.3 — Fix the pending-input data race (NOTES.md 2026-07-10).**
  `Engine.SubmitScrollReply` (`game.go:2298`) appends to
  `PendingScrollReplies` from the WebSocket goroutine while
  `GameStepWithInputs` (`game.go:1597-1633`) reads and truncates the same
  slice from the tick goroutine; `WorldInstance.mu` and `s.mu` never exclude
  each other. Same shape: `PendingDebugCommands` (`game.go:1237`),
  `PendingSaveFilenames` (`game.go:1249`), `PendingQuitReplies`
  (`game.go:1622`) — audit `gamevars.go:187-206` for any sibling added since.
  Fix: one `sync.Mutex` on `Engine` guarding exactly those reply slices; the
  `Submit*` methods lock it; the drain at the top of `GameStepWithInputs`
  swaps each slice out under the lock into locals and processes them unlocked,
  so the sim never runs holding it. Presentation/networking only: no sim-state
  change, the mutex must not enter `StateHash` or serialization, and `go vet`
  must stay clean (copylocks — Engine must never be copied by value).
  DoD: `go test -race -run TestWebSocketServerScrollReplyBuysFromVendor ./...`
  passes (it fails on a clean tree today, verified 2026-07-10); run the full
  `go test -race ./...` and record in NOTES.md if any *other* race remains;
  replay fixture unchanged.

- [ ] **M7.4 — Per-player sound attribution.** Every client in a room hears
  every player's pickups/shots/damage — accepted in M4.4 ("sound events are
  room-wide", NOTES.md), now superseded. Add `StatId int16` to `SoundEvent`
  (`gamevars.go:308`), `-1` = room-wide. Engine gains a presentation-only
  `ActingPlayerStatId int16` (init/reset `-1`): set it in
  `GameStepWithInputs`' stat loop while the ticked stat is an `E_PLAYER`
  (`game.go:1656-1694`, restore after that stat's tick — touch procs for
  pickups/doors/keys run inside it), and around `DamageStat` (`game.go:1271`)
  when the damaged stat is a player. `SoundQueue` (`sounds.go:28`) stamps it
  into the event. It must never influence a simulation decision — grep-proof
  that only `SoundQueue` reads it. Routing in `RoomManager.StepDiffs`
  (`room_manager.go:407-434`): `StatId >= 0` resolves via `playerIDForStat` →
  `pendingPlayerEvents` for that player only; `StatId < 0` stays in
  `roomEvents`. Fix the TransferEvent double-path at `room_manager.go:411-415`
  — the passage sound currently goes to both the whole room and the traveller;
  make it traveller-only. Sounds emitted during an object's own tick (`#play`)
  keep `-1` and stay room-wide; record that decision in NOTES.md.
  DoD: headless two-player test — A steps on a gem: the gem sound reaches only
  A; an object `#play` reaches both; `go test ./...` green; replay fixture
  unchanged (events are not hashed).

- [ ] **M7.5 — Next batch of ZZT games.** Requires M7.1 (joiners must land on
  authors' start squares — fake-wall floors are common in these worlds). The
  server already discovers hostable worlds by scanning a directory for `.ZZT`
  (`ListWorlds`, `web_api.go:340-351`) and lists them at `/api/worlds`; the
  M6.1 world picker consumes that. Add: (1) a fetch script (or small Go cmd)
  that downloads and unpacks the requested titles from the Museum of ZZT
  (museumofzzt.com) into the worlds directory — Teen Priest, Teen Priest 2,
  Inedible Vomit, the other bongo/wynand games, Freedom, Apparitions of the
  City (kev-san); (2) a validation gate: for each fetched world, headless
  `WorldLoad` + 200 `GameStep`s with no panic and a non-empty board render,
  as a table-driven test or `cmd/zzt-validate`; (3) a deployment note in
  README/AWS.md. The repo carries the script and a manifest, not the `.ZZT`
  files themselves (they are gitignored like other worlds; licenses vary).
  DoD: the script fetches whichever named titles the Museum actually hosts,
  validation passes for each, and the world picker lists them.

## M5 — Creation and full-featured ZZT tooling

Goal: support the creation features that make ZZT “full ZZT,” not only runtime
playback. The machine conversion kept the whole modal terminal editor
(`EditorLoop`, `editor.go:39-818`) except `EditorTransferBoard` (a TODO stub,
`editor.go:422`); it and `reference/reconstruction-of-zzt/SRC/EDITOR.PAS` are
the semantic reference for every task below. The browser editor is a new
protocol surface over a server-side, never-ticked session world (M5.0 decides
the model). CLAUDE.md rules apply unchanged: port semantics faithfully, keep
the sim untouched, replay green.

(2026-07-10: broken down from four coarse tasks into M5.0–M5.7, then pulled
forward ahead of M8/M9/M6 by owner priority — creation tools should be up
and running early. See NOTES.md.)

- [ ] **M5.0 [ADVISOR] — Editor session model and read-only shell.** Decide
  and build the session model before any editing lands: an editor session is
  a server-side copy of a world (a fresh `WorldCreate` or a loaded `.ZZT`),
  owned by exactly one client, never ticked, and invisible to live rooms —
  the `TitleSim` precedent (`title_sim.go` runs an isolated engine on a
  copied world) is the shape to follow. Protocol: `editorEnter{world}` /
  `editorExit`; reuse the existing snapshot/diff shape for the board render;
  the cursor is client-local. Read-only v1: cursor movement plus a
  tile-inspect readout matching `EditorDrawSidebar`/`EditorUpdateSidebar`
  (`editor.go:56-145`, `EDITOR.PAS:89-186`) — element name, color,
  coordinates, and P1/P2/P3 for a stat under the cursor.
  **Forward-compatibility (M10):** even though v1 is single-editor, give the
  session a *member list* (capped at one for now) rather than a single owner
  field, and route every future mutation through one serialized apply path on
  the session — collaborative editing (M10) must be "raise the cap and fan
  out diffs", never a rewrite of the session model. The advisor consult for
  this task must cover that shape.
  DoD: a browser opens a world in editor mode, moves the cursor, and reads
  tile info; live games are unaffected; protocol tests; `go test ./...` and
  replay green.

- [ ] **M5.1 — Tile placement, patterns, colors, drawing, flood fill.** Port
  the interaction semantics of `EditorPlaceTile` / `EditorSetAndCopyTile` /
  `EditorPrepareModifyTile` (`editor.go:146-203`, `EDITOR.PAS:187-246`): the
  pattern row and color selector as `EditorDrawSidebar` presents them,
  draw-mode toggle, copy-tile pickup (Enter grabs the tile under the cursor
  into the pattern slot), and `EditorFloodFill` (`editor.go:480`,
  `EDITOR.PAS:588`). Edits travel client → protocol → session world on the
  server; the render returns through the normal diff path. Preserve vanilla's
  rules for which elements take the selected color versus their fixed color,
  and what placing over an existing stat does.
  DoD: place, erase, draw-drag, and flood fill work in the browser; edits
  round-trip through `BoardClose`/`BoardOpen` (save the session, reload it);
  protocol-level tests.

- [ ] **M5.2 — Board and world property dialogs.** `EditorEditBoardInfo`
  (`editor.go:204-288`, `EDITOR.PAS:247-351`): board title, max player shots,
  dark, the four exits (targets picked via `EditorSelectBoard`,
  `editor.go:866`), reenter-when-zapped, time limit; plus the world name.
  Build the dialogs on the M4.1 window system.
  DoD: set dark, a time limit, and an exit in the browser editor; save; play
  the world in a live room — all three take effect.

- [ ] **M5.3 — Stat parameter editing.** `EditorEditStat` /
  `EditorEditStatSettings` (`editor.go:315-417`, `EDITOR.PAS:396-527`):
  per-element parameter dialogs — P1/P2/P3 with their
  `ParamTextName`/`Param1Name`/`Param2Name`/`ParamDirName`/`ParamBoardName`
  meanings from `ElementDefs`, step/direction pickers, cycle, and vanilla's
  bind behavior for objects. Centipede Follower/Leader stay untouched by the
  dialog, as in vanilla.
  DoD: edit a spinning gun's firing rate, a passage's destination board, and
  an object's cycle in the browser; each behaves accordingly in play.

- [ ] **M5.4 — Object code editor.** `EditorEditStatText` /
  `EditorOpenEditTextWindow` (`editor.go:289-314,819-835`,
  `EDITOR.PAS:352-395`): a CP437 multi-line text editor in the browser (build
  on the M4.1 window layer and the M6.1 input-capture mode) for object and
  scroll text, preserving vanilla's line/`@name`/`#`/`:label` conventions and
  the `DataLen` bookkeeping on save.
  DoD: rewrite the TOWN vendor's script in the browser, save, play — the new
  dialogue runs; the text round-trips through the serializer.

- [ ] **M5.5 — Board management and transfer.** `EditorAppendBoard` /
  `EditorSelectBoard` (`editor.go:21-38,866-889`, `EDITOR.PAS:51-70`) for
  adding, switching, and naming boards — and port the one dropped procedure:
  `EditorTransferBoard` (TODO stub at `editor.go:422-479`,
  `EDITOR.PAS:528-587`) as browser import/export of a single board (file
  download/upload of the vanilla board format; names through
  `SanitizeSaveName`).
  DoD: create a second board, link exits both ways, walk between them in
  play; export a board and re-import it into another world.

- [ ] **M5.6 — Save, host, and publish edited worlds.** Save the session
  world under a new name (`SanitizeSaveName`, bytes via `worldWriteTo`) into
  the hosted worlds directory so `ListWorlds` (`web_api.go:340-351`) and the
  world picker see it, with a collision policy — no silent overwrite of a
  world anyone is playing (follow `RestoreSnapshot`'s occupancy refusal,
  M4.3a). Include `.ZZT` download (export) and upload, gated by the M7.5
  validation (headless load + 200 steps, no panic).
  DoD: a world built in the browser editor saves, appears in the picker, and
  a second client joins and plays it; uploading a hand-made `.ZZT` works; a
  traversal filename is rejected, with a test.

- [ ] **M5.7 — ZZT-OOP authoring aids.** Label navigation and validation in
  the code editor: use the real tokenizer semantics (`OopParseWord` and
  friends in `oop.go` — do not write a second parser) to list `:labels`,
  flag `#send`s to labels that don't exist in the target object, and warn on
  unknown `#commands`. Purely advisory — never block a save; vanilla accepts
  anything and worlds rely on that.
  DoD: the label list and warnings render for the vendor object; a send to a
  missing label warns; saving an "invalid" script still succeeds.

## M8 — Vanilla parity: simulation fidelity

Goal: close the gaps where the multiplayer generalization (M2.x) drifted from
vanilla per-player semantics. Parity audit 2026-07-10 (see NOTES.md): the
element tick/touch surface, the full ZZT-OOP command set, per-player time
limits, flash messages, passage-arrival pause, energizer, cheats, and high
scores are all complete and replay-guarded; the tasks below are the remainder.

- [ ] **M8.1 — Point-blank shots must respect the *target* player's
  energizer.** `BoardShoot` (`game.go:1402-1420`) guards its damage branch
  with `e.PlayerFor(0).EnergizerTicks <= 0` (`game.go:1411`): whichever player
  stands in front of the shooter, the check reads player 0's energizer.
  Vanilla (`GAME.PAS` BoardShoot) reads `World.Info.EnergizerTicks` — the one
  player's — so the correct M2.1-style generalization is the player *on the
  target square*. Fix: when the target tile is `E_PLAYER`, resolve the stat
  via `StatAt` (`placement.go`) and use that player's `EnergizerTicks`; leave
  the rest of the condition byte-for-byte (its `A || B && C` precedence is
  original — do not "fix" it). While here, reconcile with M2.4's "player
  bullets don't damage players": the branch fires when
  `(Element == E_PLAYER) == (source >= SHOT_SOURCE_PLAYER_BASE)`, which reads
  as *a player point-blanking a player CAN hurt them*, contradicting
  BulletTick's ownership rule. Decide (recommended: point-blank follows the
  same no-PvP rule) and record the decision in NOTES.md.
  DoD: headless test — A shoots point-blank at energized B (no damage), at
  un-energized C (outcome per the PvP decision), and a creature shoots an
  energized nonzero-stat player (no damage); `go test ./...` green; replay
  fixture unchanged (single player: PlayerFor(0) *is* the target).

- [ ] **M8.2 — Sweep the remaining single-player assumptions.** M8.1's bug was
  found by grep, not luck; finish the sweep. For every `PlayerFor(0)`,
  `Stats[0]`, and `PlayerDir` read in sim files (`elements.go`, `game.go`,
  `oop.go`), classify it: (a) terminal-wrapper or title-screen only
  (`GamePlayLoop`, `GameTitleLoop`, sidebar draw — fine), (b) world-create or
  init before any join (fine), or (c) reachable from `GameStepWithInputs` in
  a multi-room engine (fix it, following M4.3's HelpEvent precedent). One
  known (c): `ResetMessageNotShownFlags` (`elements.go:1505-1518`) resets hint
  flags for player 0 only — make it cover every entry in `e.Players`
  (spawning players are already handled by `ResetPlayerState`,
  `gamevars.go:479`). Deliverable: the fixes plus a classification table
  appended to NOTES.md so the next audit starts from evidence.
  DoD: the table covers every grep hit; a test per (c) fix;
  `go test ./...` green; replay fixture unchanged.

## M9 — Vanilla parity: presentation

Goal: the browser should look like ZZT even between boards. Client-side only —
no protocol message changes, no simulation changes.

- [ ] **M9.1 — Board-change transition fade.** Vanilla covers every board
  change with `TransitionDrawBoardChange` (`game.go:1448-1456`): fill the
  60x25 viewport with purple `'\xdb'` cells in `TransitionTable` random order,
  then reveal the new board in the same order. The terminal path still does
  this; the browser cuts instantly on a `boardChange` snapshot. Implement it
  client-side in `web/src/main.ts`: on `boardChange`, overlay the board area,
  fill cells in a locally shuffled order, then reveal the freshly applied
  snapshot in that order over roughly vanilla's duration. Presentation only:
  local `Math.random` is fine (CLAUDE.md rule 2 governs simulation, not the
  client); diffs arriving during the animation must not be lost or painted
  over its final state.
  DoD: passage and edge transfers show fill-then-reveal in the browser; a
  node-driven TS test (the M4.3a pattern) covers the order and
  complete-reveal logic; `go test ./...` untouched and green.

- [ ] **M9.2 — Title-screen About and menu completeness.** Vanilla's title
  monitor accepts `A` to show ABOUT.HLP through the help viewer; the browser
  title (M4.3) has start/restart, `W`, `R`, quit, and high scores, but no `A`.
  Wire it through the existing `HelpEvent`/help-window path (M3.9) — the file
  already ships (`engine/ABOUT.HLP`). While there, compare the browser title
  key set against `ElementMonitorTick`'s vocabulary (`elements.go:1498-1503`)
  and vanilla's title loop in `GAME.PAS`; the deliberate omissions stay (`S`
  game speed — the server owns the tick; `E` editor — M5), and every other
  missing key is either wired or recorded in NOTES.md as omitted on purpose.
  DoD: `A` on the browser title opens the About window, with a protocol-level
  test; `go test ./...` green.

## M6 — Chat and identity

Goal: players in a server can talk to each other in a ZZT-styled chat box, and
eventually do so under a real account name.

**Hard constraint:** chat is presentation and networking only. No chat state may
enter the simulation — not the `Engine`, not `StateHash`, not the replay path.
Chat lives beside the engine in the server (`RoomManager`/`WebSocketServer`), so
determinism (CLAUDE.md rule 2) and the replay fixtures are unaffected.

(2026-07-10, later same day: M5 moved back ahead of this section by owner
priority — the editor comes early after the M7 fixes. M6.2 is still the
gate for M6.4 and M10.3. See NOTES.md.)

- [x] **M6.0 — Chat protocol and server relay.** Add `chatSend` (client→server)
  and `chat` (server→client) protocol messages carrying `{from, text, ts}`.
  Server relays each message to every connected client, scoped server-wide (not
  per-board) so a lobby-style conversation works across rooms. MVP identity is
  the existing `PlayerID` rendered as `Player 3`; the `JoinMessage.Name` field
  already exists and may override it. Enforce a max length, strip control bytes
  and any codepoint with no CP437 mapping, and rate-limit per player. DoD:
  protocol round-trip test plus a two-client test where one client's message
  reaches the other, and `go test ./...` replay stays green (chat touches no
  engine state).

- [x] **M6.1 — ZZT-style chat window.** Render chat in the browser as a CP437
  text panel drawn with the same cell renderer as the board and sidebar — DOS
  colors, ZZT window chrome, no HTML-widget styling. Lines are IRC style:
  `<Player 3> hello there`, with the `<name>` in a distinct DOS color from the
  message body. Include a scrollback buffer, a typing line, and an input mode
  that captures keys so typing never leaks into movement (reuse the M4.1 text
  window input router once it exists; before that, a local mode flag). DoD: two
  browsers on one server exchange visible messages; pressing the chat key opens
  the input line and the player does not move while typing.

- [ ] **M6.2 — Google OAuth authentication.** Replace MVP player numbers with
  real accounts via Google OAuth 2.0 (Authorization Code + PKCE). Server
  verifies the ID token, derives a stable account id, and maps it to a display
  name used by chat and any future roster UI. Sessions ride a signed cookie or
  token presented at WebSocket join; unauthenticated players may still play as
  `Player N` guests. Keep secrets out of the repo (env vars). DoD: a user signs
  in with Google, their display name appears in chat instead of `Player N`, and
  a guest can still join.

- [x] **M6.3 — Chat and account persistence.** Add a backend database (accounts,
  display names, chat history) behind a narrow storage interface so tests can
  use an in-memory implementation. Persist chat with timestamps; serve recent
  scrollback to a joining client. DoD: chat history survives a server restart,
  and the storage interface has an in-memory fake used by tests.

- [ ] **M6.4 — Account-keyed player-state persistence.** Requires M6.2.
  Vanilla parity gap deferred by M4.3a: restoring a save returns *your*
  health/ammo/keys, but fork snapshots drop players — `World.Info` can hold
  only one player's counters, so N players cannot round-trip through the
  vanilla file format. Once accounts exist, persist each player's
  `PlayerState` keyed by account id in the M6.3 storage interface: write it
  on disconnect and alongside `SaveSnapshot` (a sidecar JSON next to the
  `.SAV` — never change the vanilla file format), and restore it when that
  account joins the world (set the fields on the freshly spawned
  `PlayerState` before its first tick, the way `ResetPlayerState`,
  `gamevars.go:479`, does). Guests keep joining fresh. Decisions to record in
  NOTES.md: state is scoped per-(account, world) vs per-(account, snapshot),
  and what happens when one account has two concurrent sessions.
  DoD: a signed-in player collects keys, disconnects, the server restarts,
  they rejoin with the keys intact; a guest joins fresh; `go test ./...`
  green; replay fixture unchanged (restore happens outside the sim, before
  the first tick).

## M10 — Collaborative world editing (horizon: spec in detail after M5.5)

Goal: several builders in one editor session, co-building a world live — the
editor equivalent of what M2/M3 did for play. These tasks are deliberately
coarse; they get M7-style specs (file:line cites, DoD) once M5's session
model exists in code. Design pillars, decided 2026-07-10 (NOTES.md) so M5
builds toward them instead of away:

* **Same authority model as play.** The server owns the session world;
  clients send edit *operations*; the session applies them in arrival order
  through M5.0's single serialized apply path and fans out cell diffs to all
  members. Tile edits are last-write-wins per cell. No CRDTs/OT — ZZT boards
  are 60x25 and objects are tiny; arrival order plus leases is enough.
* **Leases for non-commutative surfaces.** A stat dialog, object-code
  editor, or board-info dialog takes an exclusive per-stat/per-board lease
  for its duration; a second member gets "being edited by <name>". The
  `scrollOpen` freeze (M6.1, `room_manager.go`) is the existing pattern.
* **Sessions still never tick, and publishing (M5.6) stays the only bridge
  to hosted play.** Live-editing a world people are playing in is out of
  scope at this horizon — stat reindexing under a running sim is exactly the
  bug class M2/M4.3b spent tasks killing.
* **Undo is per-user over their own ops, or absent (vanilla has none).**
  Global undo with N editors is incoherent; decide at spec time, record in
  NOTES.md.

- [ ] **M10.1 — Multi-member sessions.** Requires M5.0-M5.1. Raise the member
  cap; broadcast session diffs to all members; presence — each member's
  cursor rendered in a distinct DOS color with their name (M6.2 accounts, or
  `Player N` for guests). DoD: two browsers place tiles in one session and
  each sees the other's edits and cursor.
- [ ] **M10.2 — Edit leases.** Requires M5.3-M5.4. Exclusive per-stat and
  per-board leases around dialogs and the code editor, released on close or
  disconnect, with the "being edited by" refusal surfaced in the client.
  DoD: two members racing for one object — one edits, the other is refused
  and sees who holds it; a disconnect releases the lease.
- [ ] **M10.3 — Ownership and invites.** Requires M6.2 and M5.6. Worlds have
  an owning account; the owner invites collaborator accounts; everyone else
  is read-only in that session. DoD: an invited account edits, an uninvited
  one can look but not touch.
- [ ] **M10.4 — Co-op test play.** A session member starts a test run: the
  server spins a private, ticking room from a *copy* of the session world
  (the `TitleSim` isolation pattern) that any member may join, leaving the
  session itself untouched and un-ticked. DoD: two members test-play their
  edit together, exit, and the session world is byte-identical to before the
  run.

## Future Tasks & Community Additions

(2026-07-10: every unchecked item that lived here — spawn point, torch
illumination, sound broadcast, next games batch — was promoted to M7 above
with a full spec. New community reports land here first, then get specced
into a milestone.)

- [x] **Troubleshoot player stuck after damage.** Solve the issue where players get stuck after being zapped/damaged by a ruffian/bear due to stat index shift misalignment in RoomManager.
- [x] **Title screens aren't animating properly.** Investigate and resolve the issue where object scripts and movements on ZZT title screens do not animate or tick as they should. *(Done: `engine/title_sim.go` runs board 0 on an isolated engine — its own copied world, never written back — ticked from the server loop only while a browser is watching, with changed cells pushed over `/api/title/stream` as SSE.)*
