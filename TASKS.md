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

- [ ] **M3.9 — Browser debug/help windows.** Restore browser access to modal
  keyboard flows such as the `?` debug window, help screens, and other legacy
  text-window prompts through protocol events and client-side CP437 windows.
  DoD: pressing `?` in the browser opens an interactive debug/help-style window
  without disconnecting or corrupting gameplay input.

- [ ] **M3.10 — ZZT scroll message windows.** Render all `scroll` events from
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

## M4 — Browser ZZT UI and playability parity

Goal: the browser client should feel like complete ZZT, not just a multiplayer
terminal viewport. Preserve original CP437/DOS presentation and interaction
semantics while keeping the server authoritative.

- [ ] **M4.0 — ZZT screen shell parity.** Render the board and sidebar as the
  original 60x25 board plus 20x25 sidebar, using CP437 cells, DOS colors, blink
  behavior where relevant, and fixed text-mode geometry. DoD: TOWN in the
  browser visually matches the original board/sidebar split with no temporary
  web-dashboard panels.

- [ ] **M4.1 — Modal text-window system.** Implement the reusable browser
  CP437 text-window layer used by scrolls, help, prompts, high scores, debug
  windows, save/load confirmations, and editor dialogs. DoD: a single renderer
  and input router handles read-only text, selectable links, yes/no prompts,
  text entry, and paged help without gameplay input leaking through.

- [ ] **M4.2 — Full keyboard/control parity.** Route all original play-mode
  keys through the protocol: movement, shooting, torch, pause, save, quit, help,
  debug, sound toggle, and text-window navigation. DoD: browser key behavior
  matches the terminal client for common TOWN flows.

- [ ] **M4.3 — Title, world, save, and high-score flows.** Restore the original
  non-gameplay UI paths in the browser, including title screen/start flow,
  save/load prompts, quit confirmation, and high-score entry/display. DoD: a
  player can start, save, load, quit, die, enter a score, and restart without
  falling back to terminal-only UI.

- [ ] **M4.4 — Browser sound synthesis.** Convert `SoundEvent` notes into
  WebAudio playback with priority/queue behavior close to ZZT, plus a visible
  sound toggle in the authentic sidebar. DoD: pickups, shots, doors, damage,
  and object sounds are audible and can be toggled.

- [ ] **M4.5 — Rendering compatibility pass.** Audit CP437 glyphs, colors,
  dark-room/torch behavior, player blinking, text elements, transition effects,
  and dirty-cell updates against terminal output. DoD: a scripted visual smoke
  covers TOWN landmarks, dark rooms, torches, gems, passages, text signs, and
  player damage/respawn without stale cells or missing glyphs.

- [ ] **M4.6 — Full TOWN playthrough smoke.** Add a scripted or semi-scripted
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

