package zztgo

import (
	"os"
	"path/filepath"
	"testing"
)

// M13.3 — autosave and restore-on-boot. Nothing snapshots automatically before
// this, so a crash or restart loses every live room. These tests drive the two
// seams directly: Autosave (write) and RestoreAutosaves (boot).

// occupyDefaultInstance joins a player and marks the default instance occupied by
// registering a client for them, so Autosave treats the room as live without a
// real WebSocket. Autosave never writes to the client, so a bare struct is safe.
func occupyDefaultInstance(t *testing.T, server *WebSocketServer) (PlayerID, *Room) {
	t.Helper()
	inst := server.DefaultInstance
	playerID := inst.RoomManager.JoinPlayer(1, 0, 0)
	inst.mu.Lock()
	inst.Clients[playerID] = &webSocketClient{playerID: playerID}
	inst.mu.Unlock()
	room, ok := inst.RoomManager.Room(1)
	if !ok {
		t.Fatal("board 1 has no room after join")
	}
	return playerID, room
}

// The whole round trip: an occupied room autosaves, the process restarts (a fresh
// server over the same directories), restore-on-boot brings the room back with
// its board contents and flags, and the players are dropped from the snapshot.
func TestM133AutosaveRestoreRoundTrip(t *testing.T) {
	saves := t.TempDir()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves

	_, room := occupyDefaultInstance(t, server)

	// Puzzle progress: a flag one player set, and a change to the board.
	room.Engine.WorldSetFlag("SOLVED")
	room.Engine.Board.Tiles[20][10] = TTile{Element: E_AMMO, Color: ElementDefs[E_AMMO].Color}

	server.Autosave()

	// The file lands under SavesDir/autosave/<INSTANCENAME>.SAV.
	name, err := SanitizeSaveName(server.DefaultInstance.Name)
	if err != nil {
		t.Fatalf("default instance name %q is not save-able: %v", server.DefaultInstance.Name, err)
	}
	autosavePath := filepath.Join(saves, "autosave", name+".SAV")
	if _, err := os.Stat(autosavePath); err != nil {
		t.Fatalf("autosave file: %v", err)
	}

	// The process restarts: a fresh server over the same directories.
	restored := NewWebSocketServer(testEmptyWorld(t), 1)
	restored.SavesDir = saves
	restored.RestoreAutosaves()

	world := restored.DefaultInstance.RoomManager.FrozenWorld()
	board := openSnapshotBoard(t, world, 1)
	if got := countPlayerTiles(board); got != 0 {
		t.Errorf("restored board holds %d player tiles, want 0 (players are dropped)", got)
	}
	if board.Board.Tiles[20][10].Element != E_AMMO {
		t.Errorf("board contents lost: (20,10) = %v, want E_AMMO", board.Board.Tiles[20][10].Element)
	}

	// Autosave has no saver, so the vanilla one-player inventory fields are zero.
	if world.Info.Health != 0 || world.Info.Score != 0 {
		t.Errorf("autosave wrote a saver's inventory: health=%d score=%d, want 0/0",
			world.Info.Health, world.Info.Score)
	}

	// A joiner sees the restored world: the flag and the ammo survived.
	playerB := restored.DefaultInstance.RoomManager.JoinPlayer(1, 0, 0)
	room2, ok := restored.DefaultInstance.RoomManager.Room(1)
	if !ok {
		t.Fatal("restored board 1 has no room")
	}
	if room2.Engine.Board.Tiles[20][10].Element != E_AMMO {
		t.Error("the ammo on the board did not survive the autosave round trip")
	}
	if room2.Engine.WorldGetFlagPosition("SOLVED") < 0 {
		t.Error("flag SOLVED did not survive the autosave round trip")
	}
	// B joins fresh, exactly as they would any running world (M4.3a decision 1).
	stateB, ok := restored.DefaultInstance.RoomManager.PlayerState(playerB)
	if !ok {
		t.Fatal("B has no state")
	}
	if stateB.Score != 0 || stateB.Health != 100 {
		t.Errorf("B joined with score %d health %d, want a fresh 0/100", stateB.Score, stateB.Health)
	}
}

// An empty room has nothing new to say and is not autosaved.
func TestM133AutosaveSkipsEmptyRooms(t *testing.T) {
	saves := t.TempDir()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves

	server.Autosave()

	if entries, _ := os.ReadDir(filepath.Join(saves, "autosave")); len(entries) != 0 {
		t.Errorf("an unoccupied server wrote %d autosave files, want 0", len(entries))
	}
}

// The tick-loop cadence fires Autosave every AutosaveEveryTicks ticks, counted
// off the tick clock. maybeAutosave is the tick-loop seam; driving it directly
// keeps the test deterministic and free of the network write path.
func TestM133AutosaveTickCadence(t *testing.T) {
	saves := t.TempDir()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves
	server.AutosaveEveryTicks = 3

	occupyDefaultInstance(t, server)

	name, _ := SanitizeSaveName(server.DefaultInstance.Name)
	autosavePath := filepath.Join(saves, "autosave", name+".SAV")

	server.maybeAutosave() // tick 1
	server.maybeAutosave() // tick 2
	if _, err := os.Stat(autosavePath); !os.IsNotExist(err) {
		t.Fatalf("autosave fired before its cadence (tick 2): stat err = %v", err)
	}
	server.maybeAutosave() // tick 3: due
	if _, err := os.Stat(autosavePath); err != nil {
		t.Fatalf("autosave did not fire at its cadence (tick 3): %v", err)
	}
}

// AutosaveEveryTicks == 0 disables the cadence entirely.
func TestM133AutosaveDisabledByZeroCadence(t *testing.T) {
	saves := t.TempDir()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves
	server.AutosaveEveryTicks = 0

	occupyDefaultInstance(t, server)

	for i := 0; i < 100; i++ {
		server.maybeAutosave()
	}
	if entries, _ := os.ReadDir(filepath.Join(saves, "autosave")); len(entries) != 0 {
		t.Errorf("a zero cadence still wrote %d autosave files, want 0", len(entries))
	}
}

// A corrupt or truncated autosave is skipped with a log line, never a boot
// failure, and the pristine world is used instead.
func TestM133CorruptAutosaveSkipped(t *testing.T) {
	saves := t.TempDir()
	autosaveDir := filepath.Join(saves, "autosave")
	if err := os.MkdirAll(autosaveDir, 0o755); err != nil {
		t.Fatal(err)
	}

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves

	// A name that matches the hostable default world, so restore is attempted.
	name, _ := SanitizeSaveName(server.DefaultInstance.Name)
	if err := os.WriteFile(filepath.Join(autosaveDir, name+".SAV"), []byte("not a world"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Must not panic or fail the boot.
	server.RestoreAutosaves()

	// The default instance is still usable on its pristine world: a join succeeds
	// and the board is not the corrupt file's contents.
	playerID := server.DefaultInstance.RoomManager.JoinPlayer(1, 0, 0)
	if _, ok := server.DefaultInstance.RoomManager.PlayerState(playerID); !ok {
		t.Error("a join failed after a corrupt autosave was skipped")
	}
}

// -fresh is a boot-time gate: it simply does not call RestoreAutosaves. Prove the
// mechanism — an autosave present, but a server that skips RestoreAutosaves keeps
// the pristine world, while one that calls it picks the autosave up.
func TestM133FreshSkipsRestore(t *testing.T) {
	saves := t.TempDir()

	// Write a real autosave with a distinctive board change.
	seed := NewWebSocketServer(testEmptyWorld(t), 1)
	seed.SavesDir = saves
	_, room := occupyDefaultInstance(t, seed)
	room.Engine.Board.Tiles[20][10] = TTile{Element: E_AMMO, Color: ElementDefs[E_AMMO].Color}
	seed.Autosave()

	// -fresh: never call RestoreAutosaves. The pristine world stands.
	fresh := NewWebSocketServer(testEmptyWorld(t), 1)
	fresh.SavesDir = saves
	freshBoard := openSnapshotBoard(t, fresh.DefaultInstance.RoomManager.FrozenWorld(), 1)
	if freshBoard.Board.Tiles[20][10].Element == E_AMMO {
		t.Error("-fresh path still picked up the autosave; restore is supposed to be opt-in")
	}

	// The default (no -fresh): RestoreAutosaves brings the autosave back.
	booted := NewWebSocketServer(testEmptyWorld(t), 1)
	booted.SavesDir = saves
	booted.RestoreAutosaves()
	bootedBoard := openSnapshotBoard(t, booted.DefaultInstance.RoomManager.FrozenWorld(), 1)
	if bootedBoard.Board.Tiles[20][10].Element != E_AMMO {
		t.Error("restore-on-boot did not pick up the autosave")
	}
}

// Saving is disabled without a -saves directory: no autosave dir, no writes.
func TestM133AutosaveDisabledWithoutSavesDir(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	// SavesDir left empty.
	occupyDefaultInstance(t, server)
	server.Autosave()        // no panic, no directory to write to
	server.RestoreAutosaves() // no-op
}
