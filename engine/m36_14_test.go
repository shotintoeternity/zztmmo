package zztgo

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// M36.14 — generation retry is admitted, owned and non-destructive.
//
// A retry resumes a salvaged or failed dream and spends model calls doing it.
// It used to skip admit(), so it paid no attention to the per-client pace or the
// daily spend ceiling; its route checked no identity, and job ids are gen-1,
// gen-2, …, so anyone could repaint anyone's dream as often as they liked. And
// it re-persisted the world, so a retry after the owner had edited and
// published that world wrote the dream back over their edits.
//
// Now: a retry is admitted like a generation; a signed-in dreamer's job is
// retried only by that account and a guest's only from the client key that
// started it (owner decision 2026-11-02); and a retry refuses to persist over a
// world whose bytes are no longer the ones this job wrote.

// m3614Salvage generates a world whose Start board is stubbed, so the result is
// complete and still retryable, with one more Start board queued on the fake
// model for the retry to paint.
func m3614Salvage(t *testing.T, cfg GenerationConfig, name string, account AuthenticatedAccount) (*GenerationService, *fakeClaude, GenerationResult) {
	t.Helper()
	plan := generationPlan("1. start: begin. #endgame")
	fake, claude := newFakeClaude(t, plan, "bad", "still bad", generatedBoard("Title", false), generatedBoard("Start", false))
	t.Cleanup(claude.Close)
	cfg.APIURL, cfg.APIKey, cfg.Model, cfg.MaxTokens = claude.URL, "test-key", "test-model", 6144
	cfg.MaxAttempts, cfg.MaxConcurrent = 2, 1
	if cfg.OutputDir == "" {
		cfg.OutputDir = t.TempDir()
	}
	service, err := NewGenerationService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	salvaged, err := service.GenerateRequest(context.Background(), GenerationRequest{
		Client: "192.0.2.1", Account: account, Premise: "salvage me", Name: name,
	})
	if err != nil {
		t.Fatalf("generation should have salvaged: %v", err)
	}
	if salvaged.Retry == nil {
		t.Fatalf("salvaged result carries no retry: %+v", salvaged)
	}
	return service, fake, salvaged
}

// TestM3614ARetryPastTheDailyCapIsRefused: the dream itself spent the only slot
// of the day, so the retry is refused before a single model call.
func TestM3614ARetryPastTheDailyCapIsRefused(t *testing.T) {
	service, fake, salvaged := m3614Salvage(t, GenerationConfig{RateLimit: -1, DailyMax: 1}, "CAPPED", AuthenticatedAccount{})
	calls := len(fake.requests)
	_, err := service.RetryBoard(context.Background(), salvaged.Retry, nil)
	if !errors.Is(err, ErrGenerationBudget) {
		t.Fatalf("retry past the daily cap = %v, want ErrGenerationBudget", err)
	}
	if len(fake.requests) != calls {
		t.Errorf("the refused retry made %d model calls", len(fake.requests)-calls)
	}
}

// TestM3614ARetryIsNotPacedByTheCooldown: the cooldown paces new dreams, and a
// salvaged dream offers its repaint at once (owner decision 2026-11-02), so an
// immediate retry is admitted — and does not restart the client's cooldown.
func TestM3614ARetryIsNotPacedByTheCooldown(t *testing.T) {
	service, _, salvaged := m3614Salvage(t, GenerationConfig{RateLimit: time.Hour, DailyMax: -1}, "PROMPT", AuthenticatedAccount{})
	service.mu.Lock()
	stamped := service.lastByClient["192.0.2.1"]
	service.mu.Unlock()
	if _, err := service.RetryBoard(context.Background(), salvaged.Retry, nil); err != nil {
		t.Fatalf("an immediate retry = %v, want it admitted", err)
	}
	service.mu.Lock()
	after := service.lastByClient["192.0.2.1"]
	service.mu.Unlock()
	if !after.Equal(stamped) {
		t.Error("the retry restarted the client's cooldown")
	}
}

// TestM3614ARetryAfterAPublishIsRefused: the owner opened the dream in the
// editor and published over it. The retry must not write the dream back over
// their edits — and, refused before admission, it spends nothing.
func TestM3614ARetryAfterAPublishIsRefused(t *testing.T) {
	service, fake, salvaged := m3614Salvage(t, GenerationConfig{RateLimit: -1, DailyMax: -1}, "EDITED", AuthenticatedAccount{})
	path := filepath.Join(service.outputDir, "EDITED.ZZT")
	published := museumTestWorldBytes(t, "EDITED")
	if err := os.WriteFile(path, published, 0o644); err != nil {
		t.Fatal(err)
	}
	calls := len(fake.requests)
	_, err := service.RetryBoard(context.Background(), salvaged.Retry, nil)
	if !errors.Is(err, ErrGeneratedWorldModified) {
		t.Fatalf("retry over a published world = %v, want ErrGeneratedWorldModified", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, published) {
		t.Error("the retry overwrote the published world")
	}
	if len(fake.requests) != calls {
		t.Errorf("the refused retry made %d model calls", len(fake.requests)-calls)
	}

	// A world that is gone is not the world this job wrote either.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryBoard(context.Background(), salvaged.Retry, nil); !errors.Is(err, ErrGeneratedWorldModified) {
		t.Fatalf("retry over a deleted world = %v, want ErrGeneratedWorldModified", err)
	}
}

// m3614RetryAPI seeds one retryable job in a WebAPI with auth on.
func m3614RetryAPI(t *testing.T, service *GenerationService, salvaged GenerationResult, owner AuthenticatedAccount, client string) (*WebAPI, *AuthService, http.Handler) {
	t.Helper()
	world := testEmptyWorld(t)
	world.Info.Name = "M3614"
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour
	server.ChatDB = NewMemChatDatabase()
	api := &WebAPI{RoomManager: server.RoomManager, Server: server}
	auth := NewAuthService("m3614-client-id", "", "", []byte("m3614-route-cookie-secret"))
	server.Auth = auth
	api.Generator = service
	api.generationMu.Lock()
	api.generationJobs = map[string]*generationJob{"gen-1": {Status: "running", account: owner, client: client}}
	api.generationMu.Unlock()
	api.finishGenerationJob("gen-1", service, salvaged, nil)
	return api, auth, api.Handler()
}

func m3614PostRetry(t *testing.T, handler http.Handler, auth *AuthService, account AuthenticatedAccount, remote string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(`{"retry":"gen-1"}`))
	req.RemoteAddr = remote
	if account.ID != "" {
		req.AddCookie(signedAuthCookie(t, auth, account))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func m3614AssertStillRetryable(t *testing.T, api *WebAPI) {
	t.Helper()
	api.generationMu.Lock()
	defer api.generationMu.Unlock()
	job := api.generationJobs["gen-1"]
	if job.Status != "complete" || !job.Retryable || job.resume == nil || job.generator == nil {
		t.Fatalf("job after a refused retry = status %q retryable %v resume %v; want it untouched", job.Status, job.Retryable, job.resume != nil)
	}
}

// TestM3614ARetryNeedsTheDreamsAccount: a signed-in dreamer's job is retried by
// that account only. Anonymous and other-account retries are refused and leave
// the job exactly as retryable as it was.
func TestM3614ARetryNeedsTheDreamsAccount(t *testing.T) {
	ada := AuthenticatedAccount{ID: "acct-ada", Name: "Ada"}
	service, _, salvaged := m3614Salvage(t, GenerationConfig{RateLimit: -1, DailyMax: -1}, "ADADREAM", ada)
	api, auth, handler := m3614RetryAPI(t, service, salvaged, ada, "192.0.2.1")

	if rec := m3614PostRetry(t, handler, auth, AuthenticatedAccount{}, "192.0.2.1:1234"); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous retry of Ada's dream = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	m3614AssertStillRetryable(t, api)
	intruder := AuthenticatedAccount{ID: "acct-intruder", Name: "Intruder"}
	if rec := m3614PostRetry(t, handler, auth, intruder, "192.0.2.1:1234"); rec.Code != http.StatusForbidden {
		t.Errorf("another account's retry of Ada's dream = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	m3614AssertStillRetryable(t, api)

	if rec := m3614PostRetry(t, handler, auth, ada, "203.0.113.9:1"); rec.Code != http.StatusAccepted {
		t.Fatalf("Ada's own retry, from anywhere = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	waitForGenerationJobSettled(t, handler, "gen-1")
}

// TestM3614AGuestRetryNeedsTheGuestsClient: a guest's job is bound to the
// client key that started it — the same key the rate limiter paces.
func TestM3614AGuestRetryNeedsTheGuestsClient(t *testing.T) {
	service, _, salvaged := m3614Salvage(t, GenerationConfig{RateLimit: -1, DailyMax: -1}, "GUESTDRM", AuthenticatedAccount{})
	api, auth, handler := m3614RetryAPI(t, service, salvaged, AuthenticatedAccount{}, "192.0.2.1")

	if rec := m3614PostRetry(t, handler, auth, AuthenticatedAccount{}, "198.51.100.7:1"); rec.Code != http.StatusForbidden {
		t.Errorf("another client's retry of a guest dream = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	m3614AssertStillRetryable(t, api)
	// Signing in does not make a stranger the guest who dreamed it.
	if rec := m3614PostRetry(t, handler, auth, AuthenticatedAccount{ID: "acct-bo"}, "198.51.100.7:1"); rec.Code != http.StatusForbidden {
		t.Errorf("a signed-in stranger's retry of a guest dream = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	m3614AssertStillRetryable(t, api)

	if rec := m3614PostRetry(t, handler, auth, AuthenticatedAccount{}, "192.0.2.1:5555"); rec.Code != http.StatusAccepted {
		t.Fatalf("the guest's own retry = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	job := waitForGenerationJobSettled(t, handler, "gen-1")
	if len(job.StubbedBoards) != 0 {
		t.Errorf("the guest's retry left %v stubbed", job.StubbedBoards)
	}
}

// TestM3614AQuotaRefusalKeepsTheJobRetryable: a retry refused by the daily
// ceiling is answered 429 on the request, which the browser shows, and the job
// is left exactly as retryable as it was for when the window opens. (Polling a
// restored "complete" job would have read as a finished repaint.)
func TestM3614AQuotaRefusalKeepsTheJobRetryable(t *testing.T) {
	service, fake, salvaged := m3614Salvage(t, GenerationConfig{RateLimit: -1, DailyMax: 1}, "QUOTA", AuthenticatedAccount{})
	api, auth, handler := m3614RetryAPI(t, service, salvaged, AuthenticatedAccount{}, "192.0.2.1")
	calls := len(fake.requests)
	rec := m3614PostRetry(t, handler, auth, AuthenticatedAccount{}, "192.0.2.1:1234")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "daily limit") {
		t.Fatalf("retry past the daily cap = %d %q, want 429 naming the daily limit", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	m3614AssertStillRetryable(t, api)
	if len(fake.requests) != calls {
		t.Errorf("the refused retry made %d model calls", len(fake.requests)-calls)
	}
}

// TestM3614ARetryAfterAPublishIsRefusedOnTheRoute: the same refusal as the
// service-level test, answered 409 on the request with the job untouched.
func TestM3614ARetryAfterAPublishIsRefusedOnTheRoute(t *testing.T) {
	service, _, salvaged := m3614Salvage(t, GenerationConfig{RateLimit: -1, DailyMax: -1}, "EDITED2", AuthenticatedAccount{})
	api, auth, handler := m3614RetryAPI(t, service, salvaged, AuthenticatedAccount{}, "192.0.2.1")
	path := filepath.Join(service.outputDir, "EDITED2.ZZT")
	published := museumTestWorldBytes(t, "EDITED2")
	if err := os.WriteFile(path, published, 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := m3614PostRetry(t, handler, auth, AuthenticatedAccount{}, "192.0.2.1:1234"); rec.Code != http.StatusConflict {
		t.Fatalf("retry over a published world = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	m3614AssertStillRetryable(t, api)
	if got, _ := os.ReadFile(path); !bytes.Equal(got, published) {
		t.Error("the retry overwrote the published world")
	}
}
