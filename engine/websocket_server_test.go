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

func TestWebSocketServerTwoClientsSeeAndFight(t *testing.T) {
	world := testFightWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Run(ctx)

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn1, snap1 := joinTestClient(t, ctx, httpServer.URL, "p1")
	defer conn1.Close(websocket.StatusNormalClosure, "")
	conn2, snap2 := joinTestClient(t, ctx, httpServer.URL, "p2")
	defer conn2.Close(websocket.StatusNormalClosure, "")

	if snap1.You.X != 10 || snap1.You.Y != 12 {
		t.Fatalf("p1 spawned at (%d,%d), want (10,12)", snap1.You.X, snap1.You.Y)
	}
	if snap2.You.X != 11 || snap2.You.Y != 12 {
		t.Fatalf("p2 spawned at (%d,%d), want (11,12)", snap2.You.X, snap2.You.Y)
	}

	waitForPlayers(t, ctx, conn1, 2)
	waitForPlayers(t, ctx, conn2, 2)

	p1State, ok := server.RoomManager.PlayerState(snap1.You.ID)
	if !ok {
		t.Fatal("missing p1 state")
	}
	p1State.Ammo = 5

	if err := wsjson.Write(ctx, conn1, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: snap1.You.ID,
		Seq:      1,
		Keymask:  InputMaskShoot | InputMaskRight,
	}); err != nil {
		t.Fatalf("p1 shoot input: %v", err)
	}

	waitForPlayerHealth(t, ctx, conn1, snap2.You.ID, 90)
	waitForPlayerHealth(t, ctx, conn2, snap2.You.ID, 90)
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

func joinTestClient(t *testing.T, ctx context.Context, serverURL, name string) (*websocket.Conn, SnapshotMessage) {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: name, Board: 1}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "")
		t.Fatalf("write join: %v", err)
	}

	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "")
		t.Fatalf("read snapshot: %v", err)
	}
	return conn, snapshot
}

func waitForPlayers(t *testing.T, ctx context.Context, conn *websocket.Conn, count int) DiffMessage {
	t.Helper()

	return waitForDiff(t, ctx, conn, func(diff DiffMessage) bool {
		return len(diff.Players) == count
	})
}

func waitForPlayerHealth(t *testing.T, ctx context.Context, conn *websocket.Conn, playerID PlayerID, health int16) DiffMessage {
	t.Helper()

	return waitForDiff(t, ctx, conn, func(diff DiffMessage) bool {
		for _, player := range diff.Players {
			if player.ID == playerID && player.Health == health {
				return true
			}
		}
		return false
	})
}

func waitForDiff(t *testing.T, ctx context.Context, conn *websocket.Conn, match func(DiffMessage) bool) DiffMessage {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for matching diff")
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
		if envelope.Type != MessageTypeDiff {
			continue
		}

		var diff DiffMessage
		if err := json.Unmarshal(raw, &diff); err != nil {
			t.Fatalf("decode diff: %v", err)
		}
		if match(diff) {
			return diff
		}
	}
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

func testFightWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.Info.CurrentBoard = 1
	setup.World.BoardCount = 1
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_NORMAL, Color: 0x07}
		}
	}
	setup.Board.Tiles[10][12] = TTile{Element: E_EMPTY}
	setup.Board.Tiles[11][12] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Info.StartPlayerX = 10
	setup.Board.Info.StartPlayerY = 12
	setup.Board.Info.MaxShots = 255
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
