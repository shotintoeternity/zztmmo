package zztgo

import "testing"

func TestRoomManagerSharesWorldFlagsAcrossLiveRooms(t *testing.T) {
	rm := NewRoomManager(testMultiplayerSmokeWorld(t))
	playerA := rm.JoinPlayer(1, 10, 12)
	playerB := rm.JoinPlayer(2, 10, 12)
	if playerA == playerB {
		t.Fatal("expected distinct players")
	}

	roomA, ok := rm.Room(1)
	if !ok {
		t.Fatal("room A missing")
	}
	if _, ok := rm.Room(2); !ok {
		t.Fatal("room B missing")
	}

	roomA.Engine.WorldSetFlag("HASLENS")
	rm.syncWorldFlagsFromRoom(roomA)

	if rm.world.Info.Flags[0] != "HASLENS" {
		t.Fatalf("shared world flag = %q, want HASLENS", rm.world.Info.Flags[0])
	}
	for _, boardID := range []int16{1, 2} {
		room, _ := rm.Room(boardID)
		if room.Engine.WorldGetFlagPosition("HASLENS") < 0 {
			t.Errorf("room %d cannot observe HASLENS", boardID)
		}
	}

	roomA.Engine.WorldClearFlag("HASLENS")
	rm.syncWorldFlagsFromRoom(roomA)
	for _, boardID := range []int16{1, 2} {
		room, _ := rm.Room(boardID)
		if room.Engine.WorldGetFlagPosition("HASLENS") >= 0 {
			t.Errorf("room %d still observes cleared flag", boardID)
		}
	}
}
