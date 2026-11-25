package zztgo

// M42.2 (owner rule, 2026-11-26): a player always starts on the board's
// designed square unless something stands there. Then they start one square
// north, east, south or west, in that order, unless that is blocked too; then
// on a free square beside any of those, nearest first. The search never goes
// through a wall.

import (
	"testing"
)

// m422Board is an open board: walls round the edge, empty inside, no stats.
func m422Board(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1
	return e
}

const m422X, m422Y = int16(30), int16(12)

func m422Occupy(e *Engine, x, y int16) {
	e.Board.Tiles[x][y] = TTile{Element: E_EMPTY}
	e.AddStat(x, y, E_OBJECT, 0x0f, 1, StatTemplateDefault)
}

func m422Wall(e *Engine, x, y int16) {
	e.Board.Tiles[x][y] = TTile{Element: E_NORMAL, Color: 0x0e}
}

func m422Want(t *testing.T, e *Engine, wantX, wantY int16, why string) {
	t.Helper()
	x, y, ok := e.FindPlacement(m422X, m422Y, -1)
	if !ok || x != wantX || y != wantY {
		t.Fatalf("%s: placed at %d,%d (ok=%v), want %d,%d", why, x, y, ok, wantX, wantY)
	}
}

func TestM422TheStartWhenItIsFree(t *testing.T) {
	m422Want(t, m422Board(t), m422X, m422Y, "a free start")
}

func TestM422NorthEastSouthWestInThatOrder(t *testing.T) {
	e := m422Board(t)
	m422Occupy(e, m422X, m422Y)
	m422Want(t, e, m422X, m422Y-1, "start taken: north")

	m422Wall(e, m422X, m422Y-1)
	m422Want(t, e, m422X+1, m422Y, "north blocked: east")

	m422Occupy(e, m422X+1, m422Y)
	m422Want(t, e, m422X, m422Y+1, "north and east blocked: south")

	m422Wall(e, m422X, m422Y+1)
	m422Want(t, e, m422X-1, m422Y, "north, east and south blocked: west")
}

// Never a diagonal while a side square is free: the ring search this replaced
// took the north-west diagonal first.
func TestM422NoDiagonalBeforeASide(t *testing.T) {
	e := m422Board(t)
	m422Occupy(e, m422X, m422Y)
	x, y, _ := e.FindPlacement(m422X, m422Y, -1)
	if x != m422X && y != m422Y {
		t.Fatalf("placed diagonally at %d,%d", x, y)
	}
}

// All four sides taken by other players or objects: a free square beside one
// of them, starting from north's.
func TestM422AllFourTakenUsesASquareBesideThem(t *testing.T) {
	e := m422Board(t)
	m422Occupy(e, m422X, m422Y)
	for dir := 0; dir < 4; dir++ {
		m422Occupy(e, m422X+placementSearchDX[dir], m422Y+placementSearchDY[dir])
	}
	m422Want(t, e, m422X, m422Y-2, "all four taken: beyond north")

	// North's far side is a wall: north's east neighbour is next.
	m422Wall(e, m422X, m422Y-2)
	m422Want(t, e, m422X+1, m422Y-1, "beyond north walled: north's east side")
}

// Walls on all four sides are not crossed: the arrival walks the open
// corridor instead of appearing on the far side of a wall.
func TestM422TheSearchNeverGoesThroughAWall(t *testing.T) {
	e := m422Board(t)
	m422Occupy(e, m422X, m422Y)
	// Wall in N, S, W; a corridor runs east, crowded for three squares.
	m422Wall(e, m422X, m422Y-1)
	m422Wall(e, m422X, m422Y+1)
	m422Wall(e, m422X-1, m422Y)
	for i := int16(1); i <= 3; i++ {
		m422Occupy(e, m422X+i, m422Y)
		m422Wall(e, m422X+i, m422Y-1)
		m422Wall(e, m422X+i, m422Y+1)
	}
	// The square just north of the wall north of the start is free and two
	// steps away as the crow flies; it must not be chosen.
	m422Want(t, e, m422X+4, m422Y, "walled in: along the corridor")
}

// Through the room: joiners arrive start, north, east, south, west.
func TestM422JoinersFillTheStartThenItsSides(t *testing.T) {
	rm := m421RoomManager(t)
	room := func() *Room { return rm.rooms[m421Board] }
	want := [][2]int16{{m421StartX, m421StartY}}
	first := m421Join(t, rm)
	e := room().Engine
	for dir := 0; dir < 4; dir++ {
		x, y := m421StartX+placementSearchDX[dir], m421StartY+placementSearchDY[dir]
		if e.PlacementOpen(x, y, -1) && e.PlacementReachable(x, y) {
			want = append(want, [2]int16{x, y})
		}
	}
	ids := []PlayerID{first}
	for len(ids) < len(want) {
		ids = append(ids, m421Join(t, rm))
	}
	for i, id := range ids {
		x, y := m421Pos(t, rm, id)
		if x != want[i][0] || y != want[i][1] {
			t.Fatalf("joiner %d is at %d,%d, want %d,%d (start, then its free sides north, east, south, west)", i+1, x, y, want[i][0], want[i][1])
		}
	}
	if len(want) < 3 {
		t.Fatalf("CONTROL's start has only %d free sides; the test needs a busier check", len(want)-1)
	}
}
