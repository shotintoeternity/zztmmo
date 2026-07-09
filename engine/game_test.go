package main

import "testing"

// TestGameStepHeadlessLoop is the M0.5 definition of done: GameStep can be
// called in a loop with no terminal. It also pins the cycle-advance/wrap:
// starting at CurrentTick=1, exactly 420 advances return to 1 (the counter
// cycles 1..420), and one fewer lands on 420.
//
// The board is trivial (one stat with Cycle 0) so no TickProc fires — this
// isolates the loop/advance mechanics from full ElementDefs initialization,
// which the M0.6 TOWN.ZZT replay will exercise end to end.
func TestGameStepHeadlessLoop(t *testing.T) {
	Headless = true
	defer func() { Headless = false }()

	prevInput := activeInput
	SetInputSource(&ScriptedInput{}) // empty script => idle, never blocks
	defer SetInputSource(prevInput)

	Board.StatCount = 0
	Board.Stats[0].Cycle = 0 // stagger check short-circuits, no TickProc
	GamePlayExitRequested = false

	// 419 advances from 1 => 420.
	CurrentTick = 1
	CurrentStatTicked = Board.StatCount + 1 // first step ticks nothing, just advances
	for i := 0; i < 419; i++ {
		GameStep()
	}
	if CurrentTick != 420 {
		t.Errorf("after 419 advances from 1, CurrentTick=%d, want 420", CurrentTick)
	}

	// One more advance wraps 420 -> 1.
	GameStep()
	if CurrentTick != 1 {
		t.Errorf("advance past 420 should wrap to 1, got CurrentTick=%d", CurrentTick)
	}

	// Determinism: a fresh identical run reproduces the counter exactly.
	CurrentTick = 1
	CurrentStatTicked = Board.StatCount + 1
	for i := 0; i < 419; i++ {
		GameStep()
	}
	if CurrentTick != 420 {
		t.Errorf("second run diverged: CurrentTick=%d, want 420", CurrentTick)
	}
}

// TestGameStepStopsOnExitRequest verifies GameStep suppresses the cycle advance
// when GamePlayExitRequested is set, matching the original loop's exit guard.
func TestGameStepStopsOnExitRequest(t *testing.T) {
	Headless = true
	defer func() { Headless = false }()

	Board.StatCount = 0
	Board.Stats[0].Cycle = 0
	CurrentTick = 10
	CurrentStatTicked = Board.StatCount + 1
	GamePlayExitRequested = true
	defer func() { GamePlayExitRequested = false }()

	GameStep()
	if CurrentTick != 10 {
		t.Errorf("exit-requested GameStep must not advance; CurrentTick=%d, want 10", CurrentTick)
	}
}
