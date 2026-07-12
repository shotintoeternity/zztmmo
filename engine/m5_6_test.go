package zztgo

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// M5.6 — save, host, download, and upload edited worlds.

// A world edited in the browser editor downloads as vanilla .ZZT bytes that
// reload with board contents intact.
func TestEditorSessionWorldBytesRoundTrip(t *testing.T) {
	world := testMultiplayerSmokeWorld(t)
	world.Info.CurrentBoard = 1
	session := NewEditorSession("SMOKE", world)
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	// Make an edit so the round trip carries an authored change, not just the
	// pristine bytes.
	if _, err := session.Edit(member, EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 20, Y: 10, Element: E_SOLID, Color: 0x0E}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	data, err := session.WorldBytes(member, "MYWORLD")
	if err != nil {
		t.Fatalf("WorldBytes: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("WorldBytes returned no bytes")
	}

	// Reload through the server's disk load path, exactly as a joiner would.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MYWORLD.ZZT"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reloaded, err := LoadPristineWorld(dir, "MYWORLD")
	if err != nil {
		t.Fatalf("LoadPristineWorld: %v", err)
	}
	if reloaded.Info.Name != "MYWORLD" {
		t.Errorf("reloaded world name=%q, want MYWORLD", reloaded.Info.Name)
	}

	e := newSnapshotEngine()
	e.World = reloaded
	e.BoardOpen(1)
	if got := e.Board.Tiles[20][10].Element; got != E_SOLID {
		t.Errorf("edited tile (20,10)=%d, want E_SOLID=%d", got, E_SOLID)
	}
	if e.Board.Tiles[11][12].Element != E_GEM {
		t.Errorf("original gem at (11,12) lost after round trip: %d", e.Board.Tiles[11][12].Element)
	}
	if e.Board.Name != "Smoke A" {
		t.Errorf("board name=%q, want Smoke A", e.Board.Name)
	}
}

// Editing must not mutate the source world or its bytes: WorldBytes serializes a
// copy, and BoardOpen restores the in-memory board so the session keeps editing.
func TestEditorSessionWorldBytesLeavesSessionEditable(t *testing.T) {
	session := NewEditorSession("EMPTY", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	if _, err := session.WorldBytes(member, "OUT"); err != nil {
		t.Fatalf("WorldBytes: %v", err)
	}
	// The board is still open and editable after serializing.
	reply, err := session.Edit(member, EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 5, Y: 5, Element: E_SOLID, Color: 0x0F})
	if err != nil {
		t.Fatalf("Edit after WorldBytes: %v", err)
	}
	if reply.Inspect.Element != "Solid" {
		t.Fatalf("post-serialize edit inspect=%q, want Solid", reply.Inspect.Element)
	}
}

// Upload replaces the session world with validated .ZZT bytes; garbage is refused
// with a gate message and the session is left untouched.
func TestEditorSessionUploadWorldValidatesAndReplaces(t *testing.T) {
	// A valid world to upload: serialize the smoke world's bytes.
	src := NewEditorSession("SRC", testMultiplayerSmokeWorld(t))
	srcMember := &webSocketClient{}
	if err := src.Enter(srcMember); err != nil {
		t.Fatalf("src Enter: %v", err)
	}
	good, err := src.WorldBytes(srcMember, "UP")
	if err != nil {
		t.Fatalf("src WorldBytes: %v", err)
	}
	src.Exit(srcMember)

	session := NewEditorSession("EMPTY", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	// Garbage first: refused, session unchanged (still the 1-board empty world).
	snap, gate, err := session.UploadWorld(member, []byte("not a world"))
	if err != nil {
		t.Fatalf("UploadWorld(garbage): %v", err)
	}
	if gate == "" {
		t.Fatal("garbage upload was accepted; want a gate refusal")
	}
	if snap.Properties.BoardName != "" {
		// testEmptyWorld's board has no name; a replaced world (smoke) would.
		t.Fatalf("refused upload changed the session board to %q", snap.Properties.BoardName)
	}

	// Now the valid world: accepted, session switches to it.
	snap, gate, err = session.UploadWorld(member, good)
	if err != nil {
		t.Fatalf("UploadWorld(good): %v", err)
	}
	if gate != "" {
		t.Fatalf("valid upload refused: %q", gate)
	}
	// The smoke world's CurrentBoard is 2; upload opens it, so Smoke B.
	if snap.Properties.BoardName != "Smoke B" {
		t.Fatalf("uploaded world board=%q, want Smoke B", snap.Properties.BoardName)
	}
}

// saveEditorWorld publishes the world so the picker lists it and a second client
// can join and play it.
func TestWebSocketServerEditorSavePublishesAndPlays(t *testing.T) {
	dir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = dir

	session := NewEditorSession("EMPTY", testMultiplayerSmokeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	name, err := server.saveEditorWorld(member, session, "newworld")
	if err != nil {
		t.Fatalf("saveEditorWorld: %v", err)
	}
	if name != "NEWWORLD" {
		t.Fatalf("saved name=%q, want NEWWORLD", name)
	}
	if _, err := os.Stat(filepath.Join(dir, "NEWWORLD.ZZT")); err != nil {
		t.Fatalf("published .ZZT missing: %v", err)
	}

	worlds := ListWorlds(dir)
	found := false
	for _, w := range worlds {
		if w == "NEWWORLD" {
			found = true
		}
	}
	if !found {
		t.Fatalf("picker does not list the published world: %v", worlds)
	}

	// A second client joins the hosted world and plays it.
	inst, err := server.GetOrCreateInstance("NEWWORLD")
	if err != nil {
		t.Fatalf("GetOrCreateInstance: %v", err)
	}
	rm := inst.RoomManager
	playerID := rm.JoinPlayer(1, 0, 0)
	for i := 0; i < 10; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{playerID: {DeltaX: 1}})
	}
	if _, ok := rm.PlayerState(playerID); !ok {
		t.Fatal("player vanished from the published world")
	}
}

// A world someone is playing is never overwritten by a save.
func TestWebSocketServerEditorSaveRefusesOccupiedWorld(t *testing.T) {
	dir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = dir

	// Host BUSY and seat a client in it.
	if err := server.HostGeneratedWorld("BUSY", testMultiplayerSmokeWorld(t)); err != nil {
		t.Fatalf("HostGeneratedWorld: %v", err)
	}
	server.mu.Lock()
	server.Instances["BUSY"].Clients[PlayerID(1)] = &webSocketClient{}
	server.mu.Unlock()

	session := NewEditorSession("EMPTY", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	if _, err := server.saveEditorWorld(member, session, "BUSY"); err == nil {
		t.Fatal("save overwrote a world someone is playing")
	}
	if _, err := os.Stat(filepath.Join(dir, "BUSY.ZZT")); !os.IsNotExist(err) {
		t.Fatal("an occupied-world save wrote a file anyway")
	}
}

// A traversing name is rejected and nothing escapes the worlds directory.
func TestWebSocketServerEditorSaveRejectsTraversalName(t *testing.T) {
	root := t.TempDir()
	worlds := filepath.Join(root, "worlds")
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = worlds

	session := NewEditorSession("EMPTY", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	if _, err := server.saveEditorWorld(member, session, "../ESCAPE"); err == nil {
		t.Fatal("a traversing filename was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "ESCAPE.ZZT")); !os.IsNotExist(err) {
		t.Fatal("a rejected save escaped the worlds directory")
	}
}

// The full wire path: enter the editor, download the world, save it, and see it
// appear as a joinable instance.
func TestWebSocketEditorWorldSaveAndDownload(t *testing.T) {
	dir := t.TempDir()
	world := testMultiplayerSmokeWorld(t)
	world.Info.CurrentBoard = 1
	server := NewWebSocketServer(world, 1)
	server.WorldsDir = dir

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

	// Download the world.
	if err := wsjson.Write(ctx, conn, EditorWorldMessage{Type: MessageTypeEditorWorld, Op: "download"}); err != nil {
		t.Fatalf("write download: %v", err)
	}
	var downloaded EditorWorldDataMessage
	if err := wsjson.Read(ctx, conn, &downloaded); err != nil {
		t.Fatalf("read download: %v", err)
	}
	if downloaded.Type != MessageTypeEditorWorldData || downloaded.Data == "" {
		t.Fatalf("download reply=%+v, want world bytes", downloaded)
	}
	raw, err := base64.StdEncoding.DecodeString(downloaded.Data)
	if err != nil || len(raw) == 0 {
		t.Fatalf("download data not valid base64 .ZZT: %v", err)
	}

	// Save/publish the world.
	if err := wsjson.Write(ctx, conn, EditorWorldMessage{Type: MessageTypeEditorWorld, Op: "save", Name: "EDITED"}); err != nil {
		t.Fatalf("write save: %v", err)
	}
	var result EditorSaveResultMessage
	if err := wsjson.Read(ctx, conn, &result); err != nil {
		t.Fatalf("read saveResult: %v", err)
	}
	if result.Error != "" || result.World != "EDITED" {
		t.Fatalf("saveResult=%+v, want world EDITED", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "EDITED.ZZT")); err != nil {
		t.Fatalf("published world missing: %v", err)
	}
	server.mu.Lock()
	_, hosted := server.Instances["EDITED"]
	server.mu.Unlock()
	if !hosted {
		t.Fatal("saved world was not hosted as an instance")
	}
}
