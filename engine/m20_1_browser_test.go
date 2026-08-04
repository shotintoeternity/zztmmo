package zztgo

// M20.1's browser half: the /play/<world> deep link in a real Chromium
// (web/test/deep_link_journey.test.mjs).
//
// The same shape as M19.1's and M19.2's drivers: the PRODUCTION zzt-server
// binary, a temp worlds/saves directory and the real Vite-built client. The
// claim is that a URL a player sends someone else lands them on that world's
// title screen, and a URL is only a URL against the server that serves it —
// /play/ACCEPT reaches the client because spaFileServer falls back to the app
// for any path that is not a file, which no in-process harness would exercise.
//
// TWO WORLDS, AND WHICH IS WHICH MATTERS. TOWN is the startup world, so a boot
// with no deep link paints TOWN's board 0; ACCEPT is the deep link's target. A
// suite that deep-linked to the startup world would pass without the feature.
//
// ZZT_GOOGLE_CLIENT_ID is set so /api/auth/me reports sign-in as available and
// the title menu's G actually navigates. Nothing else about Google is real: the
// browser suite answers the start endpoint itself with the redirect Google would
// have sent, so no test reaches accounts.google.com. The server's own half —
// carrying a /play/… return path through the signed OAuth state cookie — is
// TestM201SignInReturnsToTheDeepLinkedTitleScreen in m20_1_test.go.

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

func TestM201DeepLinkJourney(t *testing.T) {
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
	cmd.Env = append(os.Environ(), "ZZT_GOOGLE_CLIENT_ID=m201-client-id", "ZZT_GOOGLE_CLIENT_SECRET=m201-client-secret")
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

	// The deep link is a path, so it must be the SERVER that answers it with the
	// client: asserted here rather than left to the browser, because a 404 there
	// looks like a client bug.
	resp, err := http.Get(baseURL + "/play/ACCEPT")
	if err != nil {
		t.Fatalf("GET /play/ACCEPT: %v", err)
	}
	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /play/ACCEPT status=%d, want 200 (spaFileServer must fall back to the app): %s", resp.StatusCode, body[:n])
	}
	if !bytes.Contains(body[:n], []byte("<canvas")) && !bytes.Contains(body[:n], []byte("<script")) {
		t.Fatalf("GET /play/ACCEPT did not answer with the client: %s", body[:n])
	}

	nodeCmd := exec.Command("node", filepath.Join("test", "deep_link_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deep-link browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("deep-link browser suite output:\n%s", string(out))
}
