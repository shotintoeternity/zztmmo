package zztgo

import (
	"log"
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
	// TransitGates is the server-owned cross-world gate table (M29.1). Empty
	// means vanilla/MMO intra-world passage behavior. Only first-party LOBBY
	// instances populate it; authored worlds cannot opt in through ZZT data.
	TransitGates map[TransitGateKey]string

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
	quits                []QuitResult
	pendingScores        map[PlayerID]QuitResult
	pendingPlayerEvents  map[PlayerID][]Event
	pendingWorldTransits []WorldTransit

	// recorder, when non-nil, logs the external stimuli this manager applies —
	// joins, leaves, submits, and per-tick inputs — for deterministic replay
	// (M14.2). Every hook is nil-guarded, so recording off is byte-for-byte the
	// prior behavior. recSuppressLeave silences the leave hook during a quit,
	// whose removal is a consequence StepDiffs regenerates rather than an
	// external stimulus. Both are touched only under the same serialization that
	// guards the manager (inst.mu on the server; single goroutine in tests).
	recorder         *SessionRecorder
	recSuppressLeave bool
}

// SetRecorder attaches (or clears, with nil) a session recorder. It must be set
// before the first tick so the recording starts from a pristine world.
func (rm *RoomManager) SetRecorder(r *SessionRecorder) {
	rm.recorder = r
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
	id        PlayerID
	boardID   int16
	statID    int16
	state     *PlayerState
	name      string
	accountID string
	// color is the "#RRGGBB" background other browsers draw this player's ☻ on
	// (M19.1). It rides the roster and nothing else: no element proc reads it,
	// no tile carries it, and SetPlayerColor deliberately records no op — see
	// there for why that is the correct asymmetry with SetPlayerName.
	color string
	// handle/hasProfile are the public profile summary (M24.1). They are
	// presentation-only like color: the full profile stays in ChatDatabase and is
	// requested on demand by PlayerID, never stored in the simulation.
	handle     string
	hasProfile bool
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

type TransitGateKey struct {
	BoardID int16
	X       int16
	Y       int16
}

type WorldTransit struct {
	PlayerID         PlayerID
	DestinationWorld string
	SourceBoard      int16
	SourceX          int16
	SourceY          int16
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
// the caller is about to write: the list is first shifted down from that slot
// and highlightScore written into it, then shown as vanilla's "-- You! --".
// That shift-then-write is what HighScoresAdd does before it draws (EDITOR.PAS
// HighScoresAdd, mirrored at game.go's HighScoreEntryEvent), so the marked row
// carries the score the player just earned and the rows below it are the ones
// their entry displaces — not the untouched list with one slot renamed.
// The shift is applied to a copy: the real list is written only when the name
// comes back, in RecordHighScore.
func (rm *RoomManager) HighScoreLines(highlightPos, highlightScore int16) []string {
	list := rm.highScores
	if highlightPos >= 1 && highlightPos <= HIGH_SCORE_COUNT {
		for i := int16(HIGH_SCORE_COUNT - 1); i >= highlightPos; i-- {
			list[i] = list[i-1]
		}
		list[highlightPos-1] = THighScoreEntry{Name: "-- You! --", Score: highlightScore}
	}

	lines := []string{"Score  Name", "-----  ----------------------------------"}
	for i := 0; i < HIGH_SCORE_COUNT; i++ {
		if Length(list[i].Name) == 0 {
			continue
		}
		lines = append(lines, StrWidth(list[i].Score, 5)+"  "+list[i].Name)
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
	rm.recorder.record(recOp{Op: "submit", Kind: "highscore", Player: playerID, Name: name})
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
	rm.recorder.record(recOp{Op: "submit", Kind: "quit", Player: playerID, Quit: quit})
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
	// A quit's removal is a consequence of the recorded SubmitQuitReply, not a
	// fresh external stimulus: playback's StepDiffs produces the same QuitEvent
	// and removes the player itself. Suppress the leave hook so it is not logged
	// (and double-applied on replay).
	rm.recSuppressLeave = true
	rm.LeavePlayer(playerID)
	rm.recSuppressLeave = false
}

// JoinPlayer mints a RoomManager-scoped PlayerID and joins it. Direct callers
// (mostly tests) get sequential ids from this manager's own counter. The server
// mints process-unique ids and joins through JoinPlayerWithID instead, so ids do
// not collide across instances (M14.1).
func (rm *RoomManager) JoinPlayer(boardID, spawnX, spawnY int16) PlayerID {
	rm.nextPlayerID++
	return rm.JoinPlayerWithID(rm.nextPlayerID, boardID, spawnX, spawnY)
}

// JoinPlayerWithID joins a player under a caller-supplied PlayerID. The server
// uses it to assign server-scoped ids. It advances the manager's own counter
// past the supplied id so a later plain JoinPlayer on the same manager cannot
// reissue it.
func (rm *RoomManager) JoinPlayerWithID(playerID PlayerID, boardID, spawnX, spawnY int16) PlayerID {
	if playerID > rm.nextPlayerID {
		rm.nextPlayerID = playerID
	}
	room := rm.ensureRoom(boardID)
	statID := rm.spawnPlayerInRoom(room, spawnX, spawnY)
	room.Engine.ResetPlayerState(statID)
	drawPlayerArrivalSurroundings(room.Engine, statID)
	player := &roomPlayer{
		id:      playerID,
		boardID: room.BoardID,
		statID:  statID,
		state:   room.Engine.PlayerFor(statID),
	}
	rm.players[playerID] = player
	room.players[playerID] = struct{}{}
	rm.recorder.record(recOp{Op: "join", Player: playerID, Board: boardID, X: spawnX, Y: spawnY})
	return playerID
}

func (rm *RoomManager) SetPlayerName(playerID PlayerID, name string) {
	player := rm.players[playerID]
	if player != nil {
		player.name = name
	}
	rm.recorder.record(recOp{Op: "name", Player: playerID, Name: name})
}

// SetPlayerColor stores the presentation color a player joined with. The
// caller must have validated it (SanitizePlayerColor); an empty string is a
// legitimate value meaning "vanilla white-on-blue".
//
// Nothing is recorded here, and that is the point of the whole milestone. A
// name IS recorded (SetPlayerName above) because it reaches the simulation —
// it is what a high-score entry is written under — so a replay that dropped it
// would diverge. A color reaches only the canvas of the browsers watching, so
// recording it would make two sessions that differ in nothing the simulation
// can observe produce two different recordings. This is the M16.15a decision
// run in the other direction, and m19_1_test.go proves it rather than asserting
// it: a session played with colors set replays byte-identically to one played
// without, and recordVersion does not move.
func (rm *RoomManager) SetPlayerColor(playerID PlayerID, color string) {
	player := rm.players[playerID]
	if player != nil {
		player.color = color
	}
}

func (rm *RoomManager) SetPlayerProfileSummary(playerID PlayerID, handle string, hasProfile bool) {
	player := rm.players[playerID]
	if player != nil {
		player.handle = handle
		player.hasProfile = hasProfile
	}
}

func (rm *RoomManager) SetPlayerIdentity(playerID PlayerID, accountID, name string) {
	player := rm.players[playerID]
	if player != nil {
		player.accountID = accountID
		player.name = name
	}
	rm.recorder.record(recOp{Op: "name", Player: playerID, Name: name})
}

func (rm *RoomManager) PlayerIdentity(playerID PlayerID) (accountID, name string, ok bool) {
	player := rm.players[playerID]
	if player == nil {
		return "", "", false
	}
	return player.accountID, player.name, true
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
	if !rm.recSuppressLeave {
		rm.recorder.record(recOp{Op: "leave", Player: playerID})
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
	diffs, _ := rm.StepDiffsWithBoards(inputs)
	return diffs
}

// StepDiffsWithBoards is StepDiffs plus the same tick's frame addressed to a
// BOARD rather than to a player (M22.1) — what a read-only watcher of that room
// is sent.
//
// The two share one step and one dirty-cell drain, deliberately: a watcher that
// re-rendered the room from a second pass could disagree with the players in it,
// and a watcher that drained its own cells would take them from the players. So
// the board frame is the player frame minus the two things only a participant
// has — the HUD, which is one player's inventory, and the events, which are
// addressed to somebody who can answer them.
func (rm *RoomManager) StepDiffsWithBoards(inputs map[PlayerID]PlayerInput) (map[PlayerID]DiffMessage, map[int16]DiffMessage) {
	// Record this tick before stepping: the buffered ops are exactly the
	// external stimuli that arrived since the last step, and inputs is what the
	// step consumes. Quit/transfer removals happen below and are deliberately
	// not recorded — playback regenerates them (M14.2).
	rm.recorder.flush(inputs)

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
		// Each room has an isolated engine and therefore a copied TWorld.
		// World-scope state (flags) is not board state: refresh before this room
		// runs so a #set in an earlier room is visible to every later player this
		// tick. Adding another world-scope field is one line in copyWorldScope.
		rm.refreshRoomWorldScope(room)
		inputsForRoom := engineInputs[boardID]
		if inputsForRoom == nil {
			inputsForRoom = map[int16]PlayerInput{}
		}
		if !rm.stepRoom(room, inputsForRoom) {
			continue
		}

		// Transfers and quits both name a stat id, and both reindex stat ids
		// when they are applied. Resolve them to stable PlayerIDs here, while
		// the ids still mean what the engine meant by them, and act below.
		for _, event := range room.Engine.DrainEvents() {
			switch ev := event.(type) {
			case TransferEvent:
				if playerID, found := rm.playerIDForStat(boardID, ev.StatId); found {
					if destination, ok := rm.worldTransitDestination(boardID, ev); ok {
						rm.pendingWorldTransits = append(rm.pendingWorldTransits, WorldTransit{
							PlayerID:         playerID,
							DestinationWorld: destination,
							SourceBoard:      boardID,
							SourceX:          ev.SourceX,
							SourceY:          ev.SourceY,
						})
						continue
					}
					if ev.SoundNotes != "" {
						sound := SoundEvent{Notes: ev.SoundNotes, Priority: ev.SoundPriority, StatId: ev.StatId}
						rm.pendingPlayerEvents[playerID] = append(rm.pendingPlayerEvents[playerID], sound)
					}
					// M16.8a gap 2: unlike every sibling case, this used to stop here —
					// the traveler's own TransferEvent was never queued, so the wire
					// "transfer" ProtocolEvent (protocol.go's ProtocolEvents already
					// converts it, and main.ts already has a case for it) was dead
					// code. Queued here, it rides in the same BoardChangeMessage.Events
					// the arriving snapshot already carries (DrainPlayerEvents is read
					// into that message, never a separate one) — traveler-only, like
					// the sound above.
					rm.pendingPlayerEvents[playerID] = append(rm.pendingPlayerEvents[playerID], ev)
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
			case WalkClickEvent:
				// Always attributed to the mover, same as a player's own pickup/shot
				// sound (deviation per-player-sound) — never room-wide.
				if playerID, found := rm.playerIDForStat(boardID, ev.StatId); found {
					rm.pendingPlayerEvents[playerID] = append(rm.pendingPlayerEvents[playerID], ev)
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
		rm.publishRoomWorldScope(room)
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
	boardDiffs := make(map[int16]DiffMessage)
	for _, boardID := range rm.roomIDs() {
		room := rm.rooms[boardID]
		if room == nil || len(room.players) == 0 {
			continue
		}
		cells := room.Engine.DrainScreenDirty()
		events := ProtocolEvents(roomEvents[boardID])
		players := rm.playerSnapshotsForRoom(room)
		// One hash for the room, not one per recipient. Every frame built below
		// carries the same value — it is the room's state, and the room stepped
		// once — and the watcher's frame added here is the third audience for it.
		hash := StateHash(room.Engine)
		boardDiffs[room.BoardID] = DiffMessage{
			Type:    MessageTypeDiff,
			BoardID: room.BoardID,
			Tick:    room.Engine.CurrentTick,
			Hash:    hash,
			Cells:   cells,
			Players: players,
		}
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
				Hash:    hash,
				Cells:   cells,
				Players: players,
				HUD:     &hud,
				Events:  events,
			}
		}
	}
	return diffs, boardDiffs
}

func (rm *RoomManager) worldTransitDestination(boardID int16, transfer TransferEvent) (string, bool) {
	if len(rm.TransitGates) == 0 {
		return "", false
	}
	destination := rm.TransitGates[TransitGateKey{BoardID: boardID, X: transfer.SourceX, Y: transfer.SourceY}]
	if destination == "" {
		return "", false
	}
	return destination, true
}

func (rm *RoomManager) DrainWorldTransits() []WorldTransit {
	transits := rm.pendingWorldTransits
	rm.pendingWorldTransits = nil
	return transits
}

// stepRoom contains a simulation panic to the room that caused it.  Engines
// are deliberately isolated per board, so retaining a partially-mutated one
// after a panic is unsafe; drop it and its players instead of allowing a bad
// uploaded/generated world to stop unrelated rooms or the server process.
func (rm *RoomManager) stepRoom(room *Room, inputs map[int16]PlayerInput) (ok bool) {
	ok = true
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("zztgo: isolating board %d after simulation panic: %v", room.BoardID, recovered)
			rm.dropPanickedRoom(room)
			ok = false
		}
	}()
	room.Engine.GameStepWithInputs(inputs)
	return ok
}

func (rm *RoomManager) dropPanickedRoom(room *Room) {
	for playerID := range room.players {
		delete(rm.players, playerID)
		delete(rm.pendingPlayerEvents, playerID)
		delete(rm.pendingScores, playerID)
	}
	delete(rm.rooms, room.BoardID)
}

func (rm *RoomManager) PlayerState(playerID PlayerID) (*PlayerState, bool) {
	player := rm.players[playerID]
	if player == nil {
		return nil, false
	}
	return player.state, true
}

// ApplyPlayerState overwrites a player's state wholesale. The server calls it on
// the authenticated fresh-join path to hand a returning account the inventory
// from its sidecar — a stimulus the room cannot derive, so it is recorded like a
// join (M16.15a). Only a state that was actually applied is logged: a refused
// call changes nothing here, and replaying it would change nothing there.
func (rm *RoomManager) ApplyPlayerState(playerID PlayerID, state PlayerState) bool {
	player := rm.players[playerID]
	if player == nil || player.state == nil {
		return false
	}
	*player.state = state
	applied := state
	rm.recorder.record(recOp{Op: "state", Player: playerID, State: &applied})
	return true
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

// RoomStateHashes returns the per-room StateHash keyed by board id, in a stable
// order. It is the determinism checkpoint used to prove a replay reproduces a
// recorded session tick-for-tick (M14.2).
func (rm *RoomManager) RoomStateHashes() map[int16]uint64 {
	hashes := make(map[int16]uint64, len(rm.rooms))
	for _, boardID := range rm.roomIDs() {
		room := rm.rooms[boardID]
		if room == nil {
			continue
		}
		hashes[boardID] = StateHash(room.Engine)
	}
	return hashes
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
	// `you` is built from the engine, which has no idea who this player is, so
	// the presentation fields are attached the same way the roster's are
	// (M19.1). Without this a client would learn its own color only from its
	// entry in `players` — true today, but it is the same fact and it should
	// not disagree with itself.
	snapshot.You.Name = player.name
	snapshot.You.Color = player.color
	// The dirty-cell list belongs to the ROOM, not to this connection, so it may
	// only be discarded when nobody else could be owed it. A snapshot already
	// carries the whole screen, which is why draining it here is free for the
	// recipient — but the cells drawn between ticks (a newcomer's own square,
	// an arriving traveler's) are the only notice the players already in the
	// room ever get that someone appeared, and draining threw them away: the
	// newcomer stayed invisible to everyone until something else happened to
	// repaint that square.
	//
	// Found while landing M16.12a. The old engine-global player glyph hid it:
	// an energized player flipped the shared byte, so every OTHER player's tick
	// took ElementPlayerTick's "force it back" branch and redrew their own
	// square, incidentally restoring the cell this drop had lost. Making the
	// blink per-player removed that accidental repaint and left the ghost on
	// screen (NOTES.md M16.12a).
	if len(room.players) == 1 {
		if _, alone := room.players[playerID]; alone {
			room.Engine.DrainScreenDirty()
		}
	}
	room.Engine.DrainEvents()
	return snapshot, true
}

// SpectatorSnapshot is the whole-screen frame a read-only watcher of boardID
// gets (M22.1), plus the room engine it was rendered from — nil when that board
// has nobody in it. The server compares that engine against the one it rendered
// last, which is how a watcher is re-sent a full frame when a board it is
// watching comes back to life under a new engine.
//
// It is deliberately NOT Snapshot with the player parts blanked. Snapshot does
// two things a watcher must not do: it drains the room's dirty cells (they are
// the only notice the players already in the room get that someone appeared —
// see there), and it drains the engine's events. A watcher takes nothing from
// the room; it reads the screen the room has already drawn.
//
// The event channel stays shut for the same reason the HUD does. Every event is
// addressed to somebody who can answer it — a scroll wants a reply, a save
// prompt wants a filename, a high score wants a name — and a watcher can answer
// none of them; a room-wide #play would be the one safe member of the set, and
// half-opening the channel for it is how the other half gets let through later.
//
// An idle board is rendered from the frozen world on a throwaway engine, which
// is what "the board sitting still" is: BoardOpen paints the stored bytes, and
// nothing ticks it. The copy is cloneWorld's, exactly as TitleSim's is, so a
// read-only render can never write through to the boards the rooms are playing.
func (rm *RoomManager) SpectatorSnapshot(boardID int16) (SnapshotMessage, *Engine) {
	room := rm.rooms[boardID]
	if room == nil {
		engine := newSpectatorEngine(rm.world, boardID)
		return SnapshotMessage{
			Type:      MessageTypeSnapshot,
			BoardID:   boardID,
			Spectator: true,
			// A watcher has no stat, and stat 0 is a real one: -1 is the same
			// "nobody" ProtocolEvent.PlayerStatID already uses.
			You:    PlayerSnapshot{StatID: -1},
			Screen: screenCells(engine),
		}, nil
	}
	return SnapshotMessage{
		Type:      MessageTypeSnapshot,
		BoardID:   room.BoardID,
		Tick:      room.Engine.CurrentTick,
		Seed:      room.Engine.RandSeed,
		Hash:      StateHash(room.Engine),
		Spectator: true,
		You:       PlayerSnapshot{StatID: -1},
		Players:   rm.playerSnapshotsForRoom(room),
		Screen:    screenCells(room.Engine),
	}, room.Engine
}

// newSpectatorEngine renders one board of a world and is never ticked. It is
// ensureRoom's opening without the room: same headless multi-room engine, same
// BoardOpen/TransitionDrawToBoard paint, on a deep copy so nothing it touches is
// shared with a live room.
func newSpectatorEngine(world TWorld, boardID int16) *Engine {
	engine := NewEngine()
	engine.Headless = true
	engine.MultiRoom = true
	engine.GameStateElement = E_PLAYER
	engine.SetInputSource(&ScriptedInput{})
	engine.World = cloneWorld(world)
	engine.BoardOpen(boardID)
	engine.GenerateTransitionTable()
	engine.TransitionDrawToBoard()
	return engine
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
			snap := playerSnapshot(room.Engine, id, p.statID)
			// Presentation only, and deliberately attached HERE rather than
			// inside playerSnapshot: the engine knows where a player is, the
			// room manager knows who they are (M19.1).
			snap.Name = p.name
			snap.Color = p.color
			snap.Handle = p.handle
			snap.HasProfile = p.hasProfile
			players = append(players, snap)
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
	rm.publishRoomWorldScope(room)
	delete(rm.rooms, boardID)
	rm.syncFrozenBoardToLiveRooms(boardID)
}

// copyWorldScope copies the world-scope subset of TWorldInfo — state shared by
// every room of a world rather than per-player or per-engine — from src to dst.
// It is the single list the two seams (refresh/publish) iterate: adding the next
// world-scope field is one line here. The M14.0 audit (NOTES.md 2026-07-12)
// found Flags is the only per-tick-mutable world-scope field; Name is
// world-scope but immutable during play and read straight off rm.world, so it is
// deliberately out of this seam.
func copyWorldScope(dst, src *TWorldInfo) {
	dst.Flags = src.Flags
}

// worldScopeEqual reports whether two infos already agree on every world-scope
// field, so publishRoomWorldScope can skip the O(rooms) fan-out when nothing a
// room did this tick changed shared state.
func worldScopeEqual(a, b *TWorldInfo) bool {
	return a.Flags == b.Flags
}

// refreshRoomWorldScope pulls the shared world-scope state into a room's engine
// before it steps, so a #set/#clear made by an earlier-ticking room this tick is
// visible to this one. Value copy: rooms step sequentially on one goroutine, so
// no shared pointer and no race.
func (rm *RoomManager) refreshRoomWorldScope(room *Room) {
	if room == nil {
		return
	}
	copyWorldScope(&room.Engine.World.Info, &rm.world.Info)
}

// publishRoomWorldScope copies a just-stepped room's world-scope state back to
// the shared world and fans it to every live room, so #set/#clear is not
// stranded on the board that executed it. Generalizes the M14.0-retired
// syncWorldFlagsFromRoom to the full copyWorldScope list.
func (rm *RoomManager) publishRoomWorldScope(source *Room) {
	if source == nil || worldScopeEqual(&source.Engine.World.Info, &rm.world.Info) {
		return
	}
	copyWorldScope(&rm.world.Info, &source.Engine.World.Info)
	for _, room := range rm.rooms {
		copyWorldScope(&room.Engine.World.Info, &rm.world.Info)
	}
}

func (rm *RoomManager) syncFrozenBoardToLiveRooms(boardID int16) {
	for _, roomID := range rm.roomIDs() {
		room := rm.rooms[roomID]
		room.Engine.World.BoardData[boardID] = append([]byte(nil), rm.world.BoardData[boardID]...)
		room.Engine.World.BoardLen[boardID] = rm.world.BoardLen[boardID]
		rm.refreshRoomWorldScope(room)
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
	rm.recorder.record(recOp{Op: "submit", Kind: "debug", Player: playerID, Text: text})
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
	rm.recorder.record(recOp{Op: "submit", Kind: "scroll", Player: playerID, StatID: objectStatID, Label: label})
	player.scrollOpen = false
	room := rm.rooms[player.boardID]
	if room == nil {
		return false
	}
	room.Engine.SubmitScrollReply(objectStatID, label)
	return true
}
