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
	e.PlayerFor(0).Ammo = 10

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

// TestTigerChasesNearestPlayer is the M2.2 definition of done: with 3 players
// on a board, a tiger always chases the nearest one via NearestPlayer.
//
// Layout:
//   P1 at (30,13) — nearest to the tiger
//   P2 at (30,25) — far away vertically (dist=169)
//   P3 at (58,13) — far away horizontally (dist=529)
//   Tiger at (35,13), P1=10 (max aggression, always seek), P2=0 (no shooting), Cycle=1
//
// With P1=10, the condition int16(stat.P1) < e.Random(10) is 10 < (0..9), always
// false, so the lion always calls CalcDirectionSeek → NearestPlayer.
// Nearest to (35,13) is P1 at (30,13); tiger must step left (decreasing X).
func TestTigerChasesNearestPlayer(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})

	// Clear all board interior tiles.
	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}

	// BoardCreate places a player at Stats[0] (BOARD_WIDTH/2, BOARD_HEIGHT/2).
	// Clear that tile and reset stat count so SpawnPlayer starts clean.
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1

	// Spawn player 1 at (30,13) — nearest to tiger.
	e.Board.Info.StartPlayerX = 30
	e.Board.Info.StartPlayerY = 13
	p1 := e.SpawnPlayer() // stat 0

	// Spawn player 2 at (30,25) — far vertically.
	e.Board.Info.StartPlayerX = 30
	e.Board.Info.StartPlayerY = 25
	_ = e.SpawnPlayer() // stat 1

	// Spawn player 3 at (58,13) — far horizontally.
	e.Board.Info.StartPlayerX = 58
	e.Board.Info.StartPlayerY = 13
	_ = e.SpawnPlayer() // stat 2

	// Add tiger at (35,13): max aggression, no shooting, cycle=1 so it ticks every step.
	e.AddStat(35, 13, E_TIGER, int16(ElementDefs[E_TIGER].Color), 1, StatTemplateDefault)
	tigerStatId := e.Board.StatCount
	e.Board.Stats[tigerStatId].P1 = 10 // always seek (10 < Random(0..9) is never true)
	e.Board.Stats[tigerStatId].P2 = 0  // no shooting
	e.Board.Tiles[35][13] = TTile{Element: E_TIGER, Color: ElementDefs[E_TIGER].Color}

	// Sanity-check nearest player from tiger position.
	nearest := e.NearestPlayer(35, 13)
	if nearest != p1 {
		t.Fatalf("NearestPlayer(35,13)=%d, want p1=%d (player at (30,13))", nearest, p1)
	}

	startX := int16(e.Board.Stats[tigerStatId].X) // 35

	// Tick the tiger directly for 3 cycles.
	// With cycle=1, the stagger condition (currentTick % 1 == tigerStatId % 1) is
	// always 0 == 0, so the tiger ticks on every CurrentTick value.
	for step := int16(0); step < 3; step++ {
		e.CurrentTick = step
		for e.CurrentStatTicked = 0; e.CurrentStatTicked <= e.Board.StatCount; e.CurrentStatTicked++ {
			stat := &e.Board.Stats[e.CurrentStatTicked]
			if stat.Cycle != 0 && e.CurrentTick%stat.Cycle == e.CurrentStatTicked%stat.Cycle {
				ElementDefs[e.Board.Tiles[stat.X][stat.Y].Element].TickProc(e, e.CurrentStatTicked)
			}
		}
	}

	endX := int16(e.Board.Stats[tigerStatId].X)
	if endX >= startX {
		t.Errorf("tiger x=%d after 3 ticks (started at %d); expected to move left toward P1 at x=30",
			endX, startX)
	}
	if int16(e.Board.Stats[tigerStatId].Y) != 13 {
		t.Errorf("tiger y=%d, expected 13 (same row as nearest player)", e.Board.Stats[tigerStatId].Y)
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
	if e.PlayerFor(0).Gems != 1 {
		t.Errorf("expected 1 gem, got %d", e.PlayerFor(0).Gems)
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

