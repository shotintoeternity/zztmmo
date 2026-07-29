package zztgo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestM1711WorldsAPIReportsLiveEditingOccupancy is the M17.11 server claim:
// /api/worlds reports editing occupancy per world beside playing occupancy, the
// two never being confused for each other, and both are live — they rise as
// people arrive and fall as they leave, rather than being join-time snapshots.
//
// The counts are also read while the sessions churn, which is the data race
// MemberCount and EditorCounts exist to prevent: `go test -race` fails here if
// anything ever reads EditorSession.Members outside its mutex.
func TestM1711WorldsAPIReportsLiveEditingOccupancy(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"TOWN", "OTHER"} {
		// handleWorlds only lists these; nothing loads them.
		if err := os.WriteFile(filepath.Join(dir, name+".ZZT"), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	world := testEmptyWorld(t)
	server := NewWebSocketServer(world, 1)
	server.WorldsDir = dir
	api := &WebAPI{RoomManager: server.RoomManager, Server: server}
	handler := api.Handler()

	occupancy := func(t *testing.T) map[string]WorldListEntry {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/worlds", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("/api/worlds status=%d, want 200", recorder.Code)
		}
		var body struct {
			Worlds []WorldListEntry `json:"worlds"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode /api/worlds: %v (%s)", err, recorder.Body.String())
		}
		byWorld := make(map[string]WorldListEntry, len(body.Worlds))
		for _, entry := range body.Worlds {
			byWorld[entry.World] = entry
		}
		return byWorld
	}

	// A quiet server reports nothing anywhere.
	for name, entry := range occupancy(t) {
		if entry.Players != 0 || entry.Editors != 0 {
			t.Fatalf("quiet %s = {Players:%d Editors:%d}, want both zero", name, entry.Players, entry.Editors)
		}
	}

	// One player in TOWN, two editors in TOWN, one editor in OTHER.
	inst := server.DefaultInstance
	inst.mu.Lock()
	inst.Clients[PlayerID(1)] = &webSocketClient{playerID: 1}
	inst.mu.Unlock()

	townSession := server.editorSessionForWorld("TOWN", world)
	alice, bob := &webSocketClient{}, &webSocketClient{}
	if _, err := townSession.EnterNamed(alice, "Alice"); err != nil {
		t.Fatalf("Alice enter: %v", err)
	}
	if _, err := townSession.EnterNamed(bob, "Bob"); err != nil {
		t.Fatalf("Bob enter: %v", err)
	}
	otherSession := server.editorSessionForWorld("OTHER", world)
	carol := &webSocketClient{}
	if _, err := otherSession.EnterNamed(carol, "Carol"); err != nil {
		t.Fatalf("Carol enter: %v", err)
	}

	entries := occupancy(t)
	if got := entries["TOWN"]; got.Players != 1 || got.Editors != 2 {
		t.Fatalf("TOWN = {Players:%d Editors:%d}, want {1 2}", got.Players, got.Editors)
	}
	if got := entries["OTHER"]; got.Players != 0 || got.Editors != 1 {
		t.Fatalf("OTHER = {Players:%d Editors:%d}, want {0 1}", got.Players, got.Editors)
	}

	// Counts are live: a departing editor is gone from the next listing.
	townSession.Exit(bob)
	if got := occupancy(t)["TOWN"]; got.Editors != 1 {
		t.Fatalf("TOWN editors after one left = %d, want 1", got.Editors)
	}

	// Concurrent access: churn the session while the API reads it.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			member := &webSocketClient{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := townSession.EnterNamed(member, "churn"); err != nil {
					return
				}
				townSession.Exit(member)
			}
		}()
	}
	for i := 0; i < 50; i++ {
		counts := server.EditorCounts()
		// Alice never leaves, so TOWN can never drop below her.
		if counts["TOWN"] < 1 {
			close(stop)
			wg.Wait()
			t.Fatalf("TOWN editor count = %d during churn, want at least 1", counts["TOWN"])
		}
		_ = occupancy(t)
	}
	close(stop)
	wg.Wait()

	// Everyone leaves: both counts vanish from the JSON rather than reading zero.
	townSession.Exit(alice)
	otherSession.Exit(carol)
	inst.mu.Lock()
	delete(inst.Clients, PlayerID(1))
	inst.mu.Unlock()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/worlds", nil))
	body := recorder.Body.String()
	if strings.Contains(body, `"editors":`) || strings.Contains(body, `"players":`) {
		t.Fatalf("emptied server should omit both counts, got %s", body)
	}
}
