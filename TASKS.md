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

- [x] **M7.1 [URGENT] — Spawn at the vanilla start point, not the nearest blank
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

- [x] **M7.2 — Torch light must arrive with the player in dark rooms.**
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

- [x] **M7.3 — Fix the pending-input data race (NOTES.md 2026-07-10).**
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

- [x] **M7.4 — Per-player sound attribution.** Every client in a room hears
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

- [x] **M7.5 — Next batch of ZZT games.** Requires M7.1 (joiners must land on
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

## M12 — LLM world creator (owner priority 2026-07-10: first feature after M7)

Goal: type a prompt, get a playable, *good-looking* ZZT world. Architecture
decision (see NOTES.md): the LLM never emits `.ZZT` binary. It writes **ZWD**
("ZZT World Description") — a textual format with per-board ASCII-art grids,
a legend, board/world properties, and stats with inline ZZT-OOP — and a Go
compiler produces the real world through the existing structs and
`worldWriteTo`. A matching decompiler turns existing worlds INTO ZWD, which
yields few-shot style examples from real games, round-trip tests, and repair
context. Generation is server-side (Claude API, key via env — never in the
repo), fully outside the sim: the output is just world bytes, so determinism
and replay are untouched.

- [x] **M12.0 [ADVISOR] — Design the ZWD format.** A spec document
  (`ZWD.md`, repo root) defining: world header (name, flags off-limits);
  per-board sections — name, properties (dark, exits by board name, time
  limit, max shots, reenter), a 60x25 grid of legend characters, a legend
  mapping char → element + fg/bg DOS color, and a stats list (`at X,Y`,
  element, cycle, P1-P3/step by *name* per `ElementDefs`, `Under`, and
  fenced ZZT-OOP code blocks). Ground every field in `fileformat.html` and
  the structs in `gamevars.go:60-135`; write the limits table (≤150 stats,
  board RLE ≤~20000 bytes, OOP length, 16 colors, one player start) — the
  compiler will enforce it, the generation prompt will quote it. Include
  two hand-written example boards in the doc. DoD: the doc; a reviewer can
  hand-write a board from it; no code yet.

- [x] **M12.1 — ZWD compiler.** `zwd.go` (+`zwd_test.go`): parse ZWD →
  populate `TWorld`/`TBoard`/`TStat` exactly as `serialize.go` expects →
  bytes via `worldWriteTo` (`game.go`, the M4.3a seam). Errors carry
  line/column and say what's legal ("board 2, row 7, col 61: grid row wider
  than 60") — they are the repair-loop's food, so precision is the feature.
  Enforce every M12.0 limit; reject unknown elements/params rather than
  guessing. DoD: both M12.0 example boards compile; the compiled world
  passes the M7.5 gate (headless `WorldLoad` + 200 steps); property-based
  test: no ZWD input may panic the compiler.

- [x] **M12.2 — ZWD decompiler + round-trip.** `TWorld` → ZWD text, picking
  legend chars sensibly (element defaults first, then digits). DoD:
  TOWN/CAVES/CITY decompile → recompile → reload, and board tiles, stats,
  properties, and OOP survive semantically (StateHash of a fresh load
  matches after 0 steps); the decompiled TOWN board 1 is committed as a
  fixture and doubles as documentation.

- [x] **M12.3 — Style corpus and prompt kit.** The raw material landed
  2026-07-10: `llmworld/examples/` (200 decompiled boards from ~100 Museum
  games) and `llmworld/STYLE.md` (the distilled idiom analysis: composed
  scenes, gray-family shading, fake-wall floors, monumental text lettering,
  the three OOP rituals). Remaining work: assemble the generation system
  prompt from STYLE.md + the M12.0 limits table verbatim + 3-5 few-shots
  hand-picked from `examples/` for archetype spread (action arena, interior
  scene, texture showcase, story board — CUTLASS_board27, ONAMOON_board19,
  SEWERS_board17, OBELISK_board59 are strong candidates), loadable from Go.
  Proof of the register already exists: `llmworld/generated/MOSSGATE.zwd`
  was authored from the corpus idioms and compiled + validated first try
  (`gen_generated_test.go`). DoD: prompt kit loads from Go; a documented
  manual run against a real LLM produced at least one compiling world
  (record the transcript path in NOTES.md).

- [x] **M12.3a [ADVISOR] — World planner (plan-then-paint, phase 1).** A
  whole game cannot be one-shot (a single dense board is 8-65KB of ZWD) and
  independently generated boards cannot cohere (exits, keys, theme). So
  generation is two-phase: the LLM first emits a compact **world plan** —
  premise, palette rules, board table (id, name, one-line concept, dark,
  exits/links), progression spine (which keys/doors/flags/passages gate
  what, in order), and generation order (hub-first) — and only then are
  boards painted one at a time against that plan.
  `llmworld/plans/LASTLITE.md` is the reference plan and format exemplar.
  This task builds the plan **validator**: parse the board table and spine,
  then check mechanically — board count within limits, exit reciprocity
  (A→E→B implies B→W→A), passage targets exist, graph connected from the
  start board, and the spine solvable (walking the graph in spine order,
  every key/flag is reachable before the door/gate it opens, the finale is
  reachable). Bad plans fail with precise errors (the planner's repair
  food, same philosophy as M12.1). Tests: LASTLITE.md passes; a plan with
  an orphan board, a key behind its own door, and a missing passage target
  each fail with the right message. No LLM call in this task.

- [x] **M12.4 — Board-by-board generation service (plan-then-paint, phase
  2).** Server endpoint `/api/generate` (pattern of `web_api.go`): prompt in
  → Claude API call for the world plan (model/key/max-tokens via env; refuse
  politely if unset) → M12.3a plan validation, with plan-level repair on
  precise errors → then for each board in the plan's generation order: one
  LLM call with system prompt + few-shots (identical across calls — use
  prompt caching) + the world plan + the **edge rows of already-generated
  adjacent boards** (so paths meet across board seams) → extract ZWD →
  M12.1 compile → per-board M7.5 validate → feed compiler/validator errors
  back for up to K repair attempts (K=3 default; a failed board costs one
  board's regeneration, never the world's) → assemble all boards into one
  ZWD document → full-world compile → cross-board checks (exit reciprocity,
  passage targets, the spine's promised keys/flags actually placed —
  regenerate the offending board with the omission named in the error) →
  name via `SanitizeSaveName`, host as an instance like M11.1 does, return
  the world name. Rate-limit per client and cap concurrent generations;
  persist accepted worlds plus their prompt+plan+ZWD sidecars. Tests use a
  faked LLM (httptest): success, plan-repair-then-success, board-repair-
  then-success, exhausted-repairs, spine-omission-caught, and injection
  resistance (a prompt that tries to escape ZWD is just bad ZWD — the
  compiler is the security boundary; nothing from the LLM is ever executed,
  only compiled). DoD: all paths tested; `go test ./...` green; replay
  unchanged.

- [x] **M12.5 — Browser "Dream a world" flow.** A CP437 window (M4.1 style)
  from the title/world-picker: multi-line prompt entry (M6.1 input capture),
  a progress state that narrates the plan-then-paint pipeline ZZT-style
  ("Imagining the world...", then per-board "Painting board 7 of 12: The
  Tide Cellar", with repair attempts shown as "attempt 2 of 3"), failure
  window on give-up, and on success the standard join flow into the new
  world, which also appears in the picker for everyone. DoD: scripted
  client against the faked LLM goes prompt → playing with per-board
  progress events; failure path renders its window; node-driven TS tests.

## M12 cleanup — Known errors (deliberately after the MVP)

Bugs and quality gaps discovered during M7.5/M12 that do NOT block the
plan-then-paint MVP (M12.4 uses the forward compile path, which is green —
MOSSGATE and ARCHIVE compile + validate first try). Positioned here on
purpose: owner priority (2026-07-10) is to get a fully AI-created world
generated and played end-to-end ASAP, then pay these down. The executor
protocol is positional, so these sit just after M12.5 and before M5.

- [x] **M12.6 — Decompiler emits only compiler-legal ZWD.** `DecompileZWD`
  (`zwd_decompile.go`) currently emits tokens the compiler rejects, so a
  decompiled board is not guaranteed to recompile (a corpus scan found only
  45/200 examples recompile cleanly, and the few-shot picks ONAMOON_board19 /
  OBELISK_board59 had to be swapped out in M12.3 for this reason). Known
  offenders: (1) nameless raw elements written as `element 33` / `element 43`
  for element ids whose `ElementDefs[N].Name == ""` — the compiler's
  `elementByZWDName` (`zwd.go:871`) refuses them; decide whether to map them to
  a safe drawable element, skip them, or extend the legend grammar. (2)
  Off-board stat coordinates written as `respawn 98,98` and `at 0,0` centipede
  sentinels — the compiler's coordinate check (`1..60,1..25`) rejects them;
  either drop off-board stats on decompile (as the corpus run already drops
  (0,0) sentinels — STYLE.md §4) or teach the format to carry them. DoD: the
  clean-recompile rate over `llmworld/examples/` rises markedly (target: all
  200, or a documented list of genuinely un-expressible boards); a test
  compiles every decompiled example (wrapped as a one-board world, exits
  neutralized) and asserts the pass count; `go test ./...` green.

  Re-scoped (2026-07-11): `DecompileZWD` returns authorable source or an empty
  result; `DecompileZWDAuthorable` additionally returns structured warnings
  for safe lowerings and errors for non-representable worlds. The corpus
  generator uses that boundary and skips rejected worlds. A separate forensic
  format is future work; it must not be presented as compilable ZWD.

- [x] **M12.7 — Green the three ZWD round-trip tests (or re-scope them
  honestly).** `TestZWDRoundTrip{TOWN,CAVES,CITY}` (`zwd_decompile_test.go`)
  have been committed red since M12.2. NOTES.md (2026-07-10 M7.5) diagnoses the
  StateHash mismatches as systematic, not per-world corruption: `World.Info`
  `CurrentBoard` and `Flags` that ZWD cannot express by design, `StartPlayerX/Y`
  defaulting, and dropped (0,0) centipede sentinels. Requires M12.6 for the
  legal-token half. Then choose per field: express it in ZWD (e.g. teach the
  format a world-level `current-board`, accept that `Flags` stay unauthored),
  OR narrow the round-trip `StateHash` to exactly the board/stat/OOP fields ZWD
  is defined to preserve and document each by-design exclusion. The three tests
  must end green with no `known-red baseline` — the safety net cannot include a
  test the repo ships failing. DoD: `go test ./...` fully green; NOTES.md states
  which fields are preserved vs excluded and why; replay fixture unchanged.

- [x] **M12.8 — Fix `cmd/zzt-validate`'s "board render is empty" false
  negatives.** The M7.5 validation command reports "board render is empty" for
  50 of 108 worlds that load fine via `WorldLoad` in the corpus run
  (`cmd/zzt-validate/main.go`, NOTES.md 2026-07-10 M7.5 "Open"). It is a harness
  bug, not a world defect. Because M12.4 runs a per-board M7.5 validate in its
  repair loop, a false "empty render" would make the generator reject good
  boards — so confirm M12.4 uses the working `validateCompiledZWD`
  (`gen_generated_test.go`) path, not this broken one, and fix the standalone
  command to agree with it (likely a Screen/VideoInstall or headless-render
  ordering bug). DoD: `zzt-validate` passes the same worlds the corpus run
  loads; a test covers a known-good world that currently false-fails.

- [x] **M12.9 — Enforce non-stat-backed elements are excluded from stats in compiler validation.** `zwd.go` now rejects a `stats` entry for elements such as Gem, Ammo, Key, or Door with a precise diagnostic.

- [x] **M12.10 — Prevent LLM from using stat-backed Object elements for static art.** The authoritative ZWD spec and embedded generation prompt now require Objects to be interactive and direct decorative shapes to Text, Solid, Normal, or Fake tiles.

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

- [x] **M5.0 [ADVISOR] — Editor session model and read-only shell.** Decide
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

- [x] **M5.1 — Tile placement, patterns, colors, drawing, flood fill.** Port
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
  M4.3a). Include both directions of file transfer: **download** — a browser
  button that serves the session world as a real `.ZZT` (bytes from
  `worldWriteTo`, so the file is vanilla-format and loads in DOS ZZT/zeta and
  in this engine alike; creators own their work as a portable file) — and
  **upload**, gated by the M7.5 validation (headless load + 200 steps, no
  panic).
  DoD: a world built in the browser editor saves, appears in the picker, and
  a second client joins and plays it; the downloaded `.ZZT`'s bytes reload
  through `WorldLoad` with board contents intact (round-trip test); uploading
  a hand-made `.ZZT` works; a traversal filename is rejected, with a test.

- [ ] **M5.7 — ZZT-OOP authoring aids.** Label navigation and validation in
  the code editor: use the real tokenizer semantics (`OopParseWord` and
  friends in `oop.go` — do not write a second parser) to list `:labels`,
  flag `#send`s to labels that don't exist in the target object, and warn on
  unknown `#commands`. Purely advisory — never block a save; vanilla accepts
  anything and worlds rely on that.
  DoD: the label list and warnings render for the vendor object; a send to a
  missing label warns; saving an "invalid" script still succeeds.

## M11 — Museum of ZZT: search and play anything

Goal: from the browser, search the Museum of ZZT's archive of community
worlds, pick one, and be playing it moments later — the server fetches,
validates, and hosts on demand. Composes M7.5's fetch/validate pipeline with
M5.6's hosting path. Positioned right after M5 by owner priority.

- [ ] **M11.1 — Server-side Museum client: search, fetch, validate, host.**
  A Go client for the Museum of ZZT's public JSON API (museumofzzt.com — read
  the current API docs at implementation time rather than trusting memory;
  they document search and file endpoints). Server endpoints:
  `/api/museum/search?q=` proxying title/author/genre search (the browser
  must not call the Museum directly — CORS, and we want one polite,
  cached consumer), and `/api/museum/play` which downloads the selected
  release zip, extracts its `.ZZT` file(s), maps names through
  `SanitizeSaveName` (Museum filenames exceed vanilla's charset), runs the
  M7.5 validation gate (headless `WorldLoad` + 200 steps, no panic), and
  registers the world for hosting exactly as `ListWorlds`/`Instances` worlds
  are. Courtesy constraints are part of the task: cache downloads on disk
  (re-plays must not re-fetch), rate-limit outbound Museum calls, send an
  identifying User-Agent, and never commit fetched worlds to the repo
  (gitignored like other `.ZZT`s). Multi-`.ZZT` zips: list the contained
  worlds and let the client pick. Zeta-only or Super ZZT files are rejected
  with a clear error, not a panic.
  DoD: an httptest-faked Museum API drives search → fetch → validate → host
  in a Go test; a corrupt zip and a traversal filename are rejected with
  tests; a second play of the same world hits the cache; `go test ./...`
  green; replay fixture unchanged (nothing touches the sim).

- [ ] **M11.2 — Browser Museum search window.** A CP437 search UI on the
  title screen (a new key in the monitor menu, e.g. `M`), built from the
  M4.1 window system: a text-entry line (the M6.1 input-capture mode), a
  scrollable result list (title, author, year — whatever the search proxy
  returns), and selection → `/api/museum/play` → a "fetching…" state → join
  the newly hosted world through the existing world-picker flow. Failures
  (Museum down, validation rejected) render as a ZZT-style message window,
  never a dead client.
  DoD: with the Go test's faked Museum, a scripted client searches, picks a
  result, and ends up joined to the fetched world; the not-found and
  validation-failure paths render their windows; node-driven TS tests for
  the window logic; `go test ./...` green.

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

- [ ] **M10.1 — Multi-member sessions with live cursors.** Requires
  M5.0-M5.1. Raise the member cap; broadcast session diffs to all members.
  The feel is Google-Docs-in-CP437: everyone draws on the board *at the same
  time* — no turn-taking, no canvas locks (M10.2's leases cover modal
  dialogs only, never the board surface). Presence: each member's cursor
  position streams continuously (a low-rate presence message, not only on
  edits) and renders in a DOS color pinned to that member for the session,
  labeled with their name (M6.2 account, or `Player N` for guests); own
  cursor stays local-echo so editing never waits on the server round trip.
  DoD: two browsers draw simultaneously in one session, each sees the
  other's tiles appear live and the other's named, colored cursor move
  between edits.
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

- [ ] **Rewrite the launch text.** The copy at launch is weak: the name prompt
  ("Welcome to ZZTMMO!  Enter your name:", `web/src/main.ts:310`) and
  especially the world-select blurb (`WORLD_SELECT_BLURB`,
  `web/src/main.ts:467-476` — the "Bring your friends… TOWN is the lobby…
  Drop in and hang out" lines). Write launch copy with actual personality
  that fits ZZT's voice (terse, playful, a little weird — read TOWN's scrolls
  for the register), sized to the CP437 windows it renders in. Owner signs
  off on the final strings. Filler task — spec is just "make it good".

- [ ] **README refresh: sell the multiplayer game and roadmap.** Remove the
  development-docs block from `README.md` entirely:
  "For a deep dive into the architecture:" plus the `TASKS.md`,
  `IMPLEMENTATION.md`, `ANALYSIS.md`, and `NOTES.md` bullets. Also remove the
  standalone `---` horizontal rules; the `##` headers already separate the
  page enough. Replace the dry structure with a friendlier player-facing
  section about what people can actually do in multiplayer ZZT today: explore
  classic worlds together, chat, share rooms, save/restore snapshots, buy and
  use items, read scrolls, take damage/respawn, transfer boards, and call out
  planned/available PvP explicitly and honestly based on the current rules.
  Add a "Moonshot Roadmap" style section that talks about the big creative
  directions from the idea backlog — LLM world creation, Museum of ZZT
  search-and-play, player identity, party instances, replays/daily challenges,
  ghost racing, possession/live-DM ideas, and future editor/community
  publishing — in public-friendly language, without exposing local planning
  docs. DoD: README reads like an inviting project page for players and
  contributors, contains no links to ignored private planning docs, contains
  no horizontal rules, and still keeps practical local run instructions.

### Idea backlog (2026-07-10 — NOT tasks; owner picks, then each gets an M7-style spec)

Plain bullets on purpose: executors must not pick these up. Verified gaps
first, then features that exploit what this codebase is uniquely good at.

**Verified gaps (checked against the code 2026-07-10):**
* **Autosave and crash recovery.** A server crash or restart loses every live
  room — nothing snapshots automatically (`SaveSnapshot` is manual, M4.3a).
  Periodic background snapshot of occupied worlds + restore-on-boot. The
  single worst UX event the service can produce is currently unguarded.
* **Reconnect grace.** A dropped WebSocket calls `LeavePlayer` immediately
  (`websocket_server.go:655,664`) — a browser refresh or Wi-Fi blip deletes
  the run. Hold the stat for ~60s under a resume token; rejoin reclaims it.
  Guests need this as much as accounts do.
* **Tell players apart — RGB smiley backgrounds (owner-designed
  2026-07-10).** Every player is the identical white-on-blue ☻
  (`gamevars.go:467-471`); in a busy room nobody knows which one they are,
  let alone who's who. Presentation-only fix: snapshots gain a players list
  (statId, x, y, name, color); the client overlays it. Design decided with
  the owner: the glyph is **always char 2** — the ☻ is what everyone knows
  as the ZZT player/object and never changes — but each player picks an
  **arbitrary 24-bit RGB background** at join (later stored on their M6.2
  account). This is possible precisely because the color lives in the
  protocol + canvas, never in the sim: the tile stays byte-for-byte vanilla
  (char 2, 0x1F), so StateHash, replay fixtures, and exported `.ZZT` files
  are untouched. Rendering rules: foreground auto-contrasts white/black by
  background luminance; the overlay obeys visibility (dark rooms hide the
  color exactly as they hide the player; energizer blink wins while
  flashing). Picker UI is a CP437 window (M4.1) — an RGB slider/hex entry
  rendered ZZT-style, with the 16 DOS colors offered as quick picks for
  purists.
* **CI.** No workflows exist. GitHub Actions: `go build`, `go vet`,
  `go test ./...`, plus `go test -race` once M7.3 lands, and the node-driven
  TS checks. The replay fixture only protects commits that run it.
* **Deep links.** `/play/TOWN` style URLs that land a visitor in a world
  (and `/watch/TOWN` read-only spectator links). Sharing is the growth loop
  and today there is nothing to share.

**The determinism dividend (features the M0 work already paid for):**
* **Replay recording and playback.** Seeded RNG + input-driven steps means a
  complete session is just `{world hash, seed, per-tick inputs, submits}` —
  kilobytes. Record always; play back server-side into the normal snapshot
  stream (a replay is a room nobody controls). Shareable replay URLs.
  Synergy: M7.3's lock centralizes every submit path, which is exactly the
  event log a recorder needs.
* **Daily challenge.** Same world + same seed for everyone each day,
  server-verified completion time, one leaderboard. Determinism makes it
  trivial and it is the strongest known retention mechanic in its class.
* **Speedrun leaderboards.** Per-world verified times — the server is
  authoritative, so runs are cheat-proof by construction, something even
  dedicated speedrun sites cannot offer. Pairs with M11: any Museum world
  becomes a race.
* **Ghost racing.** Render a prior run's player positions as a translucent
  ghost cursor while you play the same world+seed. Replays make it free.

**Vibe and reach:**
* **CRT shader toggle.** Scanlines/phosphor/curvature over the canvas —
  cheap, optional, and the single biggest "feels like 1991" multiplier.
* **Touch controls.** A D-pad + shoot overlay for phones/tablets; the whole
  client is one canvas, so reach is currently keyboard-only.
* **Party instances.** Private copies of a world for a group ("play TOWN
  with just us") — `Instances` already keys by name; key by name+party.
* **Discord presence bridge.** "3 players in TOWN" + chat relay; community
  glue for a small MMO.
* **Achievements (post-M6.2).** Account-keyed firsts (beat TOWN, first
  purple key, 100 gems) surfaced in chat, stored via M6.3's interface.

**Moonshots (2026-07-10 — the most creative directions the architecture
enables; each is feasible precisely because of a property we already built):**
* **Possession mode — play as the monsters.** Asymmetric multiplayer: a
  second player joins someone's run as the *dungeon* — possessing a lion,
  tiger, or object and driving it with their keys. The tick loop already
  injects per-stat inputs for player stats (`GameStepWithInputs`); extending
  the input map to a possessed non-player stat (overriding its TickProc's
  movement with the possessor's deltas) is a narrow, deterministic seam.
  ZZT becomes a game of D&D with a live DM.
* **Living worlds.** Empty rooms currently freeze. Opt-in per world: keep
  ticking while nobody's there (bounded, e.g. slow-tick), so KUDZU's vines
  actually grow overnight and a bear wanders rooms between your visits.
  Determinism makes "what happened while you were gone" replayable — you
  could literally watch the recap.
* **The ZZT Continent.** Stitch every hosted world into one geography:
  cross-world passages (lobby idea) generalized so board *edges* can link
  worlds — walk west out of TOWN's map and into CAVES. One persistent
  walkable universe made of the entire Museum archive.
* **Player-authored scrolls (messages in bottles).** Let players drop a
  scroll tile holding their own text in the lobby or (owner-permitted)
  worlds — Dark Souls messages in ZZT's native medium. Scrolls are already
  first-class sim objects; placement rides the M5 edit-op path with a
  rate/curation layer.
* **Live DM console.** A world owner hot-edits object code and drops
  monsters *while players are inside* (M10's leases + a "DM" permission) —
  running a live event in a world the way a game master runs a table.
* **Crowd-controlled runs.** Spectators (watch links) vote to spawn a lion,
  douse a torch, or gift ammo in a streamer's run — inputs enter through
  the same deterministic Submit* seam as everything else, so even chaos is
  replayable.
* **Prompt-to-world.** An LLM endpoint that emits a small ZZT world from a
  text prompt ("a haunted bakery with a gem heist"), validated by the M7.5
  gate, hosted instantly, disposable. ZZT-OOP is tiny, textual, and
  well-documented — it is close to the ideal LLM target language, and the
  editor/publish pipeline (M5.6) already handles the rest.
* **Tournament nights.** Scheduled PvP arena brackets (PvP world + party
  instances + spectator links + verified results from the authoritative
  server), with the bracket itself rendered as a ZZT board in the lobby.

**First-party worlds (owner 2026-07-10 — "later on in the roadmap"):**
* **A purpose-built PvP arena world.** A ZZT world designed for
  player-vs-player: arena boards, ammo/energizer spawns via ZZT-OOP
  restore/duplicator tricks, spawn points spread apart, score kept in
  flags. Needs one engine decision first: M2.4/M8.1 make player bullets
  harmless to players *by design*, so PvP requires an explicit opt-in —
  a per-world server flag (set at load, deterministic, part of the room
  config not the world file) that re-enables player↔player bullet damage
  and point-blank shots on that world only. Death already respawns (M2.4),
  which is exactly right for an arena. Build the world itself in the M5
  editor once it exists — first-party dogfooding.
* **A purpose-built lobby world to replace TOWN as the default hangout.**
  A social hub designed for loitering: a plaza sized for crowds, signs
  (scrolls) teaching controls and chat, high-score hall, and — the
  interesting mechanic — **cross-world passages**: passage tiles the
  *server* interprets as "transfer to hosted world X" (vanilla passages
  are intra-world only, so this is a room-manager feature keyed off
  designated passages, not a sim change). The lobby becomes a physical
  world picker you walk through; TOWN goes back to being a game you beat.

**README follow-ups:**
* [ ] **Add capnkev to the README greetz list.** Keep the list alphabetical,
  names only, and preserve the README's no-emoji style.

* [ ] **M12.2 follow-up: verify ZWD round-trip StateHash.** Decompiler and compiler work — any .ZZT decompiles cleanly and recompiles to a valid loadable world. Hash checks in TestZWDRoundTripTOWN/CAVES/CITY fail because saved .ZZT files carry garbage in player stat[0] runtime fields (StepX/StepY, P1/P2/P3, Follower/Leader). Fix: normalize those to compiler defaults before hashing the original. Also fix the pre-existing scroll_window_test.go build failure (townRoomManager/findEvent undefined) blocking go test ./...\n\n* [x] **ZZT-OOP `#end`/`:touch` "race" — MISDIAGNOSIS. Real cause: compiler stat-default garbage (FIXED).**
  There is no tick race. The symptom (touching an object never emits a `ScrollEvent`,
  objects "don't respond" in vanilla ZZT) came from the ZWD compiler's default
  `zwdStat` carrying non-ZZT values `p1=4, p2=4, stepY=-1` (`engine/zwd.go`
  `parseStatLine`), copied verbatim into every compiled `.ZZT` stat.
  - For an `Object`, `P2` is the **lock** flag (`oop.go:468`, faithful to
    `OOP.PAS:508`): `P2=4 ≠ 0` = locked, so `OopSend("TOUCH")` refuses delivery and
    `:touch` never runs.
  - `StepY=-1` separately gave every object with no explicit `step` a standing
    walk-north order, so it drifts every tick.
  Proven against shipped worlds: BAKERY's pre-fix `.ZZT` had 22/22 objects locked and
  8 walking; recompiling with the fix yields 0/0.

  Fix (done): base defaults set to ZZT-neutral zeros (`p1=0, p2=0, stepX=0, stepY=0`);
  the `cycle:-1` sentinel and `Object`/`Bear` glyph overrides retained. Regression
  test `TestZWDObjectDefaultsAreZZTNeutral` (`engine/zwd_test.go`). The `ZWD.md` /
  `spec.md` table row that claimed defaults were `p1=4, p2=4` (from a nonexistent
  `InitEditorStatSettings`) was the origin of the bug and is corrected.
  ⚠ Any `.ZZT` compiled before this fix is still locked and must be recompiled from
  its `.zwd`.

* [ ] **Compiler: auto-generate Passage stats from the legend `to "BOARD"` clause.**
  A `Passage` is stat-backed, so today every passage tile needs an explicit
  `stat at X,Y ... p3 board "…"`. LLM-generated worlds routinely draw a passage glyph
  with `p = Passage color 0xNN to "BOARD"` in the legend and omit the stat, producing
  `grid contains stat-backed element Passage but no matching stat is defined`. The
  legend already carries the destination — the compiler should synthesize one passage
  stat per matching tile when the legend entry has a `to` destination.

* [ ] **Compiler: report all orphan / decorative stat-backed tiles in one pass.**
  `compileBoard` returns on the *first* orphan stat-backed tile (`engine/zwd.go`
  ~line 790). The generation-repair loop then fixes one tile, recompiles, and hits the
  next — O(n) round-trips for n mistakes. Collect and report every offending
  `(element, col, row)` in a single error so one repair round can fix them all. (A
  temporary `debugOrphanScan` pass during the touch investigation surfaced e.g.
  DYINGSTA's 5-tile passage "door" and KEEPLITE's 13-tile object drawing at once.)

* [ ] **Retire/retitle `engine/touch_race_test.go`.**
  The prior agent added it while chasing the phantom `#end`/`:touch` "race"; its
  comments still assert a race that does not exist. The accurate regression guard is
  `TestZWDObjectDefaultsAreZZTNeutral` (`engine/zwd_test.go`). Either delete
  `touch_race_test.go` or rewrite its header to describe what it actually verifies
  (an unlocked object runs `:touch` and emits a scroll on the tick after `OopSend`).

* [x] **[BUG] World flags do not propagate across boards in the multiplayer `RoomManager`.**
  `World.Info.Flags` is a value array (`[MAX_FLAG]string`, `gamevars.go:107`) embedded
  in `TWorld`, and `RoomManager.ensureRoom` gives each board its own engine via
  `engine.World = rm.world` (`room_manager.go`), which **copies** the flag array per
  room. Setting a flag with `#set` on one board's engine therefore never reaches
  another board's engine (nor back to `rm.world`). Any ZZT world that sets a flag on
  one board and checks it on another — the backbone of ZZT progression/quest logic —
  silently breaks: the `#if flag` check on the destination board always sees the flag
  unset. Reproduced by the `OBSERV.ZZT` demo: `#set haslens` in "The Cellar" is
  invisible to the telescope's `#if haslens` on "Observatory Tower", so the win can
  never fire in multiplayer. Vanilla single-engine ZZT is unaffected (one shared
  world). Fix: hoist world-level flag state (and audit other `World.Info` fields that
  are true world state vs. per-board) out of the per-room `Engine` into `RoomManager`,
  or sync flags across rooms on every `#set`/`#clear` and board transition. Add a
  regression test: set a flag in room A, assert room B observes it.

* [ ] **Passages must link to a matching-color passage on the destination board.**
  ZZT's passage teleport logic deposits the player at the first passage on the
  destination board whose color byte matches the source passage's color. If no
  color-matched passage exists on the destination, the player arrives at the
  destination's default start point instead, which is confusing and breaks the
  world layout.

  Claude currently generates passages without coordinating their colors across
  boards. For example, a red passage on the Town Plaza that leads to Inside the
  Bakery must have a corresponding red passage on Inside the Bakery pointing
  back. If the color differs (or if no return passage exists), players land at the
  wrong spot.

  **Fix needed in prompt spec:** Add to `ZWD.md` and `engine/promptkit_assets/spec.md`
  under the Passage section:
  > A passage to board B must have a matching-color return passage on board B.
  > The color byte (`0xNN`) of both passages must be identical. When linking two
  > boards, always define both the outgoing and the return passage in the same
  > legend entry color and verify they match.

  **Optional engine improvement:** Add a post-compilation cross-board check in
  `CompileZWD` (or the `M12.4` validation step) that warns when a passage's
  destination board has no matching-color passage.

* [x] **Robust engine rendering/touching & compiler enforcement for orphan stats.**
  When an `E_OBJECT` tile is placed in the board grid but not listed in the
  `stats` section of the ZWD, it compiles successfully but lacks a stat. Drawing
  or touching this orphan object tile triggers `index out of range [-1]` panic in
  `ElementObjectDraw` (`elements.go:883`) because `GetStatIdAt(x, y)` returns `-1`.
  
  **Fixes needed:**
  1. **Engine Robustness**: In `elements.go`, update `ElementObjectDraw` and
     `ElementObjectTouch` (and other draw/touch procedures like Bomb/Transporter)
     to check if `statId < 0` and handle it gracefully (e.g. falling back to the
     default element character `ElementDefs[tile.Element].Character` or doing nothing
     for touch) instead of panicking.
  2. **Compiler Check**: In `zwd.go`, check that every tile placed on the board grid
     which requires a stat (e.g., `E_OBJECT`, `E_SCROLL`, `E_PASSAGE`) has a corresponding
     stat defined at its coordinates in the board's `stats` section. Fail compilation
     with a descriptive error if any orphan stat-backed tiles are found.



