package zztgo

// Session recording (M14.2): a complete multiplayer session is just its world
// identity, the seeds, and the per-tick external stimuli — the inputs and the
// submits. Because the simulation is deterministic (CLAUDE.md rule 2), replaying
// those stimuli through the same entry points reproduces every room's state
// exactly. That is the determinism dividend: kilobytes of log stand in for
// gigabytes of frames, and it is the foundation for shareable replays, ghost
// racing, and verified leaderboards.
//
// The recorder sits at the RoomManager seam — the single serialized apply path
// the server funnels every join/leave/submit/step through under inst.mu. Every
// hook is a no-op when the recorder is nil, so recording OFF is byte-for-byte
// today's behavior. The recorder only ever READS simulation state (never mutates
// it, never enters StateHash or serialization), so determinism is untouched.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"sync"
	"sync/atomic"
)

// recordVersion is the on-disk schema version, bumped when the line format
// changes so playback can refuse a file it cannot read.
//
// v2 (M16.15a) added the "state" op: the account-sidecar inventory a returning
// signed-in player is joined with. A v1 file is refused rather than replayed,
// because the two are indistinguishable from the inside — a v1 recording of a
// returning account has no state op and no way to say one is missing, so this
// reader would reproduce it as a freshly spawned player and report success.
// Refusing is the only honest answer for a file recorded before the stimulus
// was captured.
const recordVersion = 2

// recordChannelCap bounds the buffered write channel. Flush never blocks the
// tick: a full channel drops the line and counts it (logged on Close) rather
// than stalling the simulation goroutine.
const recordChannelCap = 4096

// recHeader is the first line of a recording. WorldBytes carries the pristine
// starting world (the same bytes worldWriteTo produces), so a replay file is
// self-contained: a recipient needs nothing but the file. WorldHash is an
// FNV-1a integrity check over those bytes. Seed is the RandSeed every room
// engine is created with — 0 on the server path today, recorded explicitly so
// playback cannot silently diverge if that ever changes.
type recHeader struct {
	V          int    `json:"v"`
	World      string `json:"world"`
	WorldHash  uint64 `json:"worldHash"`
	WorldBytes string `json:"worldBytes"`
	Seed       uint32 `json:"seed"`
	// ChallengeID and ChallengeVersion say which challenge definition a run was
	// recorded under (M32.1). They are omitempty and deliberately do NOT bump
	// recordVersion: they add a fact about the recording rather than changing
	// how its stimuli replay, and a bump would refuse every recording and
	// fixture written before them.
	ChallengeID      string `json:"challengeId,omitempty"`
	ChallengeVersion int    `json:"challengeVersion,omitempty"`
	// FriendlyFire is the room manager's projectile/contact policy at the moment
	// the recording started. It used to be re-derived at playback from the world
	// identity; that derivation is gone (owner 2026-08-11), so a recording that
	// does not carry the flag has no way to say the session was played under it.
	// Recorded on the same terms as the challenge fields above — omitempty, no
	// recordVersion bump. A recording written before this field replays with the
	// policy off, which is what an absent flag has always meant everywhere else.
	FriendlyFire bool `json:"friendlyFire,omitempty"`
}

// recOp is one external stimulus applied before a tick's step: a join, a name,
// a state restore, a leave, or a submit (scroll reply, quit reply, debug
// command, save name, high-score name). Consequences the simulation derives
// from these — transfers, respawns, quit-driven removals — are NOT recorded;
// playback regenerates them.
//
// "state" is the one stimulus that does not originate at a keyboard: when a
// signed-in player rejoins, the server hands the fresh run the inventory it
// read out of that account's sidecar (ApplyPlayerState). It is external to the
// simulation in exactly the way a join is — a value the room could not have
// derived — so it is logged the same way, and State carries the whole struct
// rather than a diff so playback needs no knowledge of what a fresh spawn holds.
type recOp struct {
	Op     string       `json:"op"`             // join | name | state | leave | submit
	Kind   string       `json:"kind,omitempty"` // submit: scroll | quit | debug | save | highscore
	Player PlayerID     `json:"player"`
	Board  int16        `json:"board,omitempty"`
	X      int16        `json:"x,omitempty"`
	Y      int16        `json:"y,omitempty"`
	Name   string       `json:"name,omitempty"`
	StatID int16        `json:"statID,omitempty"`
	Label  string       `json:"label,omitempty"`
	Text   string       `json:"text,omitempty"`
	Quit   bool         `json:"quit,omitempty"`
	State  *PlayerState `json:"state,omitempty"` // state: the applied PlayerState
}

// recTick is one simulation tick: the ops applied just before it and the input
// map it was stepped with. Ticks are contiguous from 0 so playback advances the
// simulation exactly as many times as the live session did.
type recTick struct {
	Tick   int                      `json:"tick"`
	Ops    []recOp                  `json:"ops,omitempty"`
	Inputs map[PlayerID]PlayerInput `json:"inputs,omitempty"`
}

// SessionRecorder buffers the ops applied since the last tick and, on flush,
// emits one recTick to an async writer goroutine. All buffer access is under mu;
// the server already serializes every call through inst.mu, and mu keeps a raw
// RoomManager test race-clean too.
type SessionRecorder struct {
	ch   chan recTick
	done chan struct{}

	mu   sync.Mutex
	ops  []recOp
	tick int

	dropped int64 // atomic; incremented when a full channel drops a line
}

// NewSessionRecorder writes the header line synchronously (so a failure surfaces
// before any tick) and starts the writer goroutine. The writer owns w for the
// recorder's lifetime; if w is an io.Closer, Close closes it after the final
// flush.
func NewSessionRecorder(w io.Writer, header recHeader) (*SessionRecorder, error) {
	header.V = recordVersion
	bw := bufio.NewWriter(w)
	if err := writeJSONLine(bw, header); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}

	r := &SessionRecorder{
		ch:   make(chan recTick, recordChannelCap),
		done: make(chan struct{}),
	}
	go r.writeLoop(bw, w)
	return r, nil
}

func (r *SessionRecorder) writeLoop(bw *bufio.Writer, w io.Writer) {
	defer close(r.done)
	for rec := range r.ch {
		if err := writeJSONLine(bw, rec); err != nil {
			log.Printf("zztgo: session recorder write error: %v", err)
		}
	}
	if err := bw.Flush(); err != nil {
		log.Printf("zztgo: session recorder flush error: %v", err)
	}
	if c, ok := w.(io.Closer); ok {
		_ = c.Close()
	}
}

// record appends one op to the buffer for the next flush. Ops are kept in
// arrival order: two joins on the same board place the second player relative to
// the first, so the order changes StateHash and must be preserved.
func (r *SessionRecorder) record(op recOp) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.ops = append(r.ops, op)
	r.mu.Unlock()
}

// flush emits the tick that is about to run: the ops accumulated since the last
// flush plus the input map it will step with. Called at the top of StepDiffs, so
// the buffer holds exactly this tick's external ops (the server holds inst.mu
// across the whole join/submit/leave/step window, so nothing interleaves). The
// channel send is non-blocking: a full channel drops the line and counts it.
func (r *SessionRecorder) flush(inputs map[PlayerID]PlayerInput) {
	if r == nil {
		return
	}
	r.mu.Lock()
	rec := recTick{Tick: r.tick, Ops: r.ops}
	if len(inputs) > 0 {
		rec.Inputs = make(map[PlayerID]PlayerInput, len(inputs))
		for id, in := range inputs {
			rec.Inputs[id] = in
		}
	}
	r.ops = nil
	r.tick++
	r.mu.Unlock()

	select {
	case r.ch <- rec:
	default:
		atomic.AddInt64(&r.dropped, 1)
	}
}

// Close drains the writer, flushes the file, and logs any dropped ticks. After
// Close the recorder must not be used again.
func (r *SessionRecorder) Close() {
	if r == nil {
		return
	}
	close(r.ch)
	<-r.done
	if dropped := atomic.LoadInt64(&r.dropped); dropped > 0 {
		log.Printf("zztgo: session recorder dropped %d tick(s) under backpressure", dropped)
	}
}

func writeJSONLine(w io.Writer, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

// worldToBytes serializes a TWorld to ZZT's on-disk format, the same seam saves
// use. The bytes are self-contained and load back through LoadWorldBytes.
func worldToBytes(world TWorld) ([]byte, error) {
	scratch := newSnapshotEngine()
	scratch.World = world
	var buf bytes.Buffer
	if err := scratch.worldWriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// newSessionHeader builds a header capturing the pristine starting world.
func newSessionHeader(name string, world TWorld) (recHeader, []byte, error) {
	data, err := worldToBytes(world)
	if err != nil {
		return recHeader{}, nil, err
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	return recHeader{
		World:      name,
		WorldHash:  h.Sum64(),
		WorldBytes: base64.StdEncoding.EncodeToString(data),
		// Room engines are created by NewEngine with RandSeed 0; the server never
		// calls RandomSeed. Recorded so playback verifies the assumption.
		Seed: newSnapshotEngine().RandSeed,
	}, data, nil
}

// ReplaySession reconstructs a session from a recording and reproduces it tick
// by tick through the same RoomManager entry points the live session used
// (JoinPlayerWithID, SetPlayerName, Submit*, LeavePlayer, StepDiffs). onTick, if
// non-nil, is called after each step with the tick number and the manager, so a
// caller can checkpoint per-room StateHash. It returns the final manager.
func newRecordingScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return scanner
}

func readRecordingStart(scanner *bufio.Scanner) (recHeader, TWorld, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return recHeader{}, TWorld{}, err
		}
		return recHeader{}, TWorld{}, fmt.Errorf("empty recording: missing header")
	}
	var header recHeader
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return recHeader{}, TWorld{}, fmt.Errorf("bad header: %w", err)
	}
	if header.V != recordVersion {
		return recHeader{}, TWorld{}, fmt.Errorf("unsupported recording version %d (want %d)", header.V, recordVersion)
	}
	data, err := base64.StdEncoding.DecodeString(header.WorldBytes)
	if err != nil {
		return recHeader{}, TWorld{}, fmt.Errorf("bad world bytes: %w", err)
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	if got := h.Sum64(); got != header.WorldHash {
		return recHeader{}, TWorld{}, fmt.Errorf("world hash mismatch: header %d, bytes %d", header.WorldHash, got)
	}
	world, err := LoadWorldBytes(data)
	if err != nil {
		return recHeader{}, TWorld{}, fmt.Errorf("load world: %w", err)
	}
	return header, world, nil
}

func ReplaySession(r io.Reader, onTick func(tick int, rm *RoomManager)) (*RoomManager, error) {
	scanner := newRecordingScanner(r)
	header, world, err := readRecordingStart(scanner)
	if err != nil {
		return nil, err
	}
	rm := NewRoomManagerForWorld(world, header.World)
	rm.FriendlyFire = header.FriendlyFire

	for scanner.Scan() {
		var rec recTick
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("bad tick line: %w", err)
		}
		for _, op := range rec.Ops {
			applyRecordedOp(rm, op)
		}
		rm.StepDiffs(rec.Inputs)
		if onTick != nil {
			onTick(rec.Tick, rm)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rm, nil
}

// applyRecordedOp replays one external stimulus. Save is intentionally skipped:
// it writes a file (no simulation effect) and playback has no target directory,
// so it cannot and need not affect the reproduced state.
func applyRecordedOp(rm *RoomManager, op recOp) {
	switch op.Op {
	case "join":
		rm.JoinPlayerWithID(op.Player, op.Board, op.X, op.Y)
	case "name":
		rm.SetPlayerName(op.Player, op.Name)
	case "state":
		// A malformed line (op without its payload) applies nothing rather than
		// zeroing a live player's health.
		if op.State != nil {
			rm.ApplyPlayerState(op.Player, *op.State)
		}
	case "leave":
		rm.LeavePlayer(op.Player)
	case "submit":
		switch op.Kind {
		case "scroll":
			rm.SubmitScrollReply(op.Player, op.StatID, op.Label)
		case "quit":
			rm.SubmitQuitReply(op.Player, op.Quit)
		case "debug":
			rm.SubmitDebugCommand(op.Player, op.Text)
		case "highscore":
			rm.RecordHighScore(op.Player, op.Name)
		case "save":
			// no-op on playback (disk-only, no simulation effect)
		}
	}
}
