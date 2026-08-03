package zztgo

// M19.2's browser half: the colour picker in a real Chromium — opened from the
// title menu, driven by the keyboard and (a second profile) by the on-screen
// touch bar, and read back off the canvas (web/test/color_picker_journey.test.mjs).
//
// The same shape as M19.1's driver and the co-op cutline's: the PRODUCTION
// zzt-server binary, a temp worlds/saves directory and the real Vite-built
// client. The claim is that a player of the software we ship can pick a colour
// and keep it, so a harness with a control listener would weaken it.
//
// Only ACCEPT is hosted. The picker is a title-screen window and the world it
// eventually joins only has to be a world.

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

func TestM192ColorPickerJourney(t *testing.T) {
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

	outDir, err := filepath.Abs(filepath.Join("web", "test-results", "color-picker"))
	if err != nil {
		t.Fatalf("resolve the colour-picker output directory: %v", err)
	}
	nodeCmd := exec.Command("node", filepath.Join("test", "color_picker_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "PICKER_OUT="+outDir)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("colour-picker browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("colour-picker browser suite output:\n%s", string(out))
}
