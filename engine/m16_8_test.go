package zztgo

// M16.8 — engine -> room -> protocol equivalence.
//
// The M16.2 oracle scenarios (fixtures/oracle/*.scn, replayed by
// oracle_parity_test.go) already prove a bare Engine matches real ZZT.EXE.
// This file replays the SAME scenario scripts a second time, side by side,
// through a RoomManager-wrapped Engine (and, for a representative subset, a
// real WebSocket client) and asserts the room/protocol projection reconstructs
// bit-identical state — board cells, HUD, player position, sounds, scrolls,
// prompts, and StateHash — at every checkpoint the oracle scenarios already
// declare. No new fixtures are needed: this compares path to path, not to the
// oracle capture.
//
// Two facts about the existing paths this file works around rather than
// changes (both are deliberate, documented elsewhere):
//   - RoomManager.JoinPlayerWithID always calls ResetPlayerState, discarding
//     whatever counters a world file's stat 0 held (M4.3a: "a joiner arrives
//     fresh, as they already do on any running world"). A fair three-path
//     comparison needs all three paths to START from the same counters, so
//     this harness captures the direct Engine's PlayerState at the scenario's
//     `play` transition and seeds the room/WS players with
//     ApplyPlayerState/PlayerState rather than trusting the reset defaults.
//   - A title/"boot" span has no room or protocol counterpart: multiplayer has
//     no monitor loop (WorldInstance.Title is a separate, unrelated
//     simulation), so only checkpoints captured after `play` are compared
//     across all three paths; title checkpoints remain engine-only (already
//     proven by oracle_parity_test.go).
//
// A third fact this harness had to design around, not work around: since
// several scenarios (main, pass, and most of the M16.4-M16.6 device/creature/
// OOP worlds, which walk from a hub board into a dedicated feature board)
// cross a real board edge, and RoomManager always runs with MultiRoom=true (so
// a passage/board-edge touch transfers the player to a DIFFERENT Engine,
// unlike the single-player direct-swap path), two things follow:
//   - The room side's diff-only board reconstruction must resync from a full
//     Snapshot the moment PlayerLocation reports a new board — precisely
//     mirroring what WorldInstance.Tick does with a BoardChangeMessage.
//   - Each RoomManager room is a FRESH Engine with its OWN RandSeed starting
//     at 0 (room_manager.go's ensureRoom), never inheriting whatever RNG state
//     the player's PREVIOUS room had accumulated. A bare Engine, single
//     continuous simulation, carries ONE evolving RandSeed across every board
//     it ever visits. So the instant a scenario crosses a board, StateHash and
//     any RNG-dependent rendering (monster movement draws, device animation
//     phase) are NOT expected to agree between the two paths — this is a
//     real, load-bearing architectural property (one independently-seeded
//     simulation per board), not a bug. This harness therefore compares full
//     checkpoints (board+HUD+hash+events) only up to and including the FIRST
//     post-transfer checkpoint's position/HUD (deterministic: PlayerState
//     transfers by value copy) and skips hash/board-cell comparison from
//     there once a scenario has crossed a board — see runM168RoomPass's
//     rngTainted.
//
// A fourth, unrelated fact this harness discovered the hard way: it originally
// stepped the direct Engine and the RoomManager engine INTERLEAVED, tick by
// tick, in one loop. That corrupted results, because
// ElementDefs[E_PLAYER].Character (elements.go ElementPlayerTick) is a
// package-level global mutated by whichever Engine ticks a player with
// EnergizerTicks>0 (or restores the steady state) — not per-Engine state, in
// spite of gamevars.go documenting ElementDefs as "immutable after init" and
// M1.2 promising interleaved Engines "no cross-talk". Filed as M16.8a
// (TASKS.md); NOT fixed here (out of this task's remit). This harness instead
// runs the direct Engine to completion FIRST, recording every checkpoint, and
// only THEN drives RoomManager to completion — no two Engines ever tick in
// the same process at overlapping times, so the shared global is never
// contended between the two passes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m168Board is a reconstructed board (columns 0..59, rows 0..24 — the
// protocol's netScreenWidth for a MultiRoom engine), rebuilt purely from
// ScreenCell writes: a SnapshotMessage.Screen full dump, or a sequence of
// DiffMessage.Cells applied over one.
type m168Board [BOARD_WIDTH][BOARD_HEIGHT]struct{ Ch, Color byte }

func (b *m168Board) apply(cells []ScreenCell) {
	for _, c := range cells {
		if c.X < 0 || int(c.X) >= BOARD_WIDTH || c.Y < 0 || int(c.Y) >= BOARD_HEIGHT {
			continue
		}
		b[c.X][c.Y] = struct{ Ch, Color byte }{c.Ch, c.Color}
	}
}

func m168BoardFromEngine(e *Engine) m168Board {
	var b m168Board
	for x := int16(0); x < BOARD_WIDTH; x++ {
		for y := int16(0); y < BOARD_HEIGHT; y++ {
			cell := e.Screen[x][y]
			b[x][y] = struct{ Ch, Color byte }{cell.Ch, cell.Color}
		}
	}
	return b
}

// diff returns the first mismatching coordinate, in row-major order, so the
// failure message pins exactly one cell — the same diagnostic philosophy as
// oracle_parity_test.go's compareCheckpoint.
func m168BoardDiff(want, got m168Board) (x, y int16, mismatch bool) {
	return m168BoardDiffExcept(want, got, -1, -1)
}

// m168BoardDiffExcept is m168BoardDiff with one square exempted — the
// pause-blink normalization below is the only caller that needs it.
func m168BoardDiffExcept(want, got m168Board, exceptX, exceptY int16) (x, y int16, mismatch bool) {
	for yy := int16(0); yy < BOARD_HEIGHT; yy++ {
		for xx := int16(0); xx < BOARD_WIDTH; xx++ {
			if xx == exceptX && yy == exceptY {
				continue
			}
			if want[xx][yy] != got[xx][yy] {
				return xx, yy, true
			}
		}
	}
	return 0, 0, false
}

// m168Counters is the player-visible state that must agree across all three
// paths at a checkpoint: HUD counters plus board position.
type m168Counters struct {
	Health, Ammo, Gems, Torches, TorchTicks, EnergizerTicks, Score int16
	Keys                                                           [7]bool
	X, Y                                                           int16
}

func m168CountersFromEngine(e *Engine, statId int16) m168Counters {
	p := e.PlayerFor(statId)
	stat := e.Board.Stats[statId]
	return m168Counters{
		Health: p.Health, Ammo: p.Ammo, Gems: p.Gems, Torches: p.Torches,
		TorchTicks: p.TorchTicks, EnergizerTicks: p.EnergizerTicks, Score: p.Score,
		Keys: p.Keys, X: int16(stat.X), Y: int16(stat.Y),
	}
}

func m168CountersFromSnapshot(hud HUDSnapshot, you PlayerSnapshot) m168Counters {
	return m168Counters{
		Health: hud.Health, Ammo: hud.Ammo, Gems: hud.Gems, Torches: hud.Torches,
		TorchTicks: hud.TorchTicks, EnergizerTicks: hud.EnergizerTicks, Score: hud.Score,
		Keys: hud.Keys, X: you.X, Y: you.Y,
	}
}

// m168EventKeys returns a sorted, canonical string per event so two event
// streams can be compared as multisets. Order between a room-wide event
// (roomEvents) and a per-player one (pendingPlayerEvents, drained separately)
// is an artifact of which internal queue RoomManager used, not something the
// direct Engine's single linear Events slice can be expected to reproduce
// exactly — so content and count are what this harness pins, not order.
func m168EventKeys(events []ProtocolEvent) []string {
	keys := make([]string, len(events))
	for i, ev := range events {
		keys[i] = fmt.Sprintf("%+v", ev)
	}
	sort.Strings(keys)
	return keys
}

// m168Checkpoint is what gets compared at one `capture` op.
type m168Checkpoint struct {
	Board    m168Board
	Counters m168Counters
	Hash     uint64
	Events   []ProtocolEvent
}

// m168NamedCheckpoint is one scenario `capture` op's recorded state, plus the
// bookkeeping m168Compare needs to know how much of it can be compared.
type m168NamedCheckpoint struct {
	Label          string
	CP             m168Checkpoint
	PauseX, PauseY int16 // authoritative engine's paused-player square, or -1,-1
	// Tainted is true once the scenario has crossed a board edge/passage by
	// this checkpoint. See the file header: RoomManager gives every board its
	// own independently-seeded Engine, so RandSeed (and CurrentTick, also
	// reset per fresh room Engine) diverge from the bare Engine's one
	// continuous stream the instant a transfer happens — board cells, hash,
	// and events are no longer expected to agree from there on. Position and
	// HUD counters still are: they cross via a PlayerState value copy, not
	// simulation replay.
	Tainted bool
}

// m168Compare compares a checkpoint from an authoritative path (bare Engine)
// against a projected path (Room/WS), returning the first mismatch as an
// error (nil means full parity) — the same "return the error, let the caller
// decide" shape as oracleAdapterRun, which is what lets the fail-closed tests
// below inject a corruption and assert the comparison catches it rather than
// aborting the whole test via t.Fatal. pauseX/pauseY, when >= 0, name the
// authoritative engine's own paused player square: the headless engine never
// draws that square's blink-on phase (it defers presentation to the client,
// same as the real oracle harness's pause-blink normalization), while a fresh
// full-board redraw (e.g. RoomManager's TransitionDrawToBoard on room
// creation) draws the plain player glyph there — so that one square is
// exempted rather than compared.
func m168Compare(scenario, label, pathName string, want, got m168Checkpoint, pauseX, pauseY int16, tainted, compareCounters bool) error {
	if compareCounters && want.Counters != got.Counters {
		return fmt.Errorf("%s checkpoint %s: %s counters = %+v, want %+v", scenario, label, pathName, got.Counters, want.Counters)
	}
	if tainted {
		return nil
	}
	if x, y, mismatch := m168BoardDiffExcept(want.Board, got.Board, pauseX, pauseY); mismatch {
		return fmt.Errorf("%s checkpoint %s: %s board cell (%d,%d) = %+v, want %+v",
			scenario, label, pathName, x, y, got.Board[x][y], want.Board[x][y])
	}
	if want.Hash != got.Hash {
		return fmt.Errorf("%s checkpoint %s: %s StateHash = %#x, want %#x", scenario, label, pathName, got.Hash, want.Hash)
	}
	wantKeys, gotKeys := m168EventKeys(want.Events), m168EventKeys(got.Events)
	if !reflect.DeepEqual(wantKeys, gotKeys) {
		return fmt.Errorf("%s checkpoint %s: %s events = %v, want %v", scenario, label, pathName, gotKeys, wantKeys)
	}
	return nil
}

// m168LoadWorld loads a fresh, independent copy of an oracle micro-world, the
// same way testTownWorld does for TOWN.
func m168LoadWorld(t *testing.T, world string) TWorld {
	t.Helper()
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "oracle", world)
	requireFixture(t, worldBase+".ZZT")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q) failed", worldBase)
	}
	return setup.World
}

// m168ScenarioNames are every M16.3-M16.7(a) oracle scenario. All 24 replay
// through Engine + RoomManager (cheap: no sockets, no real-time waits).
var m168ScenarioNames = []string{
	"main", "scroll",
	"move", "item", "dark", "nrg", "shot", "pass", "time",
	"push", "dev", "mech", "blink", "ride",
	"beast", "fire", "ooze", "pede", "hunt",
	"talk", "walk", "cond", "morf",
	"prompt",
}

// TestThreePathEngineRoomEquivalence replays every M16.3-M16.7(a) scenario
// through a direct Engine and a RoomManager-wrapped Engine side by side,
// proving the room/protocol projection layer (dirty diffs, HUD snapshots,
// StateHash, and events) reconstructs identical state at every checkpoint.
func TestThreePathEngineRoomEquivalence(t *testing.T) {
	for _, name := range m168ScenarioNames {
		name := name
		t.Run(name, func(t *testing.T) {
			runM168EngineRoomScenario(t, name+".scn")
		})
	}
}

func runM168EngineRoomScenario(t *testing.T, scenarioFile string) {
	t.Helper()

	world, ops, phaseSensitive := parseOracleScenario(t, filepath.Join("..", "fixtures", "oracle", scenarioFile))

	initialState, initialTimerTicks, engCPs := runM168EnginePass(t, scenarioFile, world, ops)
	roomCPs, err := runM168RoomPass(t, scenarioFile, world, ops, initialState, initialTimerTicks, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(engCPs) != len(roomCPs) {
		t.Fatalf("%s: engine pass recorded %d checkpoints, room pass recorded %d", scenarioFile, len(engCPs), len(roomCPs))
	}
	// A `phase`-declaring scenario (oracle_parity_test.go's oracleSolvePhases)
	// has devices on the SAME board the title monitor sits on, so vanilla's
	// title/boot span is not just presentational — it is 120 real ticks of
	// board simulation (conveyors turning, guns rotating) that the bare Engine
	// experiences (it steps the title board during `boot`) and RoomManager
	// never does (a room does not exist, let alone simulate, before its first
	// join — M3.1's "empty rooms freeze"). So CurrentTick and RandSeed are
	// tainted from the very first post-play checkpoint, not just after a board
	// transfer. Position/HUD counters still are NOT expected to diverge from
	// this alone: every phase-sensitive fixture (dev.scn) is deliberately
	// authored so nothing RNG/CurrentTick-dependent touches the player (see
	// dev.scn's own comment), which the "first tainted checkpoint" comparison
	// below still verifies.
	seenTainted := false
	for i, eng := range engCPs {
		room := roomCPs[i]
		if eng.Label != room.Label {
			t.Fatalf("%s: checkpoint %d label mismatch: engine %q vs room %q", scenarioFile, i, eng.Label, room.Label)
		}
		tainted := phaseSensitive || eng.Tainted || room.Tainted
		compareCounters := !tainted || !seenTainted
		if tainted {
			seenTainted = true
		}
		if err := m168Compare(scenarioFile, eng.Label, "room", eng.CP, room.CP, eng.PauseX, eng.PauseY, tainted, compareCounters); err != nil {
			t.Fatal(err)
		}
	}
}

// runM168EnginePass replays a scenario through a bare Engine ONLY — no
// RoomManager involved — recording a checkpoint (and the direct Engine's own
// PlayerState right after `play`, for the room pass to seed from) at every
// post-title `capture` op. See the file header for why title checkpoints are
// skipped and why this must run to completion before any Room stepping
// starts.
func runM168EnginePass(t *testing.T, scenarioFile, world string, ops []oracleOp) (PlayerState, uint32, []m168NamedCheckpoint) {
	t.Helper()

	prevE := E
	defer func() { E = prevE }()
	E = NewEngine()
	E.Headless = true
	VideoInstall()
	TextWindowInit(5, 3, 50, 18)

	InputDeltaX = 0
	InputDeltaY = 0
	InputShiftPressed = false
	InputKeyPressed = 0
	InputLastDeltaX = 0
	InputLastDeltaY = 0
	InputKeyBuffer = ""
	E.GamePlayExitRequested = false
	E.TickSpeed = 4
	E.TickTimeDuration = int16(E.TickSpeed) * 2
	E.SoundBlockQueueing = false
	SoundClearQueue()

	worldBase := filepath.Join("..", "fixtures", "oracle", world)
	requireFixture(t, worldBase+".ZZT")

	RandomSeed(0)
	WorldCreate()
	if !WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q) failed", worldBase)
	}

	E.GameStateElement = E_MONITOR
	E.GamePlayExitRequested = false
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Element = E_MONITOR
	E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Color = ElementDefs[E_MONITOR].Color
	E.GenerateTransitionTable()
	E.TransitionDrawToBoard()
	E.CurrentTick = 0
	E.CurrentStatTicked = 0
	E.Events = nil

	var (
		interval          []ProtocolEvent
		window            *oracleTextWindow
		quit              *oracleYesNoPrompt
		debug             *oracleDebugEntry
		checkpoints       []m168NamedCheckpoint
		initialState      PlayerState
		initialTimerTicks uint32
		inTitle           = true
		startBoard        int16
	)

	step := func(input PlayerInput) {
		E.GameStepWithInputs(map[int16]PlayerInput{0: input})
		for _, ev := range E.Events {
			switch ev := ev.(type) {
			case ScrollEvent:
				window = &oracleTextWindow{Scroll: ev, LinePos: 1}
			case QuitPromptEvent:
				quit = &oracleYesNoPrompt{StatId: ev.StatId}
			case DebugPromptEvent:
				debug = &oracleDebugEntry{StatId: ev.StatId}
			}
		}
		interval = append(interval, ProtocolEvents(E.Events)...)
		E.Events = nil
	}

	for _, op := range ops {
		switch op.Kind {
		case "boot":
			for i := 0; i < op.Ticks/2; i++ {
				step(PlayerInput{})
			}

		case "play":
			E.GameStateElement = E_PLAYER
			BoardEnter(0)
			E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Element = E_PLAYER
			E.Board.Tiles[E.Board.Stats[0].X][E.Board.Stats[0].Y].Color = ElementDefs[E_PLAYER].Color
			E.PlayerFor(0).Paused = true
			E.TransitionDrawToBoard()
			E.TimerTicks += 30
			E.CurrentTick = 0
			E.CurrentStatTicked = 0
			E.Events = nil
			inTitle = false
			startBoard = E.World.Info.CurrentBoard
			initialState = *E.PlayerFor(0)
			initialTimerTicks = E.TimerTicks

		case "move", "shoot":
			step(PlayerInput{DeltaX: op.DX, DeltaY: op.DY, Key: op.Key, Shift: op.Kind == "shoot"})
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}

		case "key":
			consumed := true
			switch {
			case window != nil:
				if closed := window.key(op.Key, op.Scan); closed {
					E.SubmitScrollReply(window.Scroll.StatId, window.Hyperlink)
					window = nil
				}
			case quit != nil:
				if closed := quit.key(op.Key); closed {
					E.SubmitQuitReply(quit.StatId, quit.Yes)
					quit = nil
				}
			case debug != nil:
				if closed := debug.key(op.Key); closed {
					E.SubmitDebugCommand(debug.StatId, debug.Buffer)
					debug = nil
				}
			default:
				consumed = false
			}
			input := PlayerInput{}
			if !consumed {
				input = PlayerInput{Key: op.Key}
			}
			step(input)
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}

		case "settle":
			for i := 0; i < op.Ticks/2; i++ {
				step(PlayerInput{})
			}

		case "capture":
			if inTitle {
				// No room/protocol counterpart to a title checkpoint: see the
				// file header. Nothing to compare or accumulate.
				interval = nil
				continue
			}
			pauseX, pauseY := int16(-1), int16(-1)
			if E.PlayerFor(0).Paused {
				pauseX, pauseY = int16(E.Board.Stats[0].X)-1, int16(E.Board.Stats[0].Y)-1
			}
			cp := m168Checkpoint{
				Board:    m168BoardFromEngine(E),
				Counters: m168CountersFromEngine(E, 0),
				Hash:     StateHash(E),
				Events:   interval,
			}
			checkpoints = append(checkpoints, m168NamedCheckpoint{
				Label: op.Label, CP: cp, PauseX: pauseX, PauseY: pauseY,
				Tainted: E.World.Info.CurrentBoard != startBoard,
			})
			interval = nil
		}
	}
	return initialState, initialTimerTicks, checkpoints
}

// runM168RoomPass replays the SAME scenario ops through a RoomManager-wrapped
// Engine only, seeded at `play` with initialState (captured by
// runM168EnginePass — see the file header for why RoomManager's own join
// defaults cannot be trusted for a fair comparison). It must run only AFTER
// runM168EnginePass has fully finished — see the file header's fourth fact.
//
// initialTimerTicks seeds the room engine's own TimerTicks (BoardTimeElapsed's
// clock, game.go) to match the direct Engine's value right after `play` — the
// direct Engine bumps TimerTicks by 30 there (vanilla's own play-start
// convention) and has already accumulated more from any `boot` span, while a
// fresh room Engine starts at zero (JoinPlayer has no notion of "leaving a
// title screen"). Left unseeded, a per-board time-limit world's remaining-time
// countdown and "Running out of time!" message drift out of phase between the
// two paths — not a bug, the same "boot span has no room analog" fact as the
// RandSeed/CurrentTick taint above, just for a scenario that never crosses a
// board.
//
// corruptCells, if non-nil, is called with a 1-based tick counter and the
// dirty cells RoomManager.StepDiffs actually produced for this tick, and its
// return value is what gets applied to the diff-only reconstruction instead —
// the injection seam TestM168DroppedDirtyCellFailsClosed uses to prove a
// dropped cell is caught rather than silently accepted. Pass nil for a normal
// replay.
func runM168RoomPass(t *testing.T, scenarioFile, world string, ops []oracleOp, initialState PlayerState, initialTimerTicks uint32, corruptCells func(tick int, cells []ScreenCell) []ScreenCell) ([]m168NamedCheckpoint, error) {
	t.Helper()

	roomWorld := m168LoadWorld(t, world)
	rm := NewRoomManager(roomWorld)

	var (
		pid         PlayerID
		roomBoardID int16
		startBoard  int16
		roomBoard   m168Board
		interval    []ProtocolEvent
		window      *oracleTextWindow
		quit        *oracleYesNoPrompt
		debug       *oracleDebugEntry
		checkpoints []m168NamedCheckpoint
		joined      bool
		tick        int
	)

	step := func(input PlayerInput) {
		diffs := rm.StepDiffs(map[PlayerID]PlayerInput{pid: input})
		d := diffs[pid]
		tick++
		cells := d.Cells
		if corruptCells != nil {
			cells = corruptCells(tick, cells)
		}
		if newBoardID, _, ok := rm.PlayerLocation(pid); ok && newBoardID != roomBoardID {
			// The player crossed a passage/board edge this tick. RoomManager
			// always runs MultiRoom=true, so this Engine's diff describes the
			// room the player LEFT — the same reason WorldInstance.Tick sends a
			// BoardChangeMessage instead of a DiffMessage on a board change.
			roomBoardID = newBoardID
			if snap, ok := rm.Snapshot(pid); ok {
				roomBoard = m168Board{}
				roomBoard.apply(snap.Screen)
			}
		} else {
			roomBoard.apply(cells)
		}
		// Per-player sound/walk-click events are queued separately
		// (pendingPlayerEvents) for whoever calls DrainPlayerEvents — normally
		// WorldInstance.Tick. Driving RoomManager directly, this harness is that
		// caller.
		playerEvents := ProtocolEvents(rm.DrainPlayerEvents(pid))
		tickEvents := append(append([]ProtocolEvent{}, d.Events...), playerEvents...)
		for _, ev := range tickEvents {
			switch ev.Type {
			case "scroll":
				window = &oracleTextWindow{
					Scroll:  ScrollEvent{StatId: ev.StatID, PlayerStatId: ev.PlayerStatID, Title: ev.Title, Lines: ev.Lines},
					LinePos: 1,
				}
			case "quitPrompt":
				quit = &oracleYesNoPrompt{StatId: ev.StatID}
			case "debugPrompt":
				debug = &oracleDebugEntry{StatId: ev.StatID}
			}
		}
		interval = append(interval, tickEvents...)
	}

	for _, op := range ops {
		switch op.Kind {
		case "boot":
			// No room/monitor counterpart — see the file header.

		case "play":
			pid = rm.JoinPlayer(roomWorld.Info.CurrentBoard, 0, 0)
			rm.ApplyPlayerState(pid, initialState)
			roomBoardID, _, _ = rm.PlayerLocation(pid)
			startBoard = roomBoardID
			if room, ok := rm.Room(roomBoardID); ok {
				room.Engine.TimerTicks = initialTimerTicks
			}
			snap, ok := rm.Snapshot(pid)
			if !ok {
				t.Fatalf("%s: room snapshot after join failed", scenarioFile)
			}
			roomBoard = m168Board{}
			roomBoard.apply(snap.Screen)
			joined = true

		case "move", "shoot":
			if !joined {
				continue
			}
			step(PlayerInput{DeltaX: op.DX, DeltaY: op.DY, Key: op.Key, Shift: op.Kind == "shoot"})
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}

		case "key":
			if !joined {
				continue
			}
			consumed := true
			switch {
			case window != nil:
				if closed := window.key(op.Key, op.Scan); closed {
					rm.SubmitScrollReply(pid, window.Scroll.StatId, window.Hyperlink)
					window = nil
				}
			case quit != nil:
				if closed := quit.key(op.Key); closed {
					rm.SubmitQuitReply(pid, quit.Yes)
					quit = nil
				}
			case debug != nil:
				if closed := debug.key(op.Key); closed {
					rm.SubmitDebugCommand(pid, debug.Buffer)
					debug = nil
				}
			default:
				consumed = false
			}
			input := PlayerInput{}
			if !consumed {
				input = PlayerInput{Key: op.Key}
			}
			step(input)
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}

		case "settle":
			if !joined {
				continue
			}
			for i := 0; i < op.Ticks/2; i++ {
				step(PlayerInput{})
			}

		case "capture":
			if !joined {
				// Title checkpoint: runM168EnginePass records nothing for these
				// either, so skipping keeps both checkpoint lists index-aligned.
				continue
			}
			boardID, statID, ok := rm.PlayerLocation(pid)
			if !ok {
				t.Fatalf("%s checkpoint %s: player left the room manager", scenarioFile, op.Label)
			}
			room, ok := rm.Room(boardID)
			if !ok {
				t.Fatalf("%s checkpoint %s: room %d missing", scenarioFile, op.Label, boardID)
			}
			cp := m168Checkpoint{
				Board:    roomBoard,
				Counters: m168CountersFromEngine(room.Engine, statID),
				Hash:     StateHash(room.Engine),
				Events:   interval,
			}
			checkpoints = append(checkpoints, m168NamedCheckpoint{
				Label: op.Label, CP: cp, PauseX: -1, PauseY: -1,
				Tainted: boardID != startBoard,
			})

			// Full-snapshot fallback vs. diff-only: a fresh Snapshot must
			// reconstruct exactly what accumulating every diff's Cells already
			// built, proving the two client strategies (resync vs. incremental)
			// converge on identical state. This is Room-internal — unaffected by
			// the cross-board RNG divergence above, since both reconstructions
			// read the same room Engine.
			snap, ok := rm.Snapshot(pid)
			if !ok {
				t.Fatalf("%s checkpoint %s: snapshot fallback failed", scenarioFile, op.Label)
			}
			var snapBoard m168Board
			snapBoard.apply(snap.Screen)
			if x, y, mismatch := m168BoardDiff(roomBoard, snapBoard); mismatch {
				return checkpoints, fmt.Errorf("%s checkpoint %s: diff-reconstructed board disagrees with a fresh full snapshot at (%d,%d): diff-only=%+v snapshot=%+v",
					scenarioFile, op.Label, x, y, roomBoard[x][y], snapBoard[x][y])
			}

			interval = nil
		}
	}
	return checkpoints, nil
}

// TestM168DroppedDirtyCellFailsClosed proves the seam detects a single
// dropped dirty cell in the room path's diff-only reconstruction — the DoD's
// "a deliberately dropped dirty cell ... makes the test fail at the producing
// tick", mirroring TestOracleComparisonFailsClosed's perturbation idiom for
// the room/protocol layer. move.scn's very first `move right` always redraws
// the player's own square, so dropping that one cell from the first tick's
// diff is guaranteed to desync the reconstruction from then on.
func TestM168DroppedDirtyCellFailsClosed(t *testing.T) {
	scenarioFile := "move.scn"
	world, ops, _ := parseOracleScenario(t, filepath.Join("..", "fixtures", "oracle", scenarioFile))

	initialState, initialTimerTicks, _ := runM168EnginePass(t, scenarioFile, world, ops)

	dropped := false
	corrupt := func(tick int, cells []ScreenCell) []ScreenCell {
		if dropped {
			return cells
		}
		playerChar := ElementDefs[E_PLAYER].Character
		for i, c := range cells {
			if c.Ch == playerChar {
				dropped = true
				out := append([]ScreenCell{}, cells[:i]...)
				return append(out, cells[i+1:]...)
			}
		}
		return cells
	}

	_, err := runM168RoomPass(t, scenarioFile, world, ops, initialState, initialTimerTicks, corrupt)
	if !dropped {
		t.Fatalf("setup broken: %s's first move never produced a player-glyph dirty cell to drop", scenarioFile)
	}
	if err == nil {
		t.Fatal("dropping a dirty cell was not detected — the room path's diff-only reconstruction must fail closed")
	}
	if !strings.Contains(err.Error(), "disagrees with a fresh full snapshot") {
		t.Fatalf("failure was not pinned to the snapshot-vs-diff-only convergence check: %v", err)
	}
	t.Logf("correctly rejected: %v", err)
}

// TestM168MisroutedPerPlayerEventFailsClosed proves the seam detects a
// per-player event (M7.4's sound attribution) leaking to the wrong player —
// the DoD's "a ... misrouted per-player event makes the test fail". Two
// players share testMultiplayerSmokeWorld's board; player A steps onto the
// gem at (11,12), producing a per-player "sound" event queued only for A
// (RoomManager.pendingPlayerEvents, room_manager.go). This test captures that
// REAL event, then exercises m168Compare — the same comparison every scenario
// in this file relies on — with it deliberately leaked into player B's
// checkpoint, and asserts the comparison rejects it.
func TestM168MisroutedPerPlayerEventFailsClosed(t *testing.T) {
	world := testMultiplayerSmokeWorld(t)
	rm := NewRoomManager(world)
	pidA := rm.JoinPlayer(1, 0, 0)   // claims the world's native player stat at (10,12)
	pidB := rm.JoinPlayer(1, 20, 20) // a second player elsewhere on the same board

	rm.StepDiffs(map[PlayerID]PlayerInput{pidA: {DeltaX: 1, Key: KEY_RIGHT}})
	aEvents := ProtocolEvents(rm.DrainPlayerEvents(pidA))
	bEvents := ProtocolEvents(rm.DrainPlayerEvents(pidB))

	var gemSound ProtocolEvent
	found := false
	for _, ev := range aEvents {
		if ev.Type == "sound" {
			gemSound, found = ev, true
		}
	}
	if !found {
		t.Fatalf("setup broken: expected a per-player sound event for A's gem pickup, got %v", aEvents)
	}
	if len(bEvents) != 0 {
		t.Fatalf("setup broken: player B already has events %v before any misrouting", bEvents)
	}

	boardID, statB, ok := rm.PlayerLocation(pidB)
	if !ok {
		t.Fatal("setup broken: player B is missing from the room manager")
	}
	room, ok := rm.Room(boardID)
	if !ok {
		t.Fatalf("setup broken: room %d missing", boardID)
	}

	// want: B's real, correctly-empty event stream. got: the SAME checkpoint
	// with A's gem sound spliced in — the exact regression M7.4's per-player
	// routing exists to prevent (a "sound" meant for the toucher reaching
	// every player in the room instead).
	base := m168Checkpoint{
		Board:    m168BoardFromEngine(room.Engine),
		Counters: m168CountersFromEngine(room.Engine, statB),
		Hash:     StateHash(room.Engine),
		Events:   bEvents,
	}
	misrouted := base
	misrouted.Events = append(append([]ProtocolEvent{}, bEvents...), gemSound)

	err := m168Compare("misrouted-event", "gem-pickup", "player-b", base, misrouted, -1, -1, false, true)
	if err == nil {
		t.Fatal("a per-player event leaked onto another player's checkpoint was not detected")
	}
	if !strings.Contains(err.Error(), "events") {
		t.Fatalf("failure was not pinned to the events comparison: %v", err)
	}
	t.Logf("correctly rejected: %v", err)
}

// TestWebSocketReconstructsAuthoritativeScreen drives move.scn's full input
// script over a REAL dialed WebSocket client — not RoomManager called
// in-process, unlike every other test in this file — reconstructing the board
// purely from the wire protocol (the join snapshot's Screen, then each
// subsequent DiffMessage's Cells or BoardChangeMessage's full Snapshot.Screen)
// and comparing it, HUD, position, and StateHash to the direct authoritative
// Engine checkpoints from runM168EnginePass. This is the one link
// runM168RoomPass cannot itself prove: that JSON encode/decode and the real
// client/server goroutine boundary preserve what RoomManager already
// computes. It is deliberately just ONE scenario, not the full 24-scenario
// sweep runM168RoomPass drives: real socket I/O needs a real-time
// send/sleep/Tick/read cadence (matching the pattern every other WS test in
// this package already uses), so the cost scales with wall-clock ticks, not
// CPU; move.scn has no scroll/quit/debug prompts to route over the wire
// (fixtures/oracle/move.scn has zero `key` ops), so it alone is enough to
// prove the snapshot/diff/board-change reconstruction faithfully, leaving
// scroll/quit/debug/save/high-score wire round-trips to the directed tests
// below and to the pre-existing coverage in websocket_server_test.go (see
// NOTES.md's M16.8 entry for the full inventory).
//
// The join uses a pre-minted resume token rather than an ordinary
// JoinMessage: the oracle micro-worlds' one board is index 0
// (fixtures/oracle/*.ZZT), and JoinMessage.Board == 0 is the wire protocol's
// own "let the server pick" sentinel (ServeHTTP, websocket_server.go) — there
// is no way to name board 0 explicitly over the wire. Joining server-side via
// RoomManager.JoinPlayer(0, ...) has no such ambiguity, and resuming that
// player over the socket is a real, already-shipped path (M13.2 reconnect),
// not a test-only shortcut.
func TestWebSocketReconstructsAuthoritativeScreen(t *testing.T) {
	scenarioFile := "move.scn"
	world, ops, _ := parseOracleScenario(t, filepath.Join("..", "fixtures", "oracle", scenarioFile))
	initialState, initialTimerTicks, engCPs := runM168EnginePass(t, scenarioFile, world, ops)

	roomWorld := m168LoadWorld(t, world)
	server := NewWebSocketServer(roomWorld, roomWorld.Info.CurrentBoard)
	server.TickDuration = time.Hour // never auto-ticks; driven by step() below

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	playerID := server.RoomManager.JoinPlayer(roomWorld.Info.CurrentBoard, 0, 0)
	server.RoomManager.ApplyPlayerState(playerID, initialState)
	if room, ok := server.RoomManager.Room(roomWorld.Info.CurrentBoard); ok {
		room.Engine.TimerTicks = initialTimerTicks
	}
	inst := server.DefaultInstance
	inst.mu.Lock()
	token := inst.mintResumeTokenLocked(playerID)
	inst.mu.Unlock()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, ResumeToken: token}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.Type != MessageTypeSnapshot || snapshot.You.ID != playerID {
		t.Fatalf("unexpected join response: %+v", snapshot)
	}

	var board m168Board
	board.apply(snapshot.Screen)
	counters := m168CountersFromSnapshot(snapshot.HUD, snapshot.You)
	hash := snapshot.Hash

	var seq uint64
	step := func(input PlayerInput) {
		seq++
		msg := InputMessage{
			Type: MessageTypeInput, PlayerID: playerID, Seq: seq,
			DeltaX: input.DeltaX, DeltaY: input.DeltaY, Shift: input.Shift, Key: input.Key,
		}
		if err := wsjson.Write(ctx, conn, msg); err != nil {
			t.Fatalf("write input: %v", err)
		}
		// Give the server's read-loop goroutine time to apply the input before
		// this test drives the tick itself — the same pattern every other
		// manually-ticked WS test in this package uses (see
		// readUntilScrollEvent).
		time.Sleep(5 * time.Millisecond)
		server.Tick(ctx)

		readCtx, cancelRead := context.WithTimeout(ctx, time.Second)
		defer cancelRead()
		var raw json.RawMessage
		if err := wsjson.Read(readCtx, conn, &raw); err != nil {
			t.Fatalf("read tick message: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		switch envelope.Type {
		case MessageTypeDiff:
			var diff DiffMessage
			if err := json.Unmarshal(raw, &diff); err != nil {
				t.Fatalf("decode diff: %v", err)
			}
			board.apply(diff.Cells)
			hash = diff.Hash
			if diff.HUD != nil {
				for _, p := range diff.Players {
					if p.ID == playerID {
						counters = m168CountersFromSnapshot(*diff.HUD, p)
					}
				}
			}
		case MessageTypeBoardChange:
			var bc BoardChangeMessage
			if err := json.Unmarshal(raw, &bc); err != nil {
				t.Fatalf("decode boardChange: %v", err)
			}
			board = m168Board{}
			board.apply(bc.Snapshot.Screen)
			hash = bc.Snapshot.Hash
			counters = m168CountersFromSnapshot(bc.Snapshot.HUD, bc.Snapshot.You)
		default:
			t.Fatalf("unexpected message type %q", envelope.Type)
		}
	}

	idx := 0
	joined := false
	for _, op := range ops {
		switch op.Kind {
		case "boot":
			// No room/protocol counterpart — see the file header.
		case "play":
			joined = true
		case "move", "shoot":
			step(PlayerInput{DeltaX: op.DX, DeltaY: op.DY, Key: op.Key, Shift: op.Kind == "shoot"})
			for i := 0; i < 3; i++ {
				step(PlayerInput{})
			}
		case "settle":
			for i := 0; i < op.Ticks/2; i++ {
				step(PlayerInput{})
			}
		case "capture":
			if !joined {
				continue // title checkpoint; the engine pass records nothing for it either
			}
			eng := engCPs[idx]
			if eng.Label != op.Label {
				t.Fatalf("checkpoint order mismatch: scenario %q vs engine pass %q", op.Label, eng.Label)
			}
			idx++
			if counters != eng.CP.Counters {
				t.Fatalf("checkpoint %s: ws counters = %+v, want %+v", op.Label, counters, eng.CP.Counters)
			}
			if x, y, mismatch := m168BoardDiffExcept(eng.CP.Board, board, eng.PauseX, eng.PauseY); mismatch {
				t.Fatalf("checkpoint %s: ws board cell (%d,%d) = %+v, want %+v", op.Label, x, y, board[x][y], eng.CP.Board[x][y])
			}
			if hash != eng.CP.Hash {
				t.Fatalf("checkpoint %s: ws StateHash = %#x, want %#x", op.Label, hash, eng.CP.Hash)
			}
		}
	}
	if idx != len(engCPs) {
		t.Fatalf("scenario captured %d checkpoints over the wire, engine pass recorded %d", idx, len(engCPs))
	}
}

// TestWebSocketDeathAndRespawnEvents closes the one M16.8-assigned event pair
// (proto.event.death, proto.event.respawn) no existing test drives over a
// real socket: scroll/quit/debug (M4.1/M3.9/M3.11), pause (M4.2's
// TestM42CommandKeyOverWebSocket), save (M4.3a's TestM43aSaveOverWebSocket),
// and high-score (M4.3's TestM43QuitAndHighScoreOverWebSocket) all already
// have a WS-wire test; death/respawn did not. Death is forced directly via
// killPlayer (game.go) rather than through a combat scenario — this test's
// job is wire delivery of the two events, not damage mechanics, which
// m3_11_test.go/m16_5_test.go already cover at the Engine level.
func TestWebSocketDeathAndRespawnEvents(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "victim", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	playerID := snapshot.You.ID

	server.mu.Lock()
	room, ok := server.RoomManager.Room(1)
	if !ok {
		server.mu.Unlock()
		t.Fatal("board 1 room missing")
	}
	_, statID, _ := server.RoomManager.PlayerLocation(playerID)
	pState := room.Engine.PlayerFor(statID)
	pState.Health = 0
	room.Engine.GameUpdateSidebar()
	room.Engine.killPlayer(statID)
	server.mu.Unlock()

	death, ok := readUntilProtocolEvent(ctx, t, conn, server, "death", 20)
	if !ok {
		t.Fatal("no death event after killPlayer")
	}
	if death.StatID != statID {
		t.Errorf("death.statId=%d, want %d", death.StatID, statID)
	}

	respawn, ok := readUntilProtocolEvent(ctx, t, conn, server, "respawn", RESPAWN_TICKS+10)
	if !ok {
		t.Fatal("no respawn event within RESPAWN_TICKS of dying")
	}
	server.mu.Lock()
	health := room.Engine.PlayerFor(statID).Health
	server.mu.Unlock()
	if health <= 0 {
		t.Errorf("player still dead (health=%d) after a respawn event", health)
	}
	_ = respawn
}

// TestWebSocketUnrecognizedMessageIsIgnoredNotCrashed proves ServeHTTP's read
// loop (websocket_server.go) tolerates two distinct malformed-but-JSON shapes
// without disconnecting or crashing: a well-formed envelope with a `type` no
// case matches (falls through to the `default:` branch, which then also
// fails to parse as an InputMessage... except any JSON object legally decodes
// into InputMessage's all-optional fields, so this specifically exercises the
// "unknown type" path, not a decode failure) and a recognized `type` whose
// payload doesn't match its Go struct (a string where a number is wanted,
// failing json.Unmarshal into that message type — the `continue` branches
// each typed case has). A real input on the SAME connection afterward must
// still land, proving the read loop kept going rather than desyncing.
//
// What this does NOT cover (see the sibling test right below): a frame that
// is not valid JSON at all. wsjson.Read itself fails on that, and
// ServeHTTP's outer loop `break`s on that specific error
// (websocket_server.go's read loop) — ending THIS connection's read loop
// (a clean detach, M13.2 reconnect grace applies), not "silently ignored".
func TestWebSocketUnrecognizedMessageIsIgnoredNotCrashed(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	playerID := snapshot.You.ID
	startX := snapshot.You.X

	// 1. Well-formed JSON, unrecognized envelope type.
	if err := wsjson.Write(ctx, conn, struct {
		Type string `json:"type"`
	}{Type: "notARealMessageType"}); err != nil {
		t.Fatalf("write unknown type: %v", err)
	}
	// 2. A recognized type whose payload fails to decode into its Go struct
	// (seq wants a number, not a string).
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"input","seq":"not-a-number"}`)); err != nil {
		t.Fatalf("write malformed-payload input: %v", err)
	}

	// Neither must have crashed the server or ended this read loop: a real
	// input on the SAME connection must still move the player.
	if err := wsjson.Write(ctx, conn, InputMessage{
		Type: MessageTypeInput, PlayerID: playerID, Seq: 1, Keymask: InputMaskRight,
	}); err != nil {
		t.Fatalf("write real input after malformed ones: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		server.Tick(ctx)
		readCtx, cancelRead := context.WithTimeout(ctx, 200*time.Millisecond)
		var diff DiffMessage
		err := wsjson.Read(readCtx, conn, &diff)
		cancelRead()
		if err != nil {
			continue
		}
		for _, p := range diff.Players {
			if p.ID == playerID && p.X == startX+1 {
				return
			}
		}
	}
	t.Fatal("legitimate input never took effect — the connection did not survive the tolerated malformed messages")
}

// TestWebSocketInvalidJSONDetachesOnlyThatConnection proves a frame that
// isn't valid JSON at all (as opposed to well-formed JSON with an
// unrecognized shape, covered above) ends only the SENDING connection's read
// loop — via ServeHTTP's `break` on a wsjson.Read error, handleReadLoopExit's
// normal M13.2 detach path — without panicking the server or affecting any
// other connection. A second, unrelated player on the same room must be
// completely unaffected.
func TestWebSocketInvalidJSONDetachesOnlyThatConnection(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	connA, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.Close(websocket.StatusNormalClosure, "")
	connA.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, connA, JoinMessage{Type: MessageTypeJoin, Name: "A", Board: 1}); err != nil {
		t.Fatalf("write join A: %v", err)
	}
	var snapA SnapshotMessage
	if err := wsjson.Read(ctx, connA, &snapA); err != nil {
		t.Fatalf("read snapshot A: %v", err)
	}

	connB, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.Close(websocket.StatusNormalClosure, "")
	connB.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, connB, JoinMessage{Type: MessageTypeJoin, Name: "B", Board: 1}); err != nil {
		t.Fatalf("write join B: %v", err)
	}
	var snapB SnapshotMessage
	if err := wsjson.Read(ctx, connB, &snapB); err != nil {
		t.Fatalf("read snapshot B: %v", err)
	}
	startBX := snapB.You.X

	if err := connA.Write(ctx, websocket.MessageText, []byte("not json at all")); err != nil {
		t.Fatalf("write garbage on A: %v", err)
	}

	if err := wsjson.Write(ctx, connB, InputMessage{
		Type: MessageTypeInput, PlayerID: snapB.You.ID, Seq: 1, Keymask: InputMaskRight,
	}); err != nil {
		t.Fatalf("write input on B: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		server.Tick(ctx)
		readCtx, cancelRead := context.WithTimeout(ctx, 200*time.Millisecond)
		var diff DiffMessage
		err := wsjson.Read(readCtx, connB, &diff)
		cancelRead()
		if err != nil {
			continue
		}
		for _, p := range diff.Players {
			if p.ID == snapB.You.ID && p.X == startBX+1 {
				return
			}
		}
	}
	t.Fatal("player B's input never took effect — A's malformed frame affected the server or B's connection")
}

// TestWebSocketDebugCommandMessageOverWire closes the one M16.8-assigned
// protocol message no test — new or pre-existing — constructed over a real
// socket: debug_prompt_test.go's TestDebugCommandCreditsTypingPlayer already
// proves the "ammo" cheat credits the right player, but calls
// RoomManager.SubmitDebugCommand directly, never the wire DebugCommandMessage
// struct websocket_server.go's ServeHTTP decodes. This drives the identical
// cheat through an actual dialed client instead.
func TestWebSocketDebugCommandMessageOverWire(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "cheater", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	playerID := snapshot.You.ID
	startAmmo := snapshot.HUD.Ammo

	if err := wsjson.Write(ctx, conn, DebugCommandMessage{Type: MessageTypeDebugCommand, PlayerID: playerID, Text: "ammo"}); err != nil {
		t.Fatalf("write debugCommand: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		server.Tick(ctx)
		readCtx, cancelRead := context.WithTimeout(ctx, 200*time.Millisecond)
		var diff DiffMessage
		err := wsjson.Read(readCtx, conn, &diff)
		cancelRead()
		if err != nil {
			continue
		}
		if diff.HUD != nil && diff.HUD.Ammo == startAmmo+5 {
			return
		}
	}
	t.Fatal("the \"ammo\" debug command sent as a wire DebugCommandMessage never credited the player")
}
