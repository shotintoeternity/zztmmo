package zztgo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runServerAsync(t, ctx, server)

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

func TestWebSocketEditorSessionReadOnly(t *testing.T) {
	world := testMultiplayerSmokeWorld(t)
	world.Info.CurrentBoard = 1
	server := NewWebSocketServer(world, 1)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial editor: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, EditorEnterMessage{Type: MessageTypeEditorEnter, World: "TOWN"}); err != nil {
		t.Fatalf("write editorEnter: %v", err)
	}
	var snapshot EditorSnapshotMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &snapshot)
	if snapshot.Type != MessageTypeEditorSnapshot || snapshot.BoardID != 1 || len(snapshot.Screen) != BOARD_WIDTH*BOARD_HEIGHT {
		t.Fatalf("bad editor snapshot: type=%q board=%d cells=%d", snapshot.Type, snapshot.BoardID, len(snapshot.Screen))
	}
	// Opening an editor session must not create a room player or a live room.
	if len(server.RoomManager.players) != 0 || len(server.RoomManager.rooms) != 0 {
		t.Fatalf("editor joined live simulation: players=%d rooms=%d", len(server.RoomManager.players), len(server.RoomManager.rooms))
	}

	if err := wsjson.Write(ctx, conn, EditorInspectMessage{Type: MessageTypeEditorInspect, X: 12, Y: 12}); err != nil {
		t.Fatalf("write editorInspect: %v", err)
	}
	var inspect EditorInspectMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorInspect, &inspect)
	if inspect.Inspect.Element != "Passage" || !inspect.Inspect.HasStat || inspect.Inspect.P3 != 2 {
		t.Fatalf("editor inspect=%+v, want passage P3=2", inspect.Inspect)
	}
	if err := wsjson.Write(ctx, conn, EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 11, Y: 11, Element: E_SOLID, Color: 0x0e}); err != nil {
		t.Fatalf("write editor edit: %v", err)
	}
	var diff EditorDiffMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorDiff, &diff)
	if diff.Type != MessageTypeEditorDiff || len(diff.Cells) == 0 || diff.Inspect.ElementID != E_SOLID {
		t.Fatalf("editor diff=%+v, want dirty solid placement", diff)
	}
	if err := wsjson.Write(ctx, conn, EditorPropertyMessage{Type: MessageTypeEditorProperty, Field: "timeLimit", Value: 42}); err != nil {
		t.Fatalf("write editor property: %v", err)
	}
	var properties EditorPropertiesMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorProperties, &properties)
	if properties.Type != MessageTypeEditorProperties || properties.Properties.TimeLimitSec != 42 || len(properties.Screen) != BOARD_WIDTH*BOARD_HEIGHT {
		t.Fatalf("editor properties=%+v, want saved time limit and complete repaint", properties)
	}
	if err := wsjson.Write(ctx, conn, struct {
		Type string `json:"type"`
	}{Type: MessageTypeEditorExit}); err != nil {
		t.Fatalf("write editorExit: %v", err)
	}
}

func TestM101EditorSessionBroadcastsDiffsAndPresence(t *testing.T) {
	world := testEmptyWorld(t)
	world.Info.CurrentBoard = 1
	server := NewWebSocketServer(world, 1)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	connA := dialEditorClient(t, ctx, wsURL)
	defer connA.Close(websocket.StatusNormalClosure, "")
	connB := dialEditorClient(t, ctx, wsURL)
	defer connB.Close(websocket.StatusNormalClosure, "")

	presence := readEditorPresenceWithMembers(t, ctx, connA, 2)

	if err := wsjson.Write(ctx, connA, EditorInspectMessage{Type: MessageTypeEditorInspect, X: 8, Y: 9}); err != nil {
		t.Fatalf("write editorInspect: %v", err)
	}
	var inspect EditorInspectMessage
	readEditorMessage(t, ctx, connA, MessageTypeEditorInspect, &inspect)
	presence = readEditorPresenceAt(t, ctx, connB, 8, 9)
	var sawMoved bool
	for _, member := range presence.Members {
		if member.X == 8 && member.Y == 9 {
			sawMoved = true
		}
	}
	if !sawMoved {
		t.Fatalf("presence after cursor move=%+v, want one member at 8,9", presence.Members)
	}

	if err := wsjson.Write(ctx, connA, EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 12, Y: 10, Element: E_SOLID, Color: 0x0e}); err != nil {
		t.Fatalf("write editor edit: %v", err)
	}
	var diffA EditorDiffMessage
	readEditorMessage(t, ctx, connA, MessageTypeEditorDiff, &diffA)
	var diffB EditorDiffMessage
	readEditorMessage(t, ctx, connB, MessageTypeEditorDiff, &diffB)
	if diffA.MemberID == "" || diffA.MemberID != diffB.MemberID {
		t.Fatalf("diff member ids A=%q B=%q, want same author", diffA.MemberID, diffB.MemberID)
	}
	if !editorDiffHasCell(diffB, 11, 9, E_SOLID) {
		t.Fatalf("remote diff cells=%+v, want solid tile at screen 11,9", diffB.Cells)
	}
}

func TestM102EditorLeasesRefuseAndDisconnectRelease(t *testing.T) {
	world := testMultiplayerSmokeWorld(t)
	world.Info.CurrentBoard = 1
	server := NewWebSocketServer(world, 1)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	connA := dialEditorClient(t, ctx, wsURL)
	connB := dialEditorClient(t, ctx, wsURL)
	defer connB.Close(websocket.StatusNormalClosure, "")
	readEditorPresenceWithMembers(t, ctx, connA, 2)

	if err := wsjson.Write(ctx, connA, EditorInspectMessage{Type: MessageTypeEditorInspect, X: 12, Y: 12}); err != nil {
		t.Fatalf("write editorInspect: %v", err)
	}
	var inspect EditorInspectMessage
	readEditorMessage(t, ctx, connA, MessageTypeEditorInspect, &inspect)
	if !inspect.Inspect.HasStat {
		t.Fatalf("inspect=%+v, want a stat tile to lease", inspect.Inspect)
	}
	lease := EditorLeaseMessage{
		Type:    MessageTypeEditorLease,
		Op:      "request",
		Kind:    "stat",
		BoardID: 1,
		StatID:  inspect.Inspect.StatID,
	}
	if err := wsjson.Write(ctx, connA, lease); err != nil {
		t.Fatalf("write connA lease: %v", err)
	}
	var granted EditorLeaseMessage
	readEditorMessage(t, ctx, connA, MessageTypeEditorLease, &granted)
	if granted.Op != "granted" {
		t.Fatalf("connA lease=%+v, want granted", granted)
	}
	if err := wsjson.Write(ctx, connB, lease); err != nil {
		t.Fatalf("write connB lease: %v", err)
	}
	var refused EditorLeaseMessage
	readEditorMessage(t, ctx, connB, MessageTypeEditorLease, &refused)
	if refused.Op != "refused" || refused.HolderName == "" {
		t.Fatalf("connB lease=%+v, want refused with holder", refused)
	}

	_ = connA.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("connB never acquired lease after connA disconnected")
		}
		if err := wsjson.Write(ctx, connB, lease); err != nil {
			t.Fatalf("retry connB lease: %v", err)
		}
		readEditorMessage(t, ctx, connB, MessageTypeEditorLease, &granted)
		if granted.Op == "granted" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestM103EditorOwnershipInvitesGateEdits(t *testing.T) {
	dir := t.TempDir()
	world := testEmptyWorld(t)
	world.Info.CurrentBoard = 1
	server := NewWebSocketServer(world, 1)
	server.WorldsDir = dir
	server.Auth = NewAuthService("client-id", "", "", []byte("test-cookie-secret"))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	ownerCookie := signedAuthCookie(t, server.Auth, AuthenticatedAccount{ID: "google:owner", Name: "Owner"})
	ownerConn, ownerSnap := dialEditorClientWithCookie(t, ctx, wsURL, "TOWN", ownerCookie)
	defer ownerConn.Close(websocket.StatusNormalClosure, "")
	if ownerSnap.ReadOnly {
		t.Fatal("owner's initial unowned editor session is read-only")
	}

	if err := wsjson.Write(ctx, ownerConn, EditorWorldMessage{Type: MessageTypeEditorWorld, Op: "save", Name: "OWNED"}); err != nil {
		t.Fatalf("write owner save: %v", err)
	}
	var save EditorSaveResultMessage
	readEditorMessage(t, ctx, ownerConn, MessageTypeEditorSaveResult, &save)
	if save.Error != "" || save.World != "OWNED" {
		t.Fatalf("owner save=%+v, want OWNED", save)
	}
	access, ok, err := loadWorldAccess(dir, "OWNED")
	if err != nil || !ok || access.OwnerAccountID != "google:owner" {
		t.Fatalf("access=(%+v,%v,%v), want owner google:owner", access, ok, err)
	}
	if err := wsjson.Write(ctx, ownerConn, EditorWorldMessage{Type: MessageTypeEditorWorld, Op: "invite", AccountID: "google:collab"}); err != nil {
		t.Fatalf("write invite: %v", err)
	}
	var invite EditorSaveResultMessage
	readEditorMessage(t, ctx, ownerConn, MessageTypeEditorSaveResult, &invite)
	if invite.Error != "" {
		t.Fatalf("invite refused: %+v", invite)
	}

	intruderCookie := signedAuthCookie(t, server.Auth, AuthenticatedAccount{ID: "google:intruder", Name: "Intruder"})
	intruderConn, intruderSnap := dialEditorClientWithCookie(t, ctx, wsURL, "OWNED", intruderCookie)
	defer intruderConn.Close(websocket.StatusNormalClosure, "")
	if !intruderSnap.ReadOnly {
		t.Fatal("uninvited account entered owned world with edit access")
	}
	if err := wsjson.Write(ctx, intruderConn, EditorLeaseMessage{Type: MessageTypeEditorLease, Op: "request", Kind: "board", BoardID: intruderSnap.BoardID}); err != nil {
		t.Fatalf("write intruder lease: %v", err)
	}
	var refused EditorLeaseMessage
	readEditorMessage(t, ctx, intruderConn, MessageTypeEditorLease, &refused)
	if refused.Op != "refused" || refused.Error != "read-only" {
		t.Fatalf("intruder lease=%+v, want read-only refusal", refused)
	}

	collabCookie := signedAuthCookie(t, server.Auth, AuthenticatedAccount{ID: "google:collab", Name: "Collaborator"})
	collabConn, collabSnap := dialEditorClientWithCookie(t, ctx, wsURL, "OWNED", collabCookie)
	defer collabConn.Close(websocket.StatusNormalClosure, "")
	if collabSnap.ReadOnly {
		t.Fatal("invited collaborator entered owned world as read-only")
	}
	if err := wsjson.Write(ctx, collabConn, EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 12, Y: 10, Element: E_SOLID, Color: 0x0e}); err != nil {
		t.Fatalf("write collaborator edit: %v", err)
	}
	var diff EditorDiffMessage
	readEditorMessage(t, ctx, collabConn, MessageTypeEditorDiff, &diff)
	if !editorDiffHasCell(diff, 11, 9, E_SOLID) {
		t.Fatalf("collaborator diff cells=%+v, want solid tile", diff.Cells)
	}
}

func TestM104EditorTestPlayUsesIsolatedTickingCopy(t *testing.T) {
	world := testEmptyWorld(t)
	world.Info.CurrentBoard = 1
	server := NewWebSocketServer(world, 1)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	session := NewEditorSession("TOWN", world)
	memberA := &webSocketClient{}
	memberB := &webSocketClient{}
	if err := session.Enter(memberA); err != nil {
		t.Fatalf("Enter(A): %v", err)
	}
	defer session.Exit(memberA)
	if err := session.Enter(memberB); err != nil {
		t.Fatalf("Enter(B): %v", err)
	}
	defer session.Exit(memberB)
	if _, err := session.Edit(memberA, EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 12, Y: 10, Element: E_SOLID, Color: 0x0e}); err != nil {
		t.Fatalf("session edit: %v", err)
	}
	before, err := session.WorldBytes(memberA, "")
	if err != nil {
		t.Fatalf("WorldBytes(before): %v", err)
	}

	testWorld, err := server.startEditorTestPlay(memberA, session)
	if err != nil {
		t.Fatalf("startEditorTestPlay: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connA, snapA := joinTestClientWorld(t, ctx, httpServer.URL, testWorld, "tester-a")
	defer connA.Close(websocket.StatusNormalClosure, "")
	connB, snapB := joinTestClientWorld(t, ctx, httpServer.URL, testWorld, "tester-b")
	defer connB.Close(websocket.StatusNormalClosure, "")
	if snapA.You.ID == snapB.You.ID {
		t.Fatalf("test players share id %d", snapA.You.ID)
	}
	for i := 0; i < 3; i++ {
		server.Tick(ctx)
	}
	after, err := session.WorldBytes(memberA, "")
	if err != nil {
		t.Fatalf("WorldBytes(after): %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("test play mutated the editor session world")
	}
}

// M5.5: the editorBoard dispatch over a real WebSocket — add, switch, export,
// and import — replies with the shapes the browser client consumes.
func TestWebSocketEditorBoardManagement(t *testing.T) {
	world := testMultiplayerSmokeWorld(t)
	world.Info.CurrentBoard = 1
	server := NewWebSocketServer(world, 1)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial editor: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, EditorEnterMessage{Type: MessageTypeEditorEnter, World: "TOWN"}); err != nil {
		t.Fatalf("write editorEnter: %v", err)
	}
	var snapshot EditorSnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read editor snapshot: %v", err)
	}

	// Add a board: reply is a full snapshot on the new board.
	if err := wsjson.Write(ctx, conn, EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "add", Name: "NORTH"}); err != nil {
		t.Fatalf("write editorBoard add: %v", err)
	}
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &snapshot)
	if snapshot.BoardID != 3 || snapshot.Properties.BoardName != "NORTH" {
		t.Fatalf("add reply=%+v, want new board 3 NORTH", snapshot.Properties)
	}

	// Switch back to board 1 (the gem/passage board).
	if err := wsjson.Write(ctx, conn, EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "switch", BoardID: 1}); err != nil {
		t.Fatalf("write editorBoard switch: %v", err)
	}
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &snapshot)
	if snapshot.BoardID != 1 {
		t.Fatalf("switch reply board=%d, want 1", snapshot.BoardID)
	}

	// Export the current board.
	if err := wsjson.Write(ctx, conn, EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "export"}); err != nil {
		t.Fatalf("write editorBoard export: %v", err)
	}
	var exported EditorBoardDataMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorBoardData, &exported)
	if exported.Type != MessageTypeEditorBoardData || exported.Data == "" {
		t.Fatalf("export=%+v, want board data", exported)
	}

	// Import those bytes over board 3.
	if err := wsjson.Write(ctx, conn, EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "switch", BoardID: 3}); err != nil {
		t.Fatalf("write switch to 3: %v", err)
	}
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &snapshot)
	if err := wsjson.Write(ctx, conn, EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "import", Data: exported.Data}); err != nil {
		t.Fatalf("write editorBoard import: %v", err)
	}
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &snapshot)
	if snapshot.BoardID != 3 {
		t.Fatalf("import reply board=%d, want 3", snapshot.BoardID)
	}
	// Board 3 now holds board 1's passage at 12,12.
	if err := wsjson.Write(ctx, conn, EditorInspectMessage{Type: MessageTypeEditorInspect, X: 12, Y: 12}); err != nil {
		t.Fatalf("write inspect: %v", err)
	}
	var inspect EditorInspectMessage
	if err := wsjson.Read(ctx, conn, &inspect); err != nil {
		t.Fatalf("read inspect: %v", err)
	}
	if inspect.Inspect.Element != "Passage" {
		t.Fatalf("imported board 3 at 12,12=%q, want Passage", inspect.Inspect.Element)
	}

	// Opening/editing an editor session must not create a live room or player.
	if len(server.RoomManager.players) != 0 || len(server.RoomManager.rooms) != 0 {
		t.Fatalf("editor touched live simulation: players=%d rooms=%d", len(server.RoomManager.players), len(server.RoomManager.rooms))
	}
}

func TestWebSocketServerBoardEdgeSendsBoardChange(t *testing.T) {
	world := testEdgeWorld(t)
	server := NewWebSocketServer(world, 1)
	server.TickDuration = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runServerAsync(t, ctx, server)

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
	runServerAsync(t, ctx, server)

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
	runServerAsync(t, ctx, server)

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
	if !hasSoundEvent(boardChange.Snapshot.Events, passageSoundPriority, soundNoteBytes(passageSoundPattern)) {
		t.Fatalf("boardChange events missing passage sound: %+v", boardChange.Snapshot.Events)
	}

	waitForDiff(t, ctx, conn2, func(diff DiffMessage) bool {
		return diff.BoardID == 1 && len(diff.Players) == 1 && diff.Players[0].ID == snap2.You.ID
	})
}

func hasSoundEvent(events []ProtocolEvent, priority int16, notes []uint16) bool {
	for _, event := range events {
		if event.Type == "sound" && event.Priority == priority && reflect.DeepEqual(event.Notes, notes) {
			return true
		}
	}
	return false
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

	inst := server.DefaultInstance
	inst.mu.Lock()
	if got := len(inst.Clients); got != botCount {
		inst.mu.Unlock()
		t.Fatalf("active clients=%d, want %d", got, botCount)
	}
	roomHashes := make(map[int16]uint64)
	for _, boardID := range server.RoomManager.roomIDs() {
		room, ok := server.RoomManager.Room(boardID)
		if ok {
			roomHashes[boardID] = StateHash(room.Engine)
		}
	}
	inst.mu.Unlock()

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

// runServerAsync starts server.Run in a goroutine and registers a cleanup that
// waits for it to return, so a server's tick goroutine never outlives its test
// and leaks into the next one. A leaked ticker reads the package-global
// ElementDefs that the next test's WorldCreate rewrites — a -count race the
// detector flags (M13.4, see NOTES.md). The caller's own `defer cancel()` fires
// before test cleanups, so it unblocks Run; this helper only joins it.
func runServerAsync(t *testing.T, ctx context.Context, server *WebSocketServer) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Run(ctx)
	}()
	t.Cleanup(func() { <-done })
}

func joinTestClient(t *testing.T, ctx context.Context, serverURL, name string) (*websocket.Conn, SnapshotMessage) {
	return joinTestClientWorld(t, ctx, serverURL, "", name)
}

func joinTestClientWorld(t *testing.T, ctx context.Context, serverURL, world, name string) (*websocket.Conn, SnapshotMessage) {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http")
	if world != "" {
		wsURL += "?world=" + world
	}
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

func dialEditorClient(t *testing.T, ctx context.Context, wsURL string) *websocket.Conn {
	t.Helper()
	conn, _ := dialEditorClientWithCookie(t, ctx, wsURL, "TOWN", nil)
	return conn
}

func dialEditorClientWithCookie(t *testing.T, ctx context.Context, wsURL, world string, cookie *http.Cookie) (*websocket.Conn, EditorSnapshotMessage) {
	t.Helper()
	opts := &websocket.DialOptions{}
	if cookie != nil {
		opts.HTTPHeader = http.Header{"Cookie": []string{cookie.String()}}
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial editor: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, EditorEnterMessage{Type: MessageTypeEditorEnter, World: world}); err != nil {
		t.Fatalf("write editorEnter: %v", err)
	}
	var snapshot EditorSnapshotMessage
	readEditorMessage(t, ctx, conn, MessageTypeEditorSnapshot, &snapshot)
	if snapshot.Type != MessageTypeEditorSnapshot || snapshot.MemberID == "" {
		t.Fatalf("editor snapshot=%+v, want member id", snapshot)
	}
	return conn, snapshot
}

func readEditorMessage(t *testing.T, ctx context.Context, conn *websocket.Conn, wantType string, out interface{}) {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read editor %s: %v", wantType, err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode editor envelope: %v", err)
		}
		if envelope.Type == MessageTypeEditorPresence && wantType != MessageTypeEditorPresence {
			continue
		}
		if envelope.Type != wantType {
			t.Fatalf("read editor type=%q, want %q raw=%s", envelope.Type, wantType, string(raw))
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode editor %s: %v", wantType, err)
		}
		return
	}
}

func readEditorPresenceWithMembers(t *testing.T, ctx context.Context, conn *websocket.Conn, want int) EditorPresenceMessage {
	t.Helper()
	for {
		var presence EditorPresenceMessage
		readEditorMessage(t, ctx, conn, MessageTypeEditorPresence, &presence)
		if len(presence.Members) == want {
			return presence
		}
	}
}

func readEditorPresenceAt(t *testing.T, ctx context.Context, conn *websocket.Conn, x, y int16) EditorPresenceMessage {
	t.Helper()
	for {
		var presence EditorPresenceMessage
		readEditorMessage(t, ctx, conn, MessageTypeEditorPresence, &presence)
		for _, member := range presence.Members {
			if member.X == x && member.Y == y {
				return presence
			}
		}
	}
}

func editorDiffHasCell(diff EditorDiffMessage, x, y int16, element byte) bool {
	for _, cell := range diff.Cells {
		if cell.X == x && cell.Y == y && cell.Ch == ElementDefs[element].Character {
			return true
		}
	}
	return false
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

// The full browser path for a scroll: touch the vendor over a real WebSocket,
// receive the scroll event, send back a scrollReply, and watch the trade land.
// This exercises the JSON dispatch added alongside debugCommand — a message
// that is not an InputMessage must not be silently parsed as one.
func TestWebSocketServerScrollReplyBuysFromVendor(t *testing.T) {
	world := testTownWorld(t)
	server := NewWebSocketServer(world, vendorBoard)
	server.TickDuration = time.Hour // never auto-ticks; we drive Tick ourselves

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "shopper", Board: vendorBoard}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	playerID := snapshot.You.ID

	// Stand the player immediately west of the vendor and stock them with gems.
	server.mu.Lock()
	room, ok := server.RoomManager.Room(vendorBoard)
	if !ok {
		server.mu.Unlock()
		t.Fatal("vendor room missing")
	}
	vendorStat, vx, vy := findVendor(t, room)
	_, statID, _ := server.RoomManager.PlayerLocation(playerID)
	movePlayerStat(room.Engine, statID, vx-1, vy)
	state, _ := server.RoomManager.PlayerState(playerID)
	state.Gems = 5
	state.Ammo = 0
	server.mu.Unlock()

	if err := wsjson.Write(ctx, conn, InputMessage{
		Type: MessageTypeInput, PlayerID: playerID, Seq: 1, Keymask: InputMaskRight,
	}); err != nil {
		t.Fatalf("write input: %v", err)
	}

	scroll, ok := readUntilScrollEvent(ctx, t, conn, server, 40)
	if !ok {
		t.Fatal("never received a scroll event from the vendor")
	}
	if scroll.StatID != vendorStat {
		t.Fatalf("scroll StatID=%d, want vendor %d", scroll.StatID, vendorStat)
	}
	if scroll.Title != "Vendor" {
		t.Fatalf("scroll title=%q, want Vendor", scroll.Title)
	}

	if err := wsjson.Write(ctx, conn, ScrollReplyMessage{
		Type: MessageTypeScrollReply, PlayerID: playerID, StatID: scroll.StatID, Label: "ba",
	}); err != nil {
		t.Fatalf("write scrollReply: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.Tick(ctx)
		server.mu.Lock()
		ammo, gems := state.Ammo, state.Gems
		server.mu.Unlock()
		if ammo == 3 && gems == 4 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("vendor never traded: ammo=%d gems=%d, want 3 and 4", state.Ammo, state.Gems)
}

// readUntilScrollEvent drives ticks and drains socket messages looking for a
// scroll. The input the client sent is applied by the server's reader goroutine,
// so ticks and reads are interleaved with a small pause.
func readUntilScrollEvent(ctx context.Context, t *testing.T, conn *websocket.Conn, server *WebSocketServer, maxTicks int) (ProtocolEvent, bool) {
	t.Helper()

	for i := 0; i < maxTicks; i++ {
		time.Sleep(5 * time.Millisecond)
		server.Tick(ctx)

		readCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		var raw json.RawMessage
		err := wsjson.Read(readCtx, conn, &raw)
		cancel()
		if err != nil {
			continue
		}
		var msg struct {
			Events []ProtocolEvent `json:"events"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		for _, event := range msg.Events {
			if event.Type == "scroll" {
				return event, true
			}
		}
	}
	return ProtocolEvent{}, false
}

func testTownWorld(t *testing.T) TWorld {
	t.Helper()

	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	worldBase := filepath.Join("..", "fixtures", "TOWN")
	if !setup.WorldLoad(worldBase, ".ZZT", false) {
		t.Fatalf("WorldLoad(%q, %q) failed", worldBase, ".ZZT")
	}
	return setup.World
}

// TestAnnounceShutdownSkipsWedgedInstance is the M-fix guarantee for the graceful
// shutdown warning: AnnounceShutdown must warn healthy worlds without blocking on
// a world whose engine is wedged (its lock cannot be taken). If it blocked, the
// whole shutdown drain would stall — the exact failure the warning exists to
// survive ("as long as the engine for their world is still up").
func TestAnnounceShutdownSkipsWedgedInstance(t *testing.T) {
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.Instances = map[string]*WorldInstance{
		"healthy": {Name: "healthy", Clients: make(map[PlayerID]*webSocketClient)},
		"wedged":  {Name: "wedged", Clients: make(map[PlayerID]*webSocketClient)},
	}

	// Simulate a wedged engine: hold its lock and never release it.
	server.Instances["wedged"].mu.Lock()
	defer server.Instances["wedged"].mu.Unlock()

	done := make(chan struct{})
	go func() {
		server.AnnounceShutdown(context.Background(), 60, "SERVER RESTARTING")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("AnnounceShutdown blocked on a wedged instance lock")
	}
}
