package zztgo

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M36.21: a published world and a .HI list are replaced by temp + rename, so a
// write that dies midway (a crash, or ENOSPC on the 99%-full disk of
// 2026-11-02) leaves the previous file whole. Each test fails the write after
// half the bytes have reached the disk, the shape a full disk produces.

var errM3621DiskFull = errors.New("m36.21: no space left on device")

// m3621FailWritesMidway makes every atomic write put down half its payload and
// then fail, for the rest of the test.
func m3621FailWritesMidway(t *testing.T) {
	t.Helper()
	saved := atomicWriteTestHooks.writeData
	atomicWriteTestHooks.writeData = func(f *os.File, data []byte) error {
		if _, err := f.Write(data[:len(data)/2]); err != nil {
			return err
		}
		return errM3621DiskFull
	}
	t.Cleanup(func() { atomicWriteTestHooks.writeData = saved })
}

// m3621AssertNoTempLeft checks a failed write cleaned up after itself: a stray
// temp file in the worlds directory is disk the next write needs.
func m3621AssertNoTempLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("a failed write left %s behind", e.Name())
		}
	}
}

func TestM3621AFailedWriteKeepsTheOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "OLD.ZZT")
	old := []byte("the previous file, whole")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}

	m3621FailWritesMidway(t)
	if err := writeFileAtomic(path, bytes.Repeat([]byte("new"), 100), 0o644); !errors.Is(err, errM3621DiskFull) {
		t.Fatalf("writeFileAtomic = %v, want the write error", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Fatalf("the old file is now %q, want it untouched", got)
	}
	m3621AssertNoTempLeft(t, dir)
}

func TestM3621ASuccessfulWriteKeepsTheMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "NEW.ZZT")
	if err := writeFileAtomic(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// CreateTemp makes 0600; a published world must stay readable by the
	// backup job and the static file server, as os.WriteFile(0644) left it.
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	m3621AssertNoTempLeft(t, dir)
}

func TestM3621AnEditorPublishThatFailsMidwayKeepsThePublishedWorld(t *testing.T) {
	dir := t.TempDir()
	world := m1613EditorWorld(t)
	server := NewWebSocketServer(world, 0)
	server.WorldsDir = dir

	session := server.editorSessionForWorld("SALTCELL", world)
	author := &webSocketClient{accountID: "google:ada", name: "Ada Lovelace"}
	if err := session.Enter(author); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(author)

	if _, err := server.saveEditorWorld(author, session, "SALTCELL"); err != nil {
		t.Fatalf("first publish failed: %v", err)
	}
	path := filepath.Join(dir, "SALTCELL.ZZT")
	published, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// The author keeps editing, so the republish carries different bytes.
	if err := session.Apply(author, func(e *Engine) {
		e.World.Info.Name = "The Salt Cellar"
		e.World.Info.Gems = 77
	}); err != nil {
		t.Fatal(err)
	}
	m3621FailWritesMidway(t)
	if _, err := server.saveEditorWorld(author, session, "SALTCELL"); !errors.Is(err, errM3621DiskFull) {
		t.Fatalf("republish = %v, want the write error reported", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, published) {
		t.Fatalf("the published world changed on a failed republish (%d bytes, was %d)", len(got), len(published))
	}
	if _, err := LoadWorldBytes(got); err != nil {
		t.Fatalf("the published world no longer loads: %v", err)
	}
	m3621AssertNoTempLeft(t, dir)
}

func TestM3621AHighScoreWriteThatFailsMidwayKeepsTheOldList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TOWN.HI")

	rm := NewRoomManager(testEmptyWorld(t))
	rm.HighScorePath = path
	rm.highScores[0] = THighScoreEntry{Name: "ACE", Score: 1234}
	rm.saveHighScores()
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the first write did not land: %v", err)
	}

	rm.highScores[0] = THighScoreEntry{Name: "NEWCOMER", Score: 9999}
	m3621FailWritesMidway(t)
	rm.saveHighScores()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Fatal("the .HI list changed on a failed write")
	}
	loader := NewRoomManager(testEmptyWorld(t))
	loader.HighScorePath = path
	loader.LoadHighScores()
	if e := loader.HighScores()[0]; e.Name != "ACE" || e.Score != 1234 {
		t.Errorf("the old list reads back as %q/%d, want ACE/1234", e.Name, e.Score)
	}
	m3621AssertNoTempLeft(t, dir)
}
