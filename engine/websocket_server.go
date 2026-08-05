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

	// RecordDir, when non-empty, is where per-instance session recordings are
	// written (M14.2). Set it through EnableRecording, which also stamps
	// recordStamp once so a restart does not clobber a prior run's files.
	RecordDir   string
	recordStamp string

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

	mu sync.Mutex
	// nextPlayerID mints process-unique PlayerIDs across every instance, so ids
	// never collide between hosted worlds (M14.1). Guarded by mu.
	nextPlayerID        PlayerID
	Instances           map[string]*WorldInstance
	DefaultInstance     *WorldInstance
	EditorSessions      map[*webSocketClient]*EditorSession
	EditorWorldSessions map[string]*EditorSession
	ChatDB              ChatDatabase
	Auth                *AuthService
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
	mu             sync.Mutex
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
	rm := NewRoomManager(world)
	name := rm.WorldName()
	if name == "Untitled" || name == "" {
		name = "TOWN"
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
	}
	s := &WebSocketServer{
		RoomManager:         rm,
		DefaultBoard:        defaultBoard,
		TickDuration:        ServerTickDuration,
		OriginHosts:         []string{"localhost:*", "127.0.0.1:*"},
		Instances:           make(map[string]*WorldInstance),
		EditorSessions:      make(map[*webSocketClient]*EditorSession),
		EditorWorldSessions: make(map[string]*EditorSession),
		ChatDB:              NewMemChatDatabase(),
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
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

func (s *WebSocketServer) Tick(ctx context.Context) {
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
	var titles []*TitleSim
	for _, inst := range s.Instances {
		if inst.Title != nil {
			titles = append(titles, inst.Title)
		}
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.Tick(ctx, s)
	}
	for _, title := range titles {
		title.Tick()
	}

	s.maybeAutosave()
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

func (inst *WorldInstance) Tick(ctx context.Context, s *WebSocketServer) {
	inst.mu.Lock()
	inputs := inst.Inputs
	inst.Inputs = make(map[PlayerID]PlayerInput)
	diffs := safeStepDiffs(inst.Name, inst.RoomManager, inputs)
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
				client.boardID = snapshot.BoardID
				snapshot.Events = append(snapshot.Events, ProtocolEvents(inst.RoomManager.DrainPlayerEvents(playerID))...)
				messages[playerID] = BoardChangeMessage{Type: MessageTypeBoardChange, Snapshot: snapshot}
				continue
			}
		}
		client.boardID = diff.BoardID
		diff.Events = append(diff.Events, ProtocolEvents(inst.RoomManager.DrainPlayerEvents(playerID))...)
		messages[playerID] = diff
	}
	for _, quit := range inst.RoomManager.DrainQuits() {
		if inst.Clients[quit.PlayerID] == nil {
			continue
		}
		messages[quit.PlayerID] = s.quitOutcome(inst.RoomManager, quit)
	}
	inst.mu.Unlock()

	for playerID, message := range messages {
		client := clients[playerID]
		if client != nil {
			_ = client.write(ctx, message)
		}
	}
}

// RoomManager.StepDiffs isolates simulation panics per room.  This outer guard
// keeps a future panic in room routing or diff construction from escaping the
// instance tick goroutine and taking down other hosted worlds.
func safeStepDiffs(worldName string, rm *RoomManager, inputs map[PlayerID]PlayerInput) (diffs map[PlayerID]DiffMessage) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("zztgo: isolating world %q after tick panic: %v", worldName, recovered)
			diffs = make(map[PlayerID]DiffMessage)
		}
	}()
	return rm.StepDiffs(inputs)
}

func (s *WebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	worldName := r.URL.Query().Get("world")
	var inst *WorldInstance
	var safeWorld string
	if worldName == "" {
		inst = s.DefaultInstance
		safeWorld = inst.Name
	} else {
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
			inst.mu.Unlock()
		}
	}

	if !resumed {
		if join.Board == 0 {
			join.Board = inst.RoomManager.FrozenWorld().Info.CurrentBoard
			if join.Board == 0 {
				join.Board = s.DefaultBoard
			}
			if join.Board == 0 {
				join.Board = 1
			}
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
			client.playerID = playerID
			inst.Clients[playerID] = client
			token := inst.mintResumeTokenLocked(playerID)
			var snapOK bool
			snapshot, snapOK = inst.RoomManager.Snapshot(playerID)
			if snapOK {
				snapshot.ResumeToken = token
				client.boardID = snapshot.BoardID
			}
			return snapOK
		}()

		if !ok {
			s.removeClientFromInstance(inst, playerID)
			return
		}
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

	if err := client.write(ctx, snapshot); err != nil {
		// The connection never got its first frame; detach (or tidy up if the
		// player is already gone) exactly as a mid-game drop would.
		s.handleReadLoopExit(inst, client, playerID)
		return
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
			s.submitDebugCommandInInstance(inst, playerID, cmd.Text)
		case MessageTypeScrollReply:
			var reply ScrollReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitScrollReplyInInstance(inst, playerID, reply.StatID, reply.Label)
		case MessageTypeQuitReply:
			var reply QuitReplyMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				continue
			}
			s.submitQuitReplyInInstance(inst, playerID, reply.Quit)
		case MessageTypeHighScoreName:
			var entry HighScoreNameMessage
			if err := json.Unmarshal(raw, &entry); err != nil {
				continue
			}
			s.submitHighScoreNameInInstance(ctx, inst, playerID, entry.Name)
		case MessageTypeSaveFilename:
			var save SaveFilenameMessage
			if err := json.Unmarshal(raw, &save); err != nil {
				continue
			}
			s.submitSaveFilenameInInstance(ctx, inst, playerID, save.Name)
		case "chat":
			var chat struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &chat); err != nil {
				continue
			}
			// Admission before any persistence or broadcast (M16.16a): a
			// refused message — unprintable-only text or the sixth in a
			// rolling ten-second window — creates no record and no broadcast.
			text, ok := admitChatText(chat.Text)
			if !ok {
				continue
			}
			if !s.chatLimiter.allow(playerID, s.clockNow()) {
				continue
			}
			inst.mu.Lock()
			name := "browser"
			player := inst.RoomManager.players[playerID]
			if player != nil && player.name != "" {
				name = player.name
			}
			inst.mu.Unlock()
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
		case MessageTypeBlock:
			var req BlockMessage
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			s.submitChatBlock(ctx, client, playerID, req)
		default:
			var input InputMessage
			if err := json.Unmarshal(raw, &input); err != nil {
				continue
			}
			inst.setInput(playerID, inputMessageToPlayerInput(input))
		}
	}
	s.handleReadLoopExit(inst, client, playerID)
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
	if displaced != nil && displaced != client {
		displaced.conn.Close(websocket.StatusNormalClosure, "editor resumed on a new connection")
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
		world := inst.RoomManager.snapshotWorldNoSaver()
		inst.mu.Unlock()
		jobs = append(jobs, autosaveJob{name: name, world: world})
	}

	for _, job := range jobs {
		if _, err := writeWorldSnapshot(dir, job.name, job.world); err != nil {
			log.Printf("zztgo: autosave of %q failed: %v", job.name, err)
		}
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
	header, _, err := newSessionHeader(inst.Name, inst.RoomManager.FrozenWorld())
	if err != nil {
		log.Printf("zztgo: session recording disabled for %q: %v", inst.Name, err)
		return
	}
	path := filepath.Join(s.RecordDir, inst.Name+"-"+s.recordStamp+".jsonl")
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

	rm := NewRoomManager(world)
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
	rm := NewRoomManager(world)
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
		if err := s.HostGeneratedWorld(name, world); err != nil {
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
		snapshot.ResumeToken = token
		client.boardID = snapshot.BoardID
	}
	inst.mu.Unlock()

	if old != nil && old != client {
		old.conn.Close(websocket.StatusNormalClosure, "resumed on a new connection")
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

// identifyPlayer answers who a PlayerID belongs to, across every hosted world:
// global chat is server-wide, so the person a player wants to blocked may not be
// in their room, or even in their world.
func (s *WebSocketServer) identifyPlayer(playerID PlayerID) (accountID, name string, found bool) {
	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()

	for _, inst := range instances {
		inst.mu.Lock()
		account, playerName, ok := inst.RoomManager.PlayerIdentity(playerID)
		inst.mu.Unlock()
		if ok {
			return account, playerName, true
		}
	}
	return "", "", false
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
