package zztgo

// M42.3: a join arrives where the editor shows the player, even when the world
// was loaded from a save. Reported on production: COTS24HR's "START: Menu"
// has its player tile at 30,14, but joins landed at 1,25 and 1,24. The world
// had been restored from its autosave, written after the last player left, so
// board 7 had no player tile and the start fell back to the board's stored
// start square, which in a .ZZT is wherever the author's player last entered.

import (
	"testing"
)

// m423StaleWorld is CONTROL with board 1's stored start square moved to the
// bottom-left corner, as COTS24HR's is, while its player tile stays at 6,12.
func m423StaleWorld(t *testing.T) TWorld {
	t.Helper()
	world := m421RoomManager(t).world
	e := newSnapshotEngine()
	e.World = cloneWorld(world)
	e.BoardOpen(m421Board)
	e.Board.Info.StartPlayerX = 1
	e.Board.Info.StartPlayerY = BOARD_HEIGHT
	e.BoardClose()
	return e.World
}

// m423Restored hosts world, lets prepare play it, saves it the way autosave
// does, and returns a manager for the same world restored from that save.
func m423Restored(t *testing.T, world TWorld, prepare func(rm *RoomManager)) *RoomManager {
	t.Helper()
	rm := NewRoomManager(world)
	prepare(rm)
	dir := t.TempDir()
	if _, err := writeWorldSnapshot(dir, "CONTROL", rm.snapshotWorldNoSaver()); err != nil {
		t.Fatalf("write the autosave: %v", err)
	}
	restored := NewRoomManager(world)
	if err := restored.RestoreSnapshot(dir, "CONTROL"); err != nil {
		t.Fatalf("restore the autosave: %v", err)
	}
	return restored
}

func m423WantAt(t *testing.T, rm *RoomManager, id PlayerID, x, y int16, who string) {
	t.Helper()
	if gx, gy := m421Pos(t, rm, id); gx != x || gy != y {
		t.Fatalf("%s is at %d,%d, want %d,%d", who, gx, gy, x, y)
	}
}

// The reported bug: everyone left, the autosave has no player tile on the
// board, and the restored world's joins went to the stored corner square.
func TestM423JoinAfterAnEmptyBoardAutosaveArrivesAtThePlayerTile(t *testing.T) {
	rm := m423Restored(t, m423StaleWorld(t), func(rm *RoomManager) {
		rm.LeavePlayer(m421Join(t, rm))
	})
	a := m421Join(t, rm)
	m423WantAt(t, rm, a, m421StartX, m421StartY, "the first joiner after the restore")
	b := m421Join(t, rm)
	m423WantAt(t, rm, b, m421StartX, m421StartY-1, "the second joiner (north of the start)")
}

// A server save drops every player tile (snapshotRoomBoard), so a board
// anybody has visited comes back from a save with none: the authored world is
// the only record of where the player starts.
func TestM423ASavedBoardCarriesNoPlayerTile(t *testing.T) {
	world := NewRoomManager(m423StaleWorld(t))
	a := m421Join(t, world)
	world.rooms[m421Board].Engine.MoveStat(world.players[a].statID, 16, 12)
	saved := world.snapshotWorldNoSaver()
	if _, _, ok := authoredPlayerTile(saved, m421Board); ok {
		t.Fatal("a save kept a player tile; M42.3's reliance on designWorld needs revisiting")
	}
}

// The pristine world still behaves as M42.1 pinned it: a stored start that
// disagrees with the player tile does not take over.
func TestM423PristineWorldIgnoresAStaleStoredStart(t *testing.T) {
	rm := NewRoomManager(m423StaleWorld(t))
	a := m421Join(t, rm)
	m423WantAt(t, rm, a, m421StartX, m421StartY, "the first joiner")
	rm.LeavePlayer(a)
	b := m421Join(t, rm)
	m423WantAt(t, rm, b, m421StartX, m421StartY, "a joiner after the board emptied")
}
