package zztgo

// M36.15 — restore is locked and stays in its own world.
//
// handleRestore swapped a hosted world's RoomManager contents without inst.mu,
// so it raced the tick goroutine and every join on the same instance; and it
// restored any save into any world, so `{world:"LOBBY", name:<a CAVES save>}`
// loaded CAVES into LOBBY, where autosave then made it permanent. The same
// unlocked reads sat in handleTitle, handleHighScores and the join/watch board
// resolution. The race test below only proves anything under -race:
//
//	go test -race -run TestM3615 ./...

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m3615Server hosts ALPHA as its default world and BETA out of its worlds
// directory, with one saves directory shared by the socket server and the API,
// as cmd/zztmmo wires them.
func m3615Server(t *testing.T) (*WebSocketServer, *WebAPI, string) {
	t.Helper()
	world := testEmptyWorld(t)
	world.Info.Name = "ALPHA"
	server := NewWebSocketServer(world, 1)
	server.WorldsDir = t.TempDir()
	saves := t.TempDir()
	server.SavesDir = saves

	beta := testEmptyWorld(t)
	beta.Info.Name = "BETA"
	scratch := newSnapshotEngine()
	scratch.World = beta
	var buf bytes.Buffer
	if err := scratch.worldWriteTo(&buf); err != nil {
		t.Fatalf("write BETA: %v", err)
	}
	if err := os.WriteFile(filepath.Join(server.WorldsDir, "BETA.ZZT"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write BETA.ZZT: %v", err)
	}

	api := &WebAPI{RoomManager: server.RoomManager, World: world, Server: server, SavesDir: saves}
	return server, api, saves
}

// m3615Save writes dir/<name>.SAV from world, the way a player's 'S' does: a
// player joins, saves and leaves. It runs before any goroutine is started.
func m3615Save(t *testing.T, server *WebSocketServer, world, name string) {
	t.Helper()
	inst, err := server.GetOrCreateInstance(world)
	if err != nil {
		t.Fatalf("GetOrCreateInstance(%s): %v", world, err)
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	player := inst.RoomManager.JoinPlayer(1, 0, 0)
	if _, err := inst.RoomManager.SaveSnapshot(server.SavesDir, name, player); err != nil {
		t.Fatalf("SaveSnapshot(%s): %v", name, err)
	}
	inst.RoomManager.LeavePlayer(player)
}

func m3615PostRestore(client *http.Client, base, world, name string) (int, string, error) {
	body, _ := json.Marshal(map[string]string{"world": world, "name": name})
	resp, err := client.Post(base+"/api/restore", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(out)), nil
}

// A restore runs while the world ticks, players join it naming no board (so
// resolveJoinBoard reads the world), watchers open it, and the title and
// high-score routes read it. Under -race, any of those touching the
// RoomManager outside inst.mu is reported.
func TestM3615RestoreIsLockedAgainstTicksAndJoins(t *testing.T) {
	server, api, _ := m3615Server(t)
	m3615Save(t, server, "ALPHA", "ALPHA1")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runServerAsync(t, ctx, server)

	wsApp := httptest.NewServer(server)
	defer wsApp.Close()
	apiApp := httptest.NewServer(api.Handler())
	defer apiApp.Close()
	wsURL := "ws" + strings.TrimPrefix(wsApp.URL, "http")

	var restored atomic.Int32
	var stop atomic.Bool
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	fail := func(format string) {
		select {
		case errs <- format:
		default:
		}
	}

	// Restores, until the joiners are done. 200 (nobody in) and 409 (somebody
	// in) are both right; anything else is not.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			code, body, err := m3615PostRestore(apiApp.Client(), apiApp.URL, "ALPHA", "ALPHA1")
			switch {
			case err != nil:
				fail("restore: " + err.Error())
				return
			case code == http.StatusOK:
				restored.Add(1)
			case code == http.StatusConflict:
			default:
				fail("restore answered " + http.StatusText(code) + ": " + body)
				return
			}
		}
	}()

	// The title and high-score routes read the same world.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			for _, route := range []string{"/api/title?world=ALPHA", "/api/highscores?world=ALPHA"} {
				resp, err := apiApp.Client().Get(apiApp.URL + route)
				if err != nil {
					fail(route + ": " + err.Error())
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					fail(route + " answered " + http.StatusText(resp.StatusCode))
					return
				}
			}
		}
	}()

	// Let some restores land on a ticking, empty world before anyone joins.
	deadline := time.Now().Add(10 * time.Second)
	for restored.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if restored.Load() < 3 {
		stop.Store(true)
		wg.Wait()
		t.Fatalf("only %d restores succeeded on an empty world", restored.Load())
	}

	// Watchers, then players, each naming board 0 so the world's own current
	// board is read to place them. Watchers first: they are not players, so
	// restores keep succeeding (and keep replacing the world) while they join.
	// The first player turns every later restore into a 409, because a dropped
	// player's stat lingers for the reconnect grace.
	for i := 0; i < 26; i++ {
		for _, spectate := range []bool{i < 20} {
			conn, _, err := websocket.Dial(ctx, wsURL, nil)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			conn.SetReadLimit(ServerReadLimit)
			if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "racer", Spectate: spectate}); err != nil {
				t.Fatalf("write join: %v", err)
			}
			var snapshot SnapshotMessage
			if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
				t.Fatalf("read snapshot (spectate=%v): %v", spectate, err)
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
	}

	stop.Store(true)
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// A save of BETA restored into ALPHA is refused, and ALPHA is left as it was.
func TestM3615RestoreRefusesASaveFromAnotherWorld(t *testing.T) {
	server, api, _ := m3615Server(t)
	m3615Save(t, server, "BETA", "BETA1")
	apiApp := httptest.NewServer(api.Handler())
	defer apiApp.Close()

	before := server.DefaultInstance.RoomManager.FrozenWorld().Info.Name
	code, body, err := m3615PostRestore(apiApp.Client(), apiApp.URL, "ALPHA", "BETA1")
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusConflict {
		t.Fatalf("restore of a BETA save into ALPHA = %d (%s), want 409", code, body)
	}
	if !strings.Contains(body, "another world") {
		t.Errorf("refusal said %q, want it to say the save is from another world", body)
	}
	if got := server.DefaultInstance.RoomManager.FrozenWorld().Info.Name; got != before {
		t.Fatalf("ALPHA's world is now %q after a refused restore, want %q", got, before)
	}

	// The same save still restores into its own world.
	if code, body, _ := m3615PostRestore(apiApp.Client(), apiApp.URL, "BETA", "BETA1"); code != http.StatusOK {
		t.Fatalf("restore of BETA1 into BETA = %d (%s), want 200", code, body)
	}
}

// The restore and load-world bodies are bounded, like every other JSON route.
func TestM3615RestoreAndLoadWorldBodiesAreBounded(t *testing.T) {
	_, api, _ := m3615Server(t)
	apiApp := httptest.NewServer(api.Handler())
	defer apiApp.Close()

	huge := `{"world":"ALPHA","name":"` + strings.Repeat("A", 1<<20) + `"}`
	for _, route := range []string{"/api/restore", "/api/loadworld"} {
		resp, err := apiApp.Client().Post(apiApp.URL+route, "application/json", strings.NewReader(huge))
		if err != nil {
			t.Fatalf("POST %s: %v", route, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "bad request body") {
			t.Errorf("POST %s with a 1 MiB body = %d (%s), want 400 bad request body", route, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}
