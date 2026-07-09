package zztgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// M4.3a — savable, rejoinable room snapshots.

// openSnapshotBoard reads one board out of a world without joining anybody, so
// a test can inspect what a snapshot actually holds.
func openSnapshotBoard(t *testing.T, world TWorld, boardID int16) *Engine {
	t.Helper()

	e := newSnapshotEngine()
	e.World = world
	e.BoardOpen(boardID)
	return e
}

func countPlayerTiles(e *Engine) int {
	count := 0
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			if e.Board.Tiles[ix][iy].Element == E_PLAYER {
				count++
			}
		}
	}
	return count
}

func TestM43aSanitizeSaveName(t *testing.T) {
	valid := map[string]string{
		"TOWN":     "TOWN",
		"town":     "TOWN",
		"Save1":    "SAVE1",
		"A":        "A",
		"MY-SAVE":  "MY-SAVE",
		"12345678": "12345678",
	}
	for input, want := range valid {
		got, err := SanitizeSaveName(input)
		if err != nil {
			t.Errorf("SanitizeSaveName(%q) = error %v, want %q", input, err, want)
			continue
		}
		if got != want {
			t.Errorf("SanitizeSaveName(%q) = %q, want %q", input, got, want)
		}
	}

	invalid := []string{
		"",                 // empty
		"123456789",        // one over the 8-wide prompt
		"../TOWN",          // traversal
		"..",               // traversal
		"../../etc/passwd", // traversal
		"/etc/passwd",      // absolute
		`..\WINDOWS`,       // traversal, the other separator
		"a/b",              // separator
		`a\b`,              // separator
		"TOWN.ZZT",         // '.' is not in the charset
		"NUL\x00",          // NUL
		"SAVE ME",          // space
		"SAVE\nME",         // newline
		"~root",            // shell expansion
		"$HOME",            // shell expansion
		"CAFÉ",             // non-ASCII
	}
	for _, input := range invalid {
		if got, err := SanitizeSaveName(input); err == nil {
			t.Errorf("SanitizeSaveName(%q) = %q, want error", input, got)
		}
	}
}

// The DoD's named case: a filename containing "../" never escapes -saves, and
// leaves nothing behind when it is rejected.
func TestM43aTraversalFilenameIsRejected(t *testing.T) {
	root := t.TempDir()
	saves := filepath.Join(root, "saves")
	if err := os.MkdirAll(saves, 0o755); err != nil {
		t.Fatal(err)
	}

	rm := NewRoomManager(testEmptyWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)

	for _, name := range []string{"../ESCAPE", "../../ESCAPE", "/tmp/ESCAPE", `..\ESCAPE`} {
		path, err := rm.SaveSnapshot(saves, name, playerID)
		if !errors.Is(err, ErrInvalidSaveName) {
			t.Errorf("SaveSnapshot(%q) = (%q, %v), want ErrInvalidSaveName", name, path, err)
		}
	}

	// Nothing was written anywhere: not in -saves, not above it.
	for _, dir := range []string{root, saves} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() != "saves" {
				t.Errorf("%s: unexpected %q created by a rejected save", dir, entry.Name())
			}
		}
	}
}

func TestM43aSaveRequiresSavesDirAndRealPlayer(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)

	if _, err := rm.SaveSnapshot("", "TOWN", playerID); !errors.Is(err, ErrSavesDisabled) {
		t.Errorf("SaveSnapshot with no -saves dir: err = %v, want ErrSavesDisabled", err)
	}
	if _, err := rm.SaveSnapshot(t.TempDir(), "TOWN", playerID+99); !errors.Is(err, ErrNoSuchPlayer) {
		t.Errorf("SaveSnapshot for an unknown player: err = %v, want ErrNoSuchPlayer", err)
	}
}

// The whole DoD: A saves, the process "restarts" (a fresh RoomManager that has
// never seen the world), B loads the snapshot by name and joins it, and the
// board contents, flags, and puzzle progress are all there.
func TestM43aSaveRestoreRoundTrip(t *testing.T) {
	saves := t.TempDir()

	rm := NewRoomManager(testEmptyWorld(t))
	playerA := rm.JoinPlayer(1, 0, 0)
	room, ok := rm.Room(1)
	if !ok {
		t.Fatal("board 1 has no room")
	}

	// Puzzle progress: a flag one player set, and a change to the board.
	room.Engine.WorldSetFlag("SOLVED")
	room.Engine.Board.Tiles[20][10] = TTile{Element: E_AMMO, Color: ElementDefs[E_AMMO].Color}
	rm.PlayerState(playerA)
	state, _ := rm.PlayerState(playerA)
	state.Score = 137

	if _, err := rm.SaveSnapshot(saves, "town1", playerA); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	// SanitizeSaveName upper-cases, so the lower-case name landed in TOWN1.SAV.
	if _, err := os.Stat(filepath.Join(saves, "TOWN1.SAV")); err != nil {
		t.Fatalf("snapshot file: %v", err)
	}
	if got := ListSnapshots(saves); len(got) != 1 || got[0] != "TOWN1" {
		t.Fatalf("ListSnapshots = %v, want [TOWN1]", got)
	}

	// The process restarts: nothing of rm survives.
	restored := NewRoomManager(TWorld{})
	if err := restored.RestoreSnapshot(saves, "TOWN1"); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	// Before anybody joins, the snapshot holds no players at all.
	board := openSnapshotBoard(t, restored.FrozenWorld(), 1)
	if got := countPlayerTiles(board); got != 0 {
		t.Errorf("restored board holds %d player tiles, want 0", got)
	}
	if board.Board.Tiles[20][10].Element != E_AMMO {
		t.Errorf("board contents lost: (20,10) = %v, want E_AMMO", board.Board.Tiles[20][10].Element)
	}

	playerB := restored.JoinPlayer(1, 0, 0)
	room2, ok := restored.Room(1)
	if !ok {
		t.Fatal("restored board 1 has no room")
	}
	if room2.Engine.Board.Tiles[20][10].Element != E_AMMO {
		t.Error("the ammo A left on the board did not survive the round trip")
	}
	if room2.Engine.WorldGetFlagPosition("SOLVED") < 0 {
		t.Error("flag SOLVED did not survive the round trip")
	}
	if got := countPlayerTiles(room2.Engine); got != 1 {
		t.Errorf("after B joins, board holds %d player tiles, want 1", got)
	}

	// DECISION (NOTES.md): B joins a restored snapshot exactly as B would join
	// any running world — fresh stats, not A's.
	stateB, ok := restored.PlayerState(playerB)
	if !ok {
		t.Fatal("B has no state")
	}
	if stateB.Score != 0 || stateB.Health != 100 {
		t.Errorf("B joined with score %d health %d, want a fresh 0/100", stateB.Score, stateB.Health)
	}
}

// A save must not disturb the game it is a save of.
func TestM43aSaveDoesNotDisturbTheLiveRoom(t *testing.T) {
	saves := t.TempDir()

	rm := NewRoomManager(testEmptyWorld(t))
	playerA := rm.JoinPlayer(1, 0, 0)
	playerB := rm.JoinPlayer(1, 20, 12)
	rm.Step(nil)

	room, _ := rm.Room(1)
	before := StateHash(room.Engine)
	statCount := room.Engine.Board.StatCount
	players := countPlayerTiles(room.Engine)

	if _, err := rm.SaveSnapshot(saves, "LIVE", playerA); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	if after := StateHash(room.Engine); after != before {
		t.Errorf("saving changed the live room: hash %d -> %d", before, after)
	}
	if room.Engine.Board.StatCount != statCount {
		t.Errorf("saving changed StatCount: %d -> %d", statCount, room.Engine.Board.StatCount)
	}
	if got := countPlayerTiles(room.Engine); got != players {
		t.Errorf("saving removed a live player tile: %d -> %d", players, got)
	}

	// Both players still tick, and both still have their own state.
	rm.Step(nil)
	if _, ok := rm.PlayerState(playerA); !ok {
		t.Error("player A vanished from the live room")
	}
	if _, ok := rm.PlayerState(playerB); !ok {
		t.Error("player B vanished from the live room")
	}
}

// Two players in two rooms each set a flag. Neither room's copy of World.Info
// holds both, so the snapshot has to union them.
func TestM43aSnapshotUnionsFlagsAcrossRooms(t *testing.T) {
	saves := t.TempDir()

	rm := NewRoomManager(testMultiplayerSmokeWorld(t))
	playerA := rm.JoinPlayer(1, 0, 0)
	rm.JoinPlayer(2, 0, 0)

	roomA, _ := rm.Room(1)
	roomB, _ := rm.Room(2)
	roomA.Engine.WorldSetFlag("DOORA")
	roomB.Engine.WorldSetFlag("DOORB")

	// Precondition for the test to mean anything: the two rooms disagree.
	if roomA.Engine.WorldGetFlagPosition("DOORB") >= 0 {
		t.Fatal("rooms already share flags; the union is untested")
	}

	if _, err := rm.SaveSnapshot(saves, "FLAGS", playerA); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	restored := NewRoomManager(TWorld{})
	if err := restored.RestoreSnapshot(saves, "FLAGS"); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	restored.JoinPlayer(1, 0, 0)
	room, _ := restored.Room(1)

	for _, flag := range []string{"DOORA", "DOORB"} {
		if room.Engine.WorldGetFlagPosition(flag) < 0 {
			t.Errorf("flag %s was lost by the snapshot", flag)
		}
	}
}

// Every player on every board is dropped, not frozen onto the board.
func TestM43aSnapshotDropsEveryPlayer(t *testing.T) {
	saves := t.TempDir()

	rm := NewRoomManager(testMultiplayerSmokeWorld(t))
	playerA := rm.JoinPlayer(1, 0, 0)
	rm.JoinPlayer(1, 20, 12)
	rm.JoinPlayer(2, 0, 0)

	if _, err := rm.SaveSnapshot(saves, "DROP", playerA); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	restored := NewRoomManager(TWorld{})
	if err := restored.RestoreSnapshot(saves, "DROP"); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	for _, boardID := range []int16{1, 2} {
		board := openSnapshotBoard(t, restored.FrozenWorld(), boardID)
		if got := countPlayerTiles(board); got != 0 {
			t.Errorf("board %d: %d player tiles survived the snapshot, want 0", boardID, got)
		}
		for statID := int16(0); statID <= board.Board.StatCount; statID++ {
			stat := board.Board.Stats[statID]
			if board.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
				t.Errorf("board %d: stat %d is still a player", boardID, statID)
			}
		}
	}
}

// A restore rewrites every board, so it may not happen under a live player.
func TestM43aRestoreRefusedWhileOccupied(t *testing.T) {
	saves := t.TempDir()

	rm := NewRoomManager(testEmptyWorld(t))
	playerA := rm.JoinPlayer(1, 0, 0)
	if _, err := rm.SaveSnapshot(saves, "BUSY", playerA); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	if err := rm.RestoreSnapshot(saves, "BUSY"); !errors.Is(err, ErrWorldOccupied) {
		t.Fatalf("RestoreSnapshot with a player in a room: err = %v, want ErrWorldOccupied", err)
	}
	if _, ok := rm.PlayerState(playerA); !ok {
		t.Error("the refused restore removed the player anyway")
	}

	// Once they leave, it is allowed.
	rm.LeavePlayer(playerA)
	if err := rm.RestoreSnapshot(saves, "BUSY"); err != nil {
		t.Errorf("RestoreSnapshot on an idle world: %v", err)
	}
}

// PlayerIDs keep climbing across a restore: the RoomManager is reused, so no
// client id can ever be handed out twice.
func TestM43aRestoreKeepsPlayerIDsUnique(t *testing.T) {
	saves := t.TempDir()

	rm := NewRoomManager(testEmptyWorld(t))
	playerA := rm.JoinPlayer(1, 0, 0)
	if _, err := rm.SaveSnapshot(saves, "IDS", playerA); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	rm.LeavePlayer(playerA)

	if err := rm.RestoreSnapshot(saves, "IDS"); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if playerB := rm.JoinPlayer(1, 0, 0); playerB == playerA {
		t.Errorf("PlayerID %d was reused after a restore", playerB)
	}
}

func TestM43aRestoreRejectsMissingAndUnsafeNames(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))

	if err := rm.RestoreSnapshot(t.TempDir(), "../ESCAPE"); !errors.Is(err, ErrInvalidSaveName) {
		t.Errorf("RestoreSnapshot(\"../ESCAPE\"): err = %v, want ErrInvalidSaveName", err)
	}
	if err := rm.RestoreSnapshot(t.TempDir(), "ABSENT"); err == nil {
		t.Error("RestoreSnapshot of a missing snapshot succeeded")
	}
	if err := rm.RestoreSnapshot("", "ANY"); !errors.Is(err, ErrSavesDisabled) {
		t.Errorf("RestoreSnapshot with no -saves dir: err = %v, want ErrSavesDisabled", err)
	}
}

// The serializer, on its own: StoreWorldInfo dropped Flags entirely, so a saved
// world lost its puzzle progress. LoadWorldInfo has always read them back from
// offsets 46+21*i (GAMEVARS.PAS:120). Mutation-checked: removing the store loop
// turns this red.
func TestM43aWorldInfoFlagsRoundTrip(t *testing.T) {
	info := TWorldInfo{Name: "TOWN", Health: 42, BoardTimeSec: 7}
	info.Flags[0] = "SOLVED"
	info.Flags[3] = "DOOROPEN"
	info.Flags[MAX_FLAG-1] = "LAST"

	buf := make([]byte, SizeOfWorldInfo)
	StoreWorldInfo(buf, &info)

	var back TWorldInfo
	LoadWorldInfo(buf, &back)

	if back.Flags != info.Flags {
		t.Errorf("flags round trip = %v, want %v", back.Flags, info.Flags)
	}
	// The neighbouring fields must not have moved.
	if back.Name != "TOWN" || back.Health != 42 || back.BoardTimeSec != 7 {
		t.Errorf("flags overwrote a neighbour: name=%q health=%d boardTimeSec=%d",
			back.Name, back.Health, back.BoardTimeSec)
	}
}

// dialSavePlayer joins one player and returns its connection and snapshot.
func dialSavePlayer(ctx context.Context, t *testing.T, url string) (*websocket.Conn, SnapshotMessage) {
	t.Helper()

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "saver", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return conn, snapshot
}

// The full wire path: 'S' opens the prompt, the name comes back, the snapshot
// lands in -saves, and the player is told what it is called.
func TestM43aSaveOverWebSocket(t *testing.T) {
	saves := t.TempDir()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, snapshot := dialSavePlayer(ctx, t, "ws"+strings.TrimPrefix(httpServer.URL, "http"))
	defer conn.Close(websocket.StatusNormalClosure, "")
	playerID := snapshot.You.ID

	if err := wsjson.Write(ctx, conn, InputMessage{Type: MessageTypeInput, PlayerID: playerID, Seq: 1, Key: 'S'}); err != nil {
		t.Fatalf("write S: %v", err)
	}
	prompt, ok := readUntilProtocolEvent(ctx, t, conn, server, "savePrompt", 20)
	if !ok {
		t.Fatal("no savePrompt event after pressing S")
	}
	if prompt.StatID != snapshot.You.StatID {
		t.Errorf("savePrompt.statId=%d, want %d", prompt.StatID, snapshot.You.StatID)
	}

	if err := wsjson.Write(ctx, conn, SaveFilenameMessage{Type: MessageTypeSaveFilename, PlayerID: playerID, Name: "mysave"}); err != nil {
		t.Fatalf("write saveFilename: %v", err)
	}
	result, ok := readUntilProtocolEvent(ctx, t, conn, server, "saveResult", 20)
	if !ok {
		t.Fatal("no saveResult event after submitting a filename")
	}
	if result.Error != "" {
		t.Fatalf("saveResult.error = %q, want a successful save", result.Error)
	}
	if result.Filename != "MYSAVE" {
		t.Errorf("saveResult.filename = %q, want MYSAVE", result.Filename)
	}
	if _, err := os.Stat(filepath.Join(saves, "MYSAVE.SAV")); err != nil {
		t.Errorf("snapshot file: %v", err)
	}

	// The player is still playing: a save is not a quit.
	if _, ok := server.RoomManager.PlayerState(playerID); !ok {
		t.Error("saving removed the player from their room")
	}
}

// A traversing filename is refused on the wire, and nothing is written.
func TestM43aSaveRejectedOverWebSocket(t *testing.T) {
	root := t.TempDir()
	saves := filepath.Join(root, "saves")

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, snapshot := dialSavePlayer(ctx, t, "ws"+strings.TrimPrefix(httpServer.URL, "http"))
	defer conn.Close(websocket.StatusNormalClosure, "")

	msg := SaveFilenameMessage{Type: MessageTypeSaveFilename, PlayerID: snapshot.You.ID, Name: "../ESCAPE"}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		t.Fatalf("write saveFilename: %v", err)
	}
	result, ok := readUntilProtocolEvent(ctx, t, conn, server, "saveResult", 20)
	if !ok {
		t.Fatal("no saveResult event for a rejected filename")
	}
	if result.Error == "" {
		t.Error("a traversing filename was accepted")
	}

	if _, err := os.Stat(filepath.Join(root, "ESCAPE.SAV")); !os.IsNotExist(err) {
		t.Error("a rejected save escaped the -saves directory")
	}
}

// A server started without -saves refuses every save, and says so.
func TestM43aSavesDisabledOverWebSocket(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, snapshot := dialSavePlayer(ctx, t, "ws"+strings.TrimPrefix(httpServer.URL, "http"))
	defer conn.Close(websocket.StatusNormalClosure, "")

	msg := SaveFilenameMessage{Type: MessageTypeSaveFilename, PlayerID: snapshot.You.ID, Name: "ANY"}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		t.Fatalf("write saveFilename: %v", err)
	}
	result, ok := readUntilProtocolEvent(ctx, t, conn, server, "saveResult", 20)
	if !ok {
		t.Fatal("no saveResult event")
	}
	if !strings.Contains(result.Error, "disabled") {
		t.Errorf("saveResult.error = %q, want it to mention saving is disabled", result.Error)
	}
}

// /api/saves lists what /api/restore will accept, and a restore is refused with
// 409 while a player is still in a room.
func TestM43aRestoreEndpoint(t *testing.T) {
	saves := t.TempDir()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves
	api := &WebAPI{RoomManager: server.RoomManager, SavesDir: saves, Server: server}
	handler := api.Handler()

	playerID := server.RoomManager.JoinPlayer(1, 0, 0)
	if _, err := server.RoomManager.SaveSnapshot(saves, "SNAP", playerID); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/saves", nil))
	var listed struct {
		Saves []string `json:"saves"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode /api/saves: %v", err)
	}
	if len(listed.Saves) != 1 || listed.Saves[0] != "SNAP" {
		t.Errorf("/api/saves = %v, want [SNAP]", listed.Saves)
	}

	restore := func(name string) int {
		body := bytes.NewBufferString(`{"name":"` + name + `"}`)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/restore", body))
		return rec.Code
	}

	if code := restore("SNAP"); code != http.StatusConflict {
		t.Errorf("restore while a player is in a room: status %d, want 409", code)
	}
	if code := restore("../ESCAPE"); code != http.StatusBadRequest {
		t.Errorf("restore of a traversing name: status %d, want 400", code)
	}

	server.RoomManager.LeavePlayer(playerID)
	if code := restore("ABSENT"); code != http.StatusNotFound {
		t.Errorf("restore of a missing snapshot: status %d, want 404", code)
	}
	if code := restore("SNAP"); code != http.StatusOK {
		t.Errorf("restore of an idle world: status %d, want 200", code)
	}

	// And the restored world is joinable.
	if joined := server.RoomManager.JoinPlayer(1, 0, 0); joined == 0 {
		t.Error("could not join the restored world")
	}
}

// DEVIATION (NOTES.md): GAME.PAS:780 zeroes the whole 512-byte header before
// filling it. The machine conversion zeroed byte 0 five hundred and twelve
// times, so the padding between IsSave and the first board carried whatever
// BoardClose had left in IoTmpBuf. Mutation-checked: restoring `ptr[0] = 0`
// turns this red.
func TestM43aSavedHeaderPaddingIsZeroed(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine()
	e.Headless = true
	e.SetInputSource(&ScriptedInput{})
	e.World = testEmptyWorld(t)
	e.BoardOpen(1)
	// Alternating tiles defeat the RLE, so serializing this board fills
	// thousands of bytes of IoTmpBuf. That is the memory the header padding
	// used to leak — a board that serializes to under 279 bytes never reaches
	// the padding and would hide the bug.
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			if (ix+iy)%2 == 0 {
				e.Board.Tiles[ix][iy] = TTile{Element: E_NORMAL, Color: 0x07}
			} else {
				e.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
			}
		}
	}

	// WorldSave's own BoardClose dirties IoTmpBuf, exactly as in real use.
	e.WorldSave(filepath.Join(dir, "PAD"), ".SAV")
	saved, err := os.ReadFile(filepath.Join(dir, "PAD.SAV"))
	if err != nil {
		t.Fatalf("read saved world: %v", err)
	}
	if len(saved) < 512 {
		t.Fatalf("saved world is %d bytes, want a 512-byte header", len(saved))
	}

	// 2 version bytes + 2 board-count bytes + SizeOfWorldInfo, then padding.
	for i := 4 + SizeOfWorldInfo; i < 512; i++ {
		if saved[i] != 0 {
			t.Fatalf("header byte %d = %#x, want 0: board memory leaked into the file", i, saved[i])
		}
	}
}
