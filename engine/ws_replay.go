package zztgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Replays are bounded the way hosted worlds are (M36.19). A replay instance
// nobody watches is dropped after DefaultReplayEvictIdleTicks; at most
// maxReplayInstances exist at once, because the id@start key is client input;
// and a restart, which reopens and re-steps the whole recording, is honoured
// at most once per replayRestartCooldownTicks of that replay.
const (
	DefaultReplayEvictIdleTicks = 273 // about thirty seconds at 110ms/tick
	maxReplayInstances          = 16
	replayRestartCooldownTicks  = 27 // about three seconds
)

var (
	errReplayBusy           = errors.New("too many replays are being watched; try again shortly")
	errReplayRestartTooSoon = errors.New("replay restarted too recently")
	errReplayEvicted        = errors.New("replay was closed")
)

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
	// ticks counts this replay's server ticks, paused or not; it is the clock
	// the restart cooldown is measured on. restarts counts honoured restarts,
	// and lastRestart is the tick the latest one was taken at (M36.19).
	ticks       int
	restarts    int
	lastRestart int
	// idleTicks is how many consecutive ticks nobody has watched; evicted is
	// set when the replay is dropped from the table, after which no watcher
	// may register on it (M36.19).
	idleTicks int
	evicted   bool
	mu        sync.Mutex
}

func (replay *ReplayInstance) Tick(ctx context.Context) {
	replay.mu.Lock()
	replay.ticks++
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

// evictIdleReplays drops replay instances nobody has watched for
// ReplayEvictIdleTicks ticks and closes their files outside s.mu, as
// evictIdleInstances does for worlds (M36.19).
func (s *WebSocketServer) evictIdleReplays() {
	if s.ReplayEvictIdleTicks <= 0 {
		return
	}
	var evicted []*ReplayInstance
	s.mu.Lock()
	for key, replay := range s.ReplayInstances {
		if replay.advanceIdleEviction(s.ReplayEvictIdleTicks) {
			delete(s.ReplayInstances, key)
			evicted = append(evicted, replay)
		}
	}
	s.mu.Unlock()
	for _, replay := range evicted {
		replay.Close()
	}
}

// advanceIdleEviction counts one tick toward eviction and, when the replay has
// been unwatched long enough, marks it evicted and reports true.
func (replay *ReplayInstance) advanceIdleEviction(limit int) bool {
	replay.mu.Lock()
	defer replay.mu.Unlock()
	if len(replay.Spectators) != 0 {
		replay.idleTicks = 0
		return false
	}
	replay.idleTicks++
	if replay.idleTicks < limit {
		return false
	}
	replay.evicted = true
	return true
}

// evict marks the replay closed to new watchers and closes its file. The
// caller has already removed it from s.ReplayInstances.
func (replay *ReplayInstance) evict() {
	replay.mu.Lock()
	replay.evicted = true
	replay.mu.Unlock()
	replay.Close()
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
	// The cooldown is taken before the reopen, so two restarts racing each
	// other cannot both pass it (M36.19).
	replay.mu.Lock()
	if replay.restarts > 0 && replay.ticks-replay.lastRestart < replayRestartCooldownTicks {
		replay.mu.Unlock()
		return errReplayRestartTooSoon
	}
	replay.restarts++
	replay.lastRestart = replay.ticks
	replay.mu.Unlock()

	playback, f, err := newReplayPlaybackFromPath(replay.path, replay.startTick)
	if err != nil {
		return err
	}

	replay.mu.Lock()
	if replay.evicted {
		replay.mu.Unlock()
		_ = f.Close()
		return errReplayEvicted
	}
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

	replay, sub, snapshot, err := s.watchReplay(id, startTick, client, join.Board)
	if err != nil {
		_ = client.write(ctx, ReplayErrorMessage{Type: MessageTypeReplayError, Text: "Replay unavailable: " + err.Error()})
		return
	}
	s.serveReplaySpectator(ctx, conn, client, replay, sub, snapshot)
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
	return joinSanitized(dir, safe, ".jsonl")
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
	// The cap is checked before the recording is opened and stepped, so a
	// flood of distinct starts costs nothing once the table is full (M36.19).
	if len(s.ReplayInstances) >= maxReplayInstances {
		s.mu.Unlock()
		return nil, errReplayBusy
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
	// start is client input with no upper bound. Every start past the end of
	// the recording opens the same final frame, so it is snapped to the tick
	// after the last one and keyed by that, which bounds the distinct keys a
	// recording has by its length (M36.19).
	if playback.LastTick() < startTick-1 {
		startTick = playback.LastTick() + 1
		key = replayInstanceKey(safe, startTick)
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
	if len(s.ReplayInstances) >= maxReplayInstances {
		replay.Close()
		return nil, errReplayBusy
	}
	s.ReplayInstances[key] = replay
	return replay, nil
}

// watchReplay resolves the replay a watcher asked for and registers it there.
// A replay can be evicted between being resolved and the watcher registering;
// that one is refused and the lookup repeated, so the watcher always lands on
// a replay the server still ticks (M36.19, as M36.18 did for worlds).
func (s *WebSocketServer) watchReplay(id string, startTick int, client *webSocketClient, board int16) (*ReplayInstance, *spectator, SnapshotMessage, error) {
	for attempt := 0; attempt < 3; attempt++ {
		replay, err := s.GetOrCreateReplayInstanceAt(id, startTick)
		if err != nil {
			return nil, nil, SnapshotMessage{}, err
		}
		sub := &spectator{client: client, boardID: s.resolveReplayWatchBoard(replay, board)}
		if snapshot, ok := replay.replaySpectatorSnapshot(sub); ok {
			return replay, sub, snapshot, nil
		}
	}
	return nil, nil, SnapshotMessage{}, errReplayEvicted
}

// resolveReplayWatchBoard is resolveWatchBoard for a recording: the board a
// replay watcher asked for, range-checked against the recorded world, because
// SpectatorSnapshot opens it directly and an id off the table would index it
// out of bounds (M36.7).
func (s *WebSocketServer) resolveReplayWatchBoard(replay *ReplayInstance, board int16) int16 {
	fallback := s.DefaultBoard
	if fallback == 0 {
		fallback = 1
	}
	if board == 0 {
		board = fallback
	}
	replay.mu.Lock()
	count := replay.playback.RoomManager().FrozenWorld().BoardCount
	replay.mu.Unlock()
	if board < 0 || board > count {
		board = fallback
	}
	if board < 0 || board > count {
		board = 0
	}
	return board
}

// replaySpectatorSnapshot registers sub's view of its board and returns
// the frame to send. The unlock is deferred: a panic here must not leave
// replay.mu held, since every WebSocketServer.Tick takes it (M36.7). It
// reports false, registering nothing, on a replay already evicted (M36.19).
func (replay *ReplayInstance) replaySpectatorSnapshot(sub *spectator) (SnapshotMessage, bool) {
	replay.mu.Lock()
	defer replay.mu.Unlock()
	if replay.evicted {
		return SnapshotMessage{}, false
	}
	replay.Spectators[sub.client] = sub
	snapshot, engine := replay.playback.RoomManager().SpectatorSnapshot(sub.boardID)
	sub.engine = engine
	snapshot.Watchers = replay.watcherCountsLocked()[sub.boardID]
	sub.sentWatchers = snapshot.Watchers
	return snapshot, true
}

func (s *WebSocketServer) serveReplaySpectator(ctx context.Context, conn *websocket.Conn, client *webSocketClient, replay *ReplayInstance, sub *spectator, snapshot SnapshotMessage) {

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
			if err := replay.Restart(); errors.Is(err, errReplayRestartTooSoon) {
				// A restart inside the cooldown is dropped, not fatal: the
				// replay plays on (M36.19).
				continue
			} else if err != nil {
				_ = client.write(ctx, ReplayErrorMessage{Type: MessageTypeReplayError, Text: "Replay restart failed: " + err.Error()})
				return
			}
			snapshot, ok := replay.replaySpectatorSnapshot(sub)
			if !ok {
				_ = client.write(ctx, ReplayErrorMessage{Type: MessageTypeReplayError, Text: "Replay unavailable: " + errReplayEvicted.Error()})
				return
			}
			if err := client.write(ctx, snapshot); err != nil {
				return
			}
		}
	}
}
