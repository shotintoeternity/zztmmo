package zztgo // unit: M8.1 point-blank energizer + no-PvP

// M8.1: BoardShoot's point-blank damage branch must respect the *target*
// player's energizer (vanilla read the one player's World.Info.EnergizerTicks;
// the Go port read player 0's regardless of who stands in front of the
// shooter), and a player point-blanking a player must follow BulletTick's
// no-PvP ownership rule (M2.4). See NOTES.md (M8.1) for the PvP decision.

import "testing"

// pointBlankSetup builds a headless board with player A (stat 0) at (10,10) and
// a second player (stat B) on the square immediately to A's right, (11,10).
// Returns the engine and B's statId. A shoots right (deltaX=1) to point-blank B.
func pointBlankSetup(t *testing.T) (*Engine, int16) {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()

	// Player A = stat 0, the shooter.
	e.Board.Tiles[10][10] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	e.Board.Stats[0].X = 10
	e.Board.Stats[0].Y = 10

	// Player B = a second player standing on A's target square.
	e.AddStat(11, 10, E_PLAYER, int16(ElementDefs[E_PLAYER].Color), 1, StatTemplateDefault)
	bId := e.Board.StatCount
	e.Board.Tiles[11][10] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}

	return e, bId
}

// A shoots point-blank at an energized B: the target's energizer must protect
// B even when the shooter (player 0) is not energized. Under the old
// PlayerFor(0) read this damaged B; the fix reads B's own EnergizerTicks.
func TestPointBlankRespectsTargetEnergizer(t *testing.T) {
	e, bId := pointBlankSetup(t)
	e.FriendlyFire = true // isolate the energizer check from the no-PvP guard

	e.PlayerFor(0).EnergizerTicks = 0    // shooter NOT energized
	e.PlayerFor(bId).EnergizerTicks = 50 // target energized -> protected
	startHealth := e.PlayerFor(bId).Health

	hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, 0+SHOT_SOURCE_PLAYER_BASE)
	if hit {
		t.Errorf("BoardShoot should not resolve against an energized target; got hit=true")
	}
	if got := e.PlayerFor(bId).Health; got != startHealth {
		t.Errorf("energized target took damage: Health=%d, want %d", got, startHealth)
	}
}

// With FriendlyFire on, point-blanking an un-energized target damages it — and
// the target resolved is the player on the square, not player 0.
func TestPointBlankDamagesUnenergizedTargetWithFriendlyFire(t *testing.T) {
	e, bId := pointBlankSetup(t)
	e.FriendlyFire = true

	e.PlayerFor(0).EnergizerTicks = 50  // shooter energized (old bug would protect target)
	e.PlayerFor(bId).EnergizerTicks = 0 // target NOT energized -> vulnerable
	startHealth := e.PlayerFor(bId).Health

	hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, 0+SHOT_SOURCE_PLAYER_BASE)
	if !hit {
		t.Errorf("BoardShoot should resolve against an un-energized target with friendly fire; got hit=false")
	}
	if got := e.PlayerFor(bId).Health; got != startHealth-10 {
		t.Errorf("un-energized target not damaged: Health=%d, want %d", got, startHealth-10)
	}
}

// M8.1 PvP decision: point-blank follows the same no-PvP rule as BulletTick.
// With FriendlyFire off, a player point-blanking a player does no damage.
func TestPointBlankNoDamageWithoutFriendlyFire(t *testing.T) {
	e, bId := pointBlankSetup(t)
	e.FriendlyFire = false

	e.PlayerFor(bId).EnergizerTicks = 0 // vulnerable but for the no-PvP rule
	startHealth := e.PlayerFor(bId).Health

	hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, 0+SHOT_SOURCE_PLAYER_BASE)
	if hit {
		t.Errorf("no-PvP: point-blank should not resolve with friendly fire off; got hit=true")
	}
	if got := e.PlayerFor(bId).Health; got != startHealth {
		t.Errorf("no-PvP: target took damage with friendly fire off: Health=%d, want %d", got, startHealth)
	}
}

// A player never point-blanks themselves, mirroring BulletTick's self-shot
// guard: even with FriendlyFire on, a shot owned by the target's own statId
// does no damage.
func TestPointBlankNeverSelfDamage(t *testing.T) {
	e, bId := pointBlankSetup(t)
	e.FriendlyFire = true

	e.PlayerFor(bId).EnergizerTicks = 0
	startHealth := e.PlayerFor(bId).Health

	// Source encodes B as the owner: a self-shot against B's own square.
	hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, bId+SHOT_SOURCE_PLAYER_BASE)
	if hit {
		t.Errorf("self-shot should not resolve; got hit=true")
	}
	if got := e.PlayerFor(bId).Health; got != startHealth {
		t.Errorf("self-shot damaged the owner: Health=%d, want %d", got, startHealth)
	}
}

// A creature (enemy source) shooting an energized player on a nonzero stat does
// no damage — the target's energizer is read, not player 0's.
func TestPointBlankCreatureVsEnergizedPlayer(t *testing.T) {
	e, bId := pointBlankSetup(t)

	e.PlayerFor(0).EnergizerTicks = 0    // player 0 not energized (old bug source)
	e.PlayerFor(bId).EnergizerTicks = 50 // target energized
	startHealth := e.PlayerFor(bId).Health

	hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, SHOT_SOURCE_ENEMY)
	if hit {
		t.Errorf("creature should not damage an energized player point-blank; got hit=true")
	}
	if got := e.PlayerFor(bId).Health; got != startHealth {
		t.Errorf("energized player took creature damage: Health=%d, want %d", got, startHealth)
	}
}
