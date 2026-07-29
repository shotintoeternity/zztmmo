package zztgo

// M16.5 — the combat behaviors the creature/projectile sweep could not put in
// front of the vanilla oracle, and why.
//
// Everything else in the sweep is compared against the real ZZT.EXE through
// fixtures/oracle (beast, fire, ooze, pede, hunt). What remains here is the
// owner-approved deviation (PARITY.md §4 `mp-respawn`), of which an oracle
// capture would record a divergence rather than a parity, plus M16.5a's
// unit-level statement of the point-blank ownership rule the ORCLFIRE Blank Bay
// station now proves against vanilla end to end.

import "testing"

// TestPointBlankShotOwnership pins GAME.PAS:1246 BoardShoot's ownership rule:
// who a shot fired INTO an unwalkable square is allowed to damage.
//
//	ElementDefs[...].Destructible and ((Element = E_PLAYER) = Boolean(source))
//
// `source` is 0 for a player shot and non-zero for an enemy one, so `Boolean`
// reads as "the shooter is an enemy" and the test means: an enemy shot damages
// the player, a player shot damages anything but a player, and neither can
// damage its own side.
//
// M16.5 found this line translated as `== (source >= SHOT_SOURCE_PLAYER_BASE)` —
// "the shooter is a PLAYER", its negation — which inverted every point-blank
// outcome, and M16.5a restored it in the form BulletTick already carries
// (`Element = E_PLAYER or P1 = 0`, whose one extra case is the multiplayer
// friendly-fire deviation gated inside the branch and pinned by m8_1_test.go).
// The subtests below are the three consequences the inversion had, now asserted
// as vanilla resolves them; the fourth is the sharp one, and the reason the
// M16.5 sweep had to keep its tigers more than two columns from the player.
func TestPointBlankShotOwnership(t *testing.T) {
	setup := func() *Engine {
		e := NewEngine()
		e.Headless = true
		e.WorldCreate()
		e.BoardCreate()
		for x := int16(2); x < BOARD_WIDTH; x++ {
			for y := int16(2); y < BOARD_HEIGHT; y++ {
				e.Board.Tiles[x][y] = TTile{Element: E_EMPTY}
			}
		}
		e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
		e.Board.Stats[0].X, e.Board.Stats[0].Y = 10, 10
		e.Board.Tiles[10][10] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
		return e
	}

	// A player shot may damage anything Destructible that is not a player, so an
	// adjacent monster dies where it stands. ORCLFIRE's Blank Bay does this in
	// both axes against vanilla (checkpoints pb-east and pb-south).
	t.Run("player point-blanks a monster", func(t *testing.T) {
		e := setup()
		e.AddStat(11, 10, E_LION, int16(ElementDefs[E_LION].Color), ElementDefs[E_LION].Cycle, StatTemplateDefault)
		hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, SHOT_SOURCE_PLAYER_BASE)
		if !hit || e.Board.Tiles[11][10].Element == E_LION {
			t.Fatalf("point-blank on an adjacent lion reports hit=%v, tile=%d; want hit=true and the lion dead",
				hit, e.Board.Tiles[11][10].Element)
		}
	})

	// An enemy shot may damage the player, and costs the standard 10.
	t.Run("monster point-blanks the player", func(t *testing.T) {
		e := setup()
		e.AddStat(9, 10, E_TIGER, int16(ElementDefs[E_TIGER].Color), ElementDefs[E_TIGER].Cycle, StatTemplateDefault)
		hit := e.BoardShoot(E_BULLET, 9, 10, 1, 0, SHOT_SOURCE_ENEMY)
		if !hit || e.PlayerFor(0).Health != 90 {
			t.Fatalf("enemy point-blank reports hit=%v, health=%d; want hit=true and 90",
				hit, e.PlayerFor(0).Health)
		}
	})

	// The other half of the same rule: an enemy shot may damage ONLY the player,
	// so a creature standing in the way is not friendly fire — the shot simply
	// does not resolve, and the shooter's ammo-free volley is wasted.
	t.Run("enemy point-blank spares another creature", func(t *testing.T) {
		e := setup()
		e.AddStat(11, 10, E_LION, int16(ElementDefs[E_LION].Color), ElementDefs[E_LION].Cycle, StatTemplateDefault)
		hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, SHOT_SOURCE_ENEMY)
		if hit || e.Board.Tiles[11][10].Element != E_LION {
			t.Fatalf("enemy point-blank on a lion reports hit=%v, tile=%d; want hit=false and the lion alive",
				hit, e.Board.Tiles[11][10].Element)
		}
	})

	// The sharp consequence. ElementTigerTick tries its VERTICAL shot first
	// whenever `Difference(X, playerX) <= 2`, and for a tiger in the player's own
	// row that shot's delta is Signum(0) = 0: the tiger fires at its own square.
	// Vanilla refuses it (the tiger is not a player, and neither is its shooter)
	// and falls through to the horizontal shot. The inverted test accepted it and
	// the tiger blew itself up — which is why no M16.5 scenario could put a tiger
	// within two columns of the player. ORCLFIRE's Blank Bay now stands a spinning
	// gun, whose firing half is the same code, right next to one (pb-point-blank).
	t.Run("a tiger in the player's row does not shoot itself", func(t *testing.T) {
		e := setup()
		e.AddStat(12, 10, E_TIGER, int16(ElementDefs[E_TIGER].Color), ElementDefs[E_TIGER].Cycle, StatTemplateDefault)
		tigerStat := e.Board.StatCount
		e.Board.Stats[tigerStat].P1 = 9  // always seeks, never draws for its move
		e.Board.Stats[tigerStat].P2 = 27 // always fires: Random(10)*3 tops out at 27
		e.ElementTigerTick(tigerStat)
		if e.Board.Tiles[12][10].Element != E_TIGER {
			t.Fatalf("the tiger destroyed itself with its own zero-delta vertical shot (tile=%d); "+
				"vanilla refuses that shot and fires horizontally instead", e.Board.Tiles[12][10].Element)
		}
		// The refused vertical shot falls through to the horizontal one, which has
		// a walkable square to land in, so the tiger's turn ends with a bullet of
		// its own between it and the player — and that bullet then blocks the seek
		// step ElementLionTick would otherwise have taken.
		if e.Board.Tiles[11][10].Element != E_BULLET {
			t.Fatalf("no enemy bullet at 11,10 after the vertical shot was refused (tile=%d)",
				e.Board.Tiles[11][10].Element)
		}
		if statId := e.GetStatIdAt(11, 10); statId == -1 || int16(e.Board.Stats[statId].P1) != SHOT_SOURCE_ENEMY {
			t.Fatalf("the bullet at 11,10 is not enemy-owned (statId=%d)", statId)
		}
	})
}

// TestSinglePlayerDeathIsRespawnDeviation pins the one place where a
// single-player creature scenario cannot be compared against vanilla at all.
//
// Vanilla ends the game (ELEMENTS.PAS ElementPlayerTick): at Health <= 0 it
// displays ' Game over  -  Press ESCAPE' for 32000 ticks, sets
// TickTimeDuration to 0 and SoundBlockQueueing to true, and the board stops.
// This fork treats death as a respawn — deviation `mp-respawn` in PARITY.md §4,
// landed by M2.4/M4.3 — because a shared room cannot stop for one player.
//
// So no oracle scenario in this sweep is allowed to reach 0 health; every one of
// them ends with the player alive, and this test carries the death branch
// instead. The multiplayer half (score penalty, invulnerability, per-player
// isolation) is TestDeathRespawnInventoryIsolation and M16.12's business; what
// is asserted here is only that the SINGLE-player path diverges the same way,
// deliberately, and never reaches vanilla's terminal state.
func TestSinglePlayerDeathIsRespawnDeviation(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})
	e.TickSpeed = 4
	e.TickTimeDuration = int16(e.TickSpeed) * 2
	p := e.PlayerFor(0)
	p.Health = 10
	p.Score = RESPAWN_SCORE_PENALTY + 5

	e.DamageStat(0) // the last 10 health, from anything: a bite, a bullet, a star

	if p.Health > 0 {
		t.Fatalf("player survived a fatal DamageStat: health=%d", p.Health)
	}
	if p.RespawnTicks != RESPAWN_TICKS {
		t.Errorf("RespawnTicks=%d, want %d (death must start the respawn countdown, "+
			"not vanilla's game over)", p.RespawnTicks, RESPAWN_TICKS)
	}
	if p.Score != 5 {
		t.Errorf("Score=%d, want 5: the respawn penalty is the fork's substitute for "+
			"vanilla's terminal game over", p.Score)
	}
	if e.TickTimeDuration == 0 || e.SoundBlockQueueing {
		t.Errorf("engine took vanilla's halt (TickTimeDuration=%d, SoundBlockQueueing=%v); "+
			"the room must keep ticking for everyone else", e.TickTimeDuration, e.SoundBlockQueueing)
	}
	found := false
	for _, ev := range e.Events {
		if _, ok := ev.(DeathEvent); ok {
			found = true
		}
	}
	if !found {
		t.Error("no DeathEvent emitted; the client is told about death by event, not by a board message")
	}
}
