package zztgo

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemChatDatabase(t *testing.T) {
	db := NewMemChatDatabase()
	defer db.Close()

	rec1, err := db.AddMessage(ChatAuthor{Name: "alice"}, "hello")
	if err != nil {
		t.Fatalf("AddMessage failed: %v", err)
	}
	if rec1.From != "alice" || rec1.Text != "hello" {
		t.Errorf("Unexpected record content: %+v", rec1)
	}

	_, err = db.AddMessage(ChatAuthor{Name: "bob"}, "hi")
	if err != nil {
		t.Fatalf("AddMessage failed: %v", err)
	}

	recs, err := db.GetRecentMessages(10)
	if err != nil {
		t.Fatalf("GetRecentMessages failed: %v", err)
	}
	if len(recs) != 2 {
		t.Errorf("Expected 2 records, got %d", len(recs))
	}
	if recs[0].From != "alice" || recs[1].From != "bob" {
		t.Errorf("Unexpected records: %+v", recs)
	}

	recs, err = db.GetRecentMessages(1)
	if err != nil {
		t.Fatalf("GetRecentMessages failed: %v", err)
	}
	if len(recs) != 1 {
		t.Errorf("Expected 1 record, got %d", len(recs))
	}
	if recs[0].From != "bob" {
		t.Errorf("Expected bob, got %+v", recs[0])
	}
}

func TestFileChatDatabase(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "zzt-chat-test")
	if err != nil {
		t.Fatalf("TempDir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "chat.jsonl")

	db, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewFileChatDatabase failed: %v", err)
	}

	_, err = db.AddMessage(ChatAuthor{Name: "alice"}, "hello")
	if err != nil {
		t.Fatalf("AddMessage failed: %v", err)
	}
	_, err = db.AddMessage(ChatAuthor{Name: "bob"}, "hi")
	if err != nil {
		t.Fatalf("AddMessage failed: %v", err)
	}
	db.Close()

	// Reopen to verify persistence/restore
	db2, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewFileChatDatabase reopen failed: %v", err)
	}
	defer db2.Close()

	recs, err := db2.GetRecentMessages(10)
	if err != nil {
		t.Fatalf("GetRecentMessages failed: %v", err)
	}
	if len(recs) != 2 {
		t.Errorf("Expected 2 records, got %d", len(recs))
	}
	if recs[0].From != "alice" || recs[1].From != "bob" {
		t.Errorf("Unexpected records: %+v", recs)
	}
}

// A database that has been closed must not put files back on disk.
//
// The append log needs no such guard: writes to a closed *os.File fail on their
// own. The player-state and account-preference files are the problem, because
// they are written by path through a temp file and a rename, so closing the
// handle leaves them perfectly writable. A connection goroutine that outlived
// its server -- an httptest server does not wait for hijacked WebSocket conns --
// could therefore recreate both files in a directory already being torn down,
// and t.TempDir's cleanup would fail with "directory not empty". Two M21 block
// tests failed that way on and off, and the suite reported them as broken block
// features rather than as a stale goroutine, which is the expensive part.
func TestFileChatDatabaseWritesNothingAfterClose(t *testing.T) {
	dir := t.TempDir()
	db, err := NewFileChatDatabase(filepath.Join(dir, "chat.jsonl"))
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	if err := db.PutPlayerState("google:ada", "TOWN", PlayerState{Health: 100}); err != nil {
		t.Fatalf("PutPlayerState: %v", err)
	}
	if err := db.PutAccountPreferences("google:ada", AccountPreferences{Color: "#ff00ff"}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Stand in for t.TempDir's own cleanup: take the directory back to empty,
	// then let the stragglers arrive.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			t.Fatalf("clearing %s: %v", entry.Name(), err)
		}
	}

	if err := db.PutPlayerState("google:ada", "TOWN", PlayerState{Health: 50}); !errors.Is(err, ErrChatDatabaseClosed) {
		t.Errorf("PutPlayerState after Close returned %v, want ErrChatDatabaseClosed", err)
	}
	if err := db.PutAccountPreferences("google:ada", AccountPreferences{Color: "#00ff00"}); !errors.Is(err, ErrChatDatabaseClosed) {
		t.Errorf("PutAccountPreferences after Close returned %v, want ErrChatDatabaseClosed", err)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(left) != 0 {
		names := make([]string, 0, len(left))
		for _, entry := range left {
			names = append(names, entry.Name())
		}
		t.Errorf("a closed database wrote %v back into a directory being torn down", names)
	}
}

// A crash partway through an append leaves a half-written last line. Loading
// must skip it and finish (a json.Decoder loop spun on it forever), and the next
// append must start a fresh line rather than join the broken one.
func TestFileChatDatabaseSurvivesTruncatedLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.log")
	good := `{"from":"alice","text":"hello","timestamp":"2026-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(good+`{"from":"bob","te`), 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var db *FileChatDatabase
	var err error
	go func() { db, err = NewFileChatDatabase(path); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("NewFileChatDatabase did not return on a truncated log")
	}
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	if _, err := db.AddMessage(ChatAuthor{Name: "carol"}, "after"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	db.Close()

	db, err = NewFileChatDatabase(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	recs, _ := db.GetRecentMessages(10)
	if len(recs) != 2 || recs[0].From != "alice" || recs[1].From != "carol" {
		t.Fatalf("records after reload = %+v, want alice then carol", recs)
	}
}

// A prefs or player-state document that does not parse starts the store empty,
// as before, but is moved aside first: otherwise the next save would replace it
// and every account's data in it would be lost.
func TestFileChatDatabaseSetsAsideCorruptStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.log")
	for _, side := range []string{".accountprefs.json", ".playerstate.json"} {
		if err := os.WriteFile(path+side, []byte(`{"acct": {"col`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := NewFileChatDatabase(path)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	defer db.Close()
	if err := db.PutAccountPreferences("acct", AccountPreferences{}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	if err := db.PutPlayerState("acct", "TOWN", PlayerState{}); err != nil {
		t.Fatalf("PutPlayerState: %v", err)
	}
	for _, side := range []string{".accountprefs.json", ".playerstate.json"} {
		kept, err := os.ReadFile(path + side + ".corrupt")
		if err != nil || string(kept) != `{"acct": {"col` {
			t.Errorf("%s: corrupt original not kept aside (%q, %v)", side, kept, err)
		}
	}
}
