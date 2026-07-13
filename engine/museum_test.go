package zztgo

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestMuseumServiceSearchDedupesFields(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/files" {
			t.Fatalf("path=%q, want /search/files", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"SUCCESS",
			"count":1,
			"data":{"results":[{
				"letter":"t",
				"filename":"teen.zip",
				"title":"Teen Priest",
				"author":["Draco"],
				"release_date":"1998-08-31",
				"genres":["Adventure"],
				"playable_boards":59,
				"total_boards":72,
				"archive_name":"zzt_teen",
				"explicit":1
			}]}
		}`))
	}))
	defer api.Close()

	museum := NewMuseumService(nil)
	museum.APIBaseURL = api.URL
	museum.Client = api.Client()
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	result, err := museum.Search(context.Background(), "Teen")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Count != 1 || len(result.Results) != 1 {
		t.Fatalf("result count=%d len=%d, want one deduped result", result.Count, len(result.Results))
	}
	got := result.Results[0]
	if got.ID != "zzt_teen" || got.Title != "Teen Priest" || got.Author[0] != "Draco" || got.ReleaseDate != "1998-08-31" {
		t.Fatalf("bad result metadata: %+v", got)
	}
}

func TestMuseumServicePlayDownloadsCachesValidatesAndHosts(t *testing.T) {
	worldData := museumTestWorldBytes(t, "TEEN")
	zipData := museumTestZip(t, map[string][]byte{"TEEN.ZZT": worldData})
	hits := 0
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/zgames/t/teen.zip" {
			t.Fatalf("path=%q, want /zgames/t/teen.zip", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	defer files.Close()

	dir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = dir
	museum := NewMuseumService(server)
	museum.FilesBaseURL = files.URL + "/zgames"
	museum.Client = files.Client()
	museum.CacheDir = filepath.Join(dir, ".museum-cache")
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	for i := 0; i < 2; i++ {
		result, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "teen.zip"})
		if err != nil {
			t.Fatalf("Play #%d: %v", i+1, err)
		}
		if result.World != "TEEN" {
			t.Fatalf("Play #%d world=%q, want TEEN", i+1, result.World)
		}
	}
	if hits != 1 {
		t.Fatalf("download hits=%d, want one cache miss", hits)
	}
	if _, err := os.Stat(filepath.Join(dir, "TEEN.ZZT")); err != nil {
		t.Fatalf("hosted world missing: %v", err)
	}
	server.mu.Lock()
	_, hosted := server.Instances["TEEN"]
	server.mu.Unlock()
	if !hosted {
		t.Fatal("world was not hosted as an instance")
	}
}

func TestMuseumServicePlayReturnsChoicesForMultiWorldZip(t *testing.T) {
	zipData := museumTestZip(t, map[string][]byte{
		"ONE.ZZT": []byte("not validated yet"),
		"TWO.ZZT": []byte("not validated yet"),
	})
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipData)
	}))
	defer files.Close()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = t.TempDir()
	museum := NewMuseumService(server)
	museum.FilesBaseURL = files.URL
	museum.Client = files.Client()
	museum.CacheDir = t.TempDir()
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	result, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "multi.zip"})
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if len(result.Choices) != 2 || result.Choices[0].Name != "ONE.ZZT" || result.Choices[1].Name != "TWO.ZZT" {
		t.Fatalf("choices=%+v, want ONE/TWO", result.Choices)
	}
	if result.World != "" {
		t.Fatalf("world=%q, want empty when choices are returned", result.World)
	}
}

func TestMuseumServicePlayRejectsCorruptZip(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not a zip"))
	}))
	defer files.Close()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = t.TempDir()
	museum := NewMuseumService(server)
	museum.FilesBaseURL = files.URL
	museum.Client = files.Client()
	museum.CacheDir = t.TempDir()
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	if _, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "bad.zip"}); err == nil {
		t.Fatal("Play corrupt zip succeeded, want error")
	}
}

func TestMuseumServicePlayRejectsTraversalFilenames(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	museum := NewMuseumService(server)
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	if _, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "../teen.zip"}); err == nil {
		t.Fatal("Play traversal download filename succeeded, want error")
	}
	if _, err := zztFilesFromZip(museumTestZip(t, map[string][]byte{"../EVIL.ZZT": []byte("nope")})); err == nil {
		t.Fatal("zztFilesFromZip traversal entry succeeded, want error")
	}
}

func TestMuseumAPIPlayThenWebSocketJoinsFetchedWorld(t *testing.T) {
	worldData := museumTestWorldBytes(t, "TEEN")
	zipData := museumTestZip(t, map[string][]byte{"TEEN.ZZT": worldData})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/files" {
			t.Fatalf("api path=%q, want /search/files", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"SUCCESS",
			"count":1,
			"data":{"results":[{
				"letter":"t",
				"filename":"teen.zip",
				"title":"Teen Priest",
				"author":["Draco"],
				"release_date":"1998-08-31",
				"archive_name":"zzt_teen"
			}]}
		}`))
	}))
	defer api.Close()
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zgames/t/teen.zip" {
			t.Fatalf("file path=%q, want /zgames/t/teen.zip", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	defer files.Close()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = t.TempDir()
	museum := NewMuseumService(server)
	museum.APIBaseURL = api.URL
	museum.FilesBaseURL = files.URL + "/zgames"
	museum.Client = files.Client()
	museum.CacheDir = filepath.Join(server.WorldsDir, ".museum-cache")
	museum.lastRequest = time.Now().Add(-museumRequestDelay)
	web := &WebAPI{RoomManager: server.RoomManager, Server: server, Museum: museum}
	mux := http.NewServeMux()
	mux.Handle("/api/", web.Handler())
	mux.Handle("/ws", server)
	app := httptest.NewServer(mux)
	defer app.Close()

	searchResp, err := http.Get(app.URL + "/api/museum/search?q=teen")
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	defer searchResp.Body.Close()
	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("GET search status=%d", searchResp.StatusCode)
	}
	var search MuseumSearchResponse
	if err := json.NewDecoder(searchResp.Body).Decode(&search); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(search.Results) != 1 || search.Results[0].Title != "Teen Priest" {
		t.Fatalf("search=%+v, want Teen Priest result", search)
	}

	playResp, err := http.Post(app.URL+"/api/museum/play", "application/json", strings.NewReader(`{"letter":"t","filename":"teen.zip"}`))
	if err != nil {
		t.Fatalf("POST play: %v", err)
	}
	defer playResp.Body.Close()
	if playResp.StatusCode != http.StatusOK {
		t.Fatalf("POST play status=%d", playResp.StatusCode)
	}
	var play MuseumPlayResponse
	if err := json.NewDecoder(playResp.Body).Decode(&play); err != nil {
		t.Fatalf("decode play: %v", err)
	}
	if play.World != "TEEN" {
		t.Fatalf("play world=%q, want TEEN", play.World)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(app.URL, "http") + "/ws?world=" + play.World
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "museum-player", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.You.ID == 0 {
		t.Fatal("joined fetched world with zero player id")
	}
	server.mu.Lock()
	joined := len(server.Instances["TEEN"].Clients)
	server.mu.Unlock()
	if joined != 1 {
		t.Fatalf("TEEN clients=%d, want one joined client", joined)
	}
}

func museumTestWorldBytes(t *testing.T, name string) []byte {
	t.Helper()
	session := NewEditorSession(name, testMultiplayerSmokeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)
	data, err := session.WorldBytes(member, name)
	if err != nil {
		t.Fatalf("WorldBytes: %v", err)
	}
	return data
}

func museumTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}
