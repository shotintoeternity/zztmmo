package zztgo

// M42.1: a player who joins a board arrives on the board's designed start
// square, or, when someone already stands there, on the nearest free square they
// can walk off. Before this a second joiner was placed beside wherever the first
// player had walked to, because the join search started from the board's
// player stat, and that stat had become the first player.

import (
	"os"
	"path/filepath"
	"testing"
)

// CONTROL's "Control Field" (board 1) starts its player at 6,12.
const (
	m421Board  = int16(1)
	m421StartX = int16(6)
	m421StartY = int16(12)
)

func m421RoomManager(t *testing.T) *RoomManager {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "fixtures", "control.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/control.zwd: %v", err)
	}
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/control.zwd): %v", err)
	}
	return NewRoomManager(world)
}

func m421Pos(t *testing.T, rm *RoomManager, id PlayerID) (int16, int16) {
	t.Helper()
	p := rm.players[id]
	if p == nil {
		t.Fatalf("player %d is not on any board", id)
	}
	stat := rm.rooms[p.boardID].Engine.Board.Stats[p.statID]
	return int16(stat.X), int16(stat.Y)
}

func m421Join(t *testing.T, rm *RoomManager) PlayerID {
	t.Helper()
	id := rm.JoinPlayer(m421Board, 0, 0)
	if id == 0 {
		t.Fatal("the join was refused")
	}
	return id
}

// The reported bug: the first player walks off, and the second arrives beside
// them instead of at the board's start.
func TestM421SecondJoinerArrivesAtTheDesignedStart(t *testing.T) {
	rm := m421RoomManager(t)
	a := m421Join(t, rm)
	if x, y := m421Pos(t, rm, a); x != m421StartX || y != m421StartY {
		t.Fatalf("the first joiner is at %d,%d, want the designed start %d,%d", x, y, m421StartX, m421StartY)
	}
	room := rm.rooms[m421Board]
	room.Engine.MoveStat(rm.players[a].statID, 16, 12)

	b := m421Join(t, rm)
	if x, y := m421Pos(t, rm, b); x != m421StartX || y != m421StartY {
		t.Fatalf("the second joiner is at %d,%d with the first at 16,12, want the designed start %d,%d", x, y, m421StartX, m421StartY)
	}
	if room.Engine.Board.Tiles[m421StartX][m421StartY].Element != E_PLAYER {
		t.Fatal("the designed start does not hold the second joiner's player tile")
	}
}

// With the start square taken, each later joiner gets a free square next to it
// that they can walk off, never one another's square.
func TestM421CrowdedStartPlacesJoinersOnTheNearestFreeSquare(t *testing.T) {
	rm := m421RoomManager(t)
	ids := []PlayerID{m421Join(t, rm), m421Join(t, rm), m421Join(t, rm)}
	room := rm.rooms[m421Board]
	seen := map[[2]int16]PlayerID{}
	for i, id := range ids {
		x, y := m421Pos(t, rm, id)
		if other, dup := seen[[2]int16{x, y}]; dup {
			t.Fatalf("players %d and %d share %d,%d", other, id, x, y)
		}
		seen[[2]int16{x, y}] = id
		if i == 0 {
			if x != m421StartX || y != m421StartY {
				t.Fatalf("the first joiner is at %d,%d, want %d,%d", x, y, m421StartX, m421StartY)
			}
			continue
		}
		if d := absInt16(x-m421StartX) + absInt16(y-m421StartY); d > 2 {
			t.Fatalf("joiner %d is at %d,%d, %d squares from the start; want the nearest free square", i+1, x, y, d)
		}
		if !room.Engine.PlacementReachable(x, y) {
			t.Fatalf("joiner %d was placed at %d,%d, which they cannot walk off", i+1, x, y)
		}
	}
}

// Leaving does not move the start: a board that every player has left starts
// the next one at the same square, however far the last one walked.
func TestM421StartSurvivesTheBoardEmptying(t *testing.T) {
	rm := m421RoomManager(t)
	a := m421Join(t, rm)
	rm.rooms[m421Board].Engine.MoveStat(rm.players[a].statID, 11, 13)
	rm.LeavePlayer(a)

	b := m421Join(t, rm)
	if x, y := m421Pos(t, rm, b); x != m421StartX || y != m421StartY {
		t.Fatalf("after the board emptied the next joiner is at %d,%d, want %d,%d", x, y, m421StartX, m421StartY)
	}
	rm.rooms[m421Board].Engine.MoveStat(rm.players[b].statID, 20, 5)
	c := m421Join(t, rm)
	if x, y := m421Pos(t, rm, c); x != m421StartX || y != m421StartY {
		t.Fatalf("a joiner after a reopened board's first player walked off is at %d,%d, want %d,%d", x, y, m421StartX, m421StartY)
	}
}

// A .ZZT's stored start square (StartPlayerX/Y) is wherever its author's player
// last entered the board (vanilla writes it only in BoardEnter), so it need not
// be the player tile. Once every player has left, the frozen board has no player
// tile, and a start re-read from the reopened board would be that stale square.
// The designed start is the player tile, read once per loaded world.
func TestM421StaleStoredStartDoesNotTakeOverAfterTheBoardEmpties(t *testing.T) {
	rm := m421RoomManager(t)
	room := rm.ensureRoom(m421Board)
	room.Engine.Board.Info.StartPlayerX = 40
	room.Engine.Board.Info.StartPlayerY = 5

	a := m421Join(t, rm)
	if x, y := m421Pos(t, rm, a); x != m421StartX || y != m421StartY {
		t.Fatalf("the first joiner is at %d,%d, want the player tile %d,%d", x, y, m421StartX, m421StartY)
	}
	rm.LeavePlayer(a)
	if rm.rooms[m421Board] != nil {
		t.Fatal("the board did not freeze when its last player left")
	}

	b := m421Join(t, rm)
	if x, y := m421Pos(t, rm, b); x != m421StartX || y != m421StartY {
		t.Fatalf("after the board emptied the next joiner is at %d,%d, want the player tile %d,%d, not the stored 40,5", x, y, m421StartX, m421StartY)
	}
}
