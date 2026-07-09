package zztgo

import "testing"

// twoPlayerBoard builds a cleared board with two players at (10,12) and
// (40,12), both alive. Mirrors the setup in TestTwoPlayersIndependentInput.
func twoPlayerBoard(t *testing.T) (*Engine, int16, int16) {
	t.Helper()

	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.SetInputSource(&ScriptedInput{})

	for ix := int16(2); ix < BOARD_WIDTH; ix++ {
		for iy := int16(2); iy < BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}

	// Remove the default stat 0 player placed by BoardCreate.
	e.Board.Tiles[e.Board.Stats[0].X][e.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	e.Board.StatCount = -1

	e.Board.Info.StartPlayerX = 10
	e.Board.Info.StartPlayerY = 12
	p1 := e.SpawnPlayer()

	e.Board.Info.StartPlayerX = 40
	e.Board.Info.StartPlayerY = 12
	p2 := e.SpawnPlayer()

	e.PlayerFor(p1).Health = 100
	e.PlayerFor(p2).Health = 100

	e.CurrentTick = 0
	e.CurrentStatTicked = 0
	e.GamePlayExitRequested = false

	return e, p1, p2
}

// step runs one full cycle and returns the events emitted during it.
func step(e *Engine, inputs map[int16]PlayerInput) []Event {
	e.Events = e.Events[:0]
	e.CurrentTick = 0
	e.CurrentStatTicked = 0
	e.GameStepWithInputs(inputs)
	return e.Events
}

func pauseEventFor(events []Event, statId int16) (PauseEvent, bool) {
	for _, ev := range events {
		if pe, ok := ev.(PauseEvent); ok && pe.StatId == statId {
			return pe, true
		}
	}
	return PauseEvent{}, false
}

// TestM311PauseIsPerPlayer is the M3.11 definition of done for 'P': pausing
// must skip only the pausing player's tick, never freeze the room.
func TestM311PauseIsPerPlayer(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)

	// P1 presses 'P'. P2 sends nothing.
	events := step(e, map[int16]PlayerInput{p1: {Key: 'P'}})

	if !e.PlayerFor(p1).Paused {
		t.Errorf("P1 should be paused after pressing 'P'")
	}
	if e.PlayerFor(p2).Paused {
		t.Errorf("P2 must NOT be paused by P1 pressing 'P' — no global pause")
	}
	if pe, ok := pauseEventFor(events, p1); !ok || !pe.Paused {
		t.Errorf("expected PauseEvent{StatId:%d, Paused:true}, got %+v (ok=%v)", p1, pe, ok)
	}

	// While P1 stays paused, its tick must be SKIPPED. Asserting "P1 didn't
	// move" would be vacuous (non-movement input never moves anyone). Instead
	// feed P1 a key with an observable side effect: 'B' toggles sound, but only
	// if the tick runs. Meanwhile P2 keeps playing, proving no global freeze.
	soundBefore := e.PlayerFor(p1).SoundEnabled
	step(e, map[int16]PlayerInput{
		p1: {Key: 'B'}, // would toggle sound if the tick were not skipped
		p2: {DeltaX: -1},
	})

	if e.PlayerFor(p1).SoundEnabled != soundBefore {
		t.Errorf("paused P1's tick ran: 'B' toggled sound (%v→%v); it must be skipped",
			soundBefore, e.PlayerFor(p1).SoundEnabled)
	}
	if !e.PlayerFor(p1).Paused {
		t.Errorf("P1 should still be paused after non-movement input")
	}
	if int16(e.Board.Stats[p2].X) != 39 {
		t.Errorf("P2 at x=%d, want 39 — the room must keep running while P1 is paused",
			e.Board.Stats[p2].X)
	}
}

// TestM311MovementResumesPlay: movement unpauses and the move lands on that
// same tick, rather than the keypress being swallowed.
func TestM311MovementResumesPlay(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)

	step(e, map[int16]PlayerInput{p1: {Key: 'P'}})
	if !e.PlayerFor(p1).Paused {
		t.Fatalf("precondition: P1 must be paused")
	}

	startX := int16(e.Board.Stats[p1].X)
	events := step(e, map[int16]PlayerInput{p1: {DeltaX: 1}})

	if e.PlayerFor(p1).Paused {
		t.Errorf("movement input must resume play")
	}
	if pe, ok := pauseEventFor(events, p1); !ok || pe.Paused {
		t.Errorf("expected PauseEvent{Paused:false} on resume, got %+v (ok=%v)", pe, ok)
	}
	if got := int16(e.Board.Stats[p1].X); got != startX+1 {
		t.Errorf("P1 at x=%d, want %d — the resuming move must land this tick", got, startX+1)
	}
}

// TestM311SaveEmitsEventAndNeverBlocks is the M3.11 definition of done for 'S':
// the sim must emit and return. If 'S' still reached GameWorldSave →
// SidebarPromptString → InputReadWaitKey, this test would hang, not fail.
func TestM311SaveEmitsEventAndNeverBlocks(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)

	events := step(e, map[int16]PlayerInput{
		p1: {Key: 'S'},
		p2: {DeltaX: -1},
	})

	var found bool
	for _, ev := range events {
		if se, ok := ev.(SavePromptEvent); ok && se.StatId == p1 {
			found = true
		}
	}
	if !found {
		t.Errorf("pressing 'S' must emit SavePromptEvent{StatId:%d}; got %+v", p1, events)
	}

	// The other player kept moving: the room never blocked.
	if int16(e.Board.Stats[p2].X) != 39 {
		t.Errorf("P2 at x=%d, want 39 — 'S' must not stall the room", e.Board.Stats[p2].X)
	}

	// The server refuses saves by answering with an empty name. That must drain
	// the queue and write nothing.
	e.SubmitSaveFilename(p1, "")
	step(e, nil)
	if len(e.PendingSaveFilenames) != 0 {
		t.Errorf("PendingSaveFilenames not drained: %+v", e.PendingSaveFilenames)
	}
	if e.SavedGameFileName != "" {
		t.Errorf("a refused save must not set SavedGameFileName, got %q", e.SavedGameFileName)
	}
}

// TestM311SoundToggleIsPerPlayer is the M3.11 definition of done for 'B': one
// player muting must not silence anyone else, and must not touch the
// process-global SoundEnabled while headless.
func TestM311SoundToggleIsPerPlayer(t *testing.T) {
	e, p1, p2 := twoPlayerBoard(t)

	if !e.PlayerFor(p1).SoundEnabled || !e.PlayerFor(p2).SoundEnabled {
		t.Fatalf("players should default to sound enabled")
	}

	globalBefore := SoundEnabled
	step(e, map[int16]PlayerInput{p1: {Key: 'B'}})

	if e.PlayerFor(p1).SoundEnabled {
		t.Errorf("P1 pressed 'B': its own sound should be off")
	}
	if !e.PlayerFor(p2).SoundEnabled {
		t.Errorf("P1 pressing 'B' must not mute P2")
	}
	if SoundEnabled != globalBefore {
		t.Errorf("headless engine must not touch the process-global SoundEnabled")
	}

	// The per-player flag is what the HUD reports.
	if hudSnapshot(e, p1).SoundEnabled {
		t.Errorf("P1's HUD should report sound disabled")
	}
	if !hudSnapshot(e, p2).SoundEnabled {
		t.Errorf("P2's HUD should still report sound enabled")
	}
}

// TestM311DeathDoesNotUnmute: ResetPlayerState clears Paused but must leave the
// player's sound preference alone.
func TestM311DeathDoesNotUnmute(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)

	step(e, map[int16]PlayerInput{p1: {Key: 'B'}})
	step(e, map[int16]PlayerInput{p1: {Key: 'P'}})

	e.ResetPlayerState(p1)

	if e.PlayerFor(p1).Paused {
		t.Errorf("ResetPlayerState should clear Paused")
	}
	if e.PlayerFor(p1).SoundEnabled {
		t.Errorf("ResetPlayerState must not un-mute the player")
	}
}

// TestReenterWhenZappedKeepsPlayerOnBoard reproduces a live bug report: on a
// board with ReenterWhenZapped, taking a hit made the player's sprite vanish
// and the player became uncontrollable.
//
// Cause: DamageStat clears the old tile to E_EMPTY, moves stat.X/Y to the start
// position, and never sets the destination tile to E_PLAYER — faithful to
// GAME.PAS:1163, which relies on GamePlayLoop's terminal-only pause branch to
// redraw and restore E_PLAYER on unpause. Headless, nothing restores it, and
// GameStepWithInputs dispatches tick procs BY TILE ELEMENT, so the player stat
// never runs its tick again.
//
// This predates M3.11 (the old code set the no-op e.GamePaused there).
func TestReenterWhenZappedKeepsPlayerOnBoard(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)

	e.Board.Info.ReenterWhenZapped = true
	// The player entered this board at (5,5): that, not the board's stored
	// StartPlayerX/Y, is where a zap returns them. See Engine.ReenterPoint.
	e.SetReenterPoint(p1, 5, 5)

	e.DamageStat(p1)

	// The player must still exist on the board, at the start position.
	if got := e.Board.Tiles[5][5].Element; got != E_PLAYER {
		t.Errorf("tile at start (5,5) is element %d, want E_PLAYER (%d): the sprite vanished",
			got, E_PLAYER)
	}
	if int16(e.Board.Stats[p1].X) != 5 || int16(e.Board.Stats[p1].Y) != 5 {
		t.Fatalf("player stat at (%d,%d), want (5,5)", e.Board.Stats[p1].X, e.Board.Stats[p1].Y)
	}

	// Vanilla pauses after a re-enter, so movement is what resumes play. The
	// player must be controllable again: one movement input moves them.
	if !e.PlayerFor(p1).Paused {
		t.Errorf("player should be paused after re-enter, as in vanilla")
	}
	step(e, map[int16]PlayerInput{p1: {DeltaX: 1}})

	if int16(e.Board.Stats[p1].X) != 6 {
		t.Errorf("player at x=%d, want 6: the player is uncontrollable after re-enter",
			e.Board.Stats[p1].X)
	}
}

// TestReenterWhenZappedPreservesUnder: placing the player back on the start
// square must not permanently destroy whatever was standing there. The tile is
// saved into stat.Under and restored when the player steps off, as MoveStat does.
func TestReenterWhenZappedPreservesUnder(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)

	e.Board.Info.ReenterWhenZapped = true
	e.SetReenterPoint(p1, 5, 5)
	// Something the player will land on top of.
	e.Board.Tiles[5][5] = TTile{Element: E_FOREST, Color: ElementDefs[E_FOREST].Color}

	e.DamageStat(p1)

	if e.Board.Stats[p1].Under.Element != E_FOREST {
		t.Errorf("stat.Under.Element = %d, want E_FOREST (%d): the start tile was clobbered",
			e.Board.Stats[p1].Under.Element, E_FOREST)
	}
	if e.Board.Tiles[5][5].Element != E_PLAYER {
		t.Fatalf("player not placed on the start tile")
	}

	// Step off; the forest must come back.
	step(e, map[int16]PlayerInput{p1: {DeltaX: 1}})

	if got := e.Board.Tiles[5][5].Element; got != E_FOREST {
		t.Errorf("tile at (5,5) is %d after stepping off, want E_FOREST (%d) restored",
			got, E_FOREST)
	}
}

// TestReenterUsesPlayerEntrySquareNotStaleBoardValue guards a live bug: on TOWN
// board 19 ("The Mixer") the world file stores StartPlayerX/Y = (30,25), which
// is an E_NORMAL wall tile. Vanilla never reads that value because BoardEnter
// overwrites it on entry; RoomManager never calls BoardEnter, so the server used
// the stale wall coordinate and a zapped player was teleported inside the wall.
//
// Re-entry must use the square the player actually entered on.
func TestReenterUsesPlayerEntrySquareNotStaleBoardValue(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	if !e.WorldLoad("../fixtures/TOWN", ".ZZT", false) {
		t.Skip("fixtures/TOWN.ZZT unavailable")
	}
	e.BoardOpen(19)

	if !e.Board.Info.ReenterWhenZapped {
		t.Fatalf("precondition: board 19 %q should have ReenterWhenZapped", e.Board.Name)
	}
	staleX, staleY := int16(e.Board.Info.StartPlayerX), int16(e.Board.Info.StartPlayerY)
	if got := e.Board.Tiles[staleX][staleY].Element; got != E_NORMAL {
		t.Fatalf("precondition: stored start (%d,%d) should be E_NORMAL (%d), got %d",
			staleX, staleY, E_NORMAL, got)
	}

	// Put a player on a real, walkable square — as a transfer or join would.
	//
	// M4.3b: this used to be (5,24), which is where board 19's own stat 0 stands.
	// Dropping a second player stat there manufactured the very overlap M4.3b
	// fixes, so re-entry now pushes the arriving player off it and the test's
	// stale-wall assertion could never be reached. Use a square no stat holds;
	// the assertions below are unchanged.
	entryX, entryY := int16(4), int16(24)
	if _, held := e.StatAt(entryX, entryY, -1); held {
		t.Fatalf("precondition: entry square (%d,%d) must hold no stat", entryX, entryY)
	}
	e.Board.Tiles[entryX][entryY] = TTile{Element: E_EMPTY}
	e.AddStat(entryX, entryY, E_PLAYER, int16(ElementDefs[E_PLAYER].Color), 1, StatTemplateDefault)
	p := e.Board.StatCount
	e.Board.Tiles[entryX][entryY] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	e.ResetPlayerState(p)
	e.SetReenterPoint(p, entryX, entryY)

	e.DamageStat(p)

	if int16(e.Board.Stats[p].X) == staleX && int16(e.Board.Stats[p].Y) == staleY {
		t.Fatalf("player was teleported into the stale wall square (%d,%d)", staleX, staleY)
	}
	if int16(e.Board.Stats[p].X) != entryX || int16(e.Board.Stats[p].Y) != entryY {
		t.Errorf("player at (%d,%d), want their entry square (%d,%d)",
			e.Board.Stats[p].X, e.Board.Stats[p].Y, entryX, entryY)
	}
	if e.Board.Tiles[entryX][entryY].Element != E_PLAYER {
		t.Errorf("player tile not restored at the entry square")
	}
}
