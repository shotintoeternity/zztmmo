package zztgo

import (
	"os"
	"sort"
)

// PlayerID is the RoomManager-level stable player identity. Engine stat ids can
// shift when stats are removed; PlayerID does not.
type PlayerID int64

type RoomManager struct {
	world        TWorld
	rooms        map[int16]*Room
	players      map[PlayerID]*roomPlayer
	nextPlayerID PlayerID

	// HighScorePath, when non-empty, is the file the world's high-score list is
	// read from and written to. Empty keeps the list in memory only, which is
	// what tests want: NewRoomManager must not touch the filesystem.
	//
	// The list lives here rather than on Engine because there is one Engine per
	// BOARD and one high-score list per WORLD (Engine.HighScoresAdd documents
	// the same split from the other side).
	HighScorePath string
	highScores    THighScoreList

	// quits and pendingScores carry a confirmed quit out of StepDiffs. The
	// player is gone from their room by then, so they get no diff and the
	// server must deliver the outcome to them directly.
	quits               []QuitResult
	pendingScores       map[PlayerID]QuitResult
	pendingPlayerEvents map[PlayerID][]Event
}

// QuitResult is one player's confirmed quit, drained by the server after a step.
type QuitResult struct {
	PlayerID PlayerID
	Score    int16
	// ListPos is the 1-based high-score position the score earned, or 0 when it
	// did not qualify (vanilla: a score of 0, or a full list of better scores).
	ListPos int16
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
	name    string
	// scrollOpen freezes this player while they read a scroll. Vanilla's text
	// window blocks the whole game loop (TextWindowDrawOpen); here only the
	// reader stops, so the rest of the room plays on. Without it the player
	// keeps walking for the tick or two it takes their "stop" to reach us, and
	// a second scroll fires and overwrites the first before they can read it.
	// Cleared by SubmitScrollReply, which the client sends on dismiss.
	scrollOpen bool
}

type roomTransfer struct {
	playerID PlayerID
	event    TransferEvent
}

func NewRoomManager(world TWorld) *RoomManager {
	rm := &RoomManager{
		world:               world,
		rooms:               make(map[int16]*Room),
		players:             make(map[PlayerID]*roomPlayer),
		pendingScores:       make(map[PlayerID]QuitResult),
		pendingPlayerEvents: make(map[PlayerID][]Event),
	}
	// HighScoresLoad's failure path (game.go): an unset list is all -1, so any
	// positive score outranks it.
	for i := 0; i < HIGH_SCORE_COUNT; i++ {
		rm.highScores[i].Name = ""
		rm.highScores[i].Score = -1
	}
	return rm
}

// LoadHighScores reads HighScorePath, if set. A missing file leaves the empty
// list in place, exactly as Engine.HighScoresLoad does.
func (rm *RoomManager) LoadHighScores() {
	if rm.HighScorePath == "" {
		return
	}
	f, err := os.Open(rm.HighScorePath)
	if err != nil {
		return
	}
	defer f.Close()

	buf := make([]byte, SizeOfHighScoreList)
	if _, err := f.Read(buf); err != nil {
		return
	}
	LoadHighScoreList(buf, rm.highScores[:])
}

func (rm *RoomManager) saveHighScores() {
	if rm.HighScorePath == "" {
		return
	}
	buf := make([]byte, SizeOfHighScoreList)
	StoreHighScoreList(buf, rm.highScores[:])
	// Best effort: a server that cannot persist scores must still keep playing.
	_ = os.WriteFile(rm.HighScorePath, buf, 0o644)
}

// HighScores returns a copy of the world's list.
func (rm *RoomManager) HighScores() THighScoreList {
	return rm.highScores
}

// HighScoreLines renders the list the way HighScoresInitTextWindow does, for a
// client text window. highlightPos, when 1-based and in range, names the entry
// the caller is about to write — it is shown as vanilla's "-- You! --".
func (rm *RoomManager) HighScoreLines(highlightPos int16) []string {
	lines := []string{"Score  Name", "-----  ----------------------------------"}
	for i := 0; i < HIGH_SCORE_COUNT; i++ {
		name := rm.highScores[i].Name
		if int16(i)+1 == highlightPos {
			name = "-- You! --"
		}
		if Length(name) == 0 {
			continue
		}
		lines = append(lines, StrWidth(rm.highScores[i].Score, 5)+"  "+name)
	}
	return lines
}

// rankScore is HighScoresAdd's search: the 1-based slot this score earns, or 0.
func (rm *RoomManager) rankScore(score int16) int16 {
	listPos := int16(1)
	for listPos <= HIGH_SCORE_COUNT && score < rm.highScores[listPos-1].Score {
		listPos++
	}
	if listPos <= HIGH_SCORE_COUNT && score > 0 {
		return listPos
	}
	return 0
}

// RecordHighScore writes the name a quitting player typed into the slot their
// score earned, then persists the list. Returns false if that player has no
// pending entry — a client cannot claim a slot it was never offered.
func (rm *RoomManager) RecordHighScore(playerID PlayerID, name string) bool {
	pending, ok := rm.pendingScores[playerID]
	if !ok || pending.ListPos < 1 || pending.ListPos > HIGH_SCORE_COUNT {
		return false
	}
	delete(rm.pendingScores, playerID)

	for i := int16(HIGH_SCORE_COUNT - 1); i >= pending.ListPos; i-- {
		rm.highScores[i] = rm.highScores[i-1]
	}
	rm.highScores[pending.ListPos-1] = THighScoreEntry{Name: name, Score: pending.Score}
	rm.saveHighScores()
	return true
}

// DiscardPendingScore forgets a high-score slot offered to a player who left
// before naming it. Without this a client that quits and closes its socket
// leaves an entry behind for the life of the process.
func (rm *RoomManager) DiscardPendingScore(playerID PlayerID) {
	delete(rm.pendingScores, playerID)
}

// SubmitQuitReply routes a quit-prompt answer to the engine that owns the
// player. The engine turns a confirmed quit into a QuitEvent on the next step.
func (rm *RoomManager) SubmitQuitReply(playerID PlayerID, quit bool) bool {
	player := rm.players[playerID]
	if player == nil {
		return false
	}
	room := rm.rooms[player.boardID]
	if room == nil {
		return false
	}
	room.Engine.SubmitQuitReply(player.statID, quit)
	return true
}

// DrainQuits returns the players who quit during the last step and clears the
// list. They have already left their rooms.
func (rm *RoomManager) DrainQuits() []QuitResult {
	quits := rm.quits
	rm.quits = nil
	return quits
}

// DrainPlayerEvents returns presentation events that must be delivered directly
// to one player rather than through their room diff.
func (rm *RoomManager) DrainPlayerEvents(playerID PlayerID) []Event {
	events := rm.pendingPlayerEvents[playerID]
	delete(rm.pendingPlayerEvents, playerID)
	return events
}

// quitPlayer ranks a departing player's score and removes them from their room.
func (rm *RoomManager) quitPlayer(playerID PlayerID) {
	state, ok := rm.PlayerState(playerID)
	if !ok {
		return
	}
	result := QuitResult{PlayerID: playerID, Score: state.Score}
	result.ListPos = rm.rankScore(state.Score)
	if result.ListPos > 0 {
		rm.pendingScores[playerID] = result
	}
	rm.quits = append(rm.quits, result)
	rm.LeavePlayer(playerID)
}

func (rm *RoomManager) JoinPlayer(boardID, spawnX, spawnY int16) PlayerID {
	room := rm.ensureRoom(boardID)
	statID := rm.spawnPlayerInRoom(room, spawnX, spawnY)
	room.Engine.ResetPlayerState(statID)
	drawPlayerArrivalSurroundings(room.Engine, statID)
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

func (rm *RoomManager) SetPlayerName(playerID PlayerID, name string) {
	player := rm.players[playerID]
	if player != nil {
		player.name = name
	}
}

func (rm *RoomManager) spawnPlayerInRoom(room *Room, spawnX, spawnY int16) int16 {
	spawnX, spawnY = roomSpawn(room, spawnX, spawnY)
	if len(room.players) == 0 {
		if statID, ok := claimablePlayerStat(room); ok {
			movePlayerStat(room.Engine, statID, spawnX, spawnY)
			drawPlayerArrivalSurroundings(room.Engine, statID)
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
	drawPlayerArrivalSurroundings(room.Engine, statID)
	return statID
}

func drawPlayerArrivalSurroundings(engine *Engine, statID int16) {
	if !engine.Board.Info.IsDark || statID < 0 || statID > engine.Board.StatCount || engine.PlayerFor(statID).TorchTicks <= 0 {
		return
	}
	stat := engine.Board.Stats[statID]
	if engine.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
		engine.DrawPlayerSurroundings(int16(stat.X), int16(stat.Y), 0)
	}
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
	// This is the server's BoardEnter: the square a player arrives on is where
	// they re-enter after a ReenterWhenZapped hit or a death respawn.
	engine.SetReenterPoint(statID, x, y)
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
		if statID, ok := claimablePlayerStat(room); ok {
			stat := room.Engine.Board.Stats[statID]
			spawnX = int16(stat.X)
			spawnY = int16(stat.Y)
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
	if requested {
		if isSpawnOpen(room, spawnX, spawnY) || isRequestedSpawnUnoccupied(room, spawnX, spawnY) {
			return spawnX, spawnY
		}
	} else if isSpawnUnoccupied(room, spawnX, spawnY) {
		return spawnX, spawnY
	}

	if x, y, ok := room.Engine.FindPlacement(spawnX, spawnY, -1); ok {
		return x, y
	}
	return spawnX, spawnY
}

func absInt16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

// isSpawnOpen and isSpawnUnoccupied are the room-scoped spelling of the shared
// placement policy in placement.go, which re-enter and respawn use too (M4.3b).
func isSpawnOpen(room *Room, x, y int16) bool {
	return room.Engine.PlacementOpen(x, y, -1)
}

func isSpawnUnoccupied(room *Room, x, y int16) bool {
	return room.Engine.PlacementUnoccupied(x, y, -1)
}

func isRequestedSpawnUnoccupied(room *Room, x, y int16) bool {
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
	delete(rm.pendingPlayerEvents, playerID)
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
		// A player reading a scroll is frozen. Omitting the entry is how that is
		// said to the engine: GameStepWithInputs zeroes the movement globals for
		// any player stat it finds no input for.
		if player.scrollOpen {
			continue
		}
		if _, ok := engineInputs[player.boardID]; !ok {
			engineInputs[player.boardID] = make(map[int16]PlayerInput)
		}
		engineInputs[player.boardID][player.statID] = input
	}

	transfers := make([]roomTransfer, 0)
	quitters := make([]PlayerID, 0)
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

		// Transfers and quits both name a stat id, and both reindex stat ids
		// when they are applied. Resolve them to stable PlayerIDs here, while
		// the ids still mean what the engine meant by them, and act below.
		for _, event := range room.Engine.DrainEvents() {
			switch ev := event.(type) {
			case TransferEvent:
				if playerID, found := rm.playerIDForStat(boardID, ev.StatId); found {
					if ev.SoundNotes != "" {
						sound := SoundEvent{Notes: ev.SoundNotes, Priority: ev.SoundPriority, StatId: ev.StatId}
						rm.pendingPlayerEvents[playerID] = append(rm.pendingPlayerEvents[playerID], sound)
					}
					transfers = append(transfers, roomTransfer{playerID: playerID, event: ev})
				}
			case SoundEvent:
				if ev.StatId >= 0 {
					if playerID, found := rm.playerIDForStat(boardID, ev.StatId); found {
						rm.pendingPlayerEvents[playerID] = append(rm.pendingPlayerEvents[playerID], ev)
					}
				} else {
					roomEvents[boardID] = append(roomEvents[boardID], event)
				}
			case QuitEvent:
				if playerID, found := rm.playerIDForStat(boardID, ev.StatId); found {
					quitters = append(quitters, playerID)
				}
			case ScrollEvent:
				// PlayerStatId < 0 is an object talking to the whole board, not a
				// touch: nobody is reading it, so nobody freezes.
				if ev.PlayerStatId >= 0 {
					if playerID, found := rm.playerIDForStat(boardID, ev.PlayerStatId); found {
						rm.players[playerID].scrollOpen = true
					}
				}
				roomEvents[boardID] = append(roomEvents[boardID], event)
			default:
				roomEvents[boardID] = append(roomEvents[boardID], event)
			}
		}
	}

	for _, transfer := range transfers {
		rm.transferPlayer(transfer.playerID, transfer.event)
	}

	// Quitters leave before the diffs are built, so they receive none and the
	// remaining players' stat ids are already reindexed.
	for _, playerID := range quitters {
		rm.quitPlayer(playerID)
	}

	rm.syncPlayerStatIDs()

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

// WorldName is the title the sidebar and high-score windows show. Vanilla falls
// back to "Untitled" (GAME.PAS:1462-1465).
func (rm *RoomManager) WorldName() string {
	if Length(rm.world.Info.Name) == 0 {
		return "Untitled"
	}
	return rm.world.Info.Name
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
	// A scroll cannot survive the board it was read on, and a gate that did
	// would freeze the player on the far side forever.
	player.scrollOpen = false
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
	drawPlayerArrivalSurroundings(dstRoom.Engine, newStatID)
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

func (rm *RoomManager) syncPlayerStatIDs() {
	for _, playerID := range rm.playerIDs() {
		player := rm.players[playerID]
		room := rm.rooms[player.boardID]
		if room == nil {
			continue
		}
		for statID, state := range room.Engine.Players {
			if state == player.state {
				if player.statID != statID {
					player.statID = statID
				}
				break
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

// SubmitDebugCommand forwards a player's debug-prompt text to the engine that
// currently owns them, tagged with their stat id so the cheat credits the
// player who typed it rather than stat 0.
func (rm *RoomManager) SubmitDebugCommand(playerID PlayerID, text string) bool {
	player := rm.players[playerID]
	if player == nil {
		return false
	}
	room := rm.rooms[player.boardID]
	if room == nil {
		return false
	}
	room.Engine.SubmitDebugCommand(player.statID, text)
	return true
}

// SubmitScrollReply forwards a scroll hyperlink selection to the engine that
// owns the player. objectStatID is the object that showed the scroll.
//
// It is also how the client says "I have closed the scroll": an empty label is
// no hyperlink (the engine ignores it) but still unfreezes the reader. Every
// scroll the client opens is answered exactly once, on dismiss.
func (rm *RoomManager) SubmitScrollReply(playerID PlayerID, objectStatID int16, label string) bool {
	player := rm.players[playerID]
	if player == nil {
		return false
	}
	player.scrollOpen = false
	room := rm.rooms[player.boardID]
	if room == nil {
		return false
	}
	room.Engine.SubmitScrollReply(objectStatID, label)
	return true
}
