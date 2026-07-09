package zztgo

import (
	"strings"
	"testing"
)

// findVendor locates TOWN's Vendor object, whose #TOUCH code is the scroll
// fixture named in TASKS.md M3.10.
func findVendor(t *testing.T, room *Room) (statID int16, x, y int16) {
	t.Helper()

	engine := room.Engine
	for id := int16(1); id <= engine.Board.StatCount; id++ {
		stat := engine.Board.Stats[id]
		if engine.Board.Tiles[stat.X][stat.Y].Element != E_OBJECT {
			continue
		}
		if strings.Contains(stat.Data, "!ba;Ammunition") {
			return id, int16(stat.X), int16(stat.Y)
		}
	}
	t.Fatal("vendor object not found on board")
	return 0, 0, 0
}

// vendorBoard is TOWN's "Armory", where the Vendor object lives.
const vendorBoard = int16(2)

// vendorRoom brings up the room (a room only exists once someone joins), then
// reports the vendor and an empty tile beside it plus the step that touches it.
func vendorRoom(t *testing.T) (rm *RoomManager, room *Room, vendorStat, standX, standY, stepX, stepY int16) {
	t.Helper()

	rm = townRoomManager(t)
	rm.JoinPlayer(vendorBoard, 0, 0) // seed: creates the room
	room, ok := rm.Room(vendorBoard)
	if !ok {
		t.Fatal("vendor room missing")
	}
	vendorStat, vx, vy := findVendor(t, room)

	for _, d := range [][2]int16{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		x, y := vx+d[0], vy+d[1]
		if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
			continue
		}
		if room.Engine.Board.Tiles[x][y].Element == E_EMPTY {
			return rm, room, vendorStat, x, y, -d[0], -d[1]
		}
	}
	t.Fatal("no empty tile beside the vendor")
	return
}

// stepUntilScroll runs the room until a scroll event appears, or gives up.
func stepUntilScroll(t *testing.T, rm *RoomManager, playerID PlayerID, inputs map[PlayerID]PlayerInput, maxSteps int) ProtocolEvent {
	t.Helper()

	for i := 0; i < maxSteps; i++ {
		diffs := rm.StepDiffs(inputs)
		inputs = map[PlayerID]PlayerInput{}
		if event, ok := findEvent(diffs[playerID].Events, "scroll"); ok {
			return event
		}
	}
	t.Fatalf("no scroll event after %d steps", maxSteps)
	return ProtocolEvent{}
}

// Touching the vendor must produce the exact scroll from the task spec, tagged
// with the touching player so only they see it, and carrying the object's stat
// id so a selection can be routed back.
func TestVendorScrollEventFromTouch(t *testing.T) {
	rm, _, vendorStat, standX, standY, stepX, stepY := vendorRoom(t)

	playerID := rm.JoinPlayer(vendorBoard, standX, standY)
	if _, ok := rm.Snapshot(playerID); !ok {
		t.Fatal("snapshot failed")
	}
	_, playerStat, _ := rm.PlayerLocation(playerID)

	event := stepUntilScroll(t, rm, playerID, map[PlayerID]PlayerInput{
		playerID: {DeltaX: stepX, DeltaY: stepY},
	}, 12)

	if event.Title != "Vendor" {
		t.Fatalf("scroll title=%q, want %q", event.Title, "Vendor")
	}
	if event.StatID != vendorStat {
		t.Fatalf("scroll StatID=%d, want vendor stat %d", event.StatID, vendorStat)
	}
	if event.PlayerStatID != playerStat {
		t.Fatalf("scroll PlayerStatID=%d, want toucher %d", event.PlayerStatID, playerStat)
	}

	joined := strings.Join(event.Lines, "\n")
	for _, want := range []string{
		"Hello, you must be new to town!",
		"!ba;Ammunition, 3 shots.........1 gem",
		"!bt;Torch.......................1 gem",
		"!bx;Advice......................Free",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("scroll missing %q; got:\n%s", want, joined)
		}
	}
}

// Selecting "!ba;..." sends label "ba" to the vendor, whose code trades one gem
// for three ammo. This is the full round trip the browser performs.
func TestVendorScrollReplyRunsLabel(t *testing.T) {
	rm, _, _, standX, standY, stepX, stepY := vendorRoom(t)
	playerID := rm.JoinPlayer(vendorBoard, standX, standY)
	rm.Snapshot(playerID)

	state, _ := rm.PlayerState(playerID)
	state.Gems = 5
	state.Ammo = 0

	event := stepUntilScroll(t, rm, playerID, map[PlayerID]PlayerInput{
		playerID: {DeltaX: stepX, DeltaY: stepY},
	}, 12)

	if !rm.SubmitScrollReply(playerID, event.StatID, "ba") {
		t.Fatal("SubmitScrollReply failed")
	}
	for i := 0; i < 12 && state.Ammo == 0; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{})
	}

	if state.Ammo != 3 {
		t.Fatalf("ammo=%d, want 3 after buying from the vendor", state.Ammo)
	}
	if state.Gems != 4 {
		t.Fatalf("gems=%d, want 4 (one gem spent)", state.Gems)
	}
}

// A scroll must reach only the player who touched the object; a bystander on
// the same board must not have a window opened on them.
func TestVendorScrollTargetsOnlyToucher(t *testing.T) {
	rm, _, _, standX, standY, stepX, stepY := vendorRoom(t)

	toucher := rm.JoinPlayer(vendorBoard, standX, standY)
	bystander := rm.JoinPlayer(vendorBoard, standX-3, standY)
	rm.Snapshot(toucher)
	rm.Snapshot(bystander)

	_, toucherStat, _ := rm.PlayerLocation(toucher)
	_, bystanderStat, _ := rm.PlayerLocation(bystander)
	if toucherStat == bystanderStat {
		t.Fatal("players share a stat id")
	}

	event := stepUntilScroll(t, rm, toucher, map[PlayerID]PlayerInput{
		toucher: {DeltaX: stepX, DeltaY: stepY},
	}, 12)

	if event.PlayerStatID != toucherStat {
		t.Fatalf("scroll PlayerStatID=%d, want toucher %d (bystander is %d)",
			event.PlayerStatID, toucherStat, bystanderStat)
	}
}
