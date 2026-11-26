package zztgo

// M42.4 (owner rule, 2026-11-25): a joiner arrives on the board's start unless
// a player or object stands there; then one square north, east, south or west,
// in that order (M42.2's); and with all five taken the join is refused with a
// message, not placed further out. Joins only: a passage or board edge still
// names its entry square, and respawn and re-enter keep FindPlacement.

import (
	"testing"
)

// m424StepAside moves a joined player well clear of their board's start, so a
// test that joins many players to one board for some other purpose (load,
// fan-out, identity) is not refused by M42.4. It holds inst.mu, as the tick
// does, and picks the first open square scanning up from the bottom-right
// corner that is more than one square from the start: deterministic, and far
// from the four squares the next joiner needs.
func m424StepAside(t *testing.T, inst *WorldInstance, id PlayerID) {
	t.Helper()
	inst.mu.Lock()
	defer inst.mu.Unlock()
	rm := inst.RoomManager
	player := rm.players[id]
	if player == nil {
		t.Fatalf("player %d is not on any board", id)
	}
	room := rm.rooms[player.boardID]
	for y := int16(BOARD_HEIGHT); y >= 1; y-- {
		for x := int16(BOARD_WIDTH); x >= 1; x-- {
			if absInt16(x-room.startX)+absInt16(y-room.startY) <= 1 {
				continue
			}
			if room.Engine.PlacementOpen(x, y, -1) {
				room.Engine.MoveStat(player.statID, x, y)
				return
			}
		}
	}
	t.Fatalf("no open square on board %d to move player %d to", player.boardID, id)
}

// The owner's rule, square by square: start, north, east, south, west, refused.
func TestM424JoinTakesTheStartThenEachSideThenIsRefused(t *testing.T) {
	rm := m421RoomManager(t)
	want := [][2]int16{
		{m421StartX, m421StartY},
		{m421StartX, m421StartY - 1},
		{m421StartX + 1, m421StartY},
		{m421StartX, m421StartY + 1},
		{m421StartX - 1, m421StartY},
	}
	for i, w := range want {
		id := m421Join(t, rm)
		m423WantAt(t, rm, id, w[0], w[1], "joiner")
		_ = i
	}
	room := rm.rooms[m421Board]
	statsBefore := room.Engine.Board.StatCount
	id, refusal := rm.JoinPlayerChecked(99, m421Board, 0, 0)
	if id != 0 || refusal != JoinStartCrowded {
		t.Fatalf("the sixth joiner got (%d, %v), want (0, JoinStartCrowded)", id, refusal)
	}
	if room.Engine.Board.StatCount != statsBefore {
		t.Fatal("a refused join changed the board's stats")
	}
}

// A side square counts only if it is empty and nobody holds it: a wall or an
// object beside the start is skipped, not stood on.
func TestM424JoinSkipsABlockedSide(t *testing.T) {
	rm := m421RoomManager(t)
	m421Join(t, rm)
	room := rm.rooms[m421Board]
	room.Engine.Board.Tiles[m421StartX][m421StartY-1] = TTile{Element: E_NORMAL, Color: 0x0E}
	id := m421Join(t, rm)
	m423WantAt(t, rm, id, m421StartX+1, m421StartY, "the joiner with north walled off")
}

// An object on the start is "something there" just as a player is.
func TestM424AnObjectOnTheStartSendsTheJoinerNorth(t *testing.T) {
	rm := m421RoomManager(t)
	a := m421Join(t, rm)
	room := rm.rooms[m421Board]
	room.Engine.MoveStat(rm.players[a].statID, 16, 12)
	room.Engine.AddStat(m421StartX, m421StartY, E_OBJECT, 0x0F, 1, StatTemplateDefault)
	id := m421Join(t, rm)
	m423WantAt(t, rm, id, m421StartX, m421StartY-1, "the joiner with an object on the start")
}

// Once someone walks off, the start is free again and the next joiner gets it.
func TestM424TheStartFreesUpWhenItsPlayerWalksOff(t *testing.T) {
	rm := m421RoomManager(t)
	ids := make([]PlayerID, 5)
	for i := range ids {
		ids[i] = m421Join(t, rm)
	}
	rm.rooms[m421Board].Engine.MoveStat(rm.players[ids[0]].statID, 16, 12)
	id := m421Join(t, rm)
	m423WantAt(t, rm, id, m421StartX, m421StartY, "the joiner after the start emptied")
}

// The refusal the player reads is its own, not "Board full".
func TestM424CrowdedStartHasItsOwnMessage(t *testing.T) {
	crowded := joinRefusedMessage(JoinStartCrowded)
	full := joinRefusedMessage(JoinBoardFull)
	if crowded.Event.Title == full.Event.Title {
		t.Fatalf("a crowded start reads %q, the same as a full board", crowded.Event.Title)
	}
	if crowded.Event.Title != "Start crowded" {
		t.Fatalf("crowded-start title = %q", crowded.Event.Title)
	}
}
