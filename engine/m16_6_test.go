package zztgo

// M16.6 — the two ZZT-OOP surfaces the vanilla oracle cannot carry, plus a
// byte-level lock on the label rewrite the sweep had to fix.
//
// Everything else in this sweep is compared against the real ZZT.EXE through
// fixtures/oracle/{talk,walk,cond,morf}.scn. Two things cannot go there:
// #endgame, because it drives the player to 0 health and this fork does not
// have vanilla's game over (deviation `mp-respawn`, PARITY.md §4); and the
// exact byte #zap and #restore overwrite, which is only observable through its
// consequences on screen.

import (
	"strings"
	"testing"
)

// oopTestObject drops an object with the given program on the board and
// returns its stat id.
func oopTestObject(t *testing.T, e *Engine, x, y int16, program string) int16 {
	t.Helper()
	e.AddStat(x, y, E_OBJECT, 0x0F, 1, StatTemplateDefault)
	id := e.Board.StatCount
	st := &e.Board.Stats[id]
	st.Data = program
	st.DataLen = int16(len(program))
	st.DataPos = -1
	return id
}

// TestOopZapRestoreRewriteTheLabelColon pins which byte #zap and #restore
// overwrite. OOP.PAS:706-722 takes the data POINTER, advances it by
// labelDataPos+1 bytes and writes there — the second byte of the "\r:" match,
// i.e. the ':' itself. The Go port reached the same byte through Replace,
// which indexes 1-based, and so was one byte early: it overwrote the '\r' that
// begins the match.
//
// Overwriting the '\r' hides itself under #zap, because a mangled line
// terminator also stops "\r:LABEL" from matching. It only shows up under
// #restore, which then cannot find "\r'LABEL" — the apostrophe it is looking
// for ate the newline it wants in front of it. That is exactly how the oracle
// caught it: fixtures/oracle/talk.scn's ring2b never woke echo again.
func TestOopZapRestoreRewriteTheLabelColon(t *testing.T) {
	prevE := E
	defer func() { E = prevE }()
	E = NewEngine()
	e := E
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()

	const program = "@target\r#end\r:lab\r#char 65\r#end\r"
	target := oopTestObject(t, e, 10, 10, program)
	zapper := oopTestObject(t, e, 12, 10, "@zapper\r#zap target:lab\r#end\r")
	restorer := oopTestObject(t, e, 14, 10, "@restorer\r#restore target:lab\r#end\r")

	run := func(statId int16) {
		pos := int16(0)
		e.OopExecute(statId, &pos, "Interaction")
	}

	run(zapper)
	zapped := e.Board.Stats[target].Data
	if !strings.Contains(zapped, "\r'lab\r") {
		t.Fatalf("#zap did not turn \"\\r:lab\" into \"\\r'lab\": %q", zapped)
	}
	if strings.Count(zapped, "\r") != strings.Count(program, "\r") {
		t.Errorf("#zap changed the line structure (%d CRs, want %d): %q",
			strings.Count(zapped, "\r"), strings.Count(program, "\r"), zapped)
	}
	if e.OopSend(target, "lab", false) {
		t.Error("a zapped label still answered #send")
	}

	run(restorer)
	restored := e.Board.Stats[target].Data
	if restored != program {
		t.Fatalf("#restore did not put the program back:\n got %q\nwant %q", restored, program)
	}
	e.Board.Stats[target].DataPos = -1
	// OopIterateStat compares the target's @name against the lookup verbatim
	// (OOP.PAS), and the interpreter only ever hands it a word OopReadWord has
	// already upper-cased — so a hand-written send must be upper case too.
	if !e.OopSend(target, "TARGET:LAB", false) || e.Board.Stats[target].DataPos < 0 {
		t.Error("a restored label did not answer #send")
	}
}

// TestOopEndgameLeavesThePlayerInLimbo asserts M16.6a's fix: OOP.PAS:659
// `#endgame` sets the player's health to 0 and, before this task, nothing
// else. Vanilla's next ElementPlayerTick (ELEMENTS.PAS:1340) turns that into
// the game over: ' Game over  -  Press ESCAPE', TickTimeDuration 0, sound
// blocked, and the board stops. This fork replaced game over with a respawn
// (deviation `mp-respawn`, PARITY.md §4), so `#endgame` now routes through the
// same death path DamageStat's health-reaches-zero branch uses
// (Engine.killPlayer, NOTES.md M16.6a): score penalty, DeathEvent, and a
// RespawnTicks countdown that lands the player back at their entry point with
// full health — instead of the permanent limbo this test used to pin
// (Health=0, RespawnTicks=0, ElementPlayerTick's `Health <= 0` branch zeroing
// input forever).
func TestOopEndgameLeavesThePlayerInLimbo(t *testing.T) {
	prevE := E
	defer func() { E = prevE }()
	E = NewEngine()
	e := E
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})
	e.TickSpeed = 4
	e.TickTimeDuration = int16(e.TickSpeed) * 2

	p := e.PlayerFor(0)
	p.Score = RESPAWN_SCORE_PENALTY + 5
	e.SetReenterPoint(0, int16(e.Board.Stats[0].X), int16(e.Board.Stats[0].Y))

	ender := oopTestObject(t, e, 20, 10, "@ender\r#endgame\r#end\r")
	pos := int16(0)
	e.OopExecute(ender, &pos, "Interaction")

	if p.Health != 0 {
		t.Fatalf("#endgame left Health=%d, want 0 (OOP.PAS:659)", p.Health)
	}
	if p.RespawnTicks != RESPAWN_TICKS {
		t.Errorf("RespawnTicks=%d, want %d: #endgame must arm the same countdown "+
			"DamageStat's death branch does", p.RespawnTicks, RESPAWN_TICKS)
	}
	if p.Score != 5 {
		t.Errorf("Score=%d, want 5: #endgame must apply the same respawn score "+
			"penalty as any other death", p.Score)
	}
	found := false
	for _, ev := range e.Events {
		if d, ok := ev.(DeathEvent); ok {
			found = true
			if d.StatId != 0 {
				t.Errorf("DeathEvent.StatId=%d, want 0", d.StatId)
			}
		}
	}
	if !found {
		t.Error("#endgame did not emit a DeathEvent")
	}

	// The room must keep ticking — #endgame must not reintroduce vanilla's
	// single-player halt (GamePlayExitRequested would freeze the board for
	// every other player sharing it; GamePromptEndPlay's comment explains why).
	if e.TickTimeDuration == 0 || e.SoundBlockQueueing || e.GamePlayExitRequested {
		t.Errorf("engine took vanilla's halt (TickTimeDuration=%d, SoundBlockQueueing=%v, "+
			"GamePlayExitRequested=%v); a shared room must keep ticking",
			e.TickTimeDuration, e.SoundBlockQueueing, e.GamePlayExitRequested)
	}

	// A second #endgame on an already-dying player must not double the score
	// penalty, restart the countdown, or emit a second DeathEvent.
	e.Events = nil
	pos = int16(0)
	e.OopExecute(ender, &pos, "Interaction")
	if p.Score != 5 {
		t.Errorf("second #endgame changed Score to %d, want 5 unchanged", p.Score)
	}
	if p.RespawnTicks != RESPAWN_TICKS {
		t.Errorf("second #endgame changed RespawnTicks to %d, want %d unchanged",
			p.RespawnTicks, RESPAWN_TICKS)
	}
	for _, ev := range e.Events {
		if _, ok := ev.(DeathEvent); ok {
			t.Error("second #endgame on an already-dying player emitted another DeathEvent")
		}
	}

	// Tick through the countdown: the player must actually come back, exactly
	// like any other death.
	e.Events = nil
	e.CurrentTick = 1
	for i := 0; i < RESPAWN_TICKS+2; i++ {
		e.ElementPlayerTick(0)
	}
	if p.Health != 100 {
		t.Errorf("Health=%d after the respawn countdown, want 100", p.Health)
	}
	respawned := false
	for _, ev := range e.Events {
		if _, ok := ev.(RespawnEvent); ok {
			respawned = true
		}
	}
	if !respawned {
		t.Error("no RespawnEvent after the countdown expired")
	}
}

// TestOopEndgameIsolatesOtherPlayers is the multiplayer half of M16.6a's DoD:
// one player's #endgame must not touch another player sharing the room, and
// must not halt the board for them (GamePlayExitRequested is single-player-only
// in a room engine — see GamePromptEndPlay's comment).
func TestOopEndgameIsolatesOtherPlayers(t *testing.T) {
	prevE := E
	defer func() { E = prevE }()
	E = NewEngine()
	e := E
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})
	e.MultiRoom = true

	// Clear interior tiles and remove BoardCreate's default stat-0 player.
	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1

	e.Board.Info.StartPlayerX = 10
	e.Board.Info.StartPlayerY = 12
	p1 := e.SpawnPlayer()

	e.Board.Info.StartPlayerX = 40
	e.Board.Info.StartPlayerY = 12
	p2 := e.SpawnPlayer()

	e.PlayerFor(p2).Ammo = 7
	e.PlayerFor(p2).Gems = 3
	e.PlayerFor(p2).Score = 500
	e.PlayerFor(p2).Health = 100

	e.PlayerFor(p1).Score = 200

	// An object next to P1 (far from P2) resolves #endgame's NearestPlayer to P1.
	ender := oopTestObject(t, e, 11, 12, "@ender\r#endgame\r#end\r")
	pos := int16(0)
	e.OopExecute(ender, &pos, "Interaction")

	if e.PlayerFor(p1).Health != 0 {
		t.Errorf("P1.Health=%d after #endgame, want 0", e.PlayerFor(p1).Health)
	}
	if e.PlayerFor(p1).RespawnTicks != RESPAWN_TICKS {
		t.Errorf("P1.RespawnTicks=%d, want %d", e.PlayerFor(p1).RespawnTicks, RESPAWN_TICKS)
	}
	if e.PlayerFor(p1).Score != 100 {
		t.Errorf("P1.Score=%d after #endgame, want 100 (200 - %d penalty)",
			e.PlayerFor(p1).Score, RESPAWN_SCORE_PENALTY)
	}

	// P2 is untouched.
	if e.PlayerFor(p2).Ammo != 7 || e.PlayerFor(p2).Gems != 3 ||
		e.PlayerFor(p2).Score != 500 || e.PlayerFor(p2).Health != 100 ||
		e.PlayerFor(p2).RespawnTicks != 0 {
		t.Errorf("P2 changed by P1's #endgame: ammo=%d gems=%d score=%d health=%d respawnTicks=%d",
			e.PlayerFor(p2).Ammo, e.PlayerFor(p2).Gems, e.PlayerFor(p2).Score,
			e.PlayerFor(p2).Health, e.PlayerFor(p2).RespawnTicks)
	}

	// The room engine must not have taken the single-player halt.
	if e.GamePlayExitRequested {
		t.Error("#endgame set GamePlayExitRequested in a multi-room engine; " +
			"that halts GameStepWithInputs for every player sharing the board")
	}

	// Tick the room forward: P2 must keep acting normally while P1 counts down.
	e.CurrentTick = 1
	e.CurrentStatTicked = 0
	for step := 0; step < RESPAWN_TICKS+5; step++ {
		e.GameStepWithInputs(map[int16]PlayerInput{})
	}
	if e.PlayerFor(p1).Health != 100 {
		t.Errorf("P1.Health=%d after the respawn countdown, want 100", e.PlayerFor(p1).Health)
	}
	if e.PlayerFor(p2).Ammo != 7 || e.PlayerFor(p2).Gems != 3 ||
		e.PlayerFor(p2).Score != 500 || e.PlayerFor(p2).Health != 100 {
		t.Errorf("P2 changed after ticking through P1's respawn: ammo=%d gems=%d score=%d health=%d",
			e.PlayerFor(p2).Ammo, e.PlayerFor(p2).Gems, e.PlayerFor(p2).Score, e.PlayerFor(p2).Health)
	}
}
