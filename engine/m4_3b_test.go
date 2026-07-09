package zztgo

import "testing"

// M4.3b — player-on-player collision and push-out.
//
// The board holds one tile per square, and GameStepWithInputs dispatches a
// stat's tick proc by reading the element of the tile that stat stands on
// (game.go). So two stats sharing a square is not a cosmetic overlap: whoever
// loses the square starts ticking as whatever the winner's tile says. A lion
// re-entered upon stops being a lion; a player overwritten by another player
// stops being controllable.
//
// Every test below asserts the same invariant: no two stats share a square, and
// every stat stands on a tile that describes it.

// assertNoStatOverlap fails if any two live stats occupy the same square.
func assertNoStatOverlap(t *testing.T, e *Engine) {
	t.Helper()
	seen := map[[2]byte]int16{}
	for id := int16(0); id <= e.Board.StatCount; id++ {
		stat := e.Board.Stats[id]
		key := [2]byte{stat.X, stat.Y}
		if other, dup := seen[key]; dup {
			t.Errorf("stats %d and %d both stand on (%d,%d)", other, id, stat.X, stat.Y)
		}
		seen[key] = id
	}
}

// assertStatTile fails if statId is not standing on a tile of element want.
func assertStatTile(t *testing.T, e *Engine, statId int16, want byte, who string) {
	t.Helper()
	stat := e.Board.Stats[statId]
	if got := e.Board.Tiles[stat.X][stat.Y].Element; got != want {
		t.Errorf("%s (stat %d) at (%d,%d) stands on element %d, want %d",
			who, statId, stat.X, stat.Y, got, want)
	}
}

// TestM43bTwoPlayersReenterSameSquare: both players share an entry square (which
// happens whenever one joins where another entered) and are zapped on a
// ReenterWhenZapped board. The first arrival keeps the square; the second is
// pushed to open ground rather than overwriting them.
func TestM43bTwoPlayersReenterSameSquare(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)
	e.Board.Info.ReenterWhenZapped = true

	// One shared entry square, on open ground.
	const reX, reY = int16(20), int16(12)
	e.Board.Tiles[reX][reY] = TTile{Element: E_EMPTY}
	e.SetReenterPoint(p1, reX, reY)
	e.SetReenterPoint(p2, reX, reY)

	e.DamageStat(p1)
	e.DamageStat(p2)

	if int16(e.Board.Stats[p1].X) != reX || int16(e.Board.Stats[p1].Y) != reY {
		t.Errorf("p1 at (%d,%d), want the shared entry square (%d,%d): the first "+
			"arrival keeps it", e.Board.Stats[p1].X, e.Board.Stats[p1].Y, reX, reY)
	}
	// The ring search scans dy then dx from -radius, so the first open square at
	// radius 1 is the up-left diagonal. Asserted exactly, to pin the policy.
	if int16(e.Board.Stats[p2].X) != reX-1 || int16(e.Board.Stats[p2].Y) != reY-1 {
		t.Errorf("p2 pushed to (%d,%d), want (%d,%d): the nearest open square",
			e.Board.Stats[p2].X, e.Board.Stats[p2].Y, reX-1, reY-1)
	}

	assertNoStatOverlap(t, e)
	assertStatTile(t, e, p1, E_PLAYER, "p1")
	assertStatTile(t, e, p2, E_PLAYER, "p2")

	// Both must still tick. Re-entry pauses each player; a movement input resumes
	// play and moves them on the same tick. Move them apart so neither blocks the
	// other, then check both actually went somewhere.
	p1x, p1y := e.Board.Stats[p1].X, e.Board.Stats[p1].Y
	p2x, p2y := e.Board.Stats[p2].X, e.Board.Stats[p2].Y
	step(e, map[int16]PlayerInput{p1: {DeltaX: 1}, p2: {DeltaX: -1}})

	if e.Board.Stats[p1].X == p1x && e.Board.Stats[p1].Y == p1y {
		t.Errorf("p1 never moved from (%d,%d): it stopped ticking", p1x, p1y)
	}
	if e.Board.Stats[p2].X == p2x && e.Board.Stats[p2].Y == p2y {
		t.Errorf("p2 never moved from (%d,%d): it stopped ticking", p2x, p2y)
	}
	assertNoStatOverlap(t, e)
}

// TestM43bTwoPlayersRespawnSameSquare: the death-respawn path (a fork invention,
// M2.4) had no occupancy check at all — it assigned the destination tile
// wholesale. Two players dying with a shared entry square must not stack.
func TestM43bTwoPlayersRespawnSameSquare(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)

	const reX, reY = int16(20), int16(12)
	e.Board.Tiles[reX][reY] = TTile{Element: E_EMPTY}
	e.SetReenterPoint(p1, reX, reY)
	e.SetReenterPoint(p2, reX, reY)

	// One hit from death each, so DamageStat takes the death branch.
	e.PlayerFor(p1).Health = 10
	e.PlayerFor(p2).Health = 10
	e.DamageStat(p1)
	e.DamageStat(p2)

	if e.PlayerFor(p1).RespawnTicks == 0 || e.PlayerFor(p2).RespawnTicks == 0 {
		t.Fatalf("precondition: both players should be counting down to respawn")
	}

	for i := 0; i < RESPAWN_TICKS+5; i++ {
		step(e, map[int16]PlayerInput{})
		if e.PlayerFor(p1).RespawnTicks == 0 && e.PlayerFor(p2).RespawnTicks == 0 {
			break
		}
	}

	if e.PlayerFor(p1).Health != 100 || e.PlayerFor(p2).Health != 100 {
		t.Fatalf("both players should have respawned: p1.Health=%d p2.Health=%d",
			e.PlayerFor(p1).Health, e.PlayerFor(p2).Health)
	}

	assertNoStatOverlap(t, e)
	assertStatTile(t, e, p1, E_PLAYER, "p1")
	assertStatTile(t, e, p2, E_PLAYER, "p2")
}

// TestM43bReenterOntoMonsterKeepsMonster: DamageStat used to stamp E_PLAYER over
// the entry square and save only the *tile* into stat.Under. A lion standing
// there kept its stat but lost its tile, so it began dispatching through
// ElementPlayerTick — it stopped being a lion.
func TestM43bReenterOntoMonsterKeepsMonster(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	e.Board.Info.ReenterWhenZapped = true

	// A lion occupies p1's entry square.
	const lionX, lionY = int16(20), int16(12)
	e.Board.Tiles[lionX][lionY] = TTile{Element: E_EMPTY}
	e.AddStat(lionX, lionY, E_LION, int16(ElementDefs[E_LION].Color), 2, StatTemplateDefault)
	lion := e.Board.StatCount
	e.SetReenterPoint(p1, lionX, lionY)

	if e.Board.Tiles[lionX][lionY].Element != E_LION {
		t.Fatalf("precondition: lion should be on (%d,%d)", lionX, lionY)
	}

	e.DamageStat(p1)

	// The lion keeps its square, its tile, and its stat.
	if int16(e.Board.Stats[lion].X) != lionX || int16(e.Board.Stats[lion].Y) != lionY {
		t.Errorf("lion moved to (%d,%d); the incumbent should never be displaced",
			e.Board.Stats[lion].X, e.Board.Stats[lion].Y)
	}
	if got := e.Board.Tiles[lionX][lionY].Element; got != E_LION {
		t.Errorf("tile at (%d,%d) is element %d, want E_LION (%d): the player "+
			"overwrote the lion and it will now tick as a player",
			lionX, lionY, got, E_LION)
	}
	// The player went somewhere else, and it is not inside the lion.
	if int16(e.Board.Stats[p1].X) == lionX && int16(e.Board.Stats[p1].Y) == lionY {
		t.Errorf("p1 re-entered on top of the lion")
	}
	assertNoStatOverlap(t, e)
	assertStatTile(t, e, p1, E_PLAYER, "p1")
	assertStatTile(t, e, lion, E_LION, "lion")

	// And the lion still ticks as a lion, rather than as a player.
	step(e, map[int16]PlayerInput{})
	assertStatTile(t, e, lion, E_LION, "lion")
	assertNoStatOverlap(t, e)
}

// TestM43bNoOpenSquareStaysPut: with the entry square taken and every other
// interior square walled, the re-entering player must stay where they are — the
// one place guaranteed open, because their own tile was just cleared. Never an
// overlap, even when there is nowhere to go.
func TestM43bNoOpenSquareStaysPut(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)
	e.Board.Info.ReenterWhenZapped = true

	// Park p2 next to p1, then wall off the whole interior around them.
	e.Board.Tiles[e.Board.Stats[p2].X][e.Board.Stats[p2].Y] = TTile{Element: E_EMPTY}
	p1x, p1y := int16(e.Board.Stats[p1].X), int16(e.Board.Stats[p1].Y)
	p2x, p2y := p1x+1, p1y

	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_NORMAL, Color: ElementDefs[E_NORMAL].Color}
		}
	}
	e.Board.Stats[p2].X, e.Board.Stats[p2].Y = byte(p2x), byte(p2y)
	e.Board.Tiles[p1x][p1y] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	e.Board.Tiles[p2x][p2y] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}

	// p1's entry square is the one p2 is standing on.
	e.SetReenterPoint(p1, p2x, p2y)

	e.DamageStat(p1)

	if int16(e.Board.Stats[p1].X) != p1x || int16(e.Board.Stats[p1].Y) != p1y {
		t.Errorf("p1 at (%d,%d), want to stay put at (%d,%d): every other square "+
			"is a wall", e.Board.Stats[p1].X, e.Board.Stats[p1].Y, p1x, p1y)
	}
	if int16(e.Board.Stats[p2].X) != p2x || int16(e.Board.Stats[p2].Y) != p2y {
		t.Errorf("p2 was displaced to (%d,%d)", e.Board.Stats[p2].X, e.Board.Stats[p2].Y)
	}
	assertNoStatOverlap(t, e)
	assertStatTile(t, e, p1, E_PLAYER, "p1")
	assertStatTile(t, e, p2, E_PLAYER, "p2")
}
