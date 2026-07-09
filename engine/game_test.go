package zztgo

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
		GameStep(nil)
	}
	if E.CurrentTick != 420 {
		t.Errorf("after 419 advances from 1, E.CurrentTick=%d, want 420", E.CurrentTick)
	}

	// One more advance wraps 420 -> 1.
	GameStep(nil)
	if E.CurrentTick != 1 {
		t.Errorf("advance past 420 should wrap to 1, got E.CurrentTick=%d", E.CurrentTick)
	}

	// Determinism: a fresh identical run reproduces the counter exactly.
	E.CurrentTick = 1
	E.CurrentStatTicked = E.Board.StatCount + 1
	for i := 0; i < 419; i++ {
		GameStep(nil)
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

	GameStep(nil)
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
//
//	P1 at (30,13) — nearest to the tiger
//	P2 at (30,25) — far away vertically (dist=169)
//	P3 at (58,13) — far away horizontally (dist=529)
//	Tiger at (35,13), P1=10 (max aggression, always seek), P2=0 (no shooting), Cycle=1
//
// With P1=10, the condition int16(stat.P1) < e.Random(10) is 10 < (0..9), always
// false, so the lion always calls CalcDirectionSeek → NearestPlayer.
// Nearest to (35,13) is P1 at (30,13); tiger must step left (decreasing X).
// TestTwoPlayersIndependentInput is the M2.3 definition of done: two players
// on one board receive different inputs via GameStepWithInputs and each moves
// to its own destination independently in one step.
//
// Layout: P1 at (10,12), P2 at (40,12).
// Input:  P1 moves right (+1,0), P2 moves left (-1,0).
// After one step: P1 should be at (11,12), P2 at (39,12).
func TestTwoPlayersIndependentInput(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})

	// Clear interior tiles.
	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}

	// Remove the default stat 0 player placed by BoardCreate.
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1

	// Spawn P1 at (10,12).
	e.Board.Info.StartPlayerX = 10
	e.Board.Info.StartPlayerY = 12
	p1 := e.SpawnPlayer()

	// Spawn P2 at (40,12).
	e.Board.Info.StartPlayerX = 40
	e.Board.Info.StartPlayerY = 12
	p2 := e.SpawnPlayer()

	// Give both players health so the death-zero-input branch doesn't fire.
	e.PlayerFor(p1).Health = 100
	e.PlayerFor(p2).Health = 100

	// Run one full cycle: P1 moves right, P2 moves left.
	e.CurrentTick = 0
	e.CurrentStatTicked = 0
	e.GamePlayExitRequested = false

	e.GameStepWithInputs(map[int16]PlayerInput{
		p1: {DeltaX: 1, DeltaY: 0},
		p2: {DeltaX: -1, DeltaY: 0},
	})

	// Both players have cycle=1, so they both tick on currentTick=0.
	if int16(e.Board.Stats[p1].X) != 11 || int16(e.Board.Stats[p1].Y) != 12 {
		t.Errorf("P1 at (%d,%d), want (11,12)", e.Board.Stats[p1].X, e.Board.Stats[p1].Y)
	}
	if int16(e.Board.Stats[p2].X) != 39 || int16(e.Board.Stats[p2].Y) != 12 {
		t.Errorf("P2 at (%d,%d), want (39,12)", e.Board.Stats[p2].X, e.Board.Stats[p2].Y)
	}
}

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
	e.GameStep(nil)

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

// TestDeathRespawnInventoryIsolation is the M2.4 definition of done:
// one player dies (health reaches 0), a DeathEvent is emitted, a RespawnEvent
// follows after RESPAWN_TICKS, the dying player comes back at StartPlayerX/Y
// with full health and invulnerability — and the other player's inventory is
// completely untouched throughout.
func TestDeathRespawnInventoryIsolation(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})

	// Clear interior tiles.
	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}

	// Remove the default stat 0 player placed by BoardCreate.
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1

	// Spawn P1 at (10, 12). Respawn point for the board is (5, 5).
	e.Board.Info.StartPlayerX = 10
	e.Board.Info.StartPlayerY = 12
	p1 := e.SpawnPlayer()

	// Spawn P2 at (40, 12).
	e.Board.Info.StartPlayerX = 40
	e.Board.Info.StartPlayerY = 12
	p2 := e.SpawnPlayer()

	// Reset board start point to (5,5) — the intended respawn location for P1.
	// In real use the map designer sets this once; here we set it after spawning
	// so both players can be placed at different positions in the test.
	e.Board.Info.StartPlayerX = 5
	e.Board.Info.StartPlayerY = 5
	e.Board.Tiles[5][5] = TTile{Element: E_EMPTY} // ensure spawn point is clear

	// Give P2 some inventory that must not change when P1 dies.
	e.PlayerFor(p2).Ammo = 7
	e.PlayerFor(p2).Gems = 3
	e.PlayerFor(p2).Score = 500
	e.PlayerFor(p2).Health = 100

	// Set P1 health to 10 (one hit from death) and give them a score.
	e.PlayerFor(p1).Health = 10
	e.PlayerFor(p1).Score = 200

	// --- Kill P1 ---
	e.DamageStat(p1)

	// Health should now be 0.
	if e.PlayerFor(p1).Health != 0 {
		t.Errorf("after damage P1.Health=%d, want 0", e.PlayerFor(p1).Health)
	}
	// Score should have the penalty applied (200 - 100 = 100).
	if e.PlayerFor(p1).Score != 100 {
		t.Errorf("P1.Score=%d after death, want 100", e.PlayerFor(p1).Score)
	}
	// RespawnTicks should be set.
	if e.PlayerFor(p1).RespawnTicks != RESPAWN_TICKS {
		t.Errorf("P1.RespawnTicks=%d, want %d", e.PlayerFor(p1).RespawnTicks, RESPAWN_TICKS)
	}

	// A DeathEvent should have been emitted.
	var deathEv *DeathEvent
	for _, ev := range e.Events {
		if d, ok := ev.(DeathEvent); ok {
			deathEv = &d
			break
		}
	}
	if deathEv == nil {
		t.Fatal("expected DeathEvent, got none")
	}
	if deathEv.StatId != p1 {
		t.Errorf("DeathEvent.StatId=%d, want p1=%d", deathEv.StatId, p1)
	}
	e.Events = nil

	// P2 inventory must be completely unchanged.
	if e.PlayerFor(p2).Ammo != 7 || e.PlayerFor(p2).Gems != 3 || e.PlayerFor(p2).Score != 500 || e.PlayerFor(p2).Health != 100 {
		t.Errorf("P2 inventory changed: ammo=%d gems=%d score=%d health=%d",
			e.PlayerFor(p2).Ammo, e.PlayerFor(p2).Gems, e.PlayerFor(p2).Score, e.PlayerFor(p2).Health)
	}

	// --- Tick through RESPAWN_TICKS cycles and collect a RespawnEvent ---
	e.CurrentTick = 1
	e.CurrentStatTicked = 0
	e.GamePlayExitRequested = false

	var respawnEv *RespawnEvent
	for step := 0; step < RESPAWN_TICKS+5; step++ {
		e.GameStepWithInputs(map[int16]PlayerInput{})
		for _, ev := range e.Events {
			if r, ok := ev.(RespawnEvent); ok {
				respawnEv = &r
			}
		}
		e.Events = nil
		if respawnEv != nil {
			break
		}
	}

	if respawnEv == nil {
		t.Fatal("expected RespawnEvent after RESPAWN_TICKS, got none")
	}
	if respawnEv.StatId != p1 {
		t.Errorf("RespawnEvent.StatId=%d, want p1=%d", respawnEv.StatId, p1)
	}

	// P1 should be back at StartPlayerX/Y (5,5) with full health and invulnerability.
	if int16(e.Board.Stats[p1].X) != 5 || int16(e.Board.Stats[p1].Y) != 5 {
		t.Errorf("P1 respawned at (%d,%d), want (5,5)", e.Board.Stats[p1].X, e.Board.Stats[p1].Y)
	}
	if e.PlayerFor(p1).Health != 100 {
		t.Errorf("P1.Health=%d after respawn, want 100", e.PlayerFor(p1).Health)
	}
	if e.PlayerFor(p1).EnergizerTicks != RESPAWN_INVULN_TICKS {
		t.Errorf("P1.EnergizerTicks=%d after respawn, want %d", e.PlayerFor(p1).EnergizerTicks, RESPAWN_INVULN_TICKS)
	}

	// P2 inventory still untouched after all those ticks.
	if e.PlayerFor(p2).Ammo != 7 || e.PlayerFor(p2).Gems != 3 || e.PlayerFor(p2).Score != 500 || e.PlayerFor(p2).Health != 100 {
		t.Errorf("P2 inventory changed after respawn ticks: ammo=%d gems=%d score=%d health=%d",
			e.PlayerFor(p2).Ammo, e.PlayerFor(p2).Gems, e.PlayerFor(p2).Score, e.PlayerFor(p2).Health)
	}
}
