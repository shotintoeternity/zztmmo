package zztgo

// M36.18 — an evicted world comes back with its progress.
//
// After idle eviction GetOrCreateInstance reloaded the pristine .ZZT and
// ignored autosave/<NAME>.SAV, against the M13.3 policy that an autosave beats
// the pristine world; and Autosave skipped rooms with no clients, so the play
// between the last cadence save and the last player leaving was never written.
// Separately, an instance resolved at the socket upgrade could be evicted
// before the join frame arrived, leaving the joiner in an orphan nobody ticks.

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m3618Server hosts BETA out of its worlds directory, with autosave switched
// on but never due on its cadence, so every save these tests see comes from
// the last-leave path.
func m3618Server(t *testing.T) *WebSocketServer {
	t.Helper()
	world := testEmptyWorld(t)
	world.Info.Name = "ALPHA"
	server := NewWebSocketServer(world, 1)
	server.WorldsDir = t.TempDir()
	server.SavesDir = t.TempDir()
	server.AutosaveEveryTicks = 1 << 30
	server.InstanceEvictIdleTicks = 2
	m3618WriteWorld(t, server.WorldsDir, "BETA")
	return server
}

func m3618WriteWorld(t *testing.T, dir, name string) {
	t.Helper()
	beta := testEmptyWorld(t)
	beta.Info.Name = name
	scratch := newSnapshotEngine()
	scratch.World = beta
	var buf bytes.Buffer
	if err := scratch.worldWriteTo(&buf); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".ZZT"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s.ZZT: %v", name, err)
	}
}

// m3618Play joins a player into BETA, leaves ammo at (20,10) on board 1 and
// sets a flag, ticks once with the player in, then leaves.
func m3618Play(t *testing.T, server *WebSocketServer) *WorldInstance {
	t.Helper()
	inst, err := server.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatalf("GetOrCreateInstance(BETA): %v", err)
	}
	inst.mu.Lock()
	player := inst.RoomManager.JoinPlayer(1, 0, 0)
	inst.Clients[player] = &webSocketClient{playerID: player}
	room, ok := inst.RoomManager.Room(1)
	if !ok {
		inst.mu.Unlock()
		t.Fatal("board 1 has no room after join")
	}
	room.Engine.WorldSetFlag("SOLVED")
	// Published to the shared world the way a #set is at the end of a step;
	// otherwise the next tick's world-scope refresh would undo it.
	copyWorldScope(&inst.RoomManager.world.Info, &room.Engine.World.Info)
	room.Engine.Board.Tiles[20][10] = TTile{Element: E_AMMO, Color: ElementDefs[E_AMMO].Color}
	inst.mu.Unlock()

	server.Tick(context.Background())
	server.removeClientFromInstance(inst, player)
	return inst
}

func m3618TickUntilEvicted(t *testing.T, server *WebSocketServer, name string) {
	t.Helper()
	for i := 0; i < 20; i++ {
		server.Tick(context.Background())
		server.mu.Lock()
		gone := server.Instances[name] == nil
		server.mu.Unlock()
		if gone {
			return
		}
	}
	t.Fatalf("%s was never evicted", name)
}

func m3618AssertProgress(t *testing.T, world TWorld, want bool) {
	t.Helper()
	board := openSnapshotBoard(t, world, 1)
	got := board.Board.Tiles[20][10].Element == E_AMMO
	if got != want {
		t.Errorf("ammo at (20,10) present=%v, want %v", got, want)
	}
	flag := false
	for _, f := range world.Info.Flags {
		if strings.EqualFold(f, "SOLVED") {
			flag = true
		}
	}
	if flag != want {
		t.Errorf("flag SOLVED present=%v, want %v", flag, want)
	}
}

// The DoD: play, leave, evict, rejoin, and the state is still there.
func TestM3618EvictedWorldComesBackWithItsProgress(t *testing.T) {
	server := m3618Server(t)
	first := m3618Play(t, server)
	m3618TickUntilEvicted(t, server, "BETA")

	again, err := server.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatalf("GetOrCreateInstance(BETA) after eviction: %v", err)
	}
	if again == first {
		t.Fatal("eviction did not drop the instance")
	}
	again.mu.Lock()
	world := again.RoomManager.FrozenWorld()
	again.mu.Unlock()
	m3618AssertProgress(t, world, true)

	// The source world (title screen, resets) stays the pristine one, as the
	// boot-time restore leaves it.
	m3618AssertProgress(t, again.SourceWorld, false)
}

// The last player leaving is saved promptly, not at the next cadence and not
// only at eviction: the file is on disk the tick after they go.
func TestM3618LastLeaveIsAutosaved(t *testing.T) {
	server := m3618Server(t)
	server.InstanceEvictIdleTicks = 1 << 30
	m3618Play(t, server)
	path := filepath.Join(server.SavesDir, "autosave", "BETA.SAV")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("autosave written while the player was still in (test setup)")
	}

	server.Tick(context.Background())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no autosave the tick after the last player left: %v", err)
	}
	scratch := newSnapshotEngine()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := scratch.worldReadFrom(f, false, nil); err != nil {
		t.Fatal(err)
	}
	m3618AssertProgress(t, scratch.World, true)

	// And once written, an empty room is not written again every tick.
	info, _ := os.Stat(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	server.Tick(context.Background())
	server.Tick(context.Background())
	if _, err := os.Stat(path); err == nil {
		t.Errorf("an unchanged empty room was autosaved again (first write %v)", info.ModTime())
	}
}

// -autosave 0 still disables autosave: no last-leave write.
func TestM3618NoLastLeaveSaveWhenAutosaveIsOff(t *testing.T) {
	server := m3618Server(t)
	server.AutosaveEveryTicks = 0
	m3618Play(t, server)
	m3618TickUntilEvicted(t, server, "BETA")
	if _, err := os.Stat(filepath.Join(server.SavesDir, "autosave", "BETA.SAV")); err == nil {
		t.Fatal("autosave written with autosave disabled")
	}
}

// -fresh still means fresh: an autosave left by an earlier boot is not loaded
// on recreate unless the boot restored autosaves. One written by this process
// always is.
func TestM3618FreshBootIgnoresAnEarlierBootsAutosave(t *testing.T) {
	earlier := m3618Server(t)
	m3618Play(t, earlier)
	m3618TickUntilEvicted(t, earlier, "BETA")

	fresh := NewWebSocketServer(testEmptyWorld(t), 1)
	fresh.WorldsDir = earlier.WorldsDir
	fresh.SavesDir = earlier.SavesDir
	fresh.AutosaveEveryTicks = 1 << 30
	inst, err := fresh.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatal(err)
	}
	inst.mu.Lock()
	world := inst.RoomManager.FrozenWorld()
	inst.mu.Unlock()
	m3618AssertProgress(t, world, false)

	restoring := NewWebSocketServer(testEmptyWorld(t), 1)
	restoring.WorldsDir = earlier.WorldsDir
	restoring.SavesDir = earlier.SavesDir
	restoring.RestoreAutosaves()
	inst, err = restoring.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatal(err)
	}
	inst.mu.Lock()
	world = inst.RoomManager.FrozenWorld()
	inst.mu.Unlock()
	m3618AssertProgress(t, world, true)
}

// A republish replaces the world, so the old world's autosave must not come
// back over it after the next eviction.
func TestM3618RepublishDropsTheStaleAutosave(t *testing.T) {
	server := m3618Server(t)
	m3618Play(t, server)
	m3618TickUntilEvicted(t, server, "BETA")
	path := filepath.Join(server.SavesDir, "autosave", "BETA.SAV")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: no autosave: %v", err)
	}

	fresh := testEmptyWorld(t)
	fresh.Info.Name = "BETA"
	if err := server.HostGeneratedWorld("BETA", fresh); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the republished world kept the old world's autosave")
	}
}

// The join race: the instance is resolved at the upgrade, the join frame comes
// later. If the instance is evicted in between, the joiner must land in the
// live instance, not an orphan.
func TestM3618JoinAfterEvictionLandsInTheLiveInstance(t *testing.T) {
	for _, spectate := range []bool{false, true} {
		server := m3618Server(t)
		server.InstanceEvictIdleTicks = 1
		app := httptest.NewServer(server)
		wsURL := "ws" + strings.TrimPrefix(app.URL, "http") + "/?world=BETA"

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		conn.SetReadLimit(ServerReadLimit)
		server.mu.Lock()
		orphan := server.Instances["BETA"]
		server.mu.Unlock()
		if orphan == nil {
			t.Fatal("upgrade did not resolve BETA")
		}
		m3618TickUntilEvicted(t, server, "BETA")

		if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "late", Spectate: spectate}); err != nil {
			t.Fatalf("write join: %v", err)
		}
		var snapshot SnapshotMessage
		if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
			t.Fatalf("read snapshot (spectate=%v): %v", spectate, err)
		}

		server.mu.Lock()
		live := server.Instances["BETA"]
		server.mu.Unlock()
		if live == nil || live == orphan {
			t.Fatalf("spectate=%v: joiner is in an evicted instance (live=%p orphan=%p)", spectate, live, orphan)
		}
		live.mu.Lock()
		n := len(live.Clients)
		if spectate {
			n = len(live.Spectators)
		}
		live.mu.Unlock()
		if n != 1 {
			t.Errorf("spectate=%v: live instance holds %d joiners, want 1", spectate, n)
		}
		orphan.mu.Lock()
		stray := len(orphan.Clients) + len(orphan.Spectators)
		orphan.mu.Unlock()
		if stray != 0 {
			t.Errorf("spectate=%v: evicted instance holds %d joiners", spectate, stray)
		}

		_ = conn.Close(websocket.StatusNormalClosure, "")
		cancel()
		app.Close()
	}
}

// Most players leave by closing the tab, not by quitting: the connection
// drops, the reconnect grace runs out, and expireDetached removes them. That
// path is saved too, before the instance can go.
func TestM3618TabCloseLeaveIsAutosavedBeforeEviction(t *testing.T) {
	server := m3618Server(t)
	inst, err := server.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatal(err)
	}
	inst.mu.Lock()
	player := inst.RoomManager.JoinPlayer(1, 0, 0)
	client := &webSocketClient{playerID: player}
	inst.Clients[player] = client
	inst.mintResumeTokenLocked(player)
	room, _ := inst.RoomManager.Room(1)
	room.Engine.Board.Tiles[20][10] = TTile{Element: E_AMMO, Color: ElementDefs[E_AMMO].Color}
	inst.mu.Unlock()

	server.handleReadLoopExit(inst, client, player)
	// One tick of the grace with the stat still in the room, then run the rest
	// out without ticking through all of it.
	server.Tick(context.Background())
	inst.mu.Lock()
	inst.Detached[player] = 1
	inst.mu.Unlock()

	path := filepath.Join(server.SavesDir, "autosave", "BETA.SAV")
	for i := 0; i < 20; i++ {
		server.Tick(context.Background())
		server.mu.Lock()
		gone := server.Instances["BETA"] == nil
		server.mu.Unlock()
		if _, err := os.Stat(path); err == nil {
			break
		}
		if gone {
			t.Fatal("instance evicted before its last player's play was autosaved")
		}
	}
	m3618TickUntilEvicted(t, server, "BETA")
	again, err := server.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatal(err)
	}
	again.mu.Lock()
	world := again.RoomManager.FrozenWorld()
	again.mu.Unlock()
	if openSnapshotBoard(t, world, 1).Board.Tiles[20][10].Element != E_AMMO {
		t.Error("play before a tab-close leave was lost across eviction")
	}
}

// A private editor test-play copy is never autosaved: nothing could load it.
func TestM3618TestPlayCopyIsNotAutosaved(t *testing.T) {
	server := m3618Server(t)
	if err := server.hostGeneratedWorld("TPCOPY", testEmptyWorld(t), true); err != nil {
		t.Fatal(err)
	}
	inst := server.Instances["TPCOPY"]
	inst.mu.Lock()
	player := inst.RoomManager.JoinPlayer(1, 0, 0)
	inst.Clients[player] = &webSocketClient{playerID: player}
	inst.mu.Unlock()
	server.Tick(context.Background())
	server.removeClientFromInstance(inst, player)
	m3618TickUntilEvicted(t, server, "TPCOPY")
	if _, err := os.Stat(filepath.Join(server.SavesDir, "autosave", "TPCOPY.SAV")); err == nil {
		t.Fatal("a private test-play copy was autosaved")
	}
}
