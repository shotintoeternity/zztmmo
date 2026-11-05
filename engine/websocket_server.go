// The WebSocket server: everything between a browser and a running world.
//
// A WebSocketServer owns a set of WorldInstances, each one a hosted world with
// its own RoomManager, its own tick and the lock that serializes access to it.
// Gameplay never runs on a connection's goroutine — a client goroutine parses a
// message and hands it to the instance, and the instance's tick is the only
// thing that touches engine state. That is what keeps the simulation
// deterministic while several sockets talk to it at once.
//
// A webSocketClient is one connection: a write loop behind a bounded queue. A
// client that falls far enough behind is disconnected rather than stalling the
// world everybody else is standing in — and disconnected rather than having its
// messages dropped, because a client that misses a diff is left drawing tiles
// the world no longer has.
//
// Instances are evicted when nothing is left holding them — players, detached
// reconnects, spectators, title subscribers or a pending autosave — counted in
// ticks rather than wall-clock so the rule stays deterministic and testable.

package zztgo

import (
	"bytes"
	"context"
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

// maxEvictedRejoins bounds how often a join re-resolves an instance that was
// evicted under it (M36.18). One retry is all a real race needs; the bound
// only stops a pathological loop.
const maxEvictedRejoins = 3

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
	// FriendlyFire is the deployment's projectile policy for every instance this
	// server hosts. It is server-owned rather than authored — the same split
	// RoomManager.WorldIdentity documents — so a downloaded world can never
	// decide to make a party hostile. Set it through SetFriendlyFire, which also
	// reaches instances that already exist. cmd/zzt-server sets it from
	// -friendly-fire; NewWebSocketServer leaves it false, the co-op default every
	// hosted world has shipped with.
	FriendlyFire bool
	// AutosaveEveryTicks, when >0, snapshots every occupied instance every this
	// many ticks from the tick loop (M13.3). Zero disables. cmd/zzt-server sets it
	// from -autosave seconds via seconds*1000/tickMillis, so it shares the tick
	// clock rather than a second timer. NewWebSocketServer leaves it 0, so tests
	// never autosave unless they set the seam.
	AutosaveEveryTicks int
	autosaveTicks      int // countdown accumulator; touched only on the tick goroutine
	// restoreAutosaves is set by RestoreAutosaves: this boot trusts the
	// autosaves it found on disk, so an instance recreated after eviction loads
	// its autosave too (M36.18). A -fresh boot never calls RestoreAutosaves, and
	// then only the autosaves this process wrote itself (autosavedThisBoot) are
	// loaded. Both are guarded by s.mu.
	restoreAutosaves  bool
	autosavedThisBoot map[string]bool
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
	// ReplayEvictIdleTicks, when >0, drops a replay instance after this many
	// consecutive ticks with no watcher (M36.19).
	ReplayEvictIdleTicks int
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
	// saveLimiter bounds how many saves one player may write (M36.17). Its own
	// lock, never held with inst.mu.
	saveLimiter saveRateLimiter
	// saveWriteMu serializes player save writes, which run off the room lock
	// (M36.17). Two players may pick the same name, and writeWorldSnapshot's
	// temp file is named after the save, so two concurrent writes of one name
	// would share it. Never held with inst.mu.
	saveWriteMu sync.Mutex
	// saveWriteHook, when set, runs just before a player save is written, with
	// no lock held. A test seam (M36.17): it parks a write to show the room
	// ticks on while the file is being written.
	saveWriteHook func()

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
	metrics             *serverMetrics
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
	// unsaved says the room has had players in it since its last autosave
	// (M36.18). It is what lets the play after the last cadence save survive
	// everyone leaving: an empty room with unsaved set is autosaved the next
	// tick, and an instance is never evicted while it is set. Only set while
	// autosave is on.
	unsaved bool
	// autosaveFailed holds back the every-tick last-leave save after a write
	// failed; the cadence retries.
	autosaveFailed bool
	// evicted is set, under mu, when the instance leaves s.Instances. A join or
	// watch resolved before the eviction checks it under the same lock and
	// re-resolves rather than landing in an instance nobody ticks (M36.18).
	evicted bool
	// Private marks an instance nobody chose to make public: today that is an
	// editor test-play copy (M10.4). It exists because that copy is the one
	// private instance kind NOT excluded by construction — randomTestPlayWorldName
	// mints TP+6 hex, which sanitizes as cleanly as TOWN does. The mark keeps
	// the copy out of autosave. (It was added for the Gazette, M34.1, which
	// M37.4 retired.)
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

func NewWebSocketServer(world TWorld, defaultBoard int16) *WebSocketServer {
	rm := NewRoomManagerForWorld(world, world.Info.Name)
	name := rm.WorldName()
	if name == "Untitled" || name == "" {
		name = "TOWN"
		rm.WorldIdentity = name
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
	s := &WebSocketServer{
		RoomManager:            rm,
		FriendlyFire:           true,
		DefaultBoard:           defaultBoard,
		TickDuration:           ServerTickDuration,
		OriginHosts:            []string{"localhost:*", "127.0.0.1:*"},
		InstanceEvictIdleTicks: DefaultInstanceEvictIdleTicks,
		ReplayEvictIdleTicks:   DefaultReplayEvictIdleTicks,
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
	s.evictIdleReplays()
	for _, title := range titles {
		title.Tick()
	}

	s.maybeAutosave()
	s.autosaveDeparted()
	s.evictIdleInstances()
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
	inst.evicted = true
	return recorder, true
}

func (inst *WorldInstance) evictableIdleLocked() bool {
	if len(inst.Clients) != 0 ||
		len(inst.Inputs) != 0 ||
		len(inst.Detached) != 0 ||
		len(inst.Spectators) != 0 ||
		inst.autosaving ||
		inst.unsaved {
		// unsaved: the last players' progress is not on disk yet. autosaveDeparted
		// writes it the tick after they leave, before the idle clock can run out,
		// so a world is only ever dropped once its autosave is current (M36.18).
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
	if s.autosaveEnabled() && inst.Challenge == nil && !inst.Private && len(inst.RoomManager.players) != 0 {
		// Players (connected or inside the reconnect grace) are in the room, so
		// this tick may change what an autosave would write (M36.18). A private
		// test-play copy is left out: no .ZZT answers to its random name, so
		// its autosave could never be loaded and would only litter the
		// directory.
		inst.unsaved = true
	}
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

// playerJoin is one connection's join as ServeHTTP builds it up. Its fields are
// reassigned as the join resolves (a challenge swaps inst, an evicted instance
// is re-resolved), so the phases share it by pointer.
type playerJoin struct {
	inst                  *WorldInstance
	safeWorld             string
	challengeID           string
	challengeRunRequested string
	challengeRun          *ChallengeRun

	join          JoinMessage
	joinColor     string
	client        *webSocketClient
	account       AuthenticatedAccount
	authenticated bool

	storedState    PlayerState
	hasStoredState bool
	profileHandle  string
	hasProfile     bool

	playerID PlayerID
	snapshot SnapshotMessage
	resumed  bool
}

// ServeHTTP admits one connection and then serves it. The order of the phases
// is the contract:
//
//   - The refusal (M21.2) is checked before every door that admits anyone: the
//     editor, a challenge run, watching, and play. Challenge resolution and the
//     watch branch come after it, so a refused account cannot use either.
//   - A resume is tried before a fresh join, and a ?run= join that did not
//     resume ends there: it may reclaim a run but never start one.
//   - Stored blocks go live before the first frame's roster flags are computed
//     and before the chat backlog is replayed.
func (s *WebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if replayID := r.URL.Query().Get("replay"); replayID != "" {
		s.serveReplayHTTP(w, r, replayID)
		return
	}

	pj, ok := s.resolveJoinTarget(w, r)
	if !ok {
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
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	if envelope.Type == MessageTypeEditorEnter {
		s.serveEditorEntry(ctx, conn, r, raw)
		return
	}
	if envelope.Type != MessageTypeJoin {
		return
	}
	if err := json.Unmarshal(raw, &pj.join); err != nil {
		return
	}

	// The color is validated once, here, where the untrusted join is read —
	// beside the name it sits next to on the wire (M19.1). Everything
	// downstream stores and broadcasts whatever this returns, so anything that
	// is not "#" plus six hex digits must become the empty string at this line
	// and never later.
	pj.joinColor = SanitizePlayerColor(pj.join.Color)

	pj.client = newWebSocketClient(conn, pj.safeWorld)
	defer pj.client.stop()
	pj.account, pj.authenticated = s.authAccount(r)
	// Before anything is joined, spawned or resumed (M21.2). A refusal that took
	// effect after the join would put the player in the room for a tick and take
	// the reconnect path out of it, which is a drop rather than a refusal — and
	// the resume token below would let them reclaim it.
	if notice, refused := s.refusedAtTheDoor(pj.account, pj.authenticated); refused {
		_ = pj.client.write(ctx, notice)
		return
	}

	if !s.resolveChallengeRun(ctx, pj) {
		return
	}

	// M22.1: a watcher takes none of the paths below — no stored state, no color,
	// no resume, no stat, no read of anything they typed.
	if pj.join.Spectate {
		s.serveSpectator(ctx, conn, pj.client, pj.inst, pj.join)
		return
	}

	if pj.authenticated {
		pj.client.accountID = pj.account.ID
		pj.join.Name = pj.account.DisplayName()
		pj.storedState, pj.hasStoredState = s.loadAccountPlayerState(pj.account.ID, pj.safeWorld)
		// The account, not this browser, is where a signed-in player's color
		// lives (M19.3). Resolved here so both the fresh join and the resume
		// below see one already-decided value.
		pj.joinColor = s.resolveAccountPlayerColor(pj.account.ID, pj.joinColor)
	}
	pj.profileHandle, pj.hasProfile = s.accountProfileSummary(pj.account.ID)

	s.resumeJoin(pj)

	// M32.1: a `?run=` join RECLAIMS an attempt and may never start a player in
	// one. Without this, a token-less run key fresh-joins a second player into
	// somebody's measured run — and run keys are sequential, so they can be
	// guessed. The refusal is the same one an ended run gets, because from the
	// outside those are the same fact: this browser is not in that run.
	if pj.challengeRunRequested != "" && !pj.resumed {
		// Nothing to tidy: no id has been minted and no stat spawned — the
		// refusal happens before the fresh-join path below, which is the point.
		_ = pj.client.write(ctx, ChallengeErrorMessage{
			Type:        MessageTypeChallengeError,
			ChallengeID: pj.challengeID,
			Reason:      "That run has ended.",
		})
		return
	}

	if !pj.resumed && !s.freshJoin(ctx, pj) {
		return
	}

	if pj.challengeRun != nil {
		// The run's one player. It is set on both the fresh join and the resume,
		// because a reconnect inside the grace reclaims the SAME PlayerID and the
		// completion check reads it every tick.
		pj.inst.mu.Lock()
		pj.challengeRun.PlayerID = pj.playerID
		pj.inst.mu.Unlock()
		pj.snapshot.Challenge = pj.challengeRun.Def.ID
		pj.snapshot.ChallengeRun = pj.challengeRun.Key
	}

	s.seedSocialState(pj)

	if !s.sendFirstFrame(ctx, pj) {
		return
	}

	s.readLoop(ctx, conn, pj.client, pj.playerID)
	if activeInst := s.instanceForClient(pj.client); activeInst != nil {
		s.handleReadLoopExit(activeInst, pj.client, pj.playerID)
	}
}

// resolveJoinTarget reads which world the upgrade asks for, before the socket
// is accepted, so a bad name is still an HTTP error.
func (s *WebSocketServer) resolveJoinTarget(w http.ResponseWriter, r *http.Request) (*playerJoin, bool) {
	pj := &playerJoin{}
	// M32.1: a challenge join names a catalogue id (start a fresh attempt) or a
	// run key (reclaim the attempt this browser is already in). Both resolve
	// AFTER the socket is accepted and the account is known — the instance a run
	// gets is minted per attempt and per player, so there is nothing to resolve
	// until we know who is asking.
	pj.challengeID = strings.TrimSpace(r.URL.Query().Get("challenge"))
	pj.challengeRunRequested = strings.TrimSpace(r.URL.Query().Get("run"))

	worldName := r.URL.Query().Get("world")
	switch {
	case pj.challengeID != "" || pj.challengeRunRequested != "":
		// Resolved by resolveChallengeRun, once the join message and the account
		// have been read.
	case worldName == "":
		pj.inst = s.DefaultInstance
		pj.safeWorld = pj.inst.Name
	default:
		var err error
		pj.safeWorld, err = SanitizeSaveName(worldName)
		if err != nil {
			http.Error(w, "invalid world name", http.StatusBadRequest)
			return nil, false
		}
		pj.inst, err = s.GetOrCreateInstance(pj.safeWorld)
		if err != nil {
			http.Error(w, "failed to load world: "+err.Error(), http.StatusInternalServerError)
			return nil, false
		}
	}
	return pj, true
}

// serveEditorEntry is the editor's door: the first frame was editorEnter.
func (s *WebSocketServer) serveEditorEntry(ctx context.Context, conn *websocket.Conn, r *http.Request, raw json.RawMessage) {
	var enter EditorEnterMessage
	if err := json.Unmarshal(raw, &enter); err != nil {
		return
	}
	account, authenticated := s.authAccount(r)
	if notice, refused := s.refusedAtTheDoor(account, authenticated); refused {
		_ = wsjson.Write(ctx, conn, notice)
		return
	}
	s.serveEditor(ctx, conn, enter, account, authenticated)
}

// resolveChallengeRun points pj at the challenge run its query names, if any
// (M32.1). It reports false when the connection must end.
func (s *WebSocketServer) resolveChallengeRun(ctx context.Context, pj *playerJoin) bool {
	if pj.challengeID == "" && pj.challengeRunRequested == "" {
		return true
	}
	if pj.join.Spectate {
		// A run is one player's measured attempt, and it is not addressable
		// by anyone else: there is no watch route to it, and asking for one
		// here would be the only way to reach another player's run.
		_ = pj.client.write(ctx, ChallengeErrorMessage{Type: MessageTypeChallengeError, ChallengeID: pj.challengeID, Reason: "A challenge run cannot be watched."})
		return false
	}
	var err error
	pj.inst, pj.challengeRun, err = s.resolveChallengeJoin(pj.challengeID, pj.challengeRunRequested, pj.account, pj.authenticated)
	if err != nil {
		_ = pj.client.write(ctx, ChallengeErrorMessage{Type: MessageTypeChallengeError, ChallengeID: pj.challengeID, Reason: challengeRefusalText(err)})
		return false
	}
	pj.safeWorld = pj.inst.Name
	pj.client.setWorldName(pj.inst.Name)
	return true
}

// resumeJoin reclaims the player pj's resume token names, if it names one.
func (s *WebSocketServer) resumeJoin(pj *playerJoin) {
	// Resume first: a valid token reclaims the dropped run (same PlayerID/statID,
	// inventory intact). An unknown or expired token falls through to a fresh join.
	if pj.join.ResumeToken == "" {
		return
	}
	pid, snap, ok := s.tryResume(pj.inst, pj.client, pj.join.ResumeToken)
	if !ok {
		return
	}
	pj.playerID, pj.snapshot, pj.resumed = pid, snap, true
	inst := pj.inst
	inst.mu.Lock()
	if pj.authenticated {
		inst.RoomManager.SetPlayerIdentity(pj.playerID, pj.account.ID, pj.account.DisplayName())
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
	inst.RoomManager.SetPlayerColor(pj.playerID, pj.joinColor)
	if pj.authenticated {
		inst.RoomManager.SetPlayerProfileSummary(pj.playerID, pj.profileHandle, pj.hasProfile)
	}
	inst.mu.Unlock()
}

// freshJoin spawns a new player for pj. It reports false when the connection
// must end; pj.inst may have been re-resolved by then.
func (s *WebSocketServer) freshJoin(ctx context.Context, pj *playerJoin) bool {
	// Mint a process-unique id before taking inst.mu; the two locks are never
	// held together (M14.1).
	pj.playerID = s.mintPlayerID()

	// The join runs under inst.mu; a defer guarantees the unlock even if a
	// callee panics (e.g. spawning onto a corrupt board). Without it a panic
	// would leak inst.mu, and every later handleWorlds / GetOrCreateInstance
	// that waits on that lock (while holding the global server lock) wedges.
	var ok, evicted, boardFull bool
	for attempt := 0; ; attempt++ {
		ok, evicted = func() (bool, bool) {
			inst := pj.inst
			inst.mu.Lock()
			defer inst.mu.Unlock()
			if inst.evicted {
				// Evicted between the upgrade and this frame (M36.18). Joining
				// it would leave the player in an instance nobody ticks.
				return false, true
			}
			// M36.15: the board is resolved under the same lock as the join, since
			// it reads the world a restore replaces; resolved outside it, a restore
			// in between could leave the join naming a board of the old world.
			pj.join.Board = s.resolveJoinBoard(inst, pj.join.Board)
			if pj.challengeRun != nil {
				// The start board is the catalogue's, not the client's: a challenge
				// that could be entered on a board of the player's choosing is a
				// challenge whose route is the player's choosing too.
				pj.join.Board = pj.challengeRun.Def.Board
			}
			if inst.RoomManager.JoinPlayerWithID(pj.playerID, pj.join.Board, 0, 0) == 0 {
				// M36.23: the board has no room for another player stat.
				// Nobody was spawned, so nothing below has a player to name.
				boardFull = true
				return false, false
			}
			if pj.authenticated {
				inst.RoomManager.SetPlayerIdentity(pj.playerID, pj.account.ID, pj.account.DisplayName())
				if pj.hasStoredState {
					inst.RoomManager.ApplyPlayerState(pj.playerID, pj.storedState)
				}
			} else {
				inst.RoomManager.SetPlayerName(pj.playerID, pj.join.Name)
			}
			// Set before the snapshot is built, so the joining client's own
			// roster already carries it (M19.1).
			inst.RoomManager.SetPlayerColor(pj.playerID, pj.joinColor)
			if pj.authenticated {
				inst.RoomManager.SetPlayerProfileSummary(pj.playerID, pj.profileHandle, pj.hasProfile)
			}
			pj.client.playerID = pj.playerID
			inst.Clients[pj.playerID] = pj.client
			token := inst.mintResumeTokenLocked(pj.playerID)
			var snapOK bool
			pj.snapshot, snapOK = inst.RoomManager.Snapshot(pj.playerID)
			if snapOK {
				pj.snapshot.World = inst.publicWorldName()
				pj.snapshot.ResumeToken = token
				pj.client.boardID = pj.snapshot.BoardID
			}
			return snapOK, false
		}()
		if !evicted {
			break
		}
		// A challenge run's instance is minted per attempt and cannot be
		// re-resolved by name; an evicted one is simply gone.
		if pj.challengeRun != nil || attempt >= maxEvictedRejoins {
			return false
		}
		var err error
		if pj.inst, err = s.GetOrCreateInstance(pj.safeWorld); err != nil {
			return false
		}
	}

	if !ok {
		if boardFull {
			_ = pj.client.write(ctx, boardFullMessage())
		}
		s.removeClientFromInstance(pj.inst, pj.playerID)
		if pj.challengeRun != nil {
			// A run whose player never spawned measures nothing; take its
			// instance and its recording down rather than leaving an empty
			// attempt on the server.
			s.discardChallengeRun(pj.challengeRun.Key)
		}
		return false
	}
	return true
}

// seedSocialState puts a joining player's blocks live and marks the first
// frame with what this player has blocked, follows, and may do.
func (s *WebSocketServer) seedSocialState(pj *playerJoin) {
	// M21.1: a signed-in player's stored blocks go live before sendFirstFrame
	// replays the chat backlog. The other order would make the first act of a durable
	// block handing back the fifty lines it exists to suppress.
	if pj.authenticated {
		s.seedAccountBlocks(pj.playerID, pj.account.ID)
	}

	// M21.4: and the very first frame says which of the people already in the
	// room this player has blocked. It has to run AFTER the seeding above, which
	// is what puts a returning player's stored blocks into their live set — the
	// other order would compute the answer from an empty one and be right only
	// for a player who has never blocked anybody.
	pj.snapshot.BlockedPlayers = s.blockedInRoster(pj.playerID, pj.inst, pj.snapshot.Players)
	pj.snapshot.FollowedPlayers = s.followedInRoster(pj.playerID, pj.inst, pj.snapshot.Players)

	// M21.2: and whether this connection may moderate, which is what makes the
	// same window offer mute, kick and refuse. It is told once, at the join,
	// because the allowlist is deployment configuration and cannot change under a
	// running player.
	pj.snapshot.Operator = s.isOperator(pj.client.accountID)
}

// sendFirstFrame writes the join snapshot, counts the play, and replays the
// chat backlog. It reports false when the connection must end.
func (s *WebSocketServer) sendFirstFrame(ctx context.Context, pj *playerJoin) bool {
	client := pj.client
	if err := client.write(ctx, pj.snapshot); err != nil {
		// The connection never got its first frame; detach (or tidy up if the
		// player is already gone) exactly as a mid-game drop would.
		s.handleReadLoopExit(pj.inst, client, pj.playerID)
		return false
	}
	// M32.1: a challenge run is not a play of the source world. Counting it would
	// put the popularity shelf — and the front page built on it — under the
	// control of whoever cares to restart a challenge.
	if !pj.resumed && pj.challengeRun == nil && s.Activity != nil {
		if err := s.Activity.IncrementPlay(pj.safeWorld); err != nil {
			log.Printf("zztgo: failed to record play for %s: %v", pj.safeWorld, err)
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
				if s.chatBlocks.suppresses(pj.playerID, rec.PlayerID, rec.AccountID) {
					continue
				}
				msg := ChatMessage{
					Type:     MessageTypeChat,
					From:     rec.From,
					PlayerID: rec.PlayerID,
					Text:     rec.Text,
					History:  true,
				}
				_ = client.write(ctx, msg)
			}
		}
	}
	return true
}

// readLoop reads a joined player's messages until the connection ends or the
// player has no instance.
func (s *WebSocketServer) readLoop(ctx context.Context, conn *websocket.Conn, client *webSocketClient, playerID PlayerID) {
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
		s.handlePlayerMessage(ctx, client, activeInst, playerID, envelope.Type, raw)
	}
}

// handlePlayerMessage applies one message from a joined player. A message that
// does not decode is dropped.
func (s *WebSocketServer) handlePlayerMessage(ctx context.Context, client *webSocketClient, activeInst *WorldInstance, playerID PlayerID, messageType string, raw json.RawMessage) {
	switch messageType {
	case MessageTypeDebugCommand:
		var cmd DebugCommandMessage
		if err := json.Unmarshal(raw, &cmd); err != nil {
			return
		}
		s.submitDebugCommandInInstance(activeInst, playerID, cmd.Text)
	case MessageTypeScrollReply:
		var reply ScrollReplyMessage
		if err := json.Unmarshal(raw, &reply); err != nil {
			return
		}
		s.submitScrollReplyInInstance(activeInst, playerID, reply.StatID, reply.Label)
	case MessageTypeQuitReply:
		var reply QuitReplyMessage
		if err := json.Unmarshal(raw, &reply); err != nil {
			return
		}
		s.submitQuitReplyInInstance(activeInst, playerID, reply.Quit)
	case MessageTypeHighScoreName:
		var entry HighScoreNameMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			return
		}
		s.submitHighScoreNameInInstance(ctx, activeInst, playerID, entry.Name)
	case MessageTypeSaveFilename:
		var save SaveFilenameMessage
		if err := json.Unmarshal(raw, &save); err != nil {
			return
		}
		s.submitSaveFilenameInInstance(ctx, activeInst, playerID, save.Name)
	case MessageTypeChat:
		var chat ChatSubmitMessage
		if err := json.Unmarshal(raw, &chat); err != nil {
			return
		}
		text, ok := s.admitPlayerChatLikeText(ctx, client, playerID, chat.Text, false)
		if !ok {
			return
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
		var pm PrivateSubmitMessage
		if err := json.Unmarshal(raw, &pm); err != nil {
			return
		}
		s.submitPrivateMessage(ctx, client, playerID, pm)
	case MessageTypeFollow:
		var req FollowMessage
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}
		s.submitFollow(ctx, client, playerID, req)
	case MessageTypeBlock:
		var req BlockMessage
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}
		s.submitChatBlock(ctx, client, playerID, req)
	case MessageTypeModerate:
		var req ModerateMessage
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}
		s.submitModeration(ctx, client, playerID, req)
	case MessageTypeProfileRequest:
		var req ProfileRequestMessage
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}
		s.submitProfileRequest(ctx, client, req)
	default:
		var input InputMessage
		if err := json.Unmarshal(raw, &input); err != nil {
			return
		}
		activeInst.setInput(playerID, inputMessageToPlayerInput(input))
	}
}

// resolveJoinBoard is the board a join lands on when it names none: the world's
// own current board, then the server's configured default, then board 1. It is
// extracted verbatim from the player join (M22.1) so a watcher opens the board a
// player pressing Play would have opened — the two answers drifting apart is
// exactly how /watch/<world> would end up showing a room nobody is in.
//
// The caller holds inst.mu: it reads the world a restore replaces (M36.15).
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
			Title:   CP437String("New high score for " + rm.WorldName()),
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
		Title: CP437String("High scores for " + inst.RoomManager.WorldName()),
		Lines: inst.RoomManager.HighScoreLines(0, 0),
	}}
	inst.mu.Unlock()

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
// M36.17: a reply is accepted only if it answers a savePrompt this player was
// sent, and only saveRateLimitMax of them in a window. The world is copied under
// inst.mu, as Autosave's is, and written after the lock is released, so a save
// never stalls the room's tick on disk I/O.
func (s *WebSocketServer) submitSaveFilenameInInstance(ctx context.Context, inst *WorldInstance, playerID PlayerID, name string) {
	inst.mu.Lock()
	client := inst.Clients[playerID]
	if client == nil {
		inst.mu.Unlock()
		return
	}
	// The reply answers the prompt whatever happens next, so a refused or
	// cancelled save needs another S. An empty name is a cancel and says nothing.
	if !inst.RoomManager.TakeSavePrompt(playerID) {
		inst.mu.Unlock()
		if name != "" {
			_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
				Type:  "saveResult",
				Error: "no save prompt is open",
			}})
		}
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
	if !s.saveLimiter.allow(playerID, s.clockNow()) {
		inst.mu.Unlock()
		_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
			Type:  "saveResult",
			Error: "saving too often; try again in a minute",
		}})
		return
	}
	accountID, _, _ := inst.RoomManager.PlayerIdentity(playerID)
	state, hasState := inst.RoomManager.PlayerState(playerID)
	var stateCopy PlayerState
	if hasState {
		stateCopy = *state
	}
	job, err := inst.RoomManager.PrepareSnapshot(s.SavesDir, name, playerID)
	inst.mu.Unlock()

	var path string
	if err == nil {
		if hook := s.saveWriteHook; hook != nil {
			hook()
		}
		s.saveWriteMu.Lock()
		path, err = job.Write()
		s.saveWriteMu.Unlock()
	}
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

// SetFriendlyFire applies the deployment's projectile policy to this server and
// to every instance it has already made.
//
// Replay and challenge room managers are deliberately not reached: a replay's
// policy is whatever its recording header stamped (session_record.go), and
// changing it would make the playback disagree with the run it cites; a
// challenge run is a solo timed course with nobody to shoot.
func (s *WebSocketServer) SetFriendlyFire(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FriendlyFire = on
	if s.RoomManager != nil {
		s.RoomManager.SetFriendlyFire(on)
	}
	for _, name := range s.instanceNamesLocked() {
		if inst := s.Instances[name]; inst != nil && inst.RoomManager != nil {
			inst.RoomManager.SetFriendlyFire(on)
		}
	}
}

// instanceNamesLocked returns the instance keys in sorted order so any walk over
// them is deterministic. Callers must hold s.mu.
func (s *WebSocketServer) instanceNamesLocked() []string {
	names := make([]string, 0, len(s.Instances))
	for name := range s.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
	rm.FriendlyFire = s.FriendlyFire
	rm.HighScorePath = filepath.Join(dir, worldName+".HI")
	rm.LoadHighScores()
	s.restoreAutosaveOnCreateLocked(rm, worldName)

	// SourceWorld and the title screen stay the pristine world, as they do for
	// a world restored at boot.
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
	s.Instances[worldName] = inst
	s.attachRecorderLocked(inst)
	return inst, nil
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
// test-play copy exists un-marked.
func (s *WebSocketServer) hostGeneratedWorld(name string, world TWorld, private bool) error {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return err
	}
	// The superseded instance's recorder is closed after s.mu is released
	// (deferred first, so it runs last): Close waits for its writer to drain.
	var superseded *SessionRecorder
	defer func() { superseded.Close() }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Instances == nil {
		s.Instances = make(map[string]*WorldInstance)
	}
	if s.worldIsOccupiedLocked(safe) {
		return fmt.Errorf("generated world %q is already occupied", safe)
	}
	superseded = s.supersedeInstanceLocked(safe)
	rm := NewRoomManagerForWorld(world, safe)
	rm.FriendlyFire = s.FriendlyFire
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

// supersedeInstanceLocked retires whatever was hosted as name before a new
// world takes the name (M36.18). The old world's autosave is removed, because
// it is a save of a world that no longer exists: left in place, it would be
// loaded over the new one the next time the instance is created (after an
// eviction, or at boot). A test-play name never has one, which is fine. The
// old instance is marked evicted, so a join resolved to it before this
// re-resolves to the new one. The remove runs under s.mu; it is one unlink,
// like the read LoadPristineWorld already does under it. The old instance's
// recorder is detached and returned for the caller to close once s.mu is
// released; it used to be dropped open, leaking its fd (M36.19). Caller holds
// s.mu.
func (s *WebSocketServer) supersedeInstanceLocked(name string) *SessionRecorder {
	var recorder *SessionRecorder
	if old := s.Instances[name]; old != nil {
		old.mu.Lock()
		old.evicted = true
		if old.RoomManager != nil {
			recorder = old.RoomManager.recorder
			old.RoomManager.SetRecorder(nil)
		}
		old.mu.Unlock()
	}
	delete(s.autosavedThisBoot, name)
	dir := s.autosaveDir()
	if dir == "" {
		return recorder
	}
	path, err := snapshotPath(dir, name)
	if err != nil {
		return recorder
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("zztgo: stale autosave %q of a replaced world not removed: %v", name, err)
	}
	return recorder
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
	inst.RoomManager.SubmitDebugCommand(playerID, admitDebugCommandText(text))
}

func (s *WebSocketServer) submitScrollReplyInInstance(inst *WorldInstance, playerID PlayerID, objectStatID int16, label CP437String) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if _, ok := inst.Clients[playerID]; !ok {
		return
	}
	inst.RoomManager.SubmitScrollReply(playerID, objectStatID, string(label))
}

// clampInputDelta folds a wire delta to the -1..1 vanilla's keyboard can
// produce (M36.8). Every engine reader of InputDeltaX/Y indexes the tile at
// X+delta unchecked, so a raw {dx:200} panicked the room and {dx:5} walked
// through walls.
func clampInputDelta(d int16) int16 {
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	}
	return 0
}

func inputMessageToPlayerInput(input InputMessage) PlayerInput {
	playerInput := PlayerInput{
		DeltaX: clampInputDelta(input.DeltaX),
		DeltaY: clampInputDelta(input.DeltaY),
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
	if dest.RoomManager.JoinPlayerWithID(transit.PlayerID, board, 0, 0) == 0 {
		// M36.23: the far world's board is full, so nobody was spawned and no
		// object was taken. The traveller has already left the source world and
		// is left in neither, exactly as a failed snapshot below leaves them, but
		// they are told why. (Checking before the leave would mean holding both
		// instances' locks; see NOTES.md 2026-11-03.)
		dest.mu.Unlock()
		_ = client.write(ctx, boardFullMessage())
		return
	}
	if accountID != "" {
		// The name was admitted in the source world; it is CP437 now (M37.5).
		dest.RoomManager.setAdmittedPlayerIdentity(transit.PlayerID, accountID, name)
		if hasStoredState {
			dest.RoomManager.ApplyPlayerState(transit.PlayerID, storedState)
		}
	} else {
		dest.RoomManager.setAdmittedPlayerName(transit.PlayerID, name)
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

// boardFullMessage is the socket form of boardFullScroll, for a joiner who has
// no player yet and so no diff to carry it (M36.23).
func boardFullMessage() EventMessage {
	return EventMessage{Type: MessageTypeEvent, Event: ProtocolEvents([]Event{boardFullScroll()})[0]}
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

func (s *WebSocketServer) instanceForClient(client *webSocketClient) *WorldInstance {
	if client == nil {
		return nil
	}
	worldName := client.currentWorldName()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Instances[worldName]
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

	msg := AnnounceMessage{Type: MessageTypeAnnounce, Text: text, Seconds: seconds}

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
