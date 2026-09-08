package zztgo

import "testing"

// Placement reachability (owner-reported, 2026-09-08).
//
// A player walked off a board edge and arrived unable to move: E_PLAYER had
// been written into the middle of a wall, all four neighbours wall. Two holes
// let that happen, and both are covered here.
//
// 1. A *named* arrival square (a passage's far end, a board edge's mirrored
//    square) was accepted on the strength of not already showing another
//    player. A wall is not showing another player.
// 2. FindPlacement, the fallback for every other placement, answered with the
//    nearest open square without asking whether a player could walk off it. An
//    empty square sealed inside a wall is open.
//
// Vanilla refuses the transfer outright (ELEMENTS.PAS ElementBoardEdgeTouch:
// Walkable or E_PLAYER, else BoardChange back). This engine deliberately does
// not: refusing was tried and it closes a route TOWN actually uses. The castle's
// south edge lands on row 1 of the Throne Room, and row 1 of the Throne Room is
// the board's title in E_TEXT_YELLOW -- vanilla would turn you back at every
// board whose top row carries a title, and the MMO has always let you through
// (m4_6_test.go's palace path walks exactly that edge). So the crossing still
// happens and the arrival is what got fixed: land on the nearest square you can
// stand on and walk off, rather than inside the letters.

// placementTestBoard returns a headless engine whose board is solid wall, with
// no stats: a test carves the open ground it wants.
func placementTestBoard(t *testing.T) *Engine {
	t.Helper()

	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_NORMAL}
		}
	}
	// BoardCreate leaves a player as stat 0; a placement test wants the board
	// to itself.
	e.Board.StatCount = -1
	return e
}

// placementTestWorld is placementTestBoard as a one-board world a RoomManager
// can open. carve runs against the board before it is closed.
func placementTestWorld(t *testing.T, carve func(e *Engine)) TWorld {
	t.Helper()

	setup := placementTestBoard(t)
	setup.World.Info.CurrentBoard = 1
	setup.World.BoardCount = 1
	carve(setup)
	setup.BoardClose()
	return setup.World
}

// TestFindPlacementSkipsSealedSquare: the square asked for is empty, unheld and
// walled in on all four sides. It is exactly what the old single pass returned.
func TestFindPlacementSkipsSealedSquare(t *testing.T) {
	e := placementTestBoard(t)
	const sealedX, sealedY = int16(20), int16(12)
	e.Board.Tiles[sealedX][sealedY] = TTile{Element: E_EMPTY}
	// Open ground well away from the pocket, so the answer cannot be mistaken
	// for the pocket's own neighbourhood.
	for ix := int16(30); ix <= int16(34); ix++ {
		for iy := int16(10); iy <= int16(14); iy++ {
			e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}

	if e.PlacementReachable(sealedX, sealedY) {
		t.Fatalf("(%d,%d) is walled in on four sides; PlacementReachable said it is not",
			sealedX, sealedY)
	}

	x, y, ok := e.FindPlacement(sealedX, sealedY, -1)
	if !ok {
		t.Fatalf("FindPlacement found nowhere on a board with open ground")
	}
	if x == sealedX && y == sealedY {
		t.Errorf("FindPlacement answered with the sealed square (%d,%d): a "+
			"placement search that returns somewhere you cannot leave has not "+
			"placed anyone", x, y)
	}
	if !e.PlacementReachable(x, y) {
		t.Errorf("FindPlacement answered (%d,%d), which a player cannot walk off", x, y)
	}
}

// TestFindPlacementFallsBackToSealedWhenNothingElse: a board whose only open
// square is sealed still gets an answer. Overlapping another stat is worse.
func TestFindPlacementFallsBackToSealedWhenNothingElse(t *testing.T) {
	e := placementTestBoard(t)
	const sealedX, sealedY = int16(20), int16(12)
	e.Board.Tiles[sealedX][sealedY] = TTile{Element: E_EMPTY}

	x, y, ok := e.FindPlacement(sealedX, sealedY, -1)
	if !ok {
		t.Fatalf("FindPlacement gave up on the one open square the board has")
	}
	if x != sealedX || y != sealedY {
		t.Errorf("FindPlacement answered (%d,%d), want the only open square (%d,%d)",
			x, y, sealedX, sealedY)
	}
}

// TestPlacementReachableCountsWhatAWalkOpens: forest and an item are not
// walkable, but walking into either clears it, so neither seals a square.
// Water and wall do seal one.
func TestPlacementReachableCountsWhatAWalkOpens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		element byte
		want    bool
	}{
		{"empty", E_EMPTY, true},
		{"forest is cleared by the touch", E_FOREST, true},
		{"a gem is picked up", E_GEM, true},
		{"a fake wall is walked through", E_FAKE, true},
		{"a lion dies of the hit", E_LION, true},
		{"a wall stays a wall", E_NORMAL, false},
		{"water stays water", E_WATER, false},
		{"a breakable wall needs a shot, not a step", E_BREAKABLE, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := placementTestBoard(t)
			const x, y = int16(20), int16(12)
			e.Board.Tiles[x][y] = TTile{Element: E_EMPTY}
			e.Board.Tiles[x][y-1] = TTile{Element: tc.element}

			if got := e.PlacementReachable(x, y); got != tc.want {
				t.Errorf("PlacementReachable with %s to the north = %v, want %v",
					tc.name, got, tc.want)
			}
		})
	}
}

// TestRoomSpawnRefusesWallArrival is the reported bug: a transfer names a
// square that holds a wall. The player must not be written into it.
func TestRoomSpawnRefusesWallArrival(t *testing.T) {
	const wallX, wallY = int16(20), int16(12)
	world := placementTestWorld(t, func(e *Engine) {
		// Open ground the player can be put on instead, two rings out from the
		// wall square the transfer names.
		for ix := int16(22); ix <= int16(26); ix++ {
			for iy := int16(10); iy <= int16(14); iy++ {
				e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
			}
		}
		e.Board.Info.StartPlayerX = 24
		e.Board.Info.StartPlayerY = 12
	})

	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(1, wallX, wallY)
	boardID, statID, ok := rm.PlayerLocation(playerID)
	if !ok {
		t.Fatalf("player vanished on join")
	}
	room, ok := rm.Room(boardID)
	if !ok {
		t.Fatalf("room %d missing", boardID)
	}

	stat := room.Engine.Board.Stats[statID]
	gotX, gotY := int16(stat.X), int16(stat.Y)
	if gotX == wallX && gotY == wallY {
		t.Fatalf("player was placed on the wall square (%d,%d): this is the "+
			"boxed-in arrival the fix is for", wallX, wallY)
	}
	if !room.Engine.PlacementReachable(gotX, gotY) {
		t.Errorf("player landed on (%d,%d), which they cannot walk off", gotX, gotY)
	}
	if got := room.Engine.Board.Tiles[wallX][wallY].Element; got != E_NORMAL {
		t.Errorf("the wall at (%d,%d) is now element %d: the arrival overwrote it "+
			"instead of being turned away", wallX, wallY, got)
	}
}

// TestRoomSpawnKeepsPassageArrival guards the reason the arrival test was loose
// in the first place. A passage's far end holds E_PASSAGE, never E_EMPTY, and
// arriving on it is what taking a passage means.
func TestRoomSpawnKeepsPassageArrival(t *testing.T) {
	const passX, passY = int16(20), int16(12)
	world := placementTestWorld(t, func(e *Engine) {
		e.Board.Tiles[passX][passY] = TTile{Element: E_PASSAGE, Color: 0x0f}
		// One open square beside it, the way out of the passage.
		e.Board.Tiles[passX][passY+1] = TTile{Element: E_EMPTY}
		e.Board.Info.StartPlayerX = byte(passX)
		e.Board.Info.StartPlayerY = byte(passY + 1)
	})

	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(1, passX, passY)
	boardID, statID, ok := rm.PlayerLocation(playerID)
	if !ok {
		t.Fatalf("player vanished on join")
	}
	room, _ := rm.Room(boardID)
	stat := room.Engine.Board.Stats[statID]
	if int16(stat.X) != passX || int16(stat.Y) != passY {
		t.Errorf("player arrived at (%d,%d), want the passage square (%d,%d)",
			stat.X, stat.Y, passX, passY)
	}
}

// TestJoinRelocatesWhenStartSquareIsWall: the same hole on the unnamed-spawn
// path. A world whose StartPlayerX/Y sits on a wall must not seat the joiner
// inside it.
func TestJoinRelocatesWhenStartSquareIsWall(t *testing.T) {
	const startX, startY = int16(20), int16(12)
	world := placementTestWorld(t, func(e *Engine) {
		for ix := int16(22); ix <= int16(26); ix++ {
			for iy := int16(10); iy <= int16(14); iy++ {
				e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
			}
		}
		e.Board.Info.StartPlayerX = byte(startX)
		e.Board.Info.StartPlayerY = byte(startY)
	})

	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(1, 0, 0)
	boardID, statID, ok := rm.PlayerLocation(playerID)
	if !ok {
		t.Fatalf("player vanished on join")
	}
	room, _ := rm.Room(boardID)
	stat := room.Engine.Board.Stats[statID]
	gotX, gotY := int16(stat.X), int16(stat.Y)
	if gotX == startX && gotY == startY {
		t.Fatalf("joiner was seated on the wall at the board's start square (%d,%d)",
			startX, startY)
	}
	if !room.Engine.PlacementReachable(gotX, gotY) {
		t.Errorf("joiner landed on (%d,%d), which they cannot walk off", gotX, gotY)
	}
}

// TestRoomSpawnLandsBesideTextRow is TOWN's palace route in miniature. A board
// edge lands on row 1, row 1 is the board's title, and the title is text: not
// walkable, not something a walk clears, and not somewhere to put a player.
// The crossing must still happen -- it is a route the game uses -- with the
// player set down beside the letters.
func TestRoomSpawnLandsBesideTextRow(t *testing.T) {
	const entryX, entryY = int16(30), int16(1)
	world := placementTestWorld(t, func(e *Engine) {
		// A title across row 1, open floor under it: the shape of every ZZT
		// board that names itself at the top.
		for ix := int16(25); ix <= int16(35); ix++ {
			e.Board.Tiles[ix][1] = TTile{Element: E_TEXT_YELLOW, Color: 0x0e}
			for iy := int16(2); iy <= int16(6); iy++ {
				e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
			}
		}
		e.Board.Info.StartPlayerX = byte(entryX)
		e.Board.Info.StartPlayerY = 4
	})

	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(1, entryX, entryY)
	boardID, statID, ok := rm.PlayerLocation(playerID)
	if !ok {
		t.Fatalf("player vanished on join")
	}
	room, _ := rm.Room(boardID)
	stat := room.Engine.Board.Stats[statID]
	gotX, gotY := int16(stat.X), int16(stat.Y)

	if gotX == entryX && gotY == entryY {
		t.Fatalf("player was placed inside the title text at (%d,%d)", entryX, entryY)
	}
	if !room.Engine.PlacementReachable(gotX, gotY) {
		t.Errorf("player landed on (%d,%d), which they cannot walk off", gotX, gotY)
	}
	if got := room.Engine.Board.Tiles[entryX][entryY].Element; got != E_TEXT_YELLOW {
		t.Errorf("the title at (%d,%d) is now element %d: the arrival ate a letter",
			entryX, entryY, got)
	}
}
