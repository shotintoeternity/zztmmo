package zztgo

import "testing"

// M16.6b — the walk click is never heard.
//
// Vanilla pokes the PC speaker directly on every attempted player step
// (`if SoundEnabled and not SoundIsPlaying then Sound(110)`, then `NoSound`
// whether the step succeeded or was refused; ELEMENTS.PAS:1393-1402). The
// port's Sound()/NoSound() are TODO stubs (lib.go:124), so no client ever
// heard it, and fixtures/oracle's 20+ walking scenarios had to filter 110 Hz
// onsets out of every sound comparison (oracle_parity_test.go). This task
// replaces the stubbed call with WalkClickEvent, the same shape every other
// presentation-only event already takes (SoundEvent, DeathEvent, ...): never
// entering StateHash, never routed through SoundQueue's priority arbitration,
// and — unlike SoundEvent, whose StatId can be -1 for a room-wide sound —
// always attributed to the mover, because vanilla's Sound(110) has no
// "somebody else's object" case.
//
// The real parity proof is oracle_parity_test.go: removing its 110 Hz filter
// and replaying all 20+ walk scenarios against the real ZZT.EXE captures with
// clicks compared. These tests cover what the oracle scenarios don't
// isolate directly: the emission gate, the StateHash boundary, and
// multiplayer attribution.

func newWalkClickTestEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	e.Board.Stats[0].X = 15
	e.Board.Stats[0].Y = 15
	e.Board.Tiles[15][15] = TTile{Element: E_PLAYER, Color: 0x0F}
	e.Board.Tiles[16][15] = TTile{Element: E_EMPTY}
	return e
}

func walkClickEvents(events []Event) []WalkClickEvent {
	var out []WalkClickEvent
	for _, ev := range events {
		if click, ok := ev.(WalkClickEvent); ok {
			out = append(out, click)
		}
	}
	return out
}

// TestWalkClickEmittedOnAttemptedStep is the core positive case: stepping
// onto open, walkable ground emits exactly one WalkClickEvent naming the
// mover, at vanilla's 110 Hz.
func TestWalkClickEmittedOnAttemptedStep(t *testing.T) {
	e := newWalkClickTestEngine(t)
	e.SetInputSource(&ScriptedInput{Ticks: []ScriptedTick{{DeltaX: 1, DeltaY: 0}}})
	e.InputUpdate()
	e.GameStep(nil)

	clicks := walkClickEvents(e.Events)
	if len(clicks) != 1 {
		t.Fatalf("got %d WalkClickEvents, want 1: %#v", len(clicks), e.Events)
	}
	if clicks[0].StatId != 0 {
		t.Errorf("WalkClickEvent.StatId=%d, want 0", clicks[0].StatId)
	}
	if clicks[0].FreqHz != 110 {
		t.Errorf("WalkClickEvent.FreqHz=%d, want 110 (ELEMENTS.PAS:1394 Sound(110))", clicks[0].FreqHz)
	}
	if e.Board.Stats[0].X != 16 || e.Board.Stats[0].Y != 15 {
		t.Errorf("player at (%d,%d), want (16,15): the click must not have moved or blocked the step",
			e.Board.Stats[0].X, e.Board.Stats[0].Y)
	}
}

// TestWalkClickSuppressedWhenSoundDisabled pins the same gate vanilla uses:
// `if SoundEnabled and not SoundIsPlaying`. With sound off for that player, no
// click fires even though the step itself still succeeds.
func TestWalkClickSuppressedWhenSoundDisabled(t *testing.T) {
	e := newWalkClickTestEngine(t)
	e.PlayerFor(0).SoundEnabled = false
	e.SetInputSource(&ScriptedInput{Ticks: []ScriptedTick{{DeltaX: 1, DeltaY: 0}}})
	e.InputUpdate()
	e.GameStep(nil)

	if clicks := walkClickEvents(e.Events); len(clicks) != 0 {
		t.Fatalf("got %d WalkClickEvents with SoundEnabled=false, want 0: %#v", len(clicks), clicks)
	}
	if e.Board.Stats[0].X != 16 || e.Board.Stats[0].Y != 15 {
		t.Errorf("player at (%d,%d), want (16,15): disabling sound must not disable movement",
			e.Board.Stats[0].X, e.Board.Stats[0].Y)
	}
}

// TestWalkClickNotEmittedWhenTouchProcBlocksMovement covers the other half of
// ELEMENTS.PAS:1391's gate: the click sits inside `if (InputDeltaX<>0) or
// (InputDeltaY<>0)`, evaluated AFTER the destination's TouchProc runs, so an
// element whose TouchProc zeroes the deltas outright (refusing the step
// before the click check, not merely being unwalkable) suppresses the click
// too. ElementPassageTouch does exactly this for a passage tile with no
// matching stat (elements.go) — simpler to construct than a real transporter.
func TestWalkClickNotEmittedWhenTouchProcBlocksMovement(t *testing.T) {
	e := newWalkClickTestEngine(t)
	e.Board.Tiles[16][15] = TTile{Element: E_PASSAGE} // no AddStat: GetStatIdAt returns -1
	e.SetInputSource(&ScriptedInput{Ticks: []ScriptedTick{{DeltaX: 1, DeltaY: 0}}})
	e.InputUpdate()
	e.GameStep(nil)

	if clicks := walkClickEvents(e.Events); len(clicks) != 0 {
		t.Fatalf("got %d WalkClickEvents when TouchProc zeroed the deltas, want 0: %#v", len(clicks), clicks)
	}
	if e.Board.Stats[0].X != 15 || e.Board.Stats[0].Y != 15 {
		t.Errorf("player moved to (%d,%d), want to stay at (15,15): a stat-less passage refuses the step",
			e.Board.Stats[0].X, e.Board.Stats[0].Y)
	}
}

// TestWalkClickDoesNotAffectStateHash is the M16.6b DoD's "must not enter
// StateHash" requirement, demonstrated rather than asserted from reading the
// code: two engines driven through an identical scripted move, differing only
// in whether the click fires, must land on identical StateHash and identical
// board/player state.
func TestWalkClickDoesNotAffectStateHash(t *testing.T) {
	withClick := newWalkClickTestEngine(t)
	withClick.SetInputSource(&ScriptedInput{Ticks: []ScriptedTick{{DeltaX: 1, DeltaY: 0}}})
	withClick.InputUpdate()
	withClick.GameStep(nil)
	if len(walkClickEvents(withClick.Events)) != 1 {
		t.Fatalf("setup: expected the click to fire in the withClick engine")
	}

	withoutClick := newWalkClickTestEngine(t)
	withoutClick.PlayerFor(0).SoundEnabled = false
	withoutClick.SetInputSource(&ScriptedInput{Ticks: []ScriptedTick{{DeltaX: 1, DeltaY: 0}}})
	withoutClick.InputUpdate()
	withoutClick.GameStep(nil)
	if len(walkClickEvents(withoutClick.Events)) != 0 {
		t.Fatalf("setup: expected no click in the withoutClick engine")
	}

	if StateHash(withClick) != StateHash(withoutClick) {
		t.Fatalf("StateHash differs solely because a WalkClickEvent was emitted: %d vs %d",
			StateHash(withClick), StateHash(withoutClick))
	}
}

// TestWalkClickIsPrivateToTheMover mirrors TestM74PerPlayerSoundAttribution
// (M7.4, deviation per-player-sound): a WalkClickEvent has no "room-wide"
// case the way an object's own #play does for SoundEvent, so it must always
// route only to the player who took the step, never broadcast to the room.
func TestWalkClickIsPrivateToTheMover(t *testing.T) {
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.BoardCreate()
	setup.BoardClose()

	rm := NewRoomManager(setup.World)
	playerA := rm.JoinPlayer(0, 10, 10)
	playerB := rm.JoinPlayer(0, 20, 10)
	room, ok := rm.Room(0)
	if !ok {
		t.Fatal("room 0 missing")
	}
	_, statA, ok := rm.PlayerLocation(playerA)
	if !ok {
		t.Fatal("player A missing")
	}
	a := room.Engine.Board.Stats[statA]
	room.Engine.Board.Tiles[int16(a.X)+1][a.Y] = TTile{Element: E_EMPTY}

	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{
		playerA: {DeltaX: 1},
	})
	aEvents := rm.DrainPlayerEvents(playerA)
	bEvents := rm.DrainPlayerEvents(playerB)

	if len(walkClickEvents(aEvents)) != 1 {
		t.Fatalf("player A did not receive their own walk click: %#v", aEvents)
	}
	if len(walkClickEvents(bEvents)) != 0 {
		t.Fatalf("player B received player A's walk click: %#v", bEvents)
	}
	if protocolEventsHaveType(diffs[playerA].Events, "walkClick") || protocolEventsHaveType(diffs[playerB].Events, "walkClick") {
		t.Fatalf("walk click leaked through the room-wide diff: A=%#v B=%#v", diffs[playerA].Events, diffs[playerB].Events)
	}
}

func protocolEventsHaveType(events []ProtocolEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
