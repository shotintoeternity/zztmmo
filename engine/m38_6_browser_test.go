package zztgo

// M38.6's real-browser clause: a signed-in Chromium makes a live Gem Dash run,
// opens the TAS workbench from the challenge screen, imports that run, changes
// one tick's input, sees the board change at that tick, submits, and sees the
// assisted row — through the production server binary, set up as M32.1's
// journey is (a minted cookie, the catalogue as a -challenges file).

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestM386WorkbenchBrowserJourney(t *testing.T) {
	m169Parallel(t)
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
	// ... and this process needs it too. The closing assertions call
	// store.Leaderboard, which resolves the id through ChallengeByID against the
	// catalogue of whoever is asking -- so without this the browser's run is on
	// disk, the journey passes, and the leaderboard reads back empty here.
	withChallengeCatalogue(t, gemDashFixture())
	// The shipped catalogue is empty, and this is a SUBPROCESS -- the production
	// binary, which the in-process fixture swap cannot reach. It gets the row
	// the same way a deployment offering a challenge would: a file and the flag.
	cataloguePath := writeChallengeCatalogueFile(t, rootDir, gemDashFixture())

	cmd := exec.Command(binPath,
		"-world", ChallengeWorldName,
		"-challenges", cataloguePath,
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
	// M37.10: zzt-server requires a username with sign-in on, so Ada claims
	// one the way the launch box does before she plays.
	claimTestUsername(t, baseURL, cookie, "ada")

	nodeCmd := exec.Command("node", filepath.Join("test", "workbench_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL, "ZZT_AUTH_COOKIE="+cookie.Name+"="+cookie.Value)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workbench browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("workbench browser suite output:\n%s", string(out))

	// What the browser cannot see, from the server's own store: the assisted
	// row is there, and the live row the browser made is untouched by it.
	store, err := NewChallengeStore(ChallengeStorePath(savesDir))
	if err != nil {
		t.Fatalf("reopen the leaderboard the browser wrote: %v", err)
	}
	assisted := store.LeaderboardIn("gem-dash", ChallengeDivisionAssisted, "google:ada")
	live := store.Leaderboard("gem-dash", "google:ada")
	if len(assisted) != 1 || !assisted[0].You || assisted[0].Ticks <= 0 {
		t.Fatalf("stored assisted board = %+v, want the browser's one submission", assisted)
	}
	if len(live) != 1 || !live[0].You || live[0].RecordingID == assisted[0].RecordingID {
		t.Fatalf("stored live board = %+v, want the live run alone", live)
	}
	if !bytes.Contains(out, []byte(fmt.Sprintf("ASSISTED_TICKS=%d", assisted[0].Ticks))) || !bytes.Contains(out, []byte(fmt.Sprintf("LIVE_TICKS=%d", live[0].Ticks))) {
		t.Fatalf("the stored rows (assisted %d, live %d ticks) are not the ones the browser saw:\n%s", assisted[0].Ticks, live[0].Ticks, out)
	}
}
