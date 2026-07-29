package zztgo

// M16.5 — the two combat behaviors the creature/projectile sweep could not put
// in front of the vanilla oracle, and why.
//
// Everything else in the sweep is compared against the real ZZT.EXE through
// fixtures/oracle (beast, fire, ooze, pede, hunt). These two are here because
// one is a defect this sweep found (filed as gap task M16.5a) and the other is
// an owner-approved deviation (PARITY.md §4 `mp-respawn`); an oracle capture of
// either would record a divergence, not a parity.

import "testing"

// TestPointBlankShotOwnershipGap is the minimal repro for the M16.5 finding,
// filed as gap task M16.5a.
//
// GAME.PAS:1246 BoardShoot decides whether a shot fired INTO an unwalkable
// square damages what is standing there:
//
//	ElementDefs[...].Destructible and ((Element = E_PLAYER) = Boolean(source))
//
// `source` is 0 for a player shot and non-zero for an enemy one, so `Boolean`
// reads as "the shooter is an enemy" and the test means: an enemy shot damages
// the player, a player shot damages anything else, and neither can damage its
// own side. The fork re-encoded source as statId + SHOT_SOURCE_PLAYER_BASE and
// translated the same line as `== (source >= SHOT_SOURCE_PLAYER_BASE)`, which
// is "the shooter is a PLAYER" — the negation. Every point-blank outcome is
// therefore inverted, in both directions:
//
//   - a player cannot kill an adjacent monster by shooting it (vanilla can);
//   - an adjacent monster cannot hurt the player by shooting it (vanilla can);
//   - an enemy shot that lands on another creature damages it (vanilla refuses).
//
// The third has a sharp consequence worth stating, because it is what keeps the
// oracle scenarios away from close-quarters tigers: ElementTigerTick tries its
// VERTICAL shot first whenever `Difference(X, playerX) <= 2`, and for a tiger
// standing in the player's own row that shot's delta is Signum(0) = 0 — the
// tiger fires at its own square. Vanilla refuses that shot and falls through to
// the horizontal one; the fork's inverted test accepts it, so the tiger blows
// itself up.
//
// This test pins the CURRENT (defective) behavior so the gap cannot quietly
// change shape before M16.5a lands. M16.5a flips the condition, replaces the
// assertions below with the vanilla ones, and adds the oracle station this
// sweep could not.
func TestPointBlankShotOwnershipGap(t *testing.T) {
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

	t.Run("player point-blanks a monster", func(t *testing.T) {
		e := setup()
		e.AddStat(11, 10, E_LION, int16(ElementDefs[E_LION].Color), ElementDefs[E_LION].Cycle, StatTemplateDefault)
		hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, SHOT_SOURCE_PLAYER_BASE)
		if hit || e.Board.Tiles[11][10].Element != E_LION {
			t.Fatalf("GAP CLOSED? point-blank on an adjacent lion now reports hit=%v, tile=%d "+
				"(vanilla kills it; this repro tracks the M16.5a defect)", hit, e.Board.Tiles[11][10].Element)
		}
	})

	t.Run("monster point-blanks the player", func(t *testing.T) {
		e := setup()
		e.AddStat(9, 10, E_TIGER, int16(ElementDefs[E_TIGER].Color), ElementDefs[E_TIGER].Cycle, StatTemplateDefault)
		hit := e.BoardShoot(E_BULLET, 9, 10, 1, 0, SHOT_SOURCE_ENEMY)
		if hit || e.PlayerFor(0).Health != 100 {
			t.Fatalf("GAP CLOSED? enemy point-blank now reports hit=%v, health=%d "+
				"(vanilla costs the player 10; this repro tracks the M16.5a defect)", hit, e.PlayerFor(0).Health)
		}
	})

	t.Run("a tiger in the player's row shoots itself", func(t *testing.T) {
		e := setup()
		e.AddStat(12, 10, E_TIGER, int16(ElementDefs[E_TIGER].Color), ElementDefs[E_TIGER].Cycle, StatTemplateDefault)
		tigerStat := e.Board.StatCount
		e.Board.Stats[tigerStat].P1 = 9  // always seeks, never draws for its move
		e.Board.Stats[tigerStat].P2 = 27 // always fires: Random(10)*3 tops out at 27
		e.ElementTigerTick(tigerStat)
		if e.Board.Tiles[12][10].Element == E_TIGER {
			t.Fatalf("GAP CLOSED? the tiger survived its own zero-delta vertical shot " +
				"(vanilla refuses it and fires horizontally instead; M16.5a)")
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
