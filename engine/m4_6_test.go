package zztgo

import "testing"

// M4.6 — Full TOWN playthrough smoke.
//
// This is deliberately semi-scripted: it drives the same RoomManager/protocol
// inputs the browser sends, but stages the player beside TOWN landmarks so the
// smoke covers the original game loop without making CI solve every maze.
func TestM46TownProtocolPlaythroughSmoke(t *testing.T) {
	world := loadTownWorldForM45(t)
	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(1, 0, 0)
	if snapshot, ok := rm.Snapshot(playerID); !ok || snapshot.BoardID != 1 || len(snapshot.Screen) != BOARD_WIDTH*25 {
		t.Fatalf("initial TOWN snapshot = board %d screen %d ok %v, want board 1 full board screen", snapshot.BoardID, len(snapshot.Screen), ok)
	}

	// Room One: pick up ordinary inventory, then cross a real passage to the
	// Armory. These are the same HUD and board-change messages the browser uses.
	stageTownPlayer(t, rm, playerID, 1, 51, 13)
	diff := stepTownUntil(t, rm, playerID, m46Input(playerID, 1, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Gems == 1 && diff.HUD.Score == 10
	})
	if diff.HUD.Health <= 100 {
		t.Fatalf("gem pickup health=%d, want health increase", diff.HUD.Health)
	}

	stageTownPlayer(t, rm, playerID, 1, 39, 13)
	stepTownUntil(t, rm, playerID, m46Input(playerID, 2, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Torches == 1
	})

	stageTownPlayer(t, rm, playerID, 1, 14, 10)
	diff = stepTownUntil(t, rm, playerID, m46Input(playerID, 3, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.BoardID == 2 && diff.HUD != nil && diff.HUD.Gems == 1 && diff.HUD.Torches == 1
	})
	if events := ProtocolEvents(rm.DrainPlayerEvents(playerID)); len(events) == 0 {
		t.Fatal("passage transfer queued no per-player board-change events")
	} else if _, _, ok := findProtocolEvent(events, "sound"); !ok {
		t.Fatalf("passage board-change events missing sound: %+v", events)
	}

	// Armory: open the vendor scroll, choose a hyperlink reply, then use the
	// bought inventory and a real green key/door pair.
	stageTownPlayer(t, rm, playerID, 2, 20, 9)
	scroll := stepTownUntilEvent(t, rm, playerID, m46Input(playerID, 4, InputMaskRight, 0), 20, "scroll")
	if scroll.Title != "Vendor" {
		t.Fatalf("scroll title=%q, want Vendor", scroll.Title)
	}
	if !rm.SubmitScrollReply(playerID, scroll.StatID, "ba") {
		t.Fatal("SubmitScrollReply ba failed")
	}
	state := mustTownPlayerState(t, rm, playerID)
	stepTownUntil(t, rm, playerID, PlayerInput{}, 20, func(diff DiffMessage) bool {
		return state.Ammo == 3 && state.Gems == 0
	})

	stageTownPlayer(t, rm, playerID, 2, 18, 20)
	stepTownUntil(t, rm, playerID, m46Input(playerID, 5, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Keys[1]
	})

	stageTownPlayer(t, rm, playerID, 2, 44, 19)
	stepTownUntil(t, rm, playerID, m46Input(playerID, 6, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && !diff.HUD.Keys[1] && playerAt(diff, playerID, 45, 19)
	})

	// Use a torch in a dark TOWN room. This catches the client command-key path
	// and the protocol HUD update for consumable item use.
	stageTownPlayer(t, rm, playerID, 23, 6, 20)
	state.Torches = 1
	stepTownUntil(t, rm, playerID, m46Input(playerID, 7, 0, 'T'), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Torches == 0 && diff.HUD.TorchTicks > 0
	})

	// Scrolls that are actual board tiles, not only OOP vendors, must also make
	// it to the browser as modal protocol events.
	stageTownPlayer(t, rm, playerID, 21, 35, 15)
	scroll = stepTownUntilEvent(t, rm, playerID, m46Input(playerID, 8, InputMaskDown, 0), 20, "scroll")
	if len(scroll.Lines) == 0 {
		t.Fatal("prison scroll had no lines")
	}

	// Damage from a real enemy touch should change only protocol-visible player
	// state; no terminal prompt or global pause is involved.
	stageTownPlayer(t, rm, playerID, 11, 43, 2)
	beforeHealth := state.Health
	stepTownUntil(t, rm, playerID, m46Input(playerID, 9, InputMaskDown, 0), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Health == beforeHealth-10
	})

	// Palace path: collect the red key on "Path to castle", spend it on the red
	// door, cross to "Outside of castle", take the passage inside, then walk the
	// castle's south edge into the Throne Room.
	stageTownPlayer(t, rm, playerID, 6, 47, 18)
	stepTownUntil(t, rm, playerID, m46Input(playerID, 10, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Keys[3]
	})

	stageTownPlayer(t, rm, playerID, 6, 12, 13)
	stepTownUntil(t, rm, playerID, m46Input(playerID, 11, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.HUD != nil && !diff.HUD.Keys[3] && playerAt(diff, playerID, 13, 13)
	})

	stageTownPlayer(t, rm, playerID, 6, 30, 1)
	stepTownUntil(t, rm, playerID, m46Input(playerID, 12, InputMaskUp, 0), 12, func(diff DiffMessage) bool {
		return diff.BoardID == 8
	})

	stageTownPlayer(t, rm, playerID, 8, 19, 13)
	stepTownUntil(t, rm, playerID, m46Input(playerID, 13, InputMaskRight, 0), 12, func(diff DiffMessage) bool {
		return diff.BoardID == 11
	})

	stageTownPlayer(t, rm, playerID, 11, 30, BOARD_HEIGHT)
	diff = stepTownUntil(t, rm, playerID, m46Input(playerID, 14, InputMaskDown, 0), 12, func(diff DiffMessage) bool {
		return diff.BoardID == 27
	})
	room, ok := rm.Room(diff.BoardID)
	if !ok {
		t.Fatalf("room %d missing after palace transfer", diff.BoardID)
	}
	if room.Engine.Board.Name != "Throne Room" {
		t.Fatalf("final board %d name=%q, want Throne Room", diff.BoardID, room.Engine.Board.Name)
	}
}

func m46Input(playerID PlayerID, seq uint64, keymask uint16, key byte) PlayerInput {
	return inputMessageToPlayerInput(InputMessage{
		Type:     MessageTypeInput,
		PlayerID: playerID,
		Seq:      seq,
		Keymask:  keymask,
		Key:      key,
	})
}

func stageTownPlayer(t *testing.T, rm *RoomManager, playerID PlayerID, boardID, x, y int16) {
	t.Helper()

	currentBoard, _, ok := rm.PlayerLocation(playerID)
	if !ok {
		t.Fatalf("player %d missing", playerID)
	}
	if currentBoard != boardID {
		rm.transferPlayer(playerID, TransferEvent{ToBoard: boardID, EntryX: x, EntryY: y})
	}

	room, ok := rm.Room(boardID)
	if !ok {
		t.Fatalf("room %d missing", boardID)
	}
	_, statID, ok := rm.PlayerLocation(playerID)
	if !ok {
		t.Fatalf("player %d missing after staging", playerID)
	}
	if heldStat, held := room.Engine.StatAt(x, y, statID); held {
		t.Fatalf("cannot stage player on board %d at (%d,%d): held by stat %d", boardID, x, y, heldStat)
	}
	movePlayerStat(room.Engine, statID, x, y)
	if _, ok := rm.Snapshot(playerID); !ok {
		t.Fatalf("snapshot failed after staging board %d at (%d,%d)", boardID, x, y)
	}
}

func stepTownUntil(t *testing.T, rm *RoomManager, playerID PlayerID, input PlayerInput, maxSteps int, match func(DiffMessage) bool) DiffMessage {
	t.Helper()

	for i := 0; i < maxSteps; i++ {
		diffs := rm.StepDiffs(map[PlayerID]PlayerInput{playerID: input})
		if diff, ok := diffs[playerID]; ok && match(diff) {
			return diff
		}
	}
	t.Fatalf("condition not met after %d TOWN protocol steps", maxSteps)
	return DiffMessage{}
}

func stepTownUntilEvent(t *testing.T, rm *RoomManager, playerID PlayerID, input PlayerInput, maxSteps int, eventType string) ProtocolEvent {
	t.Helper()

	diff := stepTownUntil(t, rm, playerID, input, maxSteps, func(diff DiffMessage) bool {
		_, _, ok := findProtocolEvent(diff.Events, eventType)
		return ok
	})
	event, _, _ := findProtocolEvent(diff.Events, eventType)
	return event
}

func findProtocolEvent(events []ProtocolEvent, eventType string) (ProtocolEvent, int, bool) {
	for i, event := range events {
		if event.Type == eventType {
			return event, i, true
		}
	}
	return ProtocolEvent{}, 0, false
}

func mustTownPlayerState(t *testing.T, rm *RoomManager, playerID PlayerID) *PlayerState {
	t.Helper()

	state, ok := rm.PlayerState(playerID)
	if !ok {
		t.Fatalf("player %d state missing", playerID)
	}
	return state
}
