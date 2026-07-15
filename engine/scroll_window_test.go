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

// statPos reads a player's tile straight out of the room engine.
func statPos(t *testing.T, rm *RoomManager, playerID PlayerID) (int16, int16) {
	t.Helper()

	boardID, statID, ok := rm.PlayerLocation(playerID)
	if !ok {
		t.Fatal("player has no location")
	}
	room, ok := rm.Room(boardID)
	if !ok {
		t.Fatal("player has no room")
	}
	stat := room.Engine.Board.Stats[statID]
	return int16(stat.X), int16(stat.Y)
}

// freeStep finds a direction from x,y into empty floor, so a test can tell
// "the player was frozen" apart from "the player was walled in".
func freeStep(t *testing.T, room *Room, x, y int16) (int16, int16) {
	t.Helper()

	for _, d := range [][2]int16{{0, 1}, {0, -1}, {-1, 0}, {1, 0}} {
		if room.Engine.Board.Tiles[x+d[0]][y+d[1]].Element == E_EMPTY {
			return d[0], d[1]
		}
	}
	t.Fatalf("no empty tile beside %d,%d", x, y)
	return 0, 0
}

// Vanilla's text window blocks the game loop, so a player reading a scroll
// cannot walk. Here only the reader freezes: the room plays on for everybody
// else. Without the freeze the reader keeps moving for the tick or two it takes
// their "stop" to reach the server, steps onto the next scroll, and that second
// scroll overwrites the first before it can be read.
func TestScrollFreezesReaderUntilDismissed(t *testing.T) {
	rm, room, _, standX, standY, stepX, stepY := vendorRoom(t)
	toucher := rm.JoinPlayer(vendorBoard, standX, standY)
	rm.Snapshot(toucher)

	event := stepUntilScroll(t, rm, toucher, map[PlayerID]PlayerInput{
		toucher: {DeltaX: stepX, DeltaY: stepY},
	}, 12)

	// The toucher never moved: they walked into the vendor. Escaping needs a
	// direction that is actually open, or "did not move" would prove nothing.
	frozenX, frozenY := statPos(t, rm, toucher)
	awayX, awayY := freeStep(t, room, frozenX, frozenY)
	tickBefore := room.Engine.CurrentTick

	for i := 0; i < 8; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{toucher: {DeltaX: awayX, DeltaY: awayY}})
	}

	if x, y := statPos(t, rm, toucher); x != frozenX || y != frozenY {
		t.Errorf("reader walked to %d,%d with a scroll open; want frozen at %d,%d", x, y, frozenX, frozenY)
	}
	if room.Engine.CurrentTick == tickBefore {
		t.Error("room stopped ticking behind the scroll; only the reader should freeze")
	}

	// An empty label is the client's "I closed it": no hyperlink, but the
	// reader is released.
	if !rm.SubmitScrollReply(toucher, event.StatID, "") {
		t.Fatal("SubmitScrollReply failed")
	}
	for i := 0; i < 4; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{toucher: {DeltaX: awayX, DeltaY: awayY}})
	}
	if x, y := statPos(t, rm, toucher); x == frozenX && y == frozenY {
		t.Errorf("reader still frozen at %d,%d after dismissing the scroll", x, y)
	}
}

// scrollHyperlinkWorldZWD is a one-board world with a Scroll whose reward is
// gated behind a `!go` hyperlink — the M17.4 regression fixture. The `#end`
// after the hyperlink halts the initial run so `#give ammo 9` is reachable only
// via the reply, exactly like a shop object's `:label` branch.
const scrollHyperlinkWorldZWD = `zwd 1
world "SCROLLHL"

board "Room"
  start player at 30,12
  max-shots 4
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
############################################################
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................@!............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
############################################################
  end

  legend
    # = Solid color 0x0E
    . = Empty color 0x0F
    @ = Player color 0x1F under Empty color 0x00
    ! = Scroll color 0x0F
  end

  stats
    stat at 31,12 element Scroll cycle 1 under Empty color 0x00
    oop
@Reward
Take the prize?
!go;Yes please
#end
:go
#give ammo 9
#end
    end
  end
end
`

func loadScrollHyperlinkEngine(t *testing.T) *Engine {
	t.Helper()
	world, err := CompileZWDWorld(scrollHyperlinkWorldZWD)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(0)
	return e
}

func scrollTileIs(e *Engine, x, y int16) bool {
	return e.Board.Tiles[x][y].Element == E_SCROLL
}

// M17.4: touching a Scroll with a hyperlink must not delete it before the reply
// can reach it. Selecting the hyperlink runs the `:label`, then the scroll is
// consumed.
func TestScrollHyperlinkReplyGrantsRewardThenConsumes(t *testing.T) {
	e := loadScrollHyperlinkEngine(t)
	dx, dy := int16(1), int16(0)
	e.Events = nil
	e.ElementScrollTouch(31, 12, 0, &dx, &dy)

	var evStatId int16 = -1
	for _, ev := range e.Events {
		if s, ok := ev.(ScrollEvent); ok {
			evStatId = s.StatId
		}
	}
	if evStatId < 1 {
		t.Fatalf("no ScrollEvent emitted (statId=%d)", evStatId)
	}
	if !scrollTileIs(e, 31, 12) {
		t.Fatal("scroll was removed on touch; the reply would have no target")
	}
	if e.PlayerFor(0).Ammo != 0 {
		t.Fatalf("reward granted before the hyperlink was selected: ammo=%d", e.PlayerFor(0).Ammo)
	}

	e.SubmitScrollReply(evStatId, "go")
	for i := 0; i < 3; i++ {
		e.GameStepWithInputs(map[int16]PlayerInput{})
	}
	if e.PlayerFor(0).Ammo != 9 {
		t.Fatalf("hyperlink reward not granted: ammo=%d, want 9", e.PlayerFor(0).Ammo)
	}
	if scrollTileIs(e, 31, 12) {
		t.Fatal("scroll not consumed after its reply was answered")
	}
}

// A dismissed scroll (empty reply, no hyperlink) is still consumed, and grants
// nothing.
func TestScrollDismissConsumesWithoutReward(t *testing.T) {
	e := loadScrollHyperlinkEngine(t)
	dx, dy := int16(1), int16(0)
	e.Events = nil
	e.ElementScrollTouch(31, 12, 0, &dx, &dy)
	var evStatId int16 = -1
	for _, ev := range e.Events {
		if s, ok := ev.(ScrollEvent); ok {
			evStatId = s.StatId
		}
	}
	e.SubmitScrollReply(evStatId, "") // dismiss
	for i := 0; i < 3; i++ {
		e.GameStepWithInputs(map[int16]PlayerInput{})
	}
	if e.PlayerFor(0).Ammo != 0 {
		t.Fatalf("dismiss should grant nothing: ammo=%d", e.PlayerFor(0).Ammo)
	}
	if scrollTileIs(e, 31, 12) {
		t.Fatal("dismissed scroll not consumed")
	}
}
