package zztgo

import (
	"strings"
	"testing"
)

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
	rm.publishRoomWorldScope(roomA)

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
	rm.publishRoomWorldScope(roomA)
	for _, boardID := range []int16{1, 2} {
		room, _ := rm.Room(boardID)
		if room.Engine.WorldGetFlagPosition("HASLENS") >= 0 {
			t.Errorf("room %d still observes cleared flag", boardID)
		}
	}
}

// worldScopeSetterWorld builds a three-board ZWD world (board 0 title, board 1
// "Setter", board 2 "Reader"). Board 1 carries an Object that runs `#set SHARED`
// on its first tick, so a single StepDiffs proves the seam: the flag the
// earlier-ticking room sets must be visible to the later-ticking room the same
// tick. The raw compiler does not expand RLE (that is a generation-path
// preprocess), so every grid row is a literal 60 chars.
func worldScopeSetterWorld() string {
	// scopeGrid builds a 25x60 dot grid, overwriting the 1-based cells named in
	// place with their glyph, and returns it as 25 newline-terminated rows.
	scopeGrid := func(place map[[2]int]byte) string {
		rows := make([][]byte, 25)
		for y := range rows {
			rows[y] = []byte(strings.Repeat(".", 60))
		}
		for pos, ch := range place {
			rows[pos[1]-1][pos[0]-1] = ch
		}
		var g strings.Builder
		for _, r := range rows {
			g.Write(r)
			g.WriteByte('\n')
		}
		return g.String()
	}
	player := map[[2]int]byte{{5, 5}: '@'}
	setter := map[[2]int]byte{{5, 5}: '@', {1, 11}: 'o'}

	var b strings.Builder
	b.WriteString("zwd 1\nworld \"SCOPE\"\n\n")

	b.WriteString("board \"Title\"\n  exits north none south none west none east none\n")
	b.WriteString("  start player at 5,5\n  grid\n")
	b.WriteString(scopeGrid(player))
	b.WriteString("  end\n  legend\n    . = Empty color 0x00\n    @ = Player color 0x1F under Empty color 0x00\n  end\n  end\n\n")

	b.WriteString("board \"Setter\"\n  exits north none south none west none east none\n")
	b.WriteString("  start player at 5,5\n  grid\n")
	b.WriteString(scopeGrid(setter))
	b.WriteString("  end\n  legend\n    . = Empty color 0x00\n    @ = Player color 0x1F under Empty color 0x00\n    o = Object color 0x0F under Empty color 0x00\n  end\n")
	b.WriteString("  stats\n    stat at 1,11 element Object cycle 1 under Empty color 0x00\n    oop\n    @setter\n    #set SHARED\n    #end\n    end\n  end\n  end\n\n")

	b.WriteString("board \"Reader\"\n  exits north none south none west none east none\n")
	b.WriteString("  start player at 5,5\n  grid\n")
	b.WriteString(scopeGrid(player))
	b.WriteString("  end\n  legend\n    . = Empty color 0x00\n    @ = Player color 0x1F under Empty color 0x00\n  end\n  end\n")
	return b.String()
}

// TestRoomManagerFlagVisibleToLaterRoomSameTick drives an actual StepDiffs: an
// object on board 1 (which ticks before board 2) sets SHARED, and board 2's
// player must observe it in that same StepDiffs — the refresh-before-step /
// publish-after-step seam ordering.
func TestRoomManagerFlagVisibleToLaterRoomSameTick(t *testing.T) {
	world, err := CompileZWDWorld(worldScopeSetterWorld())
	if err != nil {
		t.Fatalf("compile setter world: %v", err)
	}
	rm := NewRoomManager(world)
	setterPlayer := rm.JoinPlayer(1, 5, 5) // board 1 "Setter"
	readerPlayer := rm.JoinPlayer(2, 5, 5) // board 2 "Reader"
	if setterPlayer == readerPlayer {
		t.Fatal("expected distinct players")
	}

	reader, ok := rm.Room(2)
	if !ok {
		t.Fatal("reader room missing")
	}
	if reader.Engine.WorldGetFlagPosition("SHARED") >= 0 {
		t.Fatal("SHARED set before any step")
	}

	rm.StepDiffs(map[PlayerID]PlayerInput{})

	if rm.world.Info.Flags[0] != "SHARED" {
		t.Fatalf("shared world flag = %q, want SHARED", rm.world.Info.Flags[0])
	}
	if reader.Engine.WorldGetFlagPosition("SHARED") < 0 {
		t.Error("later-ticking Reader room did not observe SHARED set by Setter the same tick")
	}
}

// TestRoomManagerFlagSurvivesFreezeThaw sets a flag on a live room, empties it so
// freezeRoomIfEmpty publishes and drops it, then re-joins to thaw it, and asserts
// the flag survived the round trip through rm.world.
func TestRoomManagerFlagSurvivesFreezeThaw(t *testing.T) {
	rm := NewRoomManager(testMultiplayerSmokeWorld(t))
	player := rm.JoinPlayer(1, 10, 12)

	room, ok := rm.Room(1)
	if !ok {
		t.Fatal("room missing")
	}
	room.Engine.WorldSetFlag("SOLVED")
	rm.publishRoomWorldScope(room)

	// Emptying the room freezes it: the flag must be published to rm.world first.
	if !rm.LeavePlayer(player) {
		t.Fatal("LeavePlayer failed")
	}
	if _, live := rm.Room(1); live {
		t.Fatal("room should be frozen after last player left")
	}
	if rm.world.Info.Flags[0] != "SOLVED" {
		t.Fatalf("frozen world flag = %q, want SOLVED", rm.world.Info.Flags[0])
	}

	// Re-joining thaws the room from rm.world, which carries the flag.
	rm.JoinPlayer(1, 10, 12)
	thawed, ok := rm.Room(1)
	if !ok {
		t.Fatal("room missing after re-join")
	}
	if thawed.Engine.WorldGetFlagPosition("SOLVED") < 0 {
		t.Error("thawed room lost SOLVED across freeze/thaw")
	}
}
