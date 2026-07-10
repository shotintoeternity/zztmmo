package zztgo

import "testing"

// M7.1 — vanilla authored start squares win over terrain. A fake wall, floor
// art, or other walkable non-empty tile at Board.Info.StartPlayerX/Y must not
// make a fresh joiner drift to the nearest truly empty square.

func testFakeStartWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.Info.CurrentBoard = 1
	setup.World.BoardCount = 1
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Info.StartPlayerX = 10
	setup.Board.Info.StartPlayerY = 12
	setup.Board.Tiles[10][12] = TTile{Element: E_FAKE, Color: 0x0F}
	setup.BoardClose()
	return setup.World
}

func roomPlayerStat(t *testing.T, rm *RoomManager, playerID PlayerID) *TStat {
	t.Helper()

	player := rm.players[playerID]
	if player == nil {
		t.Fatalf("player %d not found", playerID)
	}
	room := rm.rooms[player.boardID]
	if room == nil {
		t.Fatalf("room %d not found", player.boardID)
	}
	return &room.Engine.Board.Stats[player.statID]
}

func TestM71FakeStartSquareUsesAuthoredStart(t *testing.T) {
	rm := NewRoomManager(testFakeStartWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)
	stat := roomPlayerStat(t, rm, playerID)

	if stat.X != 10 || stat.Y != 12 {
		t.Fatalf("player spawned at (%d,%d), want authored fake-wall start (10,12)", stat.X, stat.Y)
	}
	if stat.Under.Element != E_FAKE {
		t.Fatalf("player under tile element=%d, want E_FAKE (%d)", stat.Under.Element, E_FAKE)
	}
}

func TestM71SecondPlayerDisplacedFromOccupiedStart(t *testing.T) {
	rm := NewRoomManager(testFakeStartWorld(t))
	first := rm.JoinPlayer(1, 0, 0)
	second := rm.JoinPlayer(1, 0, 0)

	firstStat := roomPlayerStat(t, rm, first)
	secondStat := roomPlayerStat(t, rm, second)

	if firstStat.X != 10 || firstStat.Y != 12 {
		t.Fatalf("first player moved to (%d,%d), want to keep start (10,12)", firstStat.X, firstStat.Y)
	}
	if secondStat.X == firstStat.X && secondStat.Y == firstStat.Y {
		t.Fatalf("second player overlapped occupied start at (%d,%d)", secondStat.X, secondStat.Y)
	}
}

func TestM71ClobberedPlayerTileStillCountsOccupied(t *testing.T) {
	rm := NewRoomManager(testFakeStartWorld(t))
	first := rm.JoinPlayer(1, 0, 0)
	firstStat := roomPlayerStat(t, rm, first)

	room := rm.rooms[rm.players[first].boardID]
	room.Engine.Board.Tiles[firstStat.X][firstStat.Y] = TTile{Element: E_EMPTY}

	second := rm.JoinPlayer(1, 0, 0)
	secondStat := roomPlayerStat(t, rm, second)

	if secondStat.X == firstStat.X && secondStat.Y == firstStat.Y {
		t.Fatalf("second player overlapped a stat-held start square whose tile was clobbered")
	}
}
