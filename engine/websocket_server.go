package zztgo

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const ServerTickDuration = 110 * time.Millisecond
const ServerReadLimit = 1 << 20

type WebSocketServer struct {
	RoomManager  *RoomManager
	DefaultBoard int16
	TickDuration time.Duration
	OriginHosts  []string
	// SavesDir is the only directory a client's save name can reach. Empty
	// refuses every save, which is what NewWebSocketServer leaves it as: a test
	// must never write to disk.
	SavesDir string

	mu              sync.Mutex
	clients         map[PlayerID]*webSocketClient
	inputs          map[PlayerID]PlayerInput
	Instances       map[string]*WorldInstance
	DefaultInstance *WorldInstance
	ChatDB          ChatDatabase
}

type WorldInstance struct {
	Name        string
	RoomManager *RoomManager
	Clients     map[PlayerID]*webSocketClient
	Inputs      map[PlayerID]PlayerInput
	mu          sync.Mutex
}

type webSocketClient struct {
	playerID  PlayerID
	boardID   int16
	conn      *websocket.Conn
	mu        sync.Mutex
	worldName string
}

func NewWebSocketServer(world TWorld, defaultBoard int16) *WebSocketServer {
	rm := NewRoomManager(world)
	name := rm.WorldName()
	if name == "Untitled" || name == "" {
		name = "TOWN"
	}
	inst := &WorldInstance{
		Name:        name,
		RoomManager: rm,
		Clients:     make(map[PlayerID]*webSocketClient),
		Inputs:      make(map[PlayerID]PlayerInput),
	}
	s := &WebSocketServer{
		RoomManager:  rm,
		DefaultBoard: defaultBoard,
		TickDuration: ServerTickDuration,
		OriginHosts:  []string{"localhost:*", "127.0.0.1:*"},
		clients:      make(map[PlayerID]*webSocketClient),
		inputs:       make(map[PlayerID]PlayerInput),
		Instances:    make(map[string]*WorldInstance),
		ChatDB:       NewMemChatDatabase(),
	}
	s.DefaultInstance = inst
	s.Instances[name] = inst
	return s
}

func (s *WebSocketServer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.TickDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

func (s *WebSocketServer) Tick(ctx context.Context) {
	s.mu.Lock()
	// Legacy tick for compatibility
	if len(s.clients) > 0 {
		inputs := s.inputs
		s.inputs = make(map[PlayerID]PlayerInput)
		diffs := s.RoomManager.StepDiffs(inputs)
		clients := make(map[PlayerID]*webSocketClient, len(s.clients))
		messages := make(map[PlayerID]interface{}, len(diffs))
		for playerID, client := range s.clients {
			clients[playerID] = client
		}
		for playerID, diff := range diffs {
			client := s.clients[playerID]
			if client == nil {
				continue
			}
			if client.boardID != 0 && client.boardID != diff.BoardID {
				snapshot, ok := s.RoomManager.Snapshot(playerID)
				if ok {
					client.boardID = snapshot.BoardID
					snapshot.Events = append(snapshot.Events, ProtocolEvents(s.RoomManager.DrainPlayerEvents(playerID))...)
					messages[playerID] = BoardChangeMessage{Type: MessageTypeBoardChange, Snapshot: snapshot}
					continue
				}
			}
			client.boardID = diff.BoardID
			diff.Events = append(diff.Events, ProtocolEvents(s.RoomManager.DrainPlayerEvents(playerID))...)
			messages[playerID] = diff
		}
		for _, quit := range s.RoomManager.DrainQuits() {
			if s.clients[quit.PlayerID] == nil {
				continue
			}
			messages[quit.PlayerID] = s.quitOutcome(s.RoomManager, quit)
		}
		s.mu.Unlock()

		for playerID, message := range messages {
			client := clients[playerID]
			if client != nil {
				_ = client.write(ctx, message)
			}
		}
		s.mu.Lock()
	}

	// Dynamic instances tick
	var instances []*WorldInstance
	for _, inst := range s.Instances {
		if len(s.clients) > 0 && inst.RoomManager == s.RoomManager {
			continue
		}
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.Tick(ctx, s)
	}
}

func (inst *WorldInstance) Tick(ctx context.Context, s *WebSocketServer) {
	inst.mu.Lock()
	inputs := inst.Inputs
	inst.Inputs = make(map[PlayerID]PlayerInput)
	diffs := inst.RoomManager.StepDiffs(inputs)
	clients := make(map[PlayerID]*webSocketClient, len(inst.Clients))
	messages := make(map[PlayerID]interface{}, len(diffs))
	for playerID, client := range inst.Clients {
		clients[playerID] = client
	}
	for playerID, diff := range diffs {
		client := inst.Clients[playerID]
		if client == nil {
			continue
		}
		if client.boardID != 0 && client.boardID != diff.BoardID {
			snapshot, ok := inst.RoomManager.Snapshot(playerID)
			if ok {
				client.boardID = snapshot.BoardID
				snapshot.Events = append(snapshot.Events, ProtocolEvents(inst.RoomManager.DrainPlayerEvents(playerID))...)
				messages[playerID] = BoardChangeMessage{Type: MessageTypeBoardChange, Snapshot: snapshot}
				continue
			}
		}
		client.boardID = diff.BoardID
		diff.Events = append(diff.Events, ProtocolEvents(inst.RoomManager.DrainPlayerEvents(playerID))...)
		messages[playerID] = diff
	}
	for _, quit := range inst.RoomManager.DrainQuits() {
		if inst.Clients[quit.PlayerID] == nil {
			continue
		}
		messages[quit.PlayerID] = s.quitOutcome(inst.RoomManager, quit)
	}
	inst.mu.Unlock()

	for playerID, message := range messages {
		client := clients[playerID]
		if client != nil {
			_ = client.write(ctx, message)
		}
	}
}

func (s *WebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	worldName := r.URL.Query().Get("world")
	var inst *WorldInstance
	var safeWorld string
	if worldName == "" {
		inst = s.DefaultInstance
		safeWorld = inst.Name
	} else {
		var err error
		safeWorld, err = SanitizeSaveName(worldName)
		if err != nil {
			http.Error(w, "invalid world name", http.StatusBadRequest)
			return
		}
		inst, err = s.GetOrCreateInstance(safeWorld)
		if err != nil {
			http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.OriginHosts})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	ctx := r.Context()
	var join JoinMessage
	if err := wsjson.Read(ctx, conn, &join); err != nil {
		return
	}
	if join.Board == 0 {
		join.Board = inst.RoomManager.FrozenWorld().Info.CurrentBoard
		if join.Board == 0 {
			join.Board = s.DefaultBoard
		}
		if join.Board == 0 {
			join.Board = 1
		}
	}

	client := &webSocketClient{conn: conn, worldName: safeWorld}
	inst.mu.Lock()
	playerID := inst.RoomManager.JoinPlayer(join.Board, 0, 0)
	inst.RoomManager.SetPlayerName(playerID, join.Name)
	client.playerID = playerID
	inst.Clients[playerID] = client
	if inst.RoomManager == s.RoomManager {
		s.mu.Lock()
		s.clients[playerID] = client
		s.mu.Unlock()
	}
	snapshot, ok := inst.RoomManager.Snapshot(playerID)
	if ok {
		client.boardID = snapshot.BoardID
		err = client.write(ctx, snapshot)
	}
	inst.mu.Unlock()

	if !ok {
		s.removeClientFromInstance(inst, playerID)
		return
	}
	if err != nil {
		s.removeClientFromInstance(inst, playerID)
		return
	}

	if s.ChatDB != nil {
		recs, dbErr := s.ChatDB.GetRecentMessages(50)
		if dbErr == nil {
			for _, rec := range recs {
				msg := struct {
					Type    string `json:"type"`
					From    string `json:"from"`
					Text    string `json:"text"`
					History bool   `json:"history"`
				}{
					Type:    "chat",
					From:    rec.From,
					Text:    rec.Text,
					History: true,
				}
				_ = client.write(ctx, msg)
			}
		}
	}

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			break
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case MessageTypeDebugCommand:
			var cmd DebugCommandMessage
			if err := json.Unmarshal(raw, &cmd); err != nil {
				continue
			}
			s.submitDebugCommandInInstance(inst, playerID, cmd.Text)
		case MessageTypeScrollReply:
			var reply ScrollReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitScrollReplyInInstance(inst, playerID, reply.StatID, reply.Label)
		case MessageTypeQuitReply:
			var reply QuitReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitQuitReplyInInstance(inst, playerID, reply.Quit)
		case MessageTypeHighScoreName:
			var entry HighScoreNameMessage
			if err := json.Unmarshal(raw, &entry); err != nil {
				continue
			}
			s.submitHighScoreNameInInstance(ctx, inst, playerID, entry.Name)
		case MessageTypeSaveFilename:
			var save SaveFilenameMessage
			if err := json.Unmarshal(raw, &save); err != nil {
				continue
			}
			s.submitSaveFilenameInInstance(ctx, inst, playerID, save.Name)
		case "chat":
			var chat struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &chat); err != nil {
				continue
			}
			if strings.TrimSpace(chat.Text) == "" {
				continue
			}
			inst.mu.Lock()
			name := "browser"
			player := inst.RoomManager.players[playerID]
			if player != nil && player.name != "" {
				name = player.name
			}
			inst.mu.Unlock()
			if s.ChatDB != nil {
				_, _ = s.ChatDB.AddMessage(name, chat.Text)
			}
			s.BroadcastGlobalChat(ctx, name, chat.Text)
		default:
			var input InputMessage
			if err := json.Unmarshal(raw, &input); err != nil {
				continue
			}
			inst.setInput(s, playerID, inputMessageToPlayerInput(input))
		}
	}
	s.removeClientFromInstance(inst, playerID)
}

// quitOutcome is what a player sees after confirming the quit prompt: either
// vanilla's "New high score" window, or a bare quit so the client can return to
// the title screen. Callers hold s.mu or inst.mu.
func (s *WebSocketServer) quitOutcome(rm *RoomManager, quit QuitResult) EventMessage {
	event := ProtocolEvent{Type: "quit", Score: quit.Score}
	if quit.ListPos > 0 {
		event = ProtocolEvent{
			Type:    "highScoreEntry",
			Score:   quit.Score,
			ListPos: quit.ListPos,
			Title:   "New high score for " + rm.WorldName(),
			Lines:   rm.HighScoreLines(quit.ListPos),
		}
	}
	return EventMessage{Type: MessageTypeEvent, Event: event}
}

// submitQuitReply routes a client's answer to the quit prompt. A confirmed quit
// becomes a QuitEvent on the next step, which RoomManager turns into a
// DrainQuits entry — the player is never removed from a room mid-tick.
func (s *WebSocketServer) submitQuitReply(playerID PlayerID, quit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[playerID]; !ok {
		return
	}
	s.RoomManager.SubmitQuitReply(playerID, quit)
}

func (s *WebSocketServer) submitQuitReplyInInstance(inst *WorldInstance, playerID PlayerID, quit bool) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if _, ok := inst.Clients[playerID]; !ok {
		return
	}
	inst.RoomManager.SubmitQuitReply(playerID, quit)
}

// submitHighScoreName writes the name a quitter typed into the slot their score
// earned, then sends the finished list back for display. A client that was
// never offered a slot gets nothing: RecordHighScore refuses it.
func (s *WebSocketServer) submitHighScoreName(ctx context.Context, playerID PlayerID, name string) {
	s.mu.Lock()
	client := s.clients[playerID]
	if client == nil || !s.RoomManager.RecordHighScore(playerID, name) {
		s.mu.Unlock()
		return
	}
	message := EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
		Type:  "highScores",
		Title: "High scores for " + s.RoomManager.WorldName(),
		Lines: s.RoomManager.HighScoreLines(0),
	}}
	s.mu.Unlock()

	_ = client.write(ctx, message)
}

func (s *WebSocketServer) submitHighScoreNameInInstance(ctx context.Context, inst *WorldInstance, playerID PlayerID, name string) {
	inst.mu.Lock()
	client := inst.Clients[playerID]
	if client == nil || !inst.RoomManager.RecordHighScore(playerID, name) {
		inst.mu.Unlock()
		return
	}
	message := EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
		Type:  "highScores",
		Title: "High scores for " + inst.RoomManager.WorldName(),
		Lines: inst.RoomManager.HighScoreLines(0),
	}}
	inst.mu.Unlock()

	_ = client.write(ctx, message)
}

// submitSaveFilename answers a savePrompt event. The name is a client's, so it
// never reaches a path before SanitizeSaveName; the reply tells the player what
// their snapshot is called, or why it was refused.
//
// The snapshot deliberately does NOT go through Engine.SubmitSaveFilename: that
// writes one room engine's world — a single board, with the other rooms stale —
// to the process working directory. It is the terminal's path. The server saves
// the whole world through the RoomManager.
//
// The write happens under s.mu, as RecordHighScore's does: a room may not tick
// while its boards are being serialized.
func (s *WebSocketServer) submitSaveFilename(ctx context.Context, playerID PlayerID, name string) {
	s.mu.Lock()
	client := s.clients[playerID]
	if client == nil {
		s.mu.Unlock()
		return
	}
	// An empty name is vanilla's cancelled prompt: save nothing, say nothing.
	if name == "" {
		s.mu.Unlock()
		return
	}
	path, err := s.RoomManager.SaveSnapshot(s.SavesDir, name, playerID)
	s.mu.Unlock()

	event := ProtocolEvent{Type: "saveResult"}
	if err != nil {
		event.Error = err.Error()
	} else {
		event.Filename = strings.TrimSuffix(filepath.Base(path), ".SAV")
	}
	_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: event})
}

func (s *WebSocketServer) submitSaveFilenameInInstance(ctx context.Context, inst *WorldInstance, playerID PlayerID, name string) {
	inst.mu.Lock()
	client := inst.Clients[playerID]
	if client == nil {
		inst.mu.Unlock()
		return
	}
	if name == "" {
		inst.mu.Unlock()
		return
	}
	path, err := inst.RoomManager.SaveSnapshot(s.SavesDir, name, playerID)
	inst.mu.Unlock()

	event := ProtocolEvent{Type: "saveResult"}
	if err != nil {
		event.Error = err.Error()
	} else {
		event.Filename = strings.TrimSuffix(filepath.Base(path), ".SAV")
	}
	_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: event})
}

func (s *WebSocketServer) GetOrCreateInstance(worldName string) (*WorldInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Instances == nil {
		s.Instances = make(map[string]*WorldInstance)
	}

	inst := s.Instances[worldName]
	if inst != nil {
		return inst, nil
	}

	dir := "."
	if E != nil && E.LoadedGameFileName != "" {
		dir = filepath.Dir(E.LoadedGameFileName)
	}
	world, err := LoadPristineWorld(dir, worldName)
	if err != nil {
		return nil, err
	}

	rm := NewRoomManager(world)
	rm.HighScorePath = filepath.Join(dir, worldName+".HI")
	rm.LoadHighScores()

	inst = &WorldInstance{
		Name:        worldName,
		RoomManager: rm,
		Clients:     make(map[PlayerID]*webSocketClient),
		Inputs:      make(map[PlayerID]PlayerInput),
	}
	s.Instances[worldName] = inst
	return inst, nil
}

func LoadPristineWorld(dir, name string) (TWorld, error) {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return TWorld{}, err
	}
	path := filepath.Join(dir, safe+".ZZT")
	f, err := os.Open(path)
	if err != nil {
		return TWorld{}, err
	}
	defer f.Close()

	scratch := newSnapshotEngine()
	if err := scratch.worldReadFrom(f, false, nil); err != nil {
		return TWorld{}, err
	}
	return scratch.World, nil
}

func (s *WebSocketServer) submitDebugCommandInInstance(inst *WorldInstance, playerID PlayerID, text string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if _, ok := inst.Clients[playerID]; !ok {
		return
	}
	inst.RoomManager.SubmitDebugCommand(playerID, text)
}

func (s *WebSocketServer) submitScrollReplyInInstance(inst *WorldInstance, playerID PlayerID, objectStatID int16, label string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if _, ok := inst.Clients[playerID]; !ok {
		return
	}
	inst.RoomManager.SubmitScrollReply(playerID, objectStatID, label)
}

func inputMessageToPlayerInput(input InputMessage) PlayerInput {
	playerInput := PlayerInput{
		DeltaX: input.DeltaX,
		DeltaY: input.DeltaY,
		Shift:  input.Shift,
		Key:    input.Key,
	}
	if input.Keymask == 0 {
		return playerInput
	}

	playerInput = PlayerInput{}
	playerInput.Shift = input.Keymask&(InputMaskShift|InputMaskShoot) != 0
	switch {
	case input.Keymask&InputMaskUp != 0:
		playerInput.DeltaY = -1
		playerInput.Key = KEY_UP
	case input.Keymask&InputMaskDown != 0:
		playerInput.DeltaY = 1
		playerInput.Key = KEY_DOWN
	case input.Keymask&InputMaskLeft != 0:
		playerInput.DeltaX = -1
		playerInput.Key = KEY_LEFT
	case input.Keymask&InputMaskRight != 0:
		playerInput.DeltaX = 1
		playerInput.Key = KEY_RIGHT
	case input.Keymask&InputMaskShoot != 0:
		playerInput.Key = ' '
	}
	return playerInput
}

func (s *WebSocketServer) setInput(playerID PlayerID, input PlayerInput) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[playerID]; ok {
		s.inputs[playerID] = input
	}
}

func (inst *WorldInstance) setInput(s *WebSocketServer, playerID PlayerID, input PlayerInput) {
	inst.mu.Lock()
	if _, ok := inst.Clients[playerID]; ok {
		inst.Inputs[playerID] = input
	}
	inst.mu.Unlock()

	if inst.RoomManager == s.RoomManager {
		s.mu.Lock()
		if _, ok := s.clients[playerID]; ok {
			s.inputs[playerID] = input
		}
		s.mu.Unlock()
	}
}

func (s *WebSocketServer) removeClient(playerID PlayerID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.clients, playerID)
	delete(s.inputs, playerID)
	s.RoomManager.LeavePlayer(playerID)
	// They may have quit and closed the tab before typing a name.
	s.RoomManager.DiscardPendingScore(playerID)
}

func (s *WebSocketServer) removeClientFromInstance(inst *WorldInstance, playerID PlayerID) {
	inst.mu.Lock()
	delete(inst.Clients, playerID)
	delete(inst.Inputs, playerID)
	inst.RoomManager.LeavePlayer(playerID)
	inst.RoomManager.DiscardPendingScore(playerID)
	inst.mu.Unlock()

	if inst.RoomManager == s.RoomManager {
		s.mu.Lock()
		delete(s.clients, playerID)
		delete(s.inputs, playerID)
		s.mu.Unlock()
	}
}

func (c *webSocketClient) write(ctx context.Context, message interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, c.conn, message)
}

func (s *WebSocketServer) RestoreSnapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.RoomManager.RestoreSnapshot(s.SavesDir, name)
}

func (s *WebSocketServer) LoadWorld(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := "."
	if E != nil && E.LoadedGameFileName != "" {
		dir = filepath.Dir(E.LoadedGameFileName)
	}
	return s.RoomManager.LoadWorld(dir, name)
}

func (s *WebSocketServer) BroadcastGlobalChat(ctx context.Context, from, text string) {
	s.mu.Lock()
	var clients []*webSocketClient
	for _, inst := range s.Instances {
		inst.mu.Lock()
		for _, client := range inst.Clients {
			clients = append(clients, client)
		}
		inst.mu.Unlock()
	}
	s.mu.Unlock()

	msg := struct {
		Type string `json:"type"`
		From string `json:"from"`
		Text string `json:"text"`
	}{
		Type: "chat",
		From: from,
		Text: text,
	}

	for _, client := range clients {
		_ = client.write(ctx, msg)
	}
}
