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

	// The committed fixtures/ACCEPT.ZZT must be what accept.zwd compiles to. It
	// is rewritten only on request (M36.26c), as charset_fixture_test.go does;
	// every other run compares, so a stale binary fails instead of being
	// silently replaced. Regenerate after editing the source with:
	//
	//	ZZT_ACCEPT_WRITE=1 go test -run TestM1611CompileAcceptanceWorld .
	//
	// which also refreshes the gitignored engine/ACCEPT.ZZT copy a local
	// zzt-server (-worlds .) hosts.
	binPath := filepath.Join("..", "fixtures", "ACCEPT.ZZT")
	if os.Getenv("ZZT_ACCEPT_WRITE") == "1" {
		for _, path := range []string{binPath, "ACCEPT.ZZT"} {
			if err := os.WriteFile(path, zztBytes, 0644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
		}
	}
	committed, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with ZZT_ACCEPT_WRITE=1)", binPath, err)
	}
	if !bytes.Equal(committed, zztBytes) {
		t.Fatalf("%s is stale against fixtures/accept.zwd; regenerate with ZZT_ACCEPT_WRITE=1", binPath)
	}

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
	m169Parallel(t)
	// This suite builds its own server rather than using M16.9's harness, so it
	// asks for the browser gate itself (owner decision 2026-08-01: the browser
	// suites are opt-in for everyday runs and mandatory for certification).
	m169RequireBrowserHarness(t)

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
	townBytes := committedTownBytes(t)
	_ = os.WriteFile(filepath.Join(spWorldsDir, "TOWN.ZZT"), townBytes, 0644)
	_ = os.WriteFile(filepath.Join(rootDir, "TOWN.ZZT"), townBytes, 0644)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		// -world names the suite's own world (M33.1). It used to be omitted, which
		// meant the server's DEFAULT world — and M29.1 changed that default to
		// LOBBY, a world this harness does not ship, so the server exited before
		// its first request. A suite should name the world it is about rather
		// than inherit whichever one the product currently starts with.
		"-world", "TOWN",
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
