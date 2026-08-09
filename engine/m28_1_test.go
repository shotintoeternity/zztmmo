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
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func m281WriteWorld(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".ZZT"), committedTownBytes(t), 0o644); err != nil {
		t.Fatalf("write %s.ZZT: %v", name, err)
	}
}

func m281AddPlayers(t *testing.T, inst *WorldInstance, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		player := inst.RoomManager.JoinPlayer(1, 0, 0)
		inst.Clients[player] = &webSocketClient{}
	}
}

func m281Lineup(t *testing.T, api *WebAPI) []WatchLiveLineupEntry {
	t.Helper()
	rr := httptest.NewRecorder()
	api.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/watch/live", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/api/watch/live status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Entries []WatchLiveLineupEntry `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode lineup: %v", err)
	}
	return body.Entries
}

func TestM281WatchLiveLineupOrderExclusionsReplayConsentAndPrivacy(t *testing.T) {
	root := t.TempDir()
	worldsDir := filepath.Join(root, "worlds")
	replayDir := filepath.Join(root, "replays")
	if err := os.MkdirAll(worldsDir, 0o755); err != nil {
		t.Fatalf("mkdir worlds: %v", err)
	}
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		t.Fatalf("mkdir replays: %v", err)
	}
	for _, name := range []string{"TOWN", "ACCEPT", "LIVE"} {
		m281WriteWorld(t, worldsDir, name)
	}
	if err := os.WriteFile(filepath.Join(worldsDir, "BAD.ZZT"), []byte("not a zzt world"), 0o644); err != nil {
		t.Fatalf("write bad world: %v", err)
	}

	server := NewWebSocketServer(townWorld(t), 1)
	server.WorldsDir = worldsDir
	server.ReplayDir = replayDir
	for _, spec := range []struct {
		world   string
		players int
	}{{"TOWN", 3}, {"LIVE", 2}, {"ACCEPT", 1}} {
		inst, err := server.GetOrCreateInstance(spec.world)
		if err != nil {
			t.Fatalf("host %s: %v", spec.world, err)
		}
		m281AddPlayers(t, inst, spec.players)
	}

	single, _, _ := m223Recording(t, "TOWN-SINGLE-20260809")
	multi := m281MultiplayerRecording(t)
	if err := os.WriteFile(filepath.Join(replayDir, "TOWN-SINGLE-20260809.jsonl"), single, 0o644); err != nil {
		t.Fatalf("write single replay: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, "TOWN-MULTI-20260809.jsonl"), multi, 0o644); err != nil {
		t.Fatalf("write multi replay: %v", err)
	}
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	_ = os.Chtimes(filepath.Join(replayDir, "TOWN-SINGLE-20260809.jsonl"), now, now)
	_ = os.Chtimes(filepath.Join(replayDir, "TOWN-MULTI-20260809.jsonl"), now.Add(-time.Minute), now.Add(-time.Minute))

	api := &WebAPI{Server: server}
	lineup := m281Lineup(t, api)
	if len(lineup) != 4 {
		t.Fatalf("lineup length=%d, want 4: %+v", len(lineup), lineup)
	}
	wantWorlds := []string{"TOWN", "LIVE", "ACCEPT"}
	for i, world := range wantWorlds {
		if got := lineup[i]; got.Kind != "live" || got.World != world || got.Players != 3-i {
			t.Fatalf("lineup[%d]=%+v, want live %s with %d players", i, got, world, 3-i)
		}
	}
	if lineup[1].Path != "/watch/LIVE" {
		t.Fatalf("world LIVE must stay a world watch URL, got %q", lineup[1].Path)
	}
	replay := lineup[3]
	if replay.Kind != "replay" || replay.ReplayID != "TOWN-SINGLE-20260809" || replay.StartTick <= 0 || replay.Ticks <= 0 {
		t.Fatalf("single-player replay entry malformed or missing: %+v", replay)
	}
	body, err := json.Marshal(lineup)
	if err != nil {
		t.Fatalf("marshal lineup: %v", err)
	}
	for _, forbidden := range []string{"account", "Ada", "Bo", "BAD", "TOWN-MULTI"} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("lineup leaked %q in %s", forbidden, body)
		}
	}
}

func TestM281WatchLiveReplayScanIsBounded(t *testing.T) {
	root := t.TempDir()
	worldsDir := filepath.Join(root, "worlds")
	replayDir := filepath.Join(root, "replays")
	if err := os.MkdirAll(worldsDir, 0o755); err != nil {
		t.Fatalf("mkdir worlds: %v", err)
	}
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		t.Fatalf("mkdir replays: %v", err)
	}
	m281WriteWorld(t, worldsDir, "TOWN")
	valid, _, _ := m223Recording(t, "TOWN-OLD-20260809")
	if err := os.WriteFile(filepath.Join(replayDir, "TOWN-OLD-20260809.jsonl"), valid, 0o644); err != nil {
		t.Fatalf("write valid replay: %v", err)
	}
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	_ = os.Chtimes(filepath.Join(replayDir, "TOWN-OLD-20260809.jsonl"), base.Add(-time.Hour), base.Add(-time.Hour))
	for i := 0; i < watchLiveReplayScanLimit; i++ {
		name := filepath.Join(replayDir, "BROKEN-"+strings.Repeat("X", i%3)+string(rune('A'+i%26))+".jsonl")
		if err := os.WriteFile(name, []byte("broken\n"), 0o644); err != nil {
			t.Fatalf("write broken replay: %v", err)
		}
		stamp := base.Add(time.Duration(i) * time.Second)
		_ = os.Chtimes(name, stamp, stamp)
	}
	server := NewWebSocketServer(townWorld(t), 1)
	server.WorldsDir = worldsDir
	server.ReplayDir = replayDir
	lineup := (&WebAPI{Server: server}).WatchLiveLineup()
	if len(lineup) != 0 {
		t.Fatalf("bounded scan reached the old valid replay: %+v", lineup)
	}
}

func TestM281ReplayStartTickOpensSelectedWindow(t *testing.T) {
	id := "TOWN-START-20260809"
	data, _, _ := m223Recording(t, id)
	root := t.TempDir()
	replayDir := filepath.Join(root, "replays")
	worldsDir := filepath.Join(root, "worlds")
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		t.Fatalf("mkdir replays: %v", err)
	}
	if err := os.MkdirAll(worldsDir, 0o755); err != nil {
		t.Fatalf("mkdir worlds: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, id+".jsonl"), data, 0o644); err != nil {
		t.Fatalf("write replay: %v", err)
	}
	m281WriteWorld(t, worldsDir, "TOWN")

	server := NewWebSocketServer(townWorld(t), 1)
	server.ReplayDir = replayDir
	server.WorldsDir = worldsDir
	replay, err := server.GetOrCreateReplayInstanceAt(id, 20)
	if err != nil {
		t.Fatalf("GetOrCreateReplayInstanceAt: %v", err)
	}
	if got := replay.playback.LastTick(); got != 19 {
		t.Fatalf("start snapshot stopped after tick %d, want 19", got)
	}
	tick, _, done, err := replay.playback.Step()
	if err != nil || done || tick != 20 {
		t.Fatalf("first replay step tick=%d done=%v err=%v, want tick 20", tick, done, err)
	}
	if err := replay.Restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := replay.playback.LastTick(); got != 19 {
		t.Fatalf("restart returned to tick %d, want 19", got)
	}
}

func TestM281LiveChannelViewerIsStillOnlyAWatcher(t *testing.T) {
	root := t.TempDir()
	worldsDir := filepath.Join(root, "worlds")
	savesDir := filepath.Join(root, "saves")
	if err := os.MkdirAll(worldsDir, 0o755); err != nil {
		t.Fatalf("mkdir worlds: %v", err)
	}
	if err := os.MkdirAll(savesDir, 0o755); err != nil {
		t.Fatalf("mkdir saves: %v", err)
	}
	m281WriteWorld(t, worldsDir, "TOWN")

	server := NewWebSocketServer(townWorld(t), 1)
	server.WorldsDir = worldsDir
	server.SavesDir = savesDir
	server.AutosaveEveryTicks = 1
	server.Activity = &WorldActivityStore{counts: make(map[string]int)}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	before := server.DefaultInstance.RoomManager.RoomStateHashes()
	conn, _, err := websocket.Dial(ctx, wsURL+"?world=TOWN", nil)
	if err != nil {
		t.Fatalf("dial channel-selected watch route: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Spectate: true}); err != nil {
		t.Fatalf("write watch join: %v", err)
	}
	var snap SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil {
		t.Fatalf("read watch snapshot: %v", err)
	}
	if !snap.Spectator || snap.Watchers != 1 {
		t.Fatalf("channel-selected watcher snapshot malformed: %+v", snap)
	}
	if server.WorldIsOccupied("TOWN") {
		t.Fatal("watching through the live channel route changed player occupancy")
	}
	if got := server.Activity.Counts()["TOWN"]; got != 0 {
		t.Fatalf("watching through the live channel route incremented play count to %d", got)
	}
	server.Autosave()
	if snapshots := ListSnapshots(server.autosaveDir()); len(snapshots) != 0 {
		t.Fatalf("watching through the live channel route autosaved: %v", snapshots)
	}
	if after := server.DefaultInstance.RoomManager.RoomStateHashes(); !reflect.DeepEqual(after, before) {
		t.Fatalf("watching changed room hashes: before=%v after=%v", before, after)
	}
}

func m281MultiplayerRecording(t *testing.T) []byte {
	t.Helper()
	world := townWorld(t)
	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, "TOWN", world, &buf)
	ada := rm.JoinPlayer(1, 0, 0)
	bo := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerName(ada, "Ada")
	rm.SetPlayerName(bo, "Bo")
	rm.StepDiffs(map[PlayerID]PlayerInput{ada: {DeltaX: 1}, bo: {DeltaX: -1}})
	rec.Close()
	return buf.Bytes()
}
