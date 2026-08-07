package zztgo

// M22.3's browser half: /replay/<id> opens a deterministic recording in the
// production server and plays it to the end for a read-only browser.

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestM223ReplayViewerJourney(t *testing.T) {
	m169RequireBrowserHarness(t)

	const replayID = "TOWN-20260807-120000"
	recording, finalTick, _ := m223Recording(t, replayID)
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
	replayDir := filepath.Join(rootDir, "replays")
	for _, dir := range []string{savesDir, worldsDir, replayDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(worldsDir, "TOWN.ZZT"), townBytes, 0o644); err != nil {
		t.Fatalf("write TOWN.ZZT: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "TOWN.ZZT"), townBytes, 0o644); err != nil {
		t.Fatalf("write root TOWN.ZZT: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, replayID+".jsonl"), recording, 0o644); err != nil {
		t.Fatalf("write replay: %v", err)
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
		"-replay", replayDir,
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

	resp, err := http.Get(baseURL + "/replay/" + replayID)
	if err != nil {
		t.Fatalf("GET /replay/%s: %v", replayID, err)
	}
	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /replay/%s status=%d, want 200: %s", replayID, resp.StatusCode, body[:n])
	}
	if !bytes.Contains(body[:n], []byte("<canvas")) && !bytes.Contains(body[:n], []byte("<script")) {
		t.Fatalf("GET /replay/%s did not answer with the client: %s", replayID, body[:n])
	}

	nodeCmd := exec.Command("node", filepath.Join("test", "replay_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(),
		"BASE_URL="+baseURL,
		"REPLAY_ID="+replayID,
		"REPLAY_FINAL_TICK="+strconv.Itoa(finalTick),
	)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("replay browser suite failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("replay browser suite output:\n%s", string(out))
}
