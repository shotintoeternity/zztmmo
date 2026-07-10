package zztgo

import (
	"bytes"
	"path/filepath"
	"testing"
)

// loadTitleWorld mirrors the server's startup order: WorldCreate populates the
// global ElementDefs (via InitElementsGame), without which no engine can tick.
func loadTitleWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()

	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q) failed", worldBase)
	}
	return setup.World
}

// The title board must animate: TOWN's board 0 pens centipedes, lions and
// tigers that mill about behind the menu.
func TestTitleSimAnimates(t *testing.T) {
	sim := NewTitleSim(loadTitleWorld(t))
	sub, cancel := sim.Subscribe()
	defer cancel()

	for i := 0; i < 60; i++ {
		sim.Tick()
	}
	cells := sub.Drain()
	if len(cells) == 0 {
		t.Fatal("title board produced no changed cells over 60 ticks; it is not animating")
	}
	for _, cell := range cells {
		if cell.X < 0 || cell.X >= BOARD_WIDTH {
			t.Fatalf("cell x=%d escaped the board columns; it would overwrite the client's sidebar", cell.X)
		}
	}
}

// With nobody watching, the sim must not advance: an idle title board is pure
// waste on the box this runs on.
func TestTitleSimIdlesWithoutSubscribers(t *testing.T) {
	sim := NewTitleSim(loadTitleWorld(t))
	before := sim.Screen()
	for i := 0; i < 60; i++ {
		sim.Tick()
	}
	after := sim.Screen()

	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("unwatched title sim advanced at cell %d,%d", after[i].X, after[i].Y)
		}
	}
}

// The premise of the whole design: ticking the title must not touch the world
// the rooms play in. If this fails, board 0's `#set` flags leak into live games.
func TestTitleSimDoesNotMutateSourceWorld(t *testing.T) {
	world := loadTitleWorld(t)
	boardBefore := append([]byte(nil), world.BoardData[0]...)
	infoBefore := world.Info

	sim := NewTitleSim(world)
	_, cancel := sim.Subscribe()
	defer cancel()
	for i := 0; i < 200; i++ {
		sim.Tick()
	}

	if !bytes.Equal(boardBefore, world.BoardData[0]) {
		t.Error("title sim mutated the source world's board 0 bytes")
	}
	if world.Info != infoBefore {
		t.Error("title sim mutated the source world's Info (flags, score, health)")
	}
}
