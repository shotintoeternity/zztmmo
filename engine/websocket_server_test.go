package zztgo

import (
	"context"
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
