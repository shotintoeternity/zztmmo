package zztgo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sessCheckpoint is the per-room StateHash captured at one tick — the fingerprint
// a replay must reproduce.
type sessCheckpoint struct {
	tick   int
	hashes map[int16]uint64
}

// recordCheckpoint snapshots the manager's per-room hashes at every 100th tick
// (the DoD cadence). The caller also records a final checkpoint at the end.
func recordCheckpoint(cps *[]sessCheckpoint, tick int, rm *RoomManager) {
	if tick%100 != 0 {
		return
	}
	*cps = append(*cps, sessCheckpoint{tick: tick, hashes: rm.RoomStateHashes()})
}

// assertCheckpointsEqual proves a replay reproduced the live session exactly:
// same checkpoint ticks, same set of rooms, same StateHash per room.
func assertCheckpointsEqual(t *testing.T, live, replay []sessCheckpoint) {
	t.Helper()
	if len(live) != len(replay) {
		t.Fatalf("checkpoint count: live %d, replay %d", len(live), len(replay))
	}
	if len(live) == 0 {
		t.Fatal("no checkpoints captured")
	}
	for i := range live {
		l, r := live[i], replay[i]
		if l.tick != r.tick {
			t.Fatalf("checkpoint %d tick: live %d, replay %d", i, l.tick, r.tick)
		}
		if len(l.hashes) != len(r.hashes) {
			t.Fatalf("tick %d room count: live %d, replay %d", l.tick, len(l.hashes), len(r.hashes))
		}
		for board, lh := range l.hashes {
			rh, ok := r.hashes[board]
			if !ok {
				t.Fatalf("tick %d: replay is missing board %d", l.tick, board)
			}
			if lh != rh {
				t.Fatalf("tick %d board %d hash: live %016x, replay %016x", l.tick, board, lh, rh)
			}
		}
	}
}

// recordedRoomManager builds a RoomManager from a world's own serialized bytes
// (so the live session starts from the exact bytes the recording embeds) with a
// recorder writing to buf. Returning the header-bytes-derived world guarantees
// live and replay begin byte-identical regardless of round-trip fidelity.
func recordedRoomManager(t *testing.T, name string, world TWorld, buf *bytes.Buffer) (*RoomManager, *SessionRecorder) {
	t.Helper()
	header, wb, err := newSessionHeader(name, world)
	if err != nil {
		t.Fatalf("session header: %v", err)
	}
	start, err := LoadWorldBytes(wb)
	if err != nil {
		t.Fatalf("load world bytes: %v", err)
	}
	rec, err := NewSessionRecorder(buf, header)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	rm := NewRoomManager(start)
	rm.SetRecorder(rec)
	return rm, rec
}

func townWorld(t *testing.T) TWorld {
	t.Helper()
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	requireFixture(t, filepath.Join("..", "fixtures", "TOWN.ZZT"))
	if !setup.WorldLoad(filepath.Join("..", "fixtures", "TOWN"), ".ZZT", false) {
		t.Fatal("loading required fixture ../fixtures/TOWN.ZZT failed")
	}
	return setup.World
}

// TestSessionRecordReplayVendorQuit is the M14.2 DoD's TOWN half: a two-player
// session — join, move, buy from the TOWN vendor via a scroll reply, one quits —
// recorded and played back headless reproduces per-room StateHash at every 100
// ticks and at the end. The two players sit on different boards so multiple
// rooms are hashed.
func TestSessionRecordReplayVendorQuit(t *testing.T) {
	// Vendor coordinates come from a throwaway manager; the board geometry is
	// identical, so the same stand tile and step direction apply to the recorded
	// manager. Using JoinPlayer to create the room in both the live run and the
	// replay keeps room creation symmetric.
	_, _, _, standX, standY, stepX, stepY := vendorRoom(t)

	world := townWorld(t)

	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, "TOWN", world, &buf)

	p1 := rm.JoinPlayer(vendorBoard, standX, standY)
	rm.SetPlayerName(p1, "Alice")
	rm.Snapshot(p1)
	p2 := rm.JoinPlayer(1, 0, 0) // a second active room
	rm.SetPlayerName(p2, "Bob")
	rm.Snapshot(p2)

	const totalTicks = 260
	var live []sessCheckpoint
	scrollReplied := false
	quitDone := false
	for k := 0; k < totalTicks; k++ {
		inputs := map[PlayerID]PlayerInput{}
		// Walk P1 into the vendor for the first few ticks, until the scroll opens.
		if !scrollReplied && k < 14 {
			inputs[p1] = PlayerInput{DeltaX: stepX, DeltaY: stepY}
		}
		// Nudge P2 back and forth so it is genuinely moving.
		if k%2 == 0 {
			inputs[p2] = PlayerInput{DeltaX: 1}
		} else {
			inputs[p2] = PlayerInput{DeltaX: -1}
		}

		diffs := rm.StepDiffs(inputs)
		recordCheckpoint(&live, k, rm)

		if !scrollReplied {
			if ev, ok := findEvent(diffs[p1].Events, "scroll"); ok {
				rm.SubmitScrollReply(p1, ev.StatID, "ba")
				scrollReplied = true
			}
		}
		if k == 200 && !quitDone {
			rm.SubmitQuitReply(p2, true)
			quitDone = true
		}
	}
	live = append(live, sessCheckpoint{tick: totalTicks - 1, hashes: rm.RoomStateHashes()})

	if !scrollReplied {
		t.Fatal("vendor scroll never opened; session did not exercise a scroll reply")
	}
	rec.Close()

	var replay []sessCheckpoint
	var lastTick int
	replayed, err := ReplaySession(&buf, func(tick int, rm *RoomManager) {
		lastTick = tick
		recordCheckpoint(&replay, tick, rm)
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	replay = append(replay, sessCheckpoint{tick: lastTick, hashes: replayed.RoomStateHashes()})

	assertCheckpointsEqual(t, live, replay)

	// The quit really removed P2: its board is gone or empty after replay.
	if _, _, ok := replayed.PlayerLocation(p2); ok {
		t.Error("P2 should have quit and left the world in the replay")
	}
}

// TestSessionRecordReplayTransfer is the M14.2 DoD's transfer half: a player
// crossing a passage from board A to board B is a consequence StepDiffs
// regenerates from the recorded inputs, never an op the recorder logs. A
// deterministic two-board world makes the crossing reachable in one tick.
func TestSessionRecordReplayTransfer(t *testing.T) {
	world := twoBoardPassageWorld(t)

	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, "PASSAGE", world, &buf)

	const boardA = int16(1)
	p1 := rm.JoinPlayer(boardA, 9, 12) // beside the passage at (10,12)
	rm.Snapshot(p1)
	p2 := rm.JoinPlayer(boardA, 30, 12) // stays on board A
	rm.Snapshot(p2)

	const totalTicks = 120
	var live []sessCheckpoint
	for k := 0; k < totalTicks; k++ {
		inputs := map[PlayerID]PlayerInput{}
		if k == 3 {
			inputs[p1] = PlayerInput{DeltaX: 1} // step onto the passage → transfer to B
		}
		if k%2 == 0 {
			inputs[p2] = PlayerInput{DeltaX: -1}
		}
		rm.StepDiffs(inputs)
		recordCheckpoint(&live, k, rm)
	}
	live = append(live, sessCheckpoint{tick: totalTicks - 1, hashes: rm.RoomStateHashes()})

	// The transfer actually happened: P1 is on board B (2), not board A.
	if boardID, _, ok := rm.PlayerLocation(p1); !ok || boardID != 2 {
		t.Fatalf("P1 should have transferred to board 2, got board %d (ok=%v)", boardID, ok)
	}
	rec.Close()

	var replay []sessCheckpoint
	var lastTick int
	replayed, err := ReplaySession(&buf, func(tick int, rm *RoomManager) {
		lastTick = tick
		recordCheckpoint(&replay, tick, rm)
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	replay = append(replay, sessCheckpoint{tick: lastTick, hashes: replayed.RoomStateHashes()})

	assertCheckpointsEqual(t, live, replay)

	if boardID, _, ok := replayed.PlayerLocation(p1); !ok || boardID != 2 {
		t.Fatalf("replay: P1 should be on board 2, got board %d (ok=%v)", boardID, ok)
	}
}

// TestSessionRecorderDisabledIsInert proves recording off is byte-for-byte the
// prior behavior: a nil recorder must change nothing a session produces.
func TestSessionRecorderDisabledIsInert(t *testing.T) {
	world := twoBoardPassageWorld(t)

	run := func(withRecorder bool) map[int16]uint64 {
		rm := NewRoomManager(world)
		if withRecorder {
			rec, err := NewSessionRecorder(&bytes.Buffer{}, recHeader{})
			if err != nil {
				t.Fatalf("recorder: %v", err)
			}
			rm.SetRecorder(rec)
			defer rec.Close()
		}
		p1 := rm.JoinPlayer(1, 9, 12)
		p2 := rm.JoinPlayer(1, 30, 12)
		for k := 0; k < 40; k++ {
			in := map[PlayerID]PlayerInput{}
			if k == 3 {
				in[p1] = PlayerInput{DeltaX: 1}
			}
			in[p2] = PlayerInput{DeltaX: -1}
			rm.StepDiffs(in)
		}
		return rm.RoomStateHashes()
	}

	off := run(false)
	on := run(true)
	if len(off) != len(on) {
		t.Fatalf("room count differs: off %d, on %d", len(off), len(on))
	}
	for board, h := range off {
		if on[board] != h {
			t.Fatalf("board %d hash differs with recording on: off %016x, on %016x", board, h, on[board])
		}
	}
}

// TestServerRecordingRoundTripsToDisk exercises the server wiring: EnableRecording
// attaches a recorder to the boot instance, a driven session writes a JSONL file,
// CloseRecorders flushes it, and ReplaySession reproduces the run from that file.
func TestServerRecordingRoundTripsToDisk(t *testing.T) {
	world := twoBoardPassageWorld(t)
	srv := NewWebSocketServer(world, 1)
	dir := t.TempDir()
	if err := srv.EnableRecording(dir); err != nil {
		t.Fatalf("EnableRecording: %v", err)
	}

	rm := srv.DefaultInstance.RoomManager
	p1 := rm.JoinPlayer(1, 9, 12)
	rm.Snapshot(p1)
	p2 := rm.JoinPlayer(1, 30, 12)
	rm.Snapshot(p2)

	var live []sessCheckpoint
	const totalTicks = 80
	for k := 0; k < totalTicks; k++ {
		in := map[PlayerID]PlayerInput{}
		if k == 3 {
			in[p1] = PlayerInput{DeltaX: 1} // transfer to board 2
		}
		if k%2 == 0 {
			in[p2] = PlayerInput{DeltaX: -1}
		}
		rm.StepDiffs(in)
		recordCheckpoint(&live, k, rm)
	}
	live = append(live, sessCheckpoint{tick: totalTicks - 1, hashes: rm.RoomStateHashes()})

	srv.CloseRecorders()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var recPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			recPath = filepath.Join(dir, e.Name())
		}
	}
	if recPath == "" {
		t.Fatalf("no .jsonl recording written to %s (found %v)", dir, entries)
	}

	f, err := os.Open(recPath)
	if err != nil {
		t.Fatalf("open recording: %v", err)
	}
	defer f.Close()

	var replay []sessCheckpoint
	var lastTick int
	replayed, err := ReplaySession(f, func(tick int, rm *RoomManager) {
		lastTick = tick
		recordCheckpoint(&replay, tick, rm)
	})
	if err != nil {
		t.Fatalf("replay from disk: %v", err)
	}
	replay = append(replay, sessCheckpoint{tick: lastTick, hashes: replayed.RoomStateHashes()})

	assertCheckpointsEqual(t, live, replay)
}

// twoBoardPassageWorld builds the two-board world used by the transfer tests:
// board A has a passage at (10,12) to board B, board B a matching one at (5,5)
// back to A. Mirrors TestRoomManagerPassageTransfer's construction.
func twoBoardPassageWorld(t *testing.T) TWorld {
	t.Helper()
	const passageColor = byte(0x0E)
	const boardA = int16(1)
	const boardB = int16(2)

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()

	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Tiles[10][12] = TTile{Element: E_PASSAGE, Color: passageColor}
	setup.AddStat(10, 12, E_PASSAGE, int16(passageColor), 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = byte(boardB)
	setup.Board.Name = "Board A"
	// Set CurrentBoard so BoardClose writes both BoardData[1] and BoardLen[1];
	// worldWriteTo needs the length prefix to round-trip (a manual IoTmpBuf copy
	// leaves BoardLen unset, which serializes to a corrupt file).
	setup.World.Info.CurrentBoard = boardA
	setup.BoardClose()

	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Tiles[5][5] = TTile{Element: E_PASSAGE, Color: passageColor}
	setup.AddStat(5, 5, E_PASSAGE, int16(passageColor), 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = byte(boardA)
	setup.Board.Name = "Board B"
	setup.World.Info.CurrentBoard = boardB
	setup.BoardClose()

	setup.World.BoardCount = 2
	setup.World.Info.CurrentBoard = boardA

	return setup.World
}
