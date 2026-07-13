package zztgo

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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
