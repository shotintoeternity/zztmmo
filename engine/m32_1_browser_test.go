package zztgo

// M32.1's real-browser clause: a signed-in Chromium plays the daily challenge
// through the production server binary.
//
// The sign-in is a MINTED cookie rather than a driven OAuth round trip: the
// session cookie is signed with ZZT_AUTH_COOKIE_SECRET, so a test that knows
// the secret can produce exactly the cookie the server would have set. That
// keeps this suite about the challenge — the OAuth flow itself already has a
// real-browser suite of its own (M16.16).

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

func TestM321ChallengeBrowserJourney(t *testing.T) {
	m169RequireBrowserHarness(t)
	m169RequireClientBuild(t)
	binPath := getM1619ServerBinary(t)

	data := m321WorldBytes(t)
	rootDir := t.TempDir()
	webDir, err := filepath.Abs(m169ClientDir())
	if err != nil {
		t.Fatalf("resolve %s: %v", m169ClientDir(), err)
	}
	savesDir := filepath.Join(rootDir, "saves")
	worldsDir := filepath.Join(rootDir, "worlds")
	recordDir := filepath.Join(rootDir, "recordings")
	for _, dir := range []string{savesDir, worldsDir, recordDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, dir := range []string{worldsDir, rootDir} {
		if err := os.WriteFile(filepath.Join(dir, ChallengeWorldName+".ZZT"), data, 0o644); err != nil {
			t.Fatalf("write %s.ZZT into %s: %v", ChallengeWorldName, dir, err)
		}
		// The client's title screen asks for TOWN when it has not chosen a world
		// yet, and a server hosting only the course would answer that with a 500
		// the journey (rightly) fails on.
		if err := os.WriteFile(filepath.Join(dir, "TOWN.ZZT"), data, 0o644); err != nil {
			t.Fatalf("write TOWN.ZZT into %s: %v", dir, err)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	const cookieSecret = "0123456789abcdef0123456789abcdef"
	cmd := exec.Command(binPath,
		"-world", ChallengeWorldName,
		"-addr", addr,
		"-web", webDir,
		"-saves", savesDir,
		"-worlds", worldsDir,
		"-record", recordDir,
		"-help", ".",
		"-shutdown-grace", "0s",
		"-fresh",
	)
	cmd.Dir = rootDir
	cmd.Env = append(os.Environ(),
		"ZZT_GOOGLE_CLIENT_ID=test-client",
		"ZZT_GOOGLE_CLIENT_SECRET=test-secret",
		"ZZT_AUTH_COOKIE_SECRET="+cookieSecret,
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
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		resp, err := http.Get(baseURL + "/api/challenge")
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
		t.Fatalf("server on %s failed to answer /api/challenge. Logs:\n%s", addr, logBuf.String())
	}

	auth := NewAuthService("test-client", "test-secret", "", []byte(cookieSecret))
	cookie := signedAuthCookie(t, auth, AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"})

	nodeCmd := exec.Command("node", filepath.Join("test", "challenge_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "ZZT_AUTH_COOKIE="+cookie.Name+"="+cookie.Value)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("challenge browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("challenge browser suite output:\n%s", string(out))

	// What the browser cannot see, checked from the server's own artifacts: the
	// run really was recorded, and the leaderboard really is on disk.
	recordings, err := os.ReadDir(recordDir)
	if err != nil {
		t.Fatalf("read recordings: %v", err)
	}
	runs := 0
	for _, entry := range recordings {
		if len(entry.Name()) > 5 && entry.Name()[:5] == "chal-" {
			runs++
		}
	}
	if runs < 2 {
		t.Errorf("recordings hold %d challenge runs, want the two attempts the journey made", runs)
	}
	store, err := NewChallengeStore(ChallengeStorePath(savesDir))
	if err != nil {
		t.Fatalf("reopen the leaderboard the browser wrote: %v", err)
	}
	rows := store.Leaderboard("gem-dash", "google:ada")
	if len(rows) != 1 || !rows[0].You || rows[0].Ticks <= 0 {
		t.Fatalf("stored leaderboard = %+v, want the signed-in browser's one run", rows)
	}
}
