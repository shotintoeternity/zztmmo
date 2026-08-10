package zztgo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const ServerTickDuration = 110 * time.Millisecond
const ServerReadLimit = 1 << 20

// ReconnectGraceTicks is how long a detached player's stat lingers on the board
// before removal, counted in ticks (not wall-clock) so tests can step it
// deterministically. 545 ticks ≈ 60s at the 110ms server tick (M13.2).
const ReconnectGraceTicks = 545

// DefaultInstanceEvictIdleTicks is how long a non-default hosted world may sit
// with no players, detached reconnects, spectators, title subscribers or
// autosave before the server releases its RoomManager. Counted in ticks so the
// rule is deterministic and testable on the same clock as reconnect grace.
const DefaultInstanceEvictIdleTicks = 2727 // about five minutes at 110ms/tick

type WebSocketServer struct {
	RoomManager  *RoomManager
	DefaultBoard int16
	TickDuration time.Duration
	OriginHosts  []string
	// SavesDir is the only directory a client's save name can reach. Empty
	// refuses every save, which is what NewWebSocketServer leaves it as: a test
	// must never write to disk.
	SavesDir string
	// WorldsDir is where the editor writes published .ZZT worlds and where the
	// world picker lists them (M5.6). Empty falls back to the loaded world's
	// directory, then the working directory — the picker's historical behavior.
	WorldsDir string
	// AutosaveEveryTicks, when >0, snapshots every occupied instance every this
	// many ticks from the tick loop (M13.3). Zero disables. cmd/zzt-server sets it
	// from -autosave seconds via seconds*1000/tickMillis, so it shares the tick
	// clock rather than a second timer. NewWebSocketServer leaves it 0, so tests
	// never autosave unless they set the seam.
	AutosaveEveryTicks int
	autosaveTicks      int // countdown accumulator; touched only on the tick goroutine
	// GazetteFlushEveryTicks, when >0, writes the Gazette ledger every this many
	// ticks from the tick loop (M34.1). It sits beside AutosaveEveryTicks on
	// purpose: Record is memory-only, and this is the one place the ledger is
	// allowed to cost a tick a disk write. Zero disables the cadence; Flush is
	// still callable directly, which is what tests use. Nothing calls it on
	// shutdown yet, so a restart costs up to one cadence of counts — filed as
	// M34.1a rather than left as a comment that says otherwise.
	GazetteFlushEveryTicks int
	// InstanceEvictIdleTicks, when >0, evicts non-default instances after this
	// many idle ticks. NewWebSocketServer sets the production default; tests may
	// lower it to make the rule observable without sleeping.
	InstanceEvictIdleTicks int

	// RecordDir, when non-empty, is where per-instance session recordings are
	// written (M14.2). Set it through EnableRecording, which also stamps
	// recordStamp once so a restart does not clobber a prior run's files.
	RecordDir   string
	recordStamp string
	// ReplayDir is where /replay/<id> loads deterministic session recordings
	// from (M22.3). Replays are intentionally NOT WorldInstances: they are never
	// joinable, never shown in the picker, never counted as occupancy, and never
	// autosaved.
	ReplayDir string
	// ChallengeStore is the durable challenge leaderboard (M32.1). Nil means the
	// service has no leaderboard: runs still play and still report their result
	// to the player who made them, but nothing becomes public.
	ChallengeStore *ChallengeStore
	// challengeSeq mints the per-run suffix that makes each attempt's instance
	// key and recording id unique. Guarded by mu.
	challengeSeq int64
	// ghostCache holds extracted ghost tracks by recording id (M32.1). Its own
	// lock: a ghost is presentation state read off finished recordings, so it is
	// never touched from a tick.
	ghostCache challengeGhostCache
	// Postcards are generated GIFs over bounded replay ranges (M22.4). They are
	// service-layer presentation state: cached by recording id + range, and
	// rate-limited by HTTP client, never by the simulation.
	postcardCache   replayPostcardCache
	postcardLimiter postcardRateLimiter

	// Now is the injected non-simulation clock (M16.16a); nil means time.Now.
	// It feeds only service-layer state like the chat rate limiter — never the
	// simulation or the replay hash.
	Now func() time.Time
	// chatLimiter holds each connected player's rolling chat-admission window;
	// it has its own lock and is not guarded by mu.
	chatLimiter chatRateLimiter
	// chatBlocks holds who each connected player has asked not to hear (M21.1).
	// Its own lock too, and never held with mu or inst.mu: it is consulted once
	// per recipient inside the chat fan-out.
	chatBlocks chatBlocks
	// Moderators is the account allowlist that decides who may mute, kick and
	// refuse (M21.2). Nil or empty means there are no operators — every operator
	// action fails closed, which is what an unset ZZT_MODERATOR_ACCOUNTS must
	// mean. Set at startup and read-only afterwards: operator status is
	// deployment configuration and cannot be granted from inside the game.
	Moderators map[string]bool
	// Refusals is the durable list of accounts that may not rejoin, and Audit is
	// the record of every action an operator took. Both are non-nil after
	// NewWebSocketServer and both are memory-only until a path is configured.
	Refusals *RefusalStore
	Audit    *ModerationAudit
	// mutes is who may not speak. Its own lock, like chatBlocks, and process
	// scoped by decision — see moderation.go.
	mutes moderationMutes
	// moderateLimiter bounds how often one connection may ask for an operator
	// action, on the same policy as chat. It is what keeps the audit (which
	// records denials, and should) from being a hostile client's write amplifier.
	moderateLimiter chatRateLimiter

	mu sync.Mutex
	// nextPlayerID mints process-unique PlayerIDs across every instance, so ids
	// never collide between hosted worlds (M14.1). Guarded by mu.
	nextPlayerID        PlayerID
	Instances           map[string]*WorldInstance
	ReplayInstances     map[string]*ReplayInstance
	DefaultInstance     *WorldInstance
	EditorSessions      map[*webSocketClient]*EditorSession
	EditorWorldSessions map[string]*EditorSession
	ChatDB              ChatDatabase
	Activity            *WorldActivityStore
	Auth                *AuthService
	// Gazette is the day's ledger (M34.1). Nil is a supported configuration —
	// every record path is nil-safe — and the default is memory-only until
	// cmd/zzt-server points it at the saves directory.
	Gazette *GazetteLedger
	// gazetteTicks counts toward GazetteFlushEveryTicks. Touched only on the
	// tick goroutine, like autosaveTicks.
	gazetteTicks int
	metrics      *serverMetrics
}

type WorldInstance struct {
	Name string
	// SourceWorld is the pristine content used for isolated title and editor
	// copies. RoomManager's frozen world is live state and must not seed edits.
	SourceWorld TWorld
	RoomManager *RoomManager
	Clients     map[PlayerID]*webSocketClient
	Inputs      map[PlayerID]PlayerInput
	// Title animates board 0 for browsers sitting on the title screen. It owns
	// its own Engine and shares no state with RoomManager — see TitleSim.
	Title *TitleSim
	// Detached, ResumeTokens, and TokensByPlayer implement reconnect grace
	// (M13.2). A dropped socket detaches its player (leaves Clients/Inputs but
	// keeps the stat) and Detached counts down ReconnectGraceTicks to removal on
	// the tick goroutine. ResumeTokens maps a minted token to its player, and
	// TokensByPlayer is the reverse index used to delete a player's token on
	// removal. All three are guarded by mu.
	Detached       map[PlayerID]int
	ResumeTokens   map[string]PlayerID
	TokensByPlayer map[PlayerID]string
	// Spectators are the read-only watchers of this world (M22.1), keyed by their
	// own connection because they have no PlayerID to be keyed by — which is the
	// point: they are not in Clients, so nothing that walks the players of a world
	// (the roster, occupancy, chat, autosave, the resume tokens) can see them.
	// Guarded by mu, like Clients.
	Spectators map[*webSocketClient]*spectator
	// spectatorDrops counts the messages watchers have sent and had discarded.
	// Tests only, and it exists to tell one outcome from another: a test that
	// only checks the room did not move passes just as well when its input never
	// arrived, and "arrived and was dropped" is the claim.
	spectatorDrops int
	idleTicks      int
	autosaving     bool
	// Private marks an instance nobody chose to make public: today that is an
	// editor test-play copy (M10.4). It exists because that copy is the one
	// private instance kind NOT excluded by construction — randomTestPlayWorldName
	// mints TP+6 hex, which sanitizes as cleanly as TOWN does, so without a mark
	// a play-test death would be printed in the Gazette as news about a world
	// nobody can visit (M34.1).
	Private bool
	// Challenge is set only on a challenge run's instance (M32.1). Its presence
	// is what makes this instance measured, always-recorded, and closed to the
	// things that would let a run be gamed or leak into the source world.
	Challenge *ChallengeRun
	// RecordWorld and RecordID split what attachRecorderLocked used to take from
	// Name alone: the recording's HEADER world (which /replay resolves against
	// the hosting directory) and the recording's file stem (which must pass
	// sanitizeReplayID). An ordinary instance leaves both empty and is recorded
	// exactly as before.
	RecordWorld string
	RecordID    string
	mu          sync.Mutex
}

type ReplayInstance struct {
	ID         string
	path       string
	startTick  int
	playback   *ReplayPlayback
	file       *os.File
	Spectators map[*webSocketClient]*spectator
	Paused     bool
	done       bool
	err        error
	mu         sync.Mutex
}

// spectator is one watching connection.
type spectator struct {
	client  *webSocketClient
	boardID int16
	// engine is the room engine the last frame was rendered from, or nil while
	// the board is idle. A watcher gets a fresh whole-screen snapshot whenever
	// this changes: a room is created when its first player arrives and destroyed
	// (frozen back into the world) when its last one leaves, so an incremental
	// diff stream cannot survive that boundary — and the joining player's own
	// snapshot may have drained the cells that would have carried it.
	engine *Engine
	// sentWatchers is the count this connection was last told. It is what lets an
	// idle board — which produces no diffs at all, because nothing is running —
	// still learn that someone else started or stopped watching it.
	sentWatchers int
}

type webSocketClient struct {
	playerID  PlayerID
	boardID   int16
	conn      *websocket.Conn
	mu        sync.Mutex
	worldName string
	accountID string
	name      string

	// The outbound queue (M16.14e). Every message for this client is handed to
	// out and written by one writer goroutine that owns the connection, so no
	// broadcaster ever waits on this browser: not the tick goroutine, which
	// walks every client of every hosted world, and not the editor's fan-out
	// gate, which serializes one session's broadcasts. Before this, a write
	// that could not complete inside a second closed the socket under the
	// browser — a collaborator was dropped to the title screen for being busy
	// for one second (M16.14e).
	//
	// out, quit, writerDone and writeErr are guarded by mu. A client built
	// without a connection (the session tests build these) has a nil out and
	// nothing to write to; its write is a no-op.
	out        chan interface{}
	quit       chan struct{}
	writerDone chan struct{}
	writeErr   error
}

const (
	// clientOutboundQueue is how many messages the server will hold for one
	// client that is not reading. A game client is handed roughly one message a
	// tick and an editor two per keystroke, so this is seconds of stall for the
	// editor and half a minute for a player — far more than the one second that
	// used to be fatal, and still bounded so a client that has genuinely stopped
	// reading cannot grow the server's memory without limit.
	clientOutboundQueue = 256
	// clientWriteTimeout bounds ONE message's time on the wire. Reaching it
	// means the connection is dead rather than slow: the queue, not this
	// deadline, is what absorbs a browser that stalls.
	clientWriteTimeout = 30 * time.Second
	// clientDrainTimeout bounds how long a connection teardown waits for the
	// messages already queued to reach the browser. It preserves what the old
	// synchronous write gave for free — a reply written just before the handler
	// returns still goes out — without letting a wedged socket hold the
	// handler open.
	clientDrainTimeout = time.Second
)

// errClientStopped ends the writer when its connection is being torn down. It
// is not a delivery failure: whatever is already queued is still flushed.
var errClientStopped = errors.New("client connection closed")

// newWebSocketClient wires a client to its connection and starts the single
// goroutine that writes to it. Callers must pair it with defer client.stop().
func newWebSocketClient(conn *websocket.Conn, worldName string) *webSocketClient {
	client := &webSocketClient{
		conn:       conn,
		worldName:  worldName,
		out:        make(chan interface{}, clientOutboundQueue),
		quit:       make(chan struct{}),
		writerDone: make(chan struct{}),
	}
	go client.writeLoop()
	return client
}

// writeLoop is the only goroutine that touches conn for writing, so messages
// reach this browser in the order they were queued.
func (c *webSocketClient) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case message := <-c.out:
			if !c.writeNow(message) {
				return
			}
		case <-c.quit:
			// Flush what is already queued, so a teardown does not swallow the
			// reply that caused it. A dead connection fails the first of these
			// immediately, so this cannot linger.
			for {
				select {
				case message := <-c.out:
					if !c.writeNow(message) {
						return
					}
				default:
					return
				}
			}
		}
	}
}

// writeNow puts one message on the wire and reports whether the connection is
// still usable. The deadline is the client's own, never the caller's: a
// broadcast used to hand one member's request context to another member's
// write, so a member who had just left could expire an innocent recipient's
// deadline and close their socket.
func (c *webSocketClient) writeNow(message interface{}) bool {
	ctx, cancel := context.WithTimeout(context.Background(), clientWriteTimeout)
	defer cancel()
	if err := wsjson.Write(ctx, c.conn, message); err != nil {
		c.fail(err)
		return false
	}
	return true
}

// markDead records the first reason this client can take no more messages and
// releases the writer. It reports whether this call was that first reason.
func (c *webSocketClient) markDead(err error) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.out == nil || c.writeErr != nil {
		return false
	}
	c.writeErr = err
	close(c.quit)
	return true
}

// fail closes the socket under a client that cannot be written to. Closing it
// is what ends that connection's read loop, which runs the same teardown a
// browser closing its tab does — detach with reconnect grace for a player, and
// session.Exit for an editor member.
//
// CloseNow, not Close: fail runs on whichever goroutine was writing, including
// the tick goroutine, and a close handshake can take seconds.
func (c *webSocketClient) fail(err error) {
	if !c.markDead(err) {
		return
	}
	if c.conn != nil {
		_ = c.conn.CloseNow()
	}
}

// stop ends the writer when a connection is being torn down, giving the
// messages already queued a bounded chance to reach the browser first.
func (c *webSocketClient) stop() {
	c.markDead(errClientStopped)
	c.mu.Lock()
	done := c.writerDone
	c.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(clientDrainTimeout):
	}
}

// queued reports how many messages are waiting for this client. Tests only: it
// is how a test proves it really did stall a reader rather than passing because
// the kernel happened to buffer everything.
func (c *webSocketClient) queued() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.out)
}

func NewWebSocketServer(world TWorld, defaultBoard int16) *WebSocketServer {
	rm := NewRoomManagerForWorld(world, world.Info.Name)
	name := rm.WorldName()
	if name == "Untitled" || name == "" {
		name = "TOWN"
		rm.WorldIdentity = name
		rm.FriendlyFire = friendlyFireForWorldIdentity(name)
	}
	inst := &WorldInstance{
		Name:           name,
		SourceWorld:    cloneWorld(world),
		RoomManager:    rm,
		Clients:        make(map[PlayerID]*webSocketClient),
		Inputs:         make(map[PlayerID]PlayerInput),
		Title:          NewTitleSim(world),
		Detached:       make(map[PlayerID]int),
		ResumeTokens:   make(map[string]PlayerID),
		TokensByPlayer: make(map[PlayerID]string),
		Spectators:     make(map[*webSocketClient]*spectator),
	}
	configureLobbyTransits(inst)
	s := &WebSocketServer{
		RoomManager:            rm,
		DefaultBoard:           defaultBoard,
		TickDuration:           ServerTickDuration,
		OriginHosts:            []string{"localhost:*", "127.0.0.1:*"},
		InstanceEvictIdleTicks: DefaultInstanceEvictIdleTicks,
		Instances:              make(map[string]*WorldInstance),
		ReplayInstances:        make(map[string]*ReplayInstance),
		EditorSessions:         make(map[*webSocketClient]*EditorSession),
		EditorWorldSessions:    make(map[string]*EditorSession),
		ChatDB:                 NewMemChatDatabase(),
		Activity:               &WorldActivityStore{counts: make(map[string]int)},
		// Memory-only until cmd/zzt-server points them at the saves directory, so
		// a test never writes an audit line or a refusal to disk (M21.2).
		Refusals: NewRefusalStore(""),
		Audit:    NewModerationAudit(""),
		metrics:  newServerMetrics(time.Now()),
	}
	// Memory-only for the same reason (M34.1), and on the server's own clock
	// seam so a test that moves Now moves the day the paper is filed under.
	s.Gazette, _ = NewGazetteLedger("", s.clockNow)
	s.DefaultInstance = inst
	s.Instances[name] = inst
	return s
}

func (s *WebSocketServer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.TickDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.CloseRecorders()
			s.CloseReplays()
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

func (s *WebSocketServer) Tick(ctx context.Context) {
	start := time.Now()
	defer func() {
		s.metrics.recordServerTick(time.Since(start))
	}()
	// Advance reconnect-grace countdowns before stepping, so an expired player is
	// removed on this same tick goroutine (M13.2).
	s.expireDetached()

	// Every hosted world — including the default instance — ticks through the
	// same WorldInstance.Tick path under its own inst.mu. The default used to be
	// stepped by a separate "legacy" block under s.mu, which meant its RoomManager
	// was guarded by two different mutexes depending on the code path (s.mu when
	// stepping, inst.mu on join/leave/detach); that split was M13.4's race
	// (DrainPlayerEvents from Tick under s.mu vs handleReadLoopExit under inst.mu).
	// With one lock per instance, the tick and every join/leave/detach on the same
	// RoomManager are mutually exclusive.
	s.mu.Lock()
	var instances []*WorldInstance
	var replays []*ReplayInstance
	var titles []*TitleSim
	for _, inst := range s.Instances {
		if inst.Title != nil {
			titles = append(titles, inst.Title)
		}
		instances = append(instances, inst)
	}
	for _, replay := range s.ReplayInstances {
		replays = append(replays, replay)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.Tick(ctx, s)
	}
	for _, replay := range replays {
		replay.Tick(ctx)
	}
	for _, title := range titles {
		title.Tick()
	}

	s.maybeAutosave()
	s.maybeFlushGazette()
	s.evictIdleInstances()
}

// maybeAutosave counts ticks toward the autosave cadence and fires Autosave when
// it comes due. It runs on the tick goroutine, so autosaveTicks needs no lock.
func (s *WebSocketServer) maybeAutosave() {
	if s.AutosaveEveryTicks <= 0 {
		return
	}
	s.autosaveTicks++
	if s.autosaveTicks < s.AutosaveEveryTicks {
		return
	}
	s.autosaveTicks = 0
	s.Autosave()
}

// maybeFlushGazette writes the day's ledger on its own cadence. Recording a
// death must never wait on a file (M16.14e), so every Record is memory-only and
// this is where the writing happens — beside maybeAutosave, on the goroutine
// this server already decided may pay for disk.
func (s *WebSocketServer) maybeFlushGazette() {
	if s.GazetteFlushEveryTicks <= 0 || s.Gazette == nil {
		return
	}
	s.gazetteTicks++
	if s.gazetteTicks < s.GazetteFlushEveryTicks {
		return
	}
	s.gazetteTicks = 0
	if err := s.Gazette.Flush(); err != nil {
		log.Printf("zztgo: gazette ledger not written: %v", err)
	}
}

// recordGazette files one happening against the world identity it happened in.
// Nil ledger, guest account and refused subject are all ordinary outcomes here:
// the point of a single funnel is that no caller has to know which.
func (s *WebSocketServer) recordGazette(kind, subject, accountID string) {
	if s == nil || s.Gazette == nil {
		return
	}
	_ = s.Gazette.Record(GazetteHappening{Kind: kind, Subject: subject, AccountKey: accountID})
}

func (s *WebSocketServer) evictIdleInstances() {
	if s.InstanceEvictIdleTicks <= 0 {
		return
	}

	var recorders []*SessionRecorder
	var evicted []string

	s.mu.Lock()
	for name, inst := range s.Instances {
		if inst == nil || inst == s.DefaultInstance {
			continue
		}
		recorder, ok := inst.advanceIdleEvictionLocked(s.InstanceEvictIdleTicks)
		if !ok {
			continue
		}
		delete(s.Instances, name)
		evicted = append(evicted, name)
		if recorder != nil {
			recorders = append(recorders, recorder)
		}
	}
	s.mu.Unlock()

	for _, name := range evicted {
		s.metrics.forgetInstance(name)
	}
	for _, recorder := range recorders {
		recorder.Close()
	}
}

func (inst *WorldInstance) advanceIdleEvictionLocked(limit int) (*SessionRecorder, bool) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if !inst.evictableIdleLocked() {
		inst.idleTicks = 0
		return nil, false
	}
	inst.idleTicks++
	if inst.idleTicks < limit {
		return nil, false
	}
	recorder := inst.RoomManager.recorder
	inst.RoomManager.SetRecorder(nil)
	return recorder, true
}

func (inst *WorldInstance) evictableIdleLocked() bool {
	if len(inst.Clients) != 0 ||
		len(inst.Inputs) != 0 ||
		len(inst.Detached) != 0 ||
		len(inst.Spectators) != 0 ||
		inst.autosaving {
		return false
	}
	if inst.Title != nil && inst.Title.SubscriberCount() != 0 {
		return false
	}
	if inst.RoomManager == nil || len(inst.RoomManager.players) != 0 {
		return false
	}
	return true
}

func (inst *WorldInstance) Tick(ctx context.Context, s *WebSocketServer) {
	tickStart := time.Now()
	var stepDuration time.Duration
	var transits []WorldTransit
	inst.mu.Lock()
	inputs := inst.Inputs
	inst.Inputs = make(map[PlayerID]PlayerInput)
	stepStart := time.Now()
	diffs, boardDiffs := safeStepDiffs(inst.Name, inst.RoomManager, inputs)
	stepDuration = time.Since(stepStart)
	watchers := inst.watcherCountsLocked()
	clients := make(map[PlayerID]*webSocketClient, len(inst.Clients))
	messages := make(map[PlayerID]interface{}, len(diffs))
	for playerID, client := range inst.Clients {
		clients[playerID] = client
	}
	for playerID, diff := range diffs {
		client := inst.Clients[playerID]
		if client == nil {
			continue
		}
		if client.boardID != 0 && client.boardID != diff.BoardID {
			snapshot, ok := inst.RoomManager.Snapshot(playerID)
			if ok {
				snapshot.World = inst.publicWorldName()
				client.boardID = snapshot.BoardID
				snapshot.Events = append(snapshot.Events, ProtocolEvents(inst.RoomManager.DrainPlayerEvents(playerID))...)
				snapshot.Watchers = watchers[snapshot.BoardID]
				messages[playerID] = BoardChangeMessage{Type: MessageTypeBoardChange, Snapshot: snapshot}
				continue
			}
		}
		client.boardID = diff.BoardID
		diff.Events = append(diff.Events, ProtocolEvents(inst.RoomManager.DrainPlayerEvents(playerID))...)
		// M22.1: how many people are watching the room this player is standing in.
		diff.Watchers = watchers[diff.BoardID]
		messages[playerID] = diff
	}
	for _, quit := range inst.RoomManager.DrainQuits() {
		if inst.Clients[quit.PlayerID] == nil {
			continue
		}
		messages[quit.PlayerID] = s.quitOutcome(inst.RoomManager, quit)
	}
	watched := inst.spectatorMessagesLocked(boardDiffs, watchers)
	transits = append(transits, inst.RoomManager.DrainWorldTransits()...)
	// M34.1: the deeds this step produced, resolved to accounts here while the
	// clients map is in hand and filed after the unlock. A challenge run and an
	// editor test-play copy are instances nobody chose to make public, so they
	// are drained (the manager must not accumulate) and dropped.
	var notables []GazetteHappening
	if inst.Challenge == nil && !inst.Private {
		for _, notable := range inst.RoomManager.DrainNotables() {
			accountID := ""
			if client := inst.Clients[notable.PlayerID]; client != nil {
				accountID = client.accountID
			}
			notables = append(notables, GazetteHappening{
				Kind:       notable.Kind,
				Subject:    inst.Name,
				AccountKey: accountID,
			})
		}
	} else {
		inst.RoomManager.DrainNotables()
	}
	// M32.1: a challenge run counts its own tick and is asked whether the goal
	// is met, under the same lock the step just ran beneath — so the count can
	// never drift from the simulation it measures. The completion itself is
	// carried out of the lock: closing the recording and writing a leaderboard
	// row are file work, and the tick does not wait on either.
	completion := inst.advanceChallengeLocked()
	inst.mu.Unlock()

	for playerID, message := range messages {
		client := clients[playerID]
		if client != nil {
			_ = client.write(ctx, message)
		}
	}
	for _, delivery := range watched {
		_ = delivery.client.write(ctx, delivery.message)
	}
	for _, transit := range transits {
		s.completeWorldTransit(ctx, inst, transit)
	}
	for _, notable := range notables {
		s.recordGazette(notable.Kind, notable.Subject, notable.AccountKey)
	}
	if completion != nil {
		result := s.finishChallengeRun(inst, completion)
		inst.mu.Lock()
		client := inst.Clients[completion.run.PlayerID]
		inst.mu.Unlock()
		if client != nil {
			_ = client.write(ctx, result)
		}
	}
	s.metrics.recordInstanceTick(inst.Name, stepDuration, time.Since(tickStart))
}

func (replay *ReplayInstance) Tick(ctx context.Context) {
	replay.mu.Lock()
	if replay.Paused || replay.done || replay.err != nil {
		replay.mu.Unlock()
		return
	}
	_, boardDiffs, done, err := replay.playback.Step()
	replay.done = done
	replay.err = err
	watchers := replay.watcherCountsLocked()
	watched := replay.spectatorMessagesLocked(boardDiffs, watchers)
	if done && err == nil {
		watched = append(watched, replay.finalMessagesLocked(watchers)...)
	}
	replay.mu.Unlock()

	for _, delivery := range watched {
		_ = delivery.client.write(ctx, delivery.message)
	}
}

func (replay *ReplayInstance) Close() {
	replay.mu.Lock()
	file := replay.file
	replay.file = nil
	replay.mu.Unlock()
	if file != nil {
		_ = file.Close()
	}
}

func newReplayPlaybackFromPath(path string, startTick int) (*ReplayPlayback, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	playback, err := NewReplayPlayback(f)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	for startTick > 0 && playback.LastTick() < startTick-1 {
		_, _, done, err := playback.Step()
		if err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		if done {
			break
		}
	}
	return playback, f, nil
}

func (replay *ReplayInstance) Restart() error {
	playback, f, err := newReplayPlaybackFromPath(replay.path, replay.startTick)
	if err != nil {
		return err
	}

	replay.mu.Lock()
	old := replay.file
	replay.file = f
	replay.playback = playback
	replay.Paused = false
	replay.done = false
	replay.err = nil
	for _, sub := range replay.Spectators {
		sub.engine = nil
	}
	replay.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// watcherCountsLocked is how many watchers each board has. Caller holds inst.mu.
func (inst *WorldInstance) watcherCountsLocked() map[int16]int {
	if len(inst.Spectators) == 0 {
		return nil
	}
	counts := make(map[int16]int, len(inst.Spectators))
	for _, sub := range inst.Spectators {
		counts[sub.boardID]++
	}
	return counts
}

func (replay *ReplayInstance) watcherCountsLocked() map[int16]int {
	if len(replay.Spectators) == 0 {
		return nil
	}
	counts := make(map[int16]int, len(replay.Spectators))
	for _, sub := range replay.Spectators {
		counts[sub.boardID]++
	}
	return counts
}

// spectatorDelivery is one watcher's frame, addressed by connection because a
// watcher has no PlayerID.
type spectatorDelivery struct {
	client  *webSocketClient
	message interface{}
}

// spectatorMessagesLocked builds this tick's frame for every watcher (M22.1).
// Caller holds inst.mu; the writes happen after it is released, like the
// players'.
//
// Three cases, and the middle one is the reason this is not just "hand them the
// board diff":
//
//   - the room this watcher is on has appeared, or been replaced by a new engine
//     — the first player arriving on a board builds it, the last one leaving
//     freezes and destroys it — so the incremental stream has no past to build
//     on and a whole screen is sent instead;
//   - the room is live and unchanged: the same board diff the players on it get,
//     minus their HUD and their events;
//   - the board is idle: nothing is running, so there is nothing to say except
//     when the watcher count itself changed, which no diff would otherwise carry.
func (inst *WorldInstance) spectatorMessagesLocked(boardDiffs map[int16]DiffMessage, watchers map[int16]int) []spectatorDelivery {
	if len(inst.Spectators) == 0 {
		return nil
	}
	deliveries := make([]spectatorDelivery, 0, len(inst.Spectators))
	for _, sub := range inst.Spectators {
		count := watchers[sub.boardID]
		var engine *Engine
		if room, live := inst.RoomManager.Room(sub.boardID); live {
			engine = room.Engine
		}

		if engine != sub.engine {
			sub.engine = engine
			if engine == nil {
				// The room just froze. The last frame it sent is what that board
				// looks like now, so leaving it on screen is the truth, not a
				// stale picture — and re-rendering the frozen bytes would be the
				// same cells at the cost of an engine.
				continue
			}
			snapshot, _ := inst.RoomManager.SpectatorSnapshot(sub.boardID)
			snapshot.Watchers = count
			sub.sentWatchers = count
			deliveries = append(deliveries, spectatorDelivery{client: sub.client, message: snapshot})
			continue
		}

		if engine != nil {
			diff, stepped := boardDiffs[sub.boardID]
			if !stepped {
				continue
			}
			diff.Watchers = count
			sub.sentWatchers = count
			deliveries = append(deliveries, spectatorDelivery{client: sub.client, message: diff})
			continue
		}

		if count != sub.sentWatchers {
			sub.sentWatchers = count
			deliveries = append(deliveries, spectatorDelivery{
				client:  sub.client,
				message: DiffMessage{Type: MessageTypeDiff, BoardID: sub.boardID, Watchers: count},
			})
		}
	}
	return deliveries
}

func (replay *ReplayInstance) spectatorMessagesLocked(boardDiffs map[int16]DiffMessage, watchers map[int16]int) []spectatorDelivery {
	if len(replay.Spectators) == 0 || replay.playback == nil || replay.playback.RoomManager() == nil {
		return nil
	}
	rm := replay.playback.RoomManager()
	deliveries := make([]spectatorDelivery, 0, len(replay.Spectators))
	for _, sub := range replay.Spectators {
		count := watchers[sub.boardID]
		var engine *Engine
		if room, live := rm.Room(sub.boardID); live {
			engine = room.Engine
		}

		if engine != sub.engine {
			sub.engine = engine
			snapshot, _ := rm.SpectatorSnapshot(sub.boardID)
			snapshot.Watchers = count
			sub.sentWatchers = count
			deliveries = append(deliveries, spectatorDelivery{client: sub.client, message: snapshot})
			continue
		}
		if engine != nil {
			diff, stepped := boardDiffs[sub.boardID]
			if !stepped {
				continue
			}
			diff.Watchers = count
			sub.sentWatchers = count
			deliveries = append(deliveries, spectatorDelivery{client: sub.client, message: diff})
			continue
		}
		if count != sub.sentWatchers {
			sub.sentWatchers = count
			deliveries = append(deliveries, spectatorDelivery{
				client:  sub.client,
				message: DiffMessage{Type: MessageTypeDiff, BoardID: sub.boardID, Watchers: count},
			})
		}
	}
	return deliveries
}

func (replay *ReplayInstance) finalMessagesLocked(watchers map[int16]int) []spectatorDelivery {
	if len(replay.Spectators) == 0 || replay.playback == nil || replay.playback.RoomManager() == nil {
		return nil
	}
	hashes := replay.playback.RoomStateHashes()
	deliveries := make([]spectatorDelivery, 0, len(replay.Spectators))
	for _, sub := range replay.Spectators {
		hash := hashes[sub.boardID]
		deliveries = append(deliveries, spectatorDelivery{
			client: sub.client,
			message: DiffMessage{
				Type:     MessageTypeDiff,
				BoardID:  sub.boardID,
				Tick:     int16(replay.playback.LastTick()),
				Hash:     hash,
				Watchers: watchers[sub.boardID],
			},
		})
	}
	return deliveries
}

// RoomManager.StepDiffs isolates simulation panics per room.  This outer guard
// keeps a future panic in room routing or diff construction from escaping the
// instance tick goroutine and taking down other hosted worlds.
func safeStepDiffs(worldName string, rm *RoomManager, inputs map[PlayerID]PlayerInput) (diffs map[PlayerID]DiffMessage, boardDiffs map[int16]DiffMessage) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("zztgo: isolating world %q after tick panic: %v", worldName, recovered)
			diffs = make(map[PlayerID]DiffMessage)
			boardDiffs = make(map[int16]DiffMessage)
		}
	}()
	return rm.StepDiffsWithBoards(inputs)
}

func (s *WebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if replayID := r.URL.Query().Get("replay"); replayID != "" {
		s.serveReplayHTTP(w, r, replayID)
		return
	}

	// M32.1: a challenge join names a catalogue id (start a fresh attempt) or a
	// run key (reclaim the attempt this browser is already in). Both resolve
	// AFTER the socket is accepted and the account is known — the instance a run
	// gets is minted per attempt and per player, so there is nothing to resolve
	// until we know who is asking.
	challengeID := strings.TrimSpace(r.URL.Query().Get("challenge"))
	challengeRunRequested := strings.TrimSpace(r.URL.Query().Get("run"))

	worldName := r.URL.Query().Get("world")
	var inst *WorldInstance
	var safeWorld string
	switch {
	case challengeID != "" || challengeRunRequested != "":
		// Resolved below, once the join message and the account have been read.
	case worldName == "":
		inst = s.DefaultInstance
		safeWorld = inst.Name
	default:
		var err error
		safeWorld, err = SanitizeSaveName(worldName)
		if err != nil {
			http.Error(w, "invalid world name", http.StatusBadRequest)
			return
		}
		inst, err = s.GetOrCreateInstance(safeWorld)
		if err != nil {
			http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
			return
		}
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
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	if envelope.Type == MessageTypeEditorEnter {
		var enter EditorEnterMessage
		if err := json.Unmarshal(raw, &enter); err != nil {
			return
		}
		account, authenticated := s.authAccount(r)
		// A refused account is refused everywhere it can be recognised (M21.2),
		// and the editor is a door into the same server: admitting them here
		// would make "refuse" mean "refuse to play".
		if notice, refused := s.refusedAtTheDoor(account, authenticated); refused {
			_ = wsjson.Write(ctx, conn, notice)
			return
		}
		s.serveEditor(ctx, conn, enter, account, authenticated)
		return
	}
	if envelope.Type != MessageTypeJoin {
		return
	}
	var join JoinMessage
	if err := json.Unmarshal(raw, &join); err != nil {
		return
	}

	// The color is validated once, here, where the untrusted join is read —
	// beside the name it sits next to on the wire (M19.1). Everything
	// downstream stores and broadcasts whatever this returns, so anything that
	// is not "#" plus six hex digits must become the empty string at this line
	// and never later.
	joinColor := SanitizePlayerColor(join.Color)

	client := newWebSocketClient(conn, safeWorld)
	defer client.stop()
	account, authenticated := s.authAccount(r)
	// Before anything is joined, spawned or resumed (M21.2). A refusal that took
	// effect after the join would put the player in the room for a tick and take
	// the reconnect path out of it, which is a drop rather than a refusal — and
	// the resume token below would let them reclaim it.
	if notice, refused := s.refusedAtTheDoor(account, authenticated); refused {
		_ = client.write(ctx, notice)
		return
	}

	// M32.1: the challenge run this connection is playing, or nil. Resolved
	// here — after the refusal, like watching — because a challenge is a door
	// into the same server, and a refused account must not walk through the one
	// that happens to hand out leaderboard rows.
	var challengeRun *ChallengeRun
	if challengeID != "" || challengeRunRequested != "" {
		if join.Spectate {
			// A run is one player's measured attempt, and it is not addressable
			// by anyone else: there is no watch route to it, and asking for one
			// here would be the only way to reach another player's run.
			_ = client.write(ctx, ChallengeErrorMessage{Type: MessageTypeChallengeError, ChallengeID: challengeID, Reason: "A challenge run cannot be watched."})
			return
		}
		var err error
		inst, challengeRun, err = s.resolveChallengeJoin(challengeID, challengeRunRequested, account, authenticated)
		if err != nil {
			_ = client.write(ctx, ChallengeErrorMessage{Type: MessageTypeChallengeError, ChallengeID: challengeID, Reason: challengeRefusalText(err)})
			return
		}
		safeWorld = inst.Name
		client.setWorldName(inst.Name)
	}

	// M22.1: a watcher takes none of the paths below — no stored state, no color,
	// no resume, no stat, no read of anything they typed. It branches here, after
	// the refusal, because watching is a door into the same server and a refused
	// account must not be able to walk through the one that happens to be
	// read-only.
	if join.Spectate {
		s.serveSpectator(ctx, conn, client, inst, join)
		return
	}

	var storedState PlayerState
	hasStoredState := false
	if authenticated {
		client.accountID = account.ID
		join.Name = account.DisplayName()
		storedState, hasStoredState = s.loadAccountPlayerState(account.ID, safeWorld)
		// The account, not this browser, is where a signed-in player's color
		// lives (M19.3). Resolved here so both the fresh join and the resume
		// below see one already-decided value.
		joinColor = s.resolveAccountPlayerColor(account.ID, joinColor)
	}
	profileHandle, hasProfile := s.accountProfileSummary(account.ID)

	// Resume first: a valid token reclaims the dropped run (same PlayerID/statID,
	// inventory intact). An unknown or expired token falls through to a fresh join.
	var playerID PlayerID
	var snapshot SnapshotMessage
	resumed := false
	if join.ResumeToken != "" {
		if pid, snap, ok := s.tryResume(inst, client, join.ResumeToken); ok {
			playerID, snapshot, resumed = pid, snap, true
			inst.mu.Lock()
			if authenticated {
				inst.RoomManager.SetPlayerIdentity(playerID, account.ID, account.DisplayName())
			}
			// A resumed run keeps its inventory but not its color: the
			// reclaimed roomPlayer predates this connection, and the color is
			// a property of the browser that is here now (M19.1). Re-applying
			// it means a reconnect looks the same as it did before the drop.
			// The snapshot tryResume already built is left alone — rebuilding
			// it would drain the room's dirty cells a second time (see
			// Snapshot) — so the color reaches this client on the first diff
			// instead, one tick later, which is how the roster reaches it
			// every other time it changes.
			inst.RoomManager.SetPlayerColor(playerID, joinColor)
			if authenticated {
				inst.RoomManager.SetPlayerProfileSummary(playerID, profileHandle, hasProfile)
			}
			inst.mu.Unlock()
		}
	}

	// M32.1: a `?run=` join RECLAIMS an attempt and may never start a player in
	// one. Without this, a token-less run key fresh-joins a second player into
	// somebody's measured run — and run keys are sequential, so they can be
	// guessed. The refusal is the same one an ended run gets, because from the
	// outside those are the same fact: this browser is not in that run.
	if challengeRunRequested != "" && !resumed {
		// Nothing to tidy: no id has been minted and no stat spawned — the
		// refusal happens before the fresh-join path below, which is the point.
		_ = client.write(ctx, ChallengeErrorMessage{
			Type:        MessageTypeChallengeError,
			ChallengeID: challengeID,
			Reason:      "That run has ended.",
		})
		return
	}

	if !resumed {
		join.Board = s.resolveJoinBoard(inst, join.Board)
		if challengeRun != nil {
			// The start board is the catalogue's, not the client's: a challenge
			// that could be entered on a board of the player's choosing is a
			// challenge whose route is the player's choosing too.
			join.Board = challengeRun.Def.Board
		}

		// Mint a process-unique id before taking inst.mu; the two locks are never
		// held together (M14.1).
		playerID = s.mintPlayerID()

		// The join runs under inst.mu; a defer guarantees the unlock even if a
		// callee panics (e.g. spawning onto a corrupt board). Without it a panic
		// would leak inst.mu, and every later handleWorlds / GetOrCreateInstance
		// that waits on that lock (while holding the global server lock) wedges.
		ok := func() bool {
			inst.mu.Lock()
			defer inst.mu.Unlock()
			inst.RoomManager.JoinPlayerWithID(playerID, join.Board, 0, 0)
			if authenticated {
				inst.RoomManager.SetPlayerIdentity(playerID, account.ID, account.DisplayName())
				if hasStoredState {
					inst.RoomManager.ApplyPlayerState(playerID, storedState)
				}
			} else {
				inst.RoomManager.SetPlayerName(playerID, join.Name)
			}
			// Set before the snapshot is built, so the joining client's own
			// roster already carries it (M19.1).
			inst.RoomManager.SetPlayerColor(playerID, joinColor)
			if authenticated {
				inst.RoomManager.SetPlayerProfileSummary(playerID, profileHandle, hasProfile)
			}
			client.playerID = playerID
			inst.Clients[playerID] = client
			token := inst.mintResumeTokenLocked(playerID)
			var snapOK bool
			snapshot, snapOK = inst.RoomManager.Snapshot(playerID)
			if snapOK {
				snapshot.World = inst.publicWorldName()
				snapshot.ResumeToken = token
				client.boardID = snapshot.BoardID
			}
			return snapOK
		}()

		if !ok {
			s.removeClientFromInstance(inst, playerID)
			if challengeRun != nil {
				// A run whose player never spawned measures nothing; take its
				// instance and its recording down rather than leaving an empty
				// attempt on the server.
				s.discardChallengeRun(challengeRun.Key)
			}
			return
		}
	}

	if challengeRun != nil {
		// The run's one player. It is set on both the fresh join and the resume,
		// because a reconnect inside the grace reclaims the SAME PlayerID and the
		// completion check reads it every tick.
		inst.mu.Lock()
		challengeRun.PlayerID = playerID
		inst.mu.Unlock()
		snapshot.Challenge = challengeRun.Def.ID
		snapshot.ChallengeRun = challengeRun.Key
	}

	// M21.1: a signed-in player's stored blocks go live before the chat backlog
	// below is replayed. The other order would make the first act of a durable
	// block handing back the fifty lines it exists to suppress.
	if authenticated {
		s.seedAccountBlocks(playerID, account.ID)
	}

	// M21.4: and the very first frame says which of the people already in the
	// room this player has blocked. It has to run AFTER the seeding above, which
	// is what puts a returning player's stored blocks into their live set — the
	// other order would compute the answer from an empty one and be right only
	// for a player who has never blocked anybody.
	snapshot.BlockedPlayers = s.blockedInRoster(playerID, inst, snapshot.Players)
	snapshot.FollowedPlayers = s.followedInRoster(playerID, inst, snapshot.Players)

	// M21.2: and whether this connection may moderate, which is what makes the
	// same window offer mute, kick and refuse. It is told once, at the join,
	// because the allowlist is deployment configuration and cannot change under a
	// running player.
	snapshot.Operator = s.isOperator(client.accountID)

	if err := client.write(ctx, snapshot); err != nil {
		// The connection never got its first frame; detach (or tidy up if the
		// player is already gone) exactly as a mid-game drop would.
		s.handleReadLoopExit(inst, client, playerID)
		return
	}
	// M32.1: a challenge run is not a play of the source world. Counting it would
	// put the popularity shelf — and the front page built on it — under the
	// control of whoever cares to restart a challenge.
	if !resumed && challengeRun == nil && s.Activity != nil {
		if err := s.Activity.IncrementPlay(safeWorld); err != nil {
			log.Printf("zztgo: failed to record play for %s: %v", safeWorld, err)
		}
	}

	if s.ChatDB != nil {
		recs, dbErr := s.ChatDB.GetRecentMessages(50)
		if dbErr == nil {
			for _, rec := range recs {
				// A block applies to the backlog too, or reconnecting would hand
				// the blocked lines straight back (M21.1). The record's PlayerID
				// is only trustworthy in the process that wrote it — the loader
				// clears the ones read from disk — so across a restart this is
				// the AccountID's job alone.
				if s.chatBlocks.suppresses(playerID, rec.PlayerID, rec.AccountID) {
					continue
				}
				msg := struct {
					Type     string   `json:"type"`
					From     string   `json:"from"`
					PlayerID PlayerID `json:"playerId,omitempty"`
					Text     string   `json:"text"`
					History  bool     `json:"history"`
				}{
					Type:     "chat",
					From:     rec.From,
					PlayerID: rec.PlayerID,
					Text:     rec.Text,
					History:  true,
				}
				_ = client.write(ctx, msg)
			}
		}
	}

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			break
		}
		activeInst := s.instanceForClient(client)
		if activeInst == nil {
			break
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case MessageTypeDebugCommand:
			var cmd DebugCommandMessage
			if err := json.Unmarshal(raw, &cmd); err != nil {
				continue
			}
			s.submitDebugCommandInInstance(activeInst, playerID, cmd.Text)
		case MessageTypeScrollReply:
			var reply ScrollReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitScrollReplyInInstance(activeInst, playerID, reply.StatID, reply.Label)
		case MessageTypeQuitReply:
			var reply QuitReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitQuitReplyInInstance(activeInst, playerID, reply.Quit)
		case MessageTypeHighScoreName:
			var entry HighScoreNameMessage
			if err := json.Unmarshal(raw, &entry); err != nil {
				continue
			}
			s.submitHighScoreNameInInstance(ctx, activeInst, playerID, entry.Name)
		case MessageTypeSaveFilename:
			var save SaveFilenameMessage
			if err := json.Unmarshal(raw, &save); err != nil {
				continue
			}
			s.submitSaveFilenameInInstance(ctx, activeInst, playerID, save.Name)
		case "chat":
			var chat struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &chat); err != nil {
				continue
			}
			text, ok := s.admitPlayerChatLikeText(ctx, client, playerID, chat.Text, false)
			if !ok {
				continue
			}
			activeInst.mu.Lock()
			name := "browser"
			player := activeInst.RoomManager.players[playerID]
			if player != nil && player.name != "" {
				name = player.name
			}
			activeInst.mu.Unlock()
			// M21.1: the line now travels with something addressable. The
			// PlayerID is this connection's, not anything the client claimed,
			// and the accountID stays server-side — it is what makes a
			// signed-in recipient's block outlive the connection, and it is
			// not another player's to see.
			author := ChatAuthor{Name: name, PlayerID: playerID, AccountID: client.accountID}
			if s.ChatDB != nil {
				_, _ = s.ChatDB.AddMessage(author, text)
			}
			s.BroadcastGlobalChat(ctx, author, text)
		case MessageTypePrivateMessage:
			var pm PrivateMessage
			if err := json.Unmarshal(raw, &pm); err != nil {
				continue
			}
			s.submitPrivateMessage(ctx, client, playerID, pm)
		case MessageTypeFollow:
			var req FollowMessage
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			s.submitFollow(ctx, client, playerID, req)
		case MessageTypeBlock:
			var req BlockMessage
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			s.submitChatBlock(ctx, client, playerID, req)
		case MessageTypeModerate:
			var req ModerateMessage
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			s.submitModeration(ctx, client, playerID, req)
		case MessageTypeProfileRequest:
			var req ProfileRequestMessage
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			s.submitProfileRequest(ctx, client, req)
		default:
			var input InputMessage
			if err := json.Unmarshal(raw, &input); err != nil {
				continue
			}
			activeInst.setInput(playerID, inputMessageToPlayerInput(input))
		}
	}
	if activeInst := s.instanceForClient(client); activeInst != nil {
		s.handleReadLoopExit(activeInst, client, playerID)
	}
}

func (s *WebSocketServer) serveReplayHTTP(w http.ResponseWriter, r *http.Request, replayID string) {
	id, err := sanitizeReplayID(replayID)
	if err != nil {
		http.Error(w, "invalid replay id", http.StatusBadRequest)
		return
	}
	startTick, err := queryInt(r.URL.Query(), "start", 0)
	if err != nil || startTick < 0 {
		http.Error(w, "invalid replay start", http.StatusBadRequest)
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
	if err := json.Unmarshal(raw, &join); err != nil || join.Type != MessageTypeJoin || !join.Spectate {
		return
	}

	client := newWebSocketClient(conn, "replay:"+id)
	defer client.stop()
	account, authenticated := s.authAccount(r)
	if notice, refused := s.refusedAtTheDoor(account, authenticated); refused {
		_ = client.write(ctx, notice)
		return
	}

	replay, err := s.GetOrCreateReplayInstanceAt(id, startTick)
	if err != nil {
		_ = client.write(ctx, ReplayErrorMessage{Type: MessageTypeReplayError, Text: "Replay unavailable: " + err.Error()})
		return
	}
	s.serveReplaySpectator(ctx, conn, client, replay, join)
}

// resolveJoinBoard is the board a join lands on when it names none: the world's
// own current board, then the server's configured default, then board 1. It is
// extracted verbatim from the player join (M22.1) so a watcher opens the board a
// player pressing Play would have opened — the two answers drifting apart is
// exactly how /watch/<world> would end up showing a room nobody is in.
func (s *WebSocketServer) resolveJoinBoard(inst *WorldInstance, board int16) int16 {
	if board != 0 {
		return board
	}
	board = inst.RoomManager.FrozenWorld().Info.CurrentBoard
	if board == 0 {
		board = s.DefaultBoard
	}
	if board == 0 {
		board = 1
	}
	return board
}

// resolveWatchBoard is resolveJoinBoard plus a range check, and the extra check
// is not tidiness: a watcher's board is rendered by opening it directly
// (SpectatorSnapshot), so a board id off the end of the world would index the
// board table out of bounds. The player path reaches the same table through
// BoardOpen's own clamp and is left exactly as it was.
func (s *WebSocketServer) resolveWatchBoard(inst *WorldInstance, board int16) int16 {
	count := inst.RoomManager.FrozenWorld().BoardCount
	resolved := s.resolveJoinBoard(inst, board)
	if resolved < 0 || resolved > count {
		// Not the title board: an id off the end is a link that named a board this
		// world does not have, and the honest answer is the board a player would
		// have been put on, not board 0.
		resolved = s.resolveJoinBoard(inst, 0)
	}
	if resolved < 0 || resolved > count {
		// A world whose own current board is out of range. Board 0 is the one
		// every world has, so it is where a nonsense world still renders.
		resolved = 0
	}
	return resolved
}

// serveSpectator runs a read-only connection: it watches one board of one world
// and can touch nothing (M22.1).
//
// Nothing here reaches RoomManager except to READ a board frame. No PlayerID is
// minted, no stat is spawned, no resume token exists, and the connection is
// registered in inst.Spectators rather than inst.Clients — which is what keeps a
// watcher out of the roster, out of the picker's occupancy, out of the chat
// fan-out and out of autosave, by construction rather than by remembering to
// exclude them at each of those places.
func (s *WebSocketServer) serveSpectator(ctx context.Context, conn *websocket.Conn, client *webSocketClient, inst *WorldInstance, join JoinMessage) {
	boardID := s.resolveWatchBoard(inst, join.Board)

	inst.mu.Lock()
	sub := &spectator{client: client, boardID: boardID}
	inst.Spectators[client] = sub
	snapshot, engine := inst.RoomManager.SpectatorSnapshot(boardID)
	sub.engine = engine
	snapshot.Watchers = inst.watcherCountLocked(boardID)
	sub.sentWatchers = snapshot.Watchers
	inst.mu.Unlock()

	defer func() {
		inst.mu.Lock()
		delete(inst.Spectators, client)
		inst.mu.Unlock()
	}()

	if err := client.write(ctx, snapshot); err != nil {
		return
	}

	// The read loop exists to notice the socket closing, and to DISCARD. A
	// watcher who could nudge the room is a cheat client, so nothing sent here is
	// decoded, dispatched or rate-limited: it is read off the wire (the read
	// limit still applies) and counted, and the count is how a test tells "the
	// input arrived and was dropped" from "the input never arrived".
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return
		}
		inst.mu.Lock()
		inst.spectatorDrops++
		inst.mu.Unlock()
	}
}

// watcherCountLocked is watcherCountsLocked narrowed to one board. Caller holds
// inst.mu.
func (inst *WorldInstance) watcherCountLocked(boardID int16) int {
	count := 0
	for _, sub := range inst.Spectators {
		if sub.boardID == boardID {
			count++
		}
	}
	return count
}

// SpectatorCount reports how many read-only watchers a hosted world has, and
// SpectatorDrops how many messages they have had discarded. Both are for tests
// and operational reporting; nothing in the simulation reads either.
func (inst *WorldInstance) SpectatorCount() int {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return len(inst.Spectators)
}

func (inst *WorldInstance) SpectatorDrops() int {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.spectatorDrops
}

func (s *WebSocketServer) authAccount(r *http.Request) (AuthenticatedAccount, bool) {
	if s.Auth == nil {
		return AuthenticatedAccount{}, false
	}
	return s.Auth.AccountFromRequest(r)
}

func (s *WebSocketServer) loadAccountPlayerState(accountID, worldName string) (PlayerState, bool) {
	if s.ChatDB == nil || accountID == "" {
		return PlayerState{}, false
	}
	// M32.1: a challenge run starts from the world's pristine state for
	// everybody. Handing a returning account its saved inventory would make two
	// runs of the same challenge measure different games, so the sidecar is
	// neither read nor written on a run — here rather than at each call site, so
	// a later caller cannot reintroduce it by forgetting.
	if isChallengeRunKey(worldName) {
		return PlayerState{}, false
	}
	state, ok, err := s.ChatDB.GetPlayerState(accountID, worldName)
	if err != nil {
		log.Printf("zztgo: failed to load player state for account %q in %q: %v", accountID, worldName, err)
		return PlayerState{}, false
	}
	return state, ok
}

func (s *WebSocketServer) persistAccountPlayerState(accountID, worldName string, state PlayerState) {
	if s.ChatDB == nil || accountID == "" {
		return
	}
	// The write half of the rule above (M32.1): what a player picked up inside a
	// challenge run is the run's, and must not follow them back into the world
	// the challenge was measured on.
	if isChallengeRunKey(worldName) {
		return
	}
	if err := s.ChatDB.PutPlayerState(accountID, worldName, state); err != nil {
		log.Printf("zztgo: failed to persist player state for account %q in %q: %v", accountID, worldName, err)
	}
}

// resolveAccountPlayerColor decides which color a signed-in player's ☻ is drawn
// on: the account's, if that account has ever expressed one, and otherwise
// whatever this browser sent (M19.3).
//
// The stored preference wins over the join even when the join carries a color,
// which is what "account-wide" means — the same player is the same color in a
// second world and in a second browser, and a pick left behind in some other
// browser's localStorage does not follow them around. It also means an EXISTING
// document whose Color is empty wins: that document is the picker's "No color"
// row, and the way back to the vanilla player has to beat a stale local pick
// the same way a red one does (the M19.2 lesson, one layer down).
//
// The one write here is the adoption of a first pick: a player who chose a
// color before this store existed (or before they signed in) has it in
// localStorage only, and their first signed-in join moves it to the account
// rather than making them pick it again. Every later change comes through
// /api/preferences, where "no color" can be said out loud.
//
// Nothing here reaches the simulation — SetPlayerColor records no op, which is
// the M19.1 invariant this task must not spend.
func (s *WebSocketServer) resolveAccountPlayerColor(accountID, joinColor string) string {
	prefs, ok := s.loadAccountPreferences(accountID)
	if ok {
		return prefs.Color
	}
	if joinColor != "" {
		s.persistAccountPreferences(accountID, AccountPreferences{Color: joinColor})
	}
	return joinColor
}

func (s *WebSocketServer) loadAccountPreferences(accountID string) (AccountPreferences, bool) {
	if s.ChatDB == nil || accountID == "" {
		return AccountPreferences{}, false
	}
	prefs, ok, err := s.ChatDB.GetAccountPreferences(accountID)
	if err != nil {
		log.Printf("zztgo: failed to load preferences for account %q: %v", accountID, err)
		return AccountPreferences{}, false
	}
	return prefs, ok
}

func (s *WebSocketServer) persistAccountPreferences(accountID string, prefs AccountPreferences) {
	if s.ChatDB == nil || accountID == "" {
		return
	}
	if err := s.ChatDB.PutAccountPreferences(accountID, prefs); err != nil {
		log.Printf("zztgo: failed to persist preferences for account %q: %v", accountID, err)
	}
}

func (s *WebSocketServer) accountProfileSummary(accountID string) (string, bool) {
	prefs, ok := s.loadAccountPreferences(accountID)
	if !ok {
		return "", false
	}
	return prefs.Profile.Handle, accountProfileHasPublicFields(prefs.Profile)
}

func accountProfileHasPublicFields(profile AccountProfilePreferences) bool {
	return profile.Handle != "" || profile.DisplayName != "" || len(profile.About) > 0
}

func (s *WebSocketServer) refreshAccountProfile(accountID string, profile AccountProfilePreferences) {
	if accountID == "" {
		return
	}
	handle := profile.Handle
	hasProfile := accountProfileHasPublicFields(profile)
	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()
	for _, inst := range instances {
		inst.mu.Lock()
		for playerID, player := range inst.RoomManager.players {
			if player.accountID == accountID {
				inst.RoomManager.SetPlayerProfileSummary(playerID, handle, hasProfile)
			}
		}
		inst.mu.Unlock()
	}
}

// serveEditor owns an editor-only WebSocket. Unlike a game connection it never
// joins RoomManager, so an editor cannot affect ticks, players, or live state.
func (s *WebSocketServer) serveEditor(ctx context.Context, conn *websocket.Conn, enter EditorEnterMessage, account AuthenticatedAccount, authenticated bool) {
	worldName := enter.World
	if worldName == "" {
		worldName = s.DefaultInstance.Name
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		return
	}
	inst, err := s.GetOrCreateInstance(safeWorld)
	if err != nil {
		return
	}

	client := newWebSocketClient(conn, safeWorld)
	defer client.stop()
	if authenticated {
		client.accountID = account.ID
		client.name = account.DisplayName()
	}
	session := s.editorSessionForWorld(safeWorld, inst.SourceWorld)
	presence, memberToken, displaced, err := session.EnterResuming(client, client.name, enter.ResumeToken)
	if err != nil {
		return
	}
	// A reconnect that arrived before the server noticed the drop takes the
	// membership over; the socket it took it from is closed here rather than
	// left to be discovered, so the session never fans out to a connection
	// nobody is reading (M16.14f).
	//
	// CloseNow, not Close: this is the RESUMING collaborator's own entry path,
	// and a graceful close waits up to five seconds for a close frame from the
	// peer least likely to send one (M18.17). Nothing is lost by dropping it —
	// the browser's close listener takes no event, so neither the code nor the
	// reason was ever read (web/src/main.ts).
	if displaced != nil && displaced != client {
		_ = displaced.conn.CloseNow()
	}
	session.SetMemberReadOnly(client, !s.editorCanEdit(safeWorld, client.accountID))
	client.name = presence.Name
	s.mu.Lock()
	if s.EditorSessions == nil {
		s.EditorSessions = make(map[*webSocketClient]*EditorSession)
	}
	s.EditorSessions[client] = session
	s.mu.Unlock()
	defer func() {
		session.Exit(client)
		s.mu.Lock()
		delete(s.EditorSessions, client)
		s.mu.Unlock()
		s.broadcastEditorPresence(context.Background(), session)
	}()

	// The cursor belongs to the browser. These are only the initial inspection
	// coordinates sent with its full frame, never session state — the middle of
	// the board for a first entry, and for a resumed membership the cell that
	// membership was last inspecting, so a reconnect does not drag the cursor
	// back to the centre.
	snapshot, err := session.Snapshot(client, presence.X, presence.Y)
	if err != nil {
		return
	}
	// The F1/F2/F3 element menus are static, so they ride the entry snapshot
	// once rather than a request per keypress (M5.8).
	snapshot.Menus = editorElementMenus()
	// The token this connection must present if it has to come back (M16.14f).
	snapshot.ResumeToken = memberToken
	if client.write(ctx, snapshot) != nil {
		return
	}
	s.broadcastEditorPresence(ctx, session)

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}
		switch envelope.Type {
		case MessageTypeEditorExit:
			return
		case MessageTypeEditorInspect:
			var inspect EditorInspectMessage
			if json.Unmarshal(raw, &inspect) != nil {
				continue
			}
			session.UpdatePresence(client, inspect.X, inspect.Y)
			reply, err := session.Inspect(client, inspect.X, inspect.Y)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
			s.broadcastEditorPresence(ctx, session)
		case MessageTypeEditorEdit:
			var edit EditorEditMessage
			if json.Unmarshal(raw, &edit) != nil {
				continue
			}
			// The diff goes out through the session's fan-out gate, in the order
			// the session applied the edits (M16.14b). Broadcasting after the
			// session lock was released left the two orders independent, so two
			// members writing one cell could leave a third screen holding the
			// tile the session threw away. The gate holds no session lock while
			// it writes, and since M16.14e each write only queues the message
			// for that member's own writer, so a stalled browser delays nobody.
			reply, err := session.EditAndFanOut(client, edit, func(diff EditorDiffMessage) {
				if diff.Type == "" {
					return
				}
				session.UpdatePresence(client, edit.X, edit.Y)
				// Only members viewing the edited board get its cells (M17.12).
				s.broadcastEditorBoard(ctx, session, diff.BoardID, diff)
				s.broadcastEditorPresence(ctx, session)
			})
			if err != nil {
				return
			}
			if reply.Type == "" {
				continue
			}
		case MessageTypeEditorLease:
			var lease EditorLeaseMessage
			if json.Unmarshal(raw, &lease) != nil {
				continue
			}
			switch lease.Op {
			case "request":
				reply, err := session.AcquireLease(client, lease)
				if err != nil {
					return
				}
				if reply.Type != "" && client.write(ctx, reply) != nil {
					return
				}
			case "release":
				session.ReleaseLease(client, lease)
			}
		case MessageTypeEditorProperty:
			var property EditorPropertyMessage
			if json.Unmarshal(raw, &property) != nil {
				continue
			}
			reply, err := session.SetProperty(client, property)
			if err != nil {
				return
			}
			if reply.Type == "" {
				continue
			}
			// Board Information and the world name are not the acting member's
			// private business (M16.14a (a)): the members watching that board get
			// the repaint, and everyone else the world-scoped half, so a rename
			// reaches every switcher rather than one screen.
			s.broadcastEditorProperties(ctx, session, reply)
		case MessageTypeEditorStat:
			var stat EditorStatMessage
			if json.Unmarshal(raw, &stat) != nil {
				continue
			}
			reply, err := session.SetStat(client, stat)
			if err != nil {
				return
			}
			if reply.Type != "" && client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorProgram:
			var req EditorProgramRequestMessage
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			reply, err := session.ProgramText(client, req.StatID)
			if err != nil {
				return
			}
			if reply.Type != "" && client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorProgramSave:
			var save EditorProgramSaveMessage
			if json.Unmarshal(raw, &save) != nil {
				continue
			}
			reply, err := session.SaveProgram(client, save.StatID, save.Lines)
			if err != nil {
				return
			}
			if reply.Type != "" && client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorBoard:
			var board EditorBoardMessage
			if json.Unmarshal(raw, &board) != nil {
				continue
			}
			if s.serveEditorBoard(ctx, client, session, board) != nil {
				return
			}
		case MessageTypeEditorWorld:
			var world EditorWorldMessage
			if json.Unmarshal(raw, &world) != nil {
				continue
			}
			if s.serveEditorWorld(ctx, client, session, world) != nil {
				return
			}
		case MessageTypeEditorTestPlay:
			world, err := s.startEditorTestPlay(client, session)
			reply := EditorTestPlayMessage{Type: MessageTypeEditorTestPlay}
			if err != nil {
				reply.Error = err.Error()
			} else {
				reply.World = world
			}
			s.broadcastEditor(ctx, session, reply)
		}
	}
}

// EditorCounts reports how many people are editing each world (M17.11), the
// editor-side counterpart to the per-instance client counts handleWorlds
// gathers for Players. Takes the server lock to walk the session map, then each
// session's own lock via MemberCount — never reads EditorSession.Members
// directly, which is guarded by that session's mutex.
func (s *WebSocketServer) EditorCounts() map[string]int {
	s.mu.Lock()
	sessions := make(map[string]*EditorSession, len(s.EditorWorldSessions))
	for name, session := range s.EditorWorldSessions {
		sessions[name] = session
	}
	s.mu.Unlock()

	counts := make(map[string]int, len(sessions))
	for name, session := range sessions {
		if session == nil {
			continue
		}
		if n := session.MemberCount(); n > 0 {
			counts[name] = n
		}
	}
	return counts
}

func (s *WebSocketServer) editorSessionForWorld(worldName string, world TWorld) *EditorSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.EditorWorldSessions == nil {
		s.EditorWorldSessions = make(map[string]*EditorSession)
	}
	session := s.EditorWorldSessions[worldName]
	if session == nil {
		session = NewEditorSession(worldName, world)
		s.EditorWorldSessions[worldName] = session
	}
	return session
}

func (s *WebSocketServer) broadcastEditor(ctx context.Context, session *EditorSession, message interface{}) {
	for _, member := range session.MemberClients() {
		_ = member.write(ctx, message)
	}
}

// broadcastEditorBoard sends a board-shaped message only to the members viewing
// that board (M17.12). An edit diff carries the dirty cells of one board; a
// member editing another board would paint them onto the screen they are
// actually looking at, which is the corruption half of the owner's report.
// Session-wide messages (presence, test play) keep using broadcastEditor.
func (s *WebSocketServer) broadcastEditorBoard(ctx context.Context, session *EditorSession, boardID int16, message interface{}) {
	for _, member := range session.MemberClientsOnBoard(boardID) {
		_ = member.write(ctx, message)
	}
}

// broadcastEditorSnapshot fans a board-scoped repaint out to the whole session
// (M16.14a (a)). Clear board, Add board, Import board and New world used to
// reply to the acting client alone, so a collaborator watching a board somebody
// else cleared kept every tile that was no longer there.
//
// A snapshot carries the ACTING member's id, cursor, inspect and read-only flag
// alongside the shared board frame, so it only goes to the members viewing that
// board — the client takes the board half of it and leaves the cursor half
// alone (applyEditorSnapshot's forMe, M17.9). Everybody else gets the
// world-scoped half on its own: the switcher's board list and the world name,
// which change when a board is added or renamed no matter who is looking where.
func (s *WebSocketServer) broadcastEditorSnapshot(ctx context.Context, session *EditorSession, snapshot EditorSnapshotMessage) {
	s.fanOutEditorBoardChange(ctx, session, snapshot.BoardID, snapshot,
		EditorPropertiesMessage{Type: MessageTypeEditorProperties, Properties: snapshot.Properties})
}

// broadcastEditorProperties is the same fan-out for an accepted Board
// Information or world-name change (M16.14a (a)). The frame rides only the copy
// sent to the members viewing that board; the rest get the properties alone, of
// which the client takes only what is world-scoped.
func (s *WebSocketServer) broadcastEditorProperties(ctx context.Context, session *EditorSession, reply EditorPropertiesMessage) {
	elsewhere := reply
	elsewhere.Screen = nil
	s.fanOutEditorBoardChange(ctx, session, reply.Properties.BoardID, reply, elsewhere)
}

// fanOutEditorBoardChange sends onBoard to the members viewing boardID and
// elsewhere to every other member of the session.
func (s *WebSocketServer) fanOutEditorBoardChange(ctx context.Context, session *EditorSession, boardID int16, onBoard, elsewhere interface{}) {
	viewing := make(map[*webSocketClient]bool)
	for _, member := range session.MemberClientsOnBoard(boardID) {
		viewing[member] = true
		_ = member.write(ctx, onBoard)
	}
	for _, member := range session.MemberClients() {
		if !viewing[member] {
			_ = member.write(ctx, elsewhere)
		}
	}
}

func (s *WebSocketServer) broadcastEditorPresence(ctx context.Context, session *EditorSession) {
	s.broadcastEditor(ctx, session, EditorPresenceMessage{
		Type:    MessageTypeEditorPresence,
		Members: session.Presence(),
	})
}

func (s *WebSocketServer) editorCanEdit(worldName, accountID string) bool {
	dir := s.worldsDir()
	if dir == "" {
		return true
	}
	access, ok, err := loadWorldAccess(dir, worldName)
	if err != nil {
		log.Printf("zztgo: failed to load access metadata for %q: %v", worldName, err)
		return false
	}
	if !ok {
		return true
	}
	return access.CanEdit(accountID)
}

// serveEditorWorld routes an editorWorld operation (M5.6). save publishes the
// session world and hosts it; download replies with the world's .ZZT bytes;
// upload replaces the session world with client bytes after the M7.5 gate. A
// malformed base64 payload or unknown op is ignored, not fatal. It returns the
// write error only.
func (s *WebSocketServer) serveEditorWorld(ctx context.Context, client *webSocketClient, session *EditorSession, world EditorWorldMessage) error {
	switch world.Op {
	case "save":
		if !session.CanEdit(client) {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: "world is read-only for this account"})
		}
		name, err := s.saveEditorWorld(client, session, world.Name)
		reply := EditorSaveResultMessage{Type: MessageTypeEditorSaveResult}
		if err != nil {
			reply.Error = err.Error()
		} else {
			reply.World = name
		}
		return client.write(ctx, reply)
	case "download":
		data, err := session.WorldBytes(client, "")
		if err != nil || data == nil {
			return nil
		}
		name, nameErr := SanitizeSaveName(session.Name())
		if nameErr != nil {
			name = "WORLD"
		}
		return client.write(ctx, EditorWorldDataMessage{
			Type: MessageTypeEditorWorldData,
			Name: name,
			Data: base64.StdEncoding.EncodeToString(data),
		})
	case "upload":
		if !session.CanEdit(client) {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: "world is read-only for this account"})
		}
		data, decErr := base64.StdEncoding.DecodeString(world.Data)
		if decErr != nil {
			return nil
		}
		snapshot, gate, err := session.UploadWorld(client, data)
		if err != nil {
			return nil
		}
		if gate != "" {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: gate})
		}
		return client.write(ctx, snapshot)
	case "invite":
		if !session.CanEdit(client) {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: "world is read-only for this account"})
		}
		reply := EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, World: session.Name()}
		if err := s.inviteEditorCollaborator(ctx, client, session, world.AccountID); err != nil {
			reply.Error = err.Error()
		}
		return client.write(ctx, reply)
	}
	return nil
}

// serveEditorBoard routes an editorBoard operation (M5.5). add/switch/import
// reply with a full editor snapshot; export replies with the board's .BRD bytes.
// A malformed base64 payload or unknown op is ignored, not fatal, so a bad
// message never drops the editor connection. It returns the write error only.
func (s *WebSocketServer) serveEditorBoard(ctx context.Context, client *webSocketClient, session *EditorSession, board EditorBoardMessage) error {
	switch board.Op {
	case "add":
		reply, err := session.AddBoard(client, board.Name)
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		// The acting member is alone on the board they just made, so the frame
		// reaches only them; everyone else learns of the board through the
		// world-scoped half of the same fan-out, and of where its author went
		// through presence.
		s.broadcastEditorSnapshot(ctx, session, reply)
		s.broadcastEditorPresence(ctx, session)
		return nil
	case "switch":
		reply, err := session.SwitchBoard(client, board.BoardID)
		if err != nil {
			return nil
		}
		// A switch changes nothing but where this member is looking, so it stays
		// a private repaint — but every other screen filters cursors by board and
		// the legend says who is elsewhere (M17.10, M17.12), so they are told.
		if err := client.write(ctx, reply); err != nil {
			return err
		}
		s.broadcastEditorPresence(ctx, session)
		return nil
	case "export":
		reply, err := session.ExportBoard(client)
		if err != nil {
			return nil
		}
		return client.write(ctx, reply)
	case "import":
		data, decErr := base64.StdEncoding.DecodeString(board.Data)
		if decErr != nil {
			return nil
		}
		reply, err := session.ImportBoard(client, data)
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		s.broadcastEditorSnapshot(ctx, session, reply)
		return nil
	case "clear":
		reply, err := session.ClearBoard(client)
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		s.broadcastEditorSnapshot(ctx, session, reply)
		return nil
	case "new":
		reply, err := session.NewWorld(client)
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		// NewWorld has already moved every member onto the only board the new
		// world has, so this repaints all of them.
		s.broadcastEditorSnapshot(ctx, session, reply)
		s.broadcastEditorPresence(ctx, session)
		return nil
	}
	return nil
}

// quitOutcome is what a player sees after confirming the quit prompt: either
// vanilla's "New high score" window, or a bare quit so the client can return to
// the title screen. Callers hold s.mu or inst.mu.
func (s *WebSocketServer) quitOutcome(rm *RoomManager, quit QuitResult) EventMessage {
	event := ProtocolEvent{Type: "quit", Score: quit.Score}
	if quit.ListPos > 0 {
		event = ProtocolEvent{
			Type:    "highScoreEntry",
			Score:   quit.Score,
			ListPos: quit.ListPos,
			Title:   "New high score for " + rm.WorldName(),
			Lines:   rm.HighScoreLines(quit.ListPos, quit.Score),
		}
	}
	return EventMessage{Type: MessageTypeEvent, Event: event}
}

// submitQuitReplyInInstance routes a client's answer to the quit prompt. A
// confirmed quit becomes a QuitEvent on the next step, which RoomManager turns
// into a DrainQuits entry — the player is never removed from a room mid-tick.
func (s *WebSocketServer) submitQuitReplyInInstance(inst *WorldInstance, playerID PlayerID, quit bool) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if _, ok := inst.Clients[playerID]; !ok {
		return
	}
	// M32.1: quitting a challenge run is allowed and IS the abandon: vanilla's
	// Q takes the player out of the game, the run's player leaves the room with
	// it, and a run with nobody in it can never complete (advanceChallengeLocked
	// finds no player state). So an abandoned attempt simply produces no result
	// — it is not refused, and it is not scored.
	inst.RoomManager.SubmitQuitReply(playerID, quit)
}

// submitHighScoreNameInInstance writes the name a quitter typed into the slot
// their score earned, then sends the finished list back for display. A client
// that was never offered a slot gets nothing: RecordHighScore refuses it.
func (s *WebSocketServer) submitHighScoreNameInInstance(ctx context.Context, inst *WorldInstance, playerID PlayerID, name string) {
	inst.mu.Lock()
	client := inst.Clients[playerID]
	if client == nil || !inst.RoomManager.RecordHighScore(playerID, name) {
		inst.mu.Unlock()
		return
	}
	message := EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
		Type:  "highScores",
		Title: "High scores for " + inst.RoomManager.WorldName(),
		Lines: inst.RoomManager.HighScoreLines(0, 0),
	}}
	accountID := client.accountID
	worldName := inst.Name
	newsworthy := inst.Challenge == nil && !inst.Private
	inst.mu.Unlock()

	// M34.1: a score is news the moment the player puts a name on it. Filed
	// after the unlock, like every other deed.
	if newsworthy {
		s.recordGazette(GazetteKindScore, worldName, accountID)
	}

	_ = client.write(ctx, message)
}

// submitSaveFilenameInInstance answers a savePrompt event. The name is a
// client's, so it never reaches a path before SanitizeSaveName; the reply tells
// the player what their snapshot is called, or why it was refused.
//
// The snapshot deliberately does NOT go through Engine.SubmitSaveFilename: that
// writes one room engine's world — a single board, with the other rooms stale —
// to the process working directory. It is the terminal's path. The server saves
// the whole world through the RoomManager.
//
// The write happens under inst.mu, as RecordHighScore's does: a room may not
// tick while its boards are being serialized.
func (s *WebSocketServer) submitSaveFilenameInInstance(ctx context.Context, inst *WorldInstance, playerID PlayerID, name string) {
	inst.mu.Lock()
	client := inst.Clients[playerID]
	if client == nil {
		inst.mu.Unlock()
		return
	}
	if name == "" {
		inst.mu.Unlock()
		return
	}
	// M32.1: a challenge run cannot be saved. A save is a mid-run checkpoint the
	// player could restore from, which is the same hole as the cheat prompt by a
	// different door — and the run's world is not one a .SAV could be restored
	// into anyway. Refused out loud so the player is not left waiting on a reply.
	if inst.Challenge != nil {
		inst.mu.Unlock()
		_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
			Type:  "saveResult",
			Error: "a challenge run cannot be saved",
		}})
		return
	}
	accountID, _, _ := inst.RoomManager.PlayerIdentity(playerID)
	state, hasState := inst.RoomManager.PlayerState(playerID)
	var stateCopy PlayerState
	if hasState {
		stateCopy = *state
	}
	path, err := inst.RoomManager.SaveSnapshot(s.SavesDir, name, playerID)
	inst.mu.Unlock()
	if err == nil && accountID != "" && hasState {
		s.persistAccountPlayerState(accountID, inst.Name, stateCopy)
	}

	event := ProtocolEvent{Type: "saveResult"}
	if err != nil {
		event.Error = err.Error()
	} else {
		event.Filename = strings.TrimSuffix(filepath.Base(path), ".SAV")
	}
	_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: event})
}

// autosaveDir is where restore-on-boot autosaves live: a subdirectory of
// SavesDir so an autosave never collides with a player's named 'S' save. Empty
// SavesDir means saving is disabled and there is no autosave directory.
func (s *WebSocketServer) autosaveDir() string {
	if s.SavesDir == "" {
		return ""
	}
	return filepath.Join(s.SavesDir, "autosave")
}

// Autosave snapshots every occupied instance to autosaveDir()/<NAME>.SAV. It is
// the M13.3 seam: the tick loop calls it on cadence, and tests call it directly.
//
// Concurrency: each world copy is taken under that instance's lock and the file
// is written after the lock is released, so a running room never blocks on disk
// I/O (mirroring SaveSnapshot's "a save never disturbs the game it is a save
// of"). The snapshot carries no saver, so the vanilla one-player inventory
// fields are zero — the server ignores them on join (M4.3a decision 1).
func (s *WebSocketServer) Autosave() {
	dir := s.autosaveDir()
	if dir == "" {
		return
	}

	type autosaveJob struct {
		inst  *WorldInstance
		name  string
		world TWorld
	}
	var jobs []autosaveJob

	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	// The default instance is registered in s.Instances (NewWebSocketServer), so
	// iterating the map already covers it.
	for _, inst := range instances {
		// M32.1: a challenge run is never autosaved. Its instance is one
		// player's measured attempt at a world it does not own, and an autosave
		// of it would be a snapshot nobody can restore under a name no world
		// answers to. (SanitizeSaveName below would refuse the key anyway; this
		// says so on purpose rather than leaving the rule to a log line.)
		if inst.Challenge != nil {
			continue
		}
		inst.mu.Lock()
		if len(inst.Clients) == 0 {
			// Empty rooms are already frozen into rm.world with nothing new to say.
			inst.mu.Unlock()
			continue
		}
		// Instance names are already sanitized on their hosting paths, but the
		// name becomes a filename here — re-verify and skip-with-log rather than
		// guess at a safe one.
		name, err := SanitizeSaveName(inst.Name)
		if err != nil {
			inst.mu.Unlock()
			log.Printf("zztgo: skipping autosave of instance %q: %v", inst.Name, err)
			continue
		}
		inst.autosaving = true
		world := inst.RoomManager.snapshotWorldNoSaver()
		inst.mu.Unlock()
		jobs = append(jobs, autosaveJob{inst: inst, name: name, world: world})
	}

	for _, job := range jobs {
		if _, err := writeWorldSnapshot(dir, job.name, job.world); err != nil {
			log.Printf("zztgo: autosave of %q failed: %v", job.name, err)
		}
		job.inst.mu.Lock()
		job.inst.autosaving = false
		job.inst.mu.Unlock()
	}
}

// RestoreAutosaves runs at boot, before serving: for every autosave whose name
// matches a hostable world it restores that world as the instance's starting
// state. An autosave beats the pristine .ZZT — that is what crash recovery means
// (freshness policy in NOTES.md). A corrupt or truncated file is logged and
// skipped, never a boot failure. -fresh skips this entirely (caller's choice).
func (s *WebSocketServer) RestoreAutosaves() {
	dir := s.autosaveDir()
	if dir == "" {
		return
	}
	for _, name := range ListSnapshots(dir) {
		// GetOrCreateInstance returns the already-registered default instance for
		// its own name, and loads a pristine hostable world for any other. A name
		// with no hostable world is not ours to restore — skip it.
		inst, err := s.GetOrCreateInstance(name)
		if err != nil {
			log.Printf("zztgo: autosave %q has no hostable world, skipping: %v", name, err)
			continue
		}
		// The occupancy refusal in RestoreSnapshot is vacuous at boot — nobody has
		// joined yet — but harmless to go through. A corrupt or truncated file may
		// error or even panic (a garbage board length reaches a make); recover so a
		// bad autosave is skipped, never a boot failure.
		inst.mu.Lock()
		err = safeRestoreSnapshot(inst.RoomManager, dir, name)
		inst.mu.Unlock()
		if err != nil {
			log.Printf("zztgo: autosave %q could not be restored, using pristine world: %v", name, err)
			continue
		}
		log.Printf("zztgo: restored autosave %q", name)
	}
}

// safeRestoreSnapshot restores rm from dir/<NAME>.SAV, turning a panic on a
// malformed file into an ordinary error so RestoreAutosaves can skip it.
func safeRestoreSnapshot(rm *RoomManager, dir, name string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("malformed snapshot: %v", r)
		}
	}()
	return rm.RestoreSnapshot(dir, name)
}

// EnableRecording turns on session recording for every current and future
// instance, writing one JSONL file per instance under dir (M14.2). It is
// best-effort: it creates the directory and attaches recorders to the instances
// that already exist (the boot world). Instances created later attach their own
// recorder from GetOrCreateInstance / HostGeneratedWorld.
func (s *WebSocketServer) EnableRecording(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RecordDir = dir
	s.recordStamp = time.Now().UTC().Format("20060102-150405")
	for _, inst := range s.Instances {
		s.attachRecorderLocked(inst)
	}
	return nil
}

// attachRecorderLocked opens a recording for one instance if recording is on and
// the instance is not already recording. Caller holds s.mu. Failures are logged,
// never fatal: a debugging feature must not stop a world from hosting.
func (s *WebSocketServer) attachRecorderLocked(inst *WorldInstance) {
	if s.RecordDir == "" || inst.RoomManager.recorder != nil {
		return
	}
	// FrozenWorld is the manager's authoritative world; before the first client
	// or tick it is the pristine starting state playback must begin from.
	//
	// The header names a WORLD and the file is named by the instance, and for a
	// challenge run those are two different strings (M32.1): the run's key is not
	// a name any directory answers to, and /replay/<id> refuses a recording whose
	// header world it cannot load.
	headerWorld := inst.Name
	if inst.RecordWorld != "" {
		headerWorld = inst.RecordWorld
	}
	header, _, err := newSessionHeader(headerWorld, inst.RoomManager.FrozenWorld())
	if err != nil {
		log.Printf("zztgo: session recording disabled for %q: %v", inst.Name, err)
		return
	}
	if inst.Challenge != nil {
		// The recording says what it is a recording OF (M32.1). These are added
		// fields rather than a recordVersion bump: an unknown key is ignored on
		// unmarshal, so every existing recording and fixture still reads, and a
		// bump would refuse them all to record something nothing old needs.
		header.ChallengeID = inst.Challenge.Def.ID
		header.ChallengeVersion = inst.Challenge.Def.Version
	}
	recordID := inst.Name + "-" + s.recordStamp
	if inst.RecordID != "" {
		recordID = inst.RecordID
	}
	path := filepath.Join(s.RecordDir, recordID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		log.Printf("zztgo: session recording disabled for %q: %v", inst.Name, err)
		return
	}
	rec, err := NewSessionRecorder(f, header)
	if err != nil {
		_ = f.Close()
		log.Printf("zztgo: session recording disabled for %q: %v", inst.Name, err)
		return
	}
	inst.RoomManager.SetRecorder(rec)
	log.Printf("zztgo: recording session for %q to %s", inst.Name, path)
}

// CloseRecorders flushes and closes every instance's session recorder. It runs
// when Run's context is cancelled so a clean shutdown does not lose the buffered
// tail of a recording. An abrupt kill can still lose buffered lines — acceptable
// for a debugging feature.
func (s *WebSocketServer) CloseRecorders() {
	s.mu.Lock()
	var recorders []*SessionRecorder
	for _, inst := range s.Instances {
		if inst.RoomManager.recorder != nil {
			recorders = append(recorders, inst.RoomManager.recorder)
			inst.RoomManager.SetRecorder(nil)
		}
	}
	s.mu.Unlock()
	for _, rec := range recorders {
		rec.Close()
	}
}

func (s *WebSocketServer) CloseReplays() {
	s.mu.Lock()
	replays := make([]*ReplayInstance, 0, len(s.ReplayInstances))
	for _, replay := range s.ReplayInstances {
		replays = append(replays, replay)
	}
	s.ReplayInstances = make(map[string]*ReplayInstance)
	s.mu.Unlock()
	for _, replay := range replays {
		replay.Close()
	}
}

func (s *WebSocketServer) GetOrCreateInstance(worldName string) (*WorldInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Instances == nil {
		s.Instances = make(map[string]*WorldInstance)
	}

	inst := s.Instances[worldName]
	if inst != nil {
		return inst, nil
	}

	world, err := LoadPristineWorld(s.worldsDir(), worldName)
	if err != nil {
		return nil, err
	}
	dir := s.worldsDir()

	rm := NewRoomManagerForWorld(world, worldName)
	rm.HighScorePath = filepath.Join(dir, worldName+".HI")
	rm.LoadHighScores()

	inst = &WorldInstance{
		Name:           worldName,
		SourceWorld:    cloneWorld(world),
		RoomManager:    rm,
		Clients:        make(map[PlayerID]*webSocketClient),
		Inputs:         make(map[PlayerID]PlayerInput),
		Title:          NewTitleSim(world),
		Detached:       make(map[PlayerID]int),
		ResumeTokens:   make(map[string]PlayerID),
		TokensByPlayer: make(map[PlayerID]string),
		Spectators:     make(map[*webSocketClient]*spectator),
	}
	configureLobbyTransits(inst)
	s.Instances[worldName] = inst
	s.attachRecorderLocked(inst)
	return inst, nil
}

func sanitizeReplayID(id string) (string, error) {
	id = strings.TrimSuffix(filepath.Base(strings.TrimSpace(id)), ".jsonl")
	if id == "" || id == "." || id == ".." || len(id) > 128 {
		return "", ErrInvalidSaveName
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return "", ErrInvalidSaveName
		}
	}
	return id, nil
}

func replayPath(dir, id string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("replay viewing is disabled")
	}
	safe, err := sanitizeReplayID(id)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, safe+".jsonl")
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", ErrInvalidSaveName
	}
	return path, nil
}

func (s *WebSocketServer) GetOrCreateReplayInstance(id string) (*ReplayInstance, error) {
	return s.GetOrCreateReplayInstanceAt(id, 0)
}

func replayInstanceKey(id string, startTick int) string {
	if startTick <= 0 {
		return id
	}
	return fmt.Sprintf("%s@%d", id, startTick)
}

func (s *WebSocketServer) GetOrCreateReplayInstanceAt(id string, startTick int) (*ReplayInstance, error) {
	if startTick < 0 {
		return nil, fmt.Errorf("replay start tick must be >= 0")
	}
	safe, err := sanitizeReplayID(id)
	if err != nil {
		return nil, err
	}
	key := replayInstanceKey(safe, startTick)

	s.mu.Lock()
	if s.ReplayInstances == nil {
		s.ReplayInstances = make(map[string]*ReplayInstance)
	}
	if replay := s.ReplayInstances[key]; replay != nil {
		s.mu.Unlock()
		return replay, nil
	}
	s.mu.Unlock()

	path, err := replayPath(s.ReplayDir, safe)
	if err != nil {
		return nil, err
	}
	playback, f, err := newReplayPlaybackFromPath(path, startTick)
	if err != nil {
		return nil, err
	}
	if _, err := LoadPristineWorld(s.worldsDir(), playback.WorldName()); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("recorded world %q is not hosted: %w", playback.WorldName(), err)
	}

	replay := &ReplayInstance{
		ID:         safe,
		path:       path,
		startTick:  startTick,
		file:       f,
		playback:   playback,
		Spectators: make(map[*webSocketClient]*spectator),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.ReplayInstances[key]; existing != nil {
		replay.Close()
		return existing, nil
	}
	s.ReplayInstances[key] = replay
	return replay, nil
}

func (s *WebSocketServer) serveReplaySpectator(ctx context.Context, conn *websocket.Conn, client *webSocketClient, replay *ReplayInstance, join JoinMessage) {
	boardID := s.DefaultBoard
	if join.Board != 0 {
		boardID = join.Board
	}
	if boardID == 0 {
		boardID = 1
	}

	replay.mu.Lock()
	sub := &spectator{client: client, boardID: boardID}
	replay.Spectators[client] = sub
	snapshot, engine := replay.playback.RoomManager().SpectatorSnapshot(boardID)
	sub.engine = engine
	snapshot.Watchers = replay.watcherCountsLocked()[boardID]
	sub.sentWatchers = snapshot.Watchers
	replay.mu.Unlock()

	defer func() {
		replay.mu.Lock()
		delete(replay.Spectators, client)
		replay.mu.Unlock()
	}()

	if err := client.write(ctx, snapshot); err != nil {
		return
	}

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}
		if envelope.Type != MessageTypeReplayControl {
			continue
		}
		var control ReplayControlMessage
		if err := json.Unmarshal(raw, &control); err != nil {
			continue
		}
		switch control.Op {
		case "pause":
			replay.mu.Lock()
			replay.Paused = !replay.Paused
			replay.mu.Unlock()
		case "restart":
			if err := replay.Restart(); err != nil {
				_ = client.write(ctx, ReplayErrorMessage{Type: MessageTypeReplayError, Text: "Replay restart failed: " + err.Error()})
				return
			}
			replay.mu.Lock()
			snapshot, engine := replay.playback.RoomManager().SpectatorSnapshot(boardID)
			sub.engine = engine
			snapshot.Watchers = replay.watcherCountsLocked()[boardID]
			sub.sentWatchers = snapshot.Watchers
			replay.mu.Unlock()
			if err := client.write(ctx, snapshot); err != nil {
				return
			}
		}
	}
}

// HostGeneratedWorld installs an already-compiled, persisted world directly
// into the instance table. Generation uses this instead of reloading its file:
// the hosted bytes are exactly the ones that passed the compiler and M7.5 gate.
// clockNow is the non-simulation clock (M16.16a): the injected Now seam when a
// test set one, the wall clock otherwise. Simulation code must never call it.
func (s *WebSocketServer) clockNow() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *WebSocketServer) HostGeneratedWorld(name string, world TWorld) error {
	return s.hostGeneratedWorld(name, world, false)
}

// hostGeneratedWorld carries the private mark into the instance's construction
// rather than stamping it afterwards, so there is no window in which a
// test-play copy exists un-marked and could tick a death into the Gazette.
func (s *WebSocketServer) hostGeneratedWorld(name string, world TWorld, private bool) error {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Instances == nil {
		s.Instances = make(map[string]*WorldInstance)
	}
	if s.worldIsOccupiedLocked(safe) {
		return fmt.Errorf("generated world %q is already occupied", safe)
	}
	rm := NewRoomManagerForWorld(world, safe)
	inst := &WorldInstance{
		Name:           safe,
		SourceWorld:    cloneWorld(world),
		RoomManager:    rm,
		Clients:        make(map[PlayerID]*webSocketClient),
		Inputs:         make(map[PlayerID]PlayerInput),
		Title:          NewTitleSim(world),
		Detached:       make(map[PlayerID]int),
		ResumeTokens:   make(map[string]PlayerID),
		TokensByPlayer: make(map[PlayerID]string),
		Spectators:     make(map[*webSocketClient]*spectator),
		Private:        private,
	}
	s.Instances[safe] = inst
	s.attachRecorderLocked(inst)
	return nil
}

// WorldIsOccupied reports whether a hosted world of this name currently has
// players in it. It is the question every overwrite path has to ask before it
// writes: the editor's publish asks it inline, and generation asks it through
// this method before the first byte of a dream reaches the disk (M16.17b).
func (s *WebSocketServer) WorldIsOccupied(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.worldIsOccupiedLocked(name)
}

// worldIsOccupiedLocked is WorldIsOccupied with s.mu already held.
func (s *WebSocketServer) worldIsOccupiedLocked(name string) bool {
	existing := s.Instances[name]
	if existing == nil {
		return false
	}
	existing.mu.Lock()
	defer existing.mu.Unlock()
	return len(existing.Clients) != 0
}

// worldsDir resolves where published worlds live. An explicit WorldsDir wins;
// otherwise it matches the picker's historical behavior — the loaded world's
// directory, then the working directory.
func (s *WebSocketServer) worldsDir() string {
	if s.WorldsDir != "" {
		return s.WorldsDir
	}
	if E != nil && E.LoadedGameFileName != "" {
		return filepath.Dir(E.LoadedGameFileName)
	}
	return "."
}

// saveEditorWorld publishes an editor session's world as dir/<NAME>.ZZT and hosts
// it so the world picker lists it and a second client can join and play it (M5.6).
// The name comes from a client, so SanitizeSaveName is the whole defense: path
// separators, '.', '..' and absolute paths all fail its charset. A world of the
// same name that anyone is currently playing is never overwritten — the same
// occupancy refusal RestoreSnapshot uses.
func (s *WebSocketServer) saveEditorWorld(client *webSocketClient, session *EditorSession, name string) (string, error) {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return "", err
	}

	// Refuse before writing anything if the target world is occupied.
	s.mu.Lock()
	existing := s.Instances[safe]
	occupied := false
	if existing != nil {
		existing.mu.Lock()
		occupied = len(existing.Clients) != 0
		existing.mu.Unlock()
	}
	s.mu.Unlock()
	if occupied {
		return "", fmt.Errorf("world %q is being played and cannot be overwritten", safe)
	}

	dir := s.worldsDir()
	if dir == "" {
		return "", ErrSavesDisabled
	}
	// M18.11: the canonical Museum worlds are the main world and player-authored
	// content never overwrites one. They carry no .access.json — to the check
	// below they are unowned and open, which is the population M16.17b meant to
	// keep open — so the carve-out has to be asked first and separately.
	if WorldIsCanonical(safe) {
		return "", fmt.Errorf("world %q is a Museum of ZZT classic and cannot be replaced", safe)
	}
	access, hasAccess, err := loadWorldAccess(dir, safe)
	if err != nil {
		return "", err
	}
	if hasAccess && !access.CanEdit(client.accountID) {
		return "", fmt.Errorf("world %q is read-only for this account", safe)
	}
	if !hasAccess && client.accountID != "" {
		access = WorldAccess{
			OwnerAccountID: client.accountID,
			OwnerName:      client.name,
		}
		hasAccess = true
	}

	// M14.4: read the authored title before WorldBytes writes the save stem over
	// it. The dialog lets an author call a world "The Salt Cellar"; publishing it
	// as SALTCELL used to throw that away, because the stem was the only name a
	// world had.
	title := session.WorldTitle()

	data, err := session.WorldBytes(client, safe)
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", fmt.Errorf("could not serialize the editor world")
	}

	path := filepath.Join(dir, safe+".ZZT")
	// Belt and braces: SanitizeSaveName cannot emit a separator, so this can only
	// fire if that charset is ever loosened.
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", ErrInvalidSaveName
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	if hasAccess {
		if err := writeWorldAccess(dir, safe, access); err != nil {
			return "", err
		}
	}
	// The title only earns a sidecar when it says something the stem does not —
	// and a republish that takes the title back off removes the stale one, so
	// the picker never shows a name the author has stopped using.
	if title != "" && title != safe {
		if err := writeWorldMeta(dir, safe, WorldMeta{Title: title, Author: client.name}); err != nil {
			return "", err
		}
	} else if err := os.Remove(worldMetaPath(dir, safe)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	world, err := LoadWorldBytes(data)
	if err != nil {
		return "", err
	}
	if err := s.HostGeneratedWorld(safe, world); err != nil {
		return "", err
	}
	session.SetWorldName(safe)
	return safe, nil
}

func (s *WebSocketServer) inviteEditorCollaborator(ctx context.Context, client *webSocketClient, session *EditorSession, accountID string) error {
	if client.accountID == "" {
		return fmt.Errorf("authentication required")
	}
	if strings.TrimSpace(accountID) == "" {
		return fmt.Errorf("collaborator account required")
	}
	dir := s.worldsDir()
	if dir == "" {
		return ErrSavesDisabled
	}
	worldName, err := SanitizeSaveName(session.Name())
	if err != nil {
		return err
	}
	access, ok, err := loadWorldAccess(dir, worldName)
	if err != nil {
		return err
	}
	if !ok || !access.IsOwner(client.accountID) {
		return fmt.Errorf("only the world owner can invite collaborators")
	}
	access.AddCollaborator(strings.TrimSpace(accountID))
	if err := writeWorldAccess(dir, worldName, access); err != nil {
		return err
	}
	// M16.14a (b): tell the invitee, if they are sitting in the session. Their
	// browser's editorReadOnly comes from a snapshot and from nowhere else, and
	// every editor key consults it before it sends anything, so an invite the
	// client never hears about leaves the new collaborator refused by their own
	// browser — with a "Read-only" window — until they leave and come back. The
	// snapshot is addressed to them and carries the cursor they last reported,
	// so being told does not move it.
	for _, member := range session.SetAccountReadOnly(strings.TrimSpace(accountID), false) {
		x, y := session.MemberCursor(member)
		snapshot, err := session.Snapshot(member, x, y)
		if err != nil {
			continue
		}
		_ = member.write(ctx, snapshot)
	}
	return nil
}

func (s *WebSocketServer) startEditorTestPlay(client *webSocketClient, session *EditorSession) (string, error) {
	data, err := session.WorldBytes(client, "")
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", fmt.Errorf("could not serialize the editor world")
	}
	world, err := LoadWorldBytes(data)
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		name, err := randomTestPlayWorldName()
		if err != nil {
			return "", err
		}
		// Private: a test-play copy is nobody's business but the session's (M34.1).
		if err := s.hostGeneratedWorld(name, world, true); err != nil {
			if strings.Contains(err.Error(), "occupied") {
				continue
			}
			return "", err
		}
		return name, nil
	}
	return "", fmt.Errorf("could not allocate test play world")
}

func randomTestPlayWorldName() (string, error) {
	var nonce [3]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return "TP" + strings.ToUpper(hex.EncodeToString(nonce[:])), nil
}

// LoadWorldBytes parses vanilla .ZZT bytes into a TWorld without touching
// disk. data is untrusted (an uploaded/generated/museum world reaching a live
// server goroutine outside any per-tick recover), so every board is validated
// here — see validateWorldBoards — rather than left to panic whenever a room
// first opens one.
func LoadWorldBytes(data []byte) (TWorld, error) {
	scratch := newSnapshotEngine()
	if err := scratch.worldReadFrom(bytes.NewReader(data), false, nil); err != nil {
		return TWorld{}, err
	}
	if err := validateWorldBoards(scratch.World); err != nil {
		return TWorld{}, err
	}
	return scratch.World, nil
}

func LoadPristineWorld(dir, name string) (TWorld, error) {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return TWorld{}, err
	}
	path := filepath.Join(dir, safe+".ZZT")
	f, err := os.Open(path)
	if err != nil {
		return TWorld{}, err
	}
	defer f.Close()

	scratch := newSnapshotEngine()
	if err := scratch.worldReadFrom(f, false, nil); err != nil {
		return TWorld{}, err
	}
	if err := validateWorldBoards(scratch.World); err != nil {
		return TWorld{}, err
	}
	return scratch.World, nil
}

func (s *WebSocketServer) submitDebugCommandInInstance(inst *WorldInstance, playerID PlayerID, text string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if _, ok := inst.Clients[playerID]; !ok {
		return
	}
	// M32.1: vanilla's `?` cheat prompt is refused inside a challenge run. It is
	// a server-created stimulus that replays faithfully, so a cheated run would
	// not merely score — it would VERIFY, and replay verification would confirm
	// a time nobody played. The one place to stop that is before it is applied.
	if inst.Challenge != nil {
		return
	}
	inst.RoomManager.SubmitDebugCommand(playerID, text)
}

func (s *WebSocketServer) submitScrollReplyInInstance(inst *WorldInstance, playerID PlayerID, objectStatID int16, label string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if _, ok := inst.Clients[playerID]; !ok {
		return
	}
	inst.RoomManager.SubmitScrollReply(playerID, objectStatID, label)
}

func inputMessageToPlayerInput(input InputMessage) PlayerInput {
	playerInput := PlayerInput{
		DeltaX: input.DeltaX,
		DeltaY: input.DeltaY,
		Shift:  input.Shift,
		Key:    input.Key,
	}
	if input.Keymask == 0 {
		return playerInput
	}

	playerInput = PlayerInput{}
	playerInput.Shift = input.Keymask&(InputMaskShift|InputMaskShoot) != 0
	switch {
	case input.Keymask&InputMaskUp != 0:
		playerInput.DeltaY = -1
		playerInput.Key = KEY_UP
	case input.Keymask&InputMaskDown != 0:
		playerInput.DeltaY = 1
		playerInput.Key = KEY_DOWN
	case input.Keymask&InputMaskLeft != 0:
		playerInput.DeltaX = -1
		playerInput.Key = KEY_LEFT
	case input.Keymask&InputMaskRight != 0:
		playerInput.DeltaX = 1
		playerInput.Key = KEY_RIGHT
	case input.Keymask&InputMaskShoot != 0:
		playerInput.Key = ' '
	}
	return playerInput
}

func (inst *WorldInstance) setInput(playerID PlayerID, input PlayerInput) {
	inst.mu.Lock()
	if _, ok := inst.Clients[playerID]; ok {
		inst.Inputs[playerID] = input
	}
	inst.mu.Unlock()
}

func (s *WebSocketServer) completeWorldTransit(ctx context.Context, source *WorldInstance, transit WorldTransit) {
	// M32.1: a challenge run does not lead anywhere. Walking a transit gate out
	// of a measured attempt would carry the run's inventory into a live world
	// and leave the attempt running with nobody in it, so the gate is refused
	// and the player stays in the run they started.
	if source != nil && source.Challenge != nil {
		s.refuseWorldTransit(ctx, source, transit.PlayerID, transit.DestinationWorld,
			fmt.Errorf("a challenge run stays in its own world"))
		return
	}
	destination, err := SanitizeSaveName(transit.DestinationWorld)
	if err != nil {
		s.refuseWorldTransit(ctx, source, transit.PlayerID, transit.DestinationWorld, err)
		return
	}
	dest, err := s.GetOrCreateInstance(destination)
	if err != nil {
		s.refuseWorldTransit(ctx, source, transit.PlayerID, destination, err)
		return
	}

	var client *webSocketClient
	var accountID, name, color, handle string
	var hasProfile bool
	var sourceState PlayerState
	var hasSourceState bool

	source.mu.Lock()
	client = source.Clients[transit.PlayerID]
	if client == nil || source.RoomManager.players[transit.PlayerID] == nil {
		source.mu.Unlock()
		return
	}
	accountID, name, _ = source.RoomManager.PlayerIdentity(transit.PlayerID)
	if state, ok := source.RoomManager.PlayerState(transit.PlayerID); ok {
		sourceState = *state
		hasSourceState = true
	}
	if player := source.RoomManager.players[transit.PlayerID]; player != nil {
		color = player.color
		handle = player.handle
		hasProfile = player.hasProfile
	}
	delete(source.Clients, transit.PlayerID)
	delete(source.Inputs, transit.PlayerID)
	source.deleteResumeTokenLocked(transit.PlayerID)
	delete(source.Detached, transit.PlayerID)
	source.RoomManager.LeavePlayer(transit.PlayerID)
	source.RoomManager.DiscardPendingScore(transit.PlayerID)
	source.mu.Unlock()

	if accountID != "" && hasSourceState {
		s.persistAccountPlayerState(accountID, source.Name, sourceState)
	}

	var storedState PlayerState
	hasStoredState := false
	if accountID != "" {
		storedState, hasStoredState = s.loadAccountPlayerState(accountID, destination)
		handle, hasProfile = s.accountProfileSummary(accountID)
	}

	dest.mu.Lock()
	board := s.resolveJoinBoard(dest, 0)
	dest.RoomManager.JoinPlayerWithID(transit.PlayerID, board, 0, 0)
	if accountID != "" {
		dest.RoomManager.SetPlayerIdentity(transit.PlayerID, accountID, name)
		if hasStoredState {
			dest.RoomManager.ApplyPlayerState(transit.PlayerID, storedState)
		}
	} else {
		dest.RoomManager.SetPlayerName(transit.PlayerID, name)
	}
	dest.RoomManager.SetPlayerColor(transit.PlayerID, color)
	if accountID != "" {
		dest.RoomManager.SetPlayerProfileSummary(transit.PlayerID, handle, hasProfile)
	}
	client.playerID = transit.PlayerID
	client.setWorldName(destination)
	dest.Clients[transit.PlayerID] = client
	token := dest.mintResumeTokenLocked(transit.PlayerID)
	snapshot, snapOK := dest.RoomManager.Snapshot(transit.PlayerID)
	if snapOK {
		snapshot.World = dest.Name
		snapshot.ResumeToken = token
		client.boardID = snapshot.BoardID
	}
	dest.mu.Unlock()

	if !snapOK {
		s.removeClientFromInstance(dest, transit.PlayerID)
		return
	}

	snapshot.BlockedPlayers = s.blockedInRoster(transit.PlayerID, dest, snapshot.Players)
	snapshot.FollowedPlayers = s.followedInRoster(transit.PlayerID, dest, snapshot.Players)
	snapshot.Operator = s.isOperator(accountID)
	if s.Activity != nil {
		if err := s.Activity.IncrementPlay(destination); err != nil {
			log.Printf("zztgo: failed to record play for %s: %v", destination, err)
		}
	}
	if err := client.write(ctx, snapshot); err != nil {
		s.handleReadLoopExit(dest, client, transit.PlayerID)
	}
}

func (s *WebSocketServer) refuseWorldTransit(ctx context.Context, source *WorldInstance, playerID PlayerID, destination string, err error) {
	source.mu.Lock()
	client := source.Clients[playerID]
	source.mu.Unlock()
	if client == nil {
		return
	}
	lines := []string{
		"",
		"  That gate is closed.",
		"  " + destination + " is not joinable right now.",
	}
	if err != nil {
		lines = append(lines, "  "+err.Error())
	}
	_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
		Type:         "scroll",
		PlayerStatID: -1,
		Title:        "Transit",
		Lines:        lines,
	}})
}

func (c *webSocketClient) currentWorldName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.worldName
}

func (c *webSocketClient) setWorldName(worldName string) {
	c.mu.Lock()
	c.worldName = worldName
	c.mu.Unlock()
}

func (s *WebSocketServer) instanceForClient(client *webSocketClient) *WorldInstance {
	if client == nil {
		return nil
	}
	worldName := client.currentWorldName()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Instances[worldName]
}

// mintPlayerID returns the next process-unique PlayerID. It locks only s.mu and
// never inst.mu, so it is safe to call before taking an instance lock.
func (s *WebSocketServer) mintPlayerID() PlayerID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextPlayerID++
	return s.nextPlayerID
}

// mintResumeTokenLocked returns a resume token for playerID, reusing the
// existing one if the player already has it (an idempotent re-join keeps the
// same token). Caller holds inst.mu.
func (inst *WorldInstance) mintResumeTokenLocked(playerID PlayerID) string {
	if inst.ResumeTokens == nil {
		inst.ResumeTokens = make(map[string]PlayerID)
	}
	if inst.TokensByPlayer == nil {
		inst.TokensByPlayer = make(map[PlayerID]string)
	}
	if token, ok := inst.TokensByPlayer[playerID]; ok {
		return token
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand should never fail on a supported platform. If it somehow
		// does, a zero token is still a valid (if predictable) session key —
		// resume is best-effort and never a security boundary.
		log.Printf("zztgo: resume token entropy unavailable: %v", err)
	}
	token := hex.EncodeToString(buf[:])
	inst.ResumeTokens[token] = playerID
	inst.TokensByPlayer[playerID] = token
	return token
}

// deleteResumeTokenLocked drops a player's resume token from both indexes.
// Caller holds inst.mu.
func (inst *WorldInstance) deleteResumeTokenLocked(playerID PlayerID) {
	if token, ok := inst.TokensByPlayer[playerID]; ok {
		delete(inst.ResumeTokens, token)
		delete(inst.TokensByPlayer, playerID)
	}
}

// tryResume reattaches client to the player named by token, if any. On success
// it returns the reclaimed PlayerID and a fresh full snapshot (with the same
// token echoed back). An unknown or stale token returns ok=false, and the caller
// falls through to a normal fresh join.
//
// Newest-wins: if the token's player still has a live client, that socket is
// displaced — the new client takes its place and the old connection is closed.
func (s *WebSocketServer) tryResume(inst *WorldInstance, client *webSocketClient, token string) (PlayerID, SnapshotMessage, bool) {
	inst.mu.Lock()
	playerID, ok := inst.ResumeTokens[token]
	if !ok {
		inst.mu.Unlock()
		return 0, SnapshotMessage{}, false
	}
	// The token outlived its player (quit/expired before the client came back):
	// clean it up and let the caller join fresh.
	if inst.RoomManager.players[playerID] == nil {
		inst.deleteResumeTokenLocked(playerID)
		delete(inst.Detached, playerID)
		inst.mu.Unlock()
		return 0, SnapshotMessage{}, false
	}

	old := inst.Clients[playerID]
	delete(inst.Detached, playerID)
	client.playerID = playerID
	inst.Clients[playerID] = client
	// A resuming player's queued per-player events are stale; the fresh snapshot
	// below carries the current world state instead.
	inst.RoomManager.DrainPlayerEvents(playerID)
	snapshot, snapOK := inst.RoomManager.Snapshot(playerID)
	if snapOK {
		snapshot.World = inst.publicWorldName()
		snapshot.ResumeToken = token
		client.boardID = snapshot.BoardID
	}
	inst.mu.Unlock()

	// CloseNow, not Close: the displaced socket is closed on the RESUMING
	// player's own join, before their snapshot is written, and a graceful close
	// waits up to five seconds for a close frame the displaced peer — a browser
	// that has lost the network, or a tab nobody is reading — is the least
	// likely of any to answer with (M18.17). The client's close listener takes
	// no event, so the code and the reason were never read.
	if old != nil && old != client {
		_ = old.conn.CloseNow()
	}
	if !snapOK {
		// The player existed a moment ago, so this should not happen; treat it as
		// a failed resume and let the caller join fresh.
		return 0, SnapshotMessage{}, false
	}
	return playerID, snapshot, true
}

// handleReadLoopExit runs when a game connection's read loop ends. If the player
// still exists it is detached (its stat lingers for ReconnectGraceTicks so a
// reconnect can reclaim it) rather than removed. A connection that has already
// been superseded by a newer one (newest-wins) owns nothing and just returns.
func (s *WebSocketServer) handleReadLoopExit(inst *WorldInstance, client *webSocketClient, playerID PlayerID) {
	s.chatLimiter.forget(playerID)
	// A guest's blocks were only ever promised for the session, and a signed-in
	// player's are already written down: either way this id is never minted
	// again, so keeping its set would be a leak rather than a memory (M21.1).
	s.chatBlocks.forget(playerID)
	// The connection half of a mute goes with the connection (M21.2); the account
	// half stays, so a muted signed-in player is still muted when they come back.
	s.mutes.forget(playerID)
	inst.mu.Lock()
	if inst.Clients[playerID] != client {
		// A newer connection took over this player; this stale socket must not
		// detach it or it would cancel the live attachment.
		inst.mu.Unlock()
		return
	}
	delete(inst.Clients, playerID)
	delete(inst.Inputs, playerID)
	if inst.RoomManager.players[playerID] == nil {
		// The player already left (confirmed quit or expiry); no grace to grant,
		// just finish tidying up.
		inst.deleteResumeTokenLocked(playerID)
		delete(inst.Detached, playerID)
		inst.mu.Unlock()
		return
	}
	accountID, _, _ := inst.RoomManager.PlayerIdentity(playerID)
	state, hasState := inst.RoomManager.PlayerState(playerID)
	var stateCopy PlayerState
	if hasState {
		stateCopy = *state
	}
	// Detach: drop queued per-player events (decision 2) and start the countdown.
	inst.RoomManager.DrainPlayerEvents(playerID)
	if inst.Detached == nil {
		inst.Detached = make(map[PlayerID]int)
	}
	inst.Detached[playerID] = ReconnectGraceTicks
	inst.mu.Unlock()
	if accountID != "" && hasState {
		s.persistAccountPlayerState(accountID, inst.Name, stateCopy)
	}
}

// expireDetached advances every instance's reconnect-grace countdown by one tick
// and removes any player whose grace has run out. It runs once per instance per
// tick from WebSocketServer.Tick, so the room-state mutation of a departing
// player happens on the tick goroutine (the shape M13.4 wants).
func (s *WebSocketServer) expireDetached() {
	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.expireDetached()
	}
}

func (inst *WorldInstance) expireDetached() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	for playerID, ticks := range inst.Detached {
		if ticks <= 1 {
			inst.removeDetachedLocked(playerID)
			continue
		}
		inst.Detached[playerID] = ticks - 1
	}
}

// removeDetachedLocked performs the room-side removal of an expired detached
// player. Caller holds inst.mu.
func (inst *WorldInstance) removeDetachedLocked(playerID PlayerID) {
	delete(inst.Detached, playerID)
	inst.deleteResumeTokenLocked(playerID)
	inst.RoomManager.LeavePlayer(playerID)
	inst.RoomManager.DiscardPendingScore(playerID)
}

func (s *WebSocketServer) removeClientFromInstance(inst *WorldInstance, playerID PlayerID) {
	inst.mu.Lock()
	delete(inst.Clients, playerID)
	delete(inst.Inputs, playerID)
	inst.deleteResumeTokenLocked(playerID)
	delete(inst.Detached, playerID)
	inst.RoomManager.LeavePlayer(playerID)
	// They may have quit and closed the tab before typing a name.
	inst.RoomManager.DiscardPendingScore(playerID)
	inst.mu.Unlock()
}

// write hands one message to this client's writer and returns without waiting
// for the network (M16.14e). An error means the client is finished — its queue
// overflowed, or an earlier message failed — never that this one message was
// slow.
//
// ctx is the caller's context and deliberately bounds nothing here: the message
// belongs to the client it is addressed to, not to whichever connection or tick
// produced it. The writer's own deadline is what bounds the wire.
func (c *webSocketClient) write(ctx context.Context, message interface{}) error {
	c.mu.Lock()
	if c.out == nil {
		// No connection to write to (the session tests build these).
		err := c.writeErr
		c.mu.Unlock()
		return err
	}
	if c.writeErr != nil {
		err := c.writeErr
		c.mu.Unlock()
		return err
	}
	select {
	case c.out <- message:
		c.mu.Unlock()
		return nil
	default:
	}
	c.mu.Unlock()

	// The queue is full: this client has not read clientOutboundQueue messages
	// worth of the game. That is no longer a browser being busy, and the
	// alternatives are worse — dropping messages would leave its screen holding
	// tiles the world no longer has, and blocking here would stall the tick
	// goroutine or the whole editor session behind it. Disconnect it, and say so.
	err := fmt.Errorf("slow client: %d queued messages unread", clientOutboundQueue)
	c.fail(err)
	log.Printf("zztgo: disconnecting slow client %q on world %q: %v", c.name, c.worldName, err)
	return err
}

func (s *WebSocketServer) RestoreSnapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.RoomManager.RestoreSnapshot(s.SavesDir, name)
}

func (s *WebSocketServer) LoadWorld(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := "."
	if E != nil && E.LoadedGameFileName != "" {
		dir = filepath.Dir(E.LoadedGameFileName)
	}
	return s.RoomManager.LoadWorld(dir, name)
}

// BroadcastGlobalChat sends one line to every connected player, EXCEPT those who
// have asked not to hear this author (M21.1).
//
// The filter is here, at the fan-out, and deliberately not in the client: a
// client-side filter is bypassable, and it would still deliver the text to the
// machine of the person who asked not to receive it. It is per-recipient, so it
// changes nothing about what anybody else is sent, and the author is told
// nothing at all — a block that announces itself invites the retaliation it
// exists to prevent.
//
// Server announcements do not come through here (AnnounceMessage has its own
// fan-out), so a shutdown warning can never be suppressed by a block.
func (s *WebSocketServer) BroadcastGlobalChat(ctx context.Context, author ChatAuthor, text string) {
	s.mu.Lock()
	var clients []*webSocketClient
	for _, inst := range s.Instances {
		inst.mu.Lock()
		for _, client := range inst.Clients {
			clients = append(clients, client)
		}
		inst.mu.Unlock()
	}
	s.mu.Unlock()

	msg := struct {
		Type     string   `json:"type"`
		From     string   `json:"from"`
		PlayerID PlayerID `json:"playerId,omitempty"`
		Text     string   `json:"text"`
	}{
		Type:     "chat",
		From:     author.Name,
		PlayerID: author.PlayerID,
		Text:     text,
	}

	for _, client := range clients {
		if s.chatBlocks.suppresses(client.playerID, author.PlayerID, author.AccountID) {
			continue
		}
		_ = client.write(ctx, msg)
	}
}

func (s *WebSocketServer) admitPlayerChatLikeText(ctx context.Context, client *webSocketClient, playerID PlayerID, raw string, reportRate bool) (string, bool) {
	// A mute is checked before admission and before the rate limiter (M21.2), so
	// a muted player's attempted PM costs them nothing and is announced exactly
	// like a refused global chat line.
	if s.mutes.muted(playerID, client.accountID) {
		s.tellPlayer(ctx, client, ModerationActionMute,
			"You are muted by a moderator. Your message was not sent.", false)
		return "", false
	}
	text, ok := admitChatText(raw)
	if !ok {
		return "", false
	}
	if !s.chatLimiter.allow(playerID, s.clockNow()) {
		if reportRate {
			_ = client.write(ctx, PrivateResultMessage{
				Type:      MessageTypePrivateResult,
				Delivered: false,
				Text:      "You are sending messages too quickly.",
			})
		}
		return "", false
	}
	return text, true
}

func (s *WebSocketServer) submitPrivateMessage(ctx context.Context, client *webSocketClient, sender PlayerID, req PrivateMessage) {
	if req.PlayerID == 0 || req.PlayerID == sender {
		return
	}
	text, ok := s.admitPlayerChatLikeText(ctx, client, sender, req.Text, true)
	if !ok {
		return
	}
	target, found := s.locatePlayer(req.PlayerID)
	if !found || target.client == nil {
		_ = client.write(ctx, PrivateResultMessage{
			Type:      MessageTypePrivateResult,
			PlayerID:  req.PlayerID,
			Delivered: false,
			Text:      "That player has already left.",
		})
		return
	}

	senderName := "browser"
	senderAccount := client.accountID
	if source, ok := s.locatePlayer(sender); ok {
		if source.name != "" {
			senderName = source.name
		}
		if source.account != "" {
			senderAccount = source.account
		}
	}
	targetName := target.name
	if targetName == "" {
		targetName = "that player"
	}
	if s.chatBlocks.suppresses(req.PlayerID, sender, senderAccount) {
		_ = client.write(ctx, PrivateResultMessage{
			Type:      MessageTypePrivateResult,
			PlayerID:  req.PlayerID,
			Delivered: false,
			Text:      "That player is not available.",
		})
		return
	}

	incoming := PrivateMessage{
		Type:   MessageTypePrivateMessage,
		From:   senderName,
		FromID: sender,
		To:     targetName,
		ToID:   req.PlayerID,
		Text:   text,
	}
	outgoing := incoming
	outgoing.Outgoing = true
	if err := target.client.write(ctx, incoming); err != nil {
		_ = client.write(ctx, PrivateResultMessage{
			Type:      MessageTypePrivateResult,
			PlayerID:  req.PlayerID,
			Delivered: false,
			Text:      "That player is not available.",
		})
		return
	}
	_ = client.write(ctx, outgoing)
}

// submitChatBlock records one player's block (or lifts it) and tells only them.
//
// The target is resolved through the server's own instances rather than trusted
// from the request: the client names a PlayerID, and what that id's account is —
// the thing durability depends on — is not the client's to say. A target that has
// already gone is a no-op with an explanation, not an error: the roster a player
// read a moment ago is always slightly out of date.
func (s *WebSocketServer) submitChatBlock(ctx context.Context, client *webSocketClient, blocker PlayerID, req BlockMessage) {
	if req.PlayerID == 0 || req.PlayerID == blocker {
		return // nothing to address, or the player themselves
	}
	targetAccount, targetName, found := s.identifyPlayer(req.PlayerID)
	if !found {
		_ = client.write(ctx, BlockResultMessage{
			Type:     MessageTypeBlockResult,
			PlayerID: req.PlayerID,
			Blocked:  false,
			Text:     "That player has already left.",
		})
		return
	}
	if targetName == "" {
		targetName = "that player"
	}

	durable := s.chatBlocks.set(blocker, req.PlayerID, targetAccount, req.Blocked)
	// Durability follows identity, and the two halves are separate on purpose: a
	// guest blocker has nowhere to store anything, and a guest TARGET has no id
	// to be stored. Either way the block is live for this session; only the
	// account-to-account case is written down.
	durable = durable && client.accountID != ""
	if durable {
		s.persistBlockedAccounts(client.accountID, blocker)
	}

	text := "Blocked " + targetName + " for this session."
	if req.Blocked && durable {
		text = "Blocked " + targetName + "."
	} else if !req.Blocked {
		text = "Unblocked " + targetName + "."
	}
	_ = client.write(ctx, BlockResultMessage{
		Type:     MessageTypeBlockResult,
		PlayerID: req.PlayerID,
		Name:     targetName,
		Blocked:  req.Blocked,
		Durable:  durable,
		Text:     text,
	})
}

func (s *WebSocketServer) submitProfileRequest(ctx context.Context, client *webSocketClient, req ProfileRequestMessage) {
	if req.PlayerID == 0 {
		return
	}
	target, found := s.locatePlayer(req.PlayerID)
	if !found {
		_ = client.write(ctx, ProfileResultMessage{
			Type:     MessageTypeProfileResult,
			PlayerID: req.PlayerID,
			Lines:    []string{"", "  That player has already left.", ""},
		})
		return
	}
	if target.account == "" || s.ChatDB == nil {
		lines := PublicProfileLines(target.name, AccountProfilePreferences{}, false)
		_ = client.write(ctx, ProfileResultMessage{
			Type:     MessageTypeProfileResult,
			PlayerID: req.PlayerID,
			Name:     target.name,
			Lines:    lines,
		})
		return
	}
	prefs, ok, err := s.ChatDB.GetAccountPreferences(target.account)
	if err != nil {
		log.Printf("zztgo: failed to read profile for player %d: %v", req.PlayerID, err)
		_ = client.write(ctx, ProfileResultMessage{
			Type:     MessageTypeProfileResult,
			PlayerID: req.PlayerID,
			Name:     target.name,
			Lines:    []string{"", "  Profile unavailable.", ""},
		})
		return
	}
	lines := PublicProfileLines(target.name, prefs.Profile, ok && accountProfileHasPublicFields(prefs.Profile))
	_ = client.write(ctx, ProfileResultMessage{
		Type:     MessageTypeProfileResult,
		PlayerID: req.PlayerID,
		Name:     target.name,
		Handle:   prefs.Profile.Handle,
		Lines:    lines,
	})
}

func (s *WebSocketServer) submitFollow(ctx context.Context, client *webSocketClient, follower PlayerID, req FollowMessage) {
	result := FollowResultMessage{
		Type:     MessageTypeFollowResult,
		PlayerID: req.PlayerID,
	}
	reply := func(text string) {
		result.Text = text
		_ = client.write(ctx, result)
	}
	if client.accountID == "" || s.ChatDB == nil {
		reply("Sign in to follow players.")
		return
	}
	if req.PlayerID == 0 || req.PlayerID == follower {
		reply("You cannot follow yourself.")
		return
	}
	target, found := s.locatePlayer(req.PlayerID)
	if !found {
		reply("That player has already left.")
		return
	}
	if target.account == "" {
		result.Name = target.name
		reply("Guests cannot be followed.")
		return
	}
	if target.account == client.accountID {
		result.Name = target.name
		reply("You cannot follow yourself.")
		return
	}

	prefs, _, err := s.ChatDB.GetAccountPreferences(client.accountID)
	if err != nil {
		log.Printf("zztgo: failed to read preferences for account %q while following: %v", client.accountID, err)
		reply("Could not update follows.")
		return
	}
	if req.Follow {
		prefs.FollowedAccounts = addAccountID(prefs.FollowedAccounts, target.account)
	} else {
		prefs.FollowedAccounts = removeAccountID(prefs.FollowedAccounts, target.account)
	}
	if err := s.ChatDB.PutAccountPreferences(client.accountID, prefs); err != nil {
		log.Printf("zztgo: failed to store follows for account %q: %v", client.accountID, err)
		reply("Could not update follows.")
		return
	}
	result.Name = target.name
	result.Followed = req.Follow
	name := publicAccountName(target.name, target.account, s.loadAccountPreferences)
	if req.Follow {
		reply("Following " + name + ".")
	} else {
		reply("Unfollowed " + name + ".")
	}
}

func addAccountID(accounts []string, account string) []string {
	account = strings.TrimSpace(account)
	if account == "" {
		return sanitizeFollowedAccounts(accounts, "")
	}
	for _, existing := range accounts {
		if existing == account {
			return sanitizeFollowedAccounts(accounts, "")
		}
	}
	return sanitizeFollowedAccounts(append(accounts, account), "")
}

func removeAccountID(accounts []string, account string) []string {
	out := accounts[:0]
	for _, existing := range accounts {
		if existing != account {
			out = append(out, existing)
		}
	}
	return sanitizeFollowedAccounts(out, "")
}

func publicAccountName(fallback, accountID string, load func(string) (AccountPreferences, bool)) string {
	prefs, ok := load(accountID)
	if ok {
		if prefs.Profile.Handle != "" {
			return "@" + prefs.Profile.Handle
		}
		if prefs.Profile.DisplayName != "" {
			return prefs.Profile.DisplayName
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "that player"
}

// blockedInRoster names which of the people this client can currently SEE are
// blocked for them (M21.4), so a returning player's Players window opens marked
// instead of claiming no knowledge.
//
// The roster is the whole visible set, and that is not an approximation: the
// window's other source of rows is the recent chat senders, and a blocked
// sender's lines never reach this socket at all — neither the live fan-out nor
// the history replay writes them — so nobody blockable can be offered from
// there.
//
// Only ids go out. The durable half of a block is keyed on the target's
// accountID, and answering with the stored list would hand the recipient
// account ids they have no other way to see; asking the question once per
// visible player answers exactly what the window needs and leaks nothing.
//
// The accounts are read under inst.mu and the block set is consulted after it is
// released: chat blocks are service state, and their lock is never held with a
// world's (chat_blocks.go).
func (s *WebSocketServer) blockedInRoster(recipient PlayerID, inst *WorldInstance, roster []PlayerSnapshot) []PlayerID {
	if len(roster) == 0 {
		return nil
	}
	type rosterMember struct {
		id      PlayerID
		account string
	}
	members := make([]rosterMember, 0, len(roster))
	inst.mu.Lock()
	for _, player := range roster {
		if player.ID == 0 || player.ID == recipient {
			continue
		}
		account, _, _ := inst.RoomManager.PlayerIdentity(player.ID)
		members = append(members, rosterMember{id: player.ID, account: account})
	}
	inst.mu.Unlock()

	var blocked []PlayerID
	for _, member := range members {
		if s.chatBlocks.suppresses(recipient, member.id, member.account) {
			blocked = append(blocked, member.id)
		}
	}
	return blocked
}

func (s *WebSocketServer) followedInRoster(recipient PlayerID, inst *WorldInstance, roster []PlayerSnapshot) []PlayerID {
	if len(roster) == 0 || s.ChatDB == nil {
		return nil
	}
	var recipientAccount string
	type rosterMember struct {
		id      PlayerID
		account string
	}
	members := make([]rosterMember, 0, len(roster))
	inst.mu.Lock()
	recipientAccount, _, _ = inst.RoomManager.PlayerIdentity(recipient)
	for _, player := range roster {
		if player.ID == 0 || player.ID == recipient {
			continue
		}
		account, _, _ := inst.RoomManager.PlayerIdentity(player.ID)
		if account != "" {
			members = append(members, rosterMember{id: player.ID, account: account})
		}
	}
	inst.mu.Unlock()
	if recipientAccount == "" {
		return nil
	}
	prefs, ok, err := s.ChatDB.GetAccountPreferences(recipientAccount)
	if err != nil {
		log.Printf("zztgo: failed to read follows for account %q: %v", recipientAccount, err)
		return nil
	}
	if !ok || len(prefs.FollowedAccounts) == 0 {
		return nil
	}
	followedAccounts := make(map[string]struct{}, len(prefs.FollowedAccounts))
	for _, account := range prefs.FollowedAccounts {
		followedAccounts[account] = struct{}{}
	}
	var followed []PlayerID
	for _, member := range members {
		if _, ok := followedAccounts[member.account]; ok {
			followed = append(followed, member.id)
		}
	}
	return followed
}

type liveAccountLocation struct {
	account string
	name    string
	world   string
}

func (s *WebSocketServer) friendPresenceByWorld(accountID string) map[string][]FriendPresenceSummary {
	if accountID == "" || s.ChatDB == nil {
		return nil
	}
	prefs, ok, err := s.ChatDB.GetAccountPreferences(accountID)
	if err != nil {
		log.Printf("zztgo: failed to read follows for /api/worlds account %q: %v", accountID, err)
		return nil
	}
	if !ok || len(prefs.FollowedAccounts) == 0 {
		return nil
	}
	followed := make(map[string]struct{}, len(prefs.FollowedAccounts))
	for _, account := range prefs.FollowedAccounts {
		followed[account] = struct{}{}
	}

	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	var live []liveAccountLocation
	for _, inst := range instances {
		// M32.1: a challenge run is not a place friends can be seen. Its key is
		// not a world anyone can join, so reporting it would put an unreachable
		// location in the picker — and it would tell followers that someone is
		// mid-attempt, which is a run's business and not the roster's.
		if inst.Challenge != nil {
			continue
		}
		inst.mu.Lock()
		ids := inst.RoomManager.playerIDs()
		for _, playerID := range ids {
			account, name, ok := inst.RoomManager.PlayerIdentity(playerID)
			if !ok || account == "" {
				continue
			}
			if _, want := followed[account]; !want {
				continue
			}
			live = append(live, liveAccountLocation{account: account, name: name, world: inst.Name})
		}
		inst.mu.Unlock()
	}
	if len(live) == 0 {
		return nil
	}

	seen := make(map[string]map[string]struct{})
	out := make(map[string][]FriendPresenceSummary)
	for _, loc := range live {
		targetPrefs, ok, err := s.ChatDB.GetAccountPreferences(loc.account)
		if err != nil {
			log.Printf("zztgo: failed to read presence preference for account %q: %v", loc.account, err)
			continue
		}
		if !ok || !targetPrefs.ShareLocationWithFollowers {
			continue
		}
		if seen[loc.world] == nil {
			seen[loc.world] = make(map[string]struct{})
		}
		if _, dup := seen[loc.world][loc.account]; dup {
			continue
		}
		seen[loc.world][loc.account] = struct{}{}
		name := targetPrefs.Profile.DisplayName
		if name == "" {
			name = loc.name
		}
		if name == "" && targetPrefs.Profile.Handle != "" {
			name = "@" + targetPrefs.Profile.Handle
		}
		if name == "" {
			name = "Player"
		}
		out[loc.world] = append(out[loc.world], FriendPresenceSummary{Name: name, Handle: targetPrefs.Profile.Handle})
	}
	for world := range out {
		sort.SliceStable(out[world], func(i, j int) bool {
			a := out[world][i]
			b := out[world][j]
			if a.Handle != "" || b.Handle != "" {
				return a.Handle < b.Handle
			}
			return strings.ToUpper(a.Name) < strings.ToUpper(b.Name)
		})
	}
	return out
}

// identifyPlayer answers who a PlayerID belongs to, across every hosted world:
// global chat is server-wide, so the person a player wants to blocked may not be
// in their room, or even in their world.
func (s *WebSocketServer) identifyPlayer(playerID PlayerID) (accountID, name string, found bool) {
	target, ok := s.locatePlayer(playerID)
	return target.account, target.name, ok
}

// moderationTarget is everything an operator action needs to know about the
// person it names: who they are, which connection to end, and where and when the
// action lands — the last two because an audit line that cannot say which world
// and which tick is a note, not a record.
type moderationTarget struct {
	inst    *WorldInstance
	client  *webSocketClient
	account string
	name    string
	world   string
	tick    int16
}

// locatePlayer finds a player in whichever hosted world holds them. It is
// identifyPlayer's question with the rest of the answer attached, and it is asked
// of every instance for the same reason: moderation, like global chat, is
// server-wide, so the person an operator names may be in another world.
//
// The instance lock is taken one at a time and released before the next, and the
// server lock is not held while any of them is: the same discipline the chat
// fan-out follows.
func (s *WebSocketServer) locatePlayer(playerID PlayerID) (moderationTarget, bool) {
	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.mu.Lock()
		account, playerName, ok := inst.RoomManager.PlayerIdentity(playerID)
		var client *webSocketClient
		var tick int16
		if ok {
			client = inst.Clients[playerID]
			if boardID, _, located := inst.RoomManager.PlayerLocation(playerID); located {
				if room, found := inst.RoomManager.Room(boardID); found && room.Engine != nil {
					tick = room.Engine.CurrentTick
				}
			}
		}
		inst.mu.Unlock()
		if ok {
			return moderationTarget{
				inst:    inst,
				client:  client,
				account: account,
				name:    playerName,
				world:   inst.Name,
				tick:    tick,
			}, true
		}
	}
	return moderationTarget{}, false
}

// isOperator is the whole of operator status: an account on the allowlist the
// deployment configured (M21.2). A guest has no account and can never match, and
// an unset or empty allowlist matches nobody — the check fails closed, so a
// misconfigured server has no moderators rather than any.
func (s *WebSocketServer) isOperator(accountID string) bool {
	return accountID != "" && s.Moderators[accountID]
}

// refusedAtTheDoor answers whether this account may not be admitted, and what to
// tell it. Called before a join, a resume or an editor entry.
//
// The attempt is logged but deliberately NOT written to the audit: a refused
// account's browser may retry on its own backoff, and an audit an outsider can
// append to at will is one nobody can read.
func (s *WebSocketServer) refusedAtTheDoor(account AuthenticatedAccount, authenticated bool) (ModerationNoticeMessage, bool) {
	if !authenticated || !s.Refusals.Refuses(account.ID) {
		return ModerationNoticeMessage{}, false
	}
	log.Printf("zztgo: refusing entry to account %q", account.ID)
	return ModerationNoticeMessage{
		Type:   MessageTypeModerationNotice,
		Action: ModerationActionRefuse,
		Text:   "A moderator has refused this account.",
		Ended:  true,
	}, true
}

// tellPlayer sends one moderated player their notice. Unlike a block, which the
// blocked player is never told about, every sanction here announces itself to its
// target: a mute nobody is told about is indistinguishable from a broken server,
// and a kick nobody is told about is indistinguishable from a dropped connection.
func (s *WebSocketServer) tellPlayer(ctx context.Context, client *webSocketClient, action, text string, ended bool) {
	if client == nil {
		return
	}
	_ = client.write(ctx, ModerationNoticeMessage{
		Type:   MessageTypeModerationNotice,
		Action: action,
		Text:   text,
		Ended:  ended,
	})
}

// endConnection closes a moderated player's socket after giving the notice a
// bounded chance to reach them, and does the waiting on its own goroutine: the
// caller is another player's read loop, and no player's connection may be made to
// wait on another player's browser (M16.14e).
//
// Nothing here removes the player from their room. Closing the socket ends its
// read loop, which runs handleReadLoopExit — the same teardown a closed tab runs,
// detaching with the usual reconnect grace. A kicked player may return; a refused
// one is turned away at the door when they try.
func (s *WebSocketServer) endConnection(client *webSocketClient) {
	if client == nil {
		return
	}
	go func() {
		client.stop()
		if client.conn != nil {
			_ = client.conn.CloseNow()
		}
	}()
}

// submitModeration performs one operator action, or refuses it, and audits
// either way.
//
// The order is the point: authority first, then the target, then the action, then
// the audit, then the operator's confirmation. The audit is written BEFORE the
// operator is told anything, so there is no outcome an operator can have seen
// that the record does not contain.
func (s *WebSocketServer) submitModeration(ctx context.Context, client *webSocketClient, operator PlayerID, req ModerateMessage) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case ModerationActionMute, ModerationActionUnmute, ModerationActionKick, ModerationActionRefuse:
	default:
		return // not an action at all: nothing to do, and nothing worth recording
	}
	// A moderation request is at most as frequent as a chat line, and it shares
	// chat's policy for the same reason: the audit below records denials, so an
	// unbounded stream of them would be a stranger writing to the operator's own
	// record.
	if !s.moderateLimiter.allow(operator, s.clockNow()) {
		return
	}

	operatorAccount := client.accountID
	audit := ModerationAuditEntry{
		At:         s.clockNow(),
		Action:     action,
		Operator:   operatorAccount,
		OperatorID: operator,
		Target:     req.PlayerID,
		World:      client.worldName,
	}

	deny := func(detail, text string) {
		audit.Result = ModerationResultDenied
		audit.Detail = detail
		s.Audit.Record(audit)
		_ = client.write(ctx, ModerateResultMessage{
			Type:     MessageTypeModerateResult,
			Action:   action,
			PlayerID: req.PlayerID,
			Applied:  false,
			Text:     text,
		})
	}

	if !s.isOperator(operatorAccount) {
		// The one denial that is a security event rather than a mistake: it is
		// recorded with the account that asked, which is the only durable thing
		// about the asker.
		deny("not on the moderator allowlist", "You are not a moderator.")
		return
	}
	if req.PlayerID == 0 || req.PlayerID == operator {
		deny("target is the operator or nobody", "You cannot moderate yourself.")
		return
	}

	// Only now, after the authority check: the operator's display name is read
	// from their room rather than from the connection, which never carries one
	// for a player. The audit's identity is the account id — that is what a denial
	// is recorded against — and the name is here so a record read a month later
	// names a person instead of an opaque id.
	_, operatorName, _ := s.identifyPlayer(operator)
	audit.OperatorName = operatorName

	target, found := s.locatePlayer(req.PlayerID)
	if !found {
		// The roster an operator read a moment ago is always slightly out of
		// date, so a vanished target is a no-op with an explanation, not an error
		// (submitChatBlock takes the same view).
		audit.Result = ModerationResultNoOp
		audit.Detail = "target not connected"
		s.Audit.Record(audit)
		_ = client.write(ctx, ModerateResultMessage{
			Type:     MessageTypeModerateResult,
			Action:   action,
			PlayerID: req.PlayerID,
			Applied:  false,
			Text:     "That player has already left.",
		})
		return
	}
	audit.TargetAccount = target.account
	audit.TargetName = target.name
	audit.World = target.world
	audit.Tick = target.tick

	name := target.name
	if name == "" {
		name = "that player"
	}

	result := ModerateResultMessage{
		Type:     MessageTypeModerateResult,
		Action:   action,
		PlayerID: req.PlayerID,
		Name:     target.name,
		Applied:  true,
	}

	switch action {
	case ModerationActionMute:
		// Durable only in the sense a mute can be: keyed to the account, so a
		// reconnect does not lift it. It still ends with the process, by decision
		// — refusal is the sanction that outlives a restart (moderation.go).
		result.Durable = s.mutes.set(req.PlayerID, target.account, true)
		s.tellPlayer(ctx, target.client, ModerationActionMute,
			"A moderator has muted you in chat.", false)
		result.Text = "Muted " + name + "."
		if !result.Durable {
			result.Text = "Muted " + name + " for this session."
		}

	case ModerationActionUnmute:
		result.Durable = s.mutes.set(req.PlayerID, target.account, false)
		s.tellPlayer(ctx, target.client, ModerationActionUnmute,
			"A moderator has unmuted you.", false)
		result.Text = "Unmuted " + name + "."

	case ModerationActionKick:
		s.tellPlayer(ctx, target.client, ModerationActionKick,
			"A moderator has removed you from the game.", true)
		s.endConnection(target.client)
		result.Text = "Kicked " + name + "."

	case ModerationActionRefuse:
		if target.account == "" {
			// The honest limit, stated where it is felt rather than discovered
			// later: a guest has no durable identity to refuse, so the strongest
			// action available against one is a kick, and the operator is told
			// that in the same breath (M21.2's spec says to ship this and say so).
			s.tellPlayer(ctx, target.client, ModerationActionKick,
				"A moderator has removed you from the game.", true)
			s.endConnection(target.client)
			audit.Detail = "guest: kicked, not refused"
			result.Durable = false
			result.Text = "Kicked " + name + ": a guest cannot be refused."
			break
		}
		err := s.Refusals.Refuse(RefusedAccount{
			Account: target.account,
			Name:    target.name,
			By:      operatorAccount,
			ByName:  operatorName,
			World:   target.world,
			At:      s.clockNow(),
		})
		if err != nil {
			// The refusal did not reach disk, so it would not survive the restart
			// it exists for. Telling the operator it held would be worse than
			// telling them to try again: they would stop watching for somebody
			// who is coming back.
			audit.Result = ModerationResultFailed
			audit.Detail = err.Error()
			s.Audit.Record(audit)
			log.Printf("zztgo: refusal of account %q not recorded: %v", target.account, err)
			_ = client.write(ctx, ModerateResultMessage{
				Type:     MessageTypeModerateResult,
				Action:   action,
				PlayerID: req.PlayerID,
				Name:     target.name,
				Applied:  false,
				Text:     "Could not record the refusal.",
			})
			return
		}
		s.tellPlayer(ctx, target.client, ModerationActionRefuse,
			"A moderator has refused this account.", true)
		s.endConnection(target.client)
		result.Durable = true
		result.Text = "Refused " + name + ". They cannot rejoin."
	}

	audit.Result = ModerationResultApplied
	s.Audit.Record(audit)
	_ = client.write(ctx, result)
}

// seedAccountBlocks loads a signed-in player's stored blocks into their live set
// for this connection. A read failure is logged and dropped rather than refusing
// the join: a player who cannot be told who they blocked should still be able to
// play, and the session-scoped half of their blocks still works.
func (s *WebSocketServer) seedAccountBlocks(playerID PlayerID, accountID string) {
	if s.ChatDB == nil || accountID == "" {
		return
	}
	prefs, ok, err := s.ChatDB.GetAccountPreferences(accountID)
	if err != nil {
		log.Printf("zztgo: failed to load blocks for account %q: %v", accountID, err)
		return
	}
	if !ok {
		return
	}
	s.chatBlocks.seedAccounts(playerID, prefs.BlockedAccounts)
}

// persistBlockedAccounts writes a signed-in blocker's durable half back to the
// M19.3 preferences store — a field on the document that store was shaped to
// grow, not a store of its own. A read-modify-write of the whole document
// because that is what the store's shape is: one document per account.
func (s *WebSocketServer) persistBlockedAccounts(accountID string, blocker PlayerID) {
	if s.ChatDB == nil || accountID == "" {
		return
	}
	prefs, _, err := s.ChatDB.GetAccountPreferences(accountID)
	if err != nil {
		log.Printf("zztgo: failed to read preferences for account %q while blocking: %v", accountID, err)
		return
	}
	prefs.BlockedAccounts = s.chatBlocks.blockedAccounts(blocker)
	sort.Strings(prefs.BlockedAccounts)
	if err := s.ChatDB.PutAccountPreferences(accountID, prefs); err != nil {
		log.Printf("zztgo: failed to store blocks for account %q: %v", accountID, err)
	}
}

// AnnounceShutdown pushes a shutdown warning to every connected player whose
// world engine is still responsive, so they can save before the server restarts.
//
// It never blocks on a wedged engine: the global lock is taken with a bounded
// TryLock, and each instance lock with a plain TryLock. An instance we cannot
// lock is skipped — that engine is already stuck, which is exactly the case we
// cannot warn a player through ("as long as the engine for their world is up").
// It returns the number of reachable players it messaged, so a caller draining
// for shutdown can skip the wait when nobody is connected.
func (s *WebSocketServer) AnnounceShutdown(ctx context.Context, seconds int, text string) int {
	insts, ok := s.instancesForAnnounce()
	if !ok {
		log.Printf("shutdown announce: server lock busy, players not warned")
		return 0
	}

	var clients []*webSocketClient
	for _, inst := range insts {
		if !inst.mu.TryLock() {
			continue // wedged engine — its players cannot be reached
		}
		for _, client := range inst.Clients {
			clients = append(clients, client)
		}
		inst.mu.Unlock()
	}

	msg := struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Seconds int    `json:"seconds"`
	}{Type: "announce", Text: text, Seconds: seconds}

	for _, client := range clients {
		_ = client.write(ctx, msg)
	}
	return len(clients)
}

// instancesForAnnounce snapshots the instance pointers under a bounded TryLock so
// a wedged global lock (the failure this warning exists for) degrades to a
// best-effort skip instead of blocking the whole shutdown drain.
func (s *WebSocketServer) instancesForAnnounce() ([]*WorldInstance, bool) {
	for i := 0; i < 20; i++ {
		if s.mu.TryLock() {
			insts := make([]*WorldInstance, 0, len(s.Instances))
			for _, inst := range s.Instances {
				insts = append(insts, inst)
			}
			s.mu.Unlock()
			return insts, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, false
}
