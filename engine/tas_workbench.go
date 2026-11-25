package zztgo

// M38.5: the TAS workbench session. One connection holds one editable script
// for one challenge; the server re-simulates it and streams frames, as the
// replay viewer does, because the client is a dumb terminal (TASKS.md M38,
// boundary 4).
//
// It is built on ReplayPlayback fed from memory: scriptPlayback turns the
// script's ticks into the recTicks a recording of the judged run would carry
// (the join and the name on tick 0, the scroll replies as submit ops), so the
// workbench steps exactly what a fresh replay of the judge's recording steps.
// Seek is re-simulation from tick 0, bounded by the tick ceiling (M38.2
// measured a full-ceiling Gem Dash script at about 80 ms); RoomManager has no
// clone, so a faster seek waits for a measurement that says it is needed.
//
// Editing never writes anything server-side: the playback has no recorder, and
// only submit, which hands the script to M38.4's path, opens a file.
//
// Locking. Every op runs on the connection's read goroutine under s.mu, and
// writes its frames to the client's outbound queue (a non-blocking enqueue)
// before unlocking, so the frames of a re-simulation and of a played tick
// cannot interleave. The server tick drives only play and idle eviction, and
// it takes the session with TryLock: a seek in progress holds the lock for a
// re-simulation, and the tick goroutine must not wait for one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	// DefaultWorkbenchEvictIdleTicks drops a session after about five minutes
	// with no op from its client, unless it is playing.
	DefaultWorkbenchEvictIdleTicks = 2727
	// maxWorkbenchSessions bounds the sessions open at once. Each holds a room
	// manager and can ask for a re-simulation up to the tick ceiling, so this
	// and the one-op-at-a-time read loop are the fleet's bound on seek cost.
	maxWorkbenchSessions = 8
)

// Workbench refusal classes beyond M38.1's.
const (
	WorkbenchRefuseOp         = "unknown-op"
	WorkbenchRefuseRange      = "tick-out-of-range"
	WorkbenchRefuseEnd        = "end-of-script"
	WorkbenchRefuseSignIn     = "sign-in-to-import"
	WorkbenchRefuseNotYours   = "not-your-run"
	WorkbenchRefuseUnreadable = "unreadable-recording"
)

var (
	errWorkbenchBusy    = errors.New("too many workbenches are open; try again shortly")
	errWorkbenchEvicted = errors.New("the workbench was closed after sitting idle")
)

// scriptPlayback is a ReplayPlayback whose ticks come from a script. ticks is
// read at each step, so an edit past the cursor is seen without a re-simulation.
func scriptPlayback(def ChallengeDefinition, worldName string, world TWorld, name string, ticks func() []TASTick) *ReplayPlayback {
	rm := newChallengeRoomManager(world, worldName)
	admitted := CP437String(admitPlayerName(name))
	next := 0
	p := &ReplayPlayback{
		header:      recHeader{World: worldName, ChallengeID: def.ID, ChallengeVersion: def.Version, FriendlyFire: rm.FriendlyFire},
		rm:          rm,
		last:        -1,
		seenPlayers: make(map[PlayerID]struct{}),
	}
	p.next = func() (recTick, bool, error) {
		script := ticks()
		if next >= len(script) {
			return recTick{}, false, nil
		}
		rec := scriptRecTick(def, admitted, next, script[next])
		next++
		return rec, true, nil
	}
	return p
}

// scriptRecTick is the recTick the judge's recording carries for script tick
// i: on tick 0 the join at the challenge start and the player's name, as
// JudgeTASScript applies them, then that tick's scroll replies.
func scriptRecTick(def ChallengeDefinition, name CP437String, i int, tick TASTick) recTick {
	rec := recTick{Tick: i, Inputs: map[PlayerID]PlayerInput{tasJudgePlayer: tick.Input()}}
	if i == 0 {
		rec.Ops = append(rec.Ops,
			recOp{Op: "join", Player: tasJudgePlayer, Board: def.Board},
			recOp{Op: "name", Player: tasJudgePlayer, Name: name})
	}
	for _, reply := range tick.Replies {
		rec.Ops = append(rec.Ops, recOp{Op: "submit", Kind: "scroll", Player: tasJudgePlayer, StatID: reply.StatID, Label: reply.Label})
	}
	return rec
}

// WorkbenchSession is one open workbench.
type WorkbenchSession struct {
	server    *WebSocketServer
	def       ChallengeDefinition
	worldName string
	world     TWorld
	who       assistedSubmitter
	client    *webSocketClient

	mu       sync.Mutex
	script   TASScript
	playback *ReplayPlayback
	// cursor is how many script ticks have been stepped.
	cursor int
	met    bool
	result ChallengeResult
	sub    *spectator
	// fresh forces the next frame to be a whole snapshot: after a
	// re-simulation, every engine pointer belongs to a new room manager.
	fresh   bool
	playing bool
	// idleTicks counts server ticks with no op from the client while not
	// playing; evicted is set when the session is dropped for it.
	idleTicks int
	evicted   bool
}

func newWorkbenchSession(s *WebSocketServer, def ChallengeDefinition, worldName string, world TWorld, who assistedSubmitter, client *webSocketClient) *WorkbenchSession {
	wb := &WorkbenchSession{
		server:    s,
		def:       def,
		worldName: worldName,
		world:     world,
		who:       who,
		client:    client,
		script:    TASScript{Challenge: def.ID, Version: def.Version},
		sub:       &spectator{client: client, boardID: def.Board},
	}
	wb.resimLocked(0)
	return wb
}

// resimLocked rebuilds the simulation from tick 0 and steps to tick.
func (wb *WorkbenchSession) resimLocked(tick int) {
	if tick > len(wb.script.Ticks) {
		tick = len(wb.script.Ticks)
	}
	wb.playback = scriptPlayback(wb.def, wb.worldName, wb.world, wb.who.name, func() []TASTick { return wb.script.Ticks })
	wb.cursor = 0
	wb.met = false
	wb.result = ChallengeResult{}
	for wb.cursor < tick {
		wb.stepLocked()
	}
	wb.fresh = true
}

// stepLocked steps one script tick and measures it with the live run's rule.
func (wb *WorkbenchSession) stepLocked() map[int16]DiffMessage {
	_, diffs, done, _ := wb.playback.Step()
	if done {
		return nil
	}
	wb.cursor++
	if !wb.met {
		if result, met := measureChallengeTick(wb.def, wb.cursor, tasJudgePlayer, wb.playback.RoomManager()); met {
			wb.met, wb.result = true, result
		}
	}
	return diffs
}

// frameLocked writes the frame for the player's board and then the status.
// diffs is the last step's, or nil when nothing was stepped.
func (wb *WorkbenchSession) frameLocked(ctx context.Context, diffs map[int16]DiffMessage) {
	rm := wb.playback.RoomManager()
	board := wb.def.Board
	if b, _, ok := rm.PlayerLocation(tasJudgePlayer); ok {
		board = b
	}
	var engine *Engine
	if room, live := rm.Room(board); live {
		engine = room.Engine
	}
	switch {
	case wb.fresh || board != wb.sub.boardID || engine != wb.sub.engine:
		wb.sub.boardID = board
		snapshot, e := rm.SpectatorSnapshot(board)
		wb.sub.engine = e
		wb.fresh = false
		_ = wb.client.write(ctx, snapshot)
	case diffs != nil:
		if diff, ok := diffs[board]; ok {
			_ = wb.client.write(ctx, diff)
		}
	}
	wb.statusLocked(ctx)
}

func (wb *WorkbenchSession) statusLocked(ctx context.Context) {
	_ = wb.client.write(ctx, wb.statusMessageLocked())
}

func (wb *WorkbenchSession) statusMessageLocked() WorkbenchStatusMessage {
	msg := WorkbenchStatusMessage{
		Type:      MessageTypeWorkbenchStatus,
		Challenge: wb.def.ID,
		Version:   wb.def.Version,
		Goal:      ChallengeGoalLine(wb.def.Goal),
		Tick:      wb.cursor,
		Length:    len(wb.script.Ticks),
		Ceiling:   TASTickCeiling,
		Playing:   wb.playing,
		Ticks:     wb.cursor,
	}
	if wb.met {
		msg.Met, msg.Ticks, msg.Score, msg.Gems = true, wb.result.Ticks, wb.result.Score, wb.result.Gems
	}
	return msg
}

func (wb *WorkbenchSession) refuseLocked(ctx context.Context, op, class, format string, args ...interface{}) {
	_ = wb.client.write(ctx, WorkbenchErrorMessage{Type: MessageTypeWorkbenchError, Op: op, Refusal: class, Reason: fmt.Sprintf(format, args...)})
}

// apply runs one op. It reports false when the connection should end.
func (wb *WorkbenchSession) apply(ctx context.Context, op WorkbenchOpMessage) bool {
	if op.Op == "submit" {
		wb.submit(ctx)
		return true
	}
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.evicted {
		return false
	}
	wb.idleTicks = 0
	switch op.Op {
	case "setInput":
		wb.setInputLocked(ctx, op)
	case "setScrollReply":
		wb.setScrollReplyLocked(ctx, op)
	case "truncate":
		if op.Tick < 0 || op.Tick > len(wb.script.Ticks) {
			wb.refuseLocked(ctx, op.Op, WorkbenchRefuseRange, "tick %d is outside the script's %d ticks", op.Tick, len(wb.script.Ticks))
			return true
		}
		wb.script.Ticks = wb.script.Ticks[:op.Tick:op.Tick]
		if wb.cursor > op.Tick {
			wb.resimLocked(op.Tick)
			wb.frameLocked(ctx, nil)
			return true
		}
		wb.statusLocked(ctx)
	case "seek":
		if op.Tick < 0 || op.Tick > TASTickCeiling {
			wb.refuseLocked(ctx, op.Op, WorkbenchRefuseRange, "tick %d is outside 0 to the %d-tick ceiling", op.Tick, TASTickCeiling)
			return true
		}
		wb.playing = false
		wb.resimLocked(op.Tick)
		wb.frameLocked(ctx, nil)
	case "step":
		if wb.cursor >= len(wb.script.Ticks) {
			wb.refuseLocked(ctx, op.Op, WorkbenchRefuseEnd, "tick %d is the end of the script; set an input there to go on", wb.cursor)
			return true
		}
		wb.frameLocked(ctx, wb.stepLocked())
	case "play":
		wb.playing = wb.cursor < len(wb.script.Ticks)
		wb.statusLocked(ctx)
	case "pause":
		wb.playing = false
		wb.statusLocked(ctx)
	case "import":
		wb.importLocked(ctx, op)
	case "export":
		_ = wb.client.write(ctx, WorkbenchScriptMessage{Type: MessageTypeWorkbenchScript, Script: wb.exportLocked()})
	default:
		wb.refuseLocked(ctx, op.Op, WorkbenchRefuseOp, "%q is not a workbench op", op.Op)
	}
	return true
}

func (wb *WorkbenchSession) exportLocked() TASScript {
	script := wb.script
	script.Ticks = make([]TASTick, len(wb.script.Ticks))
	for i, tick := range wb.script.Ticks {
		script.Ticks[i] = tick
		script.Ticks[i].Replies = append([]TASReply(nil), tick.Replies...)
	}
	return script
}

// editedLocked follows an edit at tick: an edit the simulation has already
// stepped past re-simulates to where the cursor was; one at or after the
// cursor is read when it is reached.
func (wb *WorkbenchSession) editedLocked(ctx context.Context, tick int) {
	if tick < wb.cursor {
		wb.resimLocked(wb.cursor)
		wb.frameLocked(ctx, nil)
		return
	}
	wb.statusLocked(ctx)
}

func (wb *WorkbenchSession) setInputLocked(ctx context.Context, op WorkbenchOpMessage) {
	n := len(wb.script.Ticks)
	if op.Tick < 0 || op.Tick > n {
		wb.refuseLocked(ctx, op.Op, WorkbenchRefuseRange, "tick %d is outside the script's %d ticks; tick %d appends", op.Tick, n, n)
		return
	}
	if op.Tick == n && n >= TASTickCeiling {
		wb.refuseLocked(ctx, op.Op, TASRefuseTooLong, "the script is at the %d-tick ceiling", TASTickCeiling)
		return
	}
	tick := TASTick{Tick: op.Tick, DeltaX: op.DeltaX, DeltaY: op.DeltaY, Shift: op.Shift, Key: op.Key}
	if op.Tick < n {
		tick.Replies = wb.script.Ticks[op.Tick].Replies
	}
	if err := checkTASTick(op.Tick, tick); err != nil {
		wb.refuseTASLocked(ctx, op.Op, err)
		return
	}
	if op.Tick == n {
		wb.script.Ticks = append(wb.script.Ticks, tick)
	} else {
		wb.script.Ticks[op.Tick] = tick
	}
	wb.editedLocked(ctx, op.Tick)
}

// setScrollReplyLocked sets the tick's reply to the scroll stat Stat opened,
// replacing one already given to that stat; an empty label removes it.
func (wb *WorkbenchSession) setScrollReplyLocked(ctx context.Context, op WorkbenchOpMessage) {
	n := len(wb.script.Ticks)
	if op.Tick < 0 || op.Tick >= n {
		wb.refuseLocked(ctx, op.Op, WorkbenchRefuseRange, "tick %d is outside the script's %d ticks", op.Tick, n)
		return
	}
	tick := wb.script.Ticks[op.Tick]
	var replies []TASReply
	for _, reply := range tick.Replies {
		if reply.StatID != op.Stat {
			replies = append(replies, reply)
		}
	}
	if op.Label != "" {
		replies = append(replies, TASReply{StatID: op.Stat, Label: string(op.Label)})
	}
	tick.Replies = replies
	if err := checkTASTick(op.Tick, tick); err != nil {
		wb.refuseTASLocked(ctx, op.Op, err)
		return
	}
	wb.script.Ticks[op.Tick] = tick
	wb.editedLocked(ctx, op.Tick)
}

func (wb *WorkbenchSession) refuseTASLocked(ctx context.Context, op string, err error) {
	var refusal *TASRefusal
	if errors.As(err, &refusal) {
		wb.refuseLocked(ctx, op, refusal.Class, "%s", refusal.Detail)
		return
	}
	wb.refuseLocked(ctx, op, TASRefuseMalformed, "%v", err)
}

// importLocked loads one of the player's own live runs. Own is the one thing
// the server can check: a signed-in account whose live-division row cites the
// recording. A guest has no row to cite, so a guest is asked to sign in.
func (wb *WorkbenchSession) importLocked(ctx context.Context, op WorkbenchOpMessage) {
	s := wb.server
	if !wb.who.signedIn {
		wb.refuseLocked(ctx, op.Op, WorkbenchRefuseSignIn, "sign in to import your own runs")
		return
	}
	var row ChallengeResult
	ok := false
	if s.ChallengeStore != nil {
		row, ok = s.ChallengeStore.ResultForRecordingIn(wb.def.ID, ChallengeDivisionLive, op.Recording)
	}
	if !ok || row.AccountKey != wb.who.account.ID {
		wb.refuseLocked(ctx, op.Op, WorkbenchRefuseNotYours, "that is not one of your live runs of %s", wb.def.ID)
		return
	}
	path, err := s.challengeRecordingPath(row.RecordingID)
	if err != nil {
		wb.refuseLocked(ctx, op.Op, WorkbenchRefuseUnreadable, "the run's recording is unavailable")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		wb.refuseLocked(ctx, op.Op, WorkbenchRefuseUnreadable, "the run's recording is unavailable")
		return
	}
	script, err := ProjectRecordingToTAS(f)
	_ = f.Close()
	if err == nil {
		err = CheckTASScript(script, wb.def)
	}
	if err != nil {
		wb.refuseTASLocked(ctx, op.Op, err)
		return
	}
	wb.script = script
	wb.playing = false
	wb.resimLocked(0)
	wb.frameLocked(ctx, nil)
}

// submit hands the script to M38.4's path. The judge runs outside the session
// lock; only the answer is queued.
func (wb *WorkbenchSession) submit(ctx context.Context) {
	wb.mu.Lock()
	if wb.evicted {
		wb.mu.Unlock()
		return
	}
	wb.idleTicks = 0
	script := wb.exportLocked()
	wb.mu.Unlock()
	body, err := EncodeTASScript(script)
	if err != nil {
		_ = wb.client.write(ctx, WorkbenchErrorMessage{Type: MessageTypeWorkbenchError, Op: "submit", Refusal: TASRefuseMalformed, Reason: err.Error()})
		return
	}
	out := wb.server.submitAssisted(ctx, wb.def, wb.who, body)
	resp := out.resp
	if out.plain != "" {
		resp.Reason = out.plain
	}
	_ = wb.client.write(ctx, WorkbenchSubmitMessage{Type: MessageTypeWorkbenchSubmit, Status: out.status, Result: resp})
}

// tick is the server tick's turn: play one step, and count idleness. It
// reports true when the session should be evicted.
func (wb *WorkbenchSession) tick(ctx context.Context, idleLimit int) bool {
	if !wb.mu.TryLock() {
		return false
	}
	defer wb.mu.Unlock()
	if wb.evicted {
		return false
	}
	if wb.playing {
		if wb.cursor < len(wb.script.Ticks) {
			wb.frameLocked(ctx, wb.stepLocked())
		}
		if wb.cursor >= len(wb.script.Ticks) {
			wb.playing = false
			wb.statusLocked(ctx)
		}
		return false
	}
	wb.idleTicks++
	if idleLimit > 0 && wb.idleTicks >= idleLimit {
		wb.evicted = true
		return true
	}
	return false
}

// tickWorkbenches plays and evicts the open sessions. An evicted session's
// connection is closed at once, which ends its read loop.
func (s *WebSocketServer) tickWorkbenches(ctx context.Context) {
	s.mu.Lock()
	sessions := make([]*WorkbenchSession, 0, len(s.WorkbenchSessions))
	for wb := range s.WorkbenchSessions {
		sessions = append(sessions, wb)
	}
	s.mu.Unlock()
	for _, wb := range sessions {
		if !wb.tick(ctx, s.WorkbenchEvictIdleTicks) {
			continue
		}
		s.mu.Lock()
		delete(s.WorkbenchSessions, wb)
		s.mu.Unlock()
		_ = wb.client.write(ctx, WorkbenchErrorMessage{Type: MessageTypeWorkbenchError, Refusal: "idle", Reason: errWorkbenchEvicted.Error()})
		if wb.client.conn != nil {
			_ = wb.client.conn.CloseNow()
		}
	}
}

// WorkbenchCount is how many workbench sessions are open.
func (s *WebSocketServer) WorkbenchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.WorkbenchSessions)
}

func (s *WebSocketServer) openWorkbench(wb *WorkbenchSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.WorkbenchSessions == nil {
		s.WorkbenchSessions = make(map[*WorkbenchSession]struct{})
	}
	if len(s.WorkbenchSessions) >= maxWorkbenchSessions {
		return errWorkbenchBusy
	}
	s.WorkbenchSessions[wb] = struct{}{}
	return nil
}

func (s *WebSocketServer) closeWorkbench(wb *WorkbenchSession) {
	s.mu.Lock()
	delete(s.WorkbenchSessions, wb)
	s.mu.Unlock()
}

// serveWorkbenchHTTP is `?workbench=<challenge id>`. The first message is a
// join, as the replay viewer's is; everything after it is an op.
func (s *WebSocketServer) serveWorkbenchHTTP(w http.ResponseWriter, r *http.Request, challengeID string) {
	def, ok := ChallengeByID(challengeID)
	if !ok {
		http.Error(w, "no such challenge", http.StatusNotFound)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.OriginHosts})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)

	ctx := r.Context()
	var raw json.RawMessage
	if err := wsjson.Read(ctx, conn, &raw); err != nil {
		return
	}
	var join JoinMessage
	if err := json.Unmarshal(raw, &join); err != nil || join.Type != MessageTypeJoin {
		return
	}

	client := newWebSocketClient(conn, "workbench:"+def.ID)
	defer client.stop()
	who := s.assistedSubmitterFor(r)
	if notice, refused := s.refusedAtTheDoor(who.account, who.signedIn); refused {
		_ = client.write(ctx, notice)
		return
	}
	worldName, world, err := s.ResolveChallengeWorld(def)
	if err != nil {
		_ = client.write(ctx, WorkbenchErrorMessage{Type: MessageTypeWorkbenchError, Refusal: "unavailable", Reason: challengeRefusalText(err)})
		return
	}

	wb := newWorkbenchSession(s, def, worldName, world, who, client)
	if err := s.openWorkbench(wb); err != nil {
		_ = client.write(ctx, WorkbenchErrorMessage{Type: MessageTypeWorkbenchError, Refusal: "busy", Reason: err.Error()})
		return
	}
	defer s.closeWorkbench(wb)

	wb.mu.Lock()
	wb.frameLocked(ctx, nil)
	wb.mu.Unlock()

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return
		}
		var op WorkbenchOpMessage
		if err := json.Unmarshal(raw, &op); err != nil || op.Type != MessageTypeWorkbench {
			continue
		}
		if !wb.apply(ctx, op) {
			return
		}
	}
}
