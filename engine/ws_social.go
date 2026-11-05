package zztgo

import (
	"context"
	"log"
	"sort"
	"strings"
)

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

	msg := ChatMessage{
		Type:     MessageTypeChat,
		From:     CP437String(author.Name),
		PlayerID: author.PlayerID,
		Text:     CP437String(text),
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

func (s *WebSocketServer) submitPrivateMessage(ctx context.Context, client *webSocketClient, sender PlayerID, req PrivateSubmitMessage) {
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
		From:   CP437String(senderName),
		FromID: sender,
		To:     CP437String(targetName),
		ToID:   req.PlayerID,
		Text:   CP437String(text),
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
		Name:     CP437String(targetName),
		Blocked:  req.Blocked,
		Durable:  durable,
		Text:     CP437String(text),
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
			Name:     CP437String(target.name),
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
			Name:     CP437String(target.name),
			Lines:    []string{"", "  Profile unavailable.", ""},
		})
		return
	}
	lines := PublicProfileLines(target.name, prefs.Profile, ok && accountProfileHasPublicFields(prefs.Profile))
	_ = client.write(ctx, ProfileResultMessage{
		Type:     MessageTypeProfileResult,
		PlayerID: req.PlayerID,
		Name:     CP437String(target.name),
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
		result.Text = CP437String(text)
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
		result.Name = CP437String(target.name)
		reply("Guests cannot be followed.")
		return
	}
	if target.account == client.accountID {
		result.Name = CP437String(target.name)
		reply("You cannot follow yourself.")
		return
	}

	// One step (M36.27): see UpdateAccountPreferences.
	err := s.ChatDB.UpdateAccountPreferences(client.accountID, func(prefs *AccountPreferences, _ bool) error {
		if req.Follow {
			prefs.FollowedAccounts = addAccountID(prefs.FollowedAccounts, target.account)
		} else {
			prefs.FollowedAccounts = removeAccountID(prefs.FollowedAccounts, target.account)
		}
		return nil
	})
	if err != nil {
		log.Printf("zztgo: failed to store follows for account %q: %v", client.accountID, err)
		reply("Could not update follows.")
		return
	}
	result.Name = CP437String(target.name)
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
		out[loc.world] = append(out[loc.world], FriendPresenceSummary{Name: CP437String(name), Handle: targetPrefs.Profile.Handle})
	}
	for world := range out {
		sort.SliceStable(out[world], func(i, j int) bool {
			a := out[world][i]
			b := out[world][j]
			if a.Handle != "" || b.Handle != "" {
				return a.Handle < b.Handle
			}
			return strings.ToUpper(string(a.Name)) < strings.ToUpper(string(b.Name))
		})
	}
	return out
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
	// One step, not a read then a write (M36.27): the whole document written
	// back after a Get lost anything stored in between, such as a colour.
	blocked := s.chatBlocks.blockedAccounts(blocker)
	sort.Strings(blocked)
	err := s.ChatDB.UpdateAccountPreferences(accountID, func(prefs *AccountPreferences, _ bool) error {
		prefs.BlockedAccounts = blocked
		return nil
	})
	if err != nil {
		log.Printf("zztgo: failed to store blocks for account %q: %v", accountID, err)
	}
}
