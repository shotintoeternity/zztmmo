package zztgo

// M19.1's browser half: two real Chromium instances in one room, each with its
// own picked colour, asserted on the canvas (web/test/player_color.test.mjs).
//
// The same shape as the co-op cutline driver: the PRODUCTION zzt-server binary,
// a temp worlds/saves directory and the real Vite-built client. The claim is
// that two people can tell each other apart in the software we ship, so a
// harness with a control listener would weaken it.
//
// Only ACCEPT is hosted. The colour is drawn over whatever the server sent for
// a player's square, so one deterministic board is enough — the interesting
// cases (darkness, the energizer blink, a roster ahead of the screen) are the
// pure rule's, and web/test/player_tint.test.mjs covers those without a browser.

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

func TestM191TwoBrowsersShowTwoDifferentPlayerColors(t *testing.T) {
	m169RequireBrowserHarness(t)

	zwdBytes, err := os.ReadFile(filepath.Join("..", "fixtures", "accept.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/accept.zwd: %v", err)
	}
	acceptBytes, err := CompileZWD(string(zwdBytes))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/accept.zwd): %v", err)
	}

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
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		// ACCEPT is the only world hosted, so it is also the startup world: the
		// server's default is TOWN and a missing startup world is fatal (M18.14).
		"-world", "ACCEPT",
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

	outDir, err := filepath.Abs(filepath.Join("web", "test-results", "player-color"))
	if err != nil {
		t.Fatalf("resolve the player-colour output directory: %v", err)
	}
	nodeCmd := exec.Command("node", filepath.Join("test", "player_color.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "COLOR_OUT="+outDir)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("player-colour browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("player-colour browser suite output:\n%s", string(out))
}
