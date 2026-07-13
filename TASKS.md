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

- [x] **M12.11 — Dream-a-world fixes: prose-in-grid tolerance, prompt hardening, progress window.** The top Dream failure was the LLM drawing prose straight into the grid, where every letter is an undefined legend key the compiler rejects one-per-compile (never converging within K=3 repairs). `preprocessZWDGrid` now injects a legend entry for every undefined grid char (space → Empty; else → white on-board Text via `cp437:0xNN`), deriving the exclusion set from a correct legend-key tokenization rather than the lossy `legendMap` (which drops the `=` key and pre-existing `cp437:` keys). The generation prompt was hardened against prose-in-grid. Client: Dream progress lines are clamped to the window inner width, and the progress modal updates in place with `linePos` auto-following instead of reopening each poll (which snapped the scroll to the top). Generation is outside the sim; replay fixture unchanged. See NOTES.md.

- [x] **M12.12 — Door with a 0/8 background nibble crashes the whole server.**
  Discovered 2026-07-11: playing a *generated* world, a player touched a Door
  whose color had a background nibble of 0, and `ElementDoorTouch`
  (`elements.go:1058`) computed `key = Color/16 % 8 == 0` then indexed
  `pState.Keys[key-1]` = `Keys[-1]` → `panic: index out of range [-1]`. The
  panic is in the tick goroutine with no recover, so it takes down the entire
  process for **every** player in **every** room, not just the toucher. This is
  a machine-conversion divergence from the Pascal: vanilla
  (`ELEMENTS.PAS:1101-1113`) uses 1-based `key` directly (`Keys[key]`,
  `ColorNames[key]`), so a `key==0` door harmlessly reads `Keys[0]`; the Go port
  shifted every access to `key-1` for 0-based arrays and so underflows instead.
  Two independent problems to weigh (write the decision in NOTES.md):
  (1) **Robustness** — the compiler is the security boundary for generated
  worlds (M12.4), so decide whether `zwd.go` should reject a `Door` legend
  entry / tile whose background nibble is 0 or 8 (no vanilla key color), or the
  sim should guard `key==0` (matching vanilla's tolerant read), or both. A DOS
  ZZT world could also carry such a door, so a sim guard is the safer floor.
  (2) **Server survivability** — a single sim panic should never kill the
  process. Wrap `WorldInstance.Tick`/`RoomManager.StepDiffs`
  (`websocket_server.go:188`, `room_manager.go:430`) in a per-room recover that
  isolates or drops the offending room and logs, so one bad world cannot take
  the fleet down. Keep any guard/recover out of `StateHash` and determinism.
  DoD: a headless test touches a `0x0E`/`0x8E` door without panicking; a test
  proves one room's panic does not stop the others; `go test ./...` green;
  replay fixture unchanged.

- [x] **M12.13 — Auto-synthesize stats for orphan grid glyphs (kill the dominant
  Dream failure).** The single most common generation failure (2026-07-11 log
  taxonomy in NOTES.md; Saga Archive burned all 3 repair attempts on it) is
  `grid contains stat-backed element X but no matching stat is defined at (x,y)`
  (`zwd.go:801`, the reverse-direction check at `zwd.go:786-805`): the LLM draws
  a stat-backed **glyph** in the grid but never declares a matching stat. ZWD
  forces the model to keep the grid and a separate `stats` list with exact
  `at X,Y` coordinates in byte-for-byte agreement, which it cannot do reliably.
  `preprocessZWDGrid` (`generation.go:867`) already reconciles the *declared-stat
  → nearest glyph* direction (stat-alignment block ~`generation.go:1085-1185`)
  and, as of M12.11, absorbs undefined grid chars just before the rows are
  appended (~`generation.go:1187`). This task adds the missing reverse direction:
  **after stat alignment, for every stat-backed glyph left unclaimed in the final
  25-row grid, synthesize a minimal valid `stat at <gridX>,<gridY> element <name>`
  so the coordinate is DERIVED from where the glyph sits — the model never writes
  it.** Requirements:
  * The stat-backed set is `elementNeedsStat` (`zwd.go:817`): Object, Scroll,
    Passage, Transporter, Pusher, Bomb, BlinkWall, Duplicator, Bear, Ruffian,
    SpinningGun, Lion, Tiger, Slime, Shark, CentipedeHead/Segment, Bullet, Star.
    (E_PLAYER is already handled by the player-positioning block; skip it here.)
  * Reuse the block's existing `claimed`/`gridElements` maps to find unclaimed
    stat-backed glyphs, so a glyph already matched to a declared stat is not
    double-declared.
  * Per-element defaults table: cycle from `ElementDefs[el].Cycle`; Object → an
    empty `#end` OOP body so it is inert but valid (M12.10: Objects must exist as
    stats); Passage/Transporter → a sane target (the start/hub board id or board
    0) since a Passage with no destination is useless; monsters → default
    intelligence/params. Ground each default in `gamevars.go` stat fields and the
    Pascal element inits; do NOT invent params. When a default cannot be chosen
    safely, prefer leaving the existing hard error over guessing wrong.
  * Emit the synthesized `stat`/`oop`/`end` lines into the board's `stats` block
    (create the block if absent), mirroring how the undefined-char fix injects
    into the legend. Determinism: iterate glyphs in row-major order.
  Purely preprocessing/generation — outside the sim; replay fixture unchanged.
  DoD: a regression built from the real Saga Archive failure (an Object glyph in
  the grid with no stat) compiles + validates after preprocess, with the
  synthesized Object at the glyph's exact coordinate and an empty `#end`; a
  Passage-glyph case gets a valid destination; `go test ./...` green.

- [x] **M12.14 — Compiler/preprocessor tolerance for the remaining recurring
  Dream rejections.** After M12.13, the next tier of repair-round causes
  (NOTES.md taxonomy) are still hard errors that bounce a whole board through the
  LLM. Absorb each deterministically instead, following the "derive, don't
  require" principle and the M12.11 precedent (fix in preprocess where possible;
  the compiler stays the security boundary and still rejects the genuinely
  unrepresentable):
  * `duplicate legend key` (`zwd.go:348`) — keep the first entry, drop the
    duplicate, and surface a warning rather than failing.
  * `unknown stat field <name>` (`zwd.go:612`) — drop the unknown field and warn,
    rather than rejecting the whole stat/board.
  * `board <name> missing end` (`zwd.go:204`) — have `preprocessZWDGrid`
    structurally auto-close an unterminated board/section (the model truncates),
    rather than the compiler rejecting it.
  Warnings should flow where `generatedGridDiagnostics` (`generation.go:557`)
  already surfaces context, so accepted-with-repair worlds are still visible.
  Outside the sim; replay fixture unchanged. DoD: a table-driven test feeds one
  minimal board per case above and asserts it compiles + validates after
  preprocess with the expected warning; `go test ./...` green.

## M13 — Ship-shape: hygiene, CI, and service survivability

Goal: protect the game that already works. Added 2026-07-12 (see NOTES.md):
the replay fixture only guards commits that run it, and the live service can
still lose every player's run to a crash, a Wi-Fi blip, or a known data race.
Deliberately positioned before the remaining M12 generation tasks — losing a
player's evening outranks improving a generated board's palette. Order note
(2026-07-12): after M13, execute **M12.16** before the remaining M12.15
slices — procedural repair raises generation *yield*, which the corpus/style
work then builds on. Every task in this section is presentation/server-layer
only: no simulation change, replay fixture unchanged throughout.

- [x] **M13.0 — Working-tree and log hygiene: STARGEN strays, the misplaced
  engine/NOTES.md, and the stale touch_race_test.go header.** No new code.
  * `engine/STARGEN.zwd`, `engine/STARGEN.plan.md`, `engine/STARGEN.prompt.txt`
    are Dream-pipeline outputs sitting untracked at the engine root
    (verified 2026-07-12). Precedent: the BAKERY trio (`BAKERY.zwd`,
    `BAKERY.plan.md`, `BAKERY.prompt.txt`) is git-tracked at the same
    location. Gate before adopting: `CompileZWD` on `STARGEN.zwd` must
    succeed and the compiled world must pass the M7.5 gate (headless load +
    200 `GameStep`s, no panic — reuse the validation path
    `gen_generated_test.go` uses). If it passes, commit the trio like
    BAKERY's; if it fails, record the exact failure in NOTES.md (it is
    generation-failure evidence, useful to M12.16) and add `STARGEN.*` to
    `engine/.gitignore` instead. Never commit a non-compiling `.zwd` as if
    it were a good example (the M12.6 lesson).
  * `engine/NOTES.md` (45 lines: the 2026-07-11 Dream failure taxonomy and
    the M12.12 door/panic-containment entry) is escalation-log content that
    landed in the wrong file — the project log is the repo-root `NOTES.md`
    (CLAUDE.md doc map). First check whether root NOTES.md already carries
    either entry (the taxonomy is cited by M12.13's spec); merge whatever is
    missing into root NOTES.md as new entries under their original dates,
    noting they were moved (append-only file — never rewrite around them),
    then delete `engine/NOTES.md`.
  * `engine/touch_race_test.go` still claims in its header comments to guard
    a `#end`/`:touch` tick race; that diagnosis was disproven (Future Tasks
    "MISDIAGNOSIS" entry — the real cause was ZWD compiler stat-default
    garbage, since fixed and guarded by `TestZWDObjectDefaultsAreZZTNeutral`
    in `engine/zwd_test.go`). Rewrite the file's header comment to state
    what the test actually verifies: an unlocked object (P2=0) runs `:touch`
    and emits a ScrollEvent on the tick after `OopSend`. Keep the test body.
    This executes the "Retire/retitle touch_race_test.go" backlog bullet
    (annotated as folded).
  DoD: `go test ./...` green; `git status` shows no untracked strays at the
  engine root; `engine/NOTES.md` gone with its content preserved in root
  NOTES.md; commit `M13.0: ...`.

- [x] **M13.1 — CI: a GitHub Actions gate on every push and PR.** Verified
  2026-07-12: `.github/workflows/` does not exist, so the replay fixture —
  the project's stated safety net (CLAUDE.md rule 3) — only protects commits
  whose author remembered to run it. Create `.github/workflows/ci.yml` at the
  repo root with three jobs:
  * **engine** — `actions/checkout@v4`, `actions/setup-go@v5` with
    `go-version: '1.26.x'`. Do NOT use `go-version-file`: `engine/go.mod`
    says `go 1.16`, which is the language floor `go:embed` required (NOTES.md
    M12.3 entry), not the toolchain; the baseline was verified on go1.26.5
    (this file, line 14). Steps, all with `working-directory: engine`:
    `go build ./...`, `go vet ./...`, `go test ./...`.
  * **engine-race** — same setup, `go test -race ./...`, with
    `continue-on-error: true` and a YAML comment citing the known race in
    `TestWebSocketServerTwentyBotSoak` (NOTES.md 2026-07-10 M7.3 entry).
    M13.4 removes the `continue-on-error`.
  * **web** — `actions/setup-node@v4` (LTS), `working-directory: engine/web`.
    If `engine/web/package-lock.json` does not exist, run `npm install` once
    locally and COMMIT the lockfile in this task (deterministic CI needs
    it); the job then uses `npm ci`. Steps: `npm ci`, `npm test` (runs
    `node test/dream.test.mjs && node test/modal.test.mjs` —
    `web/package.json` scripts), and `npm run build` (runs `tsc && vite
    build`, so type errors fail CI).
  * **Clean-clone proof.** CI runs on a fresh checkout, so every test must
    pass without untracked local files. `fixtures/TOWN.ZZT` and
    `engine/BAKERY.zwd` are tracked (verified 2026-07-12); the M7.5 corpus
    `.ZZT` worlds are NOT (gitignored). Before pushing, prove it locally:
    `git clone . /tmp/zztmmo-clean && cd /tmp/zztmmo-clean/engine &&
    go test ./...`. Any test that needs an untracked world must `t.Skip`
    when the file is absent, following the `testhelpers_test.go:19` pattern
    — list in NOTES.md every test you had to guard.
  DoD: the workflow file is committed; a pushed run shows the engine and web
  jobs green (engine-race may be red-but-allowed); NOTES.md records any skip
  guards added; `go test ./...` green locally.

- [x] **M13.2 — Reconnect grace: a dropped WebSocket must not delete the
  run.** Today the read loop's exit path calls
  `s.removeClientFromInstance(inst, playerID)` unconditionally
  (`websocket_server.go:426`; also the join-failure paths at `:325` and
  `:329`), which calls `RoomManager.LeavePlayer` (`room_manager.go:371`) and
  removes the player's stat immediately. A browser refresh or a Wi-Fi blip
  therefore destroys the run — position, keys, score, everything. Backlog
  item promoted with a full spec.
  * **Resume token.** Mint on join (`ServeHTTP`'s join path,
    `websocket_server.go:307-322`): 16 bytes from `crypto/rand`, hex-encoded,
    stored in a new `inst.ResumeTokens map[string]PlayerID`. Send it to the
    client by adding `ResumeToken string` (json `resumeToken,omitempty`) to
    `SnapshotMessage` (`protocol.go:382-393`), set only on the join/resume
    snapshot. `crypto/rand` is fine here — this is the network layer;
    CLAUDE.md rule 2 governs simulation state only.
  * **Detach, don't leave.** On read-loop exit, when the player still
    exists: remove them from `inst.Clients`/`inst.Inputs` (so broadcast and
    input application skip them) but do NOT call `LeavePlayer`. Instead set
    `inst.Detached map[PlayerID]int` = `ReconnectGraceTicks` (new const; 545
    ticks ≈ 60s at the fixed 110ms tick — count ticks, never wall-clock, so
    tests can step it deterministically). The stat stays on the board and
    keeps ticking with zeroed input, exactly like an idle player.
  * **Expiry runs on the tick goroutine.** `WorldInstance.Tick`
    (`websocket_server.go:185`) decrements each `Detached` entry under
    `inst.mu`; at zero it performs today's removal (factor the body of
    `removeClientFromInstance`, `websocket_server.go:1038`, into a helper the
    expiry path can call while already holding the lock). Note for M13.4:
    this moves the leave mutation onto the tick goroutine, which is the
    shape that race fix wants anyway.
  * **Resume.** `JoinMessage` (`protocol.go:82-87`) gains
    `ResumeToken string` (json `resumeToken,omitempty`). If it maps to a
    detached PlayerID in this instance: reattach — put the new client in
    `inst.Clients[playerID]`, delete the `Detached` entry, send a fresh full
    `Snapshot(playerID)`. Unknown or expired token: fall through to a normal
    fresh join — never an error, never a dead client.
  * **Decisions to record in NOTES.md before coding** (the M4.3a pattern):
    (1) a second live connection presenting a token whose player is NOT
    detached — recommend newest-wins: detach the old socket and hand the
    stat to the new connection (also the fix for "my tab froze, I opened a
    new one"); (2) `pendingPlayerEvents` (`room_manager.go:34`) accumulate
    unboundedly for a detached player because only connected clients drain
    them (`DrainPlayerEvents`, `room_manager.go:203`) — recommend clearing
    on detach and accepting the loss (events are presentation-only);
    (3) a detached player holding a scroll open (`roomPlayer.scrollOpen`)
    stays frozen until expiry — acceptable, but say so.
  * **Client.** `web/src/main.ts`: store the token in `sessionStorage` keyed
    by world name; on an unexpected socket close during play, auto-reconnect
    with capped backoff, presenting the token; repaint from the resume
    snapshot. An explicit confirmed quit clears the stored token.
  DoD: Go tests — (a) disconnect + resume within grace reclaims the same
  PlayerID/statId with inventory intact (collect a key first, assert it
  survives); (b) expiry removes the stat, frees the square, and
  `freezeRoomIfEmpty` still fires when the room empties; (c) an unknown
  token joins fresh; (d) the second-connection policy. Node-driven test for
  the client reconnect state machine (the M4.1 pattern). `go test ./...`
  green; replay fixture unchanged (all changes are server-layer).

- [x] **M13.3 — Autosave and restore-on-boot.** Verified gap (idea backlog
  2026-07-10): nothing snapshots automatically — `SaveSnapshot` runs only
  when a player presses 'S' (`websocket_server.go:745,768`) — so a crash or
  restart loses every live room. The single worst UX event the service can
  produce is currently unguarded.
  * **Cadence.** A `-autosave <seconds>` flag on `cmd/zzt-server` (default
    60; 0 disables). Drive it from the existing tick loop by counting ticks
    (`seconds*1000/110`), not a new timer — one clock, deterministic tests.
    Each firing snapshots every *occupied* instance: `s.DefaultInstance`
    plus every `s.Instances` entry with `len(inst.Clients) > 0` (empty
    rooms are already frozen into `rm.world` and have nothing new to say).
  * **Write path.** `RoomManager.SaveSnapshot(dir, name, playerID)` takes
    the saving player (their inventory is written into `World.Info`,
    M4.3a). Autosave has no saver: add an internal variant that writes
    zero-value inventory and document the choice — the server already
    ignores those fields on join (M4.3a decision 1). Files go to
    `SavesDir/autosave/<INSTANCENAME>.SAV`. Instance names already passed
    `SanitizeSaveName` on their hosting paths, but re-verify each name at
    save time and skip-with-log rather than guess. Write atomically:
    create `*.tmp`, then `os.Rename` — a crash mid-write must never corrupt
    the previous good autosave.
  * **Restore-on-boot.** At server startup, before serving: for each
    `SavesDir/autosave/*.SAV` whose name matches a hostable world, restore
    it as that instance's starting world via the existing
    `RoomManager.RestoreSnapshot` machinery (its occupancy refusal is
    vacuous at boot). Record the freshness policy in NOTES.md: an autosave
    beats the pristine `.ZZT` at boot (that is what crash recovery MEANS);
    deleting the autosave file is the operator's reset; add a `-fresh` flag
    that skips restore for a deliberately clean boot.
  * **Concurrency.** Snapshot from a copy exactly as `SaveSnapshot` already
    does (M4.3a: "a save never disturbs the game it is a save of"); hold
    `inst.mu` only long enough to take the copy, never during file I/O.
  DoD: test — join, collect items, step, fire the autosave seam directly,
  construct a fresh `WebSocketServer` over the same directories, and assert
  board contents and flags survived; players are dropped from the snapshot
  (M4.3a decision 1 — reuse its assertion); a corrupt/truncated autosave
  file is skipped with a log line, not a boot failure; `-fresh` skips
  restore; `go test ./...` green; replay fixture unchanged.

- [x] **M13.4 — Kill the room-lifecycle race; make `-race` a required CI
  job.** `go test -race ./...` fails `TestWebSocketServerTwentyBotSoak`
  (recorded 2026-07-10, M7.3 entry): an HTTP disconnect path (`ServeHTTP` →
  `removeClientFromInstance` `websocket_server.go:1038` → `LeavePlayer`
  `room_manager.go:371` → `Engine.RemovePlayer`/`RemoveStat`) mutates room
  engine state while something else reads `StateHash` on the same room.
  * **Diagnose before fixing.** `removeClientFromInstance` holds `inst.mu`
    and `WorldInstance.Tick` holds `inst.mu` (`websocket_server.go:186`),
    but the DEFAULT world runs through `removeClient` (`:1027`) under `s.mu`
    with `WebSocketServer.Tick` (`:113`) — map exactly which reader/writer
    pair the race detector reports. It may be the soak test itself calling
    `StateHash` unlocked; then the production fix is a locked accessor the
    test uses, not new locking in the tick path. Write the actual pair down
    in NOTES.md.
  * **Preferred shape.** Route ALL leaves through the tick goroutine:
    M13.2's expiry already runs there; make the immediate-leave paths
    (join-failure cleanup, explicit quit teardown) enqueue onto the same
    drain instead of mutating from the HTTP goroutine. Fewer lock orderings
    beats more locks. If M14.1 (one instance model) lands first, this
    collapses to a single code path.
  * **Constraints.** No mutex may enter `Engine` simulation state or
    `StateHash` inputs (M7.3 precedent); `go vet` stays clean (copylocks —
    Engine must never be copied by value); determinism untouched.
  * Then run the full `go test -race ./...` at least 5 times; fix or file
    (NOTES.md) every distinct race reported; remove `continue-on-error`
    from the M13.1 race job.
  DoD: `go test -race ./...` green repeatedly; the CI race job is required;
  NOTES.md names the racing pair that was actually found; replay fixture
  unchanged.

## M12 continued — world-style adapter and procedural repair (after M13)

(2026-07-12: these tasks sat above M13 before it was inserted; they keep
their M12 numbering. Execution order within this section: **M12.16 first**,
then the M12.15 slices — procedural repair raises generation yield, which
the corpus/style work builds on. The specs below are unchanged.)

- [x] **M12.16 [ADVISOR] — Error-driven procedural repair layer (compiler
  self-heals before the LLM).** (Order note 2026-07-12: execute this
  directly after M13, BEFORE the remaining M12.15 slices — yield first, then
  style; see NOTES.md. Extended the same day with three folded Future-Tasks
  bullets, marked below.) Owner priority: maximize what the
  compiler/decompiler fixes itself before resending to the model — LLM repair
  rounds are slow, cost tokens, and don't always converge (Saga Archive burned
  all 3 attempts and blanked to a placeholder). Generalize the ad-hoc procedural
  fixers (M12.11 undefined-char, M12.13 orphan-glyph, M12.14 dup-key /
  unknown-field / missing-end) into a first-class **error→fixer dispatch** with a
  fixpoint loop; the LLM becomes the fallback of last resort. Full design in
  NOTES.md (2026-07-11). Requirements:
  * **Typed error codes.** Add a structured `code` (enum of error kinds) to
    `zwdError` so fixers dispatch on the code, not by string-matching the human
    message. Keep the precise line/col/message (M12.1) for the LLM/humans.
  * **Fixpoint loop** (`compileWithRepair`): parse → on error look up
    `fixers[err.code]` → apply → re-parse → repeat until success, no fixer
    matches, or no progress (byte-identical output / recurring error → hand to
    LLM). Never spin.
  * **The bucket boundary (load-bearing).** Bucket 1 — bookkeeping/syntactic
    (undefined char, orphan glyph, dup key, unknown field, missing `end`, row
    width, off-board coords, out-of-range color, the M12.12 door nibble) →
    procedurally fix; these are the entire dominant failure taxonomy. Bucket 2 —
    semantic/intent (exit to a nonexistent board, missing passage target, key
    behind its own door) → NEVER procedurally guess; a silent wrong guess yields
    a compiling-but-broken world (worse than a repair round). Route bucket 2 to
    the LLM or prevent it upstream via M12.3a plan constraints. Composition/quality
    raises no error and is out of scope here (M12.15 territory).
  * **Auditability.** Emit a diagnostic per fix (reuse
    `generatedGridDiagnostics`, `generation.go:557`); feed diagnostics forward
    into the next board's context / prompt-hardening so the model drifts toward
    correctness without a round trip.
  * **Folded from Future Tasks (2026-07-12) — register these in the same
    dispatch table:**
    (1) *Passage-stat synthesis from the legend.* A legend entry like
    `p = Passage color 0x0F to "BOARD"` already names the destination
    (`parseLegendEntry`, `zwd.go:442`); when the orphan check (`zwd.go:842`)
    fires for a Passage tile whose legend entry carries a `to` destination,
    synthesize the stat — coordinate AND target are both derived, never
    guessed, so this is bucket 1.
    (2) *Aggregate orphan reporting.* `compileBoard` stops at the FIRST
    orphan stat-backed tile (`zwd.go:842`), costing one repair round per
    tile; collect every offending (element, col, row) into one error so a
    single fixer pass (M12.13's synthesizer) repairs all of them at once.
    (3) *Passage color reciprocity.* After full-world compile, warn when a
    passage's destination board has no matching-color return passage
    (vanilla `BoardPassageTeleport` lands on the first color-matched
    passage, else the start square). This one is bucket 2: DETECT with a
    precise message routed to the LLM/plan repair — never re-color
    procedurally — and add the authoring rule to `ZWD.md` and
    `engine/promptkit_assets/spec.md` (wording already drafted in the
    Future Tasks bullet).
  This subsumes the *mechanism* of M12.13/M12.14 — implement those as the first
  fixers registered in the dispatch table rather than as standalone preprocessor
  special cases; whoever reaches the first of {M12.13, M12.14, M12.16} should
  build the framework here. Purely generation/compile-time — outside the sim;
  replay fixture unchanged. DoD: typed error codes; a fixer table with the
  bucket-1 fixers above; a table-driven test that feeds one broken board per
  bucket-1 error class and asserts it compiles after procedural repair with the
  expected diagnostic and **no LLM call**; a bucket-2 error is confirmed to fall
  through to the LLM path unchanged; `go test ./...` green. Consult the advisor
  on the error-code taxonomy and the bucket boundary before building.

- [x] **M12.15 [ADVISOR] — Offline "world-style adapter": mine every board of
  curated worlds into corpus-derived priors + retrieval few-shots (a LoRA we
  can't train, done as offline RAG).** (Landed via slices a/b/c: curation-first
  title-screen few-shots, static visual caption sidecars, and deterministic
  few-shot metadata + retrieval. Slice **d** — mined style priors — DEFERRED
  2026-07-12 by owner call as optional; retrieval few-shots already cover the
  prompt. See M12.15d below.) Owner framing (2026-07-11): we cannot
  fine-tune a closed API model, so the offline, no-LLM equivalent of a LoRA is
  *corpus-mined priors + retrieval-augmented few-shots* — everything a LoRA would
  bake into weights, we instead compute deterministically from real worlds and
  inject at prompt time (and, where it fits "derive don't require", bake into the
  compiler as defaults). Two problems with today's few-shot setup this fixes:
  the corpus samples only **2 boards per world** (`pickRepresentativeBoards`,
  `gen_llmworld_test.go:91`) and the prompt embeds **4 fixed single-board
  few-shots from older games** (`fewShotArchetypes`, `promptkit.go:41`;
  `SystemPrompt` at `promptkit.go:107`), so the model never sees how a whole world
  coheres (title→hub→branches, reciprocal exits, shared palette, a progression
  spine). All mining is pure Go over the decompiled corpus — **no LLM request
  anywhere in this task**. Raw material is all offline-available: `NeighborBoards`
  (`gamevars.go:87`) gives the exit graph; `DecompileZWDAuthorable`
  (`zwd_decompile.go`) gives every board's tiles/colors/stats/OOP.
  Build on M12.6's decompiler boundary: `DecompileZWDAuthorable` returns
  `[]ZWDDecompileDiagnostic` (`zwd_decompile.go:12`, `Severity` "warn"/"error")
  — the per-board "status updates" that flag safe lowerings vs. non-representable
  boards. Curation must consult these: prefer worlds that decompile with no
  `error`-severity diagnostics, and per phase 1 record/skip any board a world
  cannot express cleanly (do not silently emit invalid few-shot ZWD — the M12.3
  reason ONAMOON/OBELISK were dropped).
  Phases (may split into M12.15a–d):
  1. **Whole-world corpus.** A curated allowlist of well-constructed (prefer
     modern, not the oldest) exemplar worlds; decompile *every* board of each,
     grouped per world under `llmworld/worlds/<NAME>/` (all boards + a
     mechanically-derived plan: board table with names + the `NeighborBoards`
     graph, so the plan format M12.3a defined is grounded in real topologies).
     Extend/parametrize the M12.3 corpus generator rather than forking it.
     **Board-quality filter (required — the current selection actively picks the
     wrong boards).** `pickRepresentativeBoards` scores
     `nonEmpty + stats*25 + textCells*3 + colors*20`
     (`gen_llmworld_test.go:127`), which *rewards* exactly the boards we must
     exclude. Add a reject pass, all computed offline from the decompiled board,
     before scoring/sampling:
     * **Blank / border-only rooms** — a framed empty room (e.g. only a
       yellow/CP437 border, empty interior). Reject when interior (non-border)
       non-empty tiles fall below a floor, or one element/color dominates ~all
       tiles with ~0 stats.
     * **"Toolkit" / palette rooms** — an author's stash of one-of-every
       element/color for copy-paste. Reject on abnormally high *distinct* element
       and color counts combined with low spatial coherence: many singleton stat
       types (one of each creature/object), high per-tile variety / low
       contiguous-region structure — the tell that a board is a swatch sheet, not
       a scene. Note this is the direct opposite failure from blank rooms and is
       precisely what the `stats*25 + colors*20` terms currently over-reward.
     * **Otherwise low-value rooms** — boards that read as neither a composed
       scene nor authored content. Prefer a *balanced* signal (structure AND
       content AND some authored OOP/text) over any single dimension maxed out;
       a good board is not the densest or most colorful one.
     Make the thresholds explicit and documented; a test asserts a hand-built
     border-only board and a hand-built toolkit board are both rejected while a
     real composed board passes.
  2. **Mined priors (the "adapter weights").** Deterministic Go miners emitting
     compact structured artifacts: a **palette/tile codebook** (element+color
     frequencies, common wall/floor/shading pairings, text-lettering color
     conventions), **world-architecture stats** (board-count norms, topology
     shapes, exit-reciprocity, per-board object density, spine shapes), and an
     **OOP idiom library** (mined `#`-command n-grams + the recurring rituals).
     These regenerate STYLE.md's quantitative claims from data and can seed
     M12.13's synthesized-stat/color defaults.
  3. **Retrieval few-shots (the LoRA-equivalent core).** Tag every corpus board
     offline (archetype/theme/palette/density); at generation time
     *deterministically* select the few-shots nearest the requested premise/board
     concept (keyword/tag match — no LLM) instead of 4 static boards. Feeds the
     per-board painter (`generation.go` board request) and the plan step.
  4. **Whole-world cohesion example.** Because whole worlds are too large to embed
     verbatim (token/prompt-cache budget), teach cohesion with at least one
     *paired* exemplar: a real world's derived plan + a cohesive subset of its
     own boards (same world, so exits/palette actually line up), not unrelated
     single boards.
  5. **Visual captions (optional enrichment; one-time build step, not runtime).**
     The generation model never "understands" — its whole knowledge of a board is
     whatever fits in one bounded prompt, so its behavior is dictated by the few
     (limited) few-shot slots. Raw ZWD hides the *rendered* look behind legend
     indirection; a short visual caption packs design intent into each scarce slot
     (higher understanding-per-token) rather than adding more slots. Two parts:
     * **`Screen → PNG` renderer.** Render a decompiled board headlessly via the
       existing `Screen [80][25]{Ch,Color}` buffer with a CP437 font and the
       16-color DOS palette, producing a pixel-faithful board image. A reusable
       util (`cmd/` tool or exported func); this half is pure Go, no LLM.
     * **One-time offline captioning.** Feed the PNGs to a vision model *once at
       corpus-build time*, with a **structured** caption prompt (composition,
       palette, focal point, archetype, and a good/bad quality read), and commit
       the captions as static text sidecars per corpus board. This is the only
       LLM use in M12.15 and it is build-time, not per-generation — the runtime
       pipeline consumes the committed captions with zero LLM calls (the same
       shape as labeling LoRA training data offline). Cross-check each caption
       against the phase-2 deterministic tile stats to ground it and catch vision
       hallucination on abstract tile art; consistency matters more than prose.
     The captions feed phase 2 (compositional patterns the numbers can't express)
     and phase 3 (each retrieved few-shot carries a one-line "what this board is
     doing visually" annotation). Provider-agnostic; key via env, never in repo.
  Constraints: few-shots must be valid, recompilable ZWD (M12.6/M12.3); keep the
  prompt cacheable (mined artifacts are stable across a run; only the retrieved
  subset varies per request, so order it deterministically); **no LLM calls at
  generation runtime** — phase 5's captioning is a one-time corpus-build step
  whose output is committed static text; no sim changes; replay fixture
  unchanged. DoD (per phase): the whole-world corpus
  builds under `go test`; miners emit their artifacts and a test asserts non-empty
  data-grounded output; retrieval returns relevant few-shots for a sample premise;
  the prompt kit loads the new artifacts; the `Screen → PNG` renderer produces a
  pixel-faithful image for a known board (golden test) and captions load from
  committed sidecars; `go test ./...` green. Consult the
  advisor on the artifact shapes and the retrieval/budget design before building.

- [x] **M12.15a — Curation-first title-screen few-shots from hand-selected
  worlds.** The curation-first slice of M12.15 (taste over statistics — the
  `pickRepresentativeBoards` scorer picks toolkit rooms; the owner hand-picks
  excellent boards). Ten owner-selected worlds with excellent title screens /
  lettering (zips in `~/Downloads`: `winter`, `On_A_Distant_Moon`, `scorchede`,
  `SUDOKU`, `TROLLOL`, `THOUGHTS`, `zztv7`, `VARIETY`, `nyan`, `BUBLZ14c`); two
  carry photo-like pictorial art (Jean-Luc Picard, a cat) to seed **art
  examples** specifically. Pipeline: unzip → render each title screen (board 0)
  to PNG via a headless `Screen→PNG` renderer (M12.15 phase 5: CP437 font + DOS
  16-color palette, reusing the engine's own `TileToColorAndChar`) →
  vision-analyze lettering/art/palette/composition into a structured caption →
  decompile board 0 with `DecompileZWDAuthorable` and gate on clean recompile +
  M7.5 validate (the M12.6 boundary) → assemble matched (PNG, caption, ZWD)
  triples into candidate few-shots with archetype labels (lettering + art) for
  the promptkit. Hard gate: a few-shot MUST recompile or it teaches the model
  invalid tokens (the ONAMOON/OBELISK lesson, M12.3/M12.6). Do NOT commit
  `.ZZT` or zip files (gitignored; licenses vary, as with the rest of the
  corpus). DoD: a PNG + structured caption + clean-recompiling title-board ZWD
  for each usable world; the two photo-art worlds captured as art examples; a
  candidate one-shot assembled for owner review; `go test ./...` green; replay
  fixture unchanged.

- [x] **M12.15b — Static visual caption sidecars for curated few-shots.**
  Commit a structured, consistent caption alongside every registered curated
  example (title lettering, pictorial art, and gameplay scene): title/board,
  archetype, lettering or tile technique, palette families, composition/focal
  point, pictorial-art technique where applicable, and a compact prompt-facing
  summary. Store source sidecars under `llmworld/captions/` and embedded copies
  under `engine/promptkit_assets/captions/`; `LoadPromptKit` must load them and
  fail if any registered few-shot lacks one. Show each summary beside its ZWD
  example in the system prompt. Captions are authored/offline labels only — no
  runtime vision or LLM calls. Tests must guard source/embedded drift, complete
  coverage, JSON validity, and prompt inclusion; `go test ./...` green; replay
  fixture unchanged.

- [x] **M12.15c [ADVISOR] — Deterministic few-shot metadata and retrieval.**
  Tag every authorable corpus board offline with stable archetype/theme/palette/
  density metadata, then select a bounded, deterministic, premise-and-board-
  concept-relevant subset for generation instead of embedding every static
  example. Preserve prompt-cacheability: stable artifacts are loaded once and
  only the ordered retrieved subset varies per request. Include at least one
  cohesive same-world plan-plus-board subset. Tests cover deterministic ties,
  relevance for lettering/art/gameplay premises, budget enforcement, and the
  no-LLM runtime boundary.

- [ ] **M12.15d [ADVISOR] — Mined style priors (the offline adapter weights)
  (DEFERRED 2026-07-12 — later; optional/recommended, skip unless the owner
  asks for it). Owner call: unclear this is needed yet; retrieval few-shots
  (M12.15c) already cover the generation prompt, and mined priors would only
  regenerate STYLE.md's quantitative claims from data. Revisit if generation
  quality plateaus.**
  Mine deterministic compact artifacts from the authorable whole-world corpus:
  palette/tile frequencies and shading pairings, world-architecture/topology
  norms, and OOP command idioms. Version and embed the artifacts, expose them
  to the generation prompt as stable context, and test that each artifact is
  non-empty, data-grounded, deterministic, and regenerated from the corpus.
  No sim changes, no runtime LLM calls, replay fixture unchanged.

## M14 — Rearchitecting for the service ZZTMMO is becoming

Filed 2026-07-12 from a whole-repo review (NOTES.md): three structural debts
and one banked dividend, each with a mechanical, fixture-proven path. Execute
after the M12 generation batch and before M11 — M14.1 directly de-risks
M11's multi-world hosting. Every task here must leave the replay fixture
unchanged: these are ownership/plumbing changes, never simulation changes.

- [x] **M14.0 — World-scope state: one seam instead of scattered syncs.**
  The flag-propagation bug (fixed 2026-07-11, commit 67a642c) was patched by
  copying flags into each room's engine before it steps and publishing after
  (`room_manager.go:423-426`, `:473`, `syncWorldFlagsFromRoom` `:745`, and
  the freeze path `:737`). Correct, but it is a per-field dance: the NEXT
  world-scope field someone adds will silently not propagate, exactly as
  flags didn't. Steps:
  1. **Audit table first** (M8.2's deliverable shape, appended to NOTES.md):
     classify every `TWorldInfo` field (`gamevars.go:96-110`) as per-player
     (already virtualized through `PlayerState`, M2.1 — Health, Ammo, Gems,
     Keys, Torches, Score, EnergizerTicks...), world-scope (at minimum
     `Flags` `:107`; check `Name`, `IsSave` `:110`), or save-file-only
     (`CurrentBoard` `:100`, `BoardTimeSec` `:108` given per-player time
     limits). Cite the evidence per field — which code reads it from a room
     engine after load.
  2. **Consolidate the mechanism:** two named seams —
     `refreshRoomWorldScope(room)` before a room steps and
     `publishRoomWorldScope(room)` after — each iterating ONE explicit list
     of world-scope fields, so adding a field is one list entry, not four
     call sites. Route the freeze/thaw paths (`freezeRoomIfEmpty`,
     `syncFrozenBoardToLiveRooms`) through the same seams.
  3. Do NOT introduce shared pointers into engine state unless you first
     prove `StateHash` and `worldWriteTo` are byte-unchanged — value-copy at
     two seams is the recommended shape; rooms step sequentially on one
     goroutine inside `StepDiffs`, so copies are race-free by construction.
  DoD: the table in NOTES.md; `world_flags_test.go` extended — a flag set by
  a room is visible to a later-ticking room the SAME tick, and flags survive
  a freeze/thaw cycle; `go test ./...` green; replay fixture unchanged.

- [x] **M14.1 — One instance model: retire the DefaultInstance special case;
  mint server-scoped PlayerIDs.** The boot world predates `Instances` and
  kept parallel bookkeeping: `s.clients`/`s.inputs` mirror the default
  instance only (double-written in the join path
  `websocket_server.go:312-316` and in `setInput` `:1002`/`:1013`), leaves
  have twin paths (`removeClient` `:1027` vs `removeClientFromInstance`
  `:1038`), most `submit*` helpers have an `...InInstance` twin
  (`:665`/`:675`, `:688`/`:705`, `:733`/`:757`), and there are two tick
  bodies (`WebSocketServer.Tick` `:113` vs `WorldInstance.Tick` `:185`).
  Separately, `PlayerID`s are minted per-`RoomManager`
  (`room_manager.go:17`, `:229`), so ids collide across instances — the
  blocker M4.3 recorded against multi-world world-select, and a standing
  trap for any server-wide map keyed by bare PlayerID. Steps, mechanical and
  in order:
  1. Make the boot world a normal `Instances` entry; `DefaultInstance`
     becomes a pointer into the map (keep the field so callers don't churn).
  2. Delete the mirrors and twins: one join path, one leave path, one
     setInput, one submit* family, one Tick body. Grep-proof: `removeClient\b`
     gone; no `inst.RoomManager == s.RoomManager` special-casing remains.
  3. Mint PlayerID from a server-scoped counter under `s.mu`, passed into
     `JoinPlayer` (or a `JoinPlayerWithID` variant) so every id is
     process-unique. The client treats the id as opaque — verify no Go test
     or TS code assumes ids start at 1 per world; update the ones that do.
  DoD: a test hosts two instances simultaneously with interleaved joins and
  asserts disjoint PlayerIDs; all existing WebSocket/editor/chat tests
  green; the grep proofs pass; replay fixture unchanged. Directly de-risks
  M11.1 (Museum worlds are instances) and simplifies M13.4 if that hasn't
  landed yet.

- [x] **M14.2 — Session recording: bank the determinism dividend.** A
  complete session is `{world identity, seeds, per-tick inputs and
  submits}` — kilobytes. This is the foundation for shareable replays,
  ghost racing, the daily challenge, and verified leaderboards (idea
  backlog), and M7.3 already centralized every submit into pending queues
  whose drain is the natural log point.
  * **Record at the tick boundary.** In the (post-M14.1, single) instance
    Tick: append one JSON line per tick —
    `{tick, inputs: {playerID: input}, joins: [{playerID, board, name}],
    leaves: [playerID], submits: [...]}` — where `submits` is everything
    applied that tick (scroll replies, save names, quit replies, debug
    commands: the M7.3 queues). Header line: world name, FNV-1a of the
    world bytes, and every seed involved — find them ALL by grepping
    `RandomSeed`/`RandSeed` across engine and server; each room engine's
    seed at creation is part of the record or playback diverges. If any
    seed on the server path currently derives from wall-clock, route it
    through the recorder so the header captures it.
  * **Write behind a flag** (`-record <dir>` on `cmd/zzt-server`), one file
    per instance session, buffered writes, never blocking the tick (on
    backpressure drop-and-count, log the count). Recording off = byte-for-
    byte today's behavior.
  * **Playback.** `cmd/zzt-replay <file>`: reconstruct the RoomManager from
    the recorded world + seeds, apply the log tick by tick through the SAME
    entry points (`JoinPlayer`, `StepDiffs`, `Submit*`, `LeavePlayer`).
  DoD: a scripted two-player session (join, move, buy from the TOWN vendor
  via scroll reply, transfer boards, one quits) recorded and played back
  headless reproduces per-room `StateHash` at every 100 ticks and at the
  end; recording disabled changes nothing; `go test ./...` green; replay
  fixture unchanged.

- [ ] **M14.3 — Package split, smallest honest cut (OPTIONAL — skip unless
  the single package is actually hurting, and record the decision either
  way).** Everything lives in one ~29k-line `package zztgo`: sim,
  serializer, ZWD compiler, generation service, prompt kit, room manager,
  WebSocket server, editor sessions, web API. The tempting cut — extract
  `worldgen` (zwd.go, zwd_decompile.go, generation.go, promptkit.go +
  assets, plan.go) — does NOT stand alone: worldgen needs engine types
  (`TWorld`, `ElementDefs`, `BoardClose`) while `web_api.go`/
  `websocket_server.go` (which would stay) call worldgen, so the parent
  would import its own child that imports it back — a cycle. The smallest
  honest cut is therefore TWO extractions in one task: `worldgen` AND
  `server` (`websocket_server.go`, `web_api.go`, and whatever import
  analysis proves must follow; `protocol.go` and `room_manager.go` stay
  with the engine — `RoomManager` returns protocol types and touches Engine
  internals). Rules if executed: purely mechanical — no renames beyond
  package qualifiers, no signature changes, test files move with their
  code, `// ZZT-QUIRK` comments untouched; CLAUDE.md rule 4 applies in
  full. **Stop signal:** if the split forces exporting more than a handful
  of previously-unexported identifiers, STOP, revert, and record in
  NOTES.md that the cut is wrong — that friction is the package boundary
  telling you where it wants to be.
  DoD (if executed): `go build ./... && go test ./...` green; `git diff
  --stat` shows moves and qualifier edits only; replay fixture unchanged.
  DoD (if skipped): a NOTES.md entry saying why the single package still
  isn't hurting, so the next audit doesn't re-litigate from scratch.

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

- [x] **M5.2 — Board and world property dialogs.** `EditorEditBoardInfo`
  (`editor.go:204-288`, `EDITOR.PAS:247-351`): board title, max player shots,
  dark, the four exits (targets picked via `EditorSelectBoard`,
  `editor.go:866`), reenter-when-zapped, time limit; plus the world name.
  Build the dialogs on the M4.1 window system.
  DoD: set dark, a time limit, and an exit in the browser editor; save; play
  the world in a live room — all three take effect.

- [x] **M5.3 — Stat parameter editing.** `EditorEditStat` /
  `EditorEditStatSettings` (`editor.go:315-417`, `EDITOR.PAS:396-527`):
  per-element parameter dialogs — P1/P2/P3 with their
  `ParamTextName`/`Param1Name`/`Param2Name`/`ParamDirName`/`ParamBoardName`
  meanings from `ElementDefs`, step/direction pickers, cycle, and vanilla's
  bind behavior for objects. Centipede Follower/Leader stay untouched by the
  dialog, as in vanilla.
  DoD: edit a spinning gun's firing rate, a passage's destination board, and
  an object's cycle in the browser; each behaves accordingly in play.

- [x] **M5.4 — Object code editor.** `EditorEditStatText` /
  `EditorOpenEditTextWindow` (`editor.go:289-314,819-835`,
  `EDITOR.PAS:352-395`): a CP437 multi-line text editor in the browser (build
  on the M4.1 window layer and the M6.1 input-capture mode) for object and
  scroll text, preserving vanilla's line/`@name`/`#`/`:label` conventions and
  the `DataLen` bookkeeping on save.
  DoD: rewrite the TOWN vendor's script in the browser, save, play — the new
  dialogue runs; the text round-trips through the serializer.

- [x] **M5.5 — Board management and transfer.** `EditorAppendBoard` /
  `EditorSelectBoard` (`editor.go:21-38,866-889`, `EDITOR.PAS:51-70`) for
  adding, switching, and naming boards — and port the one dropped procedure:
  `EditorTransferBoard` (TODO stub at `editor.go:422-479`,
  `EDITOR.PAS:528-587`) as browser import/export of a single board (file
  download/upload of the vanilla board format; names through
  `SanitizeSaveName`).
  DoD: create a second board, link exits both ways, walk between them in
  play; export a board and re-import it into another world.

- [x] **M5.6 — Save, host, and publish edited worlds.** Save the session
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

- [x] **M5.7 — ZZT-OOP authoring aids.** Label navigation and validation in
  the code editor: use the real tokenizer semantics (`OopParseWord` and
  friends in `oop.go` — do not write a second parser) to list `:labels`,
  flag `#send`s to labels that don't exist in the target object, and warn on
  unknown `#commands`. Purely advisory — never block a save; vanilla accepts
  anything and worlds rely on that.
  DoD: the label list and warnings render for the vendor object; a send to a
  missing label warns; saving an "invalid" script still succeeds.

- [x] **M5.8 [ADVISOR] — Full editor feature and UI parity with the original.**
  M5.0–M5.7 built the browser editor capability by capability; this task closes
  the remaining gap to the *original* DOS ZZT editor so a veteran author feels
  no missing surface. Reference is the whole modal editor — `EditorLoop`
  (`editor.go:39-818`) and `reference/reconstruction-of-zzt/SRC/EDITOR.PAS` —
  read top to bottom and diff every interaction and every drawn cell against the
  browser editor; the advisor consult must agree the gap list is complete before
  coding. Known parity surface to audit (extend as the diff finds more):
  * **Element menus and category cheatsheets.** The Item/Creature/Terrain
    selection menus and the F1–F4 element category tables
    (`EditorDrawSidebar`/`EditorDrawTileAndNeighborsAt`, the menu lines in
    `EDITOR.PAS:89-186`) — every placeable element reachable by its original
    keystroke, in the original grouping and order.
  * **Sidebar/status layout.** The editor sidebar must match the DOS editor
    cell-for-cell (pattern row, color picker with fg/bg + blink, mode
    indicators, coordinate/element readout), rendered with CP437 glyphs and DOS
    colors like the play sidebar (M3.8/M4.0), not a web-styled panel.
  * **Color selection UI.** The full 16 fg × 8 bg + blink selector and the
    "P" pattern/plot-mode and draw-mode toggles as vanilla presents them.
  * **Menu bar / help.** The editor's top menu and in-editor help (`H`) pages,
    and any keystroke the terminal editor answers that the browser drops
    (grep `EditorLoop`'s key switch, mirror M4.2's "every row has a test").
  * **Keyboard parity.** Arrow/shift-arrow paint, Tab draw toggle, Enter
    copy-tile, Insert/Delete, the `X`/`Y` and gradient helpers, board list
    navigation — the complete `EditorLoop` vocabulary, routed through the M5.0
    session apply path (never mutating a ticked room).
  Faithful port only (CLAUDE.md rules): the editor session stays server-side and
  never-ticked, the sim is untouched, and no new UI invents behavior the DOS
  editor lacks. DoD: a documented feature-by-feature parity checklist (this task
  produces it, e.g. in NOTES.md) with every row implemented or explicitly
  deferred with a reason; a browser author can build a TOWN-class world using
  only original-editor muscle memory; protocol-level tests per new keystroke;
  `go test ./...` and replay green.

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

- [x] **M8.1 — Point-blank shots must respect the *target* player's
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

- [x] **M8.2 — Sweep the remaining single-player assumptions.** M8.1's bug was
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

- [x] **M9.1 — Board-change transition fade.** Vanilla covers every board
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

- [x] **M6.2 — Google OAuth authentication.** Replace MVP player numbers with
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

**Moonshots, second batch (2026-07-12 — what the M12 generation machinery
newly enables; same rule: backlog bullets, owner promotes before spec):**
* **The Endless Dungeon — generation as geography.** When a player walks off
  the mapped edge of a designated frontier world, the server dreams the next
  board *in place* and it becomes permanent shared geography — the Continent
  idea, but grown by exploration instead of stitched from the Museum.
  Feasible because M12.4 already paints boards against the edge rows of
  adjacent boards and validates/hosts on the fly; wants M12.16's yield work
  first so a frontier board can never come back blank.
* **The critique flywheel — generate → render → score → keep.** `cmd/zzt-shot`
  plus the M12.15 caption pipeline closes an evaluation loop: generate K
  candidates per board, render each to PNG, have a vision model score them
  against the plan, keep the best, and feed accepted worlds back into the
  retrieval corpus. The compiler made correctness verifiable; this makes
  QUALITY verifiable — a self-improving generator with no fine-tuning.
* **In-world dreaming — the Dream Machine.** Put generation inside the lobby
  world: an object you touch, a scroll you type the prompt into (M6.1 input
  capture + the scroll-reply seam), and on success a cross-world passage
  materializes beside it leading into your world. Generation stops being a
  menu and becomes a place; composes with the lobby world's
  server-interpreted passages below.
* **Style séances — remix instead of create.** Decompile → LLM transform →
  recompile: "make TOWN haunted", era/author style packs mined from the
  Museum corpus (M12.15c tags), a sequel seeded from what you actually did
  (the session log, once M14.2 records it). The decompiler/compiler round
  trip is exactly this machine.
* **Daily Dreamed Challenge.** One generated world per day, identical seed
  for everyone, server-verified completion times, one leaderboard — combines
  the codebase's two strongest properties (deterministic replay + instant
  world generation) into the strongest retention mechanic in its class.
* **The AI Dungeon Master.** An LLM watches the session event stream (M14.2)
  and edits ahead of the party through the editor-session apply path and M10
  leases — drops monsters, rewrites the vendor's lines, opens a wall. Every
  action rides the same compiled/validated seams as a human editor, so
  determinism and the security boundary are untouched.

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

* *(Folded into M12.16 on 2026-07-12 — execute there, not from this bullet.)*
  **Compiler: auto-generate Passage stats from the legend `to "BOARD"` clause.**
  A `Passage` is stat-backed, so today every passage tile needs an explicit
  `stat at X,Y ... p3 board "…"`. LLM-generated worlds routinely draw a passage glyph
  with `p = Passage color 0xNN to "BOARD"` in the legend and omit the stat, producing
  `grid contains stat-backed element Passage but no matching stat is defined`. The
  legend already carries the destination — the compiler should synthesize one passage
  stat per matching tile when the legend entry has a `to` destination.

* *(Folded into M12.16 on 2026-07-12 — execute there, not from this bullet.)*
  **Compiler: report all orphan / decorative stat-backed tiles in one pass.**
  `compileBoard` returns on the *first* orphan stat-backed tile (`engine/zwd.go`
  ~line 790). The generation-repair loop then fixes one tile, recompiles, and hits the
  next — O(n) round-trips for n mistakes. Collect and report every offending
  `(element, col, row)` in a single error so one repair round can fix them all. (A
  temporary `debugOrphanScan` pass during the touch investigation surfaced e.g.
  DYINGSTA's 5-tile passage "door" and KEEPLITE's 13-tile object drawing at once.)

* *(Folded into M13.0 on 2026-07-12 — execute there, not from this bullet.)*
  **Retire/retitle `engine/touch_race_test.go`.**
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

* *(Folded into M12.16 on 2026-07-12 — execute there, not from this bullet.)*
  **Passages must link to a matching-color passage on the destination board.**
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
