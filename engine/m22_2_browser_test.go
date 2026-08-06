package zztgo

// M22.2's browser half: /watch/<world> is a real, shareable read-only URL.
//
// This is deliberately hosted by the production zzt-server binary, like
// M20.1's /play suite. The claim starts at the address bar: /watch/TOWN has to
// be served by the SPA fallback, resolved through /api/worlds, and joined as a
// spectator by the browser with no test-only state injected.

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestM222WatchLinkJourney(t *testing.T) {
	m169RequireBrowserHarness(t)

	zwdBytes, err := os.ReadFile(filepath.Join("..", "fixtures", "accept.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/accept.zwd: %v", err)
	}
	acceptBytes, err := CompileZWD(string(zwdBytes))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/accept.zwd): %v", err)
	}
	townBytes := committedTownBytes(t)

	m169RequireClientBuild(t)
	binPath := getM1619ServerBinary(t)

	rootDir := t.TempDir()
	webDir, err := filepath.Abs(m169ClientDir())
	if err != nil {
		t.Fatalf("resolve %s: %v", m169ClientDir(), err)
	}
	savesDir := filepath.Join(rootDir, "saves")
	worldsDir := filepath.Join(rootDir, "worlds")
	if err := os.MkdirAll(savesDir, 0o755); err != nil {
		t.Fatalf("mkdir saves: %v", err)
	}
	if err := os.MkdirAll(worldsDir, 0o755); err != nil {
		t.Fatalf("mkdir worlds: %v", err)
	}
	for _, dir := range []string{worldsDir, rootDir} {
		if err := os.WriteFile(filepath.Join(dir, "ACCEPT.ZZT"), acceptBytes, 0o644); err != nil {
			t.Fatalf("write ACCEPT.ZZT into %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "TOWN.ZZT"), townBytes, 0o644); err != nil {
			t.Fatalf("write TOWN.ZZT into %s: %v", dir, err)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		"-world", "TOWN",
		"-addr", addr,
		"-web", webDir,
		"-saves", savesDir,
		"-worlds", worldsDir,
		"-help", ".",
		"-shutdown-grace", "0s",
		"-fresh",
	)
	cmd.Dir = rootDir
	var logBuf bytes.Buffer
	cmd.Stdout = &logBuf
	cmd.Stderr = &logBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start zzt-server: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_ = cmd.Wait()
		}
	}()

	baseURL := "http://" + addr
	ready := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
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
		t.Fatalf("server on %s failed to become ready. Logs:\n%s", addr, logBuf.String())
	}

	resp, err := http.Get(baseURL + "/watch/TOWN")
	if err != nil {
		t.Fatalf("GET /watch/TOWN: %v", err)
	}
	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /watch/TOWN status=%d, want 200 (spaFileServer must fall back to the app): %s", resp.StatusCode, body[:n])
	}
	if !bytes.Contains(body[:n], []byte("<canvas")) && !bytes.Contains(body[:n], []byte("<script")) {
		t.Fatalf("GET /watch/TOWN did not answer with the client: %s", body[:n])
	}

	nodeCmd := exec.Command("node", filepath.Join("test", "watch_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("watch-link browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("watch-link browser suite output:\n%s", string(out))
}
