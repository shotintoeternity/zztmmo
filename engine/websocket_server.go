package zztgo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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

// ReconnectGraceTicks is how long a detached player's stat lingers on the board
// before removal, counted in ticks (not wall-clock) so tests can step it
// deterministically. 545 ticks ≈ 60s at the 110ms server tick (M13.2).
const ReconnectGraceTicks = 545

type WebSocketServer struct {
	RoomManager  *RoomManager
	DefaultBoard int16
	TickDuration time.Duration
	OriginHosts  []string
	// SavesDir is the only directory a client's save name can reach. Empty
	// refuses every save, which is what NewWebSocketServer leaves it as: a test
	// must never write to disk.
	SavesDir string
	// WorldsDir is where the editor writes published .ZZT worlds and where the
	// world picker lists them (M5.6). Empty falls back to the loaded world's
	// directory, then the working directory — the picker's historical behavior.
	WorldsDir string

	mu              sync.Mutex
	clients         map[PlayerID]*webSocketClient
	inputs          map[PlayerID]PlayerInput
	Instances       map[string]*WorldInstance
	DefaultInstance *WorldInstance
	EditorSessions  map[*webSocketClient]*EditorSession
	ChatDB          ChatDatabase
}

type WorldInstance struct {
	Name string
	// SourceWorld is the pristine content used for isolated title and editor
	// copies. RoomManager's frozen world is live state and must not seed edits.
	SourceWorld TWorld
	RoomManager *RoomManager
	Clients     map[PlayerID]*webSocketClient
	Inputs      map[PlayerID]PlayerInput
	// Title animates board 0 for browsers sitting on the title screen. It owns
	// its own Engine and shares no state with RoomManager — see TitleSim.
	Title *TitleSim
	// Detached, ResumeTokens, and TokensByPlayer implement reconnect grace
	// (M13.2). A dropped socket detaches its player (leaves Clients/Inputs but
	// keeps the stat) and Detached counts down ReconnectGraceTicks to removal on
	// the tick goroutine. ResumeTokens maps a minted token to its player, and
	// TokensByPlayer is the reverse index used to delete a player's token on
	// removal. All three are guarded by mu.
	Detached       map[PlayerID]int
	ResumeTokens   map[string]PlayerID
	TokensByPlayer map[PlayerID]string
	mu             sync.Mutex
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
		Name:           name,
		SourceWorld:    cloneWorld(world),
		RoomManager:    rm,
		Clients:        make(map[PlayerID]*webSocketClient),
		Inputs:         make(map[PlayerID]PlayerInput),
		Title:          NewTitleSim(world),
		Detached:       make(map[PlayerID]int),
		ResumeTokens:   make(map[string]PlayerID),
		TokensByPlayer: make(map[PlayerID]string),
	}
	s := &WebSocketServer{
		RoomManager:    rm,
		DefaultBoard:   defaultBoard,
		TickDuration:   ServerTickDuration,
		OriginHosts:    []string{"localhost:*", "127.0.0.1:*"},
		clients:        make(map[PlayerID]*webSocketClient),
		inputs:         make(map[PlayerID]PlayerInput),
		Instances:      make(map[string]*WorldInstance),
		EditorSessions: make(map[*webSocketClient]*EditorSession),
		ChatDB:         NewMemChatDatabase(),
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
	// Advance reconnect-grace countdowns before stepping, so an expired player is
	// removed on this same tick goroutine (M13.2).
	s.expireDetached()

	s.mu.Lock()
	// Legacy tick for compatibility
	if len(s.clients) > 0 {
		inputs := s.inputs
		s.inputs = make(map[PlayerID]PlayerInput)
		diffs := safeStepDiffs("legacy", s.RoomManager, inputs)
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
	var titles []*TitleSim
	for _, inst := range s.Instances {
		// Every world's title board advances, including the default instance's,
		// which the room loop above may already have stepped. They are separate
		// engines; the title sim is nobody's room.
		if inst.Title != nil {
			titles = append(titles, inst.Title)
		}
		if len(s.clients) > 0 && inst.RoomManager == s.RoomManager {
			continue
		}
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.Tick(ctx, s)
	}
	for _, title := range titles {
		title.Tick()
	}
}

func (inst *WorldInstance) Tick(ctx context.Context, s *WebSocketServer) {
	inst.mu.Lock()
	inputs := inst.Inputs
	inst.Inputs = make(map[PlayerID]PlayerInput)
	diffs := safeStepDiffs(inst.Name, inst.RoomManager, inputs)
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

// RoomManager.StepDiffs isolates simulation panics per room.  This outer guard
// keeps a future panic in room routing or diff construction from escaping the
// instance tick goroutine and taking down other hosted worlds.
func safeStepDiffs(worldName string, rm *RoomManager, inputs map[PlayerID]PlayerInput) (diffs map[PlayerID]DiffMessage) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("zztgo: isolating world %q after tick panic: %v", worldName, recovered)
			diffs = make(map[PlayerID]DiffMessage)
		}
	}()
	return rm.StepDiffs(inputs)
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
	var raw json.RawMessage
	if err := wsjson.Read(ctx, conn, &raw); err != nil {
		return
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	if envelope.Type == MessageTypeEditorEnter {
		var enter EditorEnterMessage
		if err := json.Unmarshal(raw, &enter); err != nil {
			return
		}
		s.serveEditor(ctx, conn, enter)
		return
	}
	if envelope.Type != MessageTypeJoin {
		return
	}
	var join JoinMessage
	if err := json.Unmarshal(raw, &join); err != nil {
		return
	}

	client := &webSocketClient{conn: conn, worldName: safeWorld}

	// Resume first: a valid token reclaims the dropped run (same PlayerID/statID,
	// inventory intact). An unknown or expired token falls through to a fresh join.
	var playerID PlayerID
	var snapshot SnapshotMessage
	resumed := false
	if join.ResumeToken != "" {
		if pid, snap, ok := s.tryResume(inst, client, join.ResumeToken); ok {
			playerID, snapshot, resumed = pid, snap, true
		}
	}

	if !resumed {
		if join.Board == 0 {
			join.Board = inst.RoomManager.FrozenWorld().Info.CurrentBoard
			if join.Board == 0 {
				join.Board = s.DefaultBoard
			}
			if join.Board == 0 {
				join.Board = 1
			}
		}

		inst.mu.Lock()
		playerID = inst.RoomManager.JoinPlayer(join.Board, 0, 0)
		inst.RoomManager.SetPlayerName(playerID, join.Name)
		client.playerID = playerID
		inst.Clients[playerID] = client
		token := inst.mintResumeTokenLocked(playerID)
		var ok bool
		snapshot, ok = inst.RoomManager.Snapshot(playerID)
		if ok {
			snapshot.ResumeToken = token
			client.boardID = snapshot.BoardID
		}
		inst.mu.Unlock()

		if inst.RoomManager == s.RoomManager {
			s.mu.Lock()
			s.clients[playerID] = client
			s.mu.Unlock()
		}
		if !ok {
			s.removeClientFromInstance(inst, playerID)
			return
		}
	}

	if err := client.write(ctx, snapshot); err != nil {
		// The connection never got its first frame; detach (or tidy up if the
		// player is already gone) exactly as a mid-game drop would.
		s.handleReadLoopExit(inst, client, playerID)
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
	s.handleReadLoopExit(inst, client, playerID)
}

// serveEditor owns an editor-only WebSocket. Unlike a game connection it never
// joins RoomManager, so an editor cannot affect ticks, players, or live state.
func (s *WebSocketServer) serveEditor(ctx context.Context, conn *websocket.Conn, enter EditorEnterMessage) {
	worldName := enter.World
	if worldName == "" {
		worldName = s.DefaultInstance.Name
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		return
	}
	inst, err := s.GetOrCreateInstance(safeWorld)
	if err != nil {
		return
	}

	client := &webSocketClient{conn: conn, worldName: safeWorld}
	inst.mu.Lock()
	session := NewEditorSession(safeWorld, inst.SourceWorld)
	inst.mu.Unlock()
	if err := session.Enter(client); err != nil {
		return
	}
	s.mu.Lock()
	if s.EditorSessions == nil {
		s.EditorSessions = make(map[*webSocketClient]*EditorSession)
	}
	s.EditorSessions[client] = session
	s.mu.Unlock()
	defer func() {
		session.Exit(client)
		s.mu.Lock()
		delete(s.EditorSessions, client)
		s.mu.Unlock()
	}()

	// The cursor belongs to the browser. These are only the initial inspection
	// coordinates sent with its full frame, never session state.
	snapshot, err := session.Snapshot(client, BOARD_WIDTH/2, BOARD_HEIGHT/2)
	if err != nil || client.write(ctx, snapshot) != nil {
		return
	}

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}
		switch envelope.Type {
		case MessageTypeEditorExit:
			return
		case MessageTypeEditorInspect:
			var inspect EditorInspectMessage
			if json.Unmarshal(raw, &inspect) != nil {
				continue
			}
			reply, err := session.Inspect(client, inspect.X, inspect.Y)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorEdit:
			var edit EditorEditMessage
			if json.Unmarshal(raw, &edit) != nil {
				continue
			}
			reply, err := session.Edit(client, edit)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorProperty:
			var property EditorPropertyMessage
			if json.Unmarshal(raw, &property) != nil {
				continue
			}
			reply, err := session.SetProperty(client, property)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorStat:
			var stat EditorStatMessage
			if json.Unmarshal(raw, &stat) != nil {
				continue
			}
			reply, err := session.SetStat(client, stat)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorProgram:
			var req EditorProgramRequestMessage
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			reply, err := session.ProgramText(client, req.StatID)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorProgramSave:
			var save EditorProgramSaveMessage
			if json.Unmarshal(raw, &save) != nil {
				continue
			}
			reply, err := session.SaveProgram(client, save.StatID, save.Lines)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorBoard:
			var board EditorBoardMessage
			if json.Unmarshal(raw, &board) != nil {
				continue
			}
			if s.serveEditorBoard(ctx, client, session, board) != nil {
				return
			}
		case MessageTypeEditorWorld:
			var world EditorWorldMessage
			if json.Unmarshal(raw, &world) != nil {
				continue
			}
			if s.serveEditorWorld(ctx, client, session, world) != nil {
				return
			}
		}
	}
}

// serveEditorWorld routes an editorWorld operation (M5.6). save publishes the
// session world and hosts it; download replies with the world's .ZZT bytes;
// upload replaces the session world with client bytes after the M7.5 gate. A
// malformed base64 payload or unknown op is ignored, not fatal. It returns the
// write error only.
func (s *WebSocketServer) serveEditorWorld(ctx context.Context, client *webSocketClient, session *EditorSession, world EditorWorldMessage) error {
	switch world.Op {
	case "save":
		name, err := s.saveEditorWorld(client, session, world.Name)
		reply := EditorSaveResultMessage{Type: MessageTypeEditorSaveResult}
		if err != nil {
			reply.Error = err.Error()
		} else {
			reply.World = name
		}
		return client.write(ctx, reply)
	case "download":
		data, err := session.WorldBytes(client, "")
		if err != nil || data == nil {
			return nil
		}
		name, nameErr := SanitizeSaveName(session.WorldName)
		if nameErr != nil {
			name = "WORLD"
		}
		return client.write(ctx, EditorWorldDataMessage{
			Type: MessageTypeEditorWorldData,
			Name: name,
			Data: base64.StdEncoding.EncodeToString(data),
		})
	case "upload":
		data, decErr := base64.StdEncoding.DecodeString(world.Data)
		if decErr != nil {
			return nil
		}
		snapshot, gate, err := session.UploadWorld(client, data)
		if err != nil {
			return nil
		}
		if gate != "" {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: gate})
		}
		return client.write(ctx, snapshot)
	}
	return nil
}

// serveEditorBoard routes an editorBoard operation (M5.5). add/switch/import
// reply with a full editor snapshot; export replies with the board's .BRD bytes.
// A malformed base64 payload or unknown op is ignored, not fatal, so a bad
// message never drops the editor connection. It returns the write error only.
func (s *WebSocketServer) serveEditorBoard(ctx context.Context, client *webSocketClient, session *EditorSession, board EditorBoardMessage) error {
	switch board.Op {
	case "add":
		reply, err := session.AddBoard(client, board.Name)
		if err != nil {
			return nil
		}
		return client.write(ctx, reply)
	case "switch":
		reply, err := session.SwitchBoard(client, board.BoardID)
		if err != nil {
			return nil
		}
		return client.write(ctx, reply)
	case "export":
		reply, err := session.ExportBoard(client)
		if err != nil {
			return nil
		}
		return client.write(ctx, reply)
	case "import":
		data, decErr := base64.StdEncoding.DecodeString(board.Data)
		if decErr != nil {
			return nil
		}
		reply, err := session.ImportBoard(client, data)
		if err != nil {
			return nil
		}
		return client.write(ctx, reply)
	}
	return nil
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

	world, err := LoadPristineWorld(s.worldsDir(), worldName)
	if err != nil {
		return nil, err
	}
	dir := s.worldsDir()

	rm := NewRoomManager(world)
	rm.HighScorePath = filepath.Join(dir, worldName+".HI")
	rm.LoadHighScores()

	inst = &WorldInstance{
		Name:           worldName,
		SourceWorld:    cloneWorld(world),
		RoomManager:    rm,
		Clients:        make(map[PlayerID]*webSocketClient),
		Inputs:         make(map[PlayerID]PlayerInput),
		Title:          NewTitleSim(world),
		Detached:       make(map[PlayerID]int),
		ResumeTokens:   make(map[string]PlayerID),
		TokensByPlayer: make(map[PlayerID]string),
	}
	s.Instances[worldName] = inst
	return inst, nil
}

// HostGeneratedWorld installs an already-compiled, persisted world directly
// into the instance table. Generation uses this instead of reloading its file:
// the hosted bytes are exactly the ones that passed the compiler and M7.5 gate.
func (s *WebSocketServer) HostGeneratedWorld(name string, world TWorld) error {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Instances == nil {
		s.Instances = make(map[string]*WorldInstance)
	}
	if existing := s.Instances[safe]; existing != nil {
		existing.mu.Lock()
		occupied := len(existing.Clients) != 0
		existing.mu.Unlock()
		if occupied {
			return fmt.Errorf("generated world %q is already occupied", safe)
		}
	}
	rm := NewRoomManager(world)
	s.Instances[safe] = &WorldInstance{
		Name:           safe,
		SourceWorld:    cloneWorld(world),
		RoomManager:    rm,
		Clients:        make(map[PlayerID]*webSocketClient),
		Inputs:         make(map[PlayerID]PlayerInput),
		Title:          NewTitleSim(world),
		Detached:       make(map[PlayerID]int),
		ResumeTokens:   make(map[string]PlayerID),
		TokensByPlayer: make(map[PlayerID]string),
	}
	return nil
}

// worldsDir resolves where published worlds live. An explicit WorldsDir wins;
// otherwise it matches the picker's historical behavior — the loaded world's
// directory, then the working directory.
func (s *WebSocketServer) worldsDir() string {
	if s.WorldsDir != "" {
		return s.WorldsDir
	}
	if E != nil && E.LoadedGameFileName != "" {
		return filepath.Dir(E.LoadedGameFileName)
	}
	return "."
}

// saveEditorWorld publishes an editor session's world as dir/<NAME>.ZZT and hosts
// it so the world picker lists it and a second client can join and play it (M5.6).
// The name comes from a client, so SanitizeSaveName is the whole defense: path
// separators, '.', '..' and absolute paths all fail its charset. A world of the
// same name that anyone is currently playing is never overwritten — the same
// occupancy refusal RestoreSnapshot uses.
func (s *WebSocketServer) saveEditorWorld(client *webSocketClient, session *EditorSession, name string) (string, error) {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return "", err
	}

	// Refuse before writing anything if the target world is occupied.
	s.mu.Lock()
	existing := s.Instances[safe]
	occupied := false
	if existing != nil {
		existing.mu.Lock()
		occupied = len(existing.Clients) != 0
		existing.mu.Unlock()
	}
	s.mu.Unlock()
	if occupied {
		return "", fmt.Errorf("world %q is being played and cannot be overwritten", safe)
	}

	data, err := session.WorldBytes(client, safe)
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", fmt.Errorf("could not serialize the editor world")
	}

	dir := s.worldsDir()
	if dir == "" {
		return "", ErrSavesDisabled
	}
	path := filepath.Join(dir, safe+".ZZT")
	// Belt and braces: SanitizeSaveName cannot emit a separator, so this can only
	// fire if that charset is ever loosened.
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", ErrInvalidSaveName
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}

	world, err := LoadWorldBytes(data)
	if err != nil {
		return "", err
	}
	if err := s.HostGeneratedWorld(safe, world); err != nil {
		return "", err
	}
	return safe, nil
}

// LoadWorldBytes parses vanilla .ZZT bytes into a TWorld without touching disk.
func LoadWorldBytes(data []byte) (TWorld, error) {
	scratch := newSnapshotEngine()
	if err := scratch.worldReadFrom(bytes.NewReader(data), false, nil); err != nil {
		return TWorld{}, err
	}
	return scratch.World, nil
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

// mintResumeTokenLocked returns a resume token for playerID, reusing the
// existing one if the player already has it (an idempotent re-join keeps the
// same token). Caller holds inst.mu.
func (inst *WorldInstance) mintResumeTokenLocked(playerID PlayerID) string {
	if inst.ResumeTokens == nil {
		inst.ResumeTokens = make(map[string]PlayerID)
	}
	if inst.TokensByPlayer == nil {
		inst.TokensByPlayer = make(map[PlayerID]string)
	}
	if token, ok := inst.TokensByPlayer[playerID]; ok {
		return token
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand should never fail on a supported platform. If it somehow
		// does, a zero token is still a valid (if predictable) session key —
		// resume is best-effort and never a security boundary.
		log.Printf("zztgo: resume token entropy unavailable: %v", err)
	}
	token := hex.EncodeToString(buf[:])
	inst.ResumeTokens[token] = playerID
	inst.TokensByPlayer[playerID] = token
	return token
}

// deleteResumeTokenLocked drops a player's resume token from both indexes.
// Caller holds inst.mu.
func (inst *WorldInstance) deleteResumeTokenLocked(playerID PlayerID) {
	if token, ok := inst.TokensByPlayer[playerID]; ok {
		delete(inst.ResumeTokens, token)
		delete(inst.TokensByPlayer, playerID)
	}
}

// tryResume reattaches client to the player named by token, if any. On success
// it returns the reclaimed PlayerID and a fresh full snapshot (with the same
// token echoed back). An unknown or stale token returns ok=false, and the caller
// falls through to a normal fresh join.
//
// Newest-wins: if the token's player still has a live client, that socket is
// displaced — the new client takes its place and the old connection is closed.
func (s *WebSocketServer) tryResume(inst *WorldInstance, client *webSocketClient, token string) (PlayerID, SnapshotMessage, bool) {
	inst.mu.Lock()
	playerID, ok := inst.ResumeTokens[token]
	if !ok {
		inst.mu.Unlock()
		return 0, SnapshotMessage{}, false
	}
	// The token outlived its player (quit/expired before the client came back):
	// clean it up and let the caller join fresh.
	if inst.RoomManager.players[playerID] == nil {
		inst.deleteResumeTokenLocked(playerID)
		delete(inst.Detached, playerID)
		inst.mu.Unlock()
		return 0, SnapshotMessage{}, false
	}

	old := inst.Clients[playerID]
	delete(inst.Detached, playerID)
	client.playerID = playerID
	inst.Clients[playerID] = client
	// A resuming player's queued per-player events are stale; the fresh snapshot
	// below carries the current world state instead.
	inst.RoomManager.DrainPlayerEvents(playerID)
	snapshot, snapOK := inst.RoomManager.Snapshot(playerID)
	if snapOK {
		snapshot.ResumeToken = token
		client.boardID = snapshot.BoardID
	}
	inst.mu.Unlock()

	if old != nil && old != client {
		old.conn.Close(websocket.StatusNormalClosure, "resumed on a new connection")
	}
	if inst.RoomManager == s.RoomManager {
		s.mu.Lock()
		s.clients[playerID] = client
		s.mu.Unlock()
	}
	if !snapOK {
		// The player existed a moment ago, so this should not happen; treat it as
		// a failed resume and let the caller join fresh.
		return 0, SnapshotMessage{}, false
	}
	return playerID, snapshot, true
}

// handleReadLoopExit runs when a game connection's read loop ends. If the player
// still exists it is detached (its stat lingers for ReconnectGraceTicks so a
// reconnect can reclaim it) rather than removed. A connection that has already
// been superseded by a newer one (newest-wins) owns nothing and just returns.
func (s *WebSocketServer) handleReadLoopExit(inst *WorldInstance, client *webSocketClient, playerID PlayerID) {
	inst.mu.Lock()
	if inst.Clients[playerID] != client {
		// A newer connection took over this player; this stale socket must not
		// detach it or it would cancel the live attachment.
		inst.mu.Unlock()
		return
	}
	delete(inst.Clients, playerID)
	delete(inst.Inputs, playerID)
	if inst.RoomManager.players[playerID] == nil {
		// The player already left (confirmed quit or expiry); no grace to grant,
		// just finish tidying up.
		inst.deleteResumeTokenLocked(playerID)
		delete(inst.Detached, playerID)
		inst.mu.Unlock()
		s.clearDefaultClient(inst, playerID)
		return
	}
	// Detach: drop queued per-player events (decision 2) and start the countdown.
	inst.RoomManager.DrainPlayerEvents(playerID)
	if inst.Detached == nil {
		inst.Detached = make(map[PlayerID]int)
	}
	inst.Detached[playerID] = ReconnectGraceTicks
	inst.mu.Unlock()
	s.clearDefaultClient(inst, playerID)
}

// clearDefaultClient removes a player from the legacy default-instance maps.
// It is a no-op for any other instance.
func (s *WebSocketServer) clearDefaultClient(inst *WorldInstance, playerID PlayerID) {
	if inst.RoomManager != s.RoomManager {
		return
	}
	s.mu.Lock()
	delete(s.clients, playerID)
	delete(s.inputs, playerID)
	s.mu.Unlock()
}

// expireDetached advances every instance's reconnect-grace countdown by one tick
// and removes any player whose grace has run out. It runs once per instance per
// tick from WebSocketServer.Tick, so the room-state mutation of a departing
// player happens on the tick goroutine (the shape M13.4 wants).
func (s *WebSocketServer) expireDetached() {
	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.expireDetached()
	}
}

func (inst *WorldInstance) expireDetached() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	for playerID, ticks := range inst.Detached {
		if ticks <= 1 {
			inst.removeDetachedLocked(playerID)
			continue
		}
		inst.Detached[playerID] = ticks - 1
	}
}

// removeDetachedLocked performs the room-side removal of an expired detached
// player. Caller holds inst.mu. The default instance's s.clients/s.inputs
// entries were already cleared at detach, so no s.mu work is needed here.
func (inst *WorldInstance) removeDetachedLocked(playerID PlayerID) {
	delete(inst.Detached, playerID)
	inst.deleteResumeTokenLocked(playerID)
	inst.RoomManager.LeavePlayer(playerID)
	inst.RoomManager.DiscardPendingScore(playerID)
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
	inst.deleteResumeTokenLocked(playerID)
	delete(inst.Detached, playerID)
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
