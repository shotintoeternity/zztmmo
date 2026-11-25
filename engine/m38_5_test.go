package zztgo

// M38.5: the workbench session. Headless websocket tests: an import, an edit,
// a seek and a step each land on the frame a fresh replay of the judge's own
// recording shows at that tick; a seek past the ceiling is refused; an idle
// session is evicted and its socket closed; editing writes nothing until
// submit, which takes M38.4's path; and the replay viewer seeks the same way.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type m385Bench struct {
	t      *testing.T
	h      *m321Harness
	conn   *websocket.Conn
	screen m221Screen
	board  int16
	status WorkbenchStatusMessage
}

func m385Open(t *testing.T, h *m321Harness, account *AuthenticatedAccount) *m385Bench {
	t.Helper()
	opts := &websocket.DialOptions{}
	if account != nil {
		cookie := signedAuthCookie(t, h.server.Auth, *account)
		opts.HTTPHeader = http.Header{"Cookie": []string{cookie.String()}}
	}
	conn, _, err := websocket.Dial(h.ctx, h.wsURL+"?workbench="+gemDashFixture().ID, opts)
	if err != nil {
		t.Fatalf("dial the workbench: %v", err)
	}
	conn.SetReadLimit(ServerReadLimit)
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	if err := wsjson.Write(h.ctx, conn, JoinMessage{Type: MessageTypeJoin}); err != nil {
		t.Fatalf("join: %v", err)
	}
	b := &m385Bench{t: t, h: h, conn: conn}
	b.untilStatus()
	return b
}

func (b *m385Bench) send(op WorkbenchOpMessage) {
	b.t.Helper()
	op.Type = MessageTypeWorkbench
	if err := wsjson.Write(b.h.ctx, b.conn, op); err != nil {
		b.t.Fatalf("send %s: %v", op.Op, err)
	}
}

// read returns the next message's type and raw bytes, applying a frame to
// the bench's screen and remembering a status.
func (b *m385Bench) read() (string, json.RawMessage) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(b.h.ctx, 10*time.Second)
	defer cancel()
	var raw json.RawMessage
	if err := wsjson.Read(ctx, b.conn, &raw); err != nil {
		b.t.Fatalf("read: %v", err)
	}
	var envelope struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &envelope)
	switch envelope.Type {
	case MessageTypeSnapshot:
		var snap SnapshotMessage
		_ = json.Unmarshal(raw, &snap)
		if !snap.Spectator {
			b.t.Fatalf("a workbench frame was not read-only: %+v", snap.You)
		}
		b.screen, b.board = newM221Screen(snap.Screen), snap.BoardID
	case MessageTypeDiff:
		var diff DiffMessage
		_ = json.Unmarshal(raw, &diff)
		if diff.BoardID != b.board {
			b.t.Fatalf("a diff for board %d on board %d's screen", diff.BoardID, b.board)
		}
		b.screen.apply(diff.Cells)
	case MessageTypeWorkbenchStatus:
		b.status = WorkbenchStatusMessage{}
		_ = json.Unmarshal(raw, &b.status)
	}

	return envelope.Type, raw
}

func (b *m385Bench) untilStatus() WorkbenchStatusMessage {
	b.t.Helper()
	for {
		typ, raw := b.read()
		if typ == MessageTypeWorkbenchError {
			b.t.Fatalf("refused: %s", raw)
		}
		if typ == MessageTypeWorkbenchStatus {
			return b.status
		}
	}
}

func (b *m385Bench) refusal(op WorkbenchOpMessage) WorkbenchErrorMessage {
	b.t.Helper()
	b.send(op)
	for {
		typ, raw := b.read()
		if typ == MessageTypeWorkbenchError {
			var msg WorkbenchErrorMessage
			_ = json.Unmarshal(raw, &msg)
			return msg
		}
		if typ == MessageTypeWorkbenchStatus {
			b.t.Fatalf("%s was applied, not refused: %+v", op.Op, b.status)
		}
	}
}

func (b *m385Bench) op(op WorkbenchOpMessage) WorkbenchStatusMessage {
	b.t.Helper()
	b.send(op)
	st := b.untilStatus()
	// A fence: the status is queued before the op's deferred unlock, and the
	// server tick only TryLocks a session, so a test that ticks the moment it
	// reads the status could find the op still holding the lock.
	wb := b.session()
	wb.mu.Lock()
	wb.mu.Unlock()
	return st
}

// session is the server's one open workbench.
func (b *m385Bench) session() *WorkbenchSession {
	b.t.Helper()
	b.h.server.mu.Lock()
	defer b.h.server.mu.Unlock()
	if len(b.h.server.WorkbenchSessions) != 1 {
		b.t.Fatalf("%d workbenches open, want 1", len(b.h.server.WorkbenchSessions))
	}
	for wb := range b.h.server.WorkbenchSessions {
		return wb
	}
	return nil
}

// m385Oracle is a fresh replay of the judge's recording of script, stepped to
// tick n: the thing the workbench's frame at n must equal.
func m385Oracle(t *testing.T, h *m321Harness, script TASScript, name string, n int) *RoomManager {
	t.Helper()
	def := gemDashFixture()
	worldName, world, err := h.server.ResolveChallengeWorld(def)
	if err != nil {
		t.Fatal(err)
	}
	var rec bytes.Buffer
	if _, err := JudgeTASScript(def, worldName, world, script, name, &rec); err != nil {
		t.Fatalf("judge: %v", err)
	}
	p, err := NewReplayPlayback(&rec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, _, done, err := p.Step(); done || err != nil {
			t.Fatalf("the judge's recording ends before tick %d: %v", n, err)
		}
	}
	return p.RoomManager()
}

// matches asserts the bench's screen and the session's simulation are the
// oracle's at the same tick.
func (b *m385Bench) matches(oracle *RoomManager, label string) {
	b.t.Helper()
	board := gemDashFixture().Board
	if id, _, ok := oracle.PlayerLocation(tasJudgePlayer); ok {
		board = id
	}
	if board != b.board {
		b.t.Fatalf("%s: the workbench shows board %d, a fresh replay is on %d", label, b.board, board)
	}
	want, _ := oracle.SpectatorSnapshot(board)
	if key, bad := b.screen.differs(newM221Screen(want.Screen)); bad {
		b.t.Fatalf("%s: the frame differs from a fresh replay at cell key %d", label, key)
	}
	wb := b.session()
	wb.mu.Lock()
	got := wb.playback.RoomManager().RoomStateHashes()
	wb.mu.Unlock()
	if !reflect.DeepEqual(got, oracle.RoomStateHashes()) {
		b.t.Fatalf("%s: room hashes %v, a fresh replay has %v", label, got, oracle.RoomStateHashes())
	}
}

func m385RecordingFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestM385EditSeekAndStepMatchAFreshReplay(t *testing.T) {
	h, run, script := m382LiveRun(t)
	b := m385Open(t, h, &m321Ada)
	if b.status.Tick != 0 || b.status.Length != 0 || b.status.Met || b.status.Ceiling != TASTickCeiling {
		t.Fatalf("a new workbench = %+v", b.status)
	}
	name := b.session().who.name

	// Import the live run: the script is the live run's projection, at tick 0.
	st := b.op(WorkbenchOpMessage{Op: "import", Recording: run.RecordingID})
	if st.Length != len(script.Ticks) || st.Tick != 0 {
		t.Fatalf("after import %+v, want %d ticks at tick 0", st, len(script.Ticks))
	}
	b.matches(m385Oracle(t, h, script, name, 0), "tick 0")

	// Seek re-simulates from 0.
	n := len(script.Ticks) / 2
	if st := b.op(WorkbenchOpMessage{Op: "seek", Tick: n}); st.Tick != n || st.Met {
		t.Fatalf("seek %d = %+v", n, st)
	}
	b.matches(m385Oracle(t, h, script, name, n), "seek")

	// Step is one tick on, as a diff.
	if st := b.op(WorkbenchOpMessage{Op: "step"}); st.Tick != n+1 {
		t.Fatalf("step = %+v", st)
	}
	b.matches(m385Oracle(t, h, script, name, n+1), "step")

	// An edit behind the cursor re-simulates to where the cursor was.
	edited := script
	edited.Ticks = append([]TASTick(nil), script.Ticks...)
	edited.Ticks[1] = TASTick{Tick: 1, DeltaY: 1}
	if st := b.op(WorkbenchOpMessage{Op: "setInput", Tick: 1, DeltaY: 1}); st.Tick != n+1 || st.Length != len(script.Ticks) {
		t.Fatalf("an edit at tick 1 = %+v", st)
	}
	b.matches(m385Oracle(t, h, edited, name, n+1), "edit")
	if reflect.DeepEqual(m385Oracle(t, h, edited, name, n+1).RoomStateHashes(), m385Oracle(t, h, script, name, n+1).RoomStateHashes()) {
		t.Fatal("the edit changed nothing, so it proves nothing")
	}

	// Put it back and seek to the end: the goal is met exactly as judged.
	b.op(WorkbenchOpMessage{Op: "setInput", Tick: 1, DeltaX: script.Ticks[1].DeltaX, DeltaY: script.Ticks[1].DeltaY, Key: script.Ticks[1].Key, Shift: script.Ticks[1].Shift})
	judged := m382Judge(t, h, script, nil)
	st = b.op(WorkbenchOpMessage{Op: "seek", Tick: len(script.Ticks)})
	if !st.Met || st.Ticks != judged.Result.Ticks || st.Score != judged.Result.Score || st.Gems != judged.Result.Gems || st.Ticks != run.Result.Ticks {
		t.Fatalf("at the end %+v; judged %+v; live %+v", st, judged.Result, run.Result)
	}
	b.matches(m385Oracle(t, h, script, name, len(script.Ticks)), "the end")

	// A seek past the script's end lands on its end.
	if st := b.op(WorkbenchOpMessage{Op: "seek", Tick: len(script.Ticks) + 50}); st.Tick != len(script.Ticks) {
		t.Fatalf("a seek past the end = %+v", st)
	}

	// Export is the script, and the edits wrote nothing server-side.
	b.send(WorkbenchOpMessage{Op: "export"})
	for {
		typ, raw := b.read()
		if typ != MessageTypeWorkbenchScript {
			continue
		}
		var msg WorkbenchScriptMessage
		_ = json.Unmarshal(raw, &msg)
		if !reflect.DeepEqual(msg.Script, script) {
			t.Fatalf("export = %+v, want the imported script", msg.Script)
		}
		break
	}
}

func TestM385EditsAreCheckedAsTheyAreApplied(t *testing.T) {
	h, _, _ := m382LiveRun(t)
	b := m385Open(t, h, nil)

	if got := b.refusal(WorkbenchOpMessage{Op: "seek", Tick: TASTickCeiling + 1}); got.Refusal != WorkbenchRefuseRange {
		t.Fatalf("a seek past the ceiling = %+v", got)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "seek", Tick: -1}); got.Refusal != WorkbenchRefuseRange {
		t.Fatalf("a seek before 0 = %+v", got)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "step"}); got.Refusal != WorkbenchRefuseEnd {
		t.Fatalf("a step past the end = %+v", got)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "setInput", Tick: 1}); got.Refusal != WorkbenchRefuseRange {
		t.Fatalf("a gap in the script = %+v", got)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "setInput", Tick: 0, Key: 'S'}); got.Refusal != TASRefuseKey {
		t.Fatalf("a save key = %+v", got)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "setInput", Tick: 0, DeltaX: 2}); got.Refusal != TASRefuseDelta {
		t.Fatalf("a delta of 2 = %+v", got)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "frobnicate"}); got.Refusal != WorkbenchRefuseOp {
		t.Fatalf("an unknown op = %+v", got)
	}

	// A script built from nothing: append, step, reply, truncate.
	for i := 0; i < 3; i++ {
		if st := b.op(WorkbenchOpMessage{Op: "setInput", Tick: i, DeltaX: 1}); st.Length != i+1 {
			t.Fatalf("append %d = %+v", i, st)
		}
	}
	b.op(WorkbenchOpMessage{Op: "step"})
	if st := b.op(WorkbenchOpMessage{Op: "step"}); st.Tick != 2 {
		t.Fatalf("two steps = %+v", st)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "setScrollReply", Tick: 0, Stat: 1, Label: CP437String([]byte{'a', 1})}); got.Refusal != TASRefuseReply {
		t.Fatalf("a control character in a reply = %+v", got)
	}
	b.op(WorkbenchOpMessage{Op: "setScrollReply", Tick: 2, Stat: 3, Label: "yes"})
	b.op(WorkbenchOpMessage{Op: "setScrollReply", Tick: 2, Stat: 3, Label: "no"})
	wb := b.session()
	wb.mu.Lock()
	replies := append([]TASReply(nil), wb.script.Ticks[2].Replies...)
	wb.mu.Unlock()
	if !reflect.DeepEqual(replies, []TASReply{{StatID: 3, Label: "no"}}) {
		t.Fatalf("replies = %+v", replies)
	}
	if st := b.op(WorkbenchOpMessage{Op: "truncate", Tick: 1}); st.Length != 1 || st.Tick != 1 {
		t.Fatalf("truncate behind the cursor = %+v", st)
	}
	if got := b.refusal(WorkbenchOpMessage{Op: "truncate", Tick: 2}); got.Refusal != WorkbenchRefuseRange {
		t.Fatalf("truncate past the end = %+v", got)
	}
}

func TestM385PlayStepsOneTickPerServerTickAndStopsAtTheEnd(t *testing.T) {
	h, run, script := m382LiveRun(t)
	b := m385Open(t, h, &m321Ada)
	b.op(WorkbenchOpMessage{Op: "import", Recording: run.RecordingID})
	if st := b.op(WorkbenchOpMessage{Op: "play"}); !st.Playing {
		t.Fatalf("play = %+v", st)
	}
	for i := 1; i <= 3; i++ {
		h.server.Tick(h.ctx)
		if st := b.untilStatus(); st.Tick != i || !st.Playing {
			t.Fatalf("server tick %d = %+v", i, st)
		}
	}
	b.matches(m385Oracle(t, h, script, b.session().who.name, 3), "played")
	if st := b.op(WorkbenchOpMessage{Op: "pause"}); st.Playing {
		t.Fatalf("pause = %+v", st)
	}
	h.server.Tick(h.ctx)
	if st := b.op(WorkbenchOpMessage{Op: "seek", Tick: len(script.Ticks) - 1}); st.Tick != len(script.Ticks)-1 {
		t.Fatalf("paused, the server tick moved the cursor: %+v", st)
	}
	b.op(WorkbenchOpMessage{Op: "play"})
	h.server.Tick(h.ctx)
	if st := b.untilStatus(); st.Tick != len(script.Ticks) {
		t.Fatalf("the last played tick = %+v", st)
	}
	if st := b.untilStatus(); st.Playing || !st.Met {
		t.Fatalf("play did not stop at the end: %+v", st)
	}
}

func TestM385ImportIsOnlyYourOwnLiveRun(t *testing.T) {
	h, run, _ := m382LiveRun(t)
	guest := m385Open(t, h, nil)
	if got := guest.refusal(WorkbenchOpMessage{Op: "import", Recording: run.RecordingID}); got.Refusal != WorkbenchRefuseSignIn {
		t.Fatalf("a guest's import = %+v", got)
	}
	guest.conn.Close(websocket.StatusNormalClosure, "")
	m385WaitClosed(t, h)

	bo := m385Open(t, h, &m321Bo)
	if got := bo.refusal(WorkbenchOpMessage{Op: "import", Recording: run.RecordingID}); got.Refusal != WorkbenchRefuseNotYours {
		t.Fatalf("Bo importing Ada's run = %+v", got)
	}
	if got := bo.refusal(WorkbenchOpMessage{Op: "import", Recording: "../../etc/passwd"}); got.Refusal != WorkbenchRefuseNotYours {
		t.Fatalf("an arbitrary path = %+v", got)
	}
}

func m385WaitClosed(t *testing.T, h *m321Harness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.server.WorkbenchCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d workbenches still open", h.server.WorkbenchCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestM385SubmitTakesTheAssistedPathAndNothingIsWrittenBefore(t *testing.T) {
	h, run, script := m382LiveRun(t)
	before := m385RecordingFiles(t, h.recordDir)
	b := m385Open(t, h, &m321Ada)
	b.op(WorkbenchOpMessage{Op: "import", Recording: run.RecordingID})
	b.op(WorkbenchOpMessage{Op: "seek", Tick: len(script.Ticks)})
	b.op(WorkbenchOpMessage{Op: "setInput", Tick: 0, DeltaX: script.Ticks[0].DeltaX, DeltaY: script.Ticks[0].DeltaY})
	b.op(WorkbenchOpMessage{Op: "truncate", Tick: len(script.Ticks)})
	if after := m385RecordingFiles(t, h.recordDir); after != before {
		t.Fatalf("editing wrote %d files", after-before)
	}

	b.send(WorkbenchOpMessage{Op: "submit"})
	var got WorkbenchSubmitMessage
	for {
		typ, raw := b.read()
		if typ == MessageTypeWorkbenchSubmit {
			_ = json.Unmarshal(raw, &got)
			break
		}
	}
	if got.Status != http.StatusOK || !got.Result.Met || !got.Result.Durable || got.Result.Rank != 1 || got.Result.Ticks != run.Result.Ticks {
		t.Fatalf("submit = %+v", got)
	}
	rows := h.server.ChallengeStore.LeaderboardIn(gemDashFixture().ID, ChallengeDivisionAssisted, m321Ada.ID)
	if len(rows) != 1 || rows[0].RecordingID != got.Result.RecordingID {
		t.Fatalf("the assisted board = %+v", rows)
	}
	if after := m385RecordingFiles(t, h.recordDir); after != before+1 {
		t.Fatalf("submit wrote %d files, want the one recording", after-before)
	}

	// A goal not met is M38.4's 422, carried back unchanged.
	b.op(WorkbenchOpMessage{Op: "truncate", Tick: 2})
	b.send(WorkbenchOpMessage{Op: "submit"})
	for {
		typ, raw := b.read()
		if typ == MessageTypeWorkbenchSubmit {
			_ = json.Unmarshal(raw, &got)
			break
		}
	}
	if got.Status != http.StatusUnprocessableEntity || got.Result.Refusal != "goal-not-met" || got.Result.StoppedAt != 2 {
		t.Fatalf("a short submit = %+v", got)
	}
}

func TestM385IdleSessionIsEvicted(t *testing.T) {
	h, _, _ := m382LiveRun(t)
	h.server.WorkbenchEvictIdleTicks = 3
	b := m385Open(t, h, nil)
	h.server.Tick(h.ctx)
	h.server.Tick(h.ctx)
	b.op(WorkbenchOpMessage{Op: "pause"}) // an op resets the count
	h.server.Tick(h.ctx)
	h.server.Tick(h.ctx)
	if h.server.WorkbenchCount() != 1 {
		t.Fatal("a session with a recent op was evicted")
	}
	h.server.Tick(h.ctx)
	if h.server.WorkbenchCount() != 0 {
		t.Fatal("an idle session was not evicted")
	}
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, b.conn, &raw); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the evicted session's socket stayed open")
			}
			return
		}
	}
}

func TestM385SessionsAreCapped(t *testing.T) {
	h, _, _ := m382LiveRun(t)
	for i := 0; i < maxWorkbenchSessions; i++ {
		m385Open(t, h, nil)
	}
	conn, _, err := websocket.Dial(h.ctx, h.wsURL+"?workbench="+gemDashFixture().ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	_ = wsjson.Write(h.ctx, conn, JoinMessage{Type: MessageTypeJoin})
	for {
		var raw json.RawMessage
		if err := wsjson.Read(h.ctx, conn, &raw); err != nil {
			t.Fatalf("read: %v", err)
		}
		var msg WorkbenchErrorMessage
		_ = json.Unmarshal(raw, &msg)
		if msg.Type == MessageTypeWorkbenchError {
			if msg.Refusal != "busy" {
				t.Fatalf("the ninth workbench = %+v", msg)
			}
			return
		}
		if msg.Type == MessageTypeWorkbenchStatus {
			t.Fatal("a ninth workbench opened")
		}
	}
}

func TestM385ReplayViewerSeeksTheSameWay(t *testing.T) {
	h, run, _ := m382LiveRun(t)
	h.server.ReplayDir = h.recordDir
	conn, snap := m223WatchReplay(t, h.ctx, h.wsURL, run.RecordingID)
	defer conn.Close(websocket.StatusNormalClosure, "")
	_ = snap

	n := run.Result.Ticks / 2
	if err := wsjson.Write(h.ctx, conn, ReplayControlMessage{Type: MessageTypeReplayControl, Op: "pause"}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(h.ctx, conn, ReplayControlMessage{Type: MessageTypeReplayControl, Op: "seek", Tick: n}); err != nil {
		t.Fatal(err)
	}
	typ, got, _ := m221ReadFrame(t, h.ctx, conn)
	if typ != MessageTypeSnapshot {
		t.Fatalf("a seek answered %q", typ)
	}

	// A fresh replay of the same file, stepped to n.
	f, err := os.Open(h.recordDir + "/" + run.RecordingID + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fresh, err := NewReplayPlayback(f)
	if err != nil {
		t.Fatal(err)
	}
	for fresh.LastTick() < n-1 {
		if _, _, done, err := fresh.Step(); done || err != nil {
			t.Fatalf("the recording ends before %d", n)
		}
	}
	want, _ := fresh.RoomManager().SpectatorSnapshot(got.BoardID)
	if key, bad := newM221Screen(got.Screen).differs(newM221Screen(want.Screen)); bad || got.Hash != want.Hash {
		t.Fatalf("the seek's frame differs from a fresh replay at %d (cell key %d, hash %x/%x)", n, key, got.Hash, want.Hash)
	}

	// The seek kept the pause: a server tick moves nothing.
	replay, err := h.server.GetOrCreateReplayInstance(run.RecordingID)
	if err != nil {
		t.Fatal(err)
	}
	replay.mu.Lock()
	paused, last := replay.Paused, replay.playback.LastTick()
	replay.mu.Unlock()
	if !paused || last != n-1 {
		t.Fatalf("after the seek paused=%v last=%d, want paused at %d", paused, last, n-1)
	}
	// Within the restart cooldown a second seek is dropped, not fatal.
	if err := wsjson.Write(h.ctx, conn, ReplayControlMessage{Type: MessageTypeReplayControl, Op: "seek", Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(h.ctx, conn, ReplayControlMessage{Type: MessageTypeReplayControl, Op: "pause"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		replay.mu.Lock()
		paused, last = replay.Paused, replay.playback.LastTick()
		replay.mu.Unlock()
		if !paused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pause fence never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if last != n-1 {
		t.Fatalf("a seek inside the cooldown moved the replay to %d", last)
	}
}

// M38.6: a tick set with the play keys is the input live play would have
// stepped for the same keymask, and it admits and exports as itself.
func TestM386KeymaskInputIsLivePlaysInput(t *testing.T) {
	h, _, _ := m382LiveRun(t)
	b := m385Open(t, h, nil)
	masks := []uint16{InputMaskRight, InputMaskUp | InputMaskShift, InputMaskShoot}
	for i, mask := range masks {
		b.op(WorkbenchOpMessage{Op: "setInput", Tick: i, Keymask: mask})
	}
	b.op(WorkbenchOpMessage{Op: "setInput", Tick: len(masks), Key: 'T'})
	wb := b.session()
	wb.mu.Lock()
	script := wb.exportLocked()
	wb.mu.Unlock()
	for i, mask := range masks {
		want := inputMessageToPlayerInput(InputMessage{Keymask: mask})
		if got := script.Ticks[i].Input(); got != want {
			t.Fatalf("keymask %#x set %+v, live play steps %+v", mask, got, want)
		}
	}
	data, err := EncodeTASScript(script)
	if err != nil {
		t.Fatal(err)
	}
	again, err := AdmitTASScript(data, gemDashFixture())
	if err != nil || !reflect.DeepEqual(again, script) {
		t.Fatalf("the play-key script does not admit as itself: %v %+v", err, again)
	}
}
