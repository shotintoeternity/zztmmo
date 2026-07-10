# NOTES — escalations and decisions log (append-only)

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
* No `A` About screen on the browser title → M9.2.
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
