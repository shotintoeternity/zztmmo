package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubSleep swaps the retry wait for a recorder, so the policy can be read off
// the delays it asks for rather than waited through.
func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	previous := backoffSleep
	backoffSleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { backoffSleep = previous })
	return &waits
}

func TestGetWithBackoffRetriesUntilServerRecovers(t *testing.T) {
	waits := stubSleep(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	resp, err := getWithBackoff(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("getWithBackoff: %v", err)
	}
	resp.Body.Close()
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	want := []time.Duration{backoffInitial, 2 * backoffInitial}
	if len(*waits) != len(want) {
		t.Fatalf("waits = %v, want %v", *waits, want)
	}
	for i := range want {
		if (*waits)[i] != want[i] {
			t.Fatalf("waits = %v, want %v", *waits, want)
		}
	}
}

func TestGetWithBackoffGivesUpAfterMaxAttempts(t *testing.T) {
	waits := stubSleep(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	if _, err := getWithBackoff(server.Client(), server.URL); err == nil {
		t.Fatal("getWithBackoff succeeded on a server that never recovers")
	}
	if calls != maxAttempts {
		t.Fatalf("calls = %d, want %d", calls, maxAttempts)
	}
	if len(*waits) != maxAttempts-1 {
		t.Fatalf("waits = %v, want %d of them", *waits, maxAttempts-1)
	}
	// The last wait is capped rather than doubling forever.
	for _, wait := range *waits {
		if wait > backoffMax {
			t.Fatalf("wait %s exceeds the %s cap", wait, backoffMax)
		}
	}
}

// A missing zip is a manifest bug, and no amount of waiting will grow one.
func TestGetWithBackoffDoesNotRetryNotFound(t *testing.T) {
	waits := stubSleep(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := getWithBackoff(server.Client(), server.URL); err == nil {
		t.Fatal("getWithBackoff succeeded on a 404")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if len(*waits) != 0 {
		t.Fatalf("waits = %v, want none", *waits)
	}
}

func TestGetWithBackoffObeysRetryAfter(t *testing.T) {
	waits := stubSleep(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	resp, err := getWithBackoff(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("getWithBackoff: %v", err)
	}
	resp.Body.Close()
	if len(*waits) != 1 || (*waits)[0] != 7*time.Second {
		t.Fatalf("waits = %v, want [7s] -- the server's own number, not the computed one", *waits)
	}
}

func TestRetryAfterForms(t *testing.T) {
	fallback := 5 * time.Second
	if got := retryAfter("", fallback); got != fallback {
		t.Fatalf("absent header: got %s, want %s", got, fallback)
	}
	if got := retryAfter("not a number", fallback); got != fallback {
		t.Fatalf("nonsense header: got %s, want %s", got, fallback)
	}
	if got := retryAfter(" 12 ", fallback); got != 12*time.Second {
		t.Fatalf("seconds header: got %s, want 12s", got)
	}
	// An HTTP date in the past means "now", not a negative wait.
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := retryAfter(past, fallback); got != 0 {
		t.Fatalf("past date: got %s, want 0", got)
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	got := retryAfter(future, fallback)
	if got < 80*time.Second || got > 90*time.Second {
		t.Fatalf("future date: got %s, want about 90s", got)
	}
}
