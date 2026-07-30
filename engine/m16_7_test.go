package zztgo

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// M16.7 — world/title/portable-file parity sweep.
//
// This file covers the load-boundary hardening the sweep's DoD line
// ("malformed fixtures cannot panic") turned up: BoardOpen and worldReadFrom
// have zero bounds checking on RLE run counts, StatCount, or board data
// length, unlike the .BRD import path's existing safeBoardOpen guard
// (editor_session.go). A truncated or adversarial .ZZT/.SAV reaching
// LoadWorldBytes, LoadPristineWorld, RoomManager.LoadWorld, or
// RoomManager.RestoreSnapshot ran outside any recover on the multiplayer
// path, so it could panic a live server goroutine and take every room down,
// not just the uploader's. See game.go's ErrWorldCorrupt/validateWorldBoards
// and NOTES.md's M16.7 entry.

// worldBytesFromFreshWorld returns valid worldWriteTo bytes for a brand-new,
// single-board (board 0 only) world, as a base for hand-corrupting.
func worldBytesFromFreshWorld(t *testing.T) []byte {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	var buf bytes.Buffer
	if err := e.worldWriteTo(&buf); err != nil {
		t.Fatalf("worldWriteTo: %v", err)
	}
	return buf.Bytes()
}

// boardZeroStatCountOffset walks board 0's RLE tile stream exactly as
// BoardOpen does, to find the byte offset of its StatCount field without
// hand-computing RLE compression output.
func boardZeroStatCountOffset(boardData []byte) int {
	ptr := boardData[SizeOfBoardName:]
	consumed := SizeOfBoardName
	var ix, iy int16 = 1, 1
	var rle TRleTile
	rle.Count = 0
	for {
		if rle.Count <= 0 {
			rle = LoadRleTile(ptr[:SizeOfRleTile])
			ptr = ptr[SizeOfRleTile:]
			consumed += SizeOfRleTile
		}
		ix++
		if ix > BOARD_WIDTH {
			ix = 1
			iy++
		}
		rle.Count--
		if iy > BOARD_HEIGHT {
			break
		}
	}
	return consumed + SizeOfBoardInfo
}

func TestWorldReadFromRejectsCorruptBoardCount(t *testing.T) {
	data := worldBytesFromFreshWorld(t)

	t.Run("direct count past MAX_BOARD", func(t *testing.T) {
		corrupt := append([]byte(nil), data...)
		StoreInt16(corrupt[:2], MAX_BOARD+50)
		e := NewEngine()
		e.Headless = true
		if err := e.worldReadFrom(bytes.NewReader(corrupt), false, nil); !errors.Is(err, ErrWorldCorrupt) {
			t.Fatalf("worldReadFrom error = %v, want ErrWorldCorrupt", err)
		}
	})

	t.Run("extended-version count past MAX_BOARD", func(t *testing.T) {
		corrupt := append([]byte(nil), data...)
		StoreInt16(corrupt[:2], -1)
		StoreInt16(corrupt[2:4], MAX_BOARD+50)
		e := NewEngine()
		e.Headless = true
		if err := e.worldReadFrom(bytes.NewReader(corrupt), false, nil); !errors.Is(err, ErrWorldCorrupt) {
			t.Fatalf("worldReadFrom error = %v, want ErrWorldCorrupt", err)
		}
	})

	t.Run("extended-version count negative", func(t *testing.T) {
		corrupt := append([]byte(nil), data...)
		StoreInt16(corrupt[:2], -1)
		StoreInt16(corrupt[2:4], -7)
		e := NewEngine()
		e.Headless = true
		if err := e.worldReadFrom(bytes.NewReader(corrupt), false, nil); !errors.Is(err, ErrWorldCorrupt) {
			t.Fatalf("worldReadFrom error = %v, want ErrWorldCorrupt", err)
		}
	})
}

// TestLoadWorldBytesRejectsTruncatedBoard corrupts board 0's declared length
// down to a handful of bytes -- far too short to hold even a board name, so
// BoardOpen slices past the end of the buffer. LoadWorldBytes must refuse,
// not panic.
func TestLoadWorldBytesRejectsTruncatedBoard(t *testing.T) {
	data := worldBytesFromFreshWorld(t)
	const headerSize = 512
	garbage := []byte{1, 2, 3, 4, 5}

	corrupt := append([]byte(nil), data[:headerSize]...)
	lenBuf := make([]byte, 2)
	StoreInt16(lenBuf, int16(len(garbage)))
	corrupt = append(corrupt, lenBuf...)
	corrupt = append(corrupt, garbage...)

	if _, err := LoadWorldBytes(corrupt); !errors.Is(err, ErrWorldCorrupt) {
		t.Fatalf("LoadWorldBytes error = %v, want ErrWorldCorrupt", err)
	}
}

// TestLoadWorldBytesRejectsOversizedStatCount leaves board 0's tiles/board-info
// intact and hand-edits only StatCount to a value past MAX_STAT, the other
// class of BoardOpen panic (Stats array index out of range) that truncation
// alone does not exercise.
func TestLoadWorldBytesRejectsOversizedStatCount(t *testing.T) {
	data := worldBytesFromFreshWorld(t)
	const headerSize = 512

	boardLen := LoadInt16(data[headerSize : headerSize+2])
	boardData := append([]byte(nil), data[headerSize+2:headerSize+2+int(boardLen)]...)

	statCountOff := boardZeroStatCountOffset(boardData)
	StoreInt16(boardData[statCountOff:statCountOff+2], MAX_STAT+50)

	corrupt := append([]byte(nil), data[:headerSize+2]...)
	corrupt = append(corrupt, boardData...)

	if _, err := LoadWorldBytes(corrupt); !errors.Is(err, ErrWorldCorrupt) {
		t.Fatalf("LoadWorldBytes error = %v, want ErrWorldCorrupt", err)
	}
}

// TestRoomManagerLoadWorldRejectsCorruptFile and
// TestRoomManagerRestoreSnapshotRejectsCorruptFile prove the same hardening
// covers the two on-disk multiplayer entry points (world picker load and
// autosave restore), not only the in-memory LoadWorldBytes path.
func TestRoomManagerLoadWorldRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	data := worldBytesFromFreshWorld(t)
	const headerSize = 512
	corrupt := append([]byte(nil), data[:headerSize]...)
	lenBuf := make([]byte, 2)
	StoreInt16(lenBuf, 3)
	corrupt = append(corrupt, lenBuf...)
	corrupt = append(corrupt, []byte{9, 9, 9}...)

	if err := os.WriteFile(filepath.Join(dir, "BAD.ZZT"), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	rm := NewRoomManager(TWorld{})
	if err := rm.LoadWorld(dir, "BAD"); err == nil {
		t.Fatal("LoadWorld accepted a corrupt .ZZT file without error")
	}
}

func TestRoomManagerRestoreSnapshotRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	data := worldBytesFromFreshWorld(t)
	const headerSize = 512
	corrupt := append([]byte(nil), data[:headerSize]...)
	lenBuf := make([]byte, 2)
	StoreInt16(lenBuf, 3)
	corrupt = append(corrupt, lenBuf...)
	corrupt = append(corrupt, []byte{9, 9, 9}...)

	if err := os.WriteFile(filepath.Join(dir, "BAD.SAV"), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	rm := NewRoomManager(TWorld{})
	if err := rm.RestoreSnapshot(dir, "BAD"); err == nil {
		t.Fatal("RestoreSnapshot accepted a corrupt .SAV file without error")
	}
}

// TestVanillaSaveRoundTripPreservesDarknessAndTime drives the real vanilla
// .SAV byte format (WorldSave/WorldLoad, game.go — the same GameWorldSave
// routine as .ZZT, just a different extension per GAME.PAS:1660), not
// snapshot.go's engine-only JSON persistence (m4_3a_test.go covers that
// mechanism; this is the byte-format contract M16.7 needs and neither
// m4_3a_test.go nor m3_11_test.go had covered before this task — no existing
// test drives darkness or the per-board timer through a save boundary).
func TestVanillaSaveRoundTripPreservesDarknessAndTime(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.Board.Info.IsDark = true
	e.Board.Info.TimeLimitSec = 30
	pState := e.PlayerFor(0)
	pState.TorchTicks = 123
	pState.Torches = 2
	pState.BoardTimeSec = 17
	pState.BoardTimeHsec = 42

	name := filepath.Join(dir, "DARKSAV")
	e.WorldSave(name, ".SAV")

	fresh := NewEngine()
	fresh.Headless = true
	if !fresh.WorldLoad(name, ".SAV", false) {
		t.Fatal("WorldLoad(.SAV) failed to reload a file this same test just saved")
	}

	if !fresh.Board.Info.IsDark {
		t.Error("darkness (Board.Info.IsDark) did not survive the .SAV round trip")
	}
	if fresh.Board.Info.TimeLimitSec != 30 {
		t.Errorf("TimeLimitSec = %d, want 30", fresh.Board.Info.TimeLimitSec)
	}
	freshP := fresh.PlayerFor(0)
	if freshP.TorchTicks != 123 {
		t.Errorf("TorchTicks = %d, want 123", freshP.TorchTicks)
	}
	if freshP.Torches != 2 {
		t.Errorf("Torches = %d, want 2", freshP.Torches)
	}
	if freshP.BoardTimeSec != 17 || freshP.BoardTimeHsec != 42 {
		t.Errorf("BoardTime = %d.%d, want 17.42", freshP.BoardTimeSec, freshP.BoardTimeHsec)
	}
}

// TestWorldLoadRejectsCorruptFileWithoutPanicking covers the terminal-facing
// WorldLoad (game.go), used by GameWorldLoad and the -startup-world flag, not
// only the headless multiplayer entry points above.
func TestWorldLoadRejectsCorruptFileWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	data := worldBytesFromFreshWorld(t)
	const headerSize = 512
	corrupt := append([]byte(nil), data[:headerSize]...)
	lenBuf := make([]byte, 2)
	StoreInt16(lenBuf, 3)
	corrupt = append(corrupt, lenBuf...)
	corrupt = append(corrupt, []byte{9, 9, 9}...)

	if err := os.WriteFile(filepath.Join(dir, "BAD.ZZT"), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEngine()
	e.Headless = true
	if got := e.WorldLoad(filepath.Join(dir, "BAD"), ".ZZT", false); got {
		t.Fatal("WorldLoad reported success on a corrupt file")
	}
}
