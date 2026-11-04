package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	zztgo "github.com/shotintoeternity/zztmmo/engine"
)

// m3627Server runs the real socket server and API in-process on TOWN, with
// /api/metrics answering only its first call: the "before" sample succeeds
// and every later one, including the final "after", fails.
func m3627Server(t *testing.T, failAfterFirst bool) *httptest.Server {
	t.Helper()
	e := zztgo.NewEngine()
	e.Headless = true
	zztgo.E = e // as zzt-server does
	zztgo.WorldCreate()
	base := filepath.Join("..", "..", "..", "fixtures", "TOWN")
	if !zztgo.WorldLoad(base, ".ZZT", false) {
		t.Fatalf("load %s.ZZT", base)
	}
	zztgo.E.World.Info.Name = "TOWN"
	server := zztgo.NewWebSocketServer(zztgo.E.World, zztgo.E.World.Info.CurrentBoard)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go server.Run(ctx)

	api := (&zztgo.WebAPI{RoomManager: server.RoomManager, World: zztgo.E.World, Server: server}).Handler()
	var metricsCalls atomic.Int32
	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		if metricsCalls.Add(1) > 1 && failAfterFirst {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		api.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// M36.27d: a failed final metrics fetch is reported, and the run's own
// measurements are still written out. main used to log.Fatal the error, so a
// whole run's numbers vanished over the last request.
func TestRunKeepsMeasurementsWhenFinalMetricsFail(t *testing.T) {
	srv := m3627Server(t, true)
	result, err := runLoad(srv.URL, "TOWN", 3, 10, 0)
	if err == nil {
		t.Fatal("runLoad hid the failed final /api/metrics fetch")
	}
	if result.MetricsAfterError == "" {
		t.Fatal("the result does not record why MetricsAfter is empty")
	}
	var out bytes.Buffer
	if code := emit(&out, result, err, true); code == 0 {
		t.Error("a failed final fetch exited 0")
	}
	if !strings.Contains(out.String(), `"totalDiffs"`) || result.TotalDiffs == 0 {
		t.Fatalf("the run's measurements were not written: diffs %d, output %q", result.TotalDiffs, out.String())
	}
}

// M36.27d: the during-run sampler is stopped and waited for before the result
// is returned. Run with -race: a sample in flight at shutdown used to append
// to result.MetricsDuring after runLoad had read it.
func TestSamplerIsJoinedBeforeTheResultIsRead(t *testing.T) {
	srv := m3627Server(t, false)
	for i := 0; i < 3; i++ {
		result, err := runLoad(srv.URL, "TOWN", 2, 5, 1_000_000) // 1ms: always mid-sample at shutdown
		if err != nil {
			t.Fatal(err)
		}
		if len(result.MetricsDuring) == 0 {
			t.Fatal("the sampler recorded nothing")
		}
	}
}
