package zztgo

import "sort"

// PlayerID is the RoomManager-level stable player identity. Engine stat ids can
// shift when stats are removed; PlayerID does not.
type PlayerID int64

type RoomManager struct {
	world        TWorld
	rooms        map[int16]*Room
	players      map[PlayerID]*roomPlayer
	nextPlayerID PlayerID
}

type Room struct {
	BoardID int16
	Engine  *Engine
	players map[PlayerID]struct{}
}

type roomPlayer struct {
	id      PlayerID
	boardID int16
	statID  int16
	state   *PlayerState
}

type roomTransfer struct {
	playerID PlayerID
	event    TransferEvent
}

func NewRoomManager(world TWorld) *RoomManager {
	return &RoomManager{
		world:   world,
		rooms:   make(map[int16]*Room),
		players: make(map[PlayerID]*roomPlayer),
	}
}

func (rm *RoomManager) JoinPlayer(boardID, spawnX, spawnY int16) PlayerID {
	room := rm.ensureRoom(boardID)
	statID := rm.spawnPlayerInRoom(room, spawnX, spawnY)
	room.Engine.ResetPlayerState(statID)
	rm.nextPlayerID++
	playerID := rm.nextPlayerID
	player := &roomPlayer{
		id:      playerID,
		boardID: room.BoardID,
		statID:  statID,
		state:   room.Engine.PlayerFor(statID),
	}
	rm.players[playerID] = player
	room.players[playerID] = struct{}{}
	return playerID
}

func (rm *RoomManager) spawnPlayerInRoom(room *Room, spawnX, spawnY int16) int16 {
	spawnX, spawnY = roomSpawn(room, spawnX, spawnY)
	if len(room.players) == 0 {
		if statID, ok := claimablePlayerStat(room); ok {
			movePlayerStat(room.Engine, statID, spawnX, spawnY)
			return statID
		}
	}

	originalSpawnX := room.Engine.Board.Info.StartPlayerX
	originalSpawnY := room.Engine.Board.Info.StartPlayerY
	room.Engine.Board.Info.StartPlayerX = byte(spawnX)
	room.Engine.Board.Info.StartPlayerY = byte(spawnY)
	statID := room.Engine.SpawnPlayer()
	room.Engine.Board.Info.StartPlayerX = originalSpawnX
	room.Engine.Board.Info.StartPlayerY = originalSpawnY
	return statID
}

func claimablePlayerStat(room *Room) (int16, bool) {
	if room.Engine.Board.StatCount < 0 {
		return 0, false
	}
	stat := room.Engine.Board.Stats[0]
	if room.Engine.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
		return 0, true
	}
	for statID := int16(1); statID <= room.Engine.Board.StatCount; statID++ {
		stat = room.Engine.Board.Stats[statID]
		if room.Engine.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
			return statID, true
		}
	}
	return 0, false
}

func movePlayerStat(engine *Engine, statID, x, y int16) {
	stat := &engine.Board.Stats[statID]
	if int16(stat.X) != x || int16(stat.Y) != y {
		engine.MoveStat(statID, x, y)
	}
	engine.Board.Tiles[x][y].Element = E_PLAYER
	engine.Board.Tiles[x][y].Color = ElementDefs[E_PLAYER].Color
	engine.BoardDrawTile(x, y)
}

func roomSpawn(room *Room, spawnX, spawnY int16) (int16, int16) {
	requested := spawnX != 0 && spawnY != 0
	if !requested && len(room.players) == 0 {
		if statID, ok := claimablePlayerStat(room); ok {
			stat := room.Engine.Board.Stats[statID]
			return int16(stat.X), int16(stat.Y)
		}
	}
	if spawnX == 0 || spawnY == 0 {
		spawnX = int16(room.Engine.Board.Info.StartPlayerX)
		spawnY = int16(room.Engine.Board.Info.StartPlayerY)
	}
	if spawnX == 0 || spawnY == 0 {
		spawnX = BOARD_WIDTH / 2
		spawnY = BOARD_HEIGHT / 2
	}
	if isSpawnOpen(room, spawnX, spawnY) || requested && isSpawnUnoccupied(room, spawnX, spawnY) {
		return spawnX, spawnY
	}

	for radius := int16(1); radius <= BOARD_WIDTH || radius <= BOARD_HEIGHT; radius++ {
		for dy := -radius; dy <= radius; dy++ {
			for dx := -radius; dx <= radius; dx++ {
				if absInt16(dx) != radius && absInt16(dy) != radius {
					continue
				}
				x := spawnX + dx
				y := spawnY + dy
				if isSpawnOpen(room, x, y) {
					return x, y
				}
			}
		}
	}
	return spawnX, spawnY
}

func absInt16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func isSpawnOpen(room *Room, x, y int16) bool {
	return isSpawnUnoccupied(room, x, y) && room.Engine.Board.Tiles[x][y].Element == E_EMPTY
}

func isSpawnUnoccupied(room *Room, x, y int16) bool {
	if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
		return false
	}
	return room.Engine.Board.Tiles[x][y].Element != E_PLAYER
}

func (rm *RoomManager) LeavePlayer(playerID PlayerID) bool {
	player := rm.players[playerID]
	if player == nil {
		return false
	}
	room := rm.rooms[player.boardID]
	if room == nil {
		delete(rm.players, playerID)
		return true
	}

	removedStatID := player.statID
	room.Engine.RemovePlayer(removedStatID)
	delete(room.players, playerID)
	delete(rm.players, playerID)
	rm.reindexRoomPlayers(room.BoardID, removedStatID)
	rm.freezeRoomIfEmpty(room.BoardID)
	return true
}

func (rm *RoomManager) Step(inputs map[PlayerID]PlayerInput) {
	rm.StepDiffs(inputs)
}

func (rm *RoomManager) StepDiffs(inputs map[PlayerID]PlayerInput) map[PlayerID]DiffMessage {
	engineInputs := make(map[int16]map[int16]PlayerInput)
	for playerID, input := range inputs {
		player := rm.players[playerID]
		if player == nil {
			continue
		}
		if _, ok := engineInputs[player.boardID]; !ok {
			engineInputs[player.boardID] = make(map[int16]PlayerInput)
		}
		engineInputs[player.boardID][player.statID] = input
	}

	transfers := make([]roomTransfer, 0)
	roomEvents := make(map[int16][]Event)
	for _, boardID := range rm.roomIDs() {
		room := rm.rooms[boardID]
		if room == nil || len(room.players) == 0 {
			continue
		}
		inputsForRoom := engineInputs[boardID]
		if inputsForRoom == nil {
			inputsForRoom = map[int16]PlayerInput{}
		}
		room.Engine.GameStepWithInputs(inputsForRoom)

		for _, event := range room.Engine.DrainEvents() {
			transfer, ok := event.(TransferEvent)
			if !ok {
				roomEvents[boardID] = append(roomEvents[boardID], event)
				continue
			}
			playerID, found := rm.playerIDForStat(boardID, transfer.StatId)
			if found {
				transfers = append(transfers, roomTransfer{playerID: playerID, event: transfer})
			}
		}
	}

	for _, transfer := range transfers {
		rm.transferPlayer(transfer.playerID, transfer.event)
	}

	diffs := make(map[PlayerID]DiffMessage)
	for _, boardID := range rm.roomIDs() {
		room := rm.rooms[boardID]
		if room == nil || len(room.players) == 0 {
			continue
		}
		cells := room.Engine.DrainScreenDirty()
		events := ProtocolEvents(roomEvents[boardID])
		players := rm.playerSnapshotsForRoom(room)
		for _, playerID := range rm.playerIDs() {
			player := rm.players[playerID]
			if player.boardID != room.BoardID {
				continue
			}
			hud := hudSnapshot(room.Engine, player.statID)
			diffs[playerID] = DiffMessage{
				Type:    MessageTypeDiff,
				BoardID: room.BoardID,
				Tick:    room.Engine.CurrentTick,
				Hash:    StateHash(room.Engine),
				Cells:   cells,
				Players: players,
				HUD:     &hud,
				Events:  events,
			}
		}
	}
	return diffs
}

func (rm *RoomManager) PlayerState(playerID PlayerID) (*PlayerState, bool) {
	player := rm.players[playerID]
	if player == nil {
		return nil, false
	}
	return player.state, true
}

func (rm *RoomManager) PlayerLocation(playerID PlayerID) (boardID, statID int16, ok bool) {
	player := rm.players[playerID]
	if player == nil {
		return 0, 0, false
	}
	return player.boardID, player.statID, true
}

func (rm *RoomManager) Room(boardID int16) (*Room, bool) {
	room := rm.rooms[boardID]
	return room, room != nil
}

func (rm *RoomManager) ActiveRoomCount() int {
	return len(rm.rooms)
}

func (rm *RoomManager) FrozenWorld() TWorld {
	return rm.world
}

func (rm *RoomManager) Snapshot(playerID PlayerID) (SnapshotMessage, bool) {
	player := rm.players[playerID]
	if player == nil {
		return SnapshotMessage{}, false
	}
	room := rm.rooms[player.boardID]
	if room == nil {
		return SnapshotMessage{}, false
	}

	players := make([]PlayerSnapshot, 0, len(room.players))
	players = append(players, rm.playerSnapshotsForRoom(room)...)
	snapshot := NewSnapshotMessage(room.Engine, room.BoardID, playerID, player.statID, players)
	room.Engine.DrainScreenDirty()
	room.Engine.DrainEvents()
	return snapshot, true
}

func (rm *RoomManager) ensureRoom(boardID int16) *Room {
	if room := rm.rooms[boardID]; room != nil {
		return room
	}

	engine := NewEngine()
	engine.Headless = true
	engine.MultiRoom = true
	engine.TickSpeed = 4
	engine.TickTimeDuration = int16(engine.TickSpeed) * 2
	engine.GameStateElement = E_PLAYER
	engine.SetInputSource(&ScriptedInput{})
	engine.World = rm.world
	engine.BoardOpen(boardID)
	engine.GenerateTransitionTable()
	engine.TransitionDrawToBoard()

	room := &Room{
		BoardID: boardID,
		Engine:  engine,
		players: make(map[PlayerID]struct{}),
	}
	rm.rooms[boardID] = room
	return room
}

func (rm *RoomManager) roomIDs() []int16 {
	ids := make([]int16, 0, len(rm.rooms))
	for boardID := range rm.rooms {
		ids = append(ids, boardID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (rm *RoomManager) playerIDs() []PlayerID {
	ids := make([]PlayerID, 0, len(rm.players))
	for playerID := range rm.players {
		ids = append(ids, playerID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (rm *RoomManager) playerIDForStat(boardID, statID int16) (PlayerID, bool) {
	for _, playerID := range rm.playerIDs() {
		player := rm.players[playerID]
		if player.boardID == boardID && player.statID == statID {
			return playerID, true
		}
	}
	return 0, false
}

func (rm *RoomManager) playerSnapshotsForRoom(room *Room) []PlayerSnapshot {
	players := make([]PlayerSnapshot, 0, len(room.players))
	for _, id := range rm.playerIDs() {
		p := rm.players[id]
		if p.boardID == room.BoardID {
			players = append(players, playerSnapshot(room.Engine, id, p.statID))
		}
	}
	return players
}

func (rm *RoomManager) transferPlayer(playerID PlayerID, transfer TransferEvent) {
	player := rm.players[playerID]
	if player == nil {
		return
	}
	srcRoom := rm.rooms[player.boardID]
	if srcRoom == nil {
		return
	}
	dstRoom := rm.ensureRoom(transfer.ToBoard)

	stateCopy := *srcRoom.Engine.PlayerFor(player.statID)
	removedStatID := player.statID
	srcRoom.Engine.RemovePlayer(removedStatID)
	delete(srcRoom.players, playerID)
	rm.reindexRoomPlayers(srcRoom.BoardID, removedStatID)

	newStatID := rm.spawnPlayerInRoom(dstRoom, transfer.EntryX, transfer.EntryY)
	*dstRoom.Engine.PlayerFor(newStatID) = stateCopy
	dstRoom.players[playerID] = struct{}{}

	player.boardID = dstRoom.BoardID
	player.statID = newStatID
	player.state = dstRoom.Engine.PlayerFor(newStatID)

	rm.freezeRoomIfEmpty(srcRoom.BoardID)
}

func (rm *RoomManager) reindexRoomPlayers(boardID, removedStatID int16) {
	for _, playerID := range rm.playerIDs() {
		player := rm.players[playerID]
		if player.boardID == boardID && player.statID > removedStatID {
			player.statID--
			room := rm.rooms[boardID]
			if room != nil {
				player.state = room.Engine.PlayerFor(player.statID)
			}
		}
	}
}

func (rm *RoomManager) freezeRoomIfEmpty(boardID int16) {
	room := rm.rooms[boardID]
	if room == nil || len(room.players) != 0 {
		return
	}

	room.Engine.BoardClose()
	rm.world.BoardData[boardID] = append([]byte(nil), room.Engine.World.BoardData[boardID]...)
	rm.world.BoardLen[boardID] = room.Engine.World.BoardLen[boardID]
	rm.world.Info.Flags = room.Engine.World.Info.Flags
	delete(rm.rooms, boardID)
	rm.syncFrozenBoardToLiveRooms(boardID)
}

func (rm *RoomManager) syncFrozenBoardToLiveRooms(boardID int16) {
	for _, roomID := range rm.roomIDs() {
		room := rm.rooms[roomID]
		room.Engine.World.BoardData[boardID] = append([]byte(nil), rm.world.BoardData[boardID]...)
		room.Engine.World.BoardLen[boardID] = rm.world.BoardLen[boardID]
		room.Engine.World.Info.Flags = rm.world.Info.Flags
	}
}
