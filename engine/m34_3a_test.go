package zztgo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// M34.3a: an unnamed title fetch used to be answered from a hardcoded "TOWN".
// M29.1 made LOBBY the server's default world and left that name behind, so the
// client's first paint — main.ts sends a bare /api/title while its world is
// still "Untitled" — asked for a world the deployment may not host, and took a
// 500 where a title board belongs. These tests host NO TOWN anywhere, so the
// old default cannot resolve by accident.

type m343aTitleResponse struct {
	World    string       `json:"world"`
	Filename string       `json:"filename"`
	Screen   []ScreenCell `json:"screen"`
}

// m343aServer is a server whose default world is deliberately not TOWN, hosting
// out of an empty worlds directory: nothing but the default instance can be
// resolved, from memory or from disk.
func m343aServer(t *testing.T) (*WebSocketServer, *httptest.Server) {
	t.Helper()
	world := testEmptyWorld(t)
	world.Info.Name = "ALPHA"

	server := NewWebSocketServer(world, 1)
	server.WorldsDir = t.TempDir()
	api := &WebAPI{RoomManager: server.RoomManager, World: world, Server: server}
	httpServer := httptest.NewServer(api.Handler())
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func TestM343AUnnamedTitleAnswersFromTheServersOwnDefaultWorld(t *testing.T) {
	server, app := m343aServer(t)

	resp, err := app.Client().Get(app.URL + "/api/title")
	if err != nil {
		t.Fatalf("GET /api/title: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unnamed /api/title status=%d body=%q, want the default world's title board", resp.StatusCode, body)
	}
	var title m343aTitleResponse
	if err := json.NewDecoder(resp.Body).Decode(&title); err != nil {
		t.Fatalf("decode title: %v", err)
	}
	if title.Filename != server.DefaultInstance.Name {
		t.Fatalf("unnamed /api/title filename = %q, want the server's default world %q", title.Filename, server.DefaultInstance.Name)
	}
	if len(title.Screen) == 0 {
		t.Fatal("unnamed /api/title painted no cells")
	}

	// The default resolves to the instance the server is already running, not to
	// a fresh load off disk: DefaultInstance.Name is the key Instances is stored
	// under, and the unnamed path has to sanitize to that same key. If it did
	// not, a second instance of the same world would appear here.
	server.mu.Lock()
	instances := len(server.Instances)
	same := server.Instances[title.Filename] == server.DefaultInstance
	server.mu.Unlock()
	if instances != 1 || !same {
		t.Fatalf("unnamed /api/title left %d instances and resolved the default instance = %v; want the one it booted with", instances, same)
	}
}

func TestM343AUnnamedTitleStreamAnswersFromTheServersOwnDefaultWorld(t *testing.T) {
	_, app := m343aServer(t)

	resp, err := app.Client().Get(app.URL + "/api/title/stream")
	if err != nil {
		t.Fatalf("GET /api/title/stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unnamed /api/title/stream status=%d body=%q, want the default world's stream", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("unnamed /api/title/stream content-type=%q, want text/event-stream", got)
	}
}

// A world nobody hosts is the client asking for something absent, not a server
// fault. Both title paths say so, with a message.
func TestM343AAbsentWorldIsANotFoundWithAMessage(t *testing.T) {
	_, app := m343aServer(t)

	for _, path := range []string{"/api/title?world=NOSUCH", "/api/title/stream?world=NOSUCH"} {
		resp, err := app.Client().Get(app.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status=%d body=%q, want 404", path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "NOSUCH") {
			t.Fatalf("GET %s said %q, want a message naming the world", path, body)
		}
	}
}

// The literal DoD case: a deployment that does not host the world it was
// started with. The boot instance is seeded into Instances, so the only way to
// reach it is to take that instance away — which is what a host missing the
// file would look like to every later request.
func TestM343AAbsentDefaultWorldIsAMessageRatherThanA500(t *testing.T) {
	server, app := m343aServer(t)
	server.mu.Lock()
	delete(server.Instances, server.DefaultInstance.Name)
	server.mu.Unlock()

	resp, err := app.Client().Get(app.URL + "/api/title")
	if err != nil {
		t.Fatalf("GET /api/title: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unhosted default world answered status=%d body=%q, want 404", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), server.DefaultInstance.Name) {
		t.Fatalf("unhosted default world said %q, want a message naming %q", body, server.DefaultInstance.Name)
	}
}
