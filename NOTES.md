# NOTES — escalations and decisions log (append-only)

## M5.10 (2026-07-13) — editor sidebar parity: full audit + close

Completed the popup audit the first slice began. Classified every editor-reachable
modal open in `main.ts` against `EDITOR.PAS`, using the rule from the first pass:
vanilla *sidebar prompts* (`SidebarPrompt{YesNo,String,Choice}`, and the row-3..20
F-key element list) must render in editor chrome; vanilla *text windows*
(`TextWindowState` centered boxes — `EditorEditBoardInfo`, `EditorSelectBoard`,
`TextWindowDisplayFile`) legitimately stay on the text-window layer. The parity
checklist, each editor mode vs. the original:

| Editor mode / interaction | EDITOR.PAS form | Browser now | Verdict |
|---|---|---|---|
| Command block + Pos/Color/Mode/element readouts | sidebar (`EditorDrawSidebar`) | `drawEditorSidebar` | parity |
| F1/F2/F3 element category picker | sidebar rows 3–20 + `InputReadWaitKey` (808–842) | `editorCategoryMenu` | parity (M5.9) |
| Stat param editing `EditorEditStat` | sidebar clear + rows 6–7 name + slider/char/choice at 9/13/17 | `editorStatPrompt` | parity (first pass) |
| Object/scroll code editor `EditorEditStatText` | text window edit | `programEditor` modal | parity |
| Transfer board import/export | `SidebarPromptChoice(true,3,…)` | `openEditorSidebarMenu` → `actionMenu` | parity (first pass) |
| Clear board / Make new world | `SidebarPromptYesNo` | `openYesNo` (renders at 63,5) | parity |
| Max shots / Time limit / Save filename | `SidebarPromptString` | `openEntry` (label→col 75 r3, field 63,5) | parity |
| Board Information (`I`) | **text window** `EditorEditBoardInfo` | `openSelectList` | parity (text window is vanilla) |
| Switch boards (`B`) / exit picker / stat P3 board | **text window** `EditorSelectBoard` | `openSelectList` | parity (text window is vanilla) |
| Editor help (`H`) → `EDITOR.HLP` | **text window** `TextWindowDisplayFile` | `fetchLines` → `openWindow` | parity of the *window*; its `!-FILE` links are dead — split out as **M5.12** |
| "World:" menu (`S`) publish/download/upload/invite | *no vanilla equivalent* (vanilla `S` = save-filename prompt) | `openSelectList` | client-only multiplayer surface, kept as a text-window menu |
| Lease-conflict / read-only / save-result "Ok" dialogs | *no vanilla equivalent* (vanilla `PauseOnError`) | `openSelectList` | client-only, acceptable |

Conclusion: **no vanilla sidebar interaction is rendered as a scroll/text-window
popup** — the first pass had already moved the two that were (`EditorEditStat`,
`EditorTransferBoard`). Two intentional non-issues noted, not fixed (neither is a
"sidebar-as-popup" defect): the board-title prompt uses the sidebar string field
where vanilla uses `PopupPromptString` (a box), and `B`→Add-new-board asks a room
title via a popup where vanilla's `EditorAppendBoard` takes a default name. The
dead `.HLP` cross-links inside the editor help window are a real functional bug,
carved out as its own task (M5.12) rather than folded in here.

Added readout state-transition coverage to `editor.test.mjs` (color name, Pos,
hovered element, and the over-a-stat swap to "x,y Stat N: P1/P2/P3" + Space→"Edit
stat"), joining the existing menu/mode/category/action/stat-prompt assertions.
Verified: `npm test` (10 web suites), `npm run build`, `go build ./...`,
`go test ./...` all green; TS-only, outside the sim, replay fixture unchanged.

## M5.10 (2026-07-13) — first editor sidebar parity slice

Started the M5.10 popup audit by splitting editor interactions into two buckets:
true text windows stay on the text-window layer, while original sidebar prompts
must render in the right editor chrome. `EDITOR.PAS` keeps Board Information and
EditorSelectBoard as text windows, so those remain `openSelectList` paths for
now. EditorEditStat is not a selectable "Object settings" menu: vanilla clears
the sidebar, writes category/name at rows 6-7, paints slider/character/choice
prompts at rows 9/13/17, and advances through them sequentially. The browser now
follows that flow, with object/scroll program editing still opening the true text
window editor at the right point. EditorTransferBoard's import/export prompt now
uses `SidebarPromptChoice`-style sidebar chrome. Board/file browser operations
still need the broader M5.10 screenshot checklist before the task can close.

## M6.4 (2026-07-13) — account player-state scope

DECISION. Account-keyed player state is scoped by `(accountID, worldName)`, not
by snapshot. A signed-in player who leaves TOWN and later rejoins TOWN gets the
same inventory/counters restored before the first tick; the same account joining
a different hosted world starts from that world's saved account state, or fresh
if none exists.

Concurrent sessions for one account are allowed for now. The server does not
kick older sessions or merge live inventories; each authenticated disconnect or
manual save writes that account/world state, so the last write wins. This keeps
M6.4 narrow and leaves stronger single-session enforcement for ownership/invite
work if it becomes necessary.

## M12.4b (2026-07-11) — The "DAEKEPERT" title screen mystery & ZZT-OOP touch race

* **DAEKEPERT Title Screen Lesson:** 
  The LLM world generation was prompted for a game named "The Keeper's Light" but produced a title screen spelling "DAEKEPERT" (or "DAE KEPER T"). The root cause was a combination of:
  1. **Legend/Grid key collision**: The grid used `T` for block-letter lines. But in the legend it defined `T = Solid color 0x08` (gray solid walls) instead of text elements. As a result, the block letter `T` was drawn using solid walls as a hollow rectangular box (a `TTT` / `T.T` / `TTT` grid layout), which visually looked like a `D` or `O`.
  2. **Bad block font layouts**: The letter `H` was drawn in the grid with a top bar (`HHH`), turning it into a block-letter `A`. The rest of the letters `E K E P E R` were correctly drawn using yellow text elements (`Text-Yellow color 0x20` which compiles to blank space blocks with yellow background), but a trailing `T` was added. Together, `T H E K E E P E R T` rendered as `D A E K E P E R T`.
  3. **Lesson for future prompts**: When prompting LLMs for ZZT block letters, strictly instruct them that:
     - All characters used for lettering in the grid must map to `Text-<Color>` elements in the legend (not solid walls, normal walls, or objects).
     - Standardize text color mapping to use character ASCII codes (e.g. `H = Text-Yellow color 0x48`) instead of spaces (`color 0x20`), or ensure they do not collide with wall keys.
     - Double-check the exact grid font layout for letters like `T` (vertical bar centered) and `H` (no top crossbar).

* **Orphan element compile gap & engine panic:**
  The generated "Lamp Room" board included decorative `o` and `X` tiles mapped to `E_OBJECT` (element 36) in the legend to draw the lighthouse bulb housing, but did not define matching `stats` entries for all of them. 
  - **The Compile Gap**: The ZWD compiler `zwd.go` checks that every listed stat has a matching grid tile, but it does NOT check the reverse (i.e. that every grid tile of type `E_OBJECT` or `E_PASSAGE` has a corresponding stat). The world compiled cleanly but contained orphan `E_OBJECT` tiles.
  - **The Engine Panic**: On player join/room transition, `TileToColorAndChar` called `ElementObjectDraw` on the orphan object tiles. `elements.go:883` tries to read `P1` of `e.Board.Stats[e.GetStatIdAt(x, y)]`. Because there was no stat, `GetStatIdAt` returned `-1`, causing an `index out of range [-1]` panic that crashed the server.
  - **Decision**: Added a task to make the engine draw/touch procs robust (handling `-1` gracefully by falling back to `ElementDefs[E_OBJECT].Character`) and added a compiler check enforcing that all stat-backed tiles on the grid must have stats.

- 2026-07-09: Project scaffolded. Baseline `engine/` (vendored from
  benhoyt/zztgo @ master, MIT) builds and passes its tests on go1.26.5,
  macOS arm64. Reference clones are gitignored; re-clone per CLAUDE.md if
  missing.

- 2026-07-09 (M0.4): The task/DoD note "SoundHasTimeElapsed still paces
  interactive mode" is inaccurate for the current code: zztgo stubs
  `SoundHasTimeElapsed` to `return true` (sounds.go), with the real timer logic
  commented out. The ONLY thing pacing interactive `GamePlayLoop` is the
  `time.Sleep(TickTimeDuration*10 ms)` at game.go:1490. So literally deleting
  that sleep would make interactive play run uncapped (DoD "interactive speed
  unchanged" would fail). DECISION: route the per-cycle pace through `Delay()`
  (removing the raw `time.Sleep` + `time` import from game.go) and make `Delay`
  a no-op when `Headless`. Interactive sleeps exactly as before; headless runs
  flat out. Fully moving the pace out to an interactive wrapper (so GameStep
  never paces) lands in M0.5. If/when `SoundHasTimeElapsed` gets real timing,
  revisit whether the Delay-based pace is still needed.

- 2026-07-09 (M0.5, [ADVISOR]): The advisor tool was unavailable this session
  (infra error on every call), so the required pre-edit advisor consult could
  NOT be done. Proceeded with user pre-authorization ("proceed with extra
  care"). Mitigation: cross-checked the extraction against GAME.PAS (1518-1590)
  before writing. Design: GameStep iterates the GLOBAL CurrentStatTicked
  (RemoveStat/DamageStat decrement it — GAME.PAS 942-943, 1233-1234), ticks the
  pending stats for the cycle, then advances CurrentTick (wrap >420->1), resets
  CurrentStatTicked, and calls InputUpdate. The wrapper is "pace (Delay), then
  if SoundHasTimeElapsed: GameStep()". The pause branch stays verbatim in the
  wrapper; the shared "all stats ticked" block folded into GameStep. The
  one-time advance the pause path used to get is reproduced by the next
  GameStep, and CurrentTick is re-randomized on unpause anyway, so the
  transition is unchanged. NOT hash-verified yet — the M0.6 replay fixture
  (next task) is what locks this pure; a manual TOWN.ZZT playtest is the interim
  DoD check. If M0.6's fixture reveals drift here, revisit GameStep.

- 2026-07-09 (M0.5 verification): Manual A/B playtest done — M0.4 binary vs
  M0.5 binary on TOWN.ZZT, side by side. Movement speed, monster behavior,
  the start-of-board pause/blink, and scroll open/close all feel identical.
  M0.5's interactive DoD ("feels identical") is MET. Still to be hash-locked by
  M0.6.

- 2026-07-09 (HANDOFF — read before continuing M0): M0.1–M0.5 are complete,
  committed, and green (`cd engine && go build ./... && go test ./...`). Next
  unchecked task is M0.6 (replay harness + fixtures). Seams now in place for
  M0.6 to build on:
    * `Headless bool` (video.go) — true = no terminal, no sleeps.
    * `Screen [80][25]{Ch,Color}` buffer + present_tcell.go presenter (M0.2).
    * `InputSource` interface + `ScriptedInput{Ticks,Pos}` + `SetInputSource`
      (input.go, M0.3) — drive input with no keyboard.
    * `RandSeed uint32` + `RandomSeed(s)` (lib.go, M0.1) — seed determinism.
    * `GameStep()` (game.go, M0.5) — one cycle, headless-callable in a loop.
    * `Delay()` is a no-op when Headless (M0.4).
  M0.6 recipe (from TASKS.md): new engine/replay_test.go with StateHash() =
  FNV-1a over Board.Tiles + all Stats fields + World.Info + RandSeed; then
  RandomSeed(42), load TOWN.ZZT, start play (GameStateElement=E_PLAYER,
  unpaused), drive 600 GameSteps with a fixed ScriptedInput, record StateHash
  every 100 steps into fixtures/town.replay.json (write if absent, compare if
  present); run twice to prove determinism. From then on it gates every commit.
  Note: TOWN.ZZT is present in engine/ but is NOT git-tracked (untracked working
  file) — the test loads it from the engine working dir.
  PROCESS: the advisor tool was unavailable for this entire session (infra
  error on every call). M0.6 is not [ADVISOR], but M1.3 and M2.2 are — try the
  advisor first, and if it is still down, escalate per CLAUDE.md rule 5 or get
  explicit user pre-authorization before editing (as was done for M0.5).

## M3.9 (2026-07-09) — the '?' key could hang the whole server

Found while starting M3.9: `ElementPlayerTick` case '?' called
`GameDebugPrompt`, which calls `PromptString`, which loops on
`InputReadWaitKey()`. That blocks on the package-global `keyChan`, fed only by
the tcell keyboard. In a headless room engine nothing ever feeds it, so a single
browser client pressing '?' would have blocked `RoomManager.StepDiffs` forever —
freezing every room and every player, not just the sender. M1.4's grep for
"zero TextWindow/SidebarPrompt/InputReadWaitKey calls in sim code" missed it
because the call site says `GameDebugPrompt`, not `PromptString`.

Fix follows the M1.3 scroll pattern: the sim emits `DebugPromptEvent{StatId}`
and returns; the caller collects the typed text and replies via
`Engine.SubmitDebugCommand(statId, text)`, applied at the top of the next
`GameStepWithInputs`. `GameDebugPrompt` (terminal/editor) keeps the modal
`PromptString` and now delegates to the new `GameApplyDebugCommand(statId,
input)`, which is the old body with `PlayerFor(0)`/`Stats[0]` replaced by the
triggering player — vanilla always meant stat 0, but a cheat must credit
whoever typed it.

Timing deviation: '?' now applies on the NEXT step rather than mid-tick. Same
class as the M1.3 scroll deviation. Replay fixture unchanged (the scripted
replay input never presses '?' or 'H'), so no `DEVIATION:` tag was required.

Also: `HelpEvent` gained `StatId`. Room events are broadcast to everyone on the
board, so without it player A pressing 'H' opened a help window on player B's
screen. `QuitPromptEvent` still has no StatId and still reads `PlayerFor(0)` —
left alone, it belongs to M4.3's quit/save/high-score pass.

The .HLP files are now git-tracked (`engine/*.HLP`), byte-identical to
`reference/reconstruction-of-zzt/DOC/*.HLP` (MIT, Epic's permission included) —
same provenance and precedent as the already-tracked `fixtures/TOWN.ZZT`. The
server reads them via the new `-help` flag / `zztgo.HelpDir`; the sim never
touches the filesystem, `HelpFileLines` runs on the protocol boundary.

## M3.10 (2026-07-09) — who is a scroll for?

`ScrollEvent.StatId` is the OBJECT running the ZZT-OOP code, because that is the
target `OopSend` needs for a hyperlink reply. It is not the player. Room events
are broadcast to everyone on the board, so without a second identity every
player on the board would get a modal window when one of them talked to the
vendor.

The toucher is not available where the scroll is emitted: `ElementObjectTouch`
only does `OopSend(-statId, "TOUCH")`, and the object runs that code on its own
later tick, by which point the call stack no longer knows who knocked. So the
touch procs now record it in `Engine.ScrollAudience[objectStatId] =
playerStatId + 1` (0 = nobody), consumed by `OopExecute` when it emits the
event. It is presentation routing only — never simulation state, absent from
StateHash — but it is indexed by stat id, so `reindexScrollAudienceAfterStatRemoval`
keeps both its keys and its values in step with the stat array on RemoveStat,
exactly as the Follower/Leader fixup does, and `BoardOpen` clears it.

`PlayerStatId = -1` means an object opened a scroll from its own code rather
than from a touch; those are shown to everybody, as in vanilla.

`PendingScrollReply`/`PendingScrollStatId` became the queue
`PendingScrollReplies`, since several players can now close a scroll on the same
tick. The terminal client submits through the same `SubmitScrollReply`.

Known gap, deliberately left: `!-FILE;text` lines (a hyperlink that opens
another help file rather than sending a label) are inert — `hyperlinkOf` returns
"" and Enter just closes the window. TOWN's scrolls do not use them. Wiring
them needs a client→server "open this help file" request; it belongs with M4.1's
text-window system.

Vendor is on TOWN board 2 ("Armory"), stat 2, at (21,9). Note that several other
boards also have a stat 2 whose `Data` string contains the vendor's code — the
element there is not E_OBJECT, so it is inert, but do not identify objects by
`Data` alone.

## Third-party world compatibility (2026-07-09) — verified, plus a loader crash

Prompted by "will this run other ZZT games?". Answer: yes for real `.ZZT`
worlds, and it is now checked rather than assumed. *Burger Joint* (1998, Museum
of ZZT, 23 boards, 57 stats) loads and runs 3000 headless steps via
`cmd/zzt-smoke`, no panic. Same seed twice → identical end state; a different
seed diverges, so the sim is genuinely seeded and not accidentally frozen. The
M0–M3 surgery did not bake in TOWN-specific assumptions.

Format gate is vanilla: `WorldLoad` (game.go:626) requires the leading `int16`
to be `-1`. A forged `-2` header (Super ZZT) is rejected through ZZT's own "You
need a newer version of ZZT!" path. That is faithful — real ZZT refuses Super
ZZT worlds too — not a gap to close.

FINDING (unfixed): a **malformed world panics the loader** rather than failing
cleanly. A hand-forged header declaring 200 boards produced
`panic: slice bounds out of range [:0] with length 3`. There is no bounds check
on `World.BoardCount` before the `boardId <= BoardCount` load loop at
game.go:658, and `BoardData` holds only `MAX_BOARD+1` = 101 slots. Being
precise about what was proven: the forged file was malformed in more than one
way, so what is demonstrated is *a malformed world panics*, not specifically
that the `MAX_BOARD` overflow is the trigger. Not compared against the Pascal —
`reference/` is gitignored and was not cloned.

Not exploitable today: `cmd/zzt-server` loads its world once from the `-world`
flag and ignores `JoinMessage.World`, so a client cannot make the server open an
arbitrary file. It becomes a crash vector the moment users can upload worlds,
which is M5.3 territory. Harden the loader before then.

## Open design question (2026-07-09) — world flags are global, players are not

`MAX_FLAG = 10` flags live once in `World.Info.Flags` (gamevars.go:105) and are
read/written by `WorldGetFlagPosition`/`WorldSetFlag` (oop.go:271,282). They are
therefore shared by every player in a room: a puzzle one player solves is solved
for all of them, and `#if flag` means "has anyone done this yet".

This is not a bug — nothing decided otherwise — but it is an unmade decision
that single-player worlds will expose immediately. ZZT-OOP was written assuming
exactly one player. Related: `?`/seek now resolves through `NearestPlayer`
(game.go:300,1336), which is identical to vanilla for one player and a design
choice for N.

Needs a policy before M5 (authoring) and arguably before M4.6 (full TOWN
playthrough). Candidates: keep flags global (co-op semantics, current
behavior); per-player flags (each player runs their own story, breaks shared
puzzles); or a per-flag declaration in the world. Deliberately not resolved here.

## M3.11 (2026-07-09) — save policy for a shared world

DECISION. The sim must never block, so `S` emits `SavePromptEvent{StatId}` and
returns; the caller answers via `Engine.SubmitSaveFilename(statId, name)`,
applied at the top of the next step. That seam is required regardless of policy,
because the terminal client keeps vanilla's modal save prompt.

What the *server* does with the reply is the policy question. Interim answer for
M3.11: **refuse**. The sim shows "Saving is disabled" and moves on. This commits
to no semantics that later work would have to undo.

TARGET (decided, not built here): a save should snapshot the whole room, and
other players should be able to load that snapshot and join it later. This is
already close — `RoomManager.world` (room_manager.go:10) is the authoritative
`TWorld`, kept in sync at room_manager.go:417-419, and `WorldSave`
(game.go:684) already writes the vanilla format. Loading one back is
`NewRoomManager(loadedWorld)`.

Why it is NOT in M3.11: the filename comes from the client. Writing a
client-supplied name to server disk is a path-traversal hole (`../../`), and it
needs its own sanitizing, storage location, snapshot listing, and join-by-name
flow. Bundling that into the change that restructures the player tick is how a
green replay test goes red for unrelated reasons. Tracked as M4.3a.

Note that a room snapshot necessarily captures *other players* — their stats are
in `World.Info` and their stat entries are on the board. Deciding whether a
reloaded snapshot respawns them, drops them, or freezes them is part of M4.3a,
not a detail to improvise.

## Correction (2026-07-09) — reference/ WAS cloned; the loader crash is faithful

The earlier entry today ("Third-party world compatibility") claims `reference/`
is "gitignored and was not cloned". **That is wrong.** Both
`reference/reconstruction-of-zzt` (11 `.PAS` files) and `reference/zztgo` are
present. The claim came from a shell `grep ... || echo "not cloned"` where the
grep searched `GAME.PAS` for `MAX_BOARD`, which actually lives in
`GAMEVARS.PAS`; grep exited 1 and the `||` fallback printed a false conclusion.
Lesson, since this will recur: never let an `||` fallback narrate a conclusion
it did not test.

With the Pascal actually consulted, the malformed-world panic is resolved:
`GAME.PAS:743-748` reads

    for boardId := 0 to World.BoardCount do begin
        BlockRead(f, World.BoardLen[boardId], 2);
        GetMem(World.BoardData[boardId], World.BoardLen[boardId]);
        BlockRead(f, World.BoardData[boardId]^, World.BoardLen[boardId]);
    end;

over `BoardData: array[0 .. MAX_BOARD]` (`GAMEVARS.PAS:139`, `MAX_BOARD = 100`).
There is no bounds check on `BoardCount` in the original either. So the Go
`WorldLoad` is a **faithful** port of an original ZZT bug, not a fork
regression. Vanilla would scribble past the array; Go panics instead, which is
strictly better behavior for the same broken input.

Consequences: do NOT "fix" `WorldLoad` — bounding the loop there is a behavior
change and would need a `DEVIATION:` line. The right shape is validation *before*
the loader (reject `BoardCount > MAX_BOARD`, short files, negative `BoardLen`)
on the untrusted-upload path only, leaving the faithful loader untouched for
worlds the operator supplies. Fold this into M4.3a / M5.3 rather than the
engine. A `// ZZT-QUIRK:` marker on the loop would be appropriate.

## Bug (2026-07-09) — ReenterWhenZapped left the player off the board

Reported from live play: on a board with `ReenterWhenZapped`, taking a bullet
made the player's sprite vanish and the player became uncontrollable.

Cause. `DamageStat` (game.go) clears the player's old tile to `E_EMPTY`, moves
`stat.X/Y` to `Board.Info.StartPlayerX/Y`, and never writes `E_PLAYER` at the
destination. This is faithful — `GAME.PAS:1163` does exactly the same — because
vanilla sets `GamePaused` and lets `GamePlayLoop`'s pause branch draw the player
each frame, restoring `E_PLAYER` on unpause. That branch is terminal-only.
Headless, nothing ever restores the tile, and `GameStepWithInputs` dispatches
tick procs **by tile element**, so the player stat stopped ticking entirely.

This **predates M3.11**. Before it the line was `e.GamePaused = true`, a headless
no-op; the tile was left `E_EMPTY` just the same. M3.11 only made the pause real.

Fix, marked `DEVIATION:` in the commit: place the player on the start tile
immediately rather than on unpause, saving the previous tile into `stat.Under`
the way `MoveStat` does so re-entering cannot permanently destroy what was
there. `MoveStat` itself is wrong here — it would copy the `0x70` damage-flash
colour onto the destination and leave the player highlighted red forever. The
room keeps running for other players, so a re-entering player has to be solid
again at once; deferring to unpause is not available to us.

Do NOT apply the same fix to `BoardPassageTeleport`. A player paused on a
passage keeps `E_PASSAGE` underneath and is merely drawn over it; forcing
`E_PLAYER` there would destroy the passage tile (vanilla's unpause writes
`E_PLAYER` at the square the player moves *to*, not the one it is standing on).
In any case `RoomManager` sets `MultiRoom = true` on every engine it creates
(room_manager.go:311), so on the server passages always take the
`TransferEvent` path and `BoardPassageTeleport` is unreachable. It stays live
for the terminal and `cmd/zzt-smoke`, where the pause branch does restore.

Residual, not fixed: if a monster or another player occupies `StartPlayerX/Y`,
the re-entering player's tile overwrites theirs, and that stat will dispatch a
player tick until the square is vacated. Vanilla cannot hit this (one player,
and start squares are empty by construction). Worth revisiting when boards get
authored in-browser (M5.1).

## Bug (2026-07-09) — re-enter/respawn used a stale board value, which can be a wall

Follow-up to the ReenterWhenZapped fix above. Placing the player back at
`Board.Info.StartPlayerX/Y` dropped them **inside a wall** on TOWN board 19
("The Mixer"): the world file stores `StartPlayer = (30,25)` and the tile there
is `E_NORMAL` (element 22), with `E_BOARD_EDGE` below it.

Why vanilla never notices: `BoardEnter` (game.go) overwrites
`Board.Info.StartPlayerX/Y` with the player's own position every time its single
player enters a board, so the value stored in the world file is never read.
`RoomManager` never calls `BoardEnter` — `spawnPlayerInRoom` even saves and
restores the pair around its spawn — so on the server the stale file value
survived and got used.

Fix: a re-enter point is per-player state, not board state. `PlayerState.ReenterX/Y`
records the square a player entered the board on; `Engine.ReenterPoint(statId)`
resolves it, falling back to `Board.Info.StartPlayerX/Y` and then the board
centre. Set from `SpawnPlayer`, `BoardEnter` (terminal parity), and
`movePlayerStat` (the server's de-facto BoardEnter, room_manager.go).

Consumers changed: `DamageStat`'s ReenterWhenZapped branch, and the death-respawn
branch in `ElementPlayerTick`, which had the identical flaw — dying on board 19
would also have respawned the player inside the wall. Nobody hit that yet.

BEHAVIOR CHANGE, deliberate: death respawn now returns a player to the square
*they* entered the board on, not to the board's designer-set start. M2.4 chose
the latter; with several players entering a room from different passages, the
board-global value is both wrong and unreliable. `TestDeathRespawnInventoryIsolation`
was updated to state this.

The stale-value fallback is retained but is now a last resort. Worlds whose
stored `StartPlayerX/Y` is a wall are common precisely because vanilla never
reads it, so nothing ever forced authors to keep it sane.

## M4.1 (2026-07-09) — one modal renderer, one input router

`src/modal.ts` now owns every modal ZZT draws: read-only text, selectable
`!label;text` links, paged help, yes/no prompts, and text entry. `main.ts` keeps
only the wiring. The three-way `Mode = "play" | "debug" | "window"` collapsed
into a single `modal: Modal | null`; `handleModalKey` is the sole consumer of
keys while one is open, and it returns `close`/`redraw`/`ignore` rather than
touching global state.

Geometry is transcribed from the engine, not invented:
`SidebarPromptYesNo` (message at 63,5 in 0x1F, 0x9E cursor), `SidebarPromptString`
(label right-aligned to column 75 on row 3, field at 63,5 width 8 + extension),
and `GameDebugPrompt` (the same field, width 11, no label, PROMPT_ANY).

Subtlety worth remembering: a modal callback can chain straight into another
modal (the save prompt opens its "disabled" notice). The router fires callbacks
and *then* reports `close`, so `handleKeyDown` compares the modal identity
before tearing down — otherwise the chained modal is destroyed on the frame it
opens. This bit once during development.

REACHABILITY, deliberate: `commandKey` still only sends `?` and `H`, so the
yes/no and entry modes have no keystroke that reaches them from the browser
until M4.2 lands the rest of the play-mode keys. They are implemented and
verified, not live. `savePrompt` resolves locally with "Saving is disabled on
this server" (M3.11's decided policy); `quitPrompt` resolves locally because
`QuitPromptEvent` carries no StatId and there is no reply channel — both are
M4.3.

Verification: no TypeScript test runner exists in this project (M3.5-M3.10 all
landed without one), so the router was exercised by compiling `modal.ts` with the
project's own tsc and driving it under node: 19 checks covering text navigation,
link selection, help-vs-scroll select behaviour, yes/no accepting only Y/N/Esc,
entry charset (PROMPT_ALPHANUM uppercases and rejects punctuation), width clamp,
cancel-vs-submit, and that every painted cell lands inside 80x25. Two checks
specifically assert a gameplay key ('W') and an arrow key are *ignored* by the
modal rather than leaked to the board — the DoD's real claim.

Adding a TS test runner (vitest) so those checks live in the repo is the obvious
next infra step. Deliberately not done here: it is a dependency decision, not
part of M4.1.

## M4.2 (2026-07-09) — full keyboard/control parity

The browser now sends every key `ElementPlayerTick`'s `switch UpCase(InputKeyPressed)`
reads (`elements.go:1374-1429`): `T` torch, `P` pause, `B` sound, `S` save,
`Q` quit, plus the already-live `H` and `?`. Commands ride the `key` byte;
movement rides the `keymask`. Neither populates the other's field, so a command
can never be read as a step or the reverse — `TestM42MovementCarriesNoCommandKey`
pins that.

DECISION, and the reason this task needed one: **WASD movement is gone.**
It was a M3.5 client invention, and it collided head-on with `S` = save game —
both arrive as the same `InputKeyPressed` byte, so the engine cannot tell them
apart. Checked against the source rather than guessed: `INPUT.PAS:217-234` is
the original's entire movement vocabulary —

    KEY_UP, '8' | KEY_LEFT, '4' | KEY_RIGHT, '6' | KEY_DOWN, '2'

arrow keys and the numeric keypad, ported faithfully at `engine/input.go:101-110`
(plus joystick and mouse, `INPUT.PAS:243-321`). So the client now sends arrows
and `Numpad8/4/6/2` and nothing else, which both frees every command letter and
is strictly *more* faithful than what it replaced.

`main.ts` grew a pause layer. Worth recording, because TASKS.md M3.11 and M4.2
both describe it backwards: vanilla does **not** blink "Pausing...".
`GAME.PAS:1518-1533` writes that label unconditionally at (64,5) every frame and
blinks the **player glyph**, alternating `ElementDefs[E_PLAYER].Character` with a
blank. Blink period is `SoundHasTimeElapsed(TickTimeCounter, 25)`; a TimerTick is
6 hundredths of a second (`SOUNDS.PAS:172`), so 250ms. Implemented as an overlay
layer painted *under* any modal, since a paused player can still have a scroll open.

Key routing moved out of `main.ts` into `src/keys.ts`, for the same reason M4.1
split out `modal.ts`: it is pure, and the failure mode is silent. A mistyped
`KeyboardEvent.code` such as `"NumPad8"` typechecks perfectly and simply never
moves the player. Driven under node the way M4.1 drove the modal router —
65 checks: every command byte, modifier suppression (Ctrl+S must stay the
browser's save dialog), each numpad digit mapping to its arrow, and the
`S`-is-save-not-move-down contract from both sides.

STILL DEFERRED, unchanged by this task: `QuitPromptEvent` carries no `StatId` and
`GamePromptEndPlay` (`elements.go:1240`) reads `PlayerFor(0).Health`, so `Q` from
player 2 prompts nobody and reads player 1's health. The client answers the quit
modal locally. That is M4.3, which names both bugs explicitly. `S` emits
`SavePromptEvent` and the server still refuses it (M3.11 policy); rejoinable
snapshots are M4.3a.

Replay fixture unchanged — no simulation code was touched, only the client and
the tests.

## M4.3 (2026-07-09) — title, world, quit, and high-score flows

The three bugs TASKS.md named are all the same shape: a non-gameplay flow that
vanilla could hard-code to "the player" because there was only ever one.
`QuitPromptEvent` carried no `StatId`, `GamePromptEndPlay` read
`PlayerFor(0).Health`, and `HighScoresAdd` took a bare score read from
`PlayerFor(0)`. All three now name a player.

**The dead branch of `GamePromptEndPlay` is single-player only.**
`ELEMENTS.PAS:1302-1306`: when the player is dead, Escape skips the prompt and
sets `GamePlayExitRequested`. Nothing ever *resets* that flag, and
`GameStepWithInputs` guards its stat loop on it, so a dead player pressing Q in
a room would have frozen that board for everyone, permanently. Guarded with
`&& !e.MultiRoom`.

Worth writing down precisely, because it is easy to overstate: the branch is
**latent, not live**. `ElementPlayerTick` returns before its key switch while
`Health <= 0` (elements.go, M2.4's respawn countdown), so a dead player cannot
currently press anything. Vanilla instead *falls through* — that is exactly how
"Game over - Press ESCAPE" works (`ELEMENTS.PAS:1340-1350`). The guard exists so
that restoring vanilla's fall-through later cannot resurrect the freeze.

**DEVIATION: a high score is entered on QUIT, not on death.** Vanilla calls
`HighScoresAdd` when `GamePlayLoop` exits with `Health <= 0` (`GAME.PAS:1598`).
M2.4 already replaced game-over with respawn, so death is no longer an ending
and there is nothing to record. Quitting is the multiplayer ending: confirm the
prompt, your score is offered to the list, you leave the room, everyone else
keeps playing.

**The high-score list moved off `Engine` and onto `RoomManager`.** `RoomManager`
runs one `Engine` per *board* and there is one list per *world*. A room engine's
`HighScoreList` is all zeros, so `Engine.HighScoresAdd` would have ranked every
score first. `Engine.HighScoresAdd(statId)` stays for the terminal; the server
never calls it. `RoomManager.HighScorePath` is empty by default so that
`NewRoomManager` never touches the filesystem in a test.

Quit rides the same seam as `TransferEvent`, for the same reason: the engine
cannot remove a player (RoomManager owns the roster), so it announces the
decision. `SubmitQuitReply` → `QuitEvent` → `RoomManager` resolves the stat id
to a stable `PlayerID` *during* the event drain (stat ids shift when anyone
leaves), then removes the player before diffs are built, so the quitter gets no
diff and the survivors' ids are already reindexed.

**DEVIATION: the browser title screen does not animate.** In ZZT the title
screen is `GamePlayLoop` on board 0 with `GameStateElement = E_MONITOR`
(`GAME.PAS:1610-1622`), so its objects move. Here the world is shared: a title
room that ticked would run board 0's objects — and any `#set` they perform
touches `World.Info.Flags`, which every room shares — for as long as *any*
browser anywhere sat on the title screen. A per-client screen in vanilla is
server-wide state here. So `/api/title` serves a static render (`web_api.go`).

Two rows of vanilla's monitor menu are deliberately absent: `' S '` Game speed,
because the server owns the tick rate for every player in a room and a slider
that moved nothing would be a lie; and `' E '` Board Editor, which is M5.

`' R '` Restore game reports that saved games are unavailable. That is not a
gap in this task — loading a snapshot by name *is* M4.3a's DoD, and it needs
the sanitized `-saves` directory M4.3a specifies.

`' W '` World select lists the one hosted world. Multi-world hosting needs
server-scoped client ids first: each `RoomManager` mints `PlayerID`s from 1, so
two of them collide in `WebSocketServer.clients`.

Fixed in passing, because the client was showing the wrong string: the in-game
quit prompt is `"End this game? "` (`ELEMENTS.PAS:1308`). `"Quit ZZT? "` is the
*title screen's* prompt (`GAME.PAS:1978`) and is now used only there.

ZZT-QUIRK ported to the client: Escape at "Congratulations! Enter your name:"
records an *empty* name, because `PopupPromptString` blanks the buffer before
`PromptString` and Escape restores that blank. The entry keeps its slot and
`HighScoresInitTextWindow` then skips it for having no name.

Verification: `go test ./...` green, replay fixture unchanged (no simulation
behavior changed for a single player). New `m4_3_test.go` drives the wire format
— both quit outcomes, the ownership of every event, and the "one player quits,
the other keeps ticking" claim. Both engine fixes were mutation-checked: undoing
the `!e.MultiRoom` guard and the `StatId` field each turn tests red. Still no TS
test runner (see the M4.1 note), so `title.ts` and the new `popupEntry` modal
were driven under node the same way — 32 checks, including that every painted
cell lands in the sidebar and that gameplay keys are inert on the title screen.
The HTTP surface and the full join → Q → quitReply → highScoreName chain were
exercised against a running `zzt-server`.

## M4.3a (2026-07-09) — savable, rejoinable room snapshots

The three decisions TASKS.md demanded be made explicitly, made before the code
was written.

**DECISION 1: a snapshot drops every player.** `World.Info` has exactly one set
of player stats — health, ammo, gems, keys — because ZZT had exactly one player.
N players cannot round-trip through it, so the snapshot does not pretend to
carry them. Every `E_PLAYER` stat is removed from every board before it is
serialized, using the same `Engine.RemovePlayer` that `LeavePlayer` uses, so a
saved board is byte-identical to one everybody walked out of. The alternatives
were worse: *freezing* them leaves uncontrolled `E_PLAYER` tiles that tick,
block squares, light dark rooms, and pull monsters through `NearestPlayer` —
a ghost with no client; *respawning* them at `Board.Info.StartPlayerX/Y` is
meaningless when there is no client to respawn.

The saver's own inventory *is* written into `World.Info`, as vanilla writes
player 0's (`GAME.PAS:763`), so the file stays a valid `.SAV` that real ZZT and
our terminal client restore exactly as before. The server ignores those fields
on join: `RoomManager.JoinPlayer` calls `ResetPlayerState`, so whoever joins a
restored snapshot arrives with 100 health and no keys — identical to joining any
running world mid-game. That consistency is the point. The cost, stated rather
than inherited: doors already opened stay open, but a key still in a pocket at
save time is gone. Flags, not inventory, are what carries puzzle progress here.

**DECISION 2: flags are unioned across live rooms, not copied from one.** The
2026-07-09 "world flags are global" entry says flags are shared; the code is
weaker than that. Each room engine holds its *own copy* of `World.Info`, and
`freezeRoomIfEmpty` only pushes flags into `RoomManager.world` when a room
empties — `syncFrozenBoardToLiveRooms` then overwrites every live room's flags
with the freezing room's. So at any instant the flag sets have diverged, and no
single one of them is "the" world's. `snapshotFlags` therefore unions
`rm.world` with every live room in sorted board order, first-seen wins, capped
at `MAX_FLAG` exactly as `WorldSetFlag` caps it. Taking the saver's room's flags
alone would silently drop a puzzle another room had solved, and the DoD says
flags survive the round trip. This does not fix freeze's clobber — that is the
open design question's to fix, not this task's.

**DECISION 3: co-op saves freeze shared progress, and that is intended.** Since
flags are global, a snapshot records the whole party's puzzle progress, not the
saver's. Player A saving in room 3 also saves the door player B opened in room
7. For a co-op world that is the only coherent reading of a save, and it falls
out of the union above rather than being bolted on.

**Restoring is refused while anyone is playing.** `RoomManager.RestoreSnapshot`
returns `ErrWorldOccupied` unless `len(rm.players) == 0`. A restore replaces
every board in the world; doing that under a live player would teleport them
into a board that no longer exists. The title screen's `R` is reachable only
before a client has joined a room (joining is what `P` does), so on a quiet
server it works and on a busy one it reports "Someone is still playing". The
`RoomManager` is mutated in place rather than replaced, so `nextPlayerID` keeps
climbing and the `PlayerID` collision that blocks multi-world hosting cannot
appear here.

**The filename is a whitelist, not a blacklist.** `SanitizeSaveName` accepts
only what vanilla's `PROMPT_ALPHANUM` prompt can even produce: 1–8 characters of
`A-Z`, `0-9`, `-` (game.go:504, width 8 at game.go:550). `/`, `\`, `.` and
therefore `..` and every absolute path fail the charset, so path traversal is
rejected by construction rather than by pattern-matching. `SaveSnapshot` then
re-checks that the joined path's directory is still the configured `-saves`
directory, which costs one line and would catch a future loosening of the
charset.

**Two seams had to stop being interactive.** `WorldSave` and `WorldLoad` report
failure through `DisplayIOError`, which opens a text window and calls
`TextWindowSelect` — it waits for a keypress. On a server holding `s.mu` that is
a permanent hang, not an error message. Both now delegate their bytes to
`worldWriteTo` / `worldReadFrom`, which return an `error`; the terminal wrappers
still show vanilla's window, and the snapshot paths get an error they can send
to the client. No behavior changed for the terminal.

**DEVIATION: `WorldSave` now zeroes its 512-byte header, as the Pascal does.**
`GAME.PAS:780` is `FillChar(IoTmpBuf^, WORLD_FILE_HEADER_SIZE, 0)`. The machine
conversion produced `for i := 0; i < 512; i++ { ptr[0] = 0 }` (game.go:705) —
it zeroes byte 0, five hundred and twelve times. Every `.SAV` and `.ZZT` this
fork wrote therefore carried ~230 bytes of whatever `BoardClose` had just left
in `IoTmpBuf` into the file's header padding. Harmless to load, but it leaks
board memory into a file players hand to each other and makes an otherwise
deterministic write depend on history. This restores the Pascal (hard rule 1);
it changes saved-file bytes, never simulation, and the replay fixture is
untouched and green.

**DEVIATION: `StoreWorldInfo` never wrote the world's flags.** Found by the
round-trip test, not by reading: `LoadWorldInfo` (serialize.go) has always read
`Flags` back from offsets `46 + 21*i`, and `StoreWorldInfo` jumped straight from
`Name` to `BoardTimeSec` and left those 210 bytes as whatever was in the buffer.
`GAMEVARS.PAS:120` is `Flags: array[1..MAX_FLAG] of string[20]`, and the loader's
offsets already prove the layout, so the writer was simply missing a field. Every
world this fork ever saved lost every flag — i.e. all ZZT-OOP puzzle progress —
and M4.3a's DoD ("flags and puzzle progress survive the round trip") cannot be
met without it. Simulation is untouched: nothing but a file write changes, and
the replay fixture is green. Mutation-checked both ways.

Verification: `go test ./...` green, replay fixture unchanged. New
`m4_3a_test.go` covers the filename whitelist (including `../`, `..\`, absolute
paths, and the DoD's own "a filename containing `../` is rejected"), the full
save → restart → restore → join round trip, the flag union across rooms, the
"every player is dropped" decision, the refusal to restore an occupied world,
`PlayerID` monotonicity across a restore, and the wire path (`S` → `savePrompt`
→ `saveFilename` → `saveResult`) plus `/api/saves` and `/api/restore`'s
409/400/404/200. Four claims were mutation-checked — removing the flag writer,
keeping the players on the board, serializing the live room instead of a copy,
and restoring `ptr[0] = 0` each turn a test red. The header-padding test needed a
board whose RLE exceeds 279 bytes before it could see the leak; with a simple
board it passed against the bug, which is exactly the kind of test that proves
nothing. Still no TS test runner (M4.1 note), so `openSelectList`'s assumption —
that `!NAME;NAME` round-trips through `hyperlinkOf` — was driven under node,
along with `R` mapping to the restore action.

## M4.3b (2026-07-09) — one placement policy, and the test that asserted the bug

PROCESS: the advisor tool was unavailable this session (`advisor tool is
unavailable` on the one call). M4.3b is not `[ADVISOR]` and no escalation stop
was needed, so per CLAUDE.md rule 5 this is recorded rather than blocking.

The overlap is not cosmetic. `GameStepWithInputs` (game.go) picks a stat's tick
proc by reading the element of the tile that stat stands on. Two stats sharing a
square therefore means one of them starts ticking as whatever the winner's tile
says: a lion re-entered upon dispatches through `ElementPlayerTick` and stops
being a lion. Only `roomSpawn` checked its destination; `DamageStat`'s
ReenterWhenZapped path and `ElementPlayerTick`'s respawn wrote `E_PLAYER` over
whatever stood there. Vanilla (`GAME.PAS:1179-1193`) never writes the
destination tile at all — it moves the stat and lets `GamePlayLoop`'s pause
branch redraw on unpause — so the stamping is fork-introduced (it already
carried a `DEVIATION:`), not a quirk to preserve.

New `engine/placement.go` holds the single policy: `StatAt`, `PlacementUnoccupied`,
`PlacementOpen`, `FindPlacement` (the ring search lifted verbatim out of
`roomSpawn`). `isSpawnOpen`/`isSpawnUnoccupied` are now room-scoped wrappers, so
join, re-enter, and respawn all choose a landing square the same way. All scans
run in stat-index / ring order and touch no map: placement is deterministic.

DECISION — what triggers a push. The spec says to reuse `roomSpawn`'s checks
rather than invent a second policy, which reads as "relocate unless the square is
`E_EMPTY`". That was implemented first and it broke
`TestReenterWhenZappedPreservesUnder`: M3.11 deliberately lets a re-entering
player land on terrain (forest, a wall) and stash the tile in `stat.Under`, and
an `E_EMPTY` requirement makes that `Under` save dead code, since the destination
would always be blank. Nothing in M4.3b's DoD asks for that — all three routes it
names (two re-enterers, two respawners, re-enter onto a monster) involve a *stat*
on the destination. So the trigger narrowed to "another stat holds the square",
while the *landing* square is still chosen by the shared `PlacementOpen` ring
search. Terrain behaviour is untouched; M3.11 stays green as written.

DECISION — who moves. The arriving player, never the incumbent. This matches
`roomSpawn` (search outward from the requested square) and avoids yanking a
stationary player mid-move.

DECISION — nowhere to go. The re-entering stat's own tile is cleared before the
search runs, so its old square is itself open ground: a player with nowhere else
to go re-enters in place. `FindPlacement`'s `ok=false` branch is therefore a
safety net rather than a live path, and it also means "stays put" never overlaps.

`TestReenterUsesPlayerEntrySquareNotStaleBoardValue` (M3.11) had to change, under
any correct policy. It put its player at (5,24) on TOWN board 19 — which is
exactly where that board's own stat 0 stands, tile element 4 (`E_PLAYER`). The
test wiped that tile and `AddStat`ed a second player stat on the same square,
manufacturing the very overlap M4.3b fixes, then asserted the overlapping
outcome. Its entry square moved to (4,24), which no stat holds; every assertion
it makes about the stale-wall bug is unchanged, and a `StatAt` precondition now
documents the requirement.

Two fixes to tile bookkeeping on lines already being touched, both fork-only
paths (respawn is an M2.4 invention; vanilla has no respawn):
  * respawn never set `stat.Under` at all, so the stale pre-death `Under` was
    stamped onto whichever square the player next walked off of.
  * re-enter set `Under` unconditionally, so a player whose entry square is the
    square they already stand on had their real `Under` replaced by the blank
    tile the code had just cleared. Both now write `Under` only on an actual move.

Verification: `go test -count=1 ./...` green, replay fixture unchanged — the
600-step TOWN replay never leaves "Room One" (`ReenterWhenZapped=false`), takes
no damage and never respawns (`healthDrops=0 teleports=0 respawnTicks=0`), so
both edited paths are unreachable under the fixture and the hash cannot move.
Probed rather than assumed. New `m4_3b_test.go` covers each DoD route and shares
an `assertNoStatOverlap` invariant; all four were mutation-checked by
neutralizing `StatAt`, and all four went red with exactly the described failure
(the lion "stands on element 4, want 41").

## M4.4 (2026-07-09) — browser sound synthesis

Sound remains presentation only. The engine still emits `SoundEvent{Notes string,
Priority}` and no simulation state or `StateHash` input changed. The browser now
owns the old `SOUNDS.PAS` queue semantics: priority `>= current` interrupts,
priority `-1` appends to the unscheduled tail, and duration units schedule at
18.2065 Hz.

Wire-format decision: `ProtocolEvent.Notes` is now `[]uint16`, not a string. Go
strings hold parsed sound bytes, but JSON strings are UTF-8; drum note bytes
`0xf0..0xf9` would not arrive in JavaScript as one code unit. Numeric bytes make
the protocol lossless and are checked by `TestProtocolSoundNotesAreBytes`.

Waveform decision: use one persistent WebAudio square oscillator, gated through
a gain node. No filters, reverb, samples, envelopes, or musical smoothing are
added; only a sub-millisecond gain ramp is used to avoid browser click artifacts.
This is the closest practical browser match for the PC speaker's single square
tone while still using WebAudio's scheduler.

Percussion decision: ZZT drums are hardcoded rapid frequency changes on the same
PC speaker tone path. The browser freezes the Go-initialized `SoundDrumTable` as
a TypeScript literal and schedules each drum step 1 ms apart: drum 0 is the
1 ms tick, drums 1..9 use the 14-step bursts (with 3 retained as the inert/N/A
entry). `TestSoundDrumTableFrozenForBrowser` guards the literal against future
RNG/init-order drift. Vanilla randomized some drums per run, so stability is the
goal rather than reproducing a new random drum timbre each page load.

Sound events are room-wide. With no `StatId` on `SoundEvent`, every client in a
room hears pickups, shots, doors, damage, and object sounds from every player.
That is accepted for now as shared-room presentation. Mute remains per-player:
the server's `HUDSnapshot.SoundEnabled` gates local playback, and the sidebar's
clickable "B / Be quiet" line sends the same `B` command as the keyboard, so the
server remains authoritative for the visible state.

## M4.6 (2026-07-09) — TOWN protocol playthrough smoke

The smoke is semi-scripted, not a full puzzle solver. It uses the real
`fixtures/TOWN.ZZT` and the same `RoomManager`/protocol-shaped `PlayerInput`
path the browser uses, but stages the player next to landmarks so CI can cover
the original loop without solving every maze and timing puzzle. The user-provided
Scott Walker walkthrough confirms the intended high-level route: stock up at
the start/armory, collect keys, buy supplies, use torches in dark rooms, take
damage, and reach the castle/throne-room path.

Coverage added in `m4_6_test.go`: Room One gem/torch pickup, passage to Armory,
Vendor scroll and `!ba` reply purchase, green key and green door, torch use in
the Bank Vault, a Prison scroll tile, real enemy damage in Inside Castle, red key
and red door on Path to castle, edge transfer to Outside of castle, passage into
Inside castle, and south-edge transfer into the Throne Room.

Transfer sounds are asserted through `RoomManager.DrainPlayerEvents`, because the
WebSocket layer appends those per-player events to the board-change snapshot
rather than the direct destination-room diff. Existing WebSocket tests still own
the JSON board-change delivery shape.

## 2026-07-10 — Pre-existing data race between HTTP handlers and the tick loop

Found while adding `-race` coverage for `TitleSim`, and **not caused by it**:
`go test -race -run TestWebSocketServerScrollReplyBuysFromVendor` fails on a
clean tree at `7d1ebd1`, before any of today's commits.

`Engine.SubmitScrollReply` (`game.go:2298`) appends to `Engine.PendingScrollReplies`
from the WebSocket goroutine (`websocket_server.go:330` → `submitScrollReplyInInstance`),
while `Engine.GameStepWithInputs` (`game.go:1602`) reads and truncates that same
slice from the tick goroutine. `WorldInstance.mu` is held by the submitter and
`s.mu` by the ticker, so the two never exclude each other. The same shape almost
certainly applies to `PendingDebugCommands` and `PendingSaveFilenames`, which are
drained beside it.

It has not been observed to corrupt a game — the window is one slice append
against one truncate — but it is a genuine race and `-race` will keep failing.
The fix is a lock (or a channel) shared by the submit path and the step path,
which is a change to the room/tick ownership model and therefore its own task,
not a drive-by.

Left unfixed and unfiled; today's scroll work (freezing a reader until they
dismiss) touches `RoomManager.SubmitScrollReply` but neither widens nor narrows
this race: `roomPlayer.scrollOpen` is written under the same goroutines as the
slice it sits beside.

## 2026-07-10 — Planning: M7 "Live-game quality" batch; M6 moved ahead of M5

PROCESS: the advisor tool was unavailable this session (one call — "advisor
tool is unavailable"), as on 2026-07-09. This was a planning session, not an
`[ADVISOR]` task; recorded per rule 5 rather than blocking.

A survey of all open work (M5, M6.2, the four unchecked backlog bugs, and the
2026-07-10 race note) ranked what most improves the game as it is played
today. Decisions, in the order the tasks now appear:

* **New M7 section, placed between M4 and the feature milestones.** The
  executor protocol is positional ("first unchecked task below"), so priority
  had to be expressed by file order, not by a note. Order inside M7: spawn
  point (M7.1 — the former `[URGENT]` backlog item, spec moved verbatim),
  torch light on arrival (M7.2), the pending-input data race (M7.3),
  per-player sound (M7.4), the new-worlds batch (M7.5, gated on M7.1 because
  those worlds are exactly the fake-wall-floor kind the spawn bug ruins).
  Bugs in the game people can already play outrank all new feature surface.
* **M6 moved ahead of M5 in file order.** Only M6.2 (Google OAuth) is open in
  M6, and stable identity outranks creation tooling for an MMO; M5's editor
  is the largest and least urgent remaining block. No task text changed.
* **Torch root cause is arrival, not lighting.** `DrawPlayerSurroundings`
  runs on torch light, expiry, respawn/re-enter, and non-adjacent moves, and
  the two M4.5 torch tests pass — but neither `roomSpawn` nor
  `transferPlayer` draws surroundings, and `MoveStat`'s adjacent-move repaint
  (game.go:1123-1135) only redraws the delta ring, which assumes the circle
  is already painted. A torch-lit player entering a dark room therefore shows
  only the moving-ring trail — exactly the reported "only the player's path".
  M7.2 requires the failing test first, since this diagnosis is from reading,
  not from a repro.
* **Sound attribution is presentation-only.** An owner `StatId` on
  `SoundEvent` plus routing in `RoomManager` — nothing enters `StateHash` or
  the replay path. M4.4's recorded "sound events are room-wide" acceptance is
  superseded by M7.4. The TransferEvent sound double-path
  (room_manager.go:411-415, room-wide *and* per-player) gets resolved to
  traveller-only in the same task.

## 2026-07-10 — Vanilla parity audit; M8/M9 added, M6.4 added, M5 broken down

PROCESS: the advisor tool was unavailable this session (recorded as before,
per rule 5).

Audited the fork against `reference/reconstruction-of-zzt/SRC/*.PAS` and the
Go port, feature by feature. Found COMPLETE and replay-guarded: the full
element tick/touch surface; the entire ZZT-OOP command set (grep of oop.go's
command strings against OOP.PAS — all present, including flags, counters,
directions, and per-player `#endgame`); per-player board time limits
(elements.go:1483-1495) with the TIME cheat and HUD fields; vanilla
message-timer flash messages (game.go:1259 — the real E_MESSAGE_TIMER stat,
so messages reach the browser as board cells); passage-arrival pause per
player (elements.go:1438, matching ELEMENTS.PAS:1439's `GamePaused := true`);
energizer including the 10-tick warning jingle; debug cheats; high scores;
world select; room snapshots (M4.3a); sound (M4.4); CP437/EGA rendering;
keyboard vocabulary (M4.2). The terminal editor survived conversion whole —
`EditorFloodFill` included (editor.go:480) — except `EditorTransferBoard`
(TODO stub, editor.go:422).

Gaps found → tasks:
* `BoardShoot`'s point-blank damage guard reads `PlayerFor(0).EnergizerTicks`
  (game.go:1411) — the wrong player whenever the target isn't stat 0 → M8.1.
  The same branch lets a player point-blank another player for damage, which
  contradicts M2.4's no-PvP bullet rule; M8.1 reconciles and records it.
* `ResetMessageNotShownFlags` resets only player 0 (elements.go:1505) → the
  known instance inside M8.2, which sweeps the whole `PlayerFor(0)`/`Stats[0]`
  class and leaves a classification table here.
* Browser board changes cut instantly; vanilla fades via
  `TransitionDrawBoardChange` (game.go:1448) → M9.1, client-side only.
* `A` About screen on the browser title → fixed in M9.2.
* Saves drop per-player inventory (M4.3a's documented decision) → M6.4,
  gated on M6.2 identity, stored as a sidecar so the vanilla file format is
  never touched.
* M5 was four coarse tasks; now M5.0–M5.7, each mapped to specific
  EDITOR.PAS/editor.go procedures with line cites so a single-session agent
  can execute one without re-deriving the map.

Deliberate non-goals — vanilla behaviors we intentionally do not restore,
so future audits don't re-flag them: game-over ending the run (death
respawns instead, M2.4/M4.3 DEVIATIONs), `S` game speed (the server owns the
110ms tick), global pause (per-player instead, M3.11), and the modal
terminal editor as the browser path (M5 replaces it; the terminal keeps it).

## M9.2 — title About/menu completeness (2026-07-13)

Browser title `A` now follows vanilla's About path via
`/api/help?file=ABOUT.HLP&title=About+ZZT...`, using the existing text-window
help renderer. The title sidebar and key map cover `W`, `P`, `R`, `Q`/Escape,
`A`, `H`, and the browser editor `E`. `S` game speed remains intentionally
omitted because the server owns tick pacing; adding a client-side slider would
not affect simulation speed. `D` dream-world generation is a ZZTMMO extension,
not vanilla title vocabulary. Tests cover the ABOUT.HLP endpoint and the pure
title key/sidebar mapping.

## 2026-07-10 — Design horizon: collaborative editing (M10)

Decided the design pillars for multiplayer editing now, because M5.0 is about
to fix the editor session model and could otherwise preclude it. The pillars:
server-authoritative edit ops through one serialized apply path with
last-write-wins per cell (no CRDTs/OT at ZZT scale); exclusive per-stat and
per-board leases for dialogs and code editing (the `scrollOpen` freeze is the
in-repo pattern); sessions never tick, publishing stays the only bridge to
hosted play (live-editing a running room re-opens the stat-reindexing bug
class M4.3b closed); undo is per-user-own-ops or absent, decided at spec
time. M5.0 gained a forward-compatibility clause: a member *list* capped at
one, so M10.1 raises a cap instead of rewriting the model. M10 tasks are
deliberately coarse until M5.5 lands — detailed specs written against code
that does not exist yet would rot.

## 2026-07-10 — Owner reprioritization: editor pulled forward; gitignore

The owner wants creation tools early: M5 moved from last to directly after
M7, giving M7 → M5 → M8 → M9 → M6 → M10. M7 stays first — it is five small
fixes to the game people already play, and M5.6 consumes M7.5's world
validation gate. Nothing in M5.0–M5.5 depends on M8/M9/M6 (only M6.4 and
M10.3 need M6.2 identity), so the pull-forward breaks no dependency. The M6
"moved ahead of M5" note from earlier today is superseded and updated in
place.

Also gitignored the local strays: `engine/zzt-server` (cross-compiled Linux
binary), `engine/deploy.tar.gz`, `engine/saves/` (runtime snapshots +
chat.jsonl), and the root `test.txt` (an old M0.5 manual-test note).
`engine/.gitignore` already covered `*.HI`, `*.ZZT`, and the local binaries.

Addendum, same day: the owner's stated vision for M10 — Google-Docs-style
tandem editing, multiple cursors each in their own color — is now explicit in
M10.1: simultaneous canvas drawing with no turn-taking (leases never apply to
the board surface, only to modal dialogs), continuously streamed cursor
presence in per-member DOS colors, and local-echo for one's own cursor.

## 2026-07-10 — Owner additions: editor .ZZT download hardened; M11 Museum of ZZT

Two more owner requests folded in the same day:
* M5.6's `.ZZT` **download** is now a first-class DoD item, not a side
  mention: the exported bytes come from `worldWriteTo` (vanilla format, so
  the file loads in DOS ZZT/zeta as well as here) and must survive a
  `WorldLoad` round-trip test. Creators own their work as a portable file.
* New **M11 — Museum of ZZT search-and-play**, positioned directly after M5
  (owner: "further on, but not too much further"). M11.1 is the server-side
  client — search proxy, on-demand zip fetch, extraction, SanitizeSaveName
  mapping, the M7.5 validation gate, disk caching, outbound rate limiting,
  identifying User-Agent, fetched worlds never committed. M11.2 is the CP437
  search window on the title screen. The spec directs the implementer to
  read the Museum's current API docs at build time instead of trusting
  model memory of the endpoints. Execution order is now:
  M7 → M5 → M11 → M8 → M9 → M6 → M10.

## 2026-07-10 — Idea backlog added (not tasks)

Gap sweep + brainstorm at the owner's request. Verified against code before
writing: no autosave/restore-on-boot exists; disconnect = immediate
LeavePlayer (websocket_server.go:655) so a refresh kills the run; all
players render as the same white-on-blue smiley; no CI workflows. These plus
the determinism-dividend features (replays, daily challenge, verified
leaderboards, ghosts) and vibe/reach items (CRT shader, touch, party
instances, Discord, achievements) are recorded in TASKS.md's Future Tasks
as plain bullets — deliberately not checkboxes, so the positional executor
protocol cannot pick up an unspecced idea. Each gets an M7-style spec only
after the owner promotes it.

Addendum, same day — owner design input on three backlog ideas:
* Player identity: glyph fixed at char 2 (☻) forever; background is an
  arbitrary 24-bit RGB picked by the player. Feasible without touching the
  sim because the color rides the protocol/canvas overlay, never the tile
  byte — StateHash, replays, and .ZZT exports stay vanilla. Contrast and
  dark-room visibility rules recorded in the backlog entry.
* A first-party PvP arena world, gated on an explicit per-world opt-in flag
  that re-enables player↔player bullet damage (M2.4/M8.1 disable it by
  design); to be built in the M5 editor as dogfooding.
* A first-party lobby world to replace TOWN as the hangout, with
  server-interpreted cross-world passages acting as a walkable world picker.
Both worlds are owner-flagged "later on in the roadmap"; all three remain
idea-backlog bullets, not tasks.

Addendum, same day: owner added a filler task for rewriting the weak launch
copy (name prompt + WORLD_SELECT_BLURB, web/src/main.ts:310,467) and asked
for the most creative feature directions; eight "moonshots" recorded in the
idea backlog (possession mode, living worlds, the ZZT Continent,
player-authored scrolls, live DM console, crowd-controlled runs,
prompt-to-world, tournament nights), each annotated with the existing
architectural property that makes it feasible. Backlog bullets, not tasks.

## 2026-07-10 — M12 LLM world creator designed; next feature after M7

Owner promoted the prompt-to-world moonshot to the first feature slot after
the M7 bug batch. Order is now M7 → M12 → M5 → M11 → M8 → M9 → M6 → M10.
Core design decisions:
* The LLM writes text, never binary: ZWD, an ASCII-art-grid + legend +
  stats + inline-OOP format compiled to real worlds through the existing
  serializer (worldWriteTo). The compiler is also the security boundary —
  LLM output is compiled, never executed, and bad output is a precise
  compile error, which is exactly what the repair loop feeds back.
* A decompiler is specced alongside the compiler: it turns TOWN/CAVES/CITY
  boards into ZWD, giving in-style few-shot examples, round-trip tests, and
  living documentation for free.
* Style is a curated corpus, not vibes: a system prompt distilling the
  design idioms of the games in this repo (composed scenes, wall outlines
  with color schemes, forest/water texture, key/door/passage gating, terse
  playful scroll writing) plus decompiled real boards as few-shots.
* Generation is entirely outside the sim (server endpoint, env-var API key,
  rate limits, M7.5 validation gate before hosting), so determinism, replay
  fixtures, and the .ZZT format are untouched.

## 2026-07-10 — M7.2 torch-light arrival fix

Room arrival now redraws a lit player's torch circle when entering a dark
board, so a torch carried through a passage or onto a newly joined dark board
does not leave the full circle stale until the next non-adjacent movement.

Known wrinkle, deliberately not fixed in M7.2: `TileToColorAndChar` still
chooses the single nearest player for dark-room lighting. With two players in
one dark room, only the nearer player's torch state controls a tile, so an
active torch held by the farther player may not light that tile. That belongs
in a later multiplayer-darkness pass.

## 2026-07-10 — M7.3 pending-input race fix

`Engine` now guards the presentation/network reply queues
(`PendingScrollReplies`, `PendingDebugCommands`, `PendingSaveFilenames`, and
`PendingQuitReplies`) with one mutex. Submitters append under the lock, and
`GameStepWithInputs` swaps the queues to local slices under the lock before
processing them unlocked, so the sim never runs while holding the mutex.

Verification: `go test -race -run TestWebSocketServerScrollReplyBuysFromVendor
./...` passes. Full `go test -race ./...` still reports an unrelated race in
`TestWebSocketServerTwentyBotSoak`: an HTTP disconnect path
(`ServeHTTP` → `removeClientFromInstance` → `RoomManager.LeavePlayer` →
`Engine.RemovePlayer`/`RemoveStat`) mutates room engine state while the test
calls `StateHash` on the same room. Left for a separate room lifecycle locking
task; it is outside the M7.3 pending-input queue race.

## 2026-07-10 — M7.4 per-player sound attribution

Sounds queued while ticking an `E_PLAYER` stat, and sounds from damage applied
to a player stat, now carry that player's stat id as presentation metadata.
`RoomManager` routes those sounds directly to the matching client instead of
broadcasting them in the room diff. Sounds queued by non-player/object ticks
keep `StatId == -1` and remain room-wide; in particular, ZZT-OOP `#play`
runs during the object's own tick, so everyone in the room hears it.

## 2026-07-10 — M12.0 ZWD format design

M7.5 was intentionally left unchecked for later at the owner's request.

The M12.0 advisor consult was intentionally skipped at the owner's request:
the advisor tool is not available in this environment. The design was grounded
directly in `gamevars.go`, `serialize.go`, `game.go`, and the Wiki of ZZT file
format page instead.

ZWD is a source format only. It does not execute anything, does not write shared
runtime flags, and compiles through the existing `TWorld`/`TBoard`/`TStat`
serializer path. Board and passage references are by board name in source and
resolved to board ids by the compiler. OOP remains stat-local text and follows
the existing `DataLen > 0` / negative bind-index rules.

## 2026-07-10 — M12.1 ZWD compiler

The first compiler pass is intentionally strict and line-oriented. It supports
the M12.0 syntax needed by the documented examples: world name, board
properties, 60x25 grid, legend entries, stats, board-name passage references,
`under` tiles, CP437 byte values, and fenced OOP blocks. It compiles through
the existing board/world serializer (`BoardClose` + `worldWriteTo`) rather than
building binary bytes by hand.

The test reads the two example ZWD fences directly from `ZWD.md`, compiles them,
loads the produced bytes back through `worldReadFrom`, and runs 200 headless
steps. A `testing/quick` property check covers malformed strings for no-panic
behavior. The parser allows a two-space structural indent on grid rows because
the examples in `ZWD.md` are indented inside their board sections; the compiled
grid is still exactly 60 cells wide.

## 2026-07-10 — M7.5 fetch + LLM corpus generation (owner-directed scope)

Owner directed this session: fetch the expanded world list and generate
decompiled training data; do not chase the ZWD round-trip hash tests
(TestZWDRoundTripTOWN/CAVES/CITY were committed red in M12.2 — "hash
verification tracked as follow-up" — and stay red; diagnosis notes: the
mismatches are systematic, mainly StartPlayerX/Y defaulting, World.Info
CurrentBoard/Flags which ZWD cannot express by design, and dropped 0,0
centipede sentinels — not per-world corruption).

Done:
* `worlds.manifest.json` (50 games: 8 required M7.5 titles + 42 high-rated
  1997+ picks) + `cmd/zzt-fetch` downloaded 90 .ZZT files into `engine/`
  (gitignored via engine/.gitignore `*.ZZT`), 0 errors.
* `gen_llmworld_test.go` now picks the TWO best boards per world, scored by
  non-empty tiles + stats*25 + text-cells*3 + distinct-colors*20, and
  recovers from corrupt boards. Regenerated `llmworld/examples/`: 200 ZWD
  boards from ~100 games (STREK1 and WEIRD01 skipped: corrupt boards panic
  BoardOpen).
* `llmworld/STYLE.md`: corpus analysis — structure, shading, lettering,
  population, OOP idioms, color conventions — the M12.3 prompt raw material.
* Restored `debug_prompt_test.go`'s four tests, which a previous session had
  deleted when moving townRoomManager/findEvent into testhelpers_test.go.

Open:
* `cmd/zzt-validate` reports "board render is empty" for 50 of 108 worlds —
  a harness bug (those worlds load fine via WorldLoad in the corpus run),
  left unfixed by owner priority. M7.5 checkbox stays unchecked: validator
  gate + README/AWS deployment note remain.
* Full `go test ./...` remains red on the three pre-existing round-trip
  tests only.

## 2026-07-10 — First generated board + plan-then-paint respec (owner-directed)

Owner goal declared: prompt → entire multi-board ZZT game, generated board by
board. TASKS.md M12.3–M12.5 respecced around a two-phase architecture:
M12.3a (new, world planner + mechanical plan validator: connectivity, exit
reciprocity, spine solvability) and M12.4 (per-board orchestrator with
neighbor-edge context, per-board repair, cross-board checks).

First generated board: `llmworld/generated/MOSSGATE.zwd` ("The Moss Gate"),
hand-authored by the assistant from the STYLE.md corpus idioms, compiled and
passed the 200-step gate on the FIRST attempt — no repair rounds. The corpus
register transfers. `gen_generated_test.go` now compiles+validates everything
in llmworld/generated/ and emits hostable .ZZT files (gitignored).

Reference world plan: `llmworld/plans/LASTLITE.md` ("The Last Lighthouse",
12 boards, key/flag spine, palette rule of rationed warm colors) — the
format exemplar M12.3a's validator will parse, and the game to build first.

## 2026-07-10 — M12.3a world-plan validator

`engine/plan.go` (`ParsePlan` / `ValidatePlan`, +`plan_test.go`). No LLM call,
no sim state. Parses the Markdown board table and progression spine out of a
plan document and checks it mechanically: duplicate ids/indices, board count
≤ MAX_BOARD (100 non-title), passage/exit targets exist, directional-edge
reciprocity, connectivity from the start board, and spine solvability. LASTLITE
passes; orphan-board, key-behind-own-door, missing-passage-target, and
no-return-exit each fail with a specific message. Errors join all problems (one
per line) as repair food for M12.4, matching the ZWD compiler's philosophy.

This task is marked `[ADVISOR]`; the advisor tool was unavailable this session,
so the two judgment calls are recorded here instead:

* **Reciprocity is edge-only, with a passage escape hatch.** The spec says
  "A→E→B implies B→W→A", but LASTLITE returns `village S→cellar` through
  cellar's `passage↔village`, not an edge. So a one-way edge A→B is satisfied
  if B links back to A by the opposite edge OR by any passage/bidirectional
  link. Passages themselves carry no reciprocity requirement — a `passage↔X`
  declared on either endpoint counts as bidirectional for both (this is why
  `undertow passage↔cellar`, declared only on undertow's row, still makes the
  undertow reachable from cellar for connectivity).
* **Spine solvability is an ordering over spine steps, not a re-derivation of
  which board each key sits on.** A key is acquired at its bold `**COLOR KEY**`
  step, a door required at its bold `**COLOR DOOR**` step, a flag set at its
  bold `**FLAG**` step and checked at any later `#if FLAG`. The key/set must
  appear in a strictly earlier step than the matching door/check — "a key
  behind its own door" is exactly the reversed order. Finale reachability is
  covered by full connectivity plus a required `#endgame` step. Tying each key
  to a board and interleaving graph reachability with spine order needs data
  the plan does not cleanly carry; the ordering check passes the exemplar and
  catches all three required bad plans, and is the M12.4 planner's contract.

Full `go test ./...` still red only on the three pre-existing M12.2 round-trip
tests (NOTES 2026-07-10 M7.5 entry); this task adds none.

## 2026-07-10 — M12.3 generation prompt kit (deliverable)

`engine/promptkit.go` (+`promptkit_test.go`): `LoadPromptKit()` /
`PromptKit.SystemPrompt()`. Assembles the generation system prompt from three
embedded ingredients under `engine/promptkit_assets/`: `spec.md` (a verbatim
copy of `ZWD.md` — format grammar AND the M12.0 limits table), `STYLE.md`
(copy of `llmworld/STYLE.md`), and four few-shot board sections. No LLM call,
no sim state — it returns a string. Assembled prompt is 72,781 bytes.

Decisions:
* **The format spec is a required ingredient, not just STYLE + limits +
  few-shots.** STYLE.md is idiom-only (composition, shading, OOP rituals) and
  teaches zero syntax; without the ZWD grammar the model cannot emit compilable
  ZWD. So the kit embeds the whole `ZWD.md` (which contains the limits table
  verbatim, satisfying that clause) plus STYLE.md plus few-shots. MOSSGATE
  compiled first try precisely because its author knew the grammar.
* **Embedded, not read-from-disk.** `llmworld/` and `ZWD.md` live outside the
  engine Go module (module root is `engine/`), so `go:embed` cannot reach them
  and the M12.4 server would be CWD-fragile reading them at runtime. The kit
  embeds committed copies in `promptkit_assets/`; a drift test asserts each copy
  is byte-identical to its source, so editing `ZWD.md`/`STYLE.md` and forgetting
  to refresh fails CI. Required bumping `go.mod` from `go 1.13` to `go 1.16`
  (go:embed minimum; toolchain is 1.26.5, builds already ran under it).
* **Few-shot swap.** Kept the spec's CUTLASS_board27 (action arena) and
  SEWERS_board17 (texture showcase); replaced ONAMOON_board19 and
  OBELISK_board59 (interior/story picks) with DUNGEONS_board20 (framed cavern
  interior) and RAEKUUL_board1 (text lettering + `#zap` dialogue). Reason: a
  few-shot must itself be valid ZWD or it teaches the model bad tokens, and the
  two spec picks carry decompiler artifacts the compiler rejects — ONAMOON has
  raw `element 33`/`element 43` legend entries (elements with empty ElementDefs
  names), OBELISK an off-board `respawn 98,98`. `TestPromptKitFewShotsCompile`
  enforces that every embedded few-shot compiles (wrapped, exits neutralized);
  a corpus scan found 45/200 boards recompile cleanly, and the final four span
  the four archetypes.

**Real-LLM run (DoD):** the assembled prompt was driven by hand against Claude
Opus 4.8 (this session's model) with a flooded-library premise; it produced
`llmworld/generated/ARCHIVE.zwd` ("The Drowned Archive"), which compiled and
passed the M7.5 200-step gate on the first attempt via the existing
`gen_generated_test.go`. Transcript, the exact 72KB system prompt, and the
60-col grid builder are committed under `llmworld/transcripts/` (ARCHIVE.md,
system_prompt.txt, build_archive.py). Honest disclosure in the transcript: the
"real LLM" is this assistant, run manually; the programmatic Anthropic-API
service is M12.4. ARCHIVE is a style/compile proof like MOSSGATE, composed and
in-voice but not solvability-tuned (the Water flood walls off the key/Codex) —
that coherence is what M12.3a + M12.4 enforce.

Full `go test ./...` still red only on the three pre-existing M12.2 round-trip
tests; this task adds none. `go vet ./...` clean.

## M12.4 (2026-07-10) — preprocessor scoping bug, legend scanner, prompt caching, and live generation

* **Preprocessor Coordinate Scoping Bug**: Discovered and fixed a critical Go loop variable pointer bug in `preprocessZWDGrid` (`engine/generation.go`). Inside the stat alignment loop, `bestCoord = &co` took the address of the loop variable `co` rather than the coordinate values. This caused all stats of the same element type (e.g. multiple objects or passages) to resolve to the coordinate of the final iteration element, piling them onto a single grid square and leaving the actual grid tiles without stats. This triggered a `panic: runtime error: index out of range [-1]` in `ElementObjectDraw` when rendering the board. Fixing this ensures accurate stat-to-grid mapping.
* **Unified Legend Scanner Bug**: Fixed naive empty-matching checks (such as checking `strings.Contains(line, "Empty")` against `@ = Player color 0x1F under Empty color 0x00`) that incorrectly parsed the player character as the empty char, corrupting row paddings.
* **Anthropic Prompt Caching**: Enabled standard Anthropic prompt caching for system prompt content blocks. The ~72KB system prompt is cached across subsequent board generation requests in a world generation session, reducing input token costs by ~90%. Test mocks were updated to parse the new structure cleanly.
* **Successful World Generation (`BAKERY`)**: The full generation loop completed successfully, outputting `BAKERY.ZZT`, `BAKERY.zwd`, and sidecars. The recompiled world boots and runs cleanly on `zzt-server` without panics, and is playable in the browser client at `:8080`.
* **OOP Block Indentation Stripping**: Discovered a parser bug where leading indentation spaces in `oop` code blocks inside `.zwd` files were preserved during compilation. In ZZT-OOP, lines with leading spaces are treated as plain message text rather than commands (e.g. `@name` or `#end`). Consequently, indented object code blocks was displayed as scroll windows filled with the raw source code text when touched. Modified `zwdParser.parseOOP` to detect the indentation level of the `oop` keyword and strip it from subsequent lines in the block.

## M12.4b (2026-07-10) — ZZT-OOP generation bugs: quoted text and Passage/Object confusion

* **Quoted dialogue in OOP**: Claude wrapped all NPC dialogue lines in double quotes inside `oop ... end` blocks (e.g. `"Hello traveler."`). In ZZT-OOP, lines beginning with `"` display that literal `"` character on screen as the opening of a text window line. Plain lines (no quotes) are the correct format for in-world dialogue text. Fixed BAKERY.zwd by stripping all quotes from OOP blocks (Python script), updated ZWD.md with an explicit rule and example distinguishing quoted vs. unquoted lines.
* **Passage vs. Object confusion**: Claude generated stats using `element Object` with passage-style glyphs (`cp437:0xF0`) to represent interactive doorways, but without any OOP code or using the `Passage` element, causing dead/unresponsive tiles. Root cause: Claude conflates the visual appearance of a tile (CP437 glyph) with its behavioral element. Added a dedicated **"Passage vs. Object: Critical Distinction"** section to `ZWD.md` and `engine/promptkit_assets/spec.md` with a comparison table and four explicit rules.
* **Monospace grid alignment**: Added a note to `STYLE.md` reminding Claude that ZWD grids are tiled monospace — every character is one fixed-size cell. Block letter art must be planned mathematically: letter widths, spacing, and vertical proportions must be aligned precisely without skewing.

## 2026-07-11 — M12 cleanup: canonical round trips and standalone rendering

M12.7 is now green. `parseStatLine` accepts the legal minimal form `stat at
X,Y element NAME`; the decompiler normalizes OOP display text using the same
text-window wrapping as the compiler, and remaps follower/leader references
when it omits an off-board stat. The TOWN, CAVES, and CITY tests compare each
board's canonical ZWD source after decompile → compile → serialize/reload.
This is intentionally narrower than `StateHash`: ZWD preserves authored board
properties, named tiles, representable stats, and OOP, but not `World.Info`
save state (including current board and flags), player stat-0 runtime fields,
unnamed raw elements (lowered to Empty), or off-board sentinel stats. Replay
hashes are unchanged.

M12.8 is now green. `cmd/zzt-validate` explicitly renders the final board
snapshot before inspecting `Screen`; a static board may otherwise run safely
for 200 ticks without dirtying a board cell. `TOWN` is the regression fixture,
and the standalone command now passes TOWN, CAVES, and CITY.

M12.6 remains open. Regenerating and auditing the historical corpus revealed
additional non-representable source: boards exceeding the one-byte legend
capacity, invalid saved respawns/player positions, and stat-backed tiles with
no stat record. Those require a documented lowering policy or a format change;
they are not masked by the M12.7 test rescope.

## 2026-07-11 — M12.6 authorable-export boundary

M12.6 is re-scoped and complete as an authoring boundary, not a claim of
lossless archival conversion. `DecompileZWD` now returns only authorable ZWD;
otherwise it returns an empty result. `DecompileZWDAuthorable` supplies
structured diagnostics: warnings cover safe lowerings (raw elements become
Empty, off-board stats are omitted, and invalid respawns are omitted), while a
compiler failure becomes an error and no source is returned. The corpus
generator uses this API and skips rejected historical worlds. This prevents
invalid examples from entering future corpus regeneration while preserving a
clear path for a distinct forensic export format later.

The regenerated corpus contains 125 authorable one-board examples. The
rejected historical worlds are intentionally absent rather than retained as
non-compiling prompt material; `TestLLMWorldExamplesCompile` wraps every
fragment as a neutral one-board world and requires all 125 to compile.

## 2026-07-11 — M5.0 editor session model

The required advisor tool was unavailable in this environment. The user gave
explicit approval to proceed, matching the documented fallback used for prior
advisor-tagged tasks.

**Decision:** an `EditorSession` owns a deep-copied pristine `TWorld` and one
headless, never-ticked `Engine`; it is not a `RoomManager` room. `WorldInstance`
therefore retains a `SourceWorld` separate from the mutable live-room state.
Opening `editorEnter{world}` creates this isolated copy, returns an
`editorSnapshot` using the existing 60x25 `ScreenCell` board frame, and leaves
the live player/room maps untouched. `editorInspect{x,y}` only reads tile/stat
data: the cursor itself remains client-local.

The session has a `Members` set (capped at one in M5.0), never an owner field,
and every session operation crosses its serialized `Apply` boundary. M10 can
raise the cap and fan out mutation diffs without changing world ownership or
concurrency semantics. The browser now has a distinct editor sidebar based on
`EditorDrawSidebar`, with a read-only coordinate/element/color/P1/P2/P3 panel;
the play HUD is not reused.

## 2026-07-11 — M5.4 object code editor

The browser code editor is a faithful `TextWindowEdit` port on the M4.1 modal
layer (a new `programEditor` modal in `modal.ts`): raw (unformatted) lines, a
block caret tracking `charPos`, insert/overwrite, `Return`/`Ctrl-Y` line ops,
and **Escape saves** — `EditorEditStatText` always rebuilds `Data` on exit, so
there is no cancel. The server owns the bytes: `editorProgram{statId}` returns
the program split on carriage returns (`CopyStatDataToTextWindow` semantics,
negative `DataLen` resolved like `BoardOpen`); `editorProgramSave{statId,lines}`
rebuilds `Data`/`DataLen` (a CR after every line) and `BoardClose`s so the text
round-trips through the vanilla serializer. Per-line width is not truncated
server-side — `TextWindowEdit` also leaves an over-long externally-authored line
untouched; the browser enforces the 42-char cap only while typing.

**Fork-specific fix (`editorUnbindSharers`):** `BoardClose` rewrites identical
stats' `DataLen` to a negative shared reference *in place*. Vanilla never
notices because its editor closes the board only at save; the fork's per-edit
`BoardClose` (M5.1) can leave a sibling object bound to the one being edited, so
overwriting that object's `Data` would silently rewrite the sibling on the next
serialize. `SaveProgram` therefore un-binds every stat sharing the target's
program (giving each its own copy of the current program) before writing the new
one. Covered by `TestEditorSessionProgramTextEditRoundTrip`.

Bookkeeping: M5.3's box was committed (9a199ea) but never checked; corrected in
this commit.

## 2026-07-11 — M5.5 board management and transfer

Add/switch/name boards and `EditorTransferBoard` (the one dropped procedure) are
now a browser surface over the isolated `EditorSession`. New session methods, all
through `Apply`: `AddBoard` (EditorAppendBoard), `SwitchBoard` (BoardChange),
`ExportBoard`, `ImportBoard`. add/switch/import reply with a full
`EditorSnapshotMessage` — a board change repaints the whole frame, exactly what
`EditorDrawRefresh` does after those ops — so the client reuses
`applyEditorSnapshot` with no new render path.

**Transfer travels over the WebSocket, not HTTP.** The `.BRD` format is a 2-byte
little-endian length + serialized board (vanilla's `BlockRead`/`BlockWrite`), so
export ships base64 board bytes as `editorBoardData`; the browser turns that into
a `Blob` download, and import reads a local file and posts base64 back. HTTP was
rejected because the session state is keyed by the editor's `*webSocketClient` —
an HTTP handler has no clean handle on the per-client session. The exported file
is genuine vanilla `.BRD`, loadable in DOS ZZT/zeta.

**Import is a client-file boundary, so a malformed board must never crash the
server.** `BoardOpen` has no bounds checks (faithful port), so a truncated or
inconsistent `.BRD` would slice past its buffer and panic. `ImportBoard` bounds
the declared length (`== len-2`, `<= len(IoTmpBuf)`) and runs `BoardOpen` under
`safeBoardOpen`, which `recover()`s and rolls the previous board back on any
panic. The editor session is isolated and never ticked, so recovering there only
rejects a bad import — it cannot reach a live room or the sim. A well-formed
all-zero board is *not* malformed: the RLE `Count` byte wraps (0-- → 255), so it
parses as a valid empty board; the guard test uses a length shorter than the
51-byte board name instead. Matching the Pascal, a successful import clears all
four edge exits (they name boards that need not exist in the destination world).

Filenames go through `SanitizeSaveName` (export download stem; "BOARD" fallback
when the board name has non-alphanumeric characters). Covered by
`TestEditorSessionAddBoardAndCrossBoardsInPlay` (create + link both ways + walk
across in a live room), `TestEditorSessionBoardExportImportRoundTrip`,
`TestEditorSessionImportRejectsMalformedBoard`, and the WebSocket-level
`TestWebSocketEditorBoardManagement`.

**Vanilla compatibility (requirement raised during M5.5):** every board edited in
the browser editor is a vanilla-format board by construction. The editor session
is a never-ticked copy that never joins a live player or fires a bullet, so no
multiplayer-only state (extra appended player stats, shot-owner statId in a
bullet's P1) can reach a serialized board; `StoreStat` writes the exact 33-byte
vanilla record and `BoardClose` the vanilla RLE/BoardInfo/stat stream. Exported
`.BRD` bytes and a fully saved world therefore load in DOS ZZT/zeta and ZZTMMO
alike. `TestEditorSessionEditedWorldRoundTripsThroughVanillaFormat` drives the
real `worldWriteTo` -> `worldReadFrom` on-disk byte path to prove it.

## M5.6 — Save, host, download, and upload edited worlds (2026-07-11)

The editor gained a whole-world equivalent of M5.5's board transfer. Three ops on
a new `editorWorld` message, all through `EditorSession.Apply`:

- **save** — `WebSocketServer.saveEditorWorld` serializes the session world via
  `EditorSession.WorldBytes` (the `worldWriteTo` seam, `BoardClose`+`BoardOpen`
  around it exactly as `WorldSave` does), writes it to the worlds directory as
  `<NAME>.ZZT` under `SanitizeSaveName`, and hosts it through the existing
  `HostGeneratedWorld`, so the picker (`ListWorlds`) lists it and a joiner plays
  it. `World.Info.Name` is set to the save name and `IsSave` cleared, so the file
  loads as an authored world, not a saved game.
- **download** — replies `editorWorldData` with the session world's `.ZZT` bytes;
  the browser saves it as a portable `<name>.ZZT` (loads in DOS ZZT/zeta too).
- **upload** — replaces the session world with client `.ZZT` bytes after the M7.5
  gate (`validateGeneratedZWD`: headless load + 200 `GameStep`s, no panic). A
  world that fails the gate is refused with the gate message; the session is left
  untouched (mirrors `ImportBoard`).

**Decisions:**
- *Worlds directory.* Added `WebSocketServer.WorldsDir` and a `-worlds` flag
  (default `.`). `worldsDir()` falls back to the loaded world's directory then the
  working directory — the picker's historical behavior — so nothing changes for a
  server that does not set it. `GetOrCreateInstance` and `handleWorlds` now both
  resolve through `worldsDir()`, so save target and picker never diverge.
- *Collision policy.* A save refuses a world of the same name that anyone is
  currently playing (`len(Instances[name].Clients) != 0`), the same occupancy
  rule as `RestoreSnapshot`, and refuses **before** writing the file — an occupied
  world is never even partially overwritten. Overwriting an empty/hosted or a new
  world is allowed (matches `HostGeneratedWorld`'s own precedent for generated
  worlds).
- *Filename safety.* `SanitizeSaveName` is the whole defense (its charset cannot
  emit a separator or `.`), with a belt-and-braces `filepath.Dir` check identical
  to `snapshotPath`.

Tests: `TestEditorSessionWorldBytesRoundTrip` (download → disk → `LoadPristineWorld`,
board contents intact), `TestEditorSessionWorldBytesLeavesSessionEditable`,
`TestEditorSessionUploadWorldValidatesAndReplaces` (valid replaces, garbage
refused), `TestWebSocketServerEditorSavePublishesAndPlays` (save → picker → join →
step), `TestWebSocketServerEditorSaveRefusesOccupiedWorld`,
`TestWebSocketServerEditorSaveRejectsTraversalName`, and the wire-level
`TestWebSocketEditorWorldSaveAndDownload`. Browser: `S` in the editor opens a
World menu (Save and publish / Download .ZZT / Upload .ZZT).

## M5.7 — ZZT-OOP authoring aids (2026-07-11)

`OopAnalyze(statId)` (`oop_authoring.go`) is an advisory static pass over one
object/scroll program for the browser code editor. It returns the object's
`:labels` (with 0-based line numbers, for navigation) and warnings, and it reuses
the runtime tokenizer primitives — `OopReadChar`/`OopReadWord`/`OopSkipLine`/
`OopReadLineToEnd` and `OopFindLabel`, the same calls `OopExecute` makes — rather
than a second parser, so what it reports is exactly what the engine resolves at
run time. It never executes, never mutates board state, and never blocks a save.

**What it flags:**
- `#send`/`#zap`/`#restore LABEL` where `OopFindLabel` cannot resolve LABEL (an
  unqualified label resolves against this object only; a `Name:label` form
  iterates named objects — both faithful to `OopSend`).
- A bare `#word` that is neither a known command nor a local label. The known set
  is exactly `OopExecute`'s dispatch vocabulary (`oopCommands`); anything else is,
  in ZZT, an implicit self-`#send`, so `#word` matching a local `:word` is valid
  and quiet, while a typo warns.
- A `!label;text` message hyperlink whose label the object does not define
  (label extraction mirrors `TextWindowSelect`; a leading `-` is a file jump, not
  a label, and is skipped).

**Decisions / limitations (advisory tool, deliberately conservative):**
- Warnings and the label list show labels **uppercased**, matching the tokenizer
  (`OopReadWord` upcases) and how ZZT matches labels case-insensitively.
- Only the **leading** command per line is classified. A compound
  `#if cond #send label` validates the `#if` and does not descend into the
  trailing `#send`, so a bad label inside an `#if` is not flagged. Erring toward
  fewer false positives; expanding this is future polish.
- Analysis runs on the **stored** program (on `ProgramText`/open). Since the
  editor closes on save (vanilla `EditorEditStatText` has no cancel), edited-then-
  saved warnings appear on reopen. Saving never blocks
  (`TestEditorSessionProgramAnalysisAndSaveSucceeds` saves a `#send ghost` and it
  stores; the warning shows on reopen).
- A shared program (negative `DataLen`) resolves to its source stat first, like
  `BoardOpen`, so a bound object still lists and validates the real text.

Protocol: `EditorProgramMessage` gained `Labels []OopLabelInfo` and
`Warnings []OopWarning`. The browser renders them in a right-margin panel beside
the code-editor window (`renderProgramAidsPanel`, `modal.ts`). Tests:
`TestOopAnalyze{VendorScript,MissingSendWarns,ZapRestoreAndHyperlink,
UnknownCommandWarns,ImplicitSelfSendIsValid,KnownCommandsAreQuiet}` and
`TestEditorSessionProgramAnalysisAndSaveSucceeds`. Replay fixture unchanged
(analysis is editor-only and never touches the sim).

## M12.11 — Dream-a-world fixes: prose-in-grid tolerance, prompt hardening, progress window (2026-07-11)

Root cause of most Dream failures (reproduced with real `CompileZWD`): the LLM
draws prose/chat straight into the board grid as literal characters. Every grid
char needs a legend entry; letters typed as prose are not legend keys. One failed
board had 31 distinct undefined chars over 147 cells. The compiler reports ONE
undefined char per compile, and the per-board repair budget is K=3 — so a
prose-heavy board can never converge and the whole dream fails. Compiler line
numbers were verified correct; no line-number bug.

Four fixes (all outside the sim — generation is world bytes; replay fixture
unchanged):

- **Fix #1 — compiler tolerance** (`generation.go` `preprocessZWDGrid`, after the
  stat-alignment block, before the rows are appended): scan the finalized 25-row
  grid for any char that is not the player/empty char and not a legend key, and
  inject a legend entry for each — space → `cp437:0x20 = Empty color 0x00`
  (walkable blank); every other byte b → `cp437:0xNN = Text-White color 0xNN`,
  which renders it as white on-board lettering (for Text tiles the legend color IS
  the CP437 char code; `parseByteToken` accepts `cp437:0xNN` as the key). One
  compile now absorbs all undefined chars instead of failing one at a time.
  **Trap found and fixed:** the exclusion set must NOT come from preprocess's own
  `legendMap`, which is built by `SplitN(line,"=",2)` and therefore silently
  drops the `=` key (key equals the separator → empty `parts[0]`) and any
  pre-existing `cp437:` key. Using it re-injected a duplicate `= ` entry and hit
  the compiler's "duplicate legend key". The scan now derives legend keys by
  tokenizing each legend line the way the compiler does (first field, then
  `parseByteToken`), reading the current `lines` so stat-injected keys are
  excluded too. Regression: `TestPreprocessProseInGridBecomesText`.
- **Fix #3 — prompt hardening** (`promptkit.go` output contract, strengthening the
  M12.10 line): every grid char MUST have a legend entry; to show text, map each
  letter to a `Text-<Color>` element (color = CP437 code) or, preferred, put
  dialogue in an Object's scroll text — never type prose into the grid.
- **Fix #A — progress window overflow** (`web/src/dream.ts`): client-composed
  progress lines (e.g. "Painting board 7 of 12: <long name> (attempt 2 of 3)")
  bled past the 50-col window into the sidebar. They are now clamped to the
  window's inner width (`TEXT_WINDOW_WIDTH-8` = 42), truncating with a CP437
  ellipsis. Engine scroll lines arrive pre-wrapped; these do not, so the client
  clamps them. Regression added to `web/test/dream.test.mjs`.
- **Fix #B — progress scroll snap-back** (`web/src/main.ts` `showGenerationProgress`):
  each 500ms poll re-opened the window with `linePos: 1`, snapping the scroll to
  the top so later lines couldn't be read. Now, if the "Dreaming a world" text
  modal is already open, its lines are updated in place and `linePos` auto-follows
  the newest line, avoiding the reopen (and its `stopHeldInput`).

Verified: `go build ./...`, `go vet ./...`, `go test ./...` green (replay fixture
unchanged); client `tsc --noEmit`, `npm test`, `npm run build` green. NOTE: the
background `zzt-serve` on :8090 is still the pre-fix binary — restart it and it
serves the rebuilt client to see these live.

## 2026-07-11 — Error-driven procedural repair layer (compiler self-heals before the LLM)

Owner direction: **maximize what the compiler/decompiler can fix itself before
resending to the model.** LLM repair rounds are slow, cost tokens, and don't
always converge (Saga Archive burned all 3 attempts and blanked to a
placeholder). Every non-convergence is a *hole in the world*, not just spend.
The whole dominant failure taxonomy (2026-07-11 entry above) is mechanical
bookkeeping — exactly the class a deterministic fixer can repair. So the repair
loop should be procedural-first, with the LLM as the fallback of last resort.

This generalizes what already exists: M12.11 (undefined grid char → Text),
M12.13 (orphan glyph → synthesized stat), M12.14 (dup legend key / unknown field
/ missing end) are all ad-hoc procedural fixers in `preprocessZWDGrid`. This
gives them a common home.

### Architecture — compile as a repair fixpoint loop

```
compileWithRepair(src):
  loop:
    result, err = parse(src)
    if err == nil: return result
    fixer = fixers[err.code]              # dispatch on error KIND
    if fixer == nil: return err            # → LLM repair (fallback)
    src, applied, diag = fixer(src, err)
    if !applied || fixpoint(src): return err   # no progress → LLM
    record(diag)                           # audit trail
```

- **Typed error codes are the prerequisite.** `zwdError` needs a structured
  `code` (enum of error kinds) so fixers dispatch on the code, not by
  string-matching the human-readable message (the message is for the LLM/humans).
- **Fixpoint / no-progress guard**: if a fixer makes no change, or the same
  error recurs, or output is byte-identical, stop and hand to the LLM — never
  spin.
- The existing whole-grid normalizations (RLE, padding, player positioning,
  M12.11/13) stay as a first preprocessing pass; the error-driven fixers handle
  what only a parse attempt reveals.

### The load-bearing boundary — which errors are procedurally fixable

**Bucket 1 — bookkeeping / syntactic → FIX procedurally, aggressively.**
Undefined char, orphan glyph, duplicate legend key, unknown stat field, missing
`end`, row width, off-board coords, out-of-range color, the door-nibble-0 crash
(M12.12). The repair is unambiguous and cannot corrupt meaning. This bucket is
the *entire* dominant taxonomy — procedural fixing can drive its repair rounds
toward zero.

**Bucket 2 — semantic / intent → NEVER guess procedurally.** Exit to a
nonexistent board, missing passage target, key placed behind its own door. The
compiler can *detect* these but cannot invent the author's intent; a silent
wrong guess (drop the exit, point at board 0) yields a world that **compiles but
is subtly broken** — worse than a repair round because it's invisible. These go
to the LLM (it knows intent) or are prevented upstream by plan constraints
(M12.3a). A third class — **composition/quality** — raises no error at all;
procedural fixing can't touch it (corpus/fine-tune territory, M12.15).

### Guardrails

- **Emit a diagnostic per fix** (reuse the `generatedGridDiagnostics` channel)
  so every silent deviation from the model's output is visible and testable.
- **Feed diagnostics forward** — into the next board's context or into
  prompt-hardening — so the model drifts toward correctness over time *without*
  a round trip. This recovers the learning signal a repair round provides,
  minus the round trip.

### Why this is high-leverage (ties to the model-choice thread)

If the compiler heals all bookkeeping, **correctness stops depending on model
tier** — the painter's remaining job narrows to composition + intent, the K=3
repair budget is freed for the genuinely hard cases (raising convergence →
fewer blank boards), and a cheaper painter becomes viable for the correctness
dimension. One change improves yield, cuts cost/latency, and decouples quality
from model choice. Filed as M12.16; it subsumes the mechanism of M12.13/M12.14,
which become the first fixers registered in it.

## 2026-07-12 — Planning: full-repo review; M13 and M14 filed; order notes

PROCESS: the advisor tool is unavailable in this environment and the owner
directed this session not to use it; recorded per rule 5. A second session
was committing M12.15b/c work concurrently while this file and TASKS.md were
being edited — two edits were clobbered mid-write and reapplied. Lesson:
when two sessions run at once, only one should hold the planning docs.

Whole-repo review at the owner's request (state verified 2026-07-12:
`go build`/`go vet`/`go test ./...` fully green including the once-red
round-trip tests; no CI workflows; web client at engine/web, ~3.9k lines TS
with node-driven tests but no runner in CI). Assessment in one line: the
engine and its process discipline are in excellent shape; nearly all current
friction is (1) generation robustness — being addressed by the M12 cleanup
arc — and (2) service survivability, which had no tasks at all. Decisions,
all filed as full specs in TASKS.md:

* **M13 (new): hygiene, CI, reconnect grace, autosave, the lifecycle race.**
  Placed before the remaining M12 tasks: the fixture only protects commits
  that run it, and a crash or a Wi-Fi blip currently deletes players' runs.
  M13.0 also absorbs the stray `engine/NOTES.md` (mis-filed log content) and
  the STARGEN untracked trio, and folds the touch_race_test retitle bullet.
* **M12.16 moved physically ahead of the remaining M12.15 slices** (under a
  new "M12 continued" header placed after M13). Procedural repair raises
  yield; the corpus/style work builds on top of it. The executor protocol
  is positional, so the priority had to be file order, not a note — the
  same reasoning as M7's placement (2026-07-10 entry). M12.16 also absorbed
  three Future-Tasks bullets as registered fixers/checks: passage-stat
  synthesis from the legend `to` clause (bucket 1), aggregate orphan
  reporting (bucket 1), passage color reciprocity (bucket 2, detect-only).
* **M14 (new): rearchitecting.** M14.0 one-seam world-scope state (the flag
  sync fix 67a642c generalized so the next world-scope field can't silently
  not propagate); M14.1 retire the DefaultInstance special case + server-
  scoped PlayerIDs (unblocks multi-world cleanly, de-risks M11); M14.2
  session recording at the tick boundary (replays/ghosts/daily challenge all
  reduce to it); M14.3 an OPTIONAL two-package split with an explicit stop
  signal — skip-and-record is a legitimate outcome.
* **Six new moonshots** in the idea backlog (second batch): Endless Dungeon
  (edge-context generation as geography), critique flywheel (zzt-shot +
  vision scoring closes a quality loop), in-world Dream Machine, style
  séances (decompile→transform→recompile), Daily Dreamed Challenge, AI
  Dungeon Master. Backlog bullets, not tasks — owner promotes first.

Execution order after this session: M13 → M12.16 → remaining M12.15 slices →
M14 → M11 → M8 → M9 → M6 → M10.

## 2026-07-12 — M13.1: CI gate; clean-clone skip guards

`.github/workflows/ci.yml` added: **engine** (go build/vet/test, Go 1.26.x
pinned — `engine/go.mod`'s `go 1.16` is the go:embed language floor, not the
toolchain), **engine-race** (`go test -race`, `continue-on-error: true` until
M13.4 fixes the TestWebSocketServerTwentyBotSoak race, 2026-07-10 M7.3 entry),
and **web** (npm ci / test / build in `engine/web`; the lockfile was already
tracked, so no lockfile commit was needed).

Clean-clone proof (`git clone . && go test ./...`) found three tests that
depended on untracked local files; guards added per the M13.1 spec:

* `TestZWDRoundTripCAVES` / `TestZWDRoundTripCITY` — `t.Skip` when the
  gitignored world file is absent (`testZWDRoundTrip` now stats the resolved
  path, the `testhelpers_test.go:19` pattern). `TestZWDRoundTripTOWN` still
  runs everywhere: `fixtures/TOWN.ZZT` is tracked.
* `TestValidateRendersStaticTown` (cmd/zzt-validate) did not fail on a clean
  clone — it **hung forever**, timing out the whole suite at go test's 10m
  limit: it pointed at the untracked `engine/TOWN.ZZT`, and a missing file
  sends `validate` → `WorldLoad` → `DisplayIOError`, whose modal
  `TextWindowSelect` blocks on a keypress nothing feeds headless. Repointed
  the test at the tracked `fixtures/TOWN.ZZT` (byte-identical to the engine
  copy, md5-verified), so the M12.8 guard actually runs in CI instead of
  skipping; a stat-based skip guard sits before the call because a skip
  after it can never be reached.

Clean clone and the local tree are both fully green (build, vet, test); the
web job's npm ci/test/build verified on the clean clone. Replay fixture
untouched — no simulation change.

## 2026-07-12 — M13.1 follow-up: first CI run; -race caught two more tests

Run 29185446610 (commit 8630a7c): engine ✓ 36s, web ✓ 13s, engine-race ✗
allowed — the DoD state. On the runner, `-race` failed not on the soak test
but on `TestM43aSaveOverWebSocket` and
`TestWebSocketServerTwoClientsSeeAndFight`, both racing between
`WebSocketServer.Tick → safeStepDiffs → RoomManager.StepDiffs` (write) and a
reader reaching `hudSnapshot` via another `StepDiffs` — i.e. the same
room-lifecycle/tick locking hole M13.4 owns, showing up in more tests under
CI timing. Evidence for M13.4's diagnosis step; no action here.

## 2026-07-11 — Dream-a-world generation failure taxonomy (planning for M12.13/M12.14)

*(Moved here by M13.0 from the misplaced `engine/NOTES.md`; original date
preserved. The "2026-07-11 entry above" reference in the 2026-07-11
"Error-driven procedural repair layer" entry points at this taxonomy.)*

Pulled from one crashed-server session log (2 worlds, ~16 board paints). EVERY
repair round was mechanical bookkeeping, never a creative failure. The LLM is
asked to keep two representations in byte-for-byte agreement — the ASCII grid
AND a separate `stats` list with exact `at X,Y` coordinates — which it cannot do
reliably. Counts from that one session:

- **`grid contains stat-backed element X but no matching stat is defined at (x,y)`
  — 5+, and Saga Archive burned all 3 repair attempts on it (never converged).**
  DOMINANT failure. The model draws an Object/Passage glyph in the grid but does
  not declare a matching stat (or declares it at the wrong coord). Compiler check
  is the reverse-direction loop at `zwd.go:786-805` (error at `:801`);
  stat-backed set is `elementNeedsStat` (`zwd.go:817`).
- `duplicate legend key` (`zwd.go:348`) — 1.
- `unknown stat field <name>` (`zwd.go:612`) — 1.
- `board <name> missing end` (`zwd.go:204`) — 1 (truncated/malformed section).
- prose-in-grid undefined chars — fixed in M12.11.
- plan-level exit reciprocity — already auto-repaired by the plan validator.

Strategy (owner direction 2026-07-11): move more mechanical burden onto the
compiler / `preprocessZWDGrid` tolerance layer so LLM slips are absorbed
deterministically instead of bouncing through the slow, token-costly, sometimes
non-converging repair loop. `preprocessZWDGrid` already pads rows, expands RLE,
positions the player, snaps DECLARED stats to their nearest glyph, and (M12.11)
absorbs undefined grid chars. The gaps that remain are the orphan-glyph direction
(M12.13) and the other recurring compiler rejections (M12.14). Principle: the
compiler owns everything deterministic (coordinates, bookkeeping, RLE, padding,
legend completeness, structural closing); the LLM owns only semantic/creative
choices (layout, palette, which entities, their words). Derive, don't require.

## 2026-07-11 — M12.12 malformed doors and room panic containment

*(Moved here by M13.0 from the misplaced `engine/NOTES.md`; original date
preserved. The "door-nibble-0 crash (M12.12)" reference in the 2026-07-11
"Error-driven procedural repair layer" entry points at this entry.)*

ZWD is the generated-world security boundary: the compiler now rejects raw
`Door` colors whose key/background nibble is `0` or `8`, since neither names a
vanilla key. The documented named shorthand remains valid (`Door color blue`
becomes `0x1F`; the other six key names map similarly). The simulation also
guards `key == 0` and treats a malformed door as locked, because imported DOS
worlds can still carry one.

`RoomManager.StepDiffs` recovers separately around each room's simulation step,
logs the panic, and drops the affected room and its players rather than keeping
a partially-mutated engine. `WorldInstance.Tick` (and the legacy tick path) has
an outer recovery guard as a final boundary. These safeguards are presentation /
server control flow only: neither changes `StateHash` nor the replay fixture.

## 2026-07-12 — M13.2 reconnect grace: decisions before coding

A dropped WebSocket (refresh, Wi-Fi blip) no longer destroys the run. On
read-loop exit the client is *detached*, not left: it leaves `inst.Clients`/
`inst.Inputs` (and the default instance's `s.clients`/`s.inputs`) but its stat
stays on the board and keeps ticking with zeroed input, exactly like an idle
player. `inst.Detached[playerID]` counts down `ReconnectGraceTicks` (545 ≈ 60s
at the 110ms tick — counted in ticks, never wall-clock, so tests step it
deterministically). Expiry runs on the tick goroutine (`s.expireDetached` at the
top of `WebSocketServer.Tick`, once per instance per tick) and performs today's
`LeavePlayer` removal. A 16-byte `crypto/rand` resume token (hex) is minted at
join, mapped in `inst.ResumeTokens`/`inst.TokensByPlayer`, and sent on the
join/resume snapshot as `SnapshotMessage.ResumeToken`. `JoinMessage.ResumeToken`
resumes: a token naming a live-or-detached player in this instance reattaches
(same PlayerID/statID, inventory intact); an unknown or stale token falls through
to a normal fresh join, never an error.

The three decisions the spec required, recorded before coding:

1. **A second live connection presenting a token whose player is NOT detached —
   newest-wins.** The resume displaces the old socket: the new client takes
   `inst.Clients[pid]`, and the old connection is closed. The old socket's
   read-loop exit then sees `inst.Clients[pid] != itsClient` and returns without
   detaching, so it cannot cancel the new attachment. This is also the fix for
   "my tab froze, I opened a new one."

2. **`pendingPlayerEvents` for a detached player are cleared on detach and the
   loss accepted.** They are presentation-only (scroll/sound/transfer routing),
   and only a connected client drains them (`DrainPlayerEvents`). Without the
   clear they would accumulate for the whole grace window; the reattaching client
   gets a fresh full `Snapshot` anyway, so nothing of value is lost.

3. **A detached player holding a scroll open (`roomPlayer.scrollOpen`) stays
   frozen until expiry — acceptable.** Their stat simply does not tick until they
   resume (and the client dismisses the scroll) or the grace runs out and they
   are removed. It matches "detached players are idle," and a co-op partner is
   never blocked because only that one stat is frozen.

Server-layer only: no simulation change, the resume machinery never enters
`StateHash` or serialization, replay fixture unchanged. `go test ./...` green.

NOTE (M13.4): detach now moves the heavy room-state mutation (`RemovePlayer`/
`RemoveStat`) off the disconnect goroutine and onto the tick goroutine at expiry
— the shape M13.4's race fix wants. Verified the default-instance race is
pre-existing, not introduced here: `go test -race -run
TestWebSocketServerMultiplayerSmokePickupTransferHUD` fails the same way on a
clean tree (read-loop exit → `removeClientFromInstance` → `RemovePlayer` under
`inst.mu`, vs the legacy tick reading the shared default RoomManager under
`s.mu`). This task's disconnect path does a *lighter* off-tick touch than the
removal it replaced (only `DrainPlayerEvents`, per decision 2), still under the
same `inst.mu`; closing the default-instance `s.mu`/`inst.mu` split is M13.4's,
which removes engine-race's `continue-on-error`.

## 2026-07-12 — M13.3 autosave and restore-on-boot: freshness policy and decisions

Nothing snapshotted automatically before this — `SaveSnapshot` ran only on a
player's `S` press — so a crash or restart lost every live room. M13.3 closes
that on the server layer only (no simulation change; replay fixture unchanged).

**Cadence.** `-autosave <seconds>` on `cmd/zzt-server` (default 60; 0 disables)
becomes `WebSocketServer.AutosaveEveryTicks = seconds*1000/110`, and the tick
loop counts toward it (`maybeAutosave` at the end of `Tick`). One clock, no
second timer — tests step the seam directly (`maybeAutosave`, `Autosave`).
`NewWebSocketServer` leaves `AutosaveEveryTicks` 0, so tests never autosave
unless they opt in.

**Write path.** `Autosave` snapshots every *occupied* instance (`len(Clients)>0`;
the default instance is in `s.Instances`, so the map loop covers it) to
`SavesDir/autosave/<INSTANCENAME>.SAV`. The world copy is taken under `inst.mu`,
then the file is written after the lock is released — a running room never blocks
on disk I/O (M4.3a's "a save never disturbs the game it is a save of"). Writes are
atomic (`*.tmp` then `os.Rename`, in the shared `writeWorldSnapshot`, which the
`S`-key `SaveSnapshot` now also uses): a crash mid-write can never corrupt the
previous good autosave. Instance names are re-`SanitizeSaveName`d at save time
and skipped-with-log if they fail, rather than guessing a safe filename.

**Autosave has no saver → zero inventory (documented choice).** `SaveSnapshot`
writes the saving player's inventory into the vanilla one-player `World.Info`
fields (M4.3a). Autosave has no saver, so `snapshotWorldNoSaver` zeroes those
fields (health/ammo/gems/torches/score/keys/board-time) rather than leaking
whatever the frozen world carried. This is harmless: the server already ignores
those fields on join (M4.3a decision 1 — a joiner arrives fresh), and a snapshot
drops all players anyway, so nobody's inventory is expected to survive.

**Players are dropped from an autosave**, same as an `S` save (M4.3a decision 1):
`snapshotRoomBoard` strips `E_PLAYER` stats. A round-trip restored board holds
zero player tiles; joiners arrive fresh.

**Freshness policy (the one the spec asked to record): an autosave beats the
pristine `.ZZT` at boot.** That is what crash recovery MEANS — the last live
state, not the shipped world. `RestoreAutosaves` runs at startup, before serving:
for each `SavesDir/autosave/*.SAV` whose name matches a hostable world
(`GetOrCreateInstance` returns the default instance for its own name, or loads a
pristine hostable world otherwise; a name with no hostable world is skipped), it
restores that world as the instance's starting state. Deleting the autosave file
is the operator's reset. `-fresh` skips restore entirely for a deliberately clean
boot (it simply does not call `RestoreAutosaves`). A corrupt or truncated autosave
is logged and skipped, never a boot failure: `safeRestoreSnapshot` wraps the
restore in a recover, because a garbage board length can reach a `make` and panic
(same survivability floor as M12.12).

**Occupancy refusal at boot is vacuous** — `RestoreSnapshot` refuses while a room
is occupied, but at boot nobody has joined, so restore always proceeds.

## 2026-07-12 — M13.4 room-lifecycle race: the pair, the fix, the leftovers

**The racing pair the detector actually reported** (not the pair the spec
guessed at). Both sides call `RoomManager.DrainPlayerEvents` on the **default
instance's** RoomManager:
* tick goroutine — `WebSocketServer.Tick` → the legacy `s.mu` block →
  `DrainPlayerEvents` (was `websocket_server.go:170`), holding `s.mu`;
* HTTP disconnect goroutine — `handleReadLoopExit` → `DrainPlayerEvents`
  (`websocket_server.go:1323`, clearing pending events on detach per M13.2
  decision 2), holding `inst.mu`.

Root cause is the **default-instance dual-mutex split**: the default instance is
the only one whose RoomManager was stepped by a separate "legacy" tick block
under `s.mu`, while every *other* access to that same RoomManager (join, leave,
detach, input) holds `inst.mu`. Two mutexes guarding one RoomManager never
exclude each other. Every non-default instance was already correct — its
`WorldInstance.Tick` and its `handleReadLoopExit` both hold `inst.mu`, so they
are mutually exclusive. The join path (`JoinPlayer` under `inst.mu`) raced the
legacy tick the same way; the detector only happened to catch `DrainPlayerEvents`
first.

**Fix (fewer lock orderings, not more locks — the spec's stated preference):**
retire the legacy `s.mu` room-stepping block entirely and tick the default
instance through `WorldInstance.Tick` under `inst.mu`, exactly like every other
hosted world. After this the default RoomManager is touched under one lock
(`inst.mu`) on every path, so tick vs join/leave/detach are mutually exclusive by
construction. This is a targeted slice of M14.1's "one instance model" collapse.
The now-orphaned `s.inputs` mirror write in `inst.setInput` was removed (nothing
drained it once the legacy tick was gone — it would have leaked, and the soak
test caps alloc growth). `s.clients` stays maintained; the soak test still reads
it to count active clients, and it is connection state consistently under `s.mu`,
never part of the race. The dead legacy `s.*` methods
(`s.setInput`/`s.submitQuitReply`/`s.submitHighScoreName`/`s.submitSaveFilename`/
`s.removeClient`, all zero-callers) were left in place for M14.1 to delete with
the rest of the default-instance special case; they cannot race because nothing
calls them. No mutex was added to `Engine`; `go vet` copylocks stays clean.

**Second distinct race found while running `-race` (fixed here, test-only):**
several tests launched `go server.Run(ctx)` with only `defer cancel()` and never
joined it, so a server's tick goroutine outlived its test and leaked into the
next one, where it read the package-global `ElementDefs` that the next test's
`WorldCreate → InitElementsGame → InitElementDefs` (`elements.go:1548`) rewrites —
a `-count`-back-to-back race. Fixed with a `runServerAsync(t, ctx, server)` test
helper that joins the goroutine in `t.Cleanup` (the caller's `defer cancel()`
still fires first and unblocks `Run`). Applied to all five launch sites.

**Third distinct race — pre-existing, NOT fixed here, filed per the spec.**
`ElementDefs` is a package-level global (`gamevars.go:415`) that
`(*Engine).InitElementDefs` rewrites, and it is read as a global in thousands of
sim sites. Any `InitElementsGame` re-init therefore writes shared state. In
production the live one is **ZWD generation**: `preprocessZWDGridWithWarnings`
(`generation.go:907`) and the ZWD compile/decompile paths (`zwd.go:33,117`,
`zwd_decompile.go:50,507`) each spin up a throwaway `NewEngine()` and call
`init.InitElementsGame()`, which — because `ElementDefs` is not per-engine —
writes the global while hosted rooms tick and read it. `WorldLoad` (`game.go:660`)
does **not** re-init, so on-demand world *load* is safe; only world *create* and
the generation/ZWD paths re-init. The write is value-benign (InitElementDefs is a
pure function of constants, so the bytes are identical every time) but still a
data race under Go's memory model. No test triggers it after the goroutine-leak
fix, so the `-race` job is green; the proper fix (a `sync.Once` init, or moving
`ElementDefs` onto the Engine) is a large mechanical change across every read
site and belongs to a future task (natural fit with M14.0/M14.1's world-scope
seam), not M13.4.

**Verification:** `go test -race ./...` run 5× over the whole module, plus 6×
`-count=3` engine-only and ~24 hammered soak/smoke iterations — 0 races, 0
failures. Server-layer only; `StateHash`, serialization, and the replay fixture
are untouched. `engine-race` loses its `continue-on-error` and is now required.

## 2026-07-12 — M12.16 error-driven procedural repair layer (implementation)

PROCESS: the advisor tool is unavailable in this environment (same as the
2026-07-12 planning entry), so the required `[ADVISOR]` consult on the
error-code taxonomy / bucket boundary could not run. Recorded per rule 5. The
one load-bearing architectural fork was surfaced to the owner instead
(AskUserQuestion): **layer on top** — keep `preprocessZWDGridWithWarnings` as
pass 1 unchanged, add `CompileZWDWithRepair` as a new error-code-driven pass 2,
and test the dispatch loop directly on RAW broken boards so each fixer actually
fires (preprocess is not in the unit path). No rewrite of the working M12.11/13/
14 fixers.

### What landed
* **Typed error codes.** `zwdError` gains a `code zwdErrCode`; a `zerrc` helper
  and `retagZerr` (preserves an inner coded error's code while stamping
  line/col) tag the bucket-1 sites. Fixers dispatch on the code, never the
  human message (which stays precise for the LLM/humans, M12.1).
* **Fixpoint loop.** `compileZWDWithRepair` / `CompileZWDWithRepair`: parse → on
  a coded bucket-1 error look up `zwdBucket1Fixers[code]` → apply to source →
  re-parse → repeat until success, no fixer, or no progress (byte-identical
  output or a `seen` source recurs → hand back to the caller/LLM). Hard
  iteration cap as a backstop; never spins.
* **Bucket-1 fixers.** missing-end → `autoCloseZWDSections`; duplicate-legend-key
  → `deduplicateZWDLegendEntries`; unknown-stat-field → `dropUnknownZWDStatFields`
  (the three M12.14 whole-source helpers wired as table entries); plus new
  source fixers: row-too-wide (truncate to 60), off-board-coord (drop the stat),
  color-range (rewrite to 0x0F), door-nibble (M12.12 — set the Door bg nibble to
  a valid key when 0/8); undefined-grid-char and orphan-stat-glyph delegate to
  `preprocessZWDGridWithWarnings` on the enclosing board section (reuses the
  M12.11 legend injection and M12.13 stat synthesis — "subsumes the mechanism").
* **Aggregate orphan reporting (folded bullet 2).** `compileZWDBoard` now
  collects EVERY orphan stat-backed tile and every undefined grid char into one
  coded error instead of stopping at the first, so one fixer pass repairs all.
* **Passage-stat synthesis from the legend `to` (folded bullet 1).** The orphan
  fixer synthesizes a Passage stat with `p3 board "NAME"` when the glyph's
  legend entry carries a `to` destination — coordinate AND target both derived,
  never guessed (bucket 1).
* **Passage color reciprocity (folded bullet 3, BUCKET 2 / detect-only).**
  `checkZWDPassageReciprocity` warns when a passage's destination board has no
  matching-color return passage; it is DETECT-only (routed to the LLM/plan
  repair), never re-colored procedurally. Authoring rule added to `ZWD.md` and
  `promptkit_assets/spec.md`.

### The bucket boundary (load-bearing, unchanged from the 2026-07-11 design)
Bucket 1 (bookkeeping/syntactic) is fixed procedurally and aggressively; it is
the entire dominant failure taxonomy. Bucket 2 (semantic/intent — exit to a
nonexistent board, missing passage target, key behind its own door, passage
reciprocity) is NEVER guessed procedurally: a silent wrong guess compiles a
subtly-broken world, worse than a repair round. Bucket-2 errors have no fixer
registered, so the fixpoint loop returns them to the caller unchanged for the
LLM path. Composition/quality raises no error and is out of scope (M12.15).

Generation wired: `paintBoard` and the batch painter call
`CompileZWDWithRepair`, feeding repair diagnostics forward via
`generatedGridDiagnostics`. Purely generation/compile-time — outside the sim;
replay fixture unchanged.

## 2026-07-12 — M14.0 world-scope state: one seam instead of scattered syncs

The required advisor tool was unavailable in this environment; the user gave
explicit approval to proceed on deferring M12.15d and moving to the next task,
matching the documented fallback for advisor-tagged work. (M14.0 itself is not
[ADVISOR]-tagged.)

**Audit table — every `TWorldInfo` field (`gamevars.go:96-110`), classified by
whether it is per-player, world-scope, or save-file/per-engine, with the
evidence (which code reads/writes it from a room engine after load):**

| Field | Class | Evidence |
|---|---|---|
| `Ammo` | per-player | virtualized in `PlayerState` (`gamevars.go:351`); sim reads `PlayerFor(statId).Ammo`, not `World.Info.Ammo` |
| `Gems` | per-player | `PlayerState.Gems` (`gamevars.go:352`) |
| `Keys[7]` | per-player | `PlayerState.Keys` (`gamevars.go:357`); door/key touch acts on the triggering player (M2.4) |
| `Health` | per-player | `PlayerState.Health` (`gamevars.go:350`); `DamageStat`/respawn are per-stat (M2.4) |
| `Torches` | per-player | `PlayerState.Torches` (`gamevars.go:353`) |
| `TorchTicks` | per-player | `PlayerState.TorchTicks` (`gamevars.go:354`); dark-room lighting is per-player (M7.2) |
| `EnergizerTicks` | per-player | `PlayerState.EnergizerTicks` (`gamevars.go:355`) |
| `Score` | per-player | `PlayerState.Score` (`gamevars.go:356`); high scores per-player on `RoomManager` (M4.3) |
| `BoardTimeSec` | per-player | `PlayerState.BoardTimeSec` (`gamevars.go:358`) — time limits are per-player |
| `BoardTimeHsec` | per-player | `PlayerState.BoardTimeHsec` (`gamevars.go:359`) |
| `Name` | world-scope, **immutable during play** | the world title; written only at load/editor (`editor.go:165`, `editor_session.go:244`), read straight off `rm.world` (`WorldName`, `room_manager.go:575`). Never mutated by a stat tick, so it needs no per-tick room->room seam. |
| `Flags[MAX_FLAG]` | **world-scope, mutable during play** | `#set`/`#clear` -> `WorldSetFlag`/`WorldClearFlag` mutate the *ticking room's* engine copy; shared puzzle progress. The ONLY per-tick-mutable world-scope field, and the one the 2026-07-11 bug (commit 67a642c) missed. |
| `CurrentBoard` | per-engine (NOT world-scope) | each room engine is opened on its own board (`ensureRoom` -> `BoardOpen`, `room_manager.go:613`); `BoardChange` writes it per engine (`game.go:193`). Syncing it across rooms would make every room render the same board, so it must NOT enter the seam. |
| `IsSave` | save-file-only | set false on load/`BoardChange` (`game.go:256`), consumed by the serializer; not touched by a tick. |
| `padding1`, `padding2` | inert | struct padding, TODO-removal; carried by value copies, never read. |

Conclusion: the world-scope sync list that must propagate room->room every tick
is exactly `{Flags}`. `Name` is world-scope but immutable-during-play (read from
`rm.world` directly, so out of the seam). Everything else is per-player or
per-engine.

**Mechanism.** Replaced the four-site flag dance (`room_manager.go:426`, `:473`,
`syncWorldFlagsFromRoom`, and the flag lines inside `freezeRoomIfEmpty`/
`syncFrozenBoardToLiveRooms`) with two named seams that each iterate ONE explicit
list — `copyWorldScope(dst, src *TWorldInfo)` (the single place to add the next
world-scope field):

* `refreshRoomWorldScope(room)` — pull world-scope fields from `rm.world` into a
  room's engine *before* it steps (so a `#set` in an earlier-ticking room is
  visible this same tick).
* `publishRoomWorldScope(source)` — copy a just-stepped room's world-scope fields
  back to `rm.world` and fan them to every live room; a `worldScopeEqual`
  short-circuit preserves the old no-op-when-unchanged behavior (avoids O(rooms^2)
  churn per tick).

Both freeze (`freezeRoomIfEmpty`) and thaw route through these: freeze publishes
the frozen room's scope before deleting it; thaw is already covered because
`ensureRoom` does `engine.World = rm.world` (a value copy that carries `Flags`).
`syncFrozenBoardToLiveRooms` now refreshes each remaining room's scope through
the seam instead of assigning `Info.Flags` inline.

**No shared pointers.** Kept the value-copy shape (rooms step sequentially on one
goroutine inside `StepDiffs`, so copies are race-free by construction); `Flags`
is a `[MAX_FLAG]string` value array, comparable and copyable. No `TWorldInfo`
field was added or removed, so `StateHash` and `worldWriteTo` are byte-unchanged;
replay fixture unchanged.

---

## 2026-07-12 — M14.2 Session recording (the determinism dividend)

**Seed audit (the "find them ALL" the spec demanded).** Grepped
`RandomSeed`/`RandSeed` across engine + server. RNG is per-`Engine`
(`e.RandSeed`, seeded via `e.Random`); the package-level `Random`/`RandomSeed`
act on the global `E` (terminal/tools only). On the *server* path there is NO
`RandomSeed` call at all: `cmd/zzt-server/main.go` never seeds, and each room
engine is minted by `NewEngine` (`room_manager.go:ensureRoom`) with the zero
value `RandSeed == 0`. No seed derives from wall-clock (CLAUDE.md rule 2 forbids
`time.Now()` in sim). So the only seed a recording must carry is the constant 0,
recorded explicitly in the header (`newSessionHeader`) so playback verifies the
assumption rather than inheriting it silently. `RandomSeed(42)` appears only in
`cmd/zzt-validate` and `cmd/zzt-smoke`, neither on the record/replay path.

**Recording altitude: the RoomManager seam, not WorldInstance.** The spec names
"the SAME entry points (`JoinPlayer`, `StepDiffs`, `Submit*`, `LeavePlayer`)" —
those are RoomManager methods, and every server mutation funnels through them
under `inst.mu`. Hooking there (not at the WebSocket layer) makes the recorder
testable without socket plumbing and keeps `websocket_server.go` changes to one
wiring call per instance-creation site. Every hook is nil-guarded, so recording
OFF is byte-for-byte prior behavior (`TestSessionRecorderDisabledIsInert`).

**Flush at the top of `StepDiffs`.** The recorder buffers external ops (joins,
names, leaves, submits) as they arrive and emits one `recTick{tick, ops, inputs}`
at the top of each `StepDiffs`. Because the server holds `inst.mu` across the
whole join/submit/leave/step window, the buffer at StepDiffs-top holds *exactly*
that tick's external stimuli. Consequences the sim derives — transfers, respawns,
quit-driven removals — are NOT recorded; playback regenerates them from the same
inputs. This is why a transfer needs no op (`TestSessionRecordReplayTransfer`).

**Quit-leave suppression.** `LeavePlayer` is the shared funnel for both an
external drop (recorded) and a quit's internal removal (`quitPlayer`, a
consequence of a recorded `SubmitQuitReply`). A `recSuppressLeave` flag set around
`quitPlayer`'s `LeavePlayer` keeps the quit-leave out of the log so replay does
not double-apply it. Set/cleared on the tick goroutine only; external leaves can
never interleave (both under `inst.mu`).

**Save vs. highscore submits.** Both are recorded for session fidelity. On
playback a highscore submit is re-applied (`RecordHighScore`, harmless — not in
per-room `StateHash`); a save submit is skipped — it only writes a file, has no
simulation effect, and playback has no target directory.

**Self-contained replay file.** The header embeds the pristine starting world as
base64 (`worldToBytes` → `LoadWorldBytes`) plus an FNV-1a integrity check, so a
recording needs nothing else to replay — the foundation for shareable replays and
ghost racing. Per-tick lines are kilobytes; the world is a one-time header cost.

**Non-blocking writes.** `SessionRecorder` hands each `recTick` to an async
writer goroutine over a bounded channel; a full channel drops the line and counts
it (logged on `Close`), so the tick never blocks (spec's "drop-and-count").

**Graceful shutdown added.** `cmd/zzt-server` previously ran on
`context.Background()` with no signal handling, so nothing ever cancelled the tick
loop — the buffered tail of a recording (and any bufio contents) would be lost on
exit. Added `signal.NotifyContext` (SIGINT/SIGTERM) → `server.Run` drains and
`CloseRecorders()` flushes on ctx.Done, plus an `http.Server.Shutdown`. Presentation/
server layer only; no sim change, replay fixture unchanged.

Verified end-to-end: `cmd/zzt-server -record <dir>` writes `TOWN-<stamp>.jsonl`
(header + one line/tick, world hash embedded), and `cmd/zzt-replay <file>`
reloads the world (hash-checked) and replays it. Tests reproduce per-room
`StateHash` at every 100 ticks and at the end for a two-player TOWN
vendor/scroll-reply/quit session and a two-board passage transfer. `go test
-race ./...` green; replay fixture unchanged.

## M8.1 — point-blank shots: target energizer + PvP decision (2026-07-12)

`BoardShoot`'s point-blank damage branch (`game.go`) had two multiplayer bugs
where vanilla read the one player's `World.Info.EnergizerTicks` (GAME.PAS
BoardShoot, `= Boolean(source)` clause and the energizer guard):

1. **Energizer (the stated M8.1 bug).** The Go read `PlayerFor(0).EnergizerTicks`
   regardless of who stood in front of the shooter, so player 0's energizer
   protected (or failed to protect) an entirely different player on the target
   square. Fix: new `pointBlankEnergizerTicks(x,y)` resolves the stat on the
   target square via `StatAt` when the tile is `E_PLAYER` and reads *that*
   player's ticks; non-player targets keep `PlayerFor(0)` (unchanged). The
   `(Element==E_PLAYER)==(source>=SHOT_SOURCE_PLAYER_BASE)` term is left
   byte-for-byte per the task.

2. **PvP decision — RESOLVED: point-blank follows BulletTick's no-PvP rule
   (the recommended option).** The condition term above lets a player-owned
   shot (`source>=BASE`) damage a player point-blank, which contradicted
   BulletTick's M2.4 ownership rule (player bullets don't damage players unless
   `FriendlyFire`, and never self). Reconciled by mirroring BulletTick: inside
   the damage branch, when the target is `E_PLAYER` and the shot is
   player-owned, no damage if `!FriendlyFire` or `targetStatId == ownerStatId`
   (returns `false`, so the shot fizzles and no ammo is spent). `FriendlyFire`
   defaults true (`NewEngine`), so default multiplayer still allows PvP but now
   honors the same flag and self-protection as bullets.

Note (out of scope, left as-is): the Go port's `source>=BASE` substitution for
Pascal `Boolean(source)` also silently dropped vanilla's *enemy* point-blank
damage to the player — an enemy shot (`source==SHOT_SOURCE_ENEMY==1 < BASE`)
now makes the term false, so creatures never point-blank-damage a player. The
task fixed this line only for the energizer read and said leave the precedence
byte-for-byte, so the enemy case is untouched; flag for a future parity pass if
it matters.

Replay fixture unchanged: single player has only stat 0, so `StatAt` on the
target returns 0 (`PlayerFor(0)` — identical), and no second player exists to
point-blank, so the no-PvP guard never fires. Tests: `m8_1_test.go` covers
energized-target protection (friendly fire on), un-energized PvP damage,
no-damage with friendly fire off, self-shot, and creature-vs-energized-player.
`go test ./...` green.

## M8.2 — sweep the remaining single-player assumptions (2026-07-12)

Finished the grep sweep M8.1 started. Classified every `PlayerFor(0)`,
`Stats[0]`, and `PlayerDir` read in the sim files (`elements.go`, `game.go`,
`oop.go`) as: **(a)** terminal-wrapper / title-screen / legacy-sidebar-draw
only, **(b)** world-create / load / save (init before any join, or the vanilla
single-player file-format bridge), or **(c)** reachable from
`GameStepWithInputs` in a multi-room engine (must fix).

`PlayerDir` has **no** sim hits — it is now a per-player `PlayerState` field
(the former `Engine.PlayerDirX/Y`, see `gamevars.go:360`), so nothing to
classify there.

Key fact that makes most hits (a): `RoomManager` sets `engine.MultiRoom = true`
on every room engine (`room_manager.go:672`), and the board-swap / edge /
passage paths all gate on `MultiRoom || PlayerCount() > 1`
(`elements.go:1017,1191`, `elements.go:1282` for death) to emit a
`TransferEvent` instead of swapping the board. So `BoardChange`,
`BoardPassageTeleport`, and their `Stats[0]` writes are unreachable on the
server; they run only in the terminal `GamePlayLoop`/`GameTitleLoop` or at init.

| site | function | class | notes |
|---|---|---|---|
| `elements.go:1534` | `ResetMessageNotShownFlags` | **(c) FIXED** | reset hint flags for stat 0 only; now loops every entry in `e.Players`. `PlayerFor(0)` kept to preserve vanilla's "stat-0 state exists after world create even before a join". All flags set to the same value → map iteration order can't affect state (CLAUDE.md rule 2 safe). Refactored the 11-flag block into `PlayerState.resetMessageFlags`, shared with `ResetPlayerState`. |
| `game.go:270` | `WorldCreate` | (b) | init, before any join |
| `game.go:623` | `worldReadFrom` | (b) | maps `World.Info` → player 0 on the vanilla single-player load format; server ignores stat-0 inventory on join (M4.3a deferred note) |
| `game.go:752` | `WorldSave` | (a)/(b) | maps player 0 → `World.Info` for the terminal save format; RoomManager snapshots via its own path (M4.3a) |
| `game.go:1187` | `GameUpdateSidebar` | (a) | legacy sidebar draw into the engine `Screen`; the server builds a per-player HUD via `hudSnapshot(e, statID)` (`protocol.go:570`), never this |
| `game.go:1226` | `GameUpdateSidebar` (`SoundEnabled`) | (a) | same legacy "Be quiet/noisy" line; per-player `SoundEnabled` reaches clients through `hudSnapshot` |
| `game.go:1455` | `pointBlankEnergizerTicks` | intentional | the `PlayerFor(0)` here is the *non-player-target* fallback decided in M8.1; when the target tile is `E_PLAYER` the stat is resolved via `StatAt` |
| `game.go:1893,1931,2017,2073,2120` | `GamePlayLoop` / `GameTitleLoop` | (a) | terminal pause / end-play / title flows |
| `game.go:197-198` | `BoardChange` | (a) | single-engine board swap; multi-room emits `TransferEvent` instead (gated, above) |
| `game.go:239-245` | `BoardCreate` | (b) | init |
| `game.go:1878-1927, 2054-2055` | `GamePlayLoop` | (a) | end-play "walk the player over" + damage-flash redraw + `MoveStat(0,…)` — terminal only |

No `Stats[0]` or `PlayerFor(0)` hits in `oop.go`; OOP's counter/health writes
already route through the triggering player (M2.1/M2.4).

Fix: `ResetMessageNotShownFlags` now iterates `e.Players`. Tests
(`m8_2_test.go`): two players' flags both reset; a fresh engine still gets a
stat-0 state with flags set. Replay fixture unchanged — single player has only
stat 0, which is still reset identically. `go test ./...` green.

## M9.1 — board-change transition fade (2026-07-12)

Client-only (no protocol/sim change). Vanilla's `TransitionDrawBoardChange`
(`game.go:1484`) fills the 60x25 viewport with purple `\xdb` in `TransitionTable`
order, then reveals the new board in the same order; the browser previously cut
instantly on a `boardChange` snapshot.

New `web/src/transition.ts` (DOM-free, node-tested like `resume.ts`) owns the
pure logic: `boardCellIndices`, a Fisher–Yates `shuffle` (local `Math.random` —
CLAUDE.md rule 2 governs the sim, not the client, so the order need not match the
server's seeded table), and `cellSource(pos, step, total)` — the fill/reveal
decision. One `order` array drives both phases, so a cell filled early reveals
early (vanilla's "same order" guarantee).

Integration in `main.ts`: on `boardChange`, `startBoardTransition` captures the
outgoing viewport into `transitionOld`, then applies the incoming snapshot to
`cells` **up front** so mid-fade diffs land normally and the final frame is
always the true board (the "diffs must not be lost or painted over" requirement).
`drawScreen` renders board cells through the transition; a `requestAnimationFrame`
timer advances `step` over ~420ms. The fade is a pure render-time overlay — it
never mutates `cells`, so `applyDiff`/`applySnapshot` are untouched. Guarded on
`mode === "playing"` so a quit/editor switch mid-fade can't paint over the title.
Test: `test/transition.test.mjs` covers order coverage/permutation and the
complete-reveal invariant (every board cell ends "new", none left purple).
`go test ./...` untouched and green; web `npm test` + build green.

## M5.8 — editor parity: approved gap checklist + decisions (2026-07-12)

[ADVISOR] task; advisor tool unavailable this session, so the owner signed off
the gap list (owner had no preference on the two scope forks below; I took the
CLAUDE.md-rule-4 default on both). Grounded in `editor.go:39-817` / `EDITOR.PAS`,
diffed against the current browser editor (M5.0–M5.7).

Present & faithful (no work): arrow/numpad move, Space plot, Tab draw-toggle,
P pattern (5: Solid/Normal/Breakable/Empty/Line, verified vs ELEMENTS.PAS:1944),
C color, Enter copy/edit-stat, I board info, B switch/add board, S save+publish/
download, X flood fill, object-code editor, stat-param dialog; plus beyond-DOS
bonuses (T .BRD import/export, world upload/download, M5.7 OOP aids).

Gaps to build (highest-impact first — the approved sequence):
1. [DONE] F1/F2/F3 element category menus (`editor.go:689-776`). Server derives
   the three tables from `ElementDefs` and rides them on the entry snapshot
   (`editorElementMenus`); the `"element"` edit op ports the placement half of
   the switch, incl. E_PLAYER-moves-not-adds and the stat-seeding from
   `EditorStatSettings`. `m5_8_test.go` covers menus + item/creature/player.
2. [DONE] F4 text-entry mode (`:552-569,:777`). New `"text"` edit op
   (`editorPlaceText`): tile element = fg-colour text variant, Color byte = the
   typed char; client `handleEditorTextKey` types + advances, Backspace erases
   left, Enter/Esc leaves. Go test `TestEditorSessionPlaceTextTile`.
3. [DONE] Shift+arrow line paint (`:571`): the client places the pattern at the
   cursor before moving, so a Shift-drag lays a line (reuses the `"place"` op).
4. [DONE] Z clear board (`:645`) → board op `"clear"` (`ClearBoard`); N new
   world (`:655`) → board op `"new"` (`NewWorld`). Both reply a full snapshot,
   gated behind a client yes/no prompt. Go tests for each.
5. [PARTIAL] H editor help (`:783`) — DONE: client fetches `EDITOR.HLP` through
   the existing `/api/help` endpoint into the M4.1 window. `?` debug (`:680`) —
   DEFERRED (see decision below): a debug prompt is meaningless in an isolated,
   never-ticked editor session.
6. [DONE] Save-on-exit prompt (`:805` EditorAskSaveChanged): leaving a modified
   world offers "Save first?"; yes runs the world save and defers the exit to
   the `saveResult` (a failed save keeps the editor open), no exits at once.
7. [DONE] Sidebar closer to DOS parity (`editor.ts`): the command block is
   transcribed row-for-row from `EditorDrawSidebar` (header, H/Q, B/I, f1–f4,
   Space/Tab, P/C + colour name, swatch+pattern selector rows with markers, Mode
   line incl. blinking "Text entry"). Two rows deviate by necessity (below).
   Web test `editor.test.mjs` asserts the new rows + mode indicator.

Deliberate omissions (recorded, not built):
- `` ` `` redraw (`:599`) — terminal screen-refresh; browser repaints from
  server snapshots, so meaningless.
- `!` edit-help-file (`:787` EditorEditHelpFile) — writes arbitrary .HLP to
  server disk; security non-starter on a hosted service.
- `L` load-from-disk (`:616`) — intent already covered by `S → Upload .ZZT`;
  not adding a separate key.
- `?` debug prompt (`:680` GameDebugPrompt) — its only effects are gameplay
  cheats on a live `World.Info` (health/ammo/keys) and toggling `DebugEnabled`
  to bypass the "can't edit a saved game" gate. The editor session is isolated
  and never ticked and has no such gate, so the prompt would do nothing an
  author could observe. Left out rather than wired to a no-op path.

Sidebar deviations from cell-for-cell DOS (both are browser-capability, not
cosmetic drift): the DOS `L Load` key is folded into the `S` world menu's
`Upload .ZZT` (M5.6), and the browser adds a `T Tran


## 2026-07-13 — M5.9: sidebar F1/F2/F3 element picker (M5.8 gap-closure)

The user reported the editor still lacked parity: pressing F2 opened a scroll
(modal list) instead of turning the sidebar into a creature picker the way the
DOS editor does. Confirmed against three layers:

- Go editor (`editor.go:689-782`): faithful — F1/F2/F3 draw the category on the
  sidebar and wait for a shortcut key (ports `EDITOR.PAS:808-887`).
- Server protocol: already correct — `editorElementMenus`
  (`editor_session.go:517`) sends all three category tables on the entry
  snapshot, and `editorPlaceElement` (op `"element"`, `editor_session.go:417`)
  ports the vanilla AddStat/seed placement.
- Browser client: the gap. `openEditorElementMenu` (`main.ts`) rendered the menu
  via `openSelectList` — a modal scroll overlay you arrow through — not the
  in-sidebar picker. M5.8 shipped the menu *data* but never brought the browser
  render to sidebar parity, though M5.8's box claimed "the F1–F4 element category
  tables… every placeable element reachable by its original keystroke".

Fix is client-only (server was already faithful):
- `editor.ts`: `drawEditorSidebar` gained an optional `categoryMenu` that, when
  set, clears sidebar rows 3-20 and lists the category (shortcut badge with the
  vanilla row-parity shading `((i%2)<<6)+0x30`, name, glyph), leaving the title
  and selector/mode rows — exactly what `EDITOR.PAS:808-842` overlays. Glyph
  colour follows `EDITOR.PAS:834-837` (`menuGlyphColor`): dark-background colours
  shown on blue for legibility.
- `main.ts`: replaced the modal with `openEditorCategoryMenu` /
  `handleEditorCategoryKey` / `selectEditorMenuItem`; added `editorCategoryMenu`
  and `editorStatEditAfterPlace` state. A matching shortcut places via op
  `"element"`; Escape or any non-match closes (vanilla reads one key and the
  no-match loop falls through to a sidebar redraw). All 12 sidebar-draw sites now
  route through one `renderEditorSidebar()` so an open picker survives async
  collaborator diffs/inspects that would otherwise repaint the plain command
  block over it. `applyEditorDiff` opens the stat editor once the placed stat's
  diff arrives, mirroring `EditorEditStat` after `AddStat`.

DECISION — exact-vanilla selection behaviour (owner-chosen): selecting from the
picker places immediately at the cursor and, for stat-backed elements, opens the
stat editor — not a select-a-brush-then-paint model.

KNOWN CAVEAT (pre-existing, NOT introduced here; shared with the old modal):
CHOICE-coloured elements resolve their placement colour against `0x0F` rather
than the editor's current selected colour, because `editorElementMenus`
neutralises CHOICE colours to `0x0F` in the menu payload. Fixing it means sending
the editor's live colour as the cursor colour on placement; left for a future
task to keep M5.9 scoped to the interaction.

Verified: `npm test` (7 web suites), `npm run build`, `go build ./...`,
`go test ./...` all green; replay fixture unchanged (change is TS-only, outside
the sim).

## 2026-07-13 — LEMWILLK/LEMMER crash: orphan stat-backed draw procs

The reported `LEMMER.zzt` crash was not present as a local file, but Museum hit
`lemmerkill.zip` contains `LEMWILLK.ZZT`; board-rendering that world reproduced a
panic on board 5. Root cause: the file contains at least one stat-backed tile
(Bomb) with no matching stat record, so `ElementBombDraw` indexed
`Board.Stats[-1]` through `GetStatIdAt`. Hardened Bomb, Duplicator, and
Transporter draw procs to fall back to their default glyph when their tile has no
stat. This preserves corrupt/world-edge content instead of crashing renderers or
hosts.

Verified: `go test ./...`; `zzt-shot` renders `LEMWILLK.ZZT` board 5 and boards
0-80 without panicking.

## 2026-07-13 — Title flow: world selection must not auto-play

The browser was still entering worlds immediately after a world picker/Museum/
dream selection because `enterWorld` loaded the selected board 0 and then called
`startPlay`. That skipped the traditional ZZT title-screen pause where selecting
or loading a world shows its title board and the player must press `P` to play.
Changed `enterWorld` to only select the world and repaint board 0; `startPlay`
remains reachable solely through the title menu's explicit Play action. Added a
small `title_flow` regression so a selected world resolves to `{ startPlay:
false }`.

## 2026-07-14 — M12.17 generation prompting-quality evaluation harness

**Advisor unavailable this session** (tool returned unavailable; same situation
as the 2026-07-10 plan.go precedent), so the [ADVISOR] consultations — the task
approach and the rubric/LLM-judge design — could not happen. Decisions were
made explicitly and recorded here instead.

### What landed

* **Tier 1 — `engine/eval.go` (`EvalGeneratedZWD`)**, an exported checker
  library shared by the CI test and `zzt-eval`, measuring the COMPILED world
  (post-preprocess) so it sees what a player gets: compiles-within-limits,
  200-step headless validation (reuses `validateGeneratedZWD`), title wordmark,
  title creature/item ban, one player start, reachable `#endgame`, no orphan
  stat-backed tiles. Design decisions:
  - *Wordmark is mechanically checkable*: ZZT text elements carry the glyph in
    `tile.Color`, so "spells the world name" is a string comparison per board
    row (single-cell gaps read as word spaces). Exactly one row must spell the
    display name; at most one other text row, strictly below (the brief's
    subtitle allowance). The display name is the PLAN's world name, not the
    sanitized file name — fixtures carry it in `NAME.title.txt`.
  - *Banned title elements* are the enumerated creatures/projectiles/items
    (Bear..Star, Gem/Ammo/Torch/Energizer/Key/Bomb). Objects, scrolls, and
    passages stay legal: vanilla titles animate with objects.
  - *Endgame walk starts at board 1* — matching the server join path for
    generated worlds (`CurrentBoard` compiles to 0, WebSocket join falls back
    to 1) — over edge exits + passage `P3` targets, looking for `#ENDGAME` in
    stat OOP (case-insensitive).
  - *Fixtures use honest expectation files* (M12.7's philosophy): a fixture
    with `NAME.expect.txt` must fail EXACTLY that check set; no file means
    full pass. A new failure is a regression, a silently fixed one forces the
    expectation update, so prompt-quality movement is always visible in a
    diff. This was necessary because current prompting cannot yet produce a
    passing title (see baseline) and the repo does not ship known-red tests.
* **CP437 renderer moved into the engine** (`render_png.go`:
  `RenderBoardImage`/`WriteBoardPNG`/`RenderZWDBoardPNG`, embedded `pc_ega.png`);
  `cmd/zzt-shot` delegates and its golden PNG hash is byte-identical.
* **Tier 2 — `cmd/zzt-eval`** + `engine/eval_judge.go`: generates each premise
  with the production pipeline, runs the tier-1 gate, renders board 0 + first
  two gameplay boards to PNG, and scores against the written rubric with one
  vision-judge API call per world (JSON verdict, parse-validated; -1 = n/a for
  grounding on ungrounded runs). The rubric lives ONCE in `llmworld/EVAL.md`
  (embedded copy drift-guarded like the promptkit assets) and the judge quotes
  it verbatim. `ZZT_EVAL_JUDGE_MODEL` overrides the judge model.
* Premise set (EVAL.md): Apollo 11 (grounded-checkable), "a dream about slowly
  forgetting someone you loved" (abstract), haunted castle (pastiche); each
  run grounded and ungrounded.

### Eval-run configuration (comparison runs MUST match)

`-attempts 5` (matches `cmd/run-generation`; the server default is 3, so the
eval is slightly more forgiving than production Dream), batch size 1, model
`claude-opus-4-8`, max tokens 8192, judge = same model. One harness fix mid-run:
the service-default 2-minute HTTP client killed a grounded planner call
(server-side web_search holds the response); `zzt-eval` now passes a 10-minute
client. The affected run was retried with the fix (`baseline-retry/`).

### Baseline (llmworld/eval/baseline/report.md + baseline-retry/report.md)

| run | world | tier-1 gate | title | comp | voice | grounding |
|---|---|---|---|---|---|---|
| apollo plain | APOLLO11 | FAIL(title-wordmark) | 0 | 3 | 4 | n/a |
| apollo grounded | — | repairs exhausted: undefined legend key "." | | | | |
| dream plain | — | repairs exhausted: orphan Object | | | | |
| dream grounded (retry) | THESLOWF | FAIL(title-wordmark) | 1 | 3 | 5 | 5 |
| castle plain | CASTLERA | FAIL(title-wordmark, reachable-endgame) | 1 | 3 | 4 | n/a |
| castle grounded | CASTLEOF | FAIL(title-wordmark) | 2 | 3 | 5 | 5 |

Findings, in priority order:
1. **Title wordmarks are broken on every world** (gate + judge agree
   independently; judge reads "SSHY", "half-formed letters"). The M12
   title-screen brief improved intent but not execution — the model builds
   monumental letters that do not resolve into the name. This is THE
   prompt-quality target; the gate's `title-wordmark` expectations in
   `fixtures/gen/*.expect.txt` are the finish line.
2. **Convergence is the second problem**: 2 of 6 runs died exhausting repairs
   on errors the M12.11/M12.13 absorbers exist to prevent → filed **M12.19**
   (undefined legend key surviving preprocess; orphan stat synthesis missing
   cases, incl. a last-column Passage clue; and #endgame never enforced —
   CASTLERA is unwinnable and everything passed it).
3. **Where generation converges, quality is decent and grounding works**:
   composition a uniform 3, OOP voice 4-5 (judge singled out Guenter Wendt's
   checklist and the ten-switch #if cascade on APOLLO11), grounding accuracy
   5/5 on both grounded successes.

Fixtures committed: `fixtures/gen/{APOLLO11,CASTLERA,CASTLEOF,THESLOWF}.zwd`
(+ `.title.txt`, `.expect.txt`). Debug transcripts preserved as
`llmworld/eval/baseline*/run.log.gz`; generated `.ZZT` binaries are gitignored
(`llmworld/eval/**/*.ZZT`). Also filed **M12.18** (owner-reported: Dream
progress scroll duplicates lines). Verified: `go build/test ./...`, `go vet`,
`npm test`, `npm run build` all green; replay fixture unchanged (generation
and eval are outside the sim).

---

## 2026-07-14 — M12.20: legible title wordmarks (deterministic stamp)

The #1 M12.17 baseline finding: every successfully generated world FAILED
`title-wordmark`, and the vision judge independently scored every title 0-2
("SSHY", "MMSS OONN CCC OOO UU NN TTT"). Root cause, confirmed by reading a real
recorded title (`fixtures/gen/CASTLEOF.zwd` board 0): the model builds the name
out of **3x5 block letters made of Text tiles** (each cell a tiny letter glyph
arranged into a big letter shape), so no single grid row spells the name, plus
scattered `*` star noise. The `titleScreenBrief` fixed intent (creatures gone —
`title-no-creatures-or-items` passes everywhere) but not execution.

**Advisor unavailable** this session (tool returned unavailable); this is an
`[ADVISOR]` task, so recording the decision here in lieu of the consult.

**Decision: option 1 (deterministic wordmark stamp), not prompt-only.** A
decisive constraint settles the two candidates: `evalTitleWordmark` requires
exactly ONE horizontal Text row whose glyphs spell the name. A block-letter font
spreads a name across five rows and can therefore *never* satisfy the single-row
check — literal one-tile-per-letter Text is the only representation that passes,
and the only one the model cannot garble. So the pipeline stamps it.

`stampTitleWordmark(section, displayName)` (`generation.go`), called from
`assembleGeneratedZWD` for the `Index==0` board (the single funnel every path —
single, batch, repair, per-board validate, final — flows through, and it already
carries `plan.WorldName`). It works purely at the ZWD-text level (grid + legend
surgery) so the persisted sidecar and the hosted world stay identical:
- Centers the folded display name as one clean row of literal `Text-White`
  glyphs, placed at the vertical center of the model's own lettering (so the
  wordmark lands where the title was intended), never on a row holding the
  player or a stat.
- Allocates a FRESH legend key per distinct glyph (never reuses the model's
  keys, so it can't change what an existing cell means).
- STRIPS every other Text tile → exactly one text row remains. Non-text scenery
  (walls, borders), the single player, and decorative Object stats are
  untouched (the strip only rewrites Text-element cells).
- On any structural surprise (empty/over-wide name, no grid/legend) it returns
  the section unchanged — the compiler stays the security boundary.

`foldWordmark` (shared by the stamp and `evalTitleWordmark`) maps a display name
to the printable CP437 bytes a wordmark can store one-per-cell (em-dash→`-`,
curly quotes→ASCII, etc.); identity on ASCII, so existing fixtures/unit tests are
unaffected. This is what lets the em-dash APOLLO11 name pass.

**Kept the prompt unchanged.** Option 1 guarantees the gate regardless of what
the model draws, and prompt tuning is the option-2 path with an explicit
"measure against the harness" requirement that needs API access; changing it
blind would be unmeasured drift. The brief still asks for the wordmark; the model
composes around it and the stamp overrides.

Evidence / DoD:
- New fixture `fixtures/gen/CRIMSONC.zwd` (+ `.title.txt`, NO `.expect.txt`):
  CASTLEOF re-assembled with the M12.20 stamp — exactly what the pipeline now
  emits for that world's board 0. It passes every tier-1 check, so
  `TestEvalGateFixtures` gates it with no `title-wordmark` waiver. The original
  CASTLEOF fixture and its expectation are left as baseline history.
- `title_wordmark_test.go`: the recorded CASTLEOF FAILS `title-wordmark` before
  stamping and the whole gate PASSES after; the strip leaves exactly one text
  row; player and Object counts are preserved; the em-dash APOLLO11 name passes
  via the fold; structural surprises are no-ops.
- **Live comparison report is owner-run** (spends API — `zzt-eval -attempts 5`,
  same model as the 2026-07-14 baseline). Cannot run it here without a key; the
  stamp guarantees `title-wordmark` by construction at assembly, so any fresh
  generation passes it regardless of model output — the deterministic tests
  above stand in for CI. When run, drop the report beside
  `llmworld/eval/baseline/` and link it here.
- `go build ./... && go vet ./... && go test ./...` green; replay fixture
  unchanged (generation/eval are outside the sim).

## 2026-07-14 — M12.22: retry a failed board (owner request)

When a board exhausted its attempts, `generate` discarded the plan and every
board already painted; the player paid for a full fresh run. Now board-scoped
failures return `*GenerationBoardError` carrying a `generationResume` (premise,
plan, name, painted sections, attempt counters) plus the generation-order
resume index, and `RetryBoard` re-enters `paintAndFinish` from the failed
board. Decisions:

- **Any board-scoped failure is retryable, not just exhaustion.** LLM transport
  errors surface through the same `paintBoard` path with the same intact state;
  refusing them would force a full re-run for a network blip. Plan-stage and
  assembled-compile failures stay non-retryable — no single board owns them.
- **Retries skip the per-client rate limit but still take a concurrency slot.**
  The player is continuing one admitted generation, not starting another; the
  semaphore still prevents retry dogpiles.
- **Batch mode retries the whole failed batch** (attempt counters of every
  board in it reset; the display name is the joined list) — the batch is the
  unit of failure and of the resume index.
- **A failure inside the cross-board repair loop resumes with
  startIdx = len(GenerationOrder)**: painting no-ops and the assembled-world
  loop re-runs from round 0, recomputing problems against the repaired
  sections.
- **Double retry cannot race the shared resume state**: the async job flips
  back to "running" under `generationMu` before the retry goroutine starts,
  and `POST /api/generate {"retry": id}` refuses (409) any job that is not
  failed-with-resume. Unknown ids are 404.

Client: `runDreamGeneration` rejects with `DreamFailure{jobId, retryable,
failedBoard}`; the failure path asks `Dream failed. Repaint "<board>"? ` and on
yes resumes polling the same job. `go test -race -run TestM1222` and the full
suites are green; replay fixture unchanged (generation is outside the sim).

## 2026-07-14 — M16.16 audit: chat/Museum certification gap

M16.16 cannot be marked complete yet. The existing tests cover individual
auth, chat-history, and Museum happy-path pieces, but the audit found two
implementation-level violations of its stated service contract:

1. `websocket_server.go` accepts any non-whitespace chat text and immediately
   persists/broadcasts it. It has no CP437/control filtering, maximum length,
   or per-player rate state. This also means a direct WebSocket client can
   bypass the browser's 30-character entry UI.
2. `museum.go` writes the downloaded archive to `.museum-cache` in
   `downloadZip`, before `Play` calls `zztFilesFromZip`, selects a `.ZZT`, or
   validates the selected bytes. A corrupt archive or traversal-bearing ZIP
   therefore returns an error after mutating the cache, contrary to M16.16's
   "security refusal paths prove no state/file mutation" criterion.

Added M16.16a with exact admission/cache-commit boundaries and required
hermetic regression coverage. This is deliberately a gap task rather than a
silent golden/test adjustment. The browser portion of M16.16 also remains
dependent on M16.9's real-browser harness; the current checkout has no pinned
browser runner.

Evidence: `cd engine && go test ./...` passed on 2026-07-14; no replay fixture
was changed.

## 2026-07-15 — M17.1/M17.4 owner reports: stale build + scroll hyperlink root cause

Owner re-tested the live browser and reported (a) the M17.1 launch popup STILL
uncentered/overflowing, and (b) scroll hyperlinks doing nothing in "multiple
worlds with hyperlinks" (not TOWN).

**(a) M17.1 was never actually broken in code — it was a STALE BUILD.** The fix
(commit 3a3d5b2) changed only `web/src`. The server serves the *built* bundle
(`zzt-server -web web/dist`), and `dist/` is gitignored, so it stayed stale
until `npm run build` ran. Proven: rendering the launch popup through a minified
production build centers it correctly (box cols 12–67, prompt cols 15–64 inside
the borders). Fix: rebuilt `dist/`; added a startup staleness guard
(`warnIfClientStale` in cmd/zzt-server) that logs a `STALE build` warning when
`web/dist` is older than `web/src`, plus a README note. This trap will recur for
any web change committed without a rebuild.

**(b) M17.4 root cause: E_SCROLL elements are removed before their async reply.**
`ElementScrollTouch` (elements.go:970) runs `OopExecute` then `RemoveStat(statId)`
*immediately*. In the M1.3 de-modal design, OopExecute only EMITS a ScrollEvent
and returns; the hyperlink reply arrives on a LATER step via PendingScrollReply →
OopSend. By then the scroll stat is gone (and stats renumbered), so the reply's
statId fails the drain guard `StatId <= Board.StatCount` and is dropped — the
`:label` never runs. Persistent objects (the vendor) survive OopExecute, so
object hyperlinks work; only consumed SCROLL elements break. Vanilla works because
OopExecute is MODAL there — it sends the hyperlink label to the scroll BEFORE
`RemoveStat`. Reproduced headlessly: a scroll whose reward is gated behind `!go`
never grants it (reply dropped, StatId=1 > StatCount=0); the same shape as an
object grants it. This is an [ADVISOR]-class change (touches M1.3): the faithful
fix is to DEFER the scroll's removal until its reply is drained (empty on dismiss,
or with a label), mirroring vanilla's post-modal RemoveStat. Risks to weigh:
replay (does any fixture touch a windowed scroll?), statId renumbering between
touch and reply (already a latent de-modal fragility for objects), and the
multiplayer case of two scrolls open on one board.

**Fix landed (engine-only, replay unchanged).** `ElementScrollTouch` now DEFERS
`RemoveStat` when the scroll opened a window (`scrollWindowEmittedFor` scans the
events OopExecute just appended). The scroll-reply drain in `GameStepWithInputs`
runs the selected `:label` — `OopSend` to position the OOP, then an inline
`OopExecute`, because a Scroll never runs its OOP on tick the way an Object does
(`ElementScrollTick` only shimmers the color) — then consumes the scroll, located
by POSITION (`GetStatIdAt`) so renumbering between touch and reply cannot delete
the wrong stat. This reproduces vanilla's modal ordering (run `:label`, then
RemoveStat). Object hyperlinks were never broken and are untouched (for an object
reply `scrollX` stays -1). M17.4 is not `[ADVISOR]`-marked, replay stayed green
(the fixture never touches a windowed scroll), the object/vendor test still
passes, `go test -race` on the reply paths is clean, and the change is faithful to
the Pascal — so it landed without an advisor (which was unavailable) or a
`DEVIATION:`. Two known edge cases carry the SAME latent de-modal fragility that
object replies already have, documented not fixed: a scroll whose `:label` opens
a *further* window (the new event targets a stat about to be consumed), and two
players reading scrolls on one board while other stats churn (a stale reply
statId). Both are rare; neither regressed an existing test.

---

## 2026-07-15 — M16.0: parity contract, manifest, and validator

Built the M16 feature-parity framework: `PARITY.md` (contract), `fixtures/
parity/manifest.json` (339 rows + 14 seeded deviations), and the validator +
scaffold `engine/parity_manifest_test.go`. Decisions and caveats worth logging:

**Advisor unavailable.** M16.0 is `[ADVISOR]` and its DoD gates on "the advisor
and owner approve the contract/deviation list." The `advisor` tool errored as
unavailable this session, so the owner is the sole approval gate; recorded here
so M16.2 (first oracle fixtures) knows the advisor half of the gate is still
outstanding and should be sought when the tool returns.

**Owner scope decisions (2026-07-15), the two the spec demanded:**
- *Mobile "playable on phones"* → **gap task M16.18a**, not a narrowed claim.
  M15.1 shipped text entry only; touch movement/shoot/torch/pause is unbuilt.
  M16.18a builds an on-screen control pad emitting the existing `PlayerInput`
  keymask (no new sim input vocabulary); it blocks M16.20. Manifest row
  `mode.mobile-touchplay` is `gap` until it lands.
- *M17 live fixes* → **in scope** as `task` rows (name-popup, world-picker,
  audio, scroll-hyperlink), so a shipped fix carries a certified regression row.

**Manifest design — derive, don't hand-list.** Five of nine dimensions are
mechanically derived from code at test time (checked `[x]` tasks; `ElementDefs`
procs by reflection over the E_ constant set; `OopWord` literals scanned and
cross-checked against a curated classification; `MessageType*`/`ProtocolEvent`
types; `mux.HandleFunc` routes + `/ws`). The remaining four (oop-structural,
input, browser-mode, service) are curated Go slice literals whose consistency
the validator still checks. Consequence the sweeps rely on: adding an element,
OOP word, protocol type, route, or checking a task box reddens `go test` until a
row exists — the "a newly added command cannot be unlisted" guarantee M16.6
asks for, generalized. The scaffold (`PARITY_SCAFFOLD=1`) regenerates the
manifest and *merges in* later sweeps' status/test/fixture edits so flipping a
row to `pass` is not clobbered on regeneration.

**Element set = E_ constants, not "has a custom proc".** First cut used a
reflection "in use" heuristic and silently dropped the 7 text tiles (E_TEXT_*,
drawn by the special case in `game.go` `TileToColorAndChar`, no DrawProc) and
the 2 blink rays (registered but unnamed) — exactly the surfaces M16.3/M16.4
name. Fixed to enumerate every defined E_ constant (index 46, the reserved
black-text slot, has no constant and is correctly omitted): 53 element rows.

**Caching caveat for the gate.** `TestParityManifest` reads the manifest and
several source files via `os.ReadFile`, which Go's test cache does not track, so
a *manifest-only* edit with a plain `go test` can return a cached pass. In the
normal commit workflow any package `.go` change (including the sweep that edits a
row) busts the cache; standalone manifest audits must use `-count=1`. M16.1
(runnable immutable evidence command) should wire `-count=1`. Fail-closed was
verified with `-count=1`: a dropped row, an orphan mechanical row, and a stale
Go-test reference are each caught.

Generation and the manifest are entirely outside the simulation; the replay
fixture is unchanged. No `DEVIATION:` — nothing in the sim moved.

---

## M16.1 — Runnable, immutable parity evidence (2026-07-15)

**The command.** `make parity` → `cmd/zzt-parity` is the single certification
entry point. `main.go` runs the seven clean gates (`go build`, `go vet`,
`go test -count=1`, `go test -race -count=1`, `npm ci`, `npm test`,
`npm run build`) in order, streaming their output, then `report.go` renders a
deterministic JSON+Markdown report keyed by `fixtures/parity/manifest.json`.
`-count=1` on the Go gates is deliberate: the manifest validator reads files the
test cache does not track, so a cached pass could otherwise mask a manifest edit
(M16.0 note). The report is a *pure* function of `(manifest, gateResults)` —
no timestamps, durations, or map/slice-order leak in — so the artifact is
byte-stable and diffable; `report_test.go` asserts that plus the certification
verdict logic.

**Why the report is a gitignored artifact, not committed.** DoD requires
`git status --short` empty after a run and "CI stores the report as an
artifact." Committing a report whose content depends on gate results would fight
determinism and dirty the tree. So `report.{json,md}` are gitignored; CI's new
`parity` job uploads them and asserts a clean tree. Certification is proven by
the committed *manifest*, never by the rendered report and never by line
coverage (the report computes none, by design — PARITY.md §2).

**Exit policy (matters for CI staying green pre-M16.20).** A failed or skipped
clean gate is always a hard failure. "Not yet certified" (338 rows still
`unverified`, 1 `gap`) is the expected state until M16.20 and is a failure only
under `-require-certified`. So the `parity` CI job is green today as long as the
gates pass, and M16.20 flips it strict.

**Fail-closed hygiene.** Required parity fixtures now fail when missing instead
of silently skipping or auto-writing:
- `town.replay.json` and `town_board1.zwd` no longer write a fresh baseline on
  absence (that would launder whatever the engine currently does past the safety
  net — CLAUDE.md rule 3). They `t.Fatal`; regeneration is the explicit
  maintainer command `ZZT_PARITY_REGEN=1` (mirrors `PARITY_SCAFFOLD`).
- `t.Skip("...unavailable")` over any committed fixture (TOWN, `fixtures/gen/*`,
  `llmworld/examples`, EVAL.md) → hard failure via the shared `requireFixture`.
- `zzt-shot`, `zzt-validate`, and the TOWN ZWD round-trip read the committed
  `fixtures/TOWN.ZZT` (sha `994ebade…`, byte-identical to the engine-dir copy)
  rather than an untracked engine-dir world, so they run in a clean clone.

**Separated, not deleted, the untracked-world/generator canaries.** Tests that
depend on untracked engine-dir worlds or write committed corpus are maintainer
tools, not parity assertions, so they moved behind `//go:build canary`
(`make parity-canaries`) and out of the certified `go test ./...`:
`gen_fixture` (regenerates `town_board1.zwd`), `gen_llmworld` (writes the
`llmworld/examples` corpus), and the CAVES/CITY round-trips (new
`worlds_canary_test.go`). `gen_generated` keeps its required compile+validate of
the committed ZWD worlds but its `.ZZT` world-picker side-effect write is now
regen-gated. The committed corpus is still verified in the required path by
`TestLLMWorldExamplesCompile`.

**Network.** Already hermetic — `auth`/`museum`/`generation`/`eval-judge` tests
use `httptest` + `fakeClaude`/`fakeIDTokenVerifier`, no live sockets. Left as-is;
they are the "required tests use hermetic fakes" the spec asks for. Live OAuth/
Museum/LLM canaries remain out of scope (none run in the required path).

Only one skip survives the required path: `TestParityManifestScaffold`, the
explicit `PARITY_SCAFFOLD=1` regen guard. `make parity` verified locally: all
seven gates green, exit 0, `git status --short` empty (report files ignored).
The `[ADVISOR]`-style consult could not run — the advisor tool was unavailable
this session, as it was at M16.0; the owner is the approval gate. No simulation
code moved; the replay fixture is unchanged; no `DEVIATION:`.

---

## 2026-07-18 — M17.7 sound: broken build was masking the M17.3 fix + regression net

**Owner field confirmation: sound works now.** Asked the owner to pin the "not
working properly" symptom before touching anything; answer was "Sound is working
now. This has already been fixed." That satisfies the DoD's "verify audibly in an
actual browser." This entry records the root cause we landed a guard for and the
cleanup around it.

**Root cause of the field breakage: a half-committed method left HEAD unbuildable,
so the M17.3 audio-unlock fix could never be deployed.** Commit `78413d4` (labeled
an M17.6 follow-up) shipped `web/src/main.ts` code that calls
`zztSound.diagnostics()` and `warnIfSoundUnplayable()`, but the `diagnostics()`
method itself was never committed to `web/src/sound.ts`. HEAD therefore fails
`tsc`: `main.ts(2098,22): Property 'diagnostics' does not exist on type
'ZztSound'` (verified by building HEAD's `sound.ts` against the working `main.ts`).
The server serves the *gitignored* built bundle (`zzt-server -web web/dist`), so a
failed `npm run build` leaves `dist/` stale — the identical trap that made M17.1
look unfixed (2026-07-15 note). Net effect: M17.3's `unlock()`-on-gesture fix sat
in source but the browser kept running an older bundle. This is a build/deploy
gap, not a synth-logic bug.

**The client synth was verified faithful to vanilla — no note/timing bug.**
Compared `web/src/sound.ts` against the authoritative `SOUNDS.PAS`: the `queue()`
priority guard (`not playing OR ((p>=cur AND cur<>-1) OR p=-1)`), the buffer
tail-swap on a lower-priority interrupt, the tone/drum/rest dispatch, the
`SoundInitFreqTable` frequency table, and the `duration * TICK_SEC` (1/18.2065)
note length all match the Pascal `SoundQueue`/`SoundTimerHandler` with the default
`SoundDurationMultiplier = 1`. So the "wrong pitch/tempo/cutoff" family was ruled
out by construction, which is why the field fix was purely getting the correct
bundle to the browser.

**What landed (client-only; sim untouched, replay fixture unchanged):**
- `sound.ts` gains `diagnostics()` — unbreaks the build (the committed `main.ts`
  already calls it) and reports live audio state (`contextState`, `enabled`,
  `isPlaying`, `schedulerRunning`) from the console via the `window.zztSound`
  handle for pinning any future silence. `contextState === "running"` means the
  AudioContext unlocked; anything else means a note would be swallowed.
- `test/sound.test.mjs` (new; wired into `npm test`) is the regression net M17.3
  never had: it bundles the real `ZztSound` under Node against a recording mock
  Web Audio graph and asserts a queued note actually gates the oscillator on
  (positive gain ramp + positive frequency), that `unlock()` resumes a suspended
  context even while muted (title-screen mute must not swallow the first in-game
  note), that a disabled synth stays silent, and that `soundNotesFromProtocol`
  keeps the numeric-array wire contract (a string would `&0xff` to NaN→0 and
  silence everything).
- The root-cause guard is the build itself: `npm run build` (run by CI and
  `make parity`) now fails loudly on any future half-committed method, and the
  M17.1 `warnIfClientStale` startup check already flags a stale `dist/`. `dist/`
  rebuilt fresh this session.

**Intentional multiplayer parity departure, restated (owner's framing 2026-07-18:
depart from vanilla only to preserve the new features).** Sound's one departure is
per-player attribution (M7.4): a player's own pickups/shots/damage reach only that
client; an object's `#play` stays room-wide (`StatId = -1`). Vanilla is
single-player and has no such split, so this is by-design, not a bug — documented,
not "fixed" back to vanilla.

**Tree hygiene this session (not part of M17.7, surfaced to the owner):**
- Deleted `engine/oracle_scratch_test.go` — a *foreign-session* M16.2 oracle
  bring-up harness (self-labeled "Deleted before commit") that read another
  session's scratchpad and was failing an oracle transfer-parity check, reddening
  `go test ./...`. It captured a real M16.2 finding (a transfer-cell mismatch,
  oracle `ch=02 at=1f` vs go `ch=20 at=0f`) but belongs to M16.2's own harness,
  not the tree; removed so the safety net is green.
- Left the unrelated `world_metadata.go`/`world_metadata_test.go` changes (a
  "curated Museum catalog only" world-picker refactor; green on their own tests)
  uncommitted and out of the M17.7 commit — separate WIP, not this task.

## 2026-07-18 — M16.2: the independent vanilla oracle

Landed the oracle seam: real **ZZT.EXE v3.2** (Museum of ZZT `zzt.zip`,
sha256-pinned; identical `ZZT.EXE` bytes to the prior bring-up) under Zeta
`ad85bcf8`, driven by the committed headless frontend `oracle/frontend_oracle.c`
(virtual clock, `.scn` scenario scripts, VRAM checkpoints, speaker log).
`make oracle-regen` regenerates `fixtures/oracle/`; tests compare offline via
`engine/oracle_parity_test.go`. A from-scratch pipeline run (fresh Zeta clone,
Museum zip, new work dir) reproduced the prior session's capture bytes exactly.
The foreign-session scratchpad harness deleted during M17.7 hygiene was
recovered from `/private/tmp` and is now the committed, extended version of
itself (80-column capture with sidebar, speaker events, scroll scenario).

**Advisor gate.** The advisor tool errored as unavailable again (third session:
M16.0, M16.1, now M16.2). The owner approved starting M16.2 this session and is
the approval gate, per the M16.0 precedent. The still-outstanding advisor half
of the M16.0 contract approval carries forward.

**What the seam caught immediately — the reason M16.2 exists:**

- **Vanilla's real cycle cadence.** `TickTimeDuration = TickSpeed*2` is in
  *hundredths of a second* (GAME.PAS:1511 with SoundHasTimeElapsed), so the
  default speed runs one game cycle per ~2 PIT ticks (~110ms), not 8. Pinned
  empirically: the gem-hint message color `9+(P2 mod 7)` decrements P2 by 4 per
  8 PIT ticks in the real ZZT.EXE. The adapter maps each oracle `move`
  (keypress + 8 PIT ticks) to 4 GameSteps: input, then 3 idle.
- **`SoundInitFreqTable` C-note truncation (fixed in sounds.go).** float64
  `Exp(octave*ln2)` lands just under the exact power of two, so `Trunc` gave
  511/255 Hz where Turbo Pascal's 48-bit real — and the oracle — plays 512/256.
  Fixed by computing the octave base as the exact power of two; all other notes
  already matched. Presentation-only (the table is not hashed; the wire carries
  note bytes): replay fixture unchanged. The TS client's `sound.ts` port has
  the same 1 Hz artifact — left for the M16.6/M17 audio surface, noted here so
  M17.7's "synth verified faithful" claim is corrected on this one detail.
- **The transfer mismatch that reddened M17.7's tree is the pause-blink
  normalization, not a board defect.** After `BoardPassageTeleport` both
  engines hold identical board state (arrival tile erased to empty, stat at the
  arrival square, player paused); vanilla's interactive loop *draws* the paused
  player blinking `02`/`1F` while the fork emits `PauseEvent` and leaves
  drawing to the client. Normalized at exactly the paused player's square
  (PARITY.md §7 `oracle-pause-blink`).

**Findings recorded for later sweeps (not fixed here — rule 4):**

- **Walk click gap (M16.6).** Vanilla plays `Sound(110)` directly per step onto
  a walkable tile (ELEMENTS.PAS, ported at elements.go:1458) but the port's
  `Sound()`/`NoSound()` are TODO stubs (lib.go:124), so no client ever hears
  vanilla's walk click. Excluded from oracle sound comparison as a *gap*, not a
  deviation.
- **Headless post-passage unpause is unreachable (M16.3).** The step loop
  dispatches by tile element, and the paused-player/unpause branch sits inside
  `tile == E_PLAYER` (game.go:1741-1758). After a passage teleport the player
  stat stands on an erased (empty) tile, so a headless single-engine player can
  never unpause — vanilla recovers because its pause branch is not
  tile-dispatched and stamps the player tile on unpause (same failure class as
  ReenterWhenZapped, NOTES 2026-07-09). Live multiplayer is unaffected
  (RoomManager's transferPlayer/roomSpawn path stamps tiles); the M16.3
  passage sweep must cover and fix the single-engine path.

**Adapter design notes.** The oracle scenarios are RNG-free on the compared
path by design; VRAM is the exposed surface (tiles/stats/RandSeed compared via
their screen projection — memory-segment capture is a possible later
extension). The adapter runs on a fresh `Engine` swapped into the package
global for the run: the first version polluted shared `E` (PlayerState hint
flags) and flipped TOWN replay hashes when tests ran in one process. The
manifest scaffold regen also added the `task.M17.7` row that M17.7's commit
forgot (same miss as M17.6's, caught by TestParityManifest).

## 2026-07-18 — M16.3: vanilla player/inventory/terrain sweep

Seven new micro-worlds (ORCLMOVE/ITEM/DARK/NRG/SHOT/PASS/TIME, authored as
`.zwd`, compiled once, byte-locked to their sources by
`TestOracleWorldsMatchZWDSources`) and seven scenarios drive the real ZZT.EXE
and the engine through movement, pushing, walls/terrain, text tiles, items,
keys/doors, darkness/torches, energizer, shooting/breakable/ricochet/max-shots,
passages, board edges, and per-board time limits. All 27 assigned element rows
are `pass`; every checkpoint (cells, sidebar counters incl. `Time:`, sounds)
matches the oracle. The advisor tool was again unavailable this session (as at
M16.0–M16.2); M16.3 carries no `[ADVISOR]` tag, so work proceeded under the
executor protocol.

**Defects found by the sweep and fixed here** (all invisible to the TOWN
replay, whose fixture is unchanged):

- **Headless unpause was unfaithful three ways** (the M16.2 finding, now
  fixed): the step loop's pause branch is a per-player port of vanilla's
  (GAME.PAS:1519-1567): the touch fires while paused, the player unpauses only
  when the move succeeds (a blocked move keeps you paused), a stat standing on
  a non-player tile (post-passage) stamp-moves instead of MoveStat — so a
  single-engine player can now unpause after a passage teleport — and a
  successful unpause falls through to a no-input PlayerTick, because vanilla's
  stale cycle gate runs a full stat cycle in the same timer window (pinned by
  ORCLTIME's message flash phase). CurrentTick still is not re-randomized and
  the room keeps ticking for other players (M3.11 deviation).
- **Per-board time limits ran ~9x fast** headless: `SoundHasTimeElapsed` is
  stubbed `true` (NOTES 2026-07-09), so `BoardTimeSec` rose every player tick.
  New `Engine.BoardTimeElapsed` ports the SOUNDS.PAS *system-time* branch
  (`UseSystemTimeForElapsed` — the path taken on any machine with a working
  BIOS clock, and under Zeta) over a virtual PIT counter the step loop
  advances: one board second per 19 ticks (~9.5 cycles) at speed 4, first
  second immediately on entering a timed board (stale-counter quirk), reset on
  damage. ORCLTIME pins boundaries, the warning message, damage, and the
  sidebar Time counter.
- **`Board.Info.MaxShots` never limited players**: PlayerTick counted player
  bullets by `P1 == 0`, but the fork stores `statId+SHOT_SOURCE_PLAYER_BASE`.
  Now counts the acting player's own bullets — vanilla-exact for one player,
  per-player budgets in a shared room.

**Findings for later sweeps (recorded, not fixed — rule 4):**

- **M16.5 (bullets):** vanilla self-shot damage (a ricochet returning the
  player's own bullet) is suppressed by the friendly-fire policy
  (elements.go `ownerStatId` check). Owner-approved deviation
  `friendly-fire-policy`; the bullet row must pin it with a focused test. The
  ORCLSHOT scenario steps the shooter aside so the V-comparison stays clean.
- **M16.10 (play inputs):** pressing into a wall while paused now stays paused
  (vanilla); the `input.play-pause` row should cover both it and the
  touch-while-paused pickup quirk.
- **Sound representation:** the oracle comparison is ISR-preemption-aware with
  drum-onset wildcards (drum tables 4/5/8/9 are `Random()`-seeded at ZZT
  boot); documented in PARITY.md §7.

**Oracle infra additions:** multi-world scenarios (`world` directive read by
regen.sh; per-scenario ZZT.CFG), `shoot DIR` (held Shift around the arrow —
vanilla samples the modifier when InputUpdate consumes the key), title-state
adapter (engine boots in monitor state and `capture title` compares the title
board against the real boot screen, certifying elem.monitor together with
TestMonitorTickExitKeys), and the Time sidebar counter comparison. M16.2's
main/scroll captures regenerate byte-identically under the extended harness.

## 2026-07-20 — M17.8 escalation: TestTouchRaceBakery blocked by regenerated fixture

Deploying M17.8's dev environment surfaced a commit blocker in the
`feature/structured-world-generation` worktree. `go test ./...` is red:

    --- FAIL: TestTouchRaceBakery
        touch_race_test.go:46: Townguide stat not found

Not pre-existing and not caused by M17.8. The test passes at the clean commit
6e7dc60 with the same BAKERY.ZZT; it fails only in the working tree, and
`touch_race_test.go` itself is unmodified. Bisecting the WIP by file showed
`generation.go` and `world_metadata.go` are both innocent (pass individually
and together).

Root cause: the uncommitted `BAKERY.zwd` is a wholesale regeneration, not an
edit. Board 1 changed from "Title Screen" to "Warm Bread Plaza", the grid is
entirely different, and `@townguide` is absent (`git show HEAD:engine/BAKERY.zwd`
has 1 occurrence; the working copy has 0). `touch_race_test.go:35-46` compiles
BAKERY.zwd and scans board 1 for an E_OBJECT at hardcoded (13,18) — the
townguide — so the assertion can no longer hold.

The coupling is the real defect: a touch-race invariant test rides on a
generated artifact that the generator is expected to rewrite. Retargeting by
label does not help (no guide object exists in the new world).

Resolved (owner authorised 2026-07-20) by pinning a stable fixture:
`engine/testdata/touch_scroll.zwd` is the HEAD copy of BAKERY.zwd, and
touch_race_test.go now reads it instead of engine/BAKERY.zwd. The test's own
header already recorded that the BAKERY filename is "historical" and that it
merely checks the unlocked-object touch -> scroll path, so nothing about the
invariant is BAKERY-specific. No assertion was weakened: the fixture is the
exact world the (13,18) board-1 coordinates were written against, and the
generated BAKERY.zwd stays free to change. `go test ./...` green after the
change. The test was not deleted or skipped, so no DEVIATION applies.

M17.8 itself is
functionally complete: dev.zztmmo.com serves commit 6e7dc60 over HTTPS with a
Let's Encrypt cert, /status reports the SHA, /api/worlds returns 65 worlds
(byte-identical to prod's catalog), and wss upgrades return 101 for TOWN,
BAKERY, and uncatalogued worlds; production stayed at 200 throughout. Instance
i-06149a1a52a126f0c, EIP 54.210.138.45, SG sg-08859294bf38ac4c3; provision,
deploy, rollback and teardown are documented in AWS.md (gitignored). The
remaining M17.8 DoD item is the owner's real-browser check, deliberately not
self-certified per the M17.3/M17.7 lesson. M17.8's TASKS.md box is therefore
still unchecked.
