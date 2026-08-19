package zztgo

// M34.3b — the same stale TOWN default, in the two handlers M34.3a's DoD did
// not name. `handleRestore` and `handleHighScores` each defaulted an absent
// world to a hardcoded "TOWN" and answered a world nobody hosts with a 500 that
// pasted the failed absolute path into the body. The shipped client always
// names the world at both call sites (main.ts:2100,4461), so this was a latent
// copy of a fixed bug rather than a live one — reachable by a direct API caller.
//
// These tests run against a server whose default world is deliberately not
// TOWN, hosting out of an empty worlds directory, so the old default cannot
// resolve by accident: putting "TOWN" back reddens them by construction.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// m343bServer is m343aServer plus a saves directory, so a restore reaches the
// snapshot stage instead of stopping at ErrSavesDisabled — the point being to
// see which stage the request dies at.
func m343bServer(t *testing.T) (*WebSocketServer, *httptest.Server) {
	t.Helper()
	world := testEmptyWorld(t)
	world.Info.Name = "ALPHA"

	server := NewWebSocketServer(world, 1)
	server.WorldsDir = t.TempDir()
	api := &WebAPI{RoomManager: server.RoomManager, World: world, Server: server, SavesDir: t.TempDir()}
	httpServer := httptest.NewServer(api.Handler())
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func m343bPostRestore(t *testing.T, app *httptest.Server, body string) (int, string) {
	t.Helper()
	resp, err := app.Client().Post(app.URL+"/api/restore", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /api/restore %s: %v", body, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// An unnamed high-score request resolves the server's own default world. Under
// the old hardcoded "TOWN" this server — which hosts no TOWN anywhere — took a
// 500 where a high-score table belongs.
func TestM343BUnnamedHighScoresAnswerFromTheServersOwnDefaultWorld(t *testing.T) {
	server, app := m343bServer(t)

	resp, err := app.Client().Get(app.URL + "/api/highscores")
	if err != nil {
		t.Fatalf("GET /api/highscores: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/highscores status=%d body=%q, want 200", resp.StatusCode, body)
	}

	var got struct {
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if !strings.Contains(got.Title, server.DefaultInstance.Name) {
		t.Fatalf("title = %q, want the server's own default world %q", got.Title, server.DefaultInstance.Name)
	}
}

// The same for restore. The world resolves, so the request now dies at the
// SNAPSHOT stage — "no such saved game" — rather than at the world stage, which
// is the whole difference between the old handler and this one.
func TestM343BUnnamedRestoreAnswersFromTheServersOwnDefaultWorld(t *testing.T) {
	_, app := m343bServer(t)

	status, body := m343bPostRestore(t, app, `{"name":"NOSAVE"}`)
	if status != http.StatusNotFound {
		t.Fatalf("POST /api/restore status=%d body=%q, want 404", status, body)
	}
	if !strings.Contains(body, "no such saved game") {
		t.Fatalf("POST /api/restore said %q, want the request to have reached the snapshot stage", body)
	}
}

// A world nobody hosts is the caller asking for something absent, not a server
// fault: 404 with a message naming it, and never the failed absolute path.
func TestM343BAbsentWorldIsANotFoundNamingIt(t *testing.T) {
	_, app := m343bServer(t)

	resp, err := app.Client().Get(app.URL + "/api/highscores?world=NOSUCH")
	if err != nil {
		t.Fatalf("GET /api/highscores?world=NOSUCH: %v", err)
	}
	hsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	hsStatus := resp.StatusCode

	restoreStatus, restoreBody := m343bPostRestore(t, app, `{"world":"NOSUCH","name":"whatever"}`)

	for _, tc := range []struct {
		route  string
		status int
		body   string
	}{
		{"/api/highscores?world=NOSUCH", hsStatus, string(hsBody)},
		{"/api/restore {world:NOSUCH}", restoreStatus, restoreBody},
	} {
		if tc.status != http.StatusNotFound {
			t.Errorf("%s status=%d body=%q, want 404", tc.route, tc.status, tc.body)
		}
		if !strings.Contains(tc.body, "NOSUCH") {
			t.Errorf("%s said %q, want a message naming the world", tc.route, tc.body)
		}
		if strings.Contains(tc.body, ".ZZT") || strings.Contains(tc.body, "/") {
			t.Errorf("%s pasted a path into the answer: %q", tc.route, tc.body)
		}
	}
}
