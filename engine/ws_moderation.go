package zztgo

import (
	"context"
	"log"
	"strings"
)

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
			Text:     CP437String(text),
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
	audit.OperatorName = CP437String(operatorName)

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
	audit.TargetName = CP437String(target.name)
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
		Name:     CP437String(target.name),
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
		result.Text = CP437String("Muted " + name + ".")
		if !result.Durable {
			result.Text = CP437String("Muted " + name + " for this session.")
		}

	case ModerationActionUnmute:
		result.Durable = s.mutes.set(req.PlayerID, target.account, false)
		s.tellPlayer(ctx, target.client, ModerationActionUnmute,
			"A moderator has unmuted you.", false)
		result.Text = CP437String("Unmuted " + name + ".")

	case ModerationActionKick:
		s.tellPlayer(ctx, target.client, ModerationActionKick,
			"A moderator has removed you from the game.", true)
		s.endConnection(target.client)
		result.Text = CP437String("Kicked " + name + ".")

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
			result.Text = CP437String("Kicked " + name + ": a guest cannot be refused.")
			break
		}
		err := s.Refusals.Refuse(RefusedAccount{
			Account: target.account,
			Name:    CP437String(target.name),
			By:      operatorAccount,
			ByName:  CP437String(operatorName),
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
				Name:     CP437String(target.name),
				Applied:  false,
				Text:     "Could not record the refusal.",
			})
			return
		}
		s.tellPlayer(ctx, target.client, ModerationActionRefuse,
			"A moderator has refused this account.", true)
		s.endConnection(target.client)
		result.Durable = true
		result.Text = CP437String("Refused " + name + ". They cannot rejoin.")
	}

	audit.Result = ModerationResultApplied
	s.Audit.Record(audit)
	_ = client.write(ctx, result)
}
