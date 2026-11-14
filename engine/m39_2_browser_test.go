package zztgo

// M39.2's browser half: the editor's ☰ menu on a touch profile, driven by taps
// (web/test/editor_touch_menu.test.mjs). The same shape as M19.2's driver: the
// PRODUCTION zzt-server binary, a temp worlds/saves directory and the real
// Vite-built client, hosting only EDIT (fixtures/editor.zwd, M16.13's
// solo-editor world), whose cursor starts over an empty tile at 30,12.

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

func TestM392EditorTouchMenuJourney(t *testing.T) {
	m169Parallel(t)
	m169RequireBrowserHarness(t)

	zwdBytes, err := os.ReadFile(filepath.Join("..", "fixtures", "editor.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/editor.zwd: %v", err)
	}
	editBytes, err := CompileZWD(string(zwdBytes))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/editor.zwd): %v", err)
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
		if err := os.WriteFile(filepath.Join(dir, "EDIT.ZZT"), editBytes, 0o644); err != nil {
			t.Fatalf("write EDIT.ZZT into %s: %v", dir, err)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		// EDIT is the only world hosted, so it is also the startup world: a
		// missing startup world is fatal (M18.14).
		"-world", "EDIT",
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

	outDir, err := filepath.Abs(filepath.Join("web", "test-results", "editor-touch-menu"))
	if err != nil {
		t.Fatalf("resolve the editor-touch-menu output directory: %v", err)
	}
	nodeCmd := exec.Command("node", filepath.Join("test", "editor_touch_menu.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "M392_OUT="+outDir)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("editor touch menu browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("editor touch menu browser suite output:\n%s", string(out))
}
