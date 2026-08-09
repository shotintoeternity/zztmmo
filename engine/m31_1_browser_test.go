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

func TestM311ComfortPreferencesBrowserJourney(t *testing.T) {
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
		if err := os.WriteFile(filepath.Join(dir, "TOWN.ZZT"), acceptBytes, 0o644); err != nil {
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
	cmd.Env = append(os.Environ(),
		"ZZT_GOOGLE_CLIENT_ID=test-client",
		"ZZT_GOOGLE_CLIENT_SECRET=test-secret",
		"ZZT_AUTH_COOKIE_SECRET=0123456789abcdef0123456789abcdef",
	)
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

	nodeCmd := exec.Command("node", filepath.Join("test", "comfort_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("comfort browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("comfort browser suite output:\n%s", string(out))
}
