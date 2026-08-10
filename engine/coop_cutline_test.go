package zztgo

// The co-op product cutline (TASKS.md, "Set and protect the co-op product
// cutline"). CUTLINE.md is the manual form of this journey and the policy that
// rests on it; engine/web/test/coop_journey.test.mjs is the script.
//
// This driver is deliberately the same shape as M16.11's: the PRODUCTION
// zzt-server binary, a temp worlds/saves directory, and the real Vite-built
// client — no harness, no control listener, no staged state. The cutline claims
// that a small group can play together on the software we ship, so anything
// that only exists for tests would weaken it.
//
// Two worlds are hosted because the journey needs both halves of the claim:
// ACCEPT (fixtures/accept.zwd) has the pickups, the locked door and the passage
// the group's shared progress is measured on, and TOWN is a shipped classic, so
// the group is proved against a real ZZT world and not only against a fixture
// built to be provable.

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

func TestCoopCutlineThreePlayerAcceptanceJourney(t *testing.T) {
	// Like M16.11 this suite stands up its own server, so it asks for the
	// browser gate itself: opt-in for everyday runs, mandatory for certification.
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
	// Absolute: the server runs with cmd.Dir = rootDir, so a relative "web/dist"
	// would resolve inside the temp dir and silently serve the build-me 404 page.
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

	townBytes := committedTownBytes(t)
	for _, w := range []struct {
		name  string
		bytes []byte
	}{{"ACCEPT.ZZT", acceptBytes}, {"TOWN.ZZT", townBytes}} {
		if err := os.WriteFile(filepath.Join(worldsDir, w.name), w.bytes, 0o644); err != nil {
			t.Fatalf("write %s: %v", w.name, err)
		}
		if err := os.WriteFile(filepath.Join(rootDir, w.name), w.bytes, 0o644); err != nil {
			t.Fatalf("write %s: %v", w.name, err)
		}
	}

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

	outDir, err := filepath.Abs(filepath.Join("web", "test-results", "coop"))
	if err != nil {
		t.Fatalf("resolve the co-op output directory: %v", err)
	}
	nodeCmd := exec.Command("node", filepath.Join("test", "coop_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "COOP_OUT="+outDir)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("co-op cutline journey failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("co-op cutline journey output:\n%s", string(out))
}
