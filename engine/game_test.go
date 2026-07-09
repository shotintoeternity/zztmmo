package main

import "testing"

// TestGameStepHeadlessLoop is the M0.5 definition of done: GameStep can be
// called in a loop with no terminal. It also pins the cycle-advance/wrap:
// starting at E.CurrentTick=1, exactly 420 advances return to 1 (the counter
// cycles 1..420), and one fewer lands on 420.
//
// The board is trivial (one stat with Cycle 0) so no TickProc fires — this
// isolates the loop/advance mechanics from full ElementDefs initialization,
// which the M0.6 TOWN.ZZT replay will exercise end to end.
func TestGameStepHeadlessLoop(t *testing.T) {
	E.Headless = true
	defer func() { E.Headless = false }()

	prevInput := E.ActiveInput
	SetInputSource(&ScriptedInput{}) // empty script => idle, never blocks
	defer SetInputSource(prevInput)

	E.Board.StatCount = 0
	E.Board.Stats[0].Cycle = 0 // stagger check short-circuits, no TickProc
	E.GamePlayExitRequested = false

	// 419 advances from 1 => 420.
	E.CurrentTick = 1
	E.CurrentStatTicked = E.Board.StatCount + 1 // first step ticks nothing, just advances
	for i := 0; i < 419; i++ {
		GameStep()
	}
	if E.CurrentTick != 420 {
		t.Errorf("after 419 advances from 1, E.CurrentTick=%d, want 420", E.CurrentTick)
	}

	// One more advance wraps 420 -> 1.
	GameStep()
	if E.CurrentTick != 1 {
		t.Errorf("advance past 420 should wrap to 1, got E.CurrentTick=%d", E.CurrentTick)
	}

	// Determinism: a fresh identical run reproduces the counter exactly.
	E.CurrentTick = 1
	E.CurrentStatTicked = E.Board.StatCount + 1
	for i := 0; i < 419; i++ {
		GameStep()
	}
	if E.CurrentTick != 420 {
		t.Errorf("second run diverged: E.CurrentTick=%d, want 420", E.CurrentTick)
	}
}

// TestGameStepStopsOnExitRequest verifies GameStep suppresses the cycle advance
// when E.GamePlayExitRequested is set, matching the original loop's exit guard.
func TestGameStepStopsOnExitRequest(t *testing.T) {
	E.Headless = true
	defer func() { E.Headless = false }()

	E.Board.StatCount = 0
	E.Board.Stats[0].Cycle = 0
	E.CurrentTick = 10
	E.CurrentStatTicked = E.Board.StatCount + 1
	E.GamePlayExitRequested = true
	defer func() { E.GamePlayExitRequested = false }()

	GameStep()
	if E.CurrentTick != 10 {
		t.Errorf("exit-requested GameStep must not advance; E.CurrentTick=%d, want 10", E.CurrentTick)
	}
}

func TestShootMaxShots(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()

	e.Board.Tiles[15][15] = TTile{Element: E_PLAYER, Color: 0x0F}
	e.Board.Stats[0].X = 15
	e.Board.Stats[0].Y = 15

	e.Board.Info.MaxShots = 2
	e.World.Info.Ammo = 10

	// First shot: shoot right
	s1 := e.BoardShoot(E_BULLET, 15, 15, 1, 0, SHOT_SOURCE_PLAYER)
	if !s1 {
		t.Fatalf("first shot failed")
	}

	bulletCount := int16(0)
	for i := int16(0); i <= e.Board.StatCount; i++ {
		if e.Board.Tiles[e.Board.Stats[i].X][e.Board.Stats[i].Y].Element == E_BULLET && e.Board.Stats[i].P1 == 0 {
			bulletCount++
		}
	}
	if bulletCount != 1 {
		t.Errorf("expected 1 bullet, got %d", bulletCount)
	}

	// Second shot: shoot down
	s2 := e.BoardShoot(E_BULLET, 15, 15, 0, 1, SHOT_SOURCE_PLAYER)
	if !s2 {
		t.Fatalf("second shot failed")
	}

	bulletCount = 0
	for i := int16(0); i <= e.Board.StatCount; i++ {
		if e.Board.Tiles[e.Board.Stats[i].X][e.Board.Stats[i].Y].Element == E_BULLET && e.Board.Stats[i].P1 == 0 {
			bulletCount++
		}
	}
	if bulletCount != 2 {
		t.Errorf("expected 2 bullets, got %d", bulletCount)
	}
}

func TestGemPickupSoundEvent(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()

	// Place player at (15, 15)
	e.Board.Tiles[15][15] = TTile{Element: E_PLAYER, Color: 0x0F}
	e.Board.Stats[0].X = 15
	e.Board.Stats[0].Y = 15

	// Place gem at (16, 15)
	e.Board.Tiles[16][15] = TTile{Element: E_GEM, Color: 0x0B}

	// Move player right onto the gem
	e.SetInputSource(&ScriptedInput{Ticks: []ScriptedTick{
		{DeltaX: 1, DeltaY: 0},
	}})
	e.InputUpdate()

	// Run step
	e.GameStep()

	// Verify player moved and collected gem
	if e.Board.Stats[0].X != 16 || e.Board.Stats[0].Y != 15 {
		t.Errorf("expected player at (16, 15), got (%d, %d)", e.Board.Stats[0].X, e.Board.Stats[0].Y)
	}
	if e.World.Info.Gems != 1 {
		t.Errorf("expected 1 gem, got %d", e.World.Info.Gems)
	}

	// Verify SoundEvent was emitted
	var soundEv *SoundEvent
	for _, ev := range e.Events {
		if s, ok := ev.(SoundEvent); ok {
			soundEv = &s
			break
		}
	}
	if soundEv == nil {
		t.Fatalf("expected SoundEvent to be emitted, got none in %v", e.Events)
	}
	if soundEv.Priority != 2 {
		t.Errorf("expected SoundEvent priority 2, got %d", soundEv.Priority)
	}
	if soundEv.Notes != "@\x017\x014\x010\x01" {
		t.Errorf("expected SoundEvent notes %q, got %q", "@\x017\x014\x010\x01", soundEv.Notes)
	}
}

