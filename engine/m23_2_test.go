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

func m232WelcomeWorldBytes(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "fixtures", "welcome.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/welcome.zwd: %v", err)
	}
	data, err := CompileZWD(string(src))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/welcome.zwd): %v", err)
	}
	validateCompiledZWD(t, data)
	return data
}

func TestM232WelcomeWorldCompilesShipsAndIsCanonical(t *testing.T) {
	data := m232WelcomeWorldBytes(t)
	if len(data) == 0 {
		t.Fatal("CompileZWD(fixtures/welcome.zwd) returned no bytes")
	}
	if err := os.WriteFile(filepath.Join("..", "fixtures", "WELCOME.ZZT"), data, 0o644); err != nil {
		t.Fatalf("write fixtures/WELCOME.ZZT: %v", err)
	}
	// The production deploy bundle is built from the engine hosting directory.
	// engine/*.ZZT is gitignored, so this local write is a generated artifact;
	// fixtures/WELCOME.ZZT is the committed source of truth for clean clones.
	if err := os.WriteFile("WELCOME.ZZT", data, 0o644); err != nil {
		t.Fatalf("write engine/WELCOME.ZZT: %v", err)
	}
	if !WorldIsCanonical("WELCOME") {
		t.Fatal("WorldIsCanonical(WELCOME) = false; the welcome world must be protected like shipped classics")
	}

	world, err := CompileZWDWorld(string(mustRead(t, "../fixtures/welcome.zwd")))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/welcome.zwd): %v", err)
	}
	if world.BoardCount != 5 {
		t.Fatalf("WELCOME has BoardCount %d, want 5 (title plus five playable boards)", world.BoardCount)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardChange(1)
	for i := 0; i < 200; i++ {
		e.GameStepWithInputs(nil)
	}
}

func TestM232WelcomeWorldThreeBrowserJourney(t *testing.T) {
	m169RequireBrowserHarness(t)

	welcomeBytes := m232WelcomeWorldBytes(t)
	m169RequireClientBuild(t)
	binPath := getM1619ServerBinary(t)

	rootDir := t.TempDir()
	webDir, err := filepath.Abs(m169ClientDir())
	if err != nil {
		t.Fatalf("resolve %s: %v", m169ClientDir(), err)
	}
	savesDir := filepath.Join(rootDir, "saves")
	worldsDir := filepath.Join(rootDir, "worlds")
	for _, dir := range []string{savesDir, worldsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, dir := range []string{worldsDir, rootDir} {
		if err := os.WriteFile(filepath.Join(dir, "WELCOME.ZZT"), welcomeBytes, 0o644); err != nil {
			t.Fatalf("write WELCOME.ZZT to %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "TOWN.ZZT"), committedTownBytes(t), 0o644); err != nil {
			t.Fatalf("write TOWN.ZZT to %s: %v", dir, err)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		"-world", "WELCOME",
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

	outDir, err := filepath.Abs(filepath.Join("web", "test-results", "welcome"))
	if err != nil {
		t.Fatalf("resolve welcome output directory: %v", err)
	}
	nodeCmd := exec.Command("node", filepath.Join("test", "welcome_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "WELCOME_OUT="+outDir)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("welcome browser journey failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("welcome browser journey output:\n%s", string(out))
}
