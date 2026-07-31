package zztgo

// M16.15 — persistence, reconnect, and replay service journey.
//
// WHAT THIS ADDS. The persistence seams each have unit coverage already:
// M4.3a save/restore, M13.2 reconnect grace, M13.3 autosave/restore-on-boot,
// M14.2 record/replay. Every one of those drives a RoomManager or a
// WebSocketServer object in the test process. None of them proves that the
// *shipped* server — the cmd/zzt-server binary, with its own flags, its own
// directories and its own boot order — puts the promised bytes on disk and
// hands them back after a crash. That is what this file does: one journey
// through the production binary over real WebSockets, plus the two boundaries
// a subprocess cannot reach (a 60-second reconnect grace, and a divergence
// only visible with the recorder's own structs).
//
// The world is fixtures/persist.zwd: two playable boards joined by a colour-
// matched passage, an item row that gives a measurable inventory, a keeper
// object that sets a world flag from the FAR board (so a snapshot has to union
// flags across two live rooms), and a reaper that kills without ending the
// run — the boundary the score-on-quit deviation lives on.
//
// EVIDENCE MAP (the manifest rows this file certifies):
//   service.save-restore        — …Journey + …RestoreRoutesThroughTheBinary
//   service.account-persistence — …Journey + …CrashRestart… + …GraceExpiry…
//   service.reconnect           — …Journey (near side) + …GraceExpiry… (far side)
//   service.session-replay      — …Journey; GAP M16.15a, pinned by
//                                 …AccountRestoreIsMissingFromTheRecording
//   service.high-scores         — …Journey (a death enters nothing; a quit does)
//   route.api.saves/.restore/.loadworld — …RestoreRoutesThroughTheBinary
//   input.title-restore         — those routes, plus M16.11's real-browser 'R'

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	m1615World       = "PERSIST"
	m1615HallBoard   = int16(1)
	m1615AnnexBoard  = int16(2)
	m1615Account     = "google:m1615-ada"
	m1615AccountName = "Ada Persistence"
	// The cookie secret the subprocess is started with, so this test can mint a
	// session cookie the real AuthService accepts. Nothing contacts Google: the
	// production auth path only HMAC-verifies this cookie (auth.go
	// AccountFromRequest), so a signed cookie is a complete, hermetic sign-in.
	m1615CookieSecret = "m1615-persistence-journey-cookie-secret"
)

// ---------------------------------------------------------------------------
// The world under test
// ---------------------------------------------------------------------------

// m1615WorldBytes compiles fixtures/persist.zwd to a real .ZZT file. Compiling
// rather than committing the binary keeps the fixture readable and keeps the
// journey honest about what it walks into.
func m1615WorldBytes(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "fixtures", "persist.zwd")
	requireFixture(t, path)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	data, err := CompileZWD(string(src))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/persist.zwd): %v", err)
	}

	// Fail here, with the reason, rather than fifty ticks later with "the
	// keeper never answered": every coordinate the journey drives to is
	// asserted against the compiled world first.
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/persist.zwd): %v", err)
	}
	if world.Info.Name != m1615World {
		t.Fatalf("world name is %q, want %q — the instance name is also the autosave filename", world.Info.Name, m1615World)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(m1615HallBoard)
	if e.Board.Name != "Persist Hall" {
		t.Fatalf("board %d is %q, want \"Persist Hall\" — fixtures/persist.zwd changed shape", m1615HallBoard, e.Board.Name)
	}
	for _, want := range []struct {
		x, y int16
		elem byte
		what string
	}{
		{6, 12, E_PLAYER, "the start square"},
		{6, 10, E_OBJECT, "the reaper"},
		{8, 12, E_GEM, "the gem"},
		{10, 12, E_AMMO, "the ammo"},
		{12, 12, E_TORCH, "the torch"},
		{15, 12, E_PASSAGE, "the passage to the Annex"},
	} {
		if got := e.Board.Tiles[want.x][want.y].Element; got != want.elem {
			t.Fatalf("Persist Hall (%d,%d) is element %d, want %d (%s) — fixtures/persist.zwd changed shape",
				want.x, want.y, got, want.elem, want.what)
		}
	}
	e.BoardOpen(m1615AnnexBoard)
	if e.Board.Name != "Persist Annex" {
		t.Fatalf("board %d is %q, want \"Persist Annex\"", m1615AnnexBoard, e.Board.Name)
	}
	if e.Board.Tiles[6][12].Element != E_PASSAGE || e.Board.Tiles[10][12].Element != E_OBJECT {
		t.Fatal("Persist Annex lost its passage at (6,12) or its keeper at (10,12)")
	}
	e.BoardClose()

	return data
}

// ---------------------------------------------------------------------------
// The production binary, its directories, and restarts over them
// ---------------------------------------------------------------------------

// m1615Dirs is one server's on-disk world. A restart reuses the same value,
// which is the whole point: crash recovery is a property of the directories,
// not of the process.
type m1615Dirs struct {
	root   string
	saves  string
	worlds string
	record string
	web    string
}

func m1615NewDirs(t *testing.T) m1615Dirs {
	t.Helper()
	root := t.TempDir()
	dirs := m1615Dirs{
		root:   root,
		saves:  filepath.Join(root, "saves"),
		worlds: filepath.Join(root, "worlds"),
		record: filepath.Join(root, "record"),
		web:    filepath.Join(root, "web"),
	}
	for _, dir := range []string{dirs.saves, dirs.worlds, dirs.record, dirs.web} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dirs.web, "index.html"), []byte("<!DOCTYPE html><html><body>M16.15</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	world := m1615WorldBytes(t)
	// -world resolves against the process working directory; the picker and
	// GetOrCreateInstance resolve against -worlds. The production layout has
	// the world in both, so mirror it.
	for _, path := range []string{filepath.Join(root, m1615World+".ZZT"), filepath.Join(dirs.worlds, m1615World+".ZZT")} {
		if err := os.WriteFile(path, world, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return dirs
}

func (d m1615Dirs) autosavePath() string {
	return filepath.Join(d.saves, "autosave", m1615World+".SAV")
}
func (d m1615Dirs) accountSidecarPath() string {
	return filepath.Join(d.saves, "chat.jsonl.playerstate.json")
}
func (d m1615Dirs) highScorePath() string { return filepath.Join(d.root, m1615World+".HI") }

// m1615SyncBuffer collects the subprocess's log without racing the copy
// goroutine exec starts for a non-*os.File Stdout.
type m1615SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *m1615SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *m1615SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type m1615Server struct {
	t       *testing.T
	cmd     *exec.Cmd
	baseURL string
	wsURL   string
	logs    *m1615SyncBuffer
	waited  bool
}

// m1615Start launches cmd/zzt-server over dirs with Google auth enabled against
// a known cookie secret. Every test that needs the shipped boot order — flag
// parsing, RestoreAutosaves before the first client, recorder attach after
// restore — goes through here rather than constructing a WebSocketServer.
func m1615Start(t *testing.T, dirs m1615Dirs, extra ...string) *m1615Server {
	t.Helper()
	bin := getM1619ServerBinary(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	args := append([]string{
		"-addr", addr,
		"-world", m1615World,
		"-board", "1",
		"-web", dirs.web,
		"-saves", dirs.saves,
		"-worlds", dirs.worlds,
		"-help", ".",
		"-shutdown-grace", "0s",
	}, extra...)

	cmd := exec.Command(bin, args...)
	cmd.Dir = dirs.root
	cmd.Env = append(os.Environ(),
		"ZZT_GOOGLE_CLIENT_ID=m1615-test-client",
		"ZZT_AUTH_COOKIE_SECRET="+m1615CookieSecret,
		// Generation must never be reachable from this journey.
		"ANTHROPIC_API_KEY=",
	)
	logs := &m1615SyncBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs

	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}

	s := &m1615Server{t: t, cmd: cmd, baseURL: "http://" + addr, wsURL: "ws://" + addr + "/ws", logs: logs}
	t.Cleanup(s.kill)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.baseURL + "/api/worlds")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("server on %s never became ready. Logs:\n%s", addr, logs.String())
	return nil
}

func (s *m1615Server) kill() {
	if s.cmd.Process == nil || s.waited {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGKILL)
	_ = s.cmd.Wait()
	s.waited = true
}

// crash is an abrupt loss of the process: no shutdown hook, no recorder flush,
// nothing but what was already on disk. It is how a real crash reaches the
// restore-on-boot path.
func (s *m1615Server) crash() {
	s.t.Helper()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGKILL)
		_ = s.cmd.Wait()
		s.waited = true
	}
}

// shutdown is the operator's SIGINT: with -shutdown-grace 0s it cancels the
// tick loop, which is what flushes and closes the session recordings.
func (s *m1615Server) shutdown() {
	s.t.Helper()
	if s.cmd.Process == nil {
		return
	}
	if err := s.cmd.Process.Signal(syscall.SIGINT); err != nil {
		s.t.Fatalf("SIGINT: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = s.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.waited = true
	case <-time.After(15 * time.Second):
		// Mark it waited before failing: the goroutine above owns the Wait, and
		// the cleanup must not call a second one.
		s.waited = true
		_ = s.cmd.Process.Signal(syscall.SIGKILL)
		s.t.Fatalf("server did not exit within 15s of SIGINT. Logs:\n%s", s.logs.String())
	}
}

func (s *m1615Server) authCookie() *http.Cookie {
	s.t.Helper()
	signer := NewCookieSigner([]byte(m1615CookieSecret))
	value, err := signer.Encode(authSession{
		Account:   AuthenticatedAccount{ID: m1615Account, Email: "ada@example.test", Name: m1615AccountName},
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		s.t.Fatalf("sign auth cookie: %v", err)
	}
	return &http.Cookie{Name: authSessionCookie, Value: value}
}

// ---------------------------------------------------------------------------
// A network client that remembers what the server told it
// ---------------------------------------------------------------------------

// m1615Checkpoint is one post-step room fingerprint as it arrived on the wire:
// the room's tick and its StateHash. A replay must reproduce both.
type m1615Checkpoint struct {
	Tick int16
	Hash uint64
}

type m1615ClientState struct {
	PlayerID    PlayerID
	StatID      int16
	Board       int16
	X, Y        int16
	HUD         HUDSnapshot
	ResumeToken string
	Snapshots   int
	Diffs       int
	Events      []ProtocolEvent
	ReadErr     error
}

// m1615Track is one connection's contribution to the live fingerprint record:
// the checkpoints it received for one board, in arrival order. Tracks are kept
// per connection rather than merged because a drop, a resume and a displaced
// socket overlap in time, and merging them would scramble the order a replay
// has to reproduce.
type m1615Track struct {
	who         string
	board       int16
	checkpoints []m1615Checkpoint
}

type m1615Client struct {
	t      *testing.T
	name   string
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	seq    uint64

	mu          sync.Mutex
	st          m1615ClientState
	checkpoints map[int16][]m1615Checkpoint
	screen      map[int32]ScreenCell
	done        chan struct{}
}

// m1615ScreenKey packs a cell coordinate; the client keeps a live screen model
// the way the browser does — full paint from a snapshot, then diff cells.
func m1615ScreenKey(x, y int16) int32 { return int32(x)<<16 | int32(uint16(y)) }

// m1615Dial joins a world over a real WebSocket. cookie nil is a guest; a
// cookie is a signed-in account. The reader goroutine records everything the
// server sends, including the per-tick StateHash the replay is checked against.
func m1615Dial(t *testing.T, srv *m1615Server, name string, cookie *http.Cookie, resumeToken string) *m1615Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	opts := &websocket.DialOptions{}
	if cookie != nil {
		opts.HTTPHeader = http.Header{"Cookie": []string{cookie.String()}}
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, srv.wsURL+"?world="+m1615World, opts)
	if err != nil {
		cancel()
		t.Fatalf("%s: dial: %v\nLogs:\n%s", name, err, srv.logs.String())
	}
	conn.SetReadLimit(ServerReadLimit)

	c := &m1615Client{
		t: t, name: name, conn: conn, ctx: ctx, cancel: cancel,
		checkpoints: make(map[int16][]m1615Checkpoint),
		screen:      make(map[int32]ScreenCell),
		done:        make(chan struct{}),
	}
	go c.readLoop()

	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: name, Board: m1615HallBoard, ResumeToken: resumeToken}); err != nil {
		t.Fatalf("%s: write join: %v", name, err)
	}
	c.waitFor("the join snapshot", 10*time.Second, func(st m1615ClientState) bool { return st.Snapshots > 0 })
	return c
}

func (c *m1615Client) readLoop() {
	defer close(c.done)
	for {
		var raw json.RawMessage
		if err := wsjson.Read(c.ctx, c.conn, &raw); err != nil {
			c.mu.Lock()
			c.st.ReadErr = err
			c.mu.Unlock()
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}
		c.mu.Lock()
		switch envelope.Type {
		case MessageTypeSnapshot:
			var msg SnapshotMessage
			if err := json.Unmarshal(raw, &msg); err == nil {
				c.applySnapshotLocked(msg)
				c.st.Snapshots++
			}
		case MessageTypeBoardChange:
			var msg BoardChangeMessage
			if err := json.Unmarshal(raw, &msg); err == nil {
				c.applySnapshotLocked(msg.Snapshot)
			}
		case MessageTypeDiff:
			var msg DiffMessage
			if err := json.Unmarshal(raw, &msg); err == nil {
				c.st.Board = msg.BoardID
				c.st.Diffs++
				if msg.HUD != nil {
					c.st.HUD = *msg.HUD
				}
				for _, p := range msg.Players {
					if p.ID == c.st.PlayerID {
						c.st.X, c.st.Y, c.st.StatID = p.X, p.Y, p.StatID
					}
				}
				c.st.Events = append(c.st.Events, msg.Events...)
				for _, cell := range msg.Cells {
					c.screen[m1615ScreenKey(cell.X, cell.Y)] = cell
				}
				// Only diffs are checkpointed: a diff is built after the step,
				// which is exactly where a replay's onTick reads its hashes.
				c.checkpoints[msg.BoardID] = append(c.checkpoints[msg.BoardID], m1615Checkpoint{Tick: msg.Tick, Hash: msg.Hash})
			}
		case MessageTypeEvent:
			var msg EventMessage
			if err := json.Unmarshal(raw, &msg); err == nil {
				c.st.Events = append(c.st.Events, msg.Event)
			}
		}
		c.mu.Unlock()
	}
}

func (c *m1615Client) applySnapshotLocked(msg SnapshotMessage) {
	c.st.Board = msg.BoardID
	c.st.PlayerID = msg.You.ID
	c.st.StatID = msg.You.StatID
	c.st.X, c.st.Y = msg.You.X, msg.You.Y
	c.st.HUD = msg.HUD
	if msg.ResumeToken != "" {
		c.st.ResumeToken = msg.ResumeToken
	}
	c.st.Events = append(c.st.Events, msg.Events...)
	c.screen = make(map[int32]ScreenCell, len(msg.Screen))
	for _, cell := range msg.Screen {
		c.screen[m1615ScreenKey(cell.X, cell.Y)] = cell
	}
}

// tileCell returns the client's current screen cell for a BOARD coordinate.
// BoardDrawTile writes tile (x,y) at screen (x-1,y-1) (game.go:341).
func (c *m1615Client) tileCell(x, y int16) ScreenCell {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.screen[m1615ScreenKey(x-1, y-1)]
}

// tracks freezes this connection's checkpoints, one track per board it saw.
func (c *m1615Client) tracks() []m1615Track {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []m1615Track
	for _, board := range []int16{m1615HallBoard, m1615AnnexBoard} {
		if list := c.checkpoints[board]; len(list) > 0 {
			out = append(out, m1615Track{who: c.name, board: board, checkpoints: append([]m1615Checkpoint(nil), list...)})
		}
	}
	return out
}

func (c *m1615Client) state() m1615ClientState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st
	st.Events = append([]ProtocolEvent(nil), c.st.Events...)
	return st
}

func (c *m1615Client) send(message interface{}) {
	c.t.Helper()
	writeCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	if err := wsjson.Write(writeCtx, c.conn, message); err != nil {
		c.t.Fatalf("%s: write %T: %v", c.name, message, err)
	}
}

func (c *m1615Client) press(key byte) {
	c.t.Helper()
	c.seq++
	c.send(InputMessage{Type: MessageTypeInput, PlayerID: c.state().PlayerID, Seq: c.seq, Key: key})
}

func (c *m1615Client) waitFor(what string, timeout time.Duration, pred func(m1615ClientState) bool) m1615ClientState {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st := c.state()
		if pred(st) {
			return st
		}
		if st.ReadErr != nil {
			c.t.Fatalf("%s: connection died while waiting for %s: %v", c.name, what, st.ReadErr)
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("%s: timed out after %s waiting for %s (board=%d at %d,%d hud=%+v events=%v)",
				c.name, timeout, what, st.Board, st.X, st.Y, st.HUD, m1615EventTypes(st.Events))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// walkUntil holds a direction the way a player holds an arrow key: one keymask
// per client frame until the world answers. The server samples one input per
// tick, so over-sending is exactly what a real held key does.
func (c *m1615Client) walkUntil(dx, dy int16, what string, pred func(m1615ClientState) bool) m1615ClientState {
	c.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		st := c.state()
		if pred(st) {
			return st
		}
		if st.ReadErr != nil {
			c.t.Fatalf("%s: connection died while walking toward %s: %v", c.name, what, st.ReadErr)
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("%s: timed out walking (%d,%d) toward %s (board=%d at %d,%d hud=%+v)",
				c.name, dx, dy, what, st.Board, st.X, st.Y, st.HUD)
		}
		c.seq++
		c.send(InputMessage{Type: MessageTypeInput, PlayerID: st.PlayerID, Seq: c.seq, DeltaX: dx, DeltaY: dy})
		time.Sleep(40 * time.Millisecond)
	}
}

func (c *m1615Client) waitEvent(eventType string, timeout time.Duration) ProtocolEvent {
	c.t.Helper()
	var found ProtocolEvent
	c.waitFor("event "+eventType, timeout, func(st m1615ClientState) bool {
		ev, ok := findEvent(st.Events, eventType)
		if ok {
			found = ev
		}
		return ok
	})
	return found
}

func (c *m1615Client) hasEvent(eventType string) bool {
	_, ok := findEvent(c.state().Events, eventType)
	return ok
}

// drop closes the socket the way a lost connection does: no quit, no goodbye.
func (c *m1615Client) drop() {
	_ = c.conn.Close(websocket.StatusAbnormalClosure, "m16.15 drop")
	c.cancel()
	<-c.done
}

func (c *m1615Client) close() {
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	c.cancel()
	<-c.done
}

func m1615EventTypes(events []ProtocolEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

// ---------------------------------------------------------------------------
// On-disk readers: what the journey is allowed to know about the files
// ---------------------------------------------------------------------------

func m1615LoadWorldFile(t *testing.T, path string) TWorld {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	world, err := LoadWorldBytes(data)
	if err != nil {
		t.Fatalf("%s is not a loadable world: %v", path, err)
	}
	return world
}

func m1615WorldHasFlag(world TWorld, flag string) bool {
	for _, name := range world.Info.Flags {
		if name == flag {
			return true
		}
	}
	return false
}

func m1615AccountStates(t *testing.T, path string) map[string]PlayerState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read account sidecar %s: %v", path, err)
	}
	states := map[string]PlayerState{}
	if err := json.Unmarshal(data, &states); err != nil {
		t.Fatalf("account sidecar %s is not JSON: %v", path, err)
	}
	return states
}

func m1615WaitForFile(t *testing.T, path, what string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s (%s)", timeout, what, path)
}

// ---------------------------------------------------------------------------
// Recording readers
// ---------------------------------------------------------------------------

func m1615RecordingPath(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read record dir %s: %v", dir, err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one .jsonl recording in %s, found %v", dir, found)
	}
	return found[0]
}

// m1615ReadRecording parses the recording's tick lines, so the journey can find
// the tick a save was taken at without replaying anything.
func m1615ReadRecording(t *testing.T, path string) []recTick {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recording %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("recording %s has %d lines, want a header and at least one tick", path, len(lines))
	}
	ticks := make([]recTick, 0, len(lines)-1)
	for _, line := range lines[1:] {
		var rec recTick
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("recording %s has an unparseable tick line: %v", path, err)
		}
		ticks = append(ticks, rec)
	}
	return ticks
}

// m1615AssertTracksReplayed proves every fingerprint a client actually received
// is reproduced, in order, by the replay. Ordered subsequence rather than
// equality because a connection only sees the ticks it was present for, and its
// first frame on a board is a snapshot rather than a diff.
//
// It also refuses to be vacuous: each board must carry a real number of
// fingerprints, so a journey that quietly stopped receiving diffs cannot pass
// by having nothing left to check.
func m1615AssertTracksReplayed(t *testing.T, tracks []m1615Track, replay map[int16][]m1615Checkpoint) {
	t.Helper()
	const minPerBoard = 10
	perBoard := map[int16]int{}
	for _, track := range tracks {
		live, replayed := track.checkpoints, replay[track.board]
		i := 0
		for _, cp := range replayed {
			if i < len(live) && live[i] == cp {
				i++
			}
		}
		if i != len(live) {
			t.Fatalf("%s on board %d: the replay reproduced only %d of %d live (tick,StateHash) fingerprints — first unmatched %+v (the replay holds %d for that board)",
				track.who, track.board, i, len(live), live[i], len(replayed))
		}
		perBoard[track.board] += len(live)
		t.Logf("%s on board %d: all %d live (tick,StateHash) fingerprints reproduced", track.who, track.board, len(live))
	}
	for _, board := range []int16{m1615HallBoard, m1615AnnexBoard} {
		if perBoard[board] < minPerBoard {
			t.Errorf("board %d contributed only %d live fingerprints (want >= %d) — the journey did not actually play there long enough to prove anything",
				board, perBoard[board], minPerBoard)
		}
	}
}

// ---------------------------------------------------------------------------
// The journey
// ---------------------------------------------------------------------------

// TestM1615PersistenceReconnectAndReplayJourney is the DoD's single journey: a
// multi-board run with a shared flag and a real inventory, carried across every
// boundary this task promises — a manual save, an account sidecar, a guest with
// no sidecar at all, an autosave taken under play, a dropped socket resumed
// inside the grace window, a second connection claiming the same run, a death
// that enters no high score, a quit that does, and finally a recording whose
// replay must reproduce every StateHash the wire actually carried.
func TestM1615PersistenceReconnectAndReplayJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the M16.15 subprocess journey in short mode")
	}
	dirs := m1615NewDirs(t)
	srv := m1615Start(t, dirs,
		// One autosave per second, so the journey plays over a live autosave
		// cadence rather than calling the seam directly.
		"-autosave", "1",
		"-record", dirs.record,
		// A pristine start: this run's own crash recovery is tested below, and
		// nothing may leak in from an earlier one.
		"-fresh",
	)
	cookie := srv.authCookie()

	// Every connection's fingerprints are banked before it goes away, so the
	// drop, the resume and the displaced socket all contribute evidence.
	var tracks []m1615Track
	bank := func(c *m1615Client) { tracks = append(tracks, c.tracks()...) }

	// --- act 1: a signed-in player arrives ---------------------------------
	ada := m1615Dial(t, srv, "ada", cookie, "")
	adaID := ada.state().PlayerID
	adaToken := ada.state().ResumeToken
	if adaID == 0 || adaToken == "" {
		t.Fatalf("join gave id=%d token=%q, want both set", adaID, adaToken)
	}

	// --- act 2: death is a respawn, and enters no high score ---------------
	// The reaper is two squares north. It runs #endgame, which routes through
	// the shared death/respawn path (mp-respawn), NOT through a quit.
	ada.walkUntil(0, -1, "the reaper", func(st m1615ClientState) bool {
		return findEventOK(st.Events, "death")
	})
	ada.waitEvent("respawn", 20*time.Second)
	ada.waitFor("health back after the respawn", 10*time.Second, func(st m1615ClientState) bool {
		return st.HUD.Health == 100
	})
	if ada.hasEvent("highScoreEntry") {
		t.Error("DEVIATION score-on-quit: dying offered a high-score slot; a death is a respawn, and only a quit enters the list")
	}
	if _, err := os.Stat(dirs.highScorePath()); err == nil {
		t.Errorf("DEVIATION score-on-quit: %s was written by a death", dirs.highScorePath())
	}

	// --- act 3: an inventory worth persisting ------------------------------
	ada.walkUntil(1, 0, "the gem, ammo and torch", func(st m1615ClientState) bool {
		return st.HUD.Gems >= 1 && st.HUD.Ammo >= 5 && st.HUD.Torches >= 1
	})
	afterPickups := ada.state()
	if afterPickups.HUD.Score <= 0 {
		t.Fatalf("picking up a gem left score %d, want a positive score to quit with", afterPickups.HUD.Score)
	}

	// --- act 4: a second board, through a real passage ---------------------
	ada.walkUntil(1, 0, "the passage to the Annex", func(st m1615ClientState) bool {
		return st.Board == m1615AnnexBoard
	})
	ada.waitEvent("transfer", 5*time.Second)

	// --- act 5: a world flag set from the far board ------------------------
	beforeKeeper := ada.state().HUD.Gems
	ada.walkUntil(1, 0, "the keeper", func(st m1615ClientState) bool {
		return st.HUD.Gems >= beforeKeeper+5
	})

	// --- act 6: a guest in the other room ----------------------------------
	// The guest keeps the Hall occupied (so the snapshot has two live rooms to
	// union flags across) and is the control for account persistence: nothing
	// about a guest may ever reach the sidecar.
	guest := m1615Dial(t, srv, "Guesty", nil, "")
	guest.walkUntil(0, 1, "a square off the Hall's start", func(st m1615ClientState) bool {
		return st.Y > 12
	})
	if guest.state().PlayerID == adaID {
		t.Fatal("the guest was handed the signed-in player's id")
	}

	// --- act 7: the socket drops; the account keeps the run ----------------
	bank(ada)
	ada.drop()
	m1615WaitForFile(t, dirs.accountSidecarPath(), "the account sidecar after a drop", 10*time.Second)
	states := m1615AccountStates(t, dirs.accountSidecarPath())
	stored, ok := states[playerStateKey(m1615Account, m1615World)]
	if !ok {
		t.Fatalf("account sidecar has no entry for %s in %s: keys %v", m1615Account, m1615World, m1615SidecarKeys(states))
	}
	if stored.Gems != afterPickups.HUD.Gems+5 || stored.Ammo != afterPickups.HUD.Ammo {
		t.Errorf("account sidecar stored gems=%d ammo=%d, want gems=%d ammo=%d",
			stored.Gems, stored.Ammo, afterPickups.HUD.Gems+5, afterPickups.HUD.Ammo)
	}
	if len(states) != 1 {
		t.Errorf("account sidecar holds %d entries (%v), want only the signed-in player's — a guest must never be persisted",
			len(states), m1615SidecarKeys(states))
	}

	// --- act 8: resume inside the grace window -----------------------------
	// The drop must not have cost the run: same PlayerID, same stat, same
	// square on the same board, same inventory.
	dropped := ada.state()
	ada = m1615Dial(t, srv, "ada-resumed", cookie, adaToken)
	resumed := ada.state()
	if resumed.PlayerID != adaID || resumed.StatID != dropped.StatID {
		t.Fatalf("resume gave player %d stat %d, want %d/%d", resumed.PlayerID, resumed.StatID, adaID, dropped.StatID)
	}
	if resumed.Board != dropped.Board || resumed.X != dropped.X || resumed.Y != dropped.Y {
		t.Errorf("resume moved the player: board %d at %d,%d, want board %d at %d,%d",
			resumed.Board, resumed.X, resumed.Y, dropped.Board, dropped.X, dropped.Y)
	}
	if resumed.HUD.Gems != dropped.HUD.Gems || resumed.HUD.Ammo != dropped.HUD.Ammo || resumed.HUD.Torches != dropped.HUD.Torches {
		t.Errorf("resume lost inventory: %+v, want gems=%d ammo=%d torches=%d",
			resumed.HUD, dropped.HUD.Gems, dropped.HUD.Ammo, dropped.HUD.Torches)
	}

	// --- act 9: a competing connection claims the same run -----------------
	// Newest wins: the second socket takes the player over and the first is
	// closed by the server. Both are holding a valid token for a LIVE player.
	loser := ada
	winner := m1615Dial(t, srv, "ada-competing", cookie, adaToken)
	if got := winner.state().PlayerID; got != adaID {
		t.Fatalf("the competing connection got player %d, want the same run %d", got, adaID)
	}
	loser.waitFor("the displaced socket to be closed by the server", 10*time.Second, func(st m1615ClientState) bool {
		return st.ReadErr != nil
	})
	bank(loser)
	loser.drop()
	ada = winner

	// --- act 10: a manual save, and its account sidecar --------------------
	ada.press('S')
	ada.waitEvent("savePrompt", 10*time.Second)
	ada.send(SaveFilenameMessage{Type: MessageTypeSaveFilename, PlayerID: adaID, Name: "SAVE01"})
	result := ada.waitEvent("saveResult", 10*time.Second)
	if result.Error != "" {
		t.Fatalf("saveResult.error = %q", result.Error)
	}
	if result.Filename != "SAVE01" {
		t.Errorf("saveResult.filename = %q, want SAVE01", result.Filename)
	}
	savePath := filepath.Join(dirs.saves, "SAVE01.SAV")
	if _, err := os.Stat(savePath); err != nil {
		t.Fatalf("the save the server acknowledged is not on disk: %v", err)
	}
	saveSidecar := savePath + ".playerstate.json"
	sidecarData, err := os.ReadFile(saveSidecar)
	if err != nil {
		t.Fatalf("read %s: %v", saveSidecar, err)
	}
	var savedSidecar snapshotPlayerStateSidecar
	if err := json.Unmarshal(sidecarData, &savedSidecar); err != nil {
		t.Fatalf("%s is not JSON: %v", saveSidecar, err)
	}
	if savedSidecar.AccountID != m1615Account || savedSidecar.World != m1615World {
		t.Errorf("save sidecar names %s/%s, want %s/%s", savedSidecar.World, savedSidecar.AccountID, m1615World, m1615Account)
	}
	if savedSidecar.State.Gems != ada.state().HUD.Gems {
		t.Errorf("save sidecar stored gems=%d, want %d", savedSidecar.State.Gems, ada.state().HUD.Gems)
	}

	// --- act 11: the autosave, written under play --------------------------
	m1615WaitForFile(t, dirs.autosavePath(), "the first autosave", 15*time.Second)
	// Atomicity: the cadence keeps firing while both players move, and every
	// read of the file must be a complete, loadable world — never a truncated
	// one. A temp file is fine mid-write; a torn .SAV is not.
	for i := 0; i < 15; i++ {
		m1615LoadWorldFile(t, dirs.autosavePath())
		time.Sleep(80 * time.Millisecond)
	}

	// --- act 12: quit, and the high-score list that only a quit fills ------
	quitScore := ada.state().HUD.Score
	ada.press('Q')
	ada.waitEvent("quitPrompt", 10*time.Second)
	ada.send(QuitReplyMessage{Type: MessageTypeQuitReply, PlayerID: adaID, Quit: true})
	entry := ada.waitEvent("highScoreEntry", 10*time.Second)
	if entry.Score != quitScore {
		t.Errorf("highScoreEntry.score = %d, want the score at quit, %d", entry.Score, quitScore)
	}
	if entry.ListPos != 1 {
		t.Errorf("highScoreEntry.listPos = %d, want 1 on an empty list", entry.ListPos)
	}
	ada.send(HighScoreNameMessage{Type: MessageTypeHighScoreName, PlayerID: adaID, Name: "ADA"})
	ada.waitEvent("highScores", 10*time.Second)
	m1615WaitForFile(t, dirs.highScorePath(), "the high-score file a quit writes", 10*time.Second)
	scores := m1615ReadHighScores(t, dirs.highScorePath())
	if scores[0].Name != "ADA" || scores[0].Score != quitScore {
		t.Errorf("%s top entry is %q/%d, want ADA/%d", dirs.highScorePath(), scores[0].Name, scores[0].Score, quitScore)
	}

	// --- act 13: shut down, so the recording is flushed --------------------
	bank(ada)
	bank(guest)
	ada.close()
	guest.close()
	srv.shutdown()

	// --- act 14: what is on disk, and what the replay makes of it ----------
	// The manual save: players dropped, the far board's flag unioned in, the
	// picked-up gem gone, and the saver's inventory in the vanilla one-player
	// fields (that pair IS the snapshot-player-drop / account-sidecar-restore
	// deviation: the file keeps the world, the account keeps the player).
	saved := m1615LoadWorldFile(t, savePath)
	if !m1615WorldHasFlag(saved, "BEACON") {
		t.Errorf("the flag set on the Annex did not reach the save: flags %v", m1615NonEmptyFlags(saved))
	}
	if !saved.Info.IsSave {
		t.Error("the save is not marked IsSave")
	}
	savedHall := openSnapshotBoard(t, saved, m1615HallBoard)
	if got := countPlayerTiles(savedHall); got != 0 {
		t.Errorf("DEVIATION snapshot-player-drop: the saved Hall holds %d player tiles, want 0", got)
	}
	if savedHall.Board.Tiles[8][12].Element == E_GEM {
		t.Error("the gem the player collected came back in the save")
	}
	savedAnnex := openSnapshotBoard(t, saved, m1615AnnexBoard)
	if got := countPlayerTiles(savedAnnex); got != 0 {
		t.Errorf("DEVIATION snapshot-player-drop: the saved Annex holds %d player tiles, want 0", got)
	}
	if saved.Info.Gems != savedSidecar.State.Gems {
		t.Errorf("the save's one-player World.Info gems=%d, want the saver's %d", saved.Info.Gems, savedSidecar.State.Gems)
	}

	// A restored world is rejoinable, and a joiner arrives fresh (the other
	// half of snapshot-player-drop), with the shared flag still set.
	restored := NewRoomManager(TWorld{})
	if err := restored.RestoreSnapshot(dirs.saves, "SAVE01"); err != nil {
		t.Fatalf("RestoreSnapshot(SAVE01): %v", err)
	}
	joiner := restored.JoinPlayer(m1615HallBoard, 0, 0)
	room, ok := restored.Room(m1615HallBoard)
	if !ok {
		t.Fatal("the restored world has no Hall to join")
	}
	if room.Engine.WorldGetFlagPosition("BEACON") < 0 {
		t.Error("BEACON did not survive save→restore")
	}
	joinerState, ok := restored.PlayerState(joiner)
	if !ok {
		t.Fatal("the joiner has no state")
	}
	if joinerState.Score != 0 || joinerState.Gems != 0 || joinerState.Health != 100 {
		t.Errorf("DEVIATION snapshot-player-drop: a joiner into a restored world got score=%d gems=%d health=%d, want a fresh 0/0/100",
			joinerState.Score, joinerState.Gems, joinerState.Health)
	}

	// The recording: replay it and require the live wire fingerprints back.
	recPath := m1615RecordingPath(t, dirs.record)
	ticks := m1615ReadRecording(t, recPath)
	saveTick, savePlayer, found := m1615FindSaveOp(ticks)
	if !found {
		t.Fatal("the recording holds no save submit — the session recorder lost the manual save")
	}

	replayCheckpoints := map[int16][]m1615Checkpoint{}
	var worldAtSave TWorld
	var haveWorldAtSave bool
	f, err := os.Open(recPath)
	if err != nil {
		t.Fatalf("open recording: %v", err)
	}
	defer f.Close()
	replayed, err := ReplaySession(f, func(tick int, rm *RoomManager) {
		for _, boardID := range rm.roomIDs() {
			room := rm.rooms[boardID]
			if room == nil {
				continue
			}
			replayCheckpoints[boardID] = append(replayCheckpoints[boardID],
				m1615Checkpoint{Tick: room.Engine.CurrentTick, Hash: StateHash(room.Engine)})
		}
		// The save was taken between two steps: the ops recorded on tick K
		// arrived after tick K-1 finished. Capture the world exactly there.
		if tick == saveTick-1 {
			if world, ok := rm.snapshotWorld(savePlayer); ok {
				worldAtSave, haveWorldAtSave = world, true
			}
		}
	})
	if err != nil {
		t.Fatalf("replay %s: %v", recPath, err)
	}

	t.Logf("the live session recorded %d ticks across %d rooms; the save landed at tick %d",
		len(ticks), len(replayCheckpoints), saveTick)
	m1615AssertTracksReplayed(t, tracks, replayCheckpoints)

	// The replay reproduced the transfer and the quit as consequences, not as
	// recorded facts: the traveller ended on the Annex, and the quitter is gone.
	if _, _, stillThere := replayed.PlayerLocation(adaID); stillThere {
		t.Error("the replayed session still holds the player who quit")
	}

	// The strongest statement available: the bytes the live server wrote for
	// SAVE01 are the bytes an independent replay of the same session produces
	// at the same instant.
	if !haveWorldAtSave {
		t.Fatal("the replay never reached the tick the save was taken at")
	}
	replayBytes, err := worldToBytes(worldAtSave)
	if err != nil {
		t.Fatalf("serialize the replayed world: %v", err)
	}
	liveBytes, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read %s: %v", savePath, err)
	}
	if !bytes.Equal(replayBytes, liveBytes) {
		t.Errorf("SAVE01.SAV is %d bytes and the replayed world at the same tick is %d bytes; they differ — the recording does not reproduce the saved state",
			len(liveBytes), len(replayBytes))
	}

	// Nothing partial was left behind by any of the atomic writes.
	m1615AssertNoTempFiles(t, dirs.saves)
}

func findEventOK(events []ProtocolEvent, eventType string) bool {
	_, ok := findEvent(events, eventType)
	return ok
}

func m1615SidecarKeys(states map[string]PlayerState) []string {
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, strings.ReplaceAll(k, "\t", "|"))
	}
	return keys
}

func m1615NonEmptyFlags(world TWorld) []string {
	var out []string
	for _, f := range world.Info.Flags {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func m1615ReadHighScores(t *testing.T, path string) THighScoreList {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) < SizeOfHighScoreList {
		t.Fatalf("%s is %d bytes, want %d", path, len(data), SizeOfHighScoreList)
	}
	var list THighScoreList
	LoadHighScoreList(data, list[:])
	return list
}

func m1615FindSaveOp(ticks []recTick) (tick int, player PlayerID, found bool) {
	for _, rec := range ticks {
		for _, op := range rec.Ops {
			if op.Op == "submit" && op.Kind == "save" {
				return rec.Tick, op.Player, true
			}
		}
	}
	return 0, 0, false
}

func m1615AssertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".tmp") {
			t.Errorf("an atomic write left %s behind", path)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Crash, restart, -fresh, and a corrupt autosave
// ---------------------------------------------------------------------------

// m1615WaitForAutosave polls the autosave until it both parses and satisfies
// want. Polling the CONTENT rather than sleeping for a cadence is what makes
// the crash below deterministic: the process is only killed once the file on
// disk demonstrably carries the progress the restart has to bring back.
func m1615WaitForAutosave(t *testing.T, dirs m1615Dirs, what string, timeout time.Duration, want func(TWorld) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(dirs.autosavePath())
		if err == nil {
			if world, err := LoadWorldBytes(data); err == nil && want(world) {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for an autosave that %s (%s)", timeout, what, dirs.autosavePath())
}

// TestM1615CrashRestartRestoreFreshAndCorruptSkip covers the three boot-time
// outcomes the -saves directory can produce, each through a real restart of the
// production binary over the same directories: a crash recovered from the
// autosave, a deliberate -fresh start that ignores it, and a corrupt autosave
// that must be skipped rather than kill the boot. It also proves what -fresh
// does NOT reset: the world starts over, the signed-in account does not.
func TestM1615CrashRestartRestoreFreshAndCorruptSkip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the M16.15 restart matrix in short mode")
	}
	dirs := m1615NewDirs(t)
	srv := m1615Start(t, dirs, "-autosave", "1", "-fresh")
	cookie := srv.authCookie()

	ada := m1615Dial(t, srv, "ada", cookie, "")
	// How the pristine world draws the gem square, straight off the wire. Every
	// later assertion is against this cell rather than a hard-coded glyph.
	pristineGem := ada.tileCell(8, 12)
	if pristineGem.Ch == 0 {
		t.Fatalf("the join snapshot carried no cell for the gem square: %+v", pristineGem)
	}

	ada.walkUntil(1, 0, "the gem and the ammo", func(st m1615ClientState) bool {
		return st.HUD.Gems >= 1 && st.HUD.Ammo >= 5
	})
	progress := ada.state()
	if got := ada.tileCell(8, 12); got == pristineGem {
		t.Fatalf("the gem square still draws as %+v after it was collected", got)
	}

	// The autosave must demonstrably carry the progress before the crash.
	m1615WaitForAutosave(t, dirs, "has the collected gem gone", 20*time.Second, func(world TWorld) bool {
		board := openSnapshotBoard(t, world, m1615HallBoard)
		return board.Board.Tiles[8][12].Element != E_GEM
	})

	// A clean drop first, so the account sidecar is on disk too; then the
	// process dies with no chance to write anything.
	ada.drop()
	m1615WaitForFile(t, dirs.accountSidecarPath(), "the account sidecar before the crash", 10*time.Second)
	srv.crash()

	// --- restart over the same directories: restore-on-boot ----------------
	restarted := m1615Start(t, dirs, "-autosave", "0")
	guest := m1615Dial(t, restarted, "guest-after-crash", nil, "")
	if got := guest.tileCell(8, 12); got == pristineGem {
		t.Errorf("after a crash and restart the gem square draws as the pristine %+v again — the autosave was not restored", got)
	}
	// The signed-in player's own inventory comes back from the account
	// sidecar, not from the world file (DEVIATION account-sidecar-restore).
	returning := m1615Dial(t, restarted, "ada-after-crash", cookie, "")
	back := returning.state()
	if back.HUD.Gems != progress.HUD.Gems || back.HUD.Ammo != progress.HUD.Ammo || back.HUD.Score != progress.HUD.Score {
		t.Errorf("DEVIATION account-sidecar-restore: a signed-in player rejoining after a restart got gems=%d ammo=%d score=%d, want %d/%d/%d",
			back.HUD.Gems, back.HUD.Ammo, back.HUD.Score, progress.HUD.Gems, progress.HUD.Ammo, progress.HUD.Score)
	}
	if back.PlayerID == progress.PlayerID {
		t.Errorf("the rejoining player reused PlayerID %d from a process that no longer exists", back.PlayerID)
	}
	guest.close()
	returning.close()
	restarted.shutdown()

	// --- -fresh: the same good autosave, deliberately ignored ---------------
	fresh := m1615Start(t, dirs, "-autosave", "0", "-fresh")
	freshGuest := m1615Dial(t, fresh, "guest-fresh", nil, "")
	if got := freshGuest.tileCell(8, 12); got != pristineGem {
		t.Errorf("-fresh started from %+v, want the pristine %+v — the autosave should have been skipped entirely", got, pristineGem)
	}
	// -fresh resets the WORLD, not the accounts: the sidecar is not an autosave
	// and is not in the autosave directory, so a signed-in player still returns
	// with their inventory. Asserted so the boundary is on the record.
	freshAda := m1615Dial(t, fresh, "ada-fresh", cookie, "")
	if freshAda.state().HUD.Gems != progress.HUD.Gems {
		t.Errorf("-fresh also discarded the account sidecar: gems=%d, want %d", freshAda.state().HUD.Gems, progress.HUD.Gems)
	}
	freshGuest.close()
	freshAda.close()
	fresh.shutdown()
	if _, err := os.Stat(dirs.autosavePath()); err != nil {
		t.Fatalf("the -fresh run destroyed the autosave it was supposed to ignore: %v", err)
	}

	// --- a corrupt autosave is skipped, never a boot failure ----------------
	if err := os.WriteFile(dirs.autosavePath(), []byte("this is not a world, it is 33 bytes"), 0o644); err != nil {
		t.Fatalf("corrupt the autosave: %v", err)
	}
	corrupt := m1615Start(t, dirs, "-autosave", "0")
	corruptGuest := m1615Dial(t, corrupt, "guest-after-corruption", nil, "")
	if got := corruptGuest.tileCell(8, 12); got != pristineGem {
		t.Errorf("after a corrupt autosave the world draws as %+v, want the pristine %+v", got, pristineGem)
	}
	if logs := corrupt.logs.String(); !strings.Contains(logs, "could not be restored") {
		t.Errorf("a corrupt autosave was not reported at boot. Logs:\n%s", logs)
	}
	corruptGuest.close()
	corrupt.shutdown()

	m1615AssertNoTempFiles(t, dirs.saves)
}

// ---------------------------------------------------------------------------
// The title screen's restore routes, through the binary
// ---------------------------------------------------------------------------

func m1615GetJSON(t *testing.T, url string, out interface{}) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func m1615PostJSON(t *testing.T, url string, body interface{}) (int, string) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(text)
}

// TestM1615RestoreRoutesThroughTheBinary drives the three HTTP routes the title
// screen's 'R' (and world picker) stand on — /api/saves, /api/restore,
// /api/loadworld — against the production binary. The browser half of this
// journey (pressing R, choosing the save, playing the restored world) is
// covered in a real browser by M16.11's e2e_journey; what is proven here is the
// server contract underneath it, including every refusal.
func TestM1615RestoreRoutesThroughTheBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the M16.15 route journey in short mode")
	}
	dirs := m1615NewDirs(t)
	srv := m1615Start(t, dirs, "-fresh")

	var saves struct {
		Saves []string `json:"saves"`
	}
	m1615GetJSON(t, srv.baseURL+"/api/saves", &saves)
	if len(saves.Saves) != 0 {
		t.Fatalf("/api/saves on a fresh server = %v, want none", saves.Saves)
	}

	player := m1615Dial(t, srv, "restorer", nil, "")
	playerID := player.state().PlayerID
	player.walkUntil(1, 0, "the gem", func(st m1615ClientState) bool { return st.HUD.Gems >= 1 })

	player.press('S')
	player.waitEvent("savePrompt", 10*time.Second)
	player.send(SaveFilenameMessage{Type: MessageTypeSaveFilename, PlayerID: playerID, Name: "TITLESV"})
	if result := player.waitEvent("saveResult", 10*time.Second); result.Error != "" {
		t.Fatalf("saveResult.error = %q", result.Error)
	}

	m1615GetJSON(t, srv.baseURL+"/api/saves", &saves)
	if len(saves.Saves) != 1 || saves.Saves[0] != "TITLESV" {
		t.Fatalf("/api/saves = %v, want [TITLESV] — this is the list the title screen's R offers", saves.Saves)
	}

	// Refused while somebody is still playing: a restore rewrites every board.
	if code, body := m1615PostJSON(t, srv.baseURL+"/api/restore", map[string]string{"world": m1615World, "name": "TITLESV"}); code != http.StatusConflict {
		t.Errorf("/api/restore while occupied = %d (%s), want 409", code, strings.TrimSpace(body))
	}

	// Quitting removes the player from the room (unlike a drop, which keeps the
	// stat for the grace window), so the restore becomes possible.
	player.press('Q')
	player.waitEvent("quitPrompt", 10*time.Second)
	player.send(QuitReplyMessage{Type: MessageTypeQuitReply, PlayerID: playerID, Quit: true})
	player.waitEvent("highScoreEntry", 10*time.Second)

	deadline := time.Now().Add(10 * time.Second)
	var code int
	var body string
	for time.Now().Before(deadline) {
		code, body = m1615PostJSON(t, srv.baseURL+"/api/restore", map[string]string{"world": m1615World, "name": "TITLESV"})
		if code != http.StatusConflict {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code != http.StatusOK {
		t.Fatalf("/api/restore after the player quit = %d (%s), want 200", code, strings.TrimSpace(body))
	}
	if !strings.Contains(body, m1615World) {
		t.Errorf("/api/restore body = %q, want it to name the restored world", strings.TrimSpace(body))
	}

	// Refusals, taken while the world is still empty so the occupancy check
	// cannot mask them: an unknown save is 404, an unsafe name never reaches a
	// path at all.
	if code, _ := m1615PostJSON(t, srv.baseURL+"/api/restore", map[string]string{"world": m1615World, "name": "NOPE"}); code != http.StatusNotFound {
		t.Errorf("/api/restore of a missing save = %d, want 404", code)
	}
	for _, bad := range []string{"../ESCAPE", "a/b", "TOOLONGNAME"} {
		if code, _ := m1615PostJSON(t, srv.baseURL+"/api/restore", map[string]string{"world": m1615World, "name": bad}); code != http.StatusBadRequest {
			t.Errorf("/api/restore of %q = %d, want 400", bad, code)
		}
		if _, err := os.Stat(filepath.Join(dirs.root, "ESCAPE.SAV")); err == nil {
			t.Fatalf("a refused restore name reached the filesystem")
		}
	}

	// The world picker's load route, and its refusal.
	if code, body := m1615PostJSON(t, srv.baseURL+"/api/loadworld", map[string]string{"name": m1615World}); code != http.StatusOK || !strings.Contains(body, m1615World) {
		t.Errorf("/api/loadworld = %d (%s), want 200 naming %s", code, strings.TrimSpace(body), m1615World)
	}
	if code, _ := m1615PostJSON(t, srv.baseURL+"/api/loadworld", map[string]string{"name": "../TOWN"}); code != http.StatusBadRequest {
		t.Errorf("/api/loadworld of a traversing name = %d, want 400", code)
	}

	// Last, because a joiner re-occupies the world for the rest of the run: a
	// rejoin after the restore is a fresh run in the restored world — the gem
	// stays collected (the world was saved) and the player starts new
	// (snapshot-player-drop).
	rejoined := m1615Dial(t, srv, "after-restore", nil, "")
	if st := rejoined.state(); st.HUD.Gems != 0 || st.HUD.Score != 0 {
		t.Errorf("DEVIATION snapshot-player-drop: a joiner into the restored world got gems=%d score=%d, want 0/0", st.HUD.Gems, st.HUD.Score)
	}
	if got := rejoined.tileCell(8, 12); got.Ch == 0x04 {
		t.Errorf("the restored world put the collected gem back at (8,12): %+v", got)
	}
	rejoined.close()

	srv.shutdown()
	m1615AssertNoTempFiles(t, dirs.saves)
}

// ---------------------------------------------------------------------------
// The grace boundary a subprocess cannot reach, and what outlives it
// ---------------------------------------------------------------------------

// m1615TestWorld compiles the journey world for the in-process tests, which
// drive the tick clock themselves rather than waiting on wall time.
func m1615TestWorld(t *testing.T) TWorld {
	t.Helper()
	path := filepath.Join("..", "fixtures", "persist.zwd")
	requireFixture(t, path)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	world, err := CompileZWDWorld(string(src))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/persist.zwd): %v", err)
	}
	return world
}

// TestM1615GraceExpiryEndsTheRunButNotTheAccount is the far side of the
// reconnect boundary. The near side (a resume inside the window, and a second
// connection claiming a live run) is driven through the production binary by
// the journey above; expiry cannot be, because the window is 545 ticks of wall
// clock. Driving server.Tick directly makes it exact instead of slow.
//
// The statement being proven is the pair: when the grace runs out the RUN is
// gone — no stat, no token, no reclaimable position — while a signed-in
// player's INVENTORY is not, because it lives in the account sidecar
// (DEVIATION account-sidecar-restore).
func TestM1615GraceExpiryEndsTheRunButNotTheAccount(t *testing.T) {
	server := NewWebSocketServer(m1615TestWorld(t), m1615HallBoard)
	server.Auth = NewAuthService("m1615-test-client", "", "", []byte(m1615CookieSecret))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	inst := server.DefaultInstance
	cookie := signedAuthCookie(t, server.Auth, AuthenticatedAccount{ID: m1615Account, Name: m1615AccountName})

	conn, snap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored-by-auth", Board: m1615HallBoard}, cookie)
	playerID, token := snap.You.ID, snap.ResumeToken
	if playerID == 0 || token == "" {
		t.Fatalf("join gave id=%d token=%q", playerID, token)
	}

	// Walk three squares east, one explicit tick at a time, so the run has a
	// position that is distinguishable from a fresh spawn.
	for i := 0; i < 3; i++ {
		if err := wsjson.Write(ctx, conn, InputMessage{Type: MessageTypeInput, PlayerID: playerID, Seq: uint64(i + 1), DeltaX: 1}); err != nil {
			t.Fatalf("write input: %v", err)
		}
		waitFor(t, "the input to reach the instance", func() bool {
			inst.mu.Lock()
			defer inst.mu.Unlock()
			_, queued := inst.Inputs[playerID]
			return queued
		})
		server.Tick(ctx)
	}

	inst.mu.Lock()
	state, ok := inst.RoomManager.PlayerState(playerID)
	if !ok {
		inst.mu.Unlock()
		t.Fatal("the joined player has no state")
	}
	state.Gems, state.Ammo, state.Torches, state.Score = 9, 25, 3, 250
	var movedX, movedY int16
	if boardID, statID, located := inst.RoomManager.PlayerLocation(playerID); located {
		room, _ := inst.RoomManager.Room(boardID)
		movedX, movedY = int16(room.Engine.Board.Stats[statID].X), int16(room.Engine.Board.Stats[statID].Y)
	}
	inst.mu.Unlock()
	if movedX == 6 {
		t.Fatalf("the player never left the start square (x=%d); the position claim below would be vacuous", movedX)
	}

	// The drop: not a quit. The account sidecar is written immediately, before
	// any grace has elapsed.
	conn.Close(websocket.StatusAbnormalClosure, "wifi blip")
	waitFor(t, "the detach", func() bool {
		_, detached := detachedCount(server, playerID)
		return detached
	})
	stored, ok, err := server.ChatDB.GetPlayerState(m1615Account, inst.Name)
	if err != nil || !ok {
		t.Fatalf("GetPlayerState after a drop = (%+v, %v, %v), want the run's state", stored, ok, err)
	}
	if stored.Gems != 9 || stored.Ammo != 25 || stored.Score != 250 {
		t.Errorf("the account kept gems=%d ammo=%d score=%d, want 9/25/250", stored.Gems, stored.Ammo, stored.Score)
	}

	// Run the window out. One tick short is covered by M13.2's own test; what
	// matters here is what is left afterwards.
	for i := 0; i < ReconnectGraceTicks; i++ {
		server.Tick(ctx)
	}
	inst.mu.Lock()
	alive := inst.RoomManager.players[playerID] != nil
	_, tokenAlive := inst.ResumeTokens[token]
	inst.mu.Unlock()
	if alive {
		t.Error("the player survived the reconnect grace")
	}
	if tokenAlive {
		t.Error("an expired run's resume token is still redeemable")
	}

	// Coming back with the stale token is not an error: it falls through to a
	// fresh join. The run is gone (new PlayerID, the board's start square), the
	// inventory is not (the account sidecar).
	conn2, snap2 := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "ignored-by-auth", Board: m1615HallBoard, ResumeToken: token}, cookie)
	if snap2.You.ID == playerID {
		t.Errorf("an expired token still reclaimed player %d", playerID)
	}
	if snap2.You.X == movedX && snap2.You.Y == movedY {
		t.Errorf("the expired run's position (%d,%d) was handed back; a fresh join starts at the board's start square", movedX, movedY)
	}
	if snap2.HUD.Gems != 9 || snap2.HUD.Ammo != 25 || snap2.HUD.Score != 250 {
		t.Errorf("DEVIATION account-sidecar-restore: the returning account got gems=%d ammo=%d score=%d, want 9/25/250",
			snap2.HUD.Gems, snap2.HUD.Ammo, snap2.HUD.Score)
	}
	// Closed before the guest half so no live socket is fed 545 unread frames.
	conn2.Close(websocket.StatusNormalClosure, "")

	// --- the control: a guest is offered none of it -------------------------
	guestConn, guestSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Guesty", Board: m1615HallBoard}, nil)
	guestID := guestSnap.You.ID
	inst.mu.Lock()
	guestState, ok := inst.RoomManager.PlayerState(guestID)
	if !ok {
		inst.mu.Unlock()
		t.Fatal("the guest has no state")
	}
	guestState.Gems, guestState.Score = 7, 700
	inst.mu.Unlock()

	guestConn.Close(websocket.StatusAbnormalClosure, "gone")
	waitFor(t, "the guest's detach", func() bool {
		_, detached := detachedCount(server, guestID)
		return detached
	})
	for i := 0; i < ReconnectGraceTicks; i++ {
		server.Tick(ctx)
	}
	guestConn2, guestSnap2 := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "Guesty", Board: m1615HallBoard}, nil)
	defer guestConn2.Close(websocket.StatusNormalClosure, "")
	if guestSnap2.HUD.Gems != 0 || guestSnap2.HUD.Score != 0 {
		t.Errorf("a returning guest got gems=%d score=%d back; only accounts persist",
			guestSnap2.HUD.Gems, guestSnap2.HUD.Score)
	}
}

// ---------------------------------------------------------------------------
// A gap this sweep found: the recording does not carry an account restore
// ---------------------------------------------------------------------------

// TestM1615AccountRestoreIsMissingFromTheRecording PINS A DEFECT, filed as
// M16.15a (NOTES.md 2026-07-31). It asserts the WRONG behaviour on purpose, so
// that the day the recorder learns to carry an account restore this test goes
// red and is inverted — the same convention M16.13a/M16.14a were filed under.
//
// What is wrong: the session recorder logs every external stimulus the server
// applies to a room EXCEPT one. When a signed-in player rejoins, the server
// calls RoomManager.ApplyPlayerState with the inventory read out of the account
// sidecar (websocket_server.go, the authenticated fresh-join branch), and
// nothing records it. A replay therefore re-runs the same session with a
// freshly-spawned player: different ammo, different gems, different score, and
// from the first tick a different StateHash. It fails silently — the replay
// completes and reports success.
//
// It cannot happen on a server without auth, and it cannot happen to a player's
// first visit, which is why the journey above (a fresh account on a fresh
// server) replays exactly. It happens to every returning signed-in player on
// the production host, which is where the recordings that matter come from.
func TestM1615AccountRestoreIsMissingFromTheRecording(t *testing.T) {
	world := m1615TestWorld(t)

	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, m1615World, world, &buf)

	const returning = PlayerID(7)
	rm.JoinPlayerWithID(returning, m1615HallBoard, 0, 0)
	rm.SetPlayerIdentity(returning, m1615Account, m1615AccountName)
	// Exactly what the server does for a returning signed-in player.
	restored := PlayerState{Health: 100, Ammo: 25, Gems: 9, Torches: 3, Score: 250}
	if !rm.ApplyPlayerState(returning, restored) {
		t.Fatal("ApplyPlayerState refused a player who is in a room")
	}
	for i := 0; i < 12; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{returning: {DeltaX: 1}})
	}
	liveHashes := rm.RoomStateHashes()
	liveState, ok := rm.PlayerState(returning)
	if !ok {
		t.Fatal("the live player vanished")
	}
	liveGems, liveAmmo, liveScore := liveState.Gems, liveState.Ammo, liveState.Score
	rec.Close()

	replayed, err := ReplaySession(&buf, nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	replayState, ok := replayed.PlayerState(returning)
	if !ok {
		t.Fatal("the replayed player vanished")
	}

	// PINNED DEFECT (invert all three when M16.15a lands).
	if replayState.Gems == liveGems && replayState.Ammo == liveAmmo && replayState.Score == liveScore {
		t.Fatalf("the replay now reproduces the account-restored inventory (gems=%d ammo=%d score=%d) — M16.15a is fixed; invert this test",
			replayState.Gems, replayState.Ammo, replayState.Score)
	}
	t.Logf("M16.15a: live inventory gems=%d ammo=%d score=%d, replayed gems=%d ammo=%d score=%d",
		liveGems, liveAmmo, liveScore, replayState.Gems, replayState.Ammo, replayState.Score)

	replayHashes := replayed.RoomStateHashes()
	diverged := false
	for board, live := range liveHashes {
		if replayHashes[board] != live {
			diverged = true
			t.Logf("M16.15a: board %d live StateHash %016x, replayed %016x", board, live, replayHashes[board])
		}
	}
	if !diverged {
		t.Fatal("every room's StateHash now matches across the replay — M16.15a is fixed; invert this test")
	}
}
