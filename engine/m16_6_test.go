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

// TestOopEndgameLeavesThePlayerInLimbo is a GAP test: it pins what the fork
// does today so the defect cannot drift, and gap task M16.6a rewrites it to
// assert the fixed behaviour.
//
// OOP.PAS:659 `#endgame` sets the player's health to 0 and nothing else.
// Vanilla's next ElementPlayerTick (ELEMENTS.PAS:1340) turns that into the
// game over: ' Game over  -  Press ESCAPE', TickTimeDuration 0, sound blocked,
// and the board stops. This fork replaced game over with a respawn (deviation
// `mp-respawn`), but the respawn is armed by DamageStat, which #endgame never
// calls — so a player an object ends the game on gets NEITHER. Health sits at
// 0, RespawnTicks is never set, and ElementPlayerTick's `Health <= 0` branch
// zeroes their input and returns, every tick, forever. In a shared room that
// is a permanently bricked player, and `#endgame` is how ZZT worlds have
// always written a losing ending.
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

	ender := oopTestObject(t, e, 20, 10, "@ender\r#endgame\r#end\r")
	pos := int16(0)
	e.OopExecute(ender, &pos, "Interaction")

	p := e.PlayerFor(0)
	if p.Health != 0 {
		t.Fatalf("#endgame left Health=%d, want 0 (OOP.PAS:659)", p.Health)
	}

	// Vanilla's terminal state, which the fork deliberately does not enter.
	e.Events = nil
	for i := 0; i < RESPAWN_TICKS*3; i++ {
		e.ElementPlayerTick(0)
	}
	if e.TickTimeDuration == 0 || e.SoundBlockQueueing {
		t.Errorf("engine took vanilla's halt (TickTimeDuration=%d, SoundBlockQueueing=%v); "+
			"a shared room must keep ticking", e.TickTimeDuration, e.SoundBlockQueueing)
	}

	// ...and the fork's own substitute, which it does not enter either.
	if p.RespawnTicks != 0 || p.Health != 0 {
		t.Fatalf("the limbo this test pins is gone (RespawnTicks=%d, Health=%d) — "+
			"M16.6a has landed; rewrite this test to assert the fixed behaviour",
			p.RespawnTicks, p.Health)
	}
	for _, ev := range e.Events {
		if _, ok := ev.(DeathEvent); ok {
			t.Fatal("#endgame now emits DeathEvent — M16.6a has landed; rewrite this test")
		}
		if _, ok := ev.(RespawnEvent); ok {
			t.Fatal("#endgame now respawns — M16.6a has landed; rewrite this test")
		}
	}
}
