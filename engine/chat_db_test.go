package zztgo

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestMemChatDatabase(t *testing.T) {
	db := NewMemChatDatabase()
	defer db.Close()

	rec1, err := db.AddMessage("alice", "hello")
	if err != nil {
		t.Fatalf("AddMessage failed: %v", err)
	}
	if rec1.From != "alice" || rec1.Text != "hello" {
		t.Errorf("Unexpected record content: %+v", rec1)
	}

	_, err = db.AddMessage("bob", "hi")
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

	_, err = db.AddMessage("alice", "hello")
	if err != nil {
		t.Fatalf("AddMessage failed: %v", err)
	}
	_, err = db.AddMessage("bob", "hi")
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
