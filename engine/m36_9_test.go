package zztgo

import "testing"

// Task M36.9 — a player's tick survives a lower stat's removal.
//
// Vanilla's player was always stat 0, so nothing could ever be removed below
// it, and ElementPlayerTick (ELEMENTS.PAS:1315) could hold its stat across a
// touch proc. Here other players sit at higher indices, and two vanilla
// assumptions break:
//
//  (a) a touch proc that removes a lower stat (BoardAttack -> BoardDamageTile
//      -> RemoveStat) shifts the player down one index, but ElementPlayerTick
//      kept the old statId and stat pointer, so MoveStat moved a stale copy:
//      an E_PLAYER tile with no stat was left behind and the real player stood
//      on E_EMPTY. Tick procs are dispatched by tile element, so that player
//      never ticked again.
//  (b) BoardAttack (GAME.PAS:1233) decrements CurrentStatTicked for any
//      attacker above 0, compensating for vanilla removing a non-player
//      attacker. A player at index > 0 is not removed, so it ticked twice.

// m369Board is an open board with player A far away at stat 0, a lion at
// stat 1 and energized player B at stat 2 immediately west of the lion.
func m369Board(t *testing.T) (e *Engine, lionX, lionY int16) {
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

	e.Board.Info.StartPlayerX, e.Board.Info.StartPlayerY = 10, 5
	if a := e.SpawnPlayer(); a != 0 {
		t.Fatalf("player A is stat %d, want 0", a)
	}
	lionX, lionY = 31, 12
	e.AddStat(lionX, lionY, E_LION, int16(ElementDefs[E_LION].Color), 2, StatTemplateDefault)
	if e.Board.StatCount != 1 {
		t.Fatalf("lion is stat %d, want 1", e.Board.StatCount)
	}
	e.Board.Info.StartPlayerX, e.Board.Info.StartPlayerY = byte(lionX-1), byte(lionY)
	if b := e.SpawnPlayer(); b != 2 {
		t.Fatalf("player B is stat %d, want 2", b)
	}
	if bx, by := e.Board.Stats[2].X, e.Board.Stats[2].Y; int16(bx) != lionX-1 || int16(by) != lionY {
		t.Fatalf("player B spawned at (%d,%d), want (%d,%d)", bx, by, lionX-1, lionY)
	}
	e.PlayerFor(0).Health = 100
	e.PlayerFor(2).Health = 100
	e.PlayerFor(2).EnergizerTicks = 50
	e.GamePlayExitRequested = false
	return e, lionX, lionY
}

// assertEveryPlayerTileHasAStat fails on a phantom: an E_PLAYER tile that no
// stat stands on.
func assertEveryPlayerTileHasAStat(t *testing.T, e *Engine) {
	t.Helper()
	for ix := int16(0); ix <= BOARD_WIDTH+1; ix++ {
		for iy := int16(0); iy <= BOARD_HEIGHT+1; iy++ {
			if e.Board.Tiles[ix][iy].Element == E_PLAYER && e.GetStatIdAt(ix, iy) == -1 {
				t.Errorf("phantom player tile at (%d,%d): no stat stands on it", ix, iy)
			}
		}
	}
}

func TestM369PlayerSurvivesALowerStatsRemoval(t *testing.T) {
	e, lionX, lionY := m369Board(t)
	pB := e.Players[2]

	step(e, map[int16]PlayerInput{2: {DeltaX: 1}})

	if e.Board.StatCount != 1 {
		t.Fatalf("StatCount = %d, want 1: the energized player should have killed the lion", e.Board.StatCount)
	}
	if e.Players[1] != pB {
		t.Fatalf("player B's state is not at stat 1 after the lion (stat 1) was removed")
	}
	if x, y := int16(e.Board.Stats[1].X), int16(e.Board.Stats[1].Y); x != lionX || y != lionY {
		t.Errorf("player B (now stat 1) at (%d,%d), want the lion's old square (%d,%d)", x, y, lionX, lionY)
	}
	assertStatTile(t, e, 1, E_PLAYER, "player B")
	assertStatTile(t, e, 0, E_PLAYER, "player A")
	assertEveryPlayerTileHasAStat(t, e)

	// B must still tick: keyed by its new id (input keying is M36.10's bug).
	step(e, map[int16]PlayerInput{1: {DeltaX: 1}})
	if x := int16(e.Board.Stats[1].X); x != lionX+1 {
		t.Errorf("player B at x=%d after a second step east, want %d: it stopped ticking", x, lionX+1)
	}
	assertEveryPlayerTileHasAStat(t, e)
}

// The paused branch of GameStepWithInputs runs the touch proc for a paused
// player too, and held the same stale stat pointer.
func TestM369PausedPlayerSurvivesALowerStatsRemoval(t *testing.T) {
	e, lionX, lionY := m369Board(t)
	e.Players[2].Paused = true

	step(e, map[int16]PlayerInput{2: {DeltaX: 1}})

	if e.Board.StatCount != 1 {
		t.Fatalf("StatCount = %d, want 1: the energized player should have killed the lion", e.Board.StatCount)
	}
	if e.Players[1].Paused {
		t.Errorf("player B is still paused: the move should have unpaused it")
	}
	if x, y := int16(e.Board.Stats[1].X), int16(e.Board.Stats[1].Y); x != lionX || y != lionY {
		t.Errorf("player B (now stat 1) at (%d,%d), want the lion's old square (%d,%d)", x, y, lionX, lionY)
	}
	assertStatTile(t, e, 1, E_PLAYER, "player B")
	assertEveryPlayerTileHasAStat(t, e)
	// A successful unpause falls through to a no-input player tick, which
	// drains the energizer once. A stale stat pointer read the vacated square,
	// found no E_PLAYER there, and silently skipped that tick.
	if got := e.Players[1].EnergizerTicks; got != 49 {
		t.Errorf("EnergizerTicks = %d after the unpausing cycle, want 49: the fall-through tick did not run", got)
	}
}

// (b): an energized player at stat 1 kills a lion at stat 2. It must move one
// square and spend one energizer tick, not tick twice.
func TestM369PlayerAttackerAboveStatZeroTicksOnce(t *testing.T) {
	e, _, p2 := twoPlayerBoard(t)
	if p2 != 1 {
		t.Fatalf("p2 is stat %d, want 1", p2)
	}
	bx, by := int16(e.Board.Stats[p2].X), int16(e.Board.Stats[p2].Y)
	e.AddStat(bx+1, by, E_LION, int16(ElementDefs[E_LION].Color), 2, StatTemplateDefault)
	if e.Board.StatCount != 2 {
		t.Fatalf("lion is stat %d, want 2", e.Board.StatCount)
	}
	e.PlayerFor(p2).EnergizerTicks = 50

	step(e, map[int16]PlayerInput{p2: {DeltaX: 1}})

	if e.Board.StatCount != 1 {
		t.Fatalf("StatCount = %d, want 1: the lion should be dead", e.Board.StatCount)
	}
	if x := int16(e.Board.Stats[p2].X); x != bx+1 {
		t.Errorf("player at x=%d after one cycle, want %d: it ticked more than once", x, bx+1)
	}
	if got := e.PlayerFor(p2).EnergizerTicks; got != 49 {
		t.Errorf("EnergizerTicks = %d after one cycle, want 49: it ticked more than once", got)
	}
	assertEveryPlayerTileHasAStat(t, e)
}
