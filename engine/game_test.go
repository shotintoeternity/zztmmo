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
