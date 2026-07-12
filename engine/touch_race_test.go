package zztgo

import (
	"io/ioutil"
	"testing"
)

// TestTouchRaceBakery verifies that an unlocked object (P2 == 0) runs its
// `:touch` label and emits a ScrollEvent on the tick after `OopSend(TOUCH)`.
//
// The filename is historical: this test was originally written to chase a
// suspected `#end`/`:touch` tick race. That diagnosis was disproven — the real
// cause was ZWD-compiler stat-default garbage, since fixed and guarded by
// TestZWDObjectDefaultsAreZZTNeutral (zwd_test.go). There is no race here; the
// test simply checks the unlocked-object touch → scroll path end to end.
func TestTouchRaceBakery(t *testing.T) {
	// Read BAKERY.zwd
	src, err := ioutil.ReadFile("BAKERY.zwd")
	if err != nil {
		t.Fatalf("Failed to read BAKERY.zwd: %v", err)
	}

	// Compile it
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld failed: %v", err)
	}

	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(1) // Open "Town Plaza"

	// Find the townguide stat
	var townguideStatId int16 = -1
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		tile := e.Board.Tiles[stat.X][stat.Y]
		if tile.Element == E_OBJECT && stat.X == 13 && stat.Y == 18 {
			townguideStatId = i
			break
		}
	}

	if townguideStatId < 0 {
		t.Fatalf("Townguide stat not found")
	}

	stat := &e.Board.Stats[townguideStatId]
	if stat.P2 != 0 {
		t.Errorf("Expected townguide P2 to be 0 (unlocked), got %d", stat.P2)
	}

	// Simulate touch
	sent := e.OopSend(-townguideStatId, "TOUCH", false)
	if !sent {
		t.Fatalf("OopSend(TOUCH) failed")
	}

	if stat.DataPos <= 0 {
		t.Fatalf("Expected DataPos to be > 0 after touch, got %d", stat.DataPos)
	}

	// Tick the engine.
	// Townguide cycle is 3. Townguide stat ID is 2.
	// So we want CurrentTick % 3 == 2 % 3 (i.e. 2).
	e.CurrentTick = 2
	e.CurrentStatTicked = 0
	e.GameStepWithInputs(nil)

	// Since it executed the :touch label, it should have emitted the text window scroll events.
	if len(e.Events) == 0 {
		t.Fatalf("Expected scroll events to be emitted, got none")
	}

	// The last instruction should have set DataPos back to -1
	if stat.DataPos != -1 {
		t.Errorf("Expected DataPos to be -1 after execution, got %d", stat.DataPos)
	}
}
