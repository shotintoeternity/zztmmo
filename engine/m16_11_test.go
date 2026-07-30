package zztgo

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

func TestM1611CompileAcceptanceWorld(t *testing.T) {
	zwdPath := filepath.Join("..", "fixtures", "accept.zwd")
	zwdBytes, err := os.ReadFile(zwdPath)
	if err != nil {
		t.Fatalf("read fixtures/accept.zwd: %v", err)
	}

	zztBytes, err := CompileZWD(string(zwdBytes))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/accept.zwd) failed: %v", err)
	}

	if len(zztBytes) == 0 {
		t.Fatalf("CompileZWD output is empty")
	}

	// Write compiled ACCEPT.ZZT into fixtures and engine directory
	_ = os.WriteFile(filepath.Join("..", "fixtures", "ACCEPT.ZZT"), zztBytes, 0644)
	_ = os.WriteFile("ACCEPT.ZZT", zztBytes, 0644)

	// Validate world loading and 200 GameSteps
	world, err := CompileZWDWorld(string(zwdBytes))
	if err != nil {
		t.Fatalf("CompileZWDWorld failed: %v", err)
	}

	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardChange(1)

	for i := 0; i < 200; i++ {
		e.GameStepWithInputs(nil)
	}

	t.Logf("ACCEPT.ZZT compiled successfully (%d bytes), 200 ticks passed cleanly", len(zztBytes))
}

func TestM1611BrowserEndToEndPlayerJourneys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser E2E journey test in short mode")
	}

	// 1. Compile ACCEPT.ZZT
	zwdPath := filepath.Join("..", "fixtures", "accept.zwd")
	zwdBytes, err := os.ReadFile(zwdPath)
	if err != nil {
		t.Fatalf("read fixtures/accept.zwd: %v", err)
	}
	zztBytes, err := CompileZWD(string(zwdBytes))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/accept.zwd): %v", err)
	}

	// 2. Build browser web/dist if needed.
	//
	// The presence of the directory is not enough: an empty or partial dist
	// makes the server answer every page with its "build the browser client"
	// 404, which a browser test can easily mistake for a working client. Key
	// the rebuild on index.html, the file the server actually serves.
	webDistDir := filepath.Join("web", "dist")
	if _, err := os.Stat(filepath.Join(webDistDir, "index.html")); err != nil {
		cmdBuild := exec.Command("npm", "--prefix", "web", "run", "build")
		if out, err := cmdBuild.CombinedOutput(); err != nil {
			t.Fatalf("npm run build failed: %v\nOutput:\n%s", err, string(out))
		}
	}

	// 3. Build zzt-server binary
	binPath := getM1619ServerBinary(t)

	// 4. Set up temp directory structure
	rootDir := t.TempDir()
	// Absolute: the server below runs with cmd.Dir = rootDir, so a relative
	// "web/dist" would resolve inside the temp dir and silently serve the
	// build-me 404 page instead of the client.
	spWebDir, err := filepath.Abs(webDistDir)
	if err != nil {
		t.Fatalf("resolve %s: %v", webDistDir, err)
	}
	spSavesDir := filepath.Join(rootDir, "saves")
	spWorldsDir := filepath.Join(rootDir, "worlds")

	_ = os.MkdirAll(spSavesDir, 0755)
	_ = os.MkdirAll(spWorldsDir, 0755)

	// Copy ACCEPT.ZZT and TOWN.ZZT into spWorldsDir and rootDir
	_ = os.WriteFile(filepath.Join(spWorldsDir, "ACCEPT.ZZT"), zztBytes, 0644)
	_ = os.WriteFile(filepath.Join(rootDir, "ACCEPT.ZZT"), zztBytes, 0644)
	if townBytes, err := os.ReadFile("TOWN.ZZT"); err == nil {
		_ = os.WriteFile(filepath.Join(spWorldsDir, "TOWN.ZZT"), townBytes, 0644)
		_ = os.WriteFile(filepath.Join(rootDir, "TOWN.ZZT"), townBytes, 0644)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		"-addr", addr,
		"-web", spWebDir,
		"-saves", spSavesDir,
		"-worlds", spWorldsDir,
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
		t.Fatalf("server on %s failed to become ready. Logs:\n%s", addr, logBuf.String())
	}

	// 5. Run node test/e2e_journey.test.mjs
	nodeCmd := exec.Command("node", "test/e2e_journey.test.mjs")
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Playwright E2E journey test failed: %v\nOutput:\n%s\nServer Logs:\n%s", err, string(out), logBuf.String())
	}

	t.Logf("Playwright E2E journey output:\n%s", string(out))
}
