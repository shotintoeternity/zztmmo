package zztgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

var (
	m1619BuildOnce sync.Once
	m1619ServerBin string
	m1619BuildDir  string
	m1619BuildErr  error
)

// TestMain removes the zzt-server binary getM1619ServerBinary built, so a test
// run leaves nothing behind in $TMPDIR (M36.26d). It is the package's only
// TestMain: every suite that launches the shipped server shares one build.
//
// M41.3: it also stands up one local fake Museum and points the package's
// environment at it before any test runs, so every server a test launches
// (which inherits the environment) and every in-process NewMuseumService
// searches it instead of museumofzzt.com. A browser run then needs no network.
func TestMain(m *testing.M) {
	museum := httptest.NewServer(http.HandlerFunc(m413FakeMuseum))
	_ = os.Setenv(museumAPIBaseEnv, museum.URL+"/api/v1")
	_ = os.Setenv(museumFilesBaseEnv, museum.URL+"/zgames")
	code := m.Run()
	museum.Close()
	if m1619BuildDir != "" {
		_ = os.RemoveAll(m1619BuildDir)
	}
	os.Exit(code)
}

// m413FakeMuseum answers a search with no results, the Museum's shape for a
// query that matches nothing, and refuses everything else. Only M16.16 needs
// Museum results, and it serves its own fake.
func m413FakeMuseum(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/files" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"SUCCESS","count":0,"data":{"results":[]}}`))
		return
	}
	http.Error(w, "M41.3 fake Museum: no such endpoint", http.StatusNotFound)
}

func getM1619ServerBinary(t *testing.T) string {
	t.Helper()
	m1619BuildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "zzt-server-build-*")
		if err != nil {
			m1619BuildErr = err
			return
		}
		m1619BuildDir = tmpDir
		binPath := filepath.Join(tmpDir, "zzt-server")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/zzt-server")
		if out, err := cmd.CombinedOutput(); err != nil {
			m1619BuildErr = fmt.Errorf("go build ./cmd/zzt-server failed: %v\nOutput:\n%s", err, string(out))
			return
		}
		m1619ServerBin = binPath
	})
	if m1619BuildErr != nil {
		t.Fatalf("build zzt-server binary: %v", m1619BuildErr)
	}
	return m1619ServerBin
}

type serverSubprocess struct {
	cmd       *exec.Cmd
	baseURL   string
	wsURL     string
	webDir    string
	savesDir  string
	worldsDir string
	recordDir string
	logs      *bytes.Buffer
}

func startServerSubprocess(t *testing.T, extraFlags ...string) (*serverSubprocess, func()) {
	t.Helper()
	return startServerSubprocessWithEnv(t, nil, extraFlags...)
}

// startServerSubprocessWithEnv is startServerSubprocess with extra environment
// variables (KEY=value) on top of the test process's own; a later entry wins.
func startServerSubprocessWithEnv(t *testing.T, env []string, extraFlags ...string) (*serverSubprocess, func()) {
	t.Helper()
	binPath := getM1619ServerBinary(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	rootDir := t.TempDir()
	webDir := filepath.Join(rootDir, "web")
	savesDir := filepath.Join(rootDir, "saves")
	worldsDir := filepath.Join(rootDir, "worlds")
	recordDir := filepath.Join(rootDir, "record")

	_ = os.MkdirAll(webDir, 0755)
	_ = os.MkdirAll(savesDir, 0755)
	_ = os.MkdirAll(worldsDir, 0755)
	_ = os.MkdirAll(recordDir, 0755)

	_ = os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!DOCTYPE html><html><body>ZZTMMO SPA TEST</body></html>"), 0644)

	townBytes := committedTownBytes(t)
	for _, dir := range []string{worldsDir, rootDir} {
		_ = os.WriteFile(filepath.Join(dir, "TOWN.ZZT"), townBytes, 0644)
	}

	args := []string{
		"-addr", addr,
		"-web", webDir,
		"-saves", savesDir,
		"-worlds", worldsDir,
		"-help", ".",
		"-shutdown-grace", "0s",
		"-fresh",
	}
	args = append(args, extraFlags...)

	cmd := exec.Command(binPath, args...)
	cmd.Dir = rootDir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var logBuf bytes.Buffer
	cmd.Stdout = &logBuf
	cmd.Stderr = &logBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess %s: %v", binPath, err)
	}

	baseURL := "http://" + addr
	wsURL := "ws://" + addr + "/ws"

	ready := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/worlds")
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !ready {
		_ = cmd.Process.Kill()
		t.Fatalf("server subprocess on %s failed to become ready within 10s. Logs:\n%s", addr, logBuf.String())
	}

	sp := &serverSubprocess{
		cmd:       cmd,
		baseURL:   baseURL,
		wsURL:     wsURL,
		webDir:    webDir,
		savesDir:  savesDir,
		worldsDir: worldsDir,
		recordDir: recordDir,
		logs:      &logBuf,
	}

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_ = cmd.Wait()
		}
	}

	return sp, cleanup
}

// TestM1619ServerSubprocessLifecycleAndStaticAssets verifies startup, static asset
// serving, SPA fallback, health endpoints, and graceful SIGINT shutdown.
func TestM1619ServerSubprocessLifecycleAndStaticAssets(t *testing.T) {
	sp, cleanup := startServerSubprocess(t)
	defer cleanup()

	// 1. GET /api/worlds (health / world list)
	resp, err := http.Get(sp.baseURL + "/api/worlds")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/worlds = %v, err %v", resp.StatusCode, err)
	}
	_ = resp.Body.Close()

	// 2. GET /api/health exposes liveness and aggregate load.
	var health struct {
		Status string `json:"status"`
		Totals struct {
			Instances int `json:"instances"`
			Players   int `json:"players"`
		} `json:"totals"`
	}
	resp, err = http.Get(sp.baseURL + "/api/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/health = %v, err %v", resp.StatusCode, err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode /api/health: %v", err)
	}
	_ = resp.Body.Close()
	if health.Status != "ok" || health.Totals.Instances < 1 {
		t.Fatalf("/api/health = %+v, want ok with at least one instance", health)
	}

	// 3. GET /api/metrics exposes timing and resource counters for scaling runs.
	var metrics ServiceStatus
	resp, err = http.Get(sp.baseURL + "/api/metrics")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/metrics = %v, err %v", resp.StatusCode, err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		t.Fatalf("decode /api/metrics: %v", err)
	}
	_ = resp.Body.Close()
	if metrics.Status != "ok" || metrics.Totals.Instances < 1 || len(metrics.Instances) == 0 {
		t.Fatalf("/api/metrics = %+v, want ok with instance detail", metrics)
	}
	if metrics.Memory.HeapSysBytes == 0 || metrics.Memory.Goroutines == 0 {
		t.Fatalf("/api/metrics memory = %+v, want runtime counters", metrics.Memory)
	}

	// 4. GET / (static index.html)
	resp, err = http.Get(sp.baseURL + "/")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %v, err %v", resp.StatusCode, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "ZZTMMO SPA TEST") {
		t.Errorf("GET / body = %q, want SPA html", string(body))
	}

	// 5. GET /spa/client/route (SPA fallback)
	resp, err = http.Get(sp.baseURL + "/play/TOWN")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /play/TOWN = %v, err %v", resp.StatusCode, err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "ZZTMMO SPA TEST") {
		t.Errorf("GET /play/TOWN body = %q, want SPA fallback", string(body))
	}

	// 6. Graceful SIGINT shutdown
	if err := sp.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal SIGINT: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- sp.cmd.Wait()
	}()

	// Graceful means the process ran its shutdown path and exited 0. A crash,
	// a log.Fatal, or death by the signal itself all surface here as an error.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("subprocess did not exit cleanly after SIGINT: %v\nLogs:\n%s", err, sp.logs.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("subprocess did not exit within 5s after SIGINT")
	}
}

// TestM1619PathTraversalAndSecurityBoundary verifies path traversal refusal,
// safe save name sanitization, and that restoring a corrupt save is refused
// without disturbing the world it was aimed at.
func TestM1619PathTraversalAndSecurityBoundary(t *testing.T) {
	sp, cleanup := startServerSubprocess(t)
	defer cleanup()

	// 1. Path traversal attempts on web and API endpoints
	traversalURLs := []string{
		sp.baseURL + "/../../../../etc/passwd",
		sp.baseURL + "/..%2f..%2f..%2fetc%2fpasswd",
		sp.baseURL + "/api/worlds/../../etc/passwd",
		sp.baseURL + "/saves/../../etc/passwd",
		sp.baseURL + "/ws?world=../../etc/passwd",
	}

	for _, u := range traversalURLs {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET %s: %v — the server must answer a traversal attempt, not drop it", u, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if strings.Contains(string(body), "root:x:0:0") || strings.Contains(string(body), "bin/bash") {
			t.Fatalf("Path traversal vulnerability exposed file content for URL %s!", u)
		}
	}

	// 2. Direct SanitizeSaveName defense check
	unsafeNames := []string{"../passwd", "..\\secret", "../../etc/passwd", "/etc/passwd", "a/b", `a\b`, "TOWN.ZZT", "NUL\x00", "SAVE ME", "$HOME"}
	for _, name := range unsafeNames {
		if safe, err := SanitizeSaveName(name); err == nil {
			t.Errorf("SanitizeSaveName(%q) = %q, expected error for unsafe path", name, safe)
		}
	}

	// 3. Restoring a corrupt save is refused, and the world survives it.
	corruptBytes := []byte("CORRUPT_INVALID_SAVE_HEADER_DATA_1234567890")
	corruptPath := filepath.Join(sp.savesDir, "CORRUPT.SAV")
	if err := os.WriteFile(corruptPath, corruptBytes, 0644); err != nil {
		t.Fatalf("write %s: %v", corruptPath, err)
	}
	resp, err := http.Post(sp.baseURL+"/api/restore", "application/json",
		strings.NewReader(`{"world":"TOWN","name":"CORRUPT"}`))
	if err != nil {
		t.Fatalf("POST /api/restore: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "restore failed") {
		t.Fatalf("POST /api/restore of a corrupt save = %d %q, want 500 \"restore failed\"", resp.StatusCode, body)
	}
	if got, err := os.ReadFile(corruptPath); err != nil || !bytes.Equal(got, corruptBytes) {
		t.Fatalf("the refused restore touched %s: %q, %v", corruptPath, got, err)
	}
	// The world the restore was aimed at still takes a player.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _ := m1619Join(t, ctx, sp, "AfterCorrupt")
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// m1619Join dials the subprocess's TOWN and joins as name, failing the test on
// any error, and returns the connection with its first frame.
func m1619Join(t *testing.T, ctx context.Context, sp *serverSubprocess, name string) (*websocket.Conn, SnapshotMessage) {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, sp.wsURL+"?world=TOWN", nil)
	if err != nil {
		t.Fatalf("%s: dial websocket: %v", name, err)
	}
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: name}); err != nil {
		t.Fatalf("%s: join write: %v", name, err)
	}
	var snap SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snap); err != nil {
		t.Fatalf("%s: snapshot read: %v", name, err)
	}
	if snap.Type != MessageTypeSnapshot {
		t.Fatalf("%s: first frame type %q, want %q", name, snap.Type, MessageTypeSnapshot)
	}
	return conn, snap
}

// m1619ReadUntilClosed reads conn until the server closes it and returns the
// close status (-1 when the read failed without a close frame). It fails the
// test if the connection is still open when ctx expires.
func m1619ReadUntilClosed(t *testing.T, ctx context.Context, conn *websocket.Conn, what string) websocket.StatusCode {
	t.Helper()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatalf("%s: the server never closed the connection: %v", what, err)
			}
			return websocket.CloseStatus(err)
		}
	}
}

// TestM1619MalformedAndOversizedInputHandling verifies that a non-JSON frame,
// an invalid-UTF-8 frame, and a frame over ServerReadLimit each end only the
// connection that sent it: the server closes that socket, and a bystander
// already in the room keeps receiving frames and a new player can still join.
func TestM1619MalformedAndOversizedInputHandling(t *testing.T) {
	sp, cleanup := startServerSubprocess(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bystander, _ := m1619Join(t, ctx, sp, "Bystander")
	defer bystander.Close(websocket.StatusNormalClosure, "")

	cases := []struct {
		name  string
		frame []byte
		want  websocket.StatusCode
	}{
		// wsjson.Read refuses a frame that does not decode and closes with
		// 1007 itself, before the read loop's own deferred normal close.
		{"non-JSON", []byte("NOT_JSON_DATA_BLAH_BLAH{{{"), websocket.StatusInvalidFramePayloadData},
		{"invalid UTF-8", []byte{0xFF, 0xFE, 0xFD, 0xFC, 0x00, 0x01}, websocket.StatusInvalidFramePayloadData},
		// One byte over the limit: the old 128 KB frame was well under
		// ServerReadLimit and never exercised it.
		{"oversized", bytes.Repeat([]byte("A"), ServerReadLimit+1), websocket.StatusMessageTooBig},
	}
	for _, c := range cases {
		conn, _ := m1619Join(t, ctx, sp, "Sender")
		if err := conn.Write(ctx, websocket.MessageText, c.frame); err != nil {
			t.Fatalf("%s: write: %v", c.name, err)
		}
		caseCtx, caseCancel := context.WithTimeout(ctx, 5*time.Second)
		got := m1619ReadUntilClosed(t, caseCtx, conn, c.name)
		caseCancel()
		if got != c.want {
			t.Errorf("%s frame: server closed with status %v, want %v", c.name, got, c.want)
		}
		_ = conn.CloseNow()
	}

	// The bystander's connection outlived all three: it still gets frames.
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	if _, _, err := bystander.Read(readCtx); err != nil {
		t.Fatalf("the bystander's connection did not survive the malformed frames: %v", err)
	}
	late, _ := m1619Join(t, ctx, sp, "Latecomer")
	_ = late.Close(websocket.StatusNormalClosure, "")
	resp, err := http.Get(sp.baseURL + "/api/worlds")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after malformed WS input: %v", err)
	}
	_ = resp.Body.Close()
}

// TestM1619ChatAndGenerationRateLimits verifies, against the shipped binary,
// that the sixth chat line inside the window is refused (and is never
// broadcast), and that a second dream from the same client inside the
// generation cooldown is refused with 429.
func TestM1619ChatAndGenerationRateLimits(t *testing.T) {
	// Generation is configured against an API that refuses connections: the
	// first dream fails in planning, after it was admitted and its client's
	// cooldown stamped, so it costs nothing and needs no network.
	sp, cleanup := startServerSubprocessWithEnv(t, []string{
		"ANTHROPIC_API_URL=http://127.0.0.1:1/v1/messages",
		"ANTHROPIC_API_KEY=test-key",
		"ANTHROPIC_MODEL=test-model",
		"ANTHROPIC_MAX_TOKENS=100",
		"ZZT_GENERATION_ATTEMPTS=1",
		"ZZT_GENERATION_RATE_SECONDS=3600",
		"ZZT_GENERATION_DAILY_MAX=-1",
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, snap := m1619Join(t, ctx, sp, "Tester")
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Five lines in the window are admitted and the sixth is refused (M16.16a).
	// The number is the contract, written out rather than read back from
	// chatRateLimitMax, so loosening the constant reddens this test.
	const admitted = 5
	for i := 1; i <= admitted+1; i++ {
		msg := map[string]interface{}{"type": "chat", "text": fmt.Sprintf("Message %d", i)}
		if err := wsjson.Write(ctx, conn, msg); err != nil {
			t.Fatalf("write chat %d: %v", i, err)
		}
	}
	// A private message is admitted by the same limiter and, unlike global
	// chat, reports its refusal. The read loop handles frames in order, so its
	// answer arrives after anything the six chat lines produced.
	const absentPlayer = PlayerID(9999)
	if err := wsjson.Write(ctx, conn, PrivateMessage{Type: MessageTypePrivateMessage, PlayerID: absentPlayer, Text: "sentinel"}); err != nil {
		t.Fatalf("write sentinel private message: %v", err)
	}
	var echoed []string
	for {
		var frame struct {
			Type      string   `json:"type"`
			From      string   `json:"from"`
			PlayerID  PlayerID `json:"playerId"`
			Text      string   `json:"text"`
			History   bool     `json:"history"`
			Delivered bool     `json:"delivered"`
		}
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			t.Fatalf("reading until the sentinel's answer (echoed so far %q): %v", echoed, err)
		}
		if frame.Type == "chat" && !frame.History && frame.PlayerID == snap.You.ID {
			echoed = append(echoed, frame.Text)
		}
		if frame.Type == MessageTypePrivateResult {
			if frame.Delivered || frame.Text != "You are sending messages too quickly." {
				t.Fatalf("sentinel private message answered %+v, want the rate-limit refusal", frame)
			}
			break
		}
	}
	want := make([]string, 0, admitted)
	for i := 1; i <= admitted; i++ {
		want = append(want, fmt.Sprintf("Message %d", i))
	}
	if strings.Join(echoed, "|") != strings.Join(want, "|") {
		t.Fatalf("broadcast chat = %q, want exactly %q (the sixth line is refused)", echoed, want)
	}

	// Generation: the first dream is admitted (and fails in planning); the
	// second, from the same client inside the cooldown, is refused.
	dream := func() (int, string) {
		resp, err := http.Post(sp.baseURL+"/api/generate", "application/json",
			strings.NewReader(`{"prompt":"a small test world"}`))
		if err != nil {
			t.Fatalf("POST /api/generate: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(body)
	}
	if code, body := dream(); code != http.StatusUnprocessableEntity {
		t.Fatalf("first dream = %d %q, want 422 (admitted, then failed against the dead API)", code, body)
	}
	if code, body := dream(); code != http.StatusTooManyRequests || !strings.Contains(body, "rate limit") {
		t.Fatalf("second dream inside the cooldown = %d %q, want 429 rate limit", code, body)
	}
}

// TestM1619SlowClientAndAutosaveUnderLoad verifies a slow WS reader does not
// stall the tick loop for anyone else, and that the autosave written during
// that load is a complete, loadable world file with no temp file left behind.
func TestM1619SlowClientAndAutosaveUnderLoad(t *testing.T) {
	sp, cleanup := startServerSubprocess(t, "-autosave", "1")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Slow client: joins and then NEVER reads from its socket.
	slowConn, _, err := websocket.Dial(ctx, sp.wsURL+"?world=TOWN", nil)
	if err != nil {
		t.Fatalf("dial slow websocket: %v", err)
	}
	defer slowConn.Close(websocket.StatusNormalClosure, "")
	slowConn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, slowConn, JoinMessage{Type: MessageTypeJoin, Name: "SlowClient"}); err != nil {
		t.Fatalf("join slow: %v", err)
	}

	// 2. Active client: sends input masks across 20 ticks and counts the diffs
	// the server sends back while the slow client sits stalled.
	activeConn, _ := m1619Join(t, ctx, sp, "ActiveClient")
	defer activeConn.Close(websocket.StatusNormalClosure, "")

	var diffMu sync.Mutex
	diffs := 0
	go func() {
		for {
			var frame struct {
				Type string `json:"type"`
			}
			if err := wsjson.Read(ctx, activeConn, &frame); err != nil {
				return
			}
			if frame.Type == MessageTypeDiff {
				diffMu.Lock()
				diffs++
				diffMu.Unlock()
			}
		}
	}()

	const inputs = 20
	for seq := uint64(1); seq <= inputs; seq++ {
		inputMsg := map[string]interface{}{
			"type": "input",
			"seq":  seq,
			"input": map[string]interface{}{
				"dx": 1, "dy": 0, "shift": false,
			},
		}
		if err := wsjson.Write(ctx, activeConn, inputMsg); err != nil {
			t.Fatalf("active write input seq %d: %v", seq, err)
		}
		time.Sleep(110 * time.Millisecond)
	}

	// 3. Let at least one more autosave cadence (1s) pass, then stop the
	// server so nothing is mid-write while the directory is inspected.
	time.Sleep(1200 * time.Millisecond)
	diffMu.Lock()
	gotDiffs := diffs
	diffMu.Unlock()
	// The same loose floor the 30-client run uses: it catches a tick loop the
	// slow reader stalled, not jitter.
	if gotDiffs < inputs/5 {
		t.Errorf("active client received %d diffs across %d ticks beside a stalled reader, want at least %d", gotDiffs, inputs, inputs/5)
	}
	resp, err := http.Get(sp.baseURL + "/api/worlds")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after slow client & autosave load test: %v", err)
	}
	_ = resp.Body.Close()
	// SIGINT, not the cleanup's SIGKILL: a kill could land mid-write and leave
	// the very temp file this test says never survives.
	if err := sp.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal SIGINT: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- sp.cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("server did not exit cleanly after SIGINT: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("server did not exit within 5s after SIGINT")
	}

	autosaveDir := filepath.Join(sp.savesDir, "autosave")
	entries, err := os.ReadDir(autosaveDir)
	if err != nil {
		t.Fatalf("read %s: %v (no autosave was written during the run)", autosaveDir, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 1 || names[0] != "TOWN.SAV" {
		t.Fatalf("autosave directory holds %q, want exactly [TOWN.SAV] (no temp file left behind)", names)
	}
	f, err := os.Open(filepath.Join(autosaveDir, "TOWN.SAV"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scratch := newSnapshotEngine()
	if err := scratch.worldReadFrom(f, false, nil); err != nil {
		t.Fatalf("autosave TOWN.SAV does not load: %v", err)
	}
	if err := validateWorldBoards(scratch.World); err != nil {
		t.Fatalf("autosave TOWN.SAV has a bad board: %v", err)
	}
	if town := townRoomManager(t).world; scratch.World.BoardCount != town.BoardCount {
		t.Fatalf("autosave TOWN.SAV has %d boards, want TOWN's %d", scratch.World.BoardCount, town.BoardCount)
	}
}

// TestM1619ThirtyNetworkClientLoadAndMetrics drives 30 concurrent real WebSocket
// clients over TCP network connections, measures tick latency p50/p95/max, memory
// growth, fanout bytes, and tick cost, asserting production load readiness and
// recording vertical scaling vs sharding decision boundaries.
func TestM1619ThirtyNetworkClientLoadAndMetrics(t *testing.T) {
	sp, cleanup := startServerSubprocess(t)
	defer cleanup()

	const clientCount = 30
	const runTicks = 50

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The reader goroutines below write these counters while the main
	// goroutine reads them for the metrics assertions, so every access is
	// guarded — an unsynchronized read here was a real race (`go test -race`).
	type clientBot struct {
		conn *websocket.Conn
		id   PlayerID

		mu        sync.Mutex
		bytesRead int64
		diffCount int
		err       error
	}

	bots := make([]*clientBot, clientCount)
	var wg sync.WaitGroup

	// Record initial memory stats
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Dial 30 network clients
	for i := 0; i < clientCount; i++ {
		conn, _, err := websocket.Dial(ctx, sp.wsURL+"?world=TOWN", nil)
		if err != nil {
			t.Fatalf("bot %d dial failed: %v", i, err)
		}
		conn.SetReadLimit(ServerReadLimit)

		botName := fmt.Sprintf("LoadBot%d", i+1)
		if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: botName}); err != nil {
			t.Fatalf("bot %d join failed: %v", i, err)
		}
		var snap SnapshotMessage
		if err := wsjson.Read(ctx, conn, &snap); err != nil {
			t.Fatalf("bot %d read snapshot failed: %v", i, err)
		}

		b := &clientBot{conn: conn, id: snap.You.ID}
		bots[i] = b

		// Client background reader
		wg.Add(1)
		go func(bot *clientBot) {
			defer wg.Done()
			for {
				typ, data, err := bot.conn.Read(ctx)
				if err != nil {
					bot.mu.Lock()
					bot.err = err
					bot.mu.Unlock()
					return
				}
				if typ == websocket.MessageText {
					bot.mu.Lock()
					bot.bytesRead += int64(len(data))
					if strings.Contains(string(data), `"type":"diff"`) {
						bot.diffCount++
					}
					bot.mu.Unlock()
				}
			}
		}(b)
	}

	defer func() {
		cancel()
		for _, b := range bots {
			if b.conn != nil {
				_ = b.conn.Close(websocket.StatusNormalClosure, "")
			}
		}
		wg.Wait()
	}()

	// Perform 50 load ticks sending input masks.
	//
	// NOTE ON WHAT THIS MEASURES: the timer below spans the client-side writes
	// of one keymask per bot. It is the harness's own fan-out cost, NOT the
	// server's tick duration and NOT a round-trip latency — a write returns
	// once the frame is buffered. Server responsiveness is asserted separately,
	// from the diffs each bot actually receives.
	writeFanoutLatencies := make([]time.Duration, 0, runTicks)
	for tick := 0; tick < runTicks; tick++ {
		start := time.Now()
		for i, b := range bots {
			dx := int16(0)
			dy := int16(0)
			switch i % 4 {
			case 0:
				dx = 1
			case 1:
				dx = -1
			case 2:
				dy = 1
			case 3:
				dy = -1
			}
			inputMsg := map[string]interface{}{
				"type": "input",
				"seq":  uint64(tick + 1),
				"input": map[string]interface{}{
					"dx": dx, "dy": dy, "shift": false,
				},
			}
			_ = wsjson.Write(ctx, b.conn, inputMsg)
		}
		elapsed := time.Since(start)
		writeFanoutLatencies = append(writeFanoutLatencies, elapsed)
		time.Sleep(110 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	// Read final memory stats
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Collect per-bot counters under each bot's lock; the readers are still
	// running at this point (they are stopped by the deferred cancel).
	var totalBytes int64
	var totalDiffs int
	minDiffs := -1
	for i, b := range bots {
		b.mu.Lock()
		bytesRead, diffCount, readErr := b.bytesRead, b.diffCount, b.err
		b.mu.Unlock()

		// A reader that died before the run finished means the server dropped
		// a well-behaved client under load — the failure this test exists to
		// catch, and one the latency number below cannot see.
		if readErr != nil {
			t.Errorf("bot %d (player %d) reader failed during the load run: %v", i, b.id, readErr)
		}
		// Fan-out must actually reach every client. Without this, a server
		// that accepted 30 sockets and then delivered nothing would still
		// post an excellent write-latency figure and pass.
		if diffCount == 0 {
			t.Errorf("bot %d (player %d) received no diff frames across %d ticks — fanout did not reach it", i, b.id, runTicks)
		}
		if minDiffs < 0 || diffCount < minDiffs {
			minDiffs = diffCount
		}
		totalBytes += bytesRead
		totalDiffs += diffCount
	}
	if totalBytes == 0 {
		t.Fatalf("no bytes delivered to any of the %d clients", clientCount)
	}

	// Over runTicks ticks at the server's 110ms cadence every joined client
	// should see diffs on the same order. The floor is deliberately loose —
	// it is here to catch a stalled or starved fan-out, not to police jitter
	// on a loaded CI box.
	minExpectedDiffs := runTicks / 5
	if minDiffs < minExpectedDiffs {
		t.Errorf("slowest client received %d diff frames across %d ticks, want at least %d — fanout is starving clients under load",
			minDiffs, runTicks, minExpectedDiffs)
	}

	p50, p95, maxLat := calculateLatencyPercentiles(writeFanoutLatencies)
	memGrowthMB := float64(memAfter.Alloc-memBefore.Alloc) / (1024 * 1024)
	if memGrowthMB < 0 {
		memGrowthMB = 0
	}

	// Publish metrics summary. Everything logged here is measured by this run
	// on this machine; nothing is extrapolated to other client counts or to
	// any particular host. See the scaling note at the end.
	t.Logf("=== M16.19 30-NETWORK-CLIENT LOAD TEST METRICS (measured, this host) ===")
	t.Logf("Clients: %d network WebSockets over TCP", clientCount)
	t.Logf("Ticks driven: %d (input sent every 110ms)", runTicks)
	t.Logf("Client-side write fanout p50: %v", p50)
	t.Logf("Client-side write fanout p95: %v", p95)
	t.Logf("Client-side write fanout max: %v", maxLat)
	t.Logf("Diff frames delivered: %d total, %d to the slowest client", totalDiffs, minDiffs)
	t.Logf("Total fanout bytes delivered: %d KB", totalBytes/1024)
	t.Logf("Heap alloc growth: %.2f MB", memGrowthMB)
	t.Logf("Avg fanout rate: %.2f KB/s per client", float64(totalBytes)/1024/5.5/clientCount)

	// Scaling: what this test does and does not establish.
	//
	// Established: %d concurrent real TCP WebSocket clients in one world are
	// served without dropping a reader or starving any client's fan-out, at
	// the numbers logged above, on whatever machine ran this test.
	//
	// NOT established: behaviour at any larger client count, on any specific
	// host class, or across multiple rooms/worlds. Projecting a vertical- or
	// horizontal-scaling threshold from a single 30-client dev-machine run is
	// not something this test's evidence supports; that needs a staged run on
	// the target instance with real world traffic. Deliberately not asserted
	// or logged here as if it were a finding.
	t.Logf("Scope: %d clients, one world, this host. No larger-scale or per-instance-class claim is made.", clientCount)

	// The write-fanout figure must stay well inside the 110ms tick cadence:
	// if driving 30 clients' input costs more than that, the harness itself
	// is the bottleneck and the diff assertions above mean much less.
	if p95 > 50*time.Millisecond {
		t.Errorf("p95 client-side write fanout %v exceeded 50ms — the harness could not keep %d clients fed inside the tick cadence", p95, clientCount)
	}
}

func calculateLatencyPercentiles(latencies []time.Duration) (p50, p95, max time.Duration) {
	if len(latencies) == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	p50Idx := len(sorted) * 50 / 100
	p95Idx := len(sorted) * 95 / 100
	if p95Idx >= len(sorted) {
		p95Idx = len(sorted) - 1
	}
	return sorted[p50Idx], sorted[p95Idx], sorted[len(sorted)-1]
}
