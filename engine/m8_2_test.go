package zztgo // unit: M8.2 sweep of single-player assumptions

// M8.2: ResetMessageNotShownFlags reset the one-shot hint flags for stat 0
// only, so in a multiplayer world a reset (e.g. a world create with players
// already tracked) left every other player's flags untouched. The fix resets
// every entry in e.Players. See NOTES.md (M8.2) for the classification table.

import "testing"

// TestResetMessageNotShownFlagsCoversAllPlayers seeds two players, dirties
// their hint flags, then asserts ResetMessageNotShownFlags clears them for
// both — not just stat 0.
func TestResetMessageNotShownFlagsCoversAllPlayers(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()

	// Two players with all their hint flags "already shown".
	p0 := e.PlayerFor(0)
	p1 := e.PlayerFor(3)
	for _, ps := range []*PlayerState{p0, p1} {
		ps.MessageAmmoNotShown = false
		ps.MessageTorchNotShown = false
		ps.MessageGemNotShown = false
		ps.MessageEnergizerNotShown = false
		ps.MessageForestNotShown = false
	}

	e.ResetMessageNotShownFlags()

	for statId, ps := range map[int16]*PlayerState{0: p0, 3: p1} {
		if !ps.MessageAmmoNotShown || !ps.MessageTorchNotShown ||
			!ps.MessageGemNotShown || !ps.MessageEnergizerNotShown ||
			!ps.MessageForestNotShown {
			t.Errorf("player %d: hint flags not reset (got Ammo=%v Torch=%v Gem=%v Energizer=%v Forest=%v)",
				statId, ps.MessageAmmoNotShown, ps.MessageTorchNotShown,
				ps.MessageGemNotShown, ps.MessageEnergizerNotShown, ps.MessageForestNotShown)
		}
	}
}

// TestResetMessageNotShownFlagsEnsuresPlayerZero preserves vanilla's guarantee
// that stat 0's state exists with flags set after a world create, even when no
// one has joined yet (the old PlayerFor(0) read created it lazily).
func TestResetMessageNotShownFlagsEnsuresPlayerZero(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	// Fresh engine: no players tracked yet.
	e.Players = nil

	e.ResetMessageNotShownFlags()

	if e.Players == nil {
		t.Fatal("ResetMessageNotShownFlags left e.Players nil")
	}
	p0 := e.Players[0]
	if p0 == nil {
		t.Fatal("ResetMessageNotShownFlags did not create player 0")
	}
	if !p0.MessageAmmoNotShown || !p0.MessageEnergizerNotShown {
		t.Errorf("player 0 flags not set after create: Ammo=%v Energizer=%v",
			p0.MessageAmmoNotShown, p0.MessageEnergizerNotShown)
	}
}
