package zztgo

import (
	"context"
	"encoding/json"
	"net/http"
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

	mu      sync.Mutex
	clients map[PlayerID]*webSocketClient
	inputs  map[PlayerID]PlayerInput
}

type webSocketClient struct {
	playerID PlayerID
	boardID  int16
	conn     *websocket.Conn
	mu       sync.Mutex
}

func NewWebSocketServer(world TWorld, defaultBoard int16) *WebSocketServer {
	return &WebSocketServer{
		RoomManager:  NewRoomManager(world),
		DefaultBoard: defaultBoard,
		TickDuration: ServerTickDuration,
		OriginHosts:  []string{"localhost:*", "127.0.0.1:*"},
		clients:      make(map[PlayerID]*webSocketClient),
		inputs:       make(map[PlayerID]PlayerInput),
	}
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
				messages[playerID] = BoardChangeMessage{Type: MessageTypeBoardChange, Snapshot: snapshot}
				continue
			}
		}
		client.boardID = diff.BoardID
		messages[playerID] = diff
	}
	// A quitter has already left their room, so they are absent from diffs.
	// Their outcome is delivered here, and it replaces any diff they had.
	for _, quit := range s.RoomManager.DrainQuits() {
		if s.clients[quit.PlayerID] == nil {
			continue
		}
		messages[quit.PlayerID] = s.quitOutcome(quit)
	}
	s.mu.Unlock()

	for playerID, message := range messages {
		client := clients[playerID]
		if client != nil {
			_ = client.write(ctx, message)
		}
	}
}

func (s *WebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		join.Board = s.DefaultBoard
	}

	client := &webSocketClient{conn: conn}
	s.mu.Lock()
	playerID := s.RoomManager.JoinPlayer(join.Board, 0, 0)
	client.playerID = playerID
	s.clients[playerID] = client
	snapshot, ok := s.RoomManager.Snapshot(playerID)
	if ok {
		client.boardID = snapshot.BoardID
		err = client.write(ctx, snapshot)
	}
	s.mu.Unlock()
	if !ok {
		s.removeClient(playerID)
		return
	}
	if err != nil {
		s.removeClient(playerID)
		return
	}

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			s.removeClient(playerID)
			return
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
			s.submitDebugCommand(playerID, cmd.Text)
		case MessageTypeScrollReply:
			var reply ScrollReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitScrollReply(playerID, reply.StatID, reply.Label)
		case MessageTypeQuitReply:
			var reply QuitReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitQuitReply(playerID, reply.Quit)
		case MessageTypeHighScoreName:
			var entry HighScoreNameMessage
			if err := json.Unmarshal(raw, &entry); err != nil {
				continue
			}
			s.submitHighScoreName(ctx, playerID, entry.Name)
		default:
			var input InputMessage
			if err := json.Unmarshal(raw, &input); err != nil {
				continue
			}
			s.setInput(playerID, inputMessageToPlayerInput(input))
		}
	}
}

// quitOutcome is what a player sees after confirming the quit prompt: either
// vanilla's "New high score" window, or a bare quit so the client can return to
// the title screen. Callers hold s.mu.
func (s *WebSocketServer) quitOutcome(quit QuitResult) EventMessage {
	event := ProtocolEvent{Type: "quit", Score: quit.Score}
	if quit.ListPos > 0 {
		event = ProtocolEvent{
			Type:    "highScoreEntry",
			Score:   quit.Score,
			ListPos: quit.ListPos,
			Title:   "New high score for " + s.RoomManager.WorldName(),
			Lines:   s.RoomManager.HighScoreLines(quit.ListPos),
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

// submitDebugCommand routes a client's '?' reply to the engine that owns that
// player. Held under s.mu because Tick mutates the same rooms.
func (s *WebSocketServer) submitDebugCommand(playerID PlayerID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[playerID]; !ok {
		return
	}
	s.RoomManager.SubmitDebugCommand(playerID, text)
}

// submitScrollReply routes a scroll selection to the player's room. Held under
// s.mu because Tick mutates the same rooms.
func (s *WebSocketServer) submitScrollReply(playerID PlayerID, objectStatID int16, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[playerID]; !ok {
		return
	}
	s.RoomManager.SubmitScrollReply(playerID, objectStatID, label)
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

func (s *WebSocketServer) removeClient(playerID PlayerID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.clients, playerID)
	delete(s.inputs, playerID)
	s.RoomManager.LeavePlayer(playerID)
	// They may have quit and closed the tab before typing a name.
	s.RoomManager.DiscardPendingScore(playerID)
}

func (c *webSocketClient) write(ctx context.Context, message interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, c.conn, message)
}
