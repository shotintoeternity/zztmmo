package zztgo

// M34.1a — the two callers M34.1 did not read.
//
// The claims:
//
//  1. A dream that only ever landed because it was RETRIED is still news. The
//     hook M34.1 placed in runGenerationJob is not on the retry goroutine's
//     path, so a world rescued by a retry was invisible to the Gazette.
//  2. A dream is news exactly once. A salvaged job is "complete" and still
//     retryable (M17.13), so it has already been recorded before its retry
//     runs — and a retry may salvage again, so "recorded" is a fact the job
//     carries rather than something inferred from its status.
//  3. The account credited is the one that ASKED for the world, not whoever
//     POSTs the retry: the retry endpoint authorizes nobody, and every
//     ownership decision about the file (refuseIfNotOurs, claimGeneratedWorld)
//     is taken on behalf of the original requester.
//  4. A clean shutdown files the paper. Thirty seconds of counts is what a
//     crash costs; a planned restart should cost nothing.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// m341aRetry POSTs a retry of one async job and insists it was accepted.
func m341aRetry(t *testing.T, handler http.Handler, id string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(`{"retry":"`+id+`"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry %s = %d: %s", id, rec.Code, rec.Body.String())
	}
}

// m341aJobRecorded reads the job's once-only flag. The test asserts on the flag
// rather than on the job's status because the two are deliberately different
// facts: a salvaged job is complete and still retryable.
func m341aJobRecorded(t *testing.T, api *WebAPI, id string) bool {
	t.Helper()
	api.generationMu.Lock()
	defer api.generationMu.Unlock()
	job := api.generationJobs[id]
	if job == nil {
		t.Fatalf("no job %s", id)
	}
	return job.recorded
}

// A job that never landed a world is rescued by a retry, and that retry is the
// moment the world became news. The retry POST here is anonymous — nothing
// about it names Ada — and the row is Ada's anyway, because the job remembers
// who asked for the dream and the file on disk is claimed for her either way.
//
// The retry salvages a second time before it finally succeeds, which is the
// case that separates a real once-only flag from "complete means recorded": the
// first retry is what records, and the second must add nothing.
func TestM341ARetryThatRescuesAFailedJobIsNewsExactlyOnce(t *testing.T) {
	plan := generationPlan("1. start: begin. #endgame")
	// Generation order is start -> title. Start exhausts its two attempts and is
	// stubbed, Title paints, and the world is salvaged; the first retry exhausts
	// two more and stubs Start again; the second retry paints it.
	_, claude := newFakeClaude(t, plan,
		"bad", "still bad", generatedBoard("Title", false),
		"bad again", "still bad again",
		generatedBoard("Start", false))
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 2)
	api, _, ledger := m341API(t)
	api.Generator = service
	handler := api.Handler()
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}

	// The state M12.22 built the retry endpoint for: a job that failed with a
	// board-scoped error and kept its resume state. Nothing was recorded,
	// because nothing was generated.
	salvaged, err := service.GenerateRequest(context.Background(), GenerationRequest{
		Client: "test", Account: account, Premise: "rescue me", Name: "RESCUEA",
	})
	if err != nil {
		t.Fatalf("first pass should have salvaged: %v", err)
	}
	if salvaged.Retry == nil {
		t.Fatalf("first pass left nothing to retry: %+v", salvaged)
	}
	api.generationMu.Lock()
	api.generationJobs = map[string]*generationJob{"gen-1": {Status: "running", account: account}}
	api.generationMu.Unlock()
	api.finishGenerationJob("gen-1", service, GenerationResult{}, salvaged.Retry)

	if got := len(ledger.Edition("", nil).Items); got != 0 {
		t.Fatalf("a failed job is already news: %+v", ledger.Edition("", nil).Items)
	}
	if m341aJobRecorded(t, api, "gen-1") {
		t.Fatal("a failed job is marked recorded")
	}

	// First retry: the world lands, one board still stubbed. This is the dream.
	m341aRetry(t, handler, "gen-1")
	job := waitForGenerationJob(t, handler, "gen-1", "complete")
	if !job.Retryable {
		t.Fatalf("the retry was expected to salvage again: %+v", job)
	}
	edition := ledger.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindDream, "RESCUEA", account.ID); got != 1 {
		t.Fatalf("dream rows for %s = %d, want 1: %+v", account.ID, got, edition.Items)
	}
	if !m341aJobRecorded(t, api, "gen-1") {
		t.Fatal("the rescued job is not marked recorded")
	}

	// Second retry: the stub is repainted. The same world is not news twice.
	m341aRetry(t, handler, "gen-1")
	job = waitForGenerationJobSettled(t, handler, "gen-1")
	if len(job.StubbedBoards) != 0 {
		t.Fatalf("the second retry left boards stubbed: %v", job.StubbedBoards)
	}
	edition = ledger.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindDream, "RESCUEA", account.ID); got != 1 {
		t.Fatalf("dream rows after the repaint = %d, want 1: %+v", got, edition.Items)
	}
	if got := len(edition.Items); got != 1 {
		t.Fatalf("edition has %d rows, want 1: %+v", got, edition.Items)
	}
}

// The whole browser journey for the salvage case: dream, get a playable world
// with stub rooms, repaint them. That world was news when it was salvaged, and
// the repaint is not a second dream.
func TestM341ARepaintOfASalvagedDreamIsNotNewsAgain(t *testing.T) {
	plan := generationPlan("1. start: begin. #endgame")
	_, claude := newFakeClaude(t, plan, "bad", "still bad", generatedBoard("Title", false), generatedBoard("Start", false))
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 2)
	api, _, ledger := m341API(t)
	api.Generator = service
	handler := api.Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/generate",
		strings.NewReader(`{"prompt":"salvage me","name":"SALVAGEA","async":true}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	job := waitForGenerationJob(t, handler, started.ID, "complete")
	if !job.Retryable || len(job.StubbedBoards) != 1 {
		t.Fatalf("salvaged job = %+v, want retryable with one stub", job)
	}
	// A guest dream is an unnamed row, which is a row.
	edition := ledger.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindDream, "SALVAGEA", ""); got != 1 {
		t.Fatalf("dream rows = %d, want 1: %+v", got, edition.Items)
	}

	m341aRetry(t, handler, started.ID)
	waitForGenerationJobSettled(t, handler, started.ID)

	edition = ledger.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindDream, "SALVAGEA", ""); got != 1 {
		t.Fatalf("dream rows after the repaint = %d, want 1 (the repaint is the same world): %+v", got, edition.Items)
	}
	if got := len(edition.Items); got != 1 {
		t.Fatalf("edition has %d rows, want 1: %+v", got, edition.Items)
	}
}

// The tick loop's shutdown branch — the one cmd/zzt-server's signal goroutine
// fires when it cancels the tick context — writes the ledger. The cadence is
// deliberately OFF here, so what is proven is the shutdown and not the timer.
func TestM341AShutdownFlushesTheLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gazette.json")
	world := testEmptyWorld(t)
	world.Info.Name = "GAZWORLD"
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour
	ledger, _ := m341Ledger(t, path)
	server.Gazette = ledger
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:ada")

	if server.GazetteFlushEveryTicks != 0 {
		t.Fatalf("GazetteFlushEveryTicks = %d, want the cadence off", server.GazetteFlushEveryTicks)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the ledger was on disk before the shutdown (stat err = %v)", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server.Run(ctx)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a clean shutdown did not write the ledger: %v", err)
	}
	reloaded, err := NewGazetteLedger(path, func() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	edition := reloaded.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindDeath, "TOWN", "google:ada"); got != 1 {
		t.Fatalf("reloaded death rows = %d, want 1: %+v", got, edition.Items)
	}
}
