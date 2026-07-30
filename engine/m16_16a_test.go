package zztgo

// M16.16a — chat admission and Museum cache-commit hardening.
//
// Chat: a message is persisted and broadcast only after normalization (at most
// 120 printable CP437 bytes, control/unmappable input removed, empty refused)
// and the rolling per-player rate limit (5 accepted per 10 seconds) both admit
// it. Refusals create no chat record and no broadcast. The rate window runs on
// the injected server clock, never the simulation's.
//
// Museum: archive caching is a post-validation commit. Every refusal — corrupt
// ZIP, unsafe entry, no .ZZT, missing selection, invalid selected world —
// creates neither a cache entry, a hosted .ZZT, nor a server instance.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestM1616aChatAdmissionBoundary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"plain", "hello", "hello", true},
		{"exact-120", strings.Repeat("a", 120), strings.Repeat("a", 120), true},
		{"truncate-121", strings.Repeat("a", 121), strings.Repeat("a", 120), true},
		{"truncate-exposes-trailing-space", strings.Repeat("a", 119) + " bb", strings.Repeat("a", 119), true},
		{"trims-ends", "  spaced out  ", "spaced out", true},
		{"empty", "", "", false},
		{"spaces-only", "   ", "", false},
		{"control-only", "\x01\x02\x1f\x7f", "", false},
		{"control-and-spaces-only", "\x01 \x02", "", false},
		{"control-stripped-inside", "a\x00b\tc\nd", "abcd", true},
		{"unmappable-dropped", "héllo", "hllo", true},
		{"emoji-only", "\U0001f642\U0001f642", "", false},
		{"typographic-folds", "“hi”—ok", `"hi"-ok`, true},
	}
	for _, tc := range cases {
		got, ok := admitChatText(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: admitChatText(%q) = (%q, %v), want (%q, %v)", tc.name, tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestM1616aChatRateLimiterRollingWindow(t *testing.T) {
	var l chatRateLimiter
	base := time.Unix(1000, 0)
	id := PlayerID(7)

	for i := 0; i < chatRateLimitMax; i++ {
		if !l.allow(id, base) {
			t.Fatalf("message %d at base refused, want first %d accepted", i+1, chatRateLimitMax)
		}
	}
	if l.allow(id, base) {
		t.Fatal("sixth message at base accepted, want refused")
	}
	if l.allow(id, base.Add(chatRateLimitWindow-time.Nanosecond)) {
		t.Fatal("message just inside the window accepted, want refused")
	}
	if !l.allow(id, base.Add(chatRateLimitWindow)) {
		t.Fatal("message exactly one window later refused, want accepted (base entries expired)")
	}

	// Refusals never consume window slots: another player's window is
	// independent, and the refused attempts above left id with one accepted
	// entry at base+window, so four more fit.
	other := PlayerID(8)
	if !l.allow(other, base) {
		t.Fatal("independent player refused, want accepted")
	}
	for i := 0; i < chatRateLimitMax-1; i++ {
		if !l.allow(id, base.Add(chatRateLimitWindow)) {
			t.Fatalf("post-expiry message %d refused, want accepted", i+2)
		}
	}
	if l.allow(id, base.Add(chatRateLimitWindow)) {
		t.Fatal("sixth post-expiry message accepted, want refused")
	}

	l.forget(id)
	if !l.allow(id, base.Add(chatRateLimitWindow)) {
		t.Fatal("message after forget refused, want a fresh window")
	}
}

// TestM1616aChatRefusalPersistsAndBroadcastsNothing drives real WebSocket
// connections through the production handler with an injected clock. The clock
// counts its own calls, and the handler consults it exactly once per
// normalization-admitted message — so waiting on the call count is a
// deterministic fence for "the server has processed that message".
func TestM1616aChatRefusalPersistsAndBroadcastsNothing(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	db := NewMemChatDatabase()
	server.ChatDB = db

	base := time.Unix(5000, 0)
	var clockNanos atomic.Int64
	var clockCalls atomic.Int64
	clockNanos.Store(base.UnixNano())
	server.Now = func() time.Time {
		clockCalls.Add(1)
		return time.Unix(0, clockNanos.Load())
	}

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	sender, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Talky", Board: 1}, nil)
	defer sender.Close(websocket.StatusNormalClosure, "")
	watcher, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Watch", Board: 1}, nil)
	defer watcher.Close(websocket.StatusNormalClosure, "")

	send := func(text string) {
		t.Helper()
		if err := wsjson.Write(ctx, sender, map[string]string{"type": "chat", "text": text}); err != nil {
			t.Fatalf("write chat %q: %v", text, err)
		}
	}
	waitClockCalls := func(n int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for clockCalls.Load() < n {
			if time.Now().After(deadline) {
				t.Fatalf("clock calls=%d, want >=%d", clockCalls.Load(), n)
			}
			time.Sleep(time.Millisecond)
		}
	}

	// Phase 1: a normalization refusal, then five admitted messages — the
	// fifth oversized so truncation is visible end to end.
	long := strings.Repeat("x", 121)
	send("\x01\x02")
	for _, text := range []string{"one", "two", "three", "four", long} {
		send(text)
	}
	wantTexts := []string{"one", "two", "three", "four", strings.Repeat("x", 120)}
	for i, want := range wantTexts {
		chat := readChatMessage(t, ctx, watcher)
		if chat.From != "Talky" || chat.Text != want {
			t.Fatalf("broadcast %d = %q/%q, want Talky/%q", i+1, chat.From, chat.Text, want)
		}
	}

	// Phase 2: another normalization refusal, then the sixth in-window
	// message, refused by rate. The sixth clock call proves both were
	// processed (the refusal precedes it on the same connection).
	send("\U0001f642")
	send("sixth")
	waitClockCalls(6)
	if recs, _ := db.GetRecentMessages(50); len(recs) != 5 {
		t.Fatalf("chat records after refusals=%d, want 5", len(recs))
	}

	// Phase 3: one full window later the slots are free again. The watcher's
	// next broadcast being "after" proves the refused messages were never
	// broadcast — same-sender broadcasts arrive in processing order.
	clockNanos.Store(base.Add(chatRateLimitWindow).UnixNano())
	send("after")
	chat := readChatMessage(t, ctx, watcher)
	if chat.Text != "after" {
		t.Fatalf("post-window broadcast=%q, want %q (a refused message leaked)", chat.Text, "after")
	}

	recs, _ := db.GetRecentMessages(50)
	if len(recs) != 6 {
		t.Fatalf("final chat records=%d, want 6", len(recs))
	}
	for i, want := range append(wantTexts, "after") {
		if recs[i].Text != want {
			t.Fatalf("record %d=%q, want %q", i, recs[i].Text, want)
		}
	}
}

// TestM1616aChatHistorySurvivesRestart proves admitted messages — and only
// admitted ones — persist across a FileChatDatabase reopen and replay to a
// client joining the restarted server.
func TestM1616aChatHistorySurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = db
	var clockCalls atomic.Int64
	server.Now = func() time.Time {
		clockCalls.Add(1)
		return time.Unix(6000, 0)
	}

	httpServer := httptest.NewServer(server)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Historian", Board: 1}, nil)
	for _, text := range []string{"\x03\x04", "keep one", "keep two"} {
		if err := wsjson.Write(ctx, conn, map[string]string{"type": "chat", "text": text}); err != nil {
			t.Fatalf("write chat: %v", err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for clockCalls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("clock calls=%d, want 2", clockCalls.Load())
		}
		time.Sleep(time.Millisecond)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	httpServer.Close()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	reopened, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("reopen FileChatDatabase: %v", err)
	}
	defer reopened.Close()
	recs, err := reopened.GetRecentMessages(50)
	if err != nil {
		t.Fatalf("GetRecentMessages: %v", err)
	}
	if len(recs) != 2 || recs[0].Text != "keep one" || recs[1].Text != "keep two" {
		t.Fatalf("restart history=%+v, want exactly the two admitted messages", recs)
	}

	// A client joining the restarted server receives that history.
	restarted := NewWebSocketServer(testEmptyWorld(t), 1)
	restarted.ChatDB = reopened
	httpServer2 := httptest.NewServer(restarted)
	defer httpServer2.Close()
	wsURL2 := "ws" + strings.TrimPrefix(httpServer2.URL, "http")
	conn2, _ := dialJoinWithCookie(t, ctx, wsURL2, JoinMessage{Type: MessageTypeJoin, Name: "Reader", Board: 1}, nil)
	defer conn2.Close(websocket.StatusNormalClosure, "")
	for _, want := range []string{"keep one", "keep two"} {
		chat := readChatMessage(t, ctx, conn2)
		if chat.Text != want {
			t.Fatalf("history replay=%q, want %q", chat.Text, want)
		}
	}
}

// museumRefusalFixture builds the archive bytes for one refusal row.
type museumRefusalFixture struct {
	name    string
	payload func(t *testing.T) []byte
	zztFile string
}

func TestM1616aMuseumRefusalsCreateNothing(t *testing.T) {
	fixtures := []museumRefusalFixture{
		{name: "corrupt-zip", payload: func(t *testing.T) []byte { return []byte("not a zip") }},
		{name: "unsafe-entry", payload: func(t *testing.T) []byte {
			return museumTestZip(t, map[string][]byte{"../EVIL.ZZT": []byte("nope")})
		}},
		{name: "no-zzt-worlds", payload: func(t *testing.T) []byte {
			return museumTestZip(t, map[string][]byte{"README.TXT": []byte("just docs")})
		}},
		{name: "missing-selection", payload: func(t *testing.T) []byte {
			return museumTestZip(t, map[string][]byte{"GOOD.ZZT": []byte("never validated")})
		}, zztFile: "NOPE.ZZT"},
		{name: "invalid-selected-world", payload: func(t *testing.T) []byte {
			return museumTestZip(t, map[string][]byte{"BAD.ZZT": []byte("garbage world bytes")})
		}},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			payload := fx.payload(t)
			files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(payload)
			}))
			defer files.Close()

			server := NewWebSocketServer(testEmptyWorld(t), 1)
			server.WorldsDir = t.TempDir()
			museum := NewMuseumService(server)
			museum.FilesBaseURL = files.URL
			museum.Client = files.Client()
			museum.CacheDir = filepath.Join(t.TempDir(), "cache")
			museum.lastRequest = time.Now().Add(-museumRequestDelay)

			server.mu.Lock()
			instancesBefore := len(server.Instances)
			server.mu.Unlock()

			_, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "sus.zip", ZZTFile: fx.zztFile})
			if err == nil {
				t.Fatal("Play succeeded, want refusal")
			}

			if n := countFilesUnder(t, museum.CacheDir); n != 0 {
				t.Errorf("cache entries after refusal=%d, want 0", n)
			}
			if n := countFilesUnder(t, server.WorldsDir); n != 0 {
				t.Errorf("hosted .ZZT files after refusal=%d, want 0", n)
			}
			server.mu.Lock()
			instancesAfter := len(server.Instances)
			server.mu.Unlock()
			if instancesAfter != instancesBefore {
				t.Errorf("instances=%d, want unchanged %d", instancesAfter, instancesBefore)
			}
		})
	}
}

// TestM1616aMuseumChoicesThenSelectionCachesOnce proves both success commits:
// the multi-world choices response caches the validated archive, and the
// follow-up selection is served from that cache, hosts, and does not re-cache.
func TestM1616aMuseumChoicesThenSelectionCachesOnce(t *testing.T) {
	zipData := museumTestZip(t, map[string][]byte{
		"AAA.ZZT": museumTestWorldBytes(t, "AAA"),
		"BBB.ZZT": museumTestWorldBytes(t, "BBB"),
	})
	hits := 0
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(zipData)
	}))
	defer files.Close()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = t.TempDir()
	museum := NewMuseumService(server)
	museum.FilesBaseURL = files.URL
	museum.Client = files.Client()
	museum.CacheDir = filepath.Join(t.TempDir(), "cache")
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	choices, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "m", Filename: "multi.zip"})
	if err != nil {
		t.Fatalf("Play choices: %v", err)
	}
	if len(choices.Choices) != 2 {
		t.Fatalf("choices=%+v, want two", choices.Choices)
	}
	if n := countFilesUnder(t, museum.CacheDir); n != 1 {
		t.Fatalf("cache entries after choices=%d, want 1 (choices response commits)", n)
	}

	hosted, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "m", Filename: "multi.zip", ZZTFile: "AAA.ZZT"})
	if err != nil {
		t.Fatalf("Play selection: %v", err)
	}
	if hosted.World != "AAA" {
		t.Fatalf("world=%q, want AAA", hosted.World)
	}
	if hits != 1 {
		t.Fatalf("download hits=%d, want the selection served from cache", hits)
	}
	if _, err := os.Stat(filepath.Join(server.WorldsDir, "AAA.ZZT")); err != nil {
		t.Fatalf("hosted world missing: %v", err)
	}
	server.mu.Lock()
	_, ok := server.Instances["AAA"]
	server.mu.Unlock()
	if !ok {
		t.Fatal("AAA was not hosted as an instance")
	}
}

// TestM1616aMuseumOccupiedReplayKeepsHostedFile: replaying an occupied world
// fails to host, but must not delete the .ZZT (or cache entry) the earlier
// successful Play legitimately created.
func TestM1616aMuseumOccupiedReplayKeepsHostedFile(t *testing.T) {
	worldData := museumTestWorldBytes(t, "TEEN")
	zipData := museumTestZip(t, map[string][]byte{"TEEN.ZZT": worldData})
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipData)
	}))
	defer files.Close()

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = t.TempDir()
	museum := NewMuseumService(server)
	museum.FilesBaseURL = files.URL
	museum.Client = files.Client()
	museum.CacheDir = filepath.Join(t.TempDir(), "cache")
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	if _, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "teen.zip"}); err != nil {
		t.Fatalf("first Play: %v", err)
	}

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws?world=TEEN"
	conn, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Occupant", Board: 1}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	museum.lastRequest = time.Now().Add(-museumRequestDelay)
	if _, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "teen.zip"}); err == nil {
		t.Fatal("replay of occupied world succeeded, want error")
	}
	if _, err := os.Stat(filepath.Join(server.WorldsDir, "TEEN.ZZT")); err != nil {
		t.Fatalf("hosted TEEN.ZZT was deleted by the failed replay: %v", err)
	}
	if n := countFilesUnder(t, museum.CacheDir); n != 1 {
		t.Fatalf("cache entries=%d, want the original entry kept", n)
	}
}

// countFilesUnder counts regular files below dir; a dir that does not exist
// counts as empty.
func countFilesUnder(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			count++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return count
}
