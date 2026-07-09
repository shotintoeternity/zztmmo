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

## M5 — Creation and full-featured ZZT tooling

Goal: support the creation features that make ZZT “full ZZT,” not only runtime
playback.

- [ ] **M5.0 — Browser board editor shell.** Bring up a browser editor surface
  using the same CP437 renderer and modal UI system, initially read-only with
  cursor movement and tile inspection. DoD: a user can open a board in editor
  mode and inspect tiles/stats in the browser.

- [ ] **M5.1 — Editable boards and object properties.** Implement tile
  placement, color/pattern selection, stat parameter editing, board properties,
  and object text editing. DoD: edits round-trip through the existing ZZT board
  serializer and reload correctly.

- [ ] **M5.2 — ZZT-OOP authoring workflow.** Add browser editing for object
  code with label navigation, validation helpers, and save/apply behavior. DoD:
  creating or modifying an object script changes runtime behavior after reload.

- [ ] **M5.3 — World persistence and publishing.** Add save/export/import paths
  for edited worlds and multiplayer-safe persistence. DoD: a browser-created
  world can be saved, reloaded, exported, and hosted for other clients.

## M6 — Chat and identity

Goal: players in a server can talk to each other in a ZZT-styled chat box, and
eventually do so under a real account name.

**Hard constraint:** chat is presentation and networking only. No chat state may
enter the simulation — not the `Engine`, not `StateHash`, not the replay path.
Chat lives beside the engine in the server (`RoomManager`/`WebSocketServer`), so
determinism (CLAUDE.md rule 2) and the replay fixtures are unaffected.

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

- [ ] **M6.3 — Chat and account persistence.** Add a backend database (accounts,
  display names, chat history) behind a narrow storage interface so tests can
  use an in-memory implementation. Persist chat with timestamps; serve recent
  scrollback to a joining client. DoD: chat history survives a server restart,
  and the storage interface has an in-memory fake used by tests.

## Future Tasks & Community Additions

- [x] **Troubleshoot player stuck after damage.** Solve the issue where players get stuck after being zapped/damaged by a ruffian/bear due to stat index shift misalignment in RoomManager.
- [ ] **Prepare next batch of ZZT games.** Add download paths, scripts, or direct curl pipelines for the rest of the requested ZZT games (Teen Priest, Teen Priest 2, Inedible Vomit, other bongo/wynand games, Freedom, Apparitions of the City by kev-san, etc.) to get them ready for deployment.
- [ ] **Torches only illuminate player's path.** Fix the torch illumination logic so that torches correctly light up the surroundings rather than just the player's path.
- [ ] **Title screens aren't animating properly.** Investigate and resolve the issue where object scripts and movements on ZZT title screens do not animate or tick as they should.
