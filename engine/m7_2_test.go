package zztgo

import "testing"

func assertTorchCircleRenderedLit(t *testing.T, e *Engine, statID int16) {
	t.Helper()

	stat := e.Board.Stats[statID]
	for x := int16(1); x <= BOARD_WIDTH; x++ {
		for y := int16(1); y <= BOARD_HEIGHT; y++ {
			if Sqr(x-int16(stat.X))+Sqr(y-int16(stat.Y))*2 >= TORCH_DIST_SQR {
				continue
			}
			cell := e.Screen[x-1][y-1]
			if cell.Ch == '\xb0' && cell.Color == 0x07 {
				t.Fatalf("cell (%d,%d) inside torch circle for stat %d is still dark shade", x, y, statID)
			}
		}
	}
}

func TestM72TorchLightArrivesWithTransferredPlayer(t *testing.T) {
	world := loadTownWorldForM45(t)
	darkBoard, _, _ := townTile(t, world, "dark room", func(e *Engine, x, y int16) bool {
		return e.Board.Info.IsDark
	})

	rm := NewRoomManager(world)
	playerID := rm.JoinPlayer(1, 30, 12)
	pState, ok := rm.PlayerState(playerID)
	if !ok {
		t.Fatal("player state missing")
	}
	pState.TorchTicks = 100

	rm.transferPlayer(playerID, TransferEvent{StatId: 0, ToBoard: darkBoard, EntryX: 30, EntryY: 12})
	player := rm.players[playerID]
	if player == nil {
		t.Fatal("player missing after transfer")
	}
	room, ok := rm.Room(darkBoard)
	if !ok {
		t.Fatalf("room %d missing after transfer", darkBoard)
	}

	assertTorchCircleRenderedLit(t, room.Engine, player.statID)
}

func TestM72TorchLightArrivesWithFreshSpawn(t *testing.T) {
	world := loadTownWorldForM45(t)
	darkBoard, _, _ := townTile(t, world, "dark room", func(e *Engine, x, y int16) bool {
		return e.Board.Info.IsDark
	})

	rm := NewRoomManager(world)
	room := rm.ensureRoom(darkBoard)
	room.Engine.PlayerFor(0).TorchTicks = 100
	spawnX := int16(room.Engine.Board.Stats[0].X)
	spawnY := int16(room.Engine.Board.Stats[0].Y)

	statID := rm.spawnPlayerInRoom(room, spawnX, spawnY)

	assertTorchCircleRenderedLit(t, room.Engine, statID)
}
