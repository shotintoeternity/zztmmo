package zztgo

import (
	"path/filepath"
	"testing"
)

func townRoomManager(t *testing.T) *RoomManager {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q, %q) failed", worldBase, ".ZZT")
	}
	return NewRoomManager(setup.World)
}

func findEvent(events []ProtocolEvent, eventType string) (ProtocolEvent, bool) {
	for _, event := range events {
		if event.Type == eventType {
			return event, true
		}
	}
	return ProtocolEvent{}, false
}

// Pressing '?' used to call GameDebugPrompt, whose PromptString blocks forever
// on InputReadWaitKey when headless — one browser client could wedge the whole
// server tick loop. It must now emit an event and return.
func TestDebugPromptKeyEmitsEventAndDoesNotBlock(t *testing.T) {
	rm := townRoomManager(t)
	playerID := rm.JoinPlayer(1, 30, 12)
	if _, ok := rm.Snapshot(playerID); !ok {
		t.Fatal("snapshot failed")
	}

	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {Key: '?'}})
	diff, ok := diffs[playerID]
	if !ok {
		t.Fatal("missing diff for player")
	}
	event, ok := findEvent(diff.Events, "debugPrompt")
	if !ok {
		t.Fatalf("no debugPrompt event; got %+v", diff.Events)
	}
	_, statID, _ := rm.PlayerLocation(playerID)
	if event.StatID != statID {
		t.Fatalf("debugPrompt StatID=%d, want %d", event.StatID, statID)
	}
}

// Vanilla ZZT's debug cheats always hit stat 0. With N players the cheat must
// credit whoever typed it.
func TestDebugCommandCreditsTypingPlayer(t *testing.T) {
	rm := townRoomManager(t)
	playerA := rm.JoinPlayer(1, 30, 12)
	playerB := rm.JoinPlayer(1, 32, 12)

	stateA, _ := rm.PlayerState(playerA)
	stateB, _ := rm.PlayerState(playerB)
	startAmmoA := stateA.Ammo
	startAmmoB := stateB.Ammo

	if !rm.SubmitDebugCommand(playerB, "ammo") {
		t.Fatal("SubmitDebugCommand failed")
	}
	rm.StepDiffs(map[PlayerID]PlayerInput{})

	// Re-fetch: the pointers stay valid, but read through the manager to be sure.
	stateA, _ = rm.PlayerState(playerA)
	stateB, _ = rm.PlayerState(playerB)
	if stateB.Ammo != startAmmoB+5 {
		t.Fatalf("player B ammo=%d, want %d (+5)", stateB.Ammo, startAmmoB+5)
	}
	if stateA.Ammo != startAmmoA {
		t.Fatalf("player A ammo=%d, want %d (untouched)", stateA.Ammo, startAmmoA)
	}
}

// The command text is upper-cased before matching, exactly as GameDebugPrompt did.
func TestDebugCommandFlagToggleAndHealth(t *testing.T) {
	rm := townRoomManager(t)
	playerID := rm.JoinPlayer(1, 30, 12)
	room, _ := rm.Room(1)

	rm.SubmitDebugCommand(playerID, "+DEBUG")
	rm.StepDiffs(map[PlayerID]PlayerInput{})
	if !room.Engine.DebugEnabled {
		t.Fatal("+DEBUG did not set DebugEnabled")
	}

	rm.SubmitDebugCommand(playerID, "-debug")
	rm.StepDiffs(map[PlayerID]PlayerInput{})
	if room.Engine.DebugEnabled {
		t.Fatal("-debug did not clear DebugEnabled")
	}

	state, _ := rm.PlayerState(playerID)
	before := state.Health
	rm.SubmitDebugCommand(playerID, "health")
	rm.StepDiffs(map[PlayerID]PlayerInput{})
	if state.Health != before+50 {
		t.Fatalf("health=%d, want %d (+50)", state.Health, before+50)
	}
}

// The 'H' key emits a HelpEvent; the protocol layer must attach the file's
// lines, since the client cannot read the server's .HLP files.
func TestHelpEventCarriesFileLines(t *testing.T) {
	rm := townRoomManager(t)
	playerID := rm.JoinPlayer(1, 30, 12)
	if _, ok := rm.Snapshot(playerID); !ok {
		t.Fatal("snapshot failed")
	}

	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {Key: 'H'}})
	event, ok := findEvent(diffs[playerID].Events, "help")
	if !ok {
		t.Fatalf("no help event; got %+v", diffs[playerID].Events)
	}
	if event.Filename != "GAME.HLP" {
		t.Fatalf("help filename=%q, want GAME.HLP", event.Filename)
	}
	if len(event.Lines) == 0 {
		t.Fatalf("help event carried no lines (HelpDir=%q)", HelpDir)
	}
	if event.Lines[0] != "$Getting Started." {
		t.Fatalf("first help line=%q, want %q", event.Lines[0], "$Getting Started.")
	}
}
