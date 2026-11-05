package zztgo

import (
	"crypto/rand"
	"encoding/hex"
	"log"
)

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
		// No token rather than a predictable one: a resume token is the only
		// thing that hands a reconnecting socket its player, so a guessable one
		// lets anyone take that player over. Without one the player just can't
		// resume; tryResume treats the empty token as unknown.
		log.Printf("zztgo: resume token entropy unavailable: %v", err)
		return ""
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
	if s.autosaveEnabled() && inst.Challenge == nil && !inst.Private {
		// Whatever they did since the last tick is saved too (M36.18).
		inst.unsaved = true
	}
	inst.mu.Unlock()
}
