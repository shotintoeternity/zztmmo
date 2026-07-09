package zztgo

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

func TestWebSocketServerMultiplayerSmokePickupTransferHUD(t *testing.T) {
	world := testMultiplayerSmokeWorld(t)
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

	if snap1.You.StatID != 0 {
		t.Fatalf("p1 stat=%d, want claimed original stat 0", snap1.You.StatID)
	}
	if snap1.You.X != 10 || snap1.You.Y != 12 {
		t.Fatalf("p1 spawned at (%d,%d), want original spawn (10,12)", snap1.You.X, snap1.You.Y)
	}
	if snap2.You.X != 10 || snap2.You.Y != 13 {
		t.Fatalf("p2 spawned at (%d,%d), want adjacent slot (10,13)", snap2.You.X, snap2.You.Y)
	}

	waitForPlayers(t, ctx, conn1, 2)
	waitForPlayers(t, ctx, conn2, 2)

	if err := wsjson.Write(ctx, conn2, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: snap2.You.ID,
		Seq:      1,
		Keymask:  InputMaskDown,
	}); err != nil {
		t.Fatalf("p2 move input: %v", err)
	}
	waitForDiff(t, ctx, conn1, func(diff DiffMessage) bool {
		return playerAt(diff, snap1.You.ID, 10, 12) && playerAt(diff, snap2.You.ID, 10, 14)
	})

	if err := wsjson.Write(ctx, conn1, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: snap1.You.ID,
		Seq:      1,
		Keymask:  InputMaskRight,
	}); err != nil {
		t.Fatalf("p1 gem input: %v", err)
	}
	waitForDiff(t, ctx, conn1, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Gems == 1 && diff.HUD.Score == 10 && playerAt(diff, snap1.You.ID, 11, 12)
	})
	waitForDiff(t, ctx, conn2, func(diff DiffMessage) bool {
		return diff.HUD != nil && diff.HUD.Gems == 0 && diff.HUD.Score == 0 && playerAt(diff, snap1.You.ID, 11, 12)
	})

	if err := wsjson.Write(ctx, conn1, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: snap1.You.ID,
		Seq:      2,
		Keymask:  InputMaskRight,
	}); err != nil {
		t.Fatalf("p1 passage input: %v", err)
	}
	boardChange := waitForBoardChange(t, ctx, conn1, 2)
	if boardChange.Snapshot.You.X != 5 || boardChange.Snapshot.You.Y != 5 {
		t.Fatalf("p1 transferred to (%d,%d), want passage entry (5,5)", boardChange.Snapshot.You.X, boardChange.Snapshot.You.Y)
	}
	if boardChange.Snapshot.HUD.Gems != 1 || boardChange.Snapshot.HUD.Score != 10 {
		t.Fatalf("p1 HUD after transfer gems=%d score=%d, want 1/10", boardChange.Snapshot.HUD.Gems, boardChange.Snapshot.HUD.Score)
	}

	waitForDiff(t, ctx, conn2, func(diff DiffMessage) bool {
		return diff.BoardID == 1 && len(diff.Players) == 1 && diff.Players[0].ID == snap2.You.ID
	})
}

func TestWebSocketServerTwentyBotSoak(t *testing.T) {
	botCount, soakTicks, maxAllocGrowth := soakTestConfig(t)

	world := testSoakWorld(t)
	server := NewWebSocketServer(world, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	bots := make([]*soakBot, 0, botCount)
	var wg sync.WaitGroup
	for i := 0; i < botCount; i++ {
		conn, snapshot := joinTestClient(t, ctx, httpServer.URL, "bot")
		bot := &soakBot{conn: conn, id: snapshot.You.ID}
		bot.board.Store(int64(snapshot.BoardID))
		bot.tick.Store(int64(snapshot.Tick))
		bot.hash.Store(snapshot.Hash)
		bots = append(bots, bot)
		wg.Add(1)
		go bot.readLoop(ctx, &wg)
	}
	defer func() {
		cancel()
		for _, bot := range bots {
			_ = bot.conn.Close(websocket.StatusNormalClosure, "")
		}
		wg.Wait()
	}()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for tick := 0; tick < soakTicks; tick++ {
		for i, bot := range bots {
			if err := bot.writeInput(ctx, soakInputMask(i, tick), uint64(tick+1)); err != nil {
				t.Fatalf("bot %d write input at tick %d: %v", i, tick, err)
			}
		}
		server.Tick(ctx)
	}

	deadline := time.After(5 * time.Second)
	for {
		allCaughtUp := true
		for _, bot := range bots {
			if bot.messageCount() < int64(soakTicks) {
				allCaughtUp = false
				break
			}
		}
		if allCaughtUp {
			break
		}
		select {
		case <-deadline:
			for i, bot := range bots {
				t.Logf("bot %d messages=%d diffs=%d boardChanges=%d err=%q", i, bot.messageCount(), bot.diffs.Load(), bot.boardChanges.Load(), bot.errText())
			}
			t.Fatal("timed out waiting for bot readers to catch up")
		case <-time.After(time.Millisecond):
		}
	}

	for i, bot := range bots {
		if errText := bot.errText(); errText != "" {
			t.Fatalf("bot %d read error: %s", i, errText)
		}
		if bot.diffs.Load() == 0 {
			t.Fatalf("bot %d read no diffs", i)
		}
	}

	server.mu.Lock()
	if got := len(server.clients); got != botCount {
		server.mu.Unlock()
		t.Fatalf("active clients=%d, want %d", got, botCount)
	}
	roomHashes := make(map[int16]uint64)
	for _, boardID := range server.RoomManager.roomIDs() {
		room, ok := server.RoomManager.Room(boardID)
		if ok {
			roomHashes[boardID] = StateHash(room.Engine)
		}
	}
	server.mu.Unlock()

	for i, bot := range bots {
		boardID := int16(bot.board.Load())
		wantHash, ok := roomHashes[boardID]
		if !ok {
			t.Fatalf("bot %d is on inactive board %d", i, boardID)
		}
		if gotHash := bot.hash.Load(); gotHash != wantHash {
			t.Fatalf("bot %d hash drift on board %d: got %d want %d", i, boardID, gotHash, wantHash)
		}
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.Alloc > before.Alloc+maxAllocGrowth {
		t.Fatalf("heap allocation grew by %d bytes, limit %d", after.Alloc-before.Alloc, maxAllocGrowth)
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

func waitForBoardChange(t *testing.T, ctx context.Context, conn *websocket.Conn, boardID int16) BoardChangeMessage {
	t.Helper()

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
		if boardChange.Snapshot.BoardID == boardID {
			return boardChange
		}
	}
}

func playerAt(diff DiffMessage, playerID PlayerID, x, y int16) bool {
	for _, player := range diff.Players {
		if player.ID == playerID && player.X == x && player.Y == y {
			return true
		}
	}
	return false
}

type soakBot struct {
	conn         *websocket.Conn
	id           PlayerID
	messages     atomic.Int64
	diffs        atomic.Int64
	boardChanges atomic.Int64
	board        atomic.Int64
	tick         atomic.Int64
	hash         atomic.Uint64
	err          atomic.Value
}

func (bot *soakBot) readLoop(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, bot.conn, &raw); err != nil {
			if ctx.Err() == nil && websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				bot.err.Store(err.Error())
			}
			return
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			bot.err.Store(err.Error())
			return
		}

		switch envelope.Type {
		case MessageTypeDiff:
			var diff DiffMessage
			if err := json.Unmarshal(raw, &diff); err != nil {
				bot.err.Store(err.Error())
				return
			}
			bot.messages.Add(1)
			bot.diffs.Add(1)
			bot.board.Store(int64(diff.BoardID))
			bot.tick.Store(int64(diff.Tick))
			bot.hash.Store(diff.Hash)
		case MessageTypeBoardChange:
			var boardChange BoardChangeMessage
			if err := json.Unmarshal(raw, &boardChange); err != nil {
				bot.err.Store(err.Error())
				return
			}
			bot.messages.Add(1)
			bot.boardChanges.Add(1)
			bot.board.Store(int64(boardChange.Snapshot.BoardID))
			bot.tick.Store(int64(boardChange.Snapshot.Tick))
			bot.hash.Store(boardChange.Snapshot.Hash)
		default:
			bot.err.Store("unexpected message type: " + envelope.Type)
			return
		}
	}
}

func (bot *soakBot) writeInput(ctx context.Context, keymask uint16, seq uint64) error {
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, bot.conn, InputMessage{
		Type:     MessageTypeInput,
		PlayerID: bot.id,
		Seq:      seq,
		Keymask:  keymask,
	})
}

func (bot *soakBot) messageCount() int64 {
	return bot.messages.Load()
}

func (bot *soakBot) errText() string {
	err, _ := bot.err.Load().(string)
	return err
}

func soakInputMask(botIndex, tick int) uint16 {
	switch (tick/8 + botIndex) % 6 {
	case 0:
		return InputMaskRight
	case 1:
		return InputMaskDown
	case 2:
		return InputMaskLeft
	case 3:
		return InputMaskUp
	case 4:
		return InputMaskShoot | InputMaskRight
	default:
		return 0
	}
}

func soakTestConfig(t *testing.T) (botCount, soakTicks int, maxAllocGrowth uint64) {
	t.Helper()

	botCount = envPositiveInt(t, "ZZT_SOAK_BOTS", 20)
	soakTicks = envPositiveInt(t, "ZZT_SOAK_TICKS", 240)
	maxAllocGrowthMB := envPositiveInt(t, "ZZT_SOAK_MAX_ALLOC_MB", 32)
	return botCount, soakTicks, uint64(maxAllocGrowthMB) << 20
}

func envPositiveInt(t *testing.T, name string, fallback int) int {
	t.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		t.Fatalf("%s=%q, want positive integer", name, value)
	}
	return parsed
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

func testMultiplayerSmokeWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.BoardCount = 2

	const passageColor = 0x0E

	setup.World.Info.CurrentBoard = 1
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_NORMAL, Color: 0x07}
		}
	}
	setup.Board.Tiles[10][12] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	setup.Board.Stats[0].X = 10
	setup.Board.Stats[0].Y = 12
	setup.Board.Stats[0].Under = TTile{Element: E_EMPTY}
	setup.Board.Tiles[10][13] = TTile{Element: E_EMPTY}
	setup.Board.Tiles[10][14] = TTile{Element: E_EMPTY}
	setup.Board.Tiles[11][12] = TTile{Element: E_GEM, Color: ElementDefs[E_GEM].Color}
	setup.Board.Tiles[12][12] = TTile{Element: E_PASSAGE, Color: passageColor}
	setup.AddStat(12, 12, E_PASSAGE, passageColor, 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = 2
	setup.Board.Info.StartPlayerX = 10
	setup.Board.Info.StartPlayerY = 12
	setup.Board.Info.MaxShots = 255
	setup.Board.Name = "Smoke A"
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
	setup.Board.Tiles[5][5] = TTile{Element: E_PASSAGE, Color: passageColor}
	setup.AddStat(5, 5, E_PASSAGE, passageColor, 0, StatTemplateDefault)
	setup.Board.Stats[setup.Board.StatCount].P3 = 1
	setup.Board.Info.StartPlayerX = 5
	setup.Board.Info.StartPlayerY = 5
	setup.Board.Name = "Smoke B"
	setup.BoardClose()

	return setup.World
}

func testSoakWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.BoardCount = 2

	setup.World.Info.CurrentBoard = 1
	setup.BoardCreate()
	fillBoard(setup, TTile{Element: E_EMPTY})
	setup.Board.Tiles[30][13] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	setup.Board.Stats[0].X = 30
	setup.Board.Stats[0].Y = 13
	setup.Board.Stats[0].Under = TTile{Element: E_EMPTY}
	setup.Board.Info.StartPlayerX = 30
	setup.Board.Info.StartPlayerY = 13
	setup.Board.Info.NeighborBoards[3] = 2
	setup.Board.Info.MaxShots = 255
	setup.Board.Name = "Soak A"
	setup.BoardClose()

	setup.World.Info.CurrentBoard = 2
	setup.BoardCreate()
	fillBoard(setup, TTile{Element: E_EMPTY})
	setup.Board.Tiles[30][13] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	setup.Board.Stats[0].X = 30
	setup.Board.Stats[0].Y = 13
	setup.Board.Stats[0].Under = TTile{Element: E_EMPTY}
	setup.Board.Info.StartPlayerX = 30
	setup.Board.Info.StartPlayerY = 13
	setup.Board.Info.NeighborBoards[2] = 1
	setup.Board.Info.MaxShots = 255
	setup.Board.Name = "Soak B"
	setup.BoardClose()

	return setup.World
}

func fillBoard(engine *Engine, tile TTile) {
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			engine.Board.Tiles[ix][iy] = tile
		}
	}
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
