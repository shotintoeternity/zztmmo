package zztgo

import "testing"

// Task M36.12 — a key of any colour is safe to touch.
//
// ElementKeyTouch takes the key's colour from the foreground nibble mod 8 and
// indexed the zero-based Keys/ColorNames with key-1. A black (0x00) or
// dark-grey (0x08) key gives key 0, so a player walking into one panicked and
// the room manager dropped the board. Such a key comes from a world file or
// from `#put e key` onto a square whose colour is 0x08.

func m3612Board(t *testing.T, keyColor byte) (e *Engine, player int16) {
	t.Helper()
	e = NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})
	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1
	e.Board.Info.StartPlayerX, e.Board.Info.StartPlayerY = 20, 10
	if player = e.SpawnPlayer(); player != 0 {
		t.Fatalf("player is stat %d, want 0", player)
	}
	e.PlayerFor(player).Health = 100
	e.GamePlayExitRequested = false
	e.Board.Tiles[21][10] = TTile{Element: E_KEY, Color: keyColor}
	return e, player
}

func TestM3612AKeyWithNoKeyColourIsSafeToTouch(t *testing.T) {
	for _, color := range []byte{0x08, 0x00, 0x80, 0x78} {
		e, player := m3612Board(t, color)
		pState := e.PlayerFor(player)
		gems := pState.Gems
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("walking into a key of colour %#02x panicked: %v", color, r)
				}
			}()
			step(e, map[int16]PlayerInput{player: {DeltaX: 1}})
			// The board must keep ticking after the touch.
			step(e, map[int16]PlayerInput{})
		}()
		if got := e.Board.Tiles[21][10]; got.Element != E_KEY || got.Color != color {
			t.Errorf("colour %#02x: key square is now element %d colour %#02x, want the key left in place", color, got.Element, got.Color)
		}
		if x, y := e.Board.Stats[player].X, e.Board.Stats[player].Y; x != 20 || y != 10 {
			t.Errorf("colour %#02x: player at (%d,%d), want (20,10): a key is not walked through", color, x, y)
		}
		if pState.Keys != [7]bool{} {
			t.Errorf("colour %#02x: player holds keys %v, want none", color, pState.Keys)
		}
		if pState.Gems != gems {
			t.Errorf("colour %#02x: gems %d, want %d unchanged", color, pState.Gems, gems)
		}
	}
}

// The guard must not catch real key colours: 0x09 is a blue key.
func TestM3612AColouredKeyIsStillPickedUp(t *testing.T) {
	e, player := m3612Board(t, 0x09)
	step(e, map[int16]PlayerInput{player: {DeltaX: 1}})
	if !e.PlayerFor(player).Keys[0] {
		t.Fatalf("player did not pick up the blue key: keys %v", e.PlayerFor(player).Keys)
	}
	if got := e.Board.Tiles[21][10].Element; got == E_KEY {
		t.Errorf("blue key still on the board after pickup")
	}
}
