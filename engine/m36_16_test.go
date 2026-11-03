package zztgo

import (
	"fmt"
	"strings"
	"testing"
)

// M36.16: a scroll reply must answer a scroll this player was shown, with a
// label that scroll offered. The reply's stat id and label both come straight
// off the wire, so before this a client could send any object to any label at
// any time — buy from a vendor it never visited, run a label no hyperlink
// offered, or delete a Scroll element from the board with a dismissal it was
// never owed (GameStepWithInputs removes a Scroll on any reply).

// m36_16Board is the play board of m36_16World.
const m36_16Board = int16(1)

// Positions on the play board. The shop is touched by walking east from
// m36_16Reader; the scroll element sits apart so nothing touches it by accident.
var (
	m36_16Reader = [2]int16{30, 12}
	m36_16Shop   = [2]int16{31, 12}
	m36_16Scroll = [2]int16{20, 18}
	m36_16Crier  = [2]int16{45, 5}
	// m36_16Start is where the board's own player tile stands, out of the way:
	// the test's players join at explicit positions.
	m36_16Start = [2]int16{5, 22}
)

// m36_16CrierOnce speaks to the board once; m36_16CrierLoop speaks every tick.
const (
	m36_16CrierOnce = `@Crier
Hear ye, hear ye.
!hear;I hear you
#end
:hear
#end
`
	m36_16CrierLoop = `@Crier
:top
Hear ye, hear ye.
!hear;I hear you
/i
#top
`
)

func m36_16Grid(cells map[[2]int16]byte) string {
	var b strings.Builder
	for y := int16(1); y <= BOARD_HEIGHT; y++ {
		for x := int16(1); x <= BOARD_WIDTH; x++ {
			switch ch, ok := cells[[2]int16{x, y}]; {
			case ok:
				b.WriteByte(ch)
			case x == 1 || y == 1 || x == BOARD_WIDTH || y == BOARD_HEIGHT:
				b.WriteByte('#')
			default:
				b.WriteByte('.')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// m36_16World has, on board 1: a Shop that shows a scroll on touch offering
// only `!go`, but whose code also holds a `:secret` label worth far more; a
// Scroll element whose `!take` reward is reachable only by its hyperlink; and,
// when crier is set, a Crier running that program, which opens a scroll to the
// whole board from its own code (no toucher), as an object announcing
// something does.
func m36_16World(t *testing.T, crier string) TWorld {
	t.Helper()
	cells := map[[2]int16]byte{m36_16Shop: 'o', m36_16Scroll: '!', m36_16Start: '@'}
	stats := fmt.Sprintf(`    stat at %d,%d element Object cycle 1 p1 cp437:0x02 step idle under Empty color 0x00
    oop
@Shop
#end
:touch
Buy something?
!go;Ammunition, 9 shots
#end
:go
#give ammo 9
#end
:secret
#give ammo 50
#end
    end
    stat at %d,%d element Scroll cycle 1 under Empty color 0x00
    oop
@Prize
Take the prize?
!take;Yes please
#end
:take
#give ammo 7
#end
    end
`, m36_16Shop[0], m36_16Shop[1], m36_16Scroll[0], m36_16Scroll[1])
	if crier != "" {
		cells[m36_16Crier] = 'c'
		stats += fmt.Sprintf(`    stat at %d,%d element Object cycle 1 p1 cp437:0x02 step idle under Empty color 0x00
    oop
%s    end
`, m36_16Crier[0], m36_16Crier[1], crier)
	}
	source := fmt.Sprintf(`zwd 1
world "SCROLLGATE"

board "Title"
  start player at 30,13
  max-shots 10
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
%s  end

  legend
    # = Solid color 0x01
    . = Empty color 0x00
    @ = Player color 0x1F under Empty color 0x00
  end
end

board "Shop"
  start player at %d,%d
  max-shots 10
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
%s  end

  legend
    # = Solid color 0x01
    . = Empty color 0x00
    @ = Player color 0x1F under Empty color 0x00
    o = Object color 0x0E
    c = Object color 0x0D
    ! = Scroll color 0x0F
  end

  stats
%s  end
end
`, m36_16Grid(map[[2]int16]byte{{30, 13}: '@'}), m36_16Start[0], m36_16Start[1], m36_16Grid(cells), stats)
	world, err := CompileZWDWorld(source)
	if err != nil {
		t.Fatalf("CompileZWDWorld: %v", err)
	}
	return world
}

// m36_16StatAt finds the stat standing at pos on the play board.
func m36_16StatAt(t *testing.T, rm *RoomManager, pos [2]int16) int16 {
	t.Helper()
	room, ok := rm.Room(m36_16Board)
	if !ok {
		t.Fatal("play room missing")
	}
	id := room.Engine.GetStatIdAt(pos[0], pos[1])
	if id < 1 {
		t.Fatalf("no stat at %v", pos)
	}
	return id
}

func m36_16Ammo(t *testing.T, rm *RoomManager, playerID PlayerID) int16 {
	t.Helper()
	state, ok := rm.PlayerState(playerID)
	if !ok {
		t.Fatal("no player state")
	}
	return state.Ammo
}

func m36_16Steps(rm *RoomManager, n int) {
	for i := 0; i < n; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{})
	}
}

// m36_16TouchShop walks the reader into the shop and returns its scroll.
func m36_16TouchShop(t *testing.T, rm *RoomManager, playerID PlayerID) ProtocolEvent {
	t.Helper()
	event := stepUntilScroll(t, rm, playerID, map[PlayerID]PlayerInput{playerID: {DeltaX: 1}}, 12)
	if event.Title != "Shop" {
		t.Fatalf("scroll title = %q, want Shop", event.Title)
	}
	return event
}

// A reply to a scroll nobody showed this player is dropped: the shop never
// sells, and the reply is not taken for a dismissal either.
func TestM3616UnsolicitedScrollReplyIsIgnored(t *testing.T) {
	rm := NewRoomManager(m36_16World(t, ""))
	playerID := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1])
	rm.Snapshot(playerID)
	shop := m36_16StatAt(t, rm, m36_16Shop)

	for _, label := range []string{"go", "secret", ""} {
		if rm.SubmitScrollReply(playerID, shop, label) {
			t.Errorf("unsolicited reply %q to the shop was accepted", label)
		}
	}
	m36_16Steps(rm, 6)
	if ammo := m36_16Ammo(t, rm, playerID); ammo != 0 {
		t.Fatalf("ammo = %d after unsolicited replies, want 0", ammo)
	}
}

// A label the open scroll did not offer is dropped, even one the object's code
// really has. The offered label still works afterwards, exactly once.
func TestM3616ForgedScrollLabelIsIgnored(t *testing.T) {
	rm := NewRoomManager(m36_16World(t, ""))
	playerID := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1])
	rm.Snapshot(playerID)
	event := m36_16TouchShop(t, rm, playerID)

	if rm.SubmitScrollReply(playerID, event.StatID, "secret") {
		t.Error("forged label :secret was accepted; the scroll offers only !go")
	}
	// The wrong object, offering the right label, is a forgery too.
	if scroll := m36_16StatAt(t, rm, m36_16Scroll); rm.SubmitScrollReply(playerID, scroll, "go") {
		t.Error("label sent to an object that showed no scroll was accepted")
	}
	m36_16Steps(rm, 6)
	if ammo := m36_16Ammo(t, rm, playerID); ammo != 0 {
		t.Fatalf("ammo = %d after a forged label, want 0 (:secret gives 50)", ammo)
	}

	// The scroll is still open, so its real hyperlink still answers it. The case
	// differs from the line's, and OopSend matches labels without case.
	if !rm.SubmitScrollReply(playerID, event.StatID, "GO") {
		t.Fatal("the offered label was refused")
	}
	m36_16Steps(rm, 6)
	if ammo := m36_16Ammo(t, rm, playerID); ammo != 9 {
		t.Fatalf("ammo = %d after !go, want 9", ammo)
	}
	// One scroll, one answer: replaying it buys nothing more.
	if rm.SubmitScrollReply(playerID, event.StatID, "go") {
		t.Error("a second reply to an answered scroll was accepted")
	}
	m36_16Steps(rm, 6)
	if ammo := m36_16Ammo(t, rm, playerID); ammo != 9 {
		t.Fatalf("ammo = %d after a replayed reply, want still 9", ammo)
	}
}

// A dismissal removes a Scroll element (the RemoveStat ElementScrollTouch
// defers to the reply), so one nobody was owed must not be honoured: without
// the gate any client could clear every scroll off a board, unread.
func TestM3616ForgedDismissalLeavesTheScroll(t *testing.T) {
	rm := NewRoomManager(m36_16World(t, ""))
	playerID := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1])
	rm.Snapshot(playerID)
	scroll := m36_16StatAt(t, rm, m36_16Scroll)

	for _, label := range []string{"", "take"} {
		if rm.SubmitScrollReply(playerID, scroll, label) {
			t.Errorf("unsolicited reply %q to the scroll was accepted", label)
		}
	}
	m36_16Steps(rm, 4)
	room, _ := rm.Room(m36_16Board)
	if room.Engine.Board.Tiles[m36_16Scroll[0]][m36_16Scroll[1]].Element != E_SCROLL {
		t.Fatal("a forged reply removed the scroll from the board")
	}
	if ammo := m36_16Ammo(t, rm, playerID); ammo != 0 {
		t.Fatalf("ammo = %d after a forged !take, want 0", ammo)
	}
}

// An object that speaks from its own code, with nobody touching it, opens its
// scroll on every client on the board (isMyScroll), and each of them answers
// it. The gate must owe each of them that answer — and only one each.
func TestM3616BoardWideScrollIsAnsweredByEveryReader(t *testing.T) {
	rm := NewRoomManager(m36_16World(t, m36_16CrierOnce))
	first := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1])
	second := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1]+3)
	rm.Snapshot(first)
	rm.Snapshot(second)

	var event ProtocolEvent
	found := false
	for i := 0; i < 6 && !found; i++ {
		diffs := rm.StepDiffs(map[PlayerID]PlayerInput{})
		var inSecond bool
		event, found = findEvent(diffs[first].Events, "scroll")
		_, inSecond = findEvent(diffs[second].Events, "scroll")
		if found != inSecond {
			t.Fatal("the crier's scroll reached only one of the two readers")
		}
	}
	if !found {
		t.Fatal("the crier never spoke")
	}
	if event.Title != "Crier" || event.PlayerStatID >= 0 {
		t.Fatalf("scroll = %q for reader %d, want the Crier's, to nobody in particular", event.Title, event.PlayerStatID)
	}

	if !rm.SubmitScrollReply(first, event.StatID, "hear") {
		t.Error("the first reader's hyperlink was refused")
	}
	if !rm.SubmitScrollReply(second, event.StatID, "") {
		t.Error("the second reader's dismissal was refused")
	}
	if rm.SubmitScrollReply(first, event.StatID, "hear") {
		t.Error("the first reader answered the same scroll twice")
	}
	if rm.SubmitScrollReply(second, event.StatID, "hear") {
		t.Error("the second reader answered after dismissing")
	}
}

// A touched scroll is owed to the player who touched it, not to everyone who
// can name the object's stat.
func TestM3616TouchedScrollIsOwedOnlyToItsReader(t *testing.T) {
	rm := NewRoomManager(m36_16World(t, ""))
	reader := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1])
	bystander := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1]+3)
	rm.Snapshot(reader)
	rm.Snapshot(bystander)
	event := m36_16TouchShop(t, rm, reader)

	if rm.SubmitScrollReply(bystander, event.StatID, "go") {
		t.Error("a bystander answered the reader's scroll")
	}
	m36_16Steps(rm, 6)
	if ammo := m36_16Ammo(t, rm, bystander); ammo != 0 {
		t.Fatalf("bystander ammo = %d, want 0", ammo)
	}
	if ammo := m36_16Ammo(t, rm, reader); ammo != 0 {
		t.Fatalf("reader ammo = %d after the bystander's forgery, want 0", ammo)
	}
}

// A Gazette notice (M34.3) is a scroll the server writes in place of the
// stand's own, and the client dismisses it like any other. The gate must owe the
// reader that dismissal, or the reader stays frozen; it offers no hyperlink, so
// the stand's own labels are not owed.
func TestM3616NoticeIsAnsweredByItsDismissal(t *testing.T) {
	tile := gazetteStandTile
	rm := NewRoomManager(gazetteStandWorld(t))
	rm.NoticeTiles = map[TransitGateKey]string{tile: GazetteNoticeKind}
	player := rm.JoinPlayer(tile.BoardID, tile.X, tile.Y-1)

	gazetteStepUntilScroll(rm, player)
	notices := rm.DrainNotices()
	if len(notices) != 1 {
		t.Fatalf("notices = %+v, want one", notices)
	}
	stand := notices[0].ObjectStatID
	if rm.SubmitScrollReply(player, stand, "touch") {
		t.Error("a label was accepted on a server-written notice, which offers none")
	}
	if !rm.SubmitScrollReply(player, stand, "") {
		t.Fatal("the notice's dismissal was refused")
	}
	if rm.players[player].scrollOpen {
		t.Error("the reader is still frozen after dismissing the notice")
	}
}

// The bound on open scrolls must never drop the one that froze the reader. An
// object that talks to the board every tick fills the list behind it; if the
// touched scroll were evicted, its dismissal would be refused and the reader
// would stay frozen until they left the board.
func TestM3616ChattyBoardCannotStrandAFrozenReader(t *testing.T) {
	rm := NewRoomManager(m36_16World(t, m36_16CrierLoop))
	playerID := rm.JoinPlayer(m36_16Board, m36_16Reader[0], m36_16Reader[1])
	rm.Snapshot(playerID)

	var shop ProtocolEvent
	found := false
	for i := 0; i < 12 && !found; i++ {
		diffs := rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {DeltaX: 1}})
		for _, ev := range diffs[playerID].Events {
			if ev.Type == "scroll" && ev.Title == "Shop" {
				shop, found = ev, true
			}
		}
	}
	if !found {
		t.Fatal("the shop never opened its scroll")
	}

	announcements := 0
	for i := 0; i < 3*maxOpenScrolls; i++ {
		diffs := rm.StepDiffs(map[PlayerID]PlayerInput{})
		for _, ev := range diffs[playerID].Events {
			if ev.Type == "scroll" && ev.Title == "Crier" {
				announcements++
			}
		}
	}
	if announcements <= maxOpenScrolls {
		t.Fatalf("the crier spoke %d times; the test needs more than %d to overflow the list", announcements, maxOpenScrolls)
	}
	if n := len(rm.players[playerID].openScrolls); n > maxOpenScrolls {
		t.Fatalf("%d open scrolls, want at most %d", n, maxOpenScrolls)
	}
	if !rm.SubmitScrollReply(playerID, shop.StatID, "") {
		t.Fatal("the reader's dismissal was refused after the crier filled the list")
	}
	if rm.players[playerID].scrollOpen {
		t.Fatal("the reader is still frozen after dismissing the shop")
	}
}
