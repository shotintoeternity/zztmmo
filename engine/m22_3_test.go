package zztgo

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func m223Recording(t *testing.T, id string) ([]byte, int, map[int16]uint64) {
	t.Helper()
	world := townWorld(t)
	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, "TOWN", world, &buf)
	p1 := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerName(p1, "Ada")
	rm.Snapshot(p1)

	const totalTicks = 48
	for tick := 0; tick < totalTicks; tick++ {
		input := PlayerInput{}
		if tick%16 < 8 {
			input = PlayerInput{DeltaX: 1}
		} else {
			input = PlayerInput{DeltaX: -1}
		}
		rm.StepDiffs(map[PlayerID]PlayerInput{p1: input})
	}
	rec.Close()
	return buf.Bytes(), totalTicks - 1, rm.RoomStateHashes()
}

func TestM223ReplayPlaybackMatchesReplaySessionFinalHash(t *testing.T) {
	data, finalTick, liveHashes := m223Recording(t, "TOWN-20260807-120000")

	var replayLast int
	replayed, err := ReplaySession(bytes.NewReader(data), func(tick int, rm *RoomManager) {
		replayLast = tick
	})
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}
	if replayLast != finalTick {
		t.Fatalf("ReplaySession final tick=%d, want %d", replayLast, finalTick)
	}
	if got := replayed.RoomStateHashes(); !reflect.DeepEqual(got, liveHashes) {
		t.Fatalf("ReplaySession hashes=%v, want live recording hashes=%v", got, liveHashes)
	}

	playback, err := NewReplayPlayback(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewReplayPlayback: %v", err)
	}
	var playbackLast int
	for {
		tick, _, done, err := playback.Step()
		if err != nil {
			t.Fatalf("playback step: %v", err)
		}
		if !done {
			playbackLast = tick
			continue
		}
		break
	}
	if playbackLast != finalTick {
		t.Fatalf("playback final tick=%d, want %d", playbackLast, finalTick)
	}
	if got := playback.RoomStateHashes(); !reflect.DeepEqual(got, liveHashes) {
		t.Fatalf("playback hashes=%v, want recording hashes=%v", got, liveHashes)
	}
}

func TestM223ReplayInstanceIsNotAHostableWorld(t *testing.T) {
	id := "TOWN-20260807-120000"
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
		t.Fatalf("write recording: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worldsDir, "TOWN.ZZT"), committedTownBytes(t), 0o644); err != nil {
		t.Fatalf("write TOWN.ZZT: %v", err)
	}

	server := NewWebSocketServer(townWorld(t), 1)
	server.ReplayDir = replayDir
	server.WorldsDir = worldsDir
	server.SavesDir = filepath.Join(root, "saves")
	server.AutosaveEveryTicks = 1
	replay, err := server.GetOrCreateReplayInstance(id)
	if err != nil {
		t.Fatalf("GetOrCreateReplayInstance: %v", err)
	}
	defer replay.Close()
	if _, ok := server.Instances[id]; ok {
		t.Fatal("replay instance was registered as a hostable world")
	}
	if server.WorldIsOccupied(id) {
		t.Fatal("replay instance counted as occupied world")
	}
	server.Autosave()
	if snapshots := ListSnapshots(server.autosaveDir()); len(snapshots) != 0 {
		t.Fatalf("autosave wrote replay snapshots: %v", snapshots)
	}
}

func TestM223ReplayMissingHostedWorldIsAPlainError(t *testing.T) {
	id := "TOWN-20260807-120000"
	data, _, _ := m223Recording(t, id)
	root := t.TempDir()
	replayDir := filepath.Join(root, "replays")
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		t.Fatalf("mkdir replays: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, id+".jsonl"), data, 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	server := NewWebSocketServer(townWorld(t), 1)
	server.ReplayDir = replayDir
	server.WorldsDir = filepath.Join(root, "worlds-missing")
	if _, err := server.GetOrCreateReplayInstance(id); err == nil || !strings.Contains(err.Error(), "not hosted") {
		t.Fatalf("missing recorded world error=%v, want not-hosted message", err)
	}
}

func TestM223TwoWatchersSeeTheSameReplayFrames(t *testing.T) {
	id := "TOWN-20260807-120000"
	data, finalTick, _ := m223Recording(t, id)
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
		t.Fatalf("write recording: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worldsDir, "TOWN.ZZT"), committedTownBytes(t), 0o644); err != nil {
		t.Fatalf("write TOWN.ZZT: %v", err)
	}

	server, baseURL, ctx := m223Server(t, replayDir, worldsDir)
	connA, snapA := m223WatchReplay(t, ctx, baseURL, id)
	defer connA.Close(websocket.StatusNormalClosure, "")
	connB, snapB := m223WatchReplay(t, ctx, baseURL, id)
	defer connB.Close(websocket.StatusNormalClosure, "")
	screenA := newM221Screen(snapA.Screen)
	screenB := newM221Screen(snapB.Screen)
	if key, bad := screenA.differs(screenB); bad {
		t.Fatalf("initial replay frames differ at cell key %d", key)
	}

	lastHashA := uint64(0)
	lastHashB := uint64(0)
	for tick := 0; tick <= finalTick+1; tick++ {
		server.Tick(ctx)
		typA, snapA, diffA := m221ReadFrame(t, ctx, connA)
		typB, snapB, diffB := m221ReadFrame(t, ctx, connB)
		if typA != typB {
			t.Fatalf("tick %d: watcher message types %q/%q differ", tick, typA, typB)
		}
		switch typA {
		case MessageTypeSnapshot:
			screenA = newM221Screen(snapA.Screen)
			screenB = newM221Screen(snapB.Screen)
			lastHashA = snapA.Hash
			lastHashB = snapB.Hash
		case MessageTypeDiff:
			screenA.apply(diffA.Cells)
			screenB.apply(diffB.Cells)
			lastHashA = diffA.Hash
			lastHashB = diffB.Hash
		default:
			t.Fatalf("tick %d: watcher got %q", tick, typA)
		}
		if key, bad := screenA.differs(screenB); bad {
			t.Fatalf("tick %d: replay watcher screens differ at cell key %d", tick, key)
		}
	}
	if lastHashA == 0 || lastHashA != lastHashB {
		t.Fatalf("final watcher hashes differ: %016x/%016x", lastHashA, lastHashB)
	}
}

func m223Server(t *testing.T, replayDir, worldsDir string) (*WebSocketServer, string, context.Context) {
	t.Helper()
	server := NewWebSocketServer(townWorld(t), 1)
	server.ReplayDir = replayDir
	server.WorldsDir = worldsDir
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return server, "ws" + strings.TrimPrefix(httpServer.URL, "http"), ctx
}

func m223WatchReplay(t *testing.T, ctx context.Context, wsURL, id string) (*websocket.Conn, SnapshotMessage) {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsURL+"?replay="+id, nil)
	if err != nil {
		t.Fatalf("dial replay watcher: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Spectate: true}); err != nil {
		t.Fatalf("write replay watch join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read replay snapshot: %v", err)
	}
	if !snapshot.Spectator {
		t.Fatalf("replay snapshot was not read-only: %+v", snapshot)
	}
	return conn, snapshot
}
