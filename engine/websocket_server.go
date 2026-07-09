package zztgo

import (
	"context"
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

	mu      sync.Mutex
	clients map[PlayerID]*webSocketClient
	inputs  map[PlayerID]PlayerInput
}

type webSocketClient struct {
	playerID PlayerID
	conn     *websocket.Conn
	mu       sync.Mutex
}

func NewWebSocketServer(world TWorld, defaultBoard int16) *WebSocketServer {
	return &WebSocketServer{
		RoomManager:  NewRoomManager(world),
		DefaultBoard: defaultBoard,
		TickDuration: ServerTickDuration,
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
	for playerID, client := range s.clients {
		clients[playerID] = client
	}
	s.mu.Unlock()

	for playerID, diff := range diffs {
		client := clients[playerID]
		if client != nil {
			_ = client.write(ctx, diff)
		}
	}
}

func (s *WebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
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
		var input InputMessage
		if err := wsjson.Read(ctx, conn, &input); err != nil {
			s.removeClient(playerID)
			return
		}
		s.setInput(playerID, inputMessageToPlayerInput(input))
	}
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
}

func (c *webSocketClient) write(ctx context.Context, message interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, c.conn, message)
}
