package zztgo

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestWebSocketServerJoinInputDiff(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Run(ctx)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "tester", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}

	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.Type != MessageTypeSnapshot {
		t.Fatalf("snapshot.Type=%q, want %q", snapshot.Type, MessageTypeSnapshot)
	}
	if snapshot.You.ID == 0 {
		t.Fatal("snapshot missing player id")
	}
	startX := snapshot.You.X

	if err := wsjson.Write(ctx, conn, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: snapshot.You.ID,
		Seq:      1,
		Keymask:  InputMaskRight,
	}); err != nil {
		t.Fatalf("write input: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for movement diff")
		default:
		}

		var diff DiffMessage
		if err := wsjson.Read(ctx, conn, &diff); err != nil {
			t.Fatalf("read diff: %v", err)
		}
		if diff.Type != MessageTypeDiff {
			t.Fatalf("diff.Type=%q, want %q", diff.Type, MessageTypeDiff)
		}
		if diff.Hash == 0 {
			t.Fatal("diff hash is zero")
		}
		for _, player := range diff.Players {
			if player.ID == snapshot.You.ID && player.X == startX+1 {
				return
			}
		}
	}
}

func TestWebSocketServerBoardEdgeSendsBoardChange(t *testing.T) {
	world := testEdgeWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Run(ctx)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "edge", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}

	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.BoardID != 1 || snapshot.You.X != BOARD_WIDTH {
		t.Fatalf("joined at board=%d x=%d, want board=1 x=%d", snapshot.BoardID, snapshot.You.X, BOARD_WIDTH)
	}

	if err := wsjson.Write(ctx, conn, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: snapshot.You.ID,
		Seq:      1,
		Keymask:  InputMaskRight,
	}); err != nil {
		t.Fatalf("write input: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for board change")
		default:
		}

		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read message: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		if envelope.Type != MessageTypeBoardChange {
			continue
		}

		var boardChange BoardChangeMessage
		if err := json.Unmarshal(raw, &boardChange); err != nil {
			t.Fatalf("decode board change: %v", err)
		}
		if boardChange.Snapshot.BoardID != 2 {
			t.Fatalf("boardChange snapshot board=%d, want 2", boardChange.Snapshot.BoardID)
		}
		if boardChange.Snapshot.You.X != 1 {
			t.Fatalf("boardChange player x=%d, want 1", boardChange.Snapshot.You.X)
		}
		return
	}
}

func TestInputMessageToPlayerInput(t *testing.T) {
	tests := []struct {
		name  string
		input InputMessage
		want  PlayerInput
	}{
		{
			name:  "expanded fields",
			input: InputMessage{DeltaX: 1, Shift: true, Key: KEY_RIGHT},
			want:  PlayerInput{DeltaX: 1, Shift: true, Key: KEY_RIGHT},
		},
		{
			name:  "right keymask",
			input: InputMessage{Keymask: InputMaskRight},
			want:  PlayerInput{DeltaX: 1, Key: KEY_RIGHT},
		},
		{
			name:  "shoot direction keymask",
			input: InputMessage{Keymask: InputMaskShoot | InputMaskUp},
			want:  PlayerInput{DeltaY: -1, Shift: true, Key: KEY_UP},
		},
		{
			name:  "shoot keymask",
			input: InputMessage{Keymask: InputMaskShoot},
			want:  PlayerInput{Shift: true, Key: ' '},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inputMessageToPlayerInput(tt.input)
			if got != tt.want {
				t.Fatalf("inputMessageToPlayerInput()=%+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPlayerInputPastBoardSentinelDoesNotPanic(t *testing.T) {
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.Info.CurrentBoard = 1
	setup.World.BoardCount = 1
	setup.BoardCreate()
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.Stats[0].X = BOARD_WIDTH + 1
	setup.Board.Stats[0].Y = 12
	setup.Board.Tiles[BOARD_WIDTH+1][12] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}

	setup.GameStepWithInputs(map[int16]PlayerInput{
		0: {DeltaX: 1, Key: KEY_RIGHT},
	})
}

func testEmptyWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.Info.CurrentBoard = 1
	setup.World.BoardCount = 1
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Info.StartPlayerX = 10
	setup.Board.Info.StartPlayerY = 12
	setup.BoardClose()
	return setup.World
}

func testEdgeWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.BoardCount = 2

	setup.World.Info.CurrentBoard = 1
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Info.StartPlayerX = BOARD_WIDTH
	setup.Board.Info.StartPlayerY = 12
	setup.Board.Info.NeighborBoards[3] = 2
	setup.Board.Name = "West"
	setup.BoardClose()

	setup.World.Info.CurrentBoard = 2
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Info.StartPlayerX = 1
	setup.Board.Info.StartPlayerY = 12
	setup.Board.Info.NeighborBoards[2] = 1
	setup.Board.Name = "East"
	setup.BoardClose()

	return setup.World
}
