package zztgo

import (
	"path/filepath"
	"testing"
)

func loadTownWorldForM45(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q, %q) failed", worldBase, ".ZZT")
	}
	return setup.World
}

func townTile(t *testing.T, world TWorld, name string, pred func(*Engine, int16, int16) bool) (int16, int16, int16) {
	t.Helper()

	e := NewEngine()
	e.Headless = true
	e.World = world
	for boardID := int16(1); boardID <= world.BoardCount; boardID++ {
		e.BoardOpen(boardID)
		for y := int16(1); y <= BOARD_HEIGHT; y++ {
			for x := int16(1); x <= BOARD_WIDTH; x++ {
				if pred(e, x, y) {
					return boardID, x, y
				}
			}
		}
	}
	t.Fatalf("no TOWN tile found for %s", name)
	return 0, 0, 0
}

func screenCell(cells []ScreenCell, x, y int16) (ScreenCell, bool) {
	for _, cell := range cells {
		if cell.X == x && cell.Y == y {
			return cell, true
		}
	}
	return ScreenCell{}, false
}

func assertProtocolCellMatchesTile(t *testing.T, room *Room, snapshot SnapshotMessage, x, y int16) {
	t.Helper()

	wantColor, wantCh := room.Engine.TileToColorAndChar(x, y)
	cell, ok := screenCell(snapshot.Screen, x-1, y-1)
	if !ok {
		t.Fatalf("snapshot missing cell for board (%d,%d)", x, y)
	}
	if cell.Ch != wantCh || cell.Color != wantColor {
		t.Fatalf("cell (%d,%d) = {ch:%#02x color:%#02x}, want {ch:%#02x color:%#02x}",
			x, y, cell.Ch, cell.Color, wantCh, wantColor)
	}
}

func TestM45TownLandmarkCellsMatchProtocolSnapshot(t *testing.T) {
	world := loadTownWorldForM45(t)

	cases := []struct {
		name string
		pred func(*Engine, int16, int16) bool
	}{
		{
			name: "gem",
			pred: func(e *Engine, x, y int16) bool {
				return !e.Board.Info.IsDark && e.Board.Tiles[x][y].Element == E_GEM
			},
		},
		{
			name: "passage",
			pred: func(e *Engine, x, y int16) bool {
				return !e.Board.Info.IsDark && e.Board.Tiles[x][y].Element == E_PASSAGE
			},
		},
		{
			name: "text",
			pred: func(e *Engine, x, y int16) bool {
				return !e.Board.Info.IsDark && e.Board.Tiles[x][y].Element >= E_TEXT_MIN
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boardID, x, y := townTile(t, world, tc.name, tc.pred)
			rm := NewRoomManager(world)
			playerID := rm.JoinPlayer(boardID, 0, 0)
			snapshot, ok := rm.Snapshot(playerID)
			if !ok {
				t.Fatal("snapshot failed")
			}
			room, ok := rm.Room(boardID)
			if !ok {
				t.Fatalf("room %d missing", boardID)
			}
			assertProtocolCellMatchesTile(t, room, snapshot, x, y)
		})
	}
}

func TestM45DirtyDiffsCoverMovementWithoutFullSnapshot(t *testing.T) {
	world := loadTownWorldForM45(t)
	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(1, 30, 12)
	snapshot, ok := rm.Snapshot(playerID)
	if !ok {
		t.Fatal("snapshot failed")
	}

	startX, startY := snapshot.You.X, snapshot.You.Y
	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{
		playerID: {DeltaX: 1, Key: KEY_RIGHT},
	})
	diff := diffs[playerID]
	if len(diff.Cells) == 0 {
		t.Fatal("movement diff has no cells")
	}
	if len(diff.Cells) >= BOARD_WIDTH*25 {
		t.Fatalf("movement diff sent %d cells, want dirty cells only", len(diff.Cells))
	}
	for _, cell := range diff.Cells {
		if cell.X >= BOARD_WIDTH {
			t.Fatalf("diff leaked legacy sidebar cell at x=%d", cell.X)
		}
	}
	if _, ok := screenCell(diff.Cells, startX-1, startY-1); !ok {
		t.Fatalf("movement diff missing old player cell (%d,%d)", startX-1, startY-1)
	}
	if _, ok := screenCell(diff.Cells, startX, startY-1); !ok {
		t.Fatalf("movement diff missing new player cell (%d,%d)", startX, startY-1)
	}
}

func TestM45DarkRoomTorchDiffFromTown(t *testing.T) {
	world := loadTownWorldForM45(t)
	darkBoard, _, _ := townTile(t, world, "dark room", func(e *Engine, x, y int16) bool {
		return e.Board.Info.IsDark
	})

	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(darkBoard, 30, 12)
	snapshot, ok := rm.Snapshot(playerID)
	if !ok {
		t.Fatal("snapshot failed")
	}

	var darkCells int
	for _, cell := range snapshot.Screen {
		if cell.Ch == '\xb0' && cell.Color == 0x07 {
			darkCells++
		}
	}
	if darkCells == 0 {
		t.Fatalf("dark TOWN board %d snapshot has no dark-room shaded cells", darkBoard)
	}

	pState, ok := rm.PlayerState(playerID)
	if !ok {
		t.Fatal("player state missing")
	}
	pState.Torches = 1

	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{
		playerID: {Key: 'T'},
	})
	diff := diffs[playerID]
	if diff.HUD == nil || diff.HUD.Torches != 0 || diff.HUD.TorchTicks <= 0 {
		t.Fatalf("torch HUD = %+v, want consumed torch with active ticks", diff.HUD)
	}
	var litCells int
	for _, cell := range diff.Cells {
		if cell.Ch != '\xb0' || cell.Color != 0x07 {
			litCells++
		}
	}
	if litCells == 0 {
		t.Fatalf("lighting torch on TOWN board %d produced no visible dirty cells", darkBoard)
	}
}

func TestM45TorchDirtyCellsFollowNonzeroPlayer(t *testing.T) {
	e, _, p2 := twoPlayerBoard(t)
	e.Board.Info.IsDark = true
	e.PlayerFor(p2).TorchTicks = 100
	e.Board.Tiles[32][12] = TTile{Element: E_GEM, Color: ElementDefs[E_GEM].Color}

	e.DrawPlayerSurroundings(40, 12, 0)
	e.DrainScreenDirty()

	e.MoveStat(p2, 39, 12)
	dirty := e.DrainScreenDirty()
	cell, ok := screenCell(dirty, 31, 11)
	if !ok {
		t.Fatalf("nonzero player torch movement did not dirty newly visible gem; cells=%v", dirty)
	}
	if cell.Ch != ElementDefs[E_GEM].Character || cell.Color != ElementDefs[E_GEM].Color {
		t.Fatalf("newly visible gem cell = {ch:%#02x color:%#02x}, want gem {ch:%#02x color:%#02x}",
			cell.Ch, cell.Color, ElementDefs[E_GEM].Character, ElementDefs[E_GEM].Color)
	}
}

func TestM45PlayerBlinkAndRespawnDirtyCells(t *testing.T) {
	e, p1, _ := twoPlayerBoard(t)
	e.PlayerFor(p1).EnergizerTicks = 3
	e.CurrentTick = 1
	e.DrainScreenDirty()

	step(e, nil)
	blink := e.DrainScreenDirty()
	if _, ok := screenCell(blink, int16(e.Board.Stats[p1].X)-1, int16(e.Board.Stats[p1].Y)-1); !ok {
		t.Fatalf("energizer blink did not dirty player cell; cells=%v", blink)
	}

	oldX, oldY := int16(e.Board.Stats[p1].X), int16(e.Board.Stats[p1].Y)
	e.PlayerFor(p1).Health = 0
	e.PlayerFor(p1).RespawnTicks = 1
	e.SetReenterPoint(p1, 20, 12)
	e.DrainScreenDirty()

	step(e, nil)
	respawn := e.DrainScreenDirty()
	if _, ok := screenCell(respawn, oldX-1, oldY-1); !ok {
		t.Fatalf("respawn did not dirty old player cell; cells=%v", respawn)
	}
	if _, ok := screenCell(respawn, 19, 11); !ok {
		t.Fatalf("respawn did not dirty new player cell; cells=%v", respawn)
	}
}
