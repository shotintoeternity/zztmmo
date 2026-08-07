package zztgo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// M18.20 — build identity reaches logs, health, and the title screen.

func TestM1820HealthReportsBuildCommit(t *testing.T) {
	oldCommit := BuildCommit
	BuildCommit = "abcdef1234567890"
	t.Cleanup(func() { BuildCommit = oldCommit })

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	mux := (&WebAPI{Server: server}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health = %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Build  struct {
			Commit string `json:"commit"`
			Short  string `json:"short"`
		} `json:"build"`
		Totals ServiceTotals `json:"totals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /api/health: %v", err)
	}
	if body.Status != "ok" || body.Build.Commit != "abcdef1234567890" || body.Build.Short != "abcdef1" {
		t.Fatalf("/api/health build = %+v", body)
	}
	if body.Totals.Instances < 1 {
		t.Fatalf("/api/health lost aggregate totals: %+v", body.Totals)
	}
}

func TestM1820ServerBinaryLogsLdflagsBuildCommit(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "zzt-server")
	const commit = "m1820ldflags"
	cmd := exec.Command("go", "build", "-ldflags", "-X github.com/shotintoeternity/zztmmo/engine.BuildCommit="+commit, "-o", binPath, "./cmd/zzt-server")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build with commit stamp failed: %v\nOutput:\n%s", err, string(out))
	}

	run := exec.Command(binPath, "-world", "MISSING", "-web", tmpDir, "-worlds", tmpDir, "-help", ".", "-shutdown-grace", "0s")
	run.Dir = tmpDir
	out, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("server unexpectedly started with missing world; output:\n%s", string(out))
	}
	logs := string(out)
	if !strings.Contains(logs, "zztmmo build commit="+commit) {
		t.Fatalf("startup log did not include build commit %q:\n%s", commit, logs)
	}
	if !strings.Contains(logs, "load MISSING.ZZT failed") {
		t.Fatalf("missing-world control did not run as expected:\n%s", logs)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("stamped binary was not written: %v", err)
	}
}
