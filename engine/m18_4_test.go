package zztgo

// M18.4 — beta operational readiness.
//
// Two of the four items have code behind them and are covered here:
//
//   (a) the in-game feedback pointer. BETA.HLP ships beside the other .HLP
//       files and is reachable over the same /api/help route the About window
//       uses, so a tester who presses F on the title screen gets the report
//       address rather than "not available".
//   (c) the generation limits actually enforced on the beta key. The per-client
//       pace already existed but keyed on RemoteAddr, which is the loopback
//       address for every request once the server sits behind Caddy; the spend
//       ceiling did not exist at all.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestM184FeedbackHelpFileIsServed(t *testing.T) {
	oldDir := HelpDir
	HelpDir = "."
	defer func() { HelpDir = oldDir }()

	mux := (&WebAPI{}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/help?file=BETA.HLP&title=ZZTMMO+Beta", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/help?file=BETA.HLP = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "github.com/shotintoeternity/zztmmo/issues") {
		t.Fatalf("BETA.HLP does not carry the report address: %s", body)
	}

	// The whole point of the file is that a tester can read it, so no line may
	// run off the CP437 text window (TextWindowInit(5, 3, 50, 18) leaves 45
	// columns inside the frame).
	for _, line := range HelpFileLines("BETA.HLP") {
		if len(line) > 45 {
			t.Fatalf("BETA.HLP line is %d columns, wider than the text window: %q", len(line), line)
		}
	}
}

func TestM184GenerationDailyCeiling(t *testing.T) {
	service, err := NewGenerationService(GenerationConfig{
		APIURL: "http://example.invalid", APIKey: "x", Model: "x", MaxTokens: 1,
		MaxConcurrent: 4, RateLimit: -1, DailyMax: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	// RateLimit: -1 takes the per-client pace out of the way; this is the
	// server-wide ceiling, so the two admissions come from different clients.
	if err := service.admit(context.Background(), "alice"); err != nil {
		t.Fatalf("first admission failed: %v", err)
	}
	if err := service.admit(context.Background(), "bob"); err != nil {
		t.Fatalf("second admission failed: %v", err)
	}
	if err := service.admit(context.Background(), "carol"); err != ErrGenerationBudget {
		t.Fatalf("third admission = %v, want ErrGenerationBudget", err)
	}
	// A refused request must not have cost the caller its cooldown stamp, and
	// must not have consumed a slot of the day's budget either.
	if _, stamped := service.lastByClient["carol"]; stamped {
		t.Fatal("a request refused by the ceiling started the client's cooldown")
	}
	if len(service.admittedDay) != 2 {
		t.Fatalf("admittedDay = %d entries, want 2", len(service.admittedDay))
	}
	// The window rolls: an admission that has aged out frees its slot.
	service.mu.Lock()
	service.admittedDay[0] = time.Now().Add(-generationDailyWindow - time.Minute)
	service.mu.Unlock()
	if err := service.admit(context.Background(), "carol"); err != nil {
		t.Fatalf("admission after the window rolled = %v, want nil", err)
	}
	for i := 0; i < 3; i++ {
		<-service.sem
	}
}

func TestM184GenerationDailyCeilingDefaultsAndOptOut(t *testing.T) {
	base := GenerationConfig{APIURL: "http://example.invalid", APIKey: "x", Model: "x", MaxTokens: 1, MaxConcurrent: 1}
	service, err := NewGenerationService(base)
	if err != nil {
		t.Fatal(err)
	}
	if service.dailyMax != GenerationDailyMaxDefault {
		t.Fatalf("unconfigured dailyMax = %d, want the %d default", service.dailyMax, GenerationDailyMaxDefault)
	}
	off := base
	off.DailyMax = -1
	service, err = NewGenerationService(off)
	if err != nil {
		t.Fatal(err)
	}
	if service.dailyMax > 0 {
		t.Fatalf("negative DailyMax left a ceiling of %d", service.dailyMax)
	}
}

func TestM184GenerationClientKeyBehindProxy(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{"direct peer, no header", "203.0.113.7:51000", "", "203.0.113.7"},
		{
			// A direct client cannot talk its way out of the rate limit by
			// claiming to be someone else.
			"direct peer forging the header", "203.0.113.7:51000", "198.51.100.9", "203.0.113.7",
		},
		{"behind the loopback proxy", "127.0.0.1:51000", "198.51.100.9", "198.51.100.9"},
		{
			// Caddy appends the peer it saw to whatever arrived, so the last
			// hop is the only trustworthy one.
			"proxied client forging earlier hops", "127.0.0.1:51000", "1.2.3.4, 198.51.100.9", "198.51.100.9",
		},
		{"proxy with no header", "127.0.0.1:51000", "", "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/generate", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			if got := generationClientKey(req); got != tc.want {
				t.Fatalf("generationClientKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestM184GenerationCeilingAnswers429(t *testing.T) {
	service, err := NewGenerationService(GenerationConfig{
		APIURL: "http://example.invalid", APIKey: "x", Model: "x", MaxTokens: 1,
		MaxConcurrent: 1, RateLimit: -1, DailyMax: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.admit(context.Background(), "someone"); err != nil {
		t.Fatal(err)
	}
	<-service.sem

	mux := (&WebAPI{Generator: service}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(`{"prompt":"a quiet clock tower"}`))
	req.RemoteAddr = "203.0.113.7:51000"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("generate past the ceiling = %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "daily limit") {
		t.Fatalf("429 body does not name the ceiling: %q", rec.Body.String())
	}
}
