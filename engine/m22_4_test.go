package zztgo

import (
	"bytes"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func m224API(t *testing.T, id string, data []byte) (*WebAPI, string) {
	t.Helper()
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
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	server.Now = func() time.Time { return now }
	return &WebAPI{Server: server}, replayDir
}

func m224Get(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "198.51.100.7:4321"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestM224ReplayPostcardGIFEndpoint(t *testing.T) {
	id := "TOWN-20260807-120000"
	data, _, _ := m223Recording(t, id)
	api, replayDir := m224API(t, id, data)
	handler := api.Handler()

	rr := m224Get(t, handler, "/api/replay/postcard.gif?id="+id+"&start=0&ticks=8")
	if rr.Code != http.StatusOK {
		t.Fatalf("postcard status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/gif" {
		t.Fatalf("Content-Type=%q", got)
	}
	if link := rr.Header().Get("Link"); !strings.Contains(link, "</replay/"+id+">; rel=\"replay\"") || !strings.Contains(link, "</play/TOWN>; rel=\"play\"") {
		t.Fatalf("missing replay/play Link headers: %q", link)
	}
	anim, err := gif.DecodeAll(bytes.NewReader(rr.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode postcard gif: %v", err)
	}
	if len(anim.Image) < 2 {
		t.Fatalf("postcard has %d frame(s), want animation", len(anim.Image))
	}
	if got, want := anim.Image[0].Bounds().Dx(), int(BOARD_WIDTH)*renderCellWidth; got != want {
		t.Fatalf("gif width=%d, want %d", got, want)
	}
	if got, want := anim.Image[0].Bounds().Dy(), int(BOARD_HEIGHT)*renderCellHeight; got != want {
		t.Fatalf("gif height=%d, want %d", got, want)
	}

	if err := os.Remove(filepath.Join(replayDir, id+".jsonl")); err != nil {
		t.Fatalf("remove recording after cache fill: %v", err)
	}
	cached := m224Get(t, handler, "/api/replay/postcard.gif?id="+id+"&start=0&ticks=8")
	if cached.Code != http.StatusOK {
		t.Fatalf("cached postcard status=%d body=%s", cached.Code, cached.Body.String())
	}
	if !bytes.Equal(cached.Body.Bytes(), rr.Body.Bytes()) {
		t.Fatal("cached postcard bytes changed after the source recording was removed")
	}
}

func TestM224ReplayPostcardBoundsRateLimitAndMissingID(t *testing.T) {
	id := "TOWN-20260807-120000"
	data, _, _ := m223Recording(t, id)
	api, _ := m224API(t, id, data)
	handler := api.Handler()

	tooWide := m224Get(t, handler, "/api/replay/postcard.gif?id="+id+"&ticks=999")
	if tooWide.Code != http.StatusBadRequest {
		t.Fatalf("wide range status=%d, want 400", tooWide.Code)
	}
	missing := m224Get(t, handler, "/api/replay/postcard.gif?id=NO_SUCH_RECORDING&start=0&ticks=4")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing recording status=%d, want 404", missing.Code)
	}

	api, _ = m224API(t, id, data)
	handler = api.Handler()
	for i := 0; i < postcardRateMax; i++ {
		rr := m224Get(t, handler, "/api/replay/postcard.gif?id="+id+"&start=0&ticks=4")
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d status=%d body=%s", i+1, rr.Code, rr.Body.String())
		}
	}
	limited := m224Get(t, handler, "/api/replay/postcard.gif?id="+id+"&start=0&ticks=4")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited request status=%d, want 429", limited.Code)
	}
}

func TestM224PostcardsAreBetaLimitedToSinglePlayerRecordings(t *testing.T) {
	world := townWorld(t)
	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, "TOWN", world, &buf)
	ada := rm.JoinPlayer(1, 0, 0)
	bo := rm.JoinPlayer(1, 0, 0)
	rm.StepDiffs(map[PlayerID]PlayerInput{ada: {DeltaX: 1}, bo: {DeltaX: -1}})
	rec.Close()

	api, _ := m224API(t, "TOWN-MULTI-20260807", buf.Bytes())
	rr := m224Get(t, api.Handler(), "/api/replay/postcard.gif?id=TOWN-MULTI-20260807&start=0&ticks=4")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("multi-player postcard status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "single-player") {
		t.Fatalf("multi-player refusal does not name the beta limit: %s", rr.Body.String())
	}
}
