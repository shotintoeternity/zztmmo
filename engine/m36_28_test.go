package zztgo

import (
	"os"
	"testing"
)

// Task M36.28 — a world tile above the last element does not drop the room.
//
// ElementDefs held MAX_ELEMENT+1 entries, so drawing, touching or testing a
// tile whose element byte is above 53 panicked and stepRoom dropped the room.
// Seven community worlds carry such tiles: DOG! uses elements 61 and 69 as
// extra-colour text, and six others carry one stray 246.
//
// What vanilla does was observed, not inferred: the real ZZT.EXE under the
// oracle (NOTES.md 2026-11-02, M36.28) draws them through BoardDrawTile's text
// branch, char = the tile's colour byte, attribute = byte(((e-47)+1)*16+$F),
// and plays on normally until something touches one.

const m3628Board = int16(1)

// m3628Tiles are the out-of-range tiles placed on the synthesized board, with
// the attribute the oracle (246, 61) or the same formula (69) gives each.
var m3628Tiles = []struct {
	x, y    int16
	element byte
	color   byte
	attr    byte
}{
	{31, 13, 246, 0x41, 0x8F},
	{33, 10, 61, 0x42, 0xFF},
	{34, 10, 69, 0x43, 0x7F},
}

func m3628World(t *testing.T) TWorld {
	t.Helper()
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
	for _, tile := range m3628Tiles {
		setup.Board.Tiles[tile.x][tile.y] = TTile{Element: tile.element, Color: tile.color}
	}
	setup.Board.Name = "Out of range"
	setup.World.Info.CurrentBoard = m3628Board
	setup.BoardClose()
	setup.World.BoardCount = 1
	return setup.World
}

func m3628NoPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// The room opens, draws each tile as vanilla does, and keeps stepping; a
// player who walks into one is stopped by it and the tile stays.
func TestM3628AnOutOfRangeTileDrawsAndTheRoomSurvives(t *testing.T) {
	rm := NewRoomManagerForWorld(m3628World(t), "M3628")
	var player PlayerID
	m3628NoPanic(t, "joining the board", func() { player = rm.JoinPlayer(m3628Board, 30, 13) })
	room, ok := rm.Room(m3628Board)
	if !ok {
		t.Fatal("no room for the board")
	}

	for _, tile := range m3628Tiles {
		var attr, ch byte
		m3628NoPanic(t, "drawing the tile", func() { attr, ch = room.Engine.TileToColorAndChar(tile.x, tile.y) })
		if attr != tile.attr || ch != tile.color {
			t.Errorf("element %d draws as char %#02x attr %#02x, want char %#02x attr %#02x (vanilla's text branch)",
				tile.element, ch, attr, tile.color, tile.attr)
		}
	}

	for i := 0; i < 6; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{player: {DeltaX: 1, Key: KEY_RIGHT}})
		if _, ok := rm.Room(m3628Board); !ok {
			t.Fatalf("the room was dropped on step %d", i)
		}
	}
	room, _ = rm.Room(m3628Board)
	if board, statID, ok := rm.PlayerLocation(player); !ok || board != m3628Board {
		t.Fatalf("player location board %d ok=%v, want board %d", board, ok, m3628Board)
	} else if x, y := room.Engine.Board.Stats[statID].X, room.Engine.Board.Stats[statID].Y; x != 30 || y != 13 {
		t.Errorf("player at (%d,%d), want (30,13): the 246 tile east of it should stop it", x, y)
	}
	for _, tile := range m3628Tiles {
		if got := room.Engine.Board.Tiles[tile.x][tile.y]; got.Element != tile.element || got.Color != tile.color {
			t.Errorf("tile at (%d,%d) is now element %d colour %#02x, want %d %#02x",
				tile.x, tile.y, got.Element, got.Color, tile.element, tile.color)
		}
	}
}

// m3628Worlds are the community worlds known to carry such tiles. They are
// gitignored, so a clone without them skips this half.
var m3628Worlds = []string{"DOG!", "ESPFILE2", "ESPFILE4", "FROST1", "PR0N4U", "ZFILES1", "ZFILES2"}

func TestM3628TheCommunityWorldsWithSuchTilesDrawEveryBoard(t *testing.T) {
	for _, name := range m3628Worlds {
		t.Run(name, func(t *testing.T) {
			if _, err := os.Stat(name + ".ZZT"); err != nil {
				t.Skipf("%s.ZZT is not in this checkout (community worlds are gitignored)", name)
			}
			e := NewEngine()
			e.Headless = true
			e.WorldCreate()
			if !e.WorldLoad(name, ".ZZT", false) {
				t.Fatalf("%s did not load", name)
			}
			seen := 0
			for b := int16(0); b <= e.World.BoardCount; b++ {
				e.BoardChange(b)
				outOfRange := false
				for x := int16(0); x <= BOARD_WIDTH+1; x++ {
					for y := int16(0); y <= BOARD_HEIGHT+1; y++ {
						if e.Board.Tiles[x][y].Element > MAX_ELEMENT {
							outOfRange = true
						}
						m3628NoPanic(t, "drawing a tile", func() { e.TileToColorAndChar(x, y) })
					}
				}
				if !outOfRange {
					continue
				}
				seen++
				// Enter it as a room and step it, as a player arriving would.
				e.BoardClose()
				rm := NewRoomManagerForWorld(e.World, name)
				m3628NoPanic(t, "joining the board", func() {
					player := rm.JoinPlayer(b, int16(e.Board.Info.StartPlayerX), int16(e.Board.Info.StartPlayerY))
					for i := 0; i < 4; i++ {
						rm.StepDiffs(map[PlayerID]PlayerInput{player: {}})
					}
				})
				if _, ok := rm.Room(b); !ok {
					t.Errorf("board %d's room was dropped", b)
				}
			}
			if seen == 0 {
				t.Errorf("%s has no out-of-range tile; the list is stale", name)
			}
		})
	}
}
