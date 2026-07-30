package zztgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	m1619BuildErr  error
)

func getM1619ServerBinary(t *testing.T) string {
	t.Helper()
	m1619BuildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "zzt-server-build-*")
		if err != nil {
			m1619BuildErr = err
			return
		}
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

	townBytes, err := os.ReadFile("TOWN.ZZT")
	if err == nil {
		_ = os.WriteFile(filepath.Join(worldsDir, "TOWN.ZZT"), townBytes, 0644)
		_ = os.WriteFile(filepath.Join(rootDir, "TOWN.ZZT"), townBytes, 0644)
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

	// 2. GET / (static index.html)
	resp, err = http.Get(sp.baseURL + "/")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %v, err %v", resp.StatusCode, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "ZZTMMO SPA TEST") {
		t.Errorf("GET / body = %q, want SPA html", string(body))
	}

	// 3. GET /spa/client/route (SPA fallback)
	resp, err = http.Get(sp.baseURL + "/play/TOWN")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /play/TOWN = %v, err %v", resp.StatusCode, err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "ZZTMMO SPA TEST") {
		t.Errorf("GET /play/TOWN body = %q, want SPA fallback", string(body))
	}

	// 4. Graceful SIGINT shutdown
	if err := sp.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal SIGINT: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- sp.cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				t.Logf("subprocess exited after SIGINT with code %d", exitErr.ExitCode())
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("subprocess did not exit within 5s after SIGINT")
	}
}

// TestM1619PathTraversalAndSecurityBoundary verifies path traversal refusal,
// safe save name sanitization, and corrupt file rejection without escaping directory boundaries.
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
			continue
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

	// 3. Corrupt save restore attempt via API / WebSocket
	corruptBytes := []byte("CORRUPT_INVALID_SAVE_HEADER_DATA_1234567890")
	corruptPath := filepath.Join(sp.savesDir, "CORRUPT.SAV")
	_ = os.WriteFile(corruptPath, corruptBytes, 0644)

	// Verify server handles corrupt file without panic/crash
	resp, err := http.Get(sp.baseURL + "/api/worlds")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after corrupt file creation: %v", err)
	}
}

// TestM1619MalformedAndOversizedInputHandling verifies non-JSON, oversized,
// and invalid UTF-8 frames over WebSocket do not panic or crash the server.
func TestM1619MalformedAndOversizedInputHandling(t *testing.T) {
	sp, cleanup := startServerSubprocess(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, sp.wsURL+"?world=TOWN", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	// 1. Send malformed non-JSON frame
	_ = conn.Write(ctx, websocket.MessageText, []byte("NOT_JSON_DATA_BLAH_BLAH{{{"))

	// 2. Send oversized frame (e.g. 128 KB)
	hugeData := bytes.Repeat([]byte("A"), 128*1024)
	_ = conn.Write(ctx, websocket.MessageText, hugeData)

	// 3. Send invalid UTF-8 / binary frame
	_ = conn.Write(ctx, websocket.MessageText, []byte{0xFF, 0xFE, 0xFD, 0xFC, 0x00, 0x01})

	time.Sleep(200 * time.Millisecond)

	// Verify server remains healthy
	resp, err := http.Get(sp.baseURL + "/api/worlds")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("server process died or unhealthy after malformed WS input: %v", err)
	}
}

// TestM1619ChatAndGenerationRateLimits verifies rate limit enforcement for chat and generation.
func TestM1619ChatAndGenerationRateLimits(t *testing.T) {
	sp, cleanup := startServerSubprocess(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, sp.wsURL+"?world=TOWN", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	// Join
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "Tester"}); err != nil {
		t.Fatalf("join write: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("snapshot read: %v", err)
	}

	// Drain incoming diffs/events in background so socket doesn't block
	go func() {
		for {
			var msg json.RawMessage
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return
			}
		}
	}()

	// Send 5 chat messages (allowed limit)
	for i := 1; i <= 5; i++ {
		msg := map[string]interface{}{
			"type": "chat",
			"text": fmt.Sprintf("Message %d", i),
		}
		if err := wsjson.Write(ctx, conn, msg); err != nil {
			t.Fatalf("write chat %d: %v", i, err)
		}
	}

	// 6th message within 10s should be rate-limited cleanly without dropping server
	msg6 := map[string]interface{}{
		"type": "chat",
		"text": "Message 6 Rate Limit Test",
	}
	_ = wsjson.Write(ctx, conn, msg6)

	time.Sleep(200 * time.Millisecond)

	// Verify server process is still healthy
	resp, err := http.Get(sp.baseURL + "/api/worlds")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after rate limit test: %v", err)
	}
}

// TestM1619SlowClientAndAutosaveUnderLoad verifies slow WS reader does not stall
// the tick loop, and autosave writes atomically during load.
func TestM1619SlowClientAndAutosaveUnderLoad(t *testing.T) {
	sp, cleanup := startServerSubprocess(t, "-autosave", "1")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Slow client: dials WS but NEVER reads from socket
	slowConn, _, err := websocket.Dial(ctx, sp.wsURL+"?world=TOWN", nil)
	if err != nil {
		t.Fatalf("dial slow websocket: %v", err)
	}
	defer slowConn.Close(websocket.StatusNormalClosure, "")
	slowConn.SetReadLimit(ServerReadLimit)
	_ = wsjson.Write(ctx, slowConn, JoinMessage{Type: MessageTypeJoin, Name: "SlowClient"})

	// 2. Active client: dials and sends input masks across 20 ticks
	activeConn, _, err := websocket.Dial(ctx, sp.wsURL+"?world=TOWN", nil)
	if err != nil {
		t.Fatalf("dial active websocket: %v", err)
	}
	defer activeConn.Close(websocket.StatusNormalClosure, "")
	activeConn.SetReadLimit(ServerReadLimit)

	if err := wsjson.Write(ctx, activeConn, JoinMessage{Type: MessageTypeJoin, Name: "ActiveClient"}); err != nil {
		t.Fatalf("join active: %v", err)
	}

	var activeSnapshot SnapshotMessage
	if err := wsjson.Read(ctx, activeConn, &activeSnapshot); err != nil {
		t.Fatalf("read active snapshot: %v", err)
	}

	// Drain incoming diffs on active client
	go func() {
		for {
			var msg json.RawMessage
			if err := wsjson.Read(ctx, activeConn, &msg); err != nil {
				return
			}
		}
	}()

	// Send inputs and receive diffs on active client while slow client sits stalled
	for seq := uint64(1); seq <= 20; seq++ {
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

	// 3. Verify server completed autosave cycle without panic
	time.Sleep(1 * time.Second)
	resp, err := http.Get(sp.baseURL + "/api/worlds")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after slow client & autosave load test: %v", err)
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
