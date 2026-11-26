package zztgo

// M42.6: a board's starting kit reaches every player once, not only the first
// to arrive. Owner-reported: on COTS24HR's "Apartment" an object opens with
// `#give gems 20` / `#give torches 120` and another runs #endgame when a
// `#take torches 1` fails, so a player arriving after somebody else had already
// been given the torches died on arrival.

import (
	"testing"
)

const m426Kit = "@kit\r#give gems 20\r#give torches 120\r"

// m426World is CONTROL with a kit object on board 1, away from the start.
func m426World(t *testing.T, program string) TWorld {
	t.Helper()
	world := m421RoomManager(t).world
	e := newSnapshotEngine()
	e.World = cloneWorld(world)
	e.BoardOpen(m421Board)
	e.Board.Tiles[40][5] = TTile{Element: E_EMPTY}
	e.AddStat(40, 5, E_OBJECT, 0x0F, 3, StatTemplateDefault)
	stat := &e.Board.Stats[e.Board.StatCount]
	stat.Data = program
	stat.DataLen = int16(len(program))
	stat.DataPos = 0
	e.BoardClose()
	return e.World
}

func m426Steps(rm *RoomManager, n int) {
	for i := 0; i < n; i++ {
		rm.Step(map[PlayerID]PlayerInput{})
	}
}

func m426Torches(rm *RoomManager, id PlayerID) int16 {
	return rm.players[id].state.Torches
}

func TestM426ObjectKitReadsTheOpeningGives(t *testing.T) {
	gives, end := objectKit(m426Kit)
	if len(gives) != 2 || gives[1].counter != "TORCHES" || gives[1].amount != 120 {
		t.Fatalf("kit = %+v", gives)
	}
	if int(end) != len(m426Kit) {
		t.Fatalf("kit ends at %d, want %d", end, len(m426Kit))
	}
	for _, notKit := range []string{
		"@vendor\r#end\r:touch\r#give ammo 5\r",
		"@x\r#if blue #give torches 5\r",
		"@x\r:start\r#give torches 5\r",
		"@x\r#take torches 1 out\r#give torches 5\r",
	} {
		if gives, _ := objectKit(notKit); len(gives) != 0 {
			t.Fatalf("%q read as a kit: %+v", notKit, gives)
		}
	}
}

// The reported bug: the first player gets the kit from the object itself; a
// second player who arrives after it has run gets it too.
func TestM426APlayerArrivingAfterTheKitRanStillGetsIt(t *testing.T) {
	rm := NewRoomManager(m426World(t, m426Kit))
	a := m421Join(t, rm)
	m426Steps(rm, 10)
	if got := m426Torches(rm, a); got != 120 {
		t.Fatalf("the first player has %d torches, want 120 from the object", got)
	}
	b := m421Join(t, rm)
	m426Steps(rm, 2)
	if got := m426Torches(rm, b); got != 120 {
		t.Fatalf("the second player has %d torches, want the board's kit (120)", got)
	}
	if got := m426Torches(rm, a); got != 120 {
		t.Fatalf("the first player now has %d torches; the kit was given twice", got)
	}
}

// The production case: the world comes back from an autosave written after the
// kit had run, so nobody on the restored board can be given it by the object.
func TestM426KitReachesAPlayerAfterAnAutosaveRestore(t *testing.T) {
	world := m426World(t, m426Kit)
	rm := m423Restored(t, world, func(rm *RoomManager) {
		a := m421Join(t, rm)
		m426Steps(rm, 10)
		rm.LeavePlayer(a)
	})
	b := m421Join(t, rm)
	m426Steps(rm, 2)
	if got := m426Torches(rm, b); got != 120 {
		t.Fatalf("the player on the restored board has %d torches, want 120", got)
	}
}

// Once per player per board: arriving again does not hand it out again.
func TestM426TheKitIsOncePerPlayerPerBoard(t *testing.T) {
	rm := NewRoomManager(m426World(t, m426Kit))
	a := m421Join(t, rm)
	m426Steps(rm, 10)
	b := m421Join(t, rm)
	m426Steps(rm, 2)
	rm.players[b].kitPending = true // as a passage back onto the board would
	m426Steps(rm, 2)
	if got := m426Torches(rm, b); got != 120 {
		t.Fatalf("a second arrival gave the kit again: %d torches", got)
	}
	_ = a
}

// A gift that needs a touch is not a kit; it still goes to whoever touches.
func TestM426ATouchGiftIsNotAKit(t *testing.T) {
	rm := NewRoomManager(m426World(t, "@chest\r#end\r:touch\r#give torches 5\r"))
	m421Join(t, rm)
	m426Steps(rm, 10)
	b := m421Join(t, rm)
	m426Steps(rm, 2)
	if got := m426Torches(rm, b); got != 0 {
		t.Fatalf("a touch gift was handed out on arrival: %d torches", got)
	}
}
