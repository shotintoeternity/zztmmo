package zztgo

import (
	"context"
	"encoding/json"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

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
	inst.mu.Lock()
	for attempt := 0; inst.evicted; attempt++ {
		// Evicted between the upgrade and this frame (M36.18): watch the live
		// instance, not one nobody ticks.
		inst.mu.Unlock()
		if attempt >= maxEvictedRejoins {
			return
		}
		var err error
		if inst, err = s.GetOrCreateInstance(inst.Name); err != nil {
			return
		}
		client.setWorldName(inst.Name)
		inst.mu.Lock()
	}
	// M36.15: resolved under inst.mu, because it reads the world a restore
	// replaces.
	boardID := s.resolveWatchBoard(inst, join.Board)
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
