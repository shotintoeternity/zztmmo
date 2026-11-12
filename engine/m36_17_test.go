package zztgo

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m3617Saver is one browser on a server with saving enabled, driven the way
// the client drives a save: S, wait for savePrompt, answer it.
type m3617Saver struct {
	t        *testing.T
	ctx      context.Context
	server   *WebSocketServer
	conn     *websocket.Conn
	snapshot SnapshotMessage
	saves    string
	seq      uint64
}

func newM3617Saver(t *testing.T) *m3617Saver {
	t.Helper()
	saves := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.SavesDir = saves
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	conn, snapshot := dialSavePlayer(ctx, t, "ws"+strings.TrimPrefix(httpServer.URL, "http"))
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return &m3617Saver{t: t, ctx: ctx, server: server, conn: conn, snapshot: snapshot, saves: saves}
}

func (p *m3617Saver) pressS() {
	p.t.Helper()
	p.seq++
	if err := wsjson.Write(p.ctx, p.conn, InputMessage{Type: MessageTypeInput, PlayerID: p.snapshot.You.ID, Seq: p.seq, Key: 'S'}); err != nil {
		p.t.Fatalf("write S: %v", err)
	}
	if _, ok := readUntilProtocolEvent(p.ctx, p.t, p.conn, p.server, "savePrompt", 20); !ok {
		p.t.Fatal("no savePrompt event after pressing S")
	}
}

func (p *m3617Saver) sendName(name string) {
	p.t.Helper()
	if err := wsjson.Write(p.ctx, p.conn, SaveFilenameMessage{Type: MessageTypeSaveFilename, PlayerID: p.snapshot.You.ID, Name: name}); err != nil {
		p.t.Fatalf("write saveFilename: %v", err)
	}
}

func (p *m3617Saver) save(name string) ProtocolEvent {
	p.t.Helper()
	p.sendName(name)
	result, ok := readUntilProtocolEvent(p.ctx, p.t, p.conn, p.server, "saveResult", 20)
	if !ok {
		p.t.Fatalf("no saveResult for %q", name)
	}
	return result
}

func (p *m3617Saver) exists(name string) bool {
	_, err := os.Stat(filepath.Join(p.saves, name+".SAV"))
	return err == nil
}

// A saveFilename that answers no savePrompt writes nothing, and one prompt buys
// one save: the reply consumes it.
func TestM3617UnpromptedSaveIsRefused(t *testing.T) {
	p := newM3617Saver(t)

	result := p.save("UNASKED")
	if result.Error == "" {
		t.Fatal("an unprompted save was accepted")
	}
	if p.exists("UNASKED") {
		t.Fatal("an unprompted save wrote a file")
	}

	p.pressS()
	if result := p.save("ASKED"); result.Error != "" {
		t.Fatalf("a prompted save was refused: %q", result.Error)
	}
	if !p.exists("ASKED") {
		t.Fatal("a prompted save wrote no file")
	}

	// The prompt was answered; a second reply to it is unprompted.
	if result := p.save("AGAIN"); result.Error == "" {
		t.Fatal("one prompt bought two saves")
	}
	if p.exists("AGAIN") {
		t.Fatal("a second reply to one prompt wrote a file")
	}
}

// A player answering prompt after prompt is held to saveRateLimitMax saves per
// window, and gets them back once the window passes.
func TestM3617SaveBurstIsLimited(t *testing.T) {
	p := newM3617Saver(t)
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	p.server.Now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	for i := 0; i < saveRateLimitMax; i++ {
		name := "BURST" + string(rune('A'+i))
		p.pressS()
		if result := p.save(name); result.Error != "" {
			t.Fatalf("save %d of %d refused: %q", i+1, saveRateLimitMax, result.Error)
		}
	}
	p.pressS()
	result := p.save("OVER")
	if !strings.Contains(result.Error, "too often") {
		t.Fatalf("save past the limit: error %q, want a rate refusal", result.Error)
	}
	if p.exists("OVER") {
		t.Fatal("a save past the limit wrote a file")
	}

	clockMu.Lock()
	now = now.Add(saveRateLimitWindow)
	clockMu.Unlock()
	p.pressS()
	if result := p.save("LATER"); result.Error != "" {
		t.Fatalf("a save after the window was refused: %q", result.Error)
	}
}

// The world copy is taken under inst.mu and the file is written after it is
// released: while a save's write is parked, the room still ticks.
func TestM3617SaveWriteDoesNotBlockTheTick(t *testing.T) {
	p := newM3617Saver(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	p.server.testHooks.saveWrite = func() {
		close(entered)
		<-release
	}
	p.pressS()
	p.sendName("PARKED")

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the save never reached its write")
	}

	// Everything that takes inst.mu runs here, bounded by a timeout, so a save
	// that held the lock across its write fails this test rather than hanging it.
	inst := p.server.DefaultInstance
	var before, after int32
	ticked := make(chan struct{})
	go func() {
		inst.mu.Lock()
		before = int32(inst.RoomManager.rooms[1].Engine.CurrentTick)
		inst.mu.Unlock()
		p.server.Tick(p.ctx)
		p.server.Tick(p.ctx)
		inst.mu.Lock()
		after = int32(inst.RoomManager.rooms[1].Engine.CurrentTick)
		inst.mu.Unlock()
		close(ticked)
	}()
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the room's tick blocked on a save's write")
	}
	if after == before {
		t.Fatalf("the room did not advance while the save was parked (tick %d)", after)
	}
	if p.exists("PARKED") {
		t.Fatal("the file was written before the parked write ran")
	}

	close(release)
	result, ok := readUntilProtocolEvent(p.ctx, t, p.conn, p.server, "saveResult", 20)
	if !ok {
		t.Fatal("no saveResult after the write was released")
	}
	if result.Error != "" || !p.exists("PARKED") {
		t.Fatalf("the released save did not land: error %q", result.Error)
	}
}
