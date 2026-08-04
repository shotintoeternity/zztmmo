package zztgo

// Chat blocks (M21.1): one player asking not to hear another.
//
// Three properties shape everything here, and each of them is a decision rather
// than an implementation detail:
//
//  1. **Filtered at fan-out, on the server.** A client-side filter is bypassable
//     and, worse, still delivers the text to the machine of the person who asked
//     not to receive it. So the block lives beside the broadcast
//     (BroadcastGlobalChat) and the line is never written to that socket.
//  2. **Per-recipient.** A block changes what ONE person receives and nothing
//     else. It is not a mute: it never affects what anyone else sees, and the
//     blocked player is never told — a block that announces itself invites the
//     retaliation it exists to prevent.
//  3. **Durability follows identity.** A signed-in player's block is keyed on the
//     target's accountID and stored in the M19.3 preferences store, so it
//     survives a restart and a different browser. A guest has no durable id to
//     key on, so their block is keyed on the target's PlayerID and lasts the
//     session — and the UI has to say so rather than silently forgetting it.
//
// This is service state, not simulation state: no lock here is ever held with
// inst.mu, nothing recorded, nothing hashed.

import "sync"

// chatBlockSet is one blocker's live view: the connection ids and the accounts
// they will not hear from. Both are consulted on every line, because a target
// can be addressable by one and not the other — a guest has no account, and an
// account that is currently offline has no connection id.
type chatBlockSet struct {
	players  map[PlayerID]bool
	accounts map[string]bool
}

// chatBlocks holds every connected player's block set, keyed by the BLOCKER's
// PlayerID: the recipient is the connection, so this is the one key every
// blocker has, signed in or not. It has its own lock, like chatRateLimiter.
type chatBlocks struct {
	mu       sync.Mutex
	blockers map[PlayerID]*chatBlockSet
}

func (b *chatBlocks) setLocked(blocker PlayerID) *chatBlockSet {
	if b.blockers == nil {
		b.blockers = make(map[PlayerID]*chatBlockSet)
	}
	set := b.blockers[blocker]
	if set == nil {
		set = &chatBlockSet{players: map[PlayerID]bool{}, accounts: map[string]bool{}}
		b.blockers[blocker] = set
	}
	return set
}

// seedAccounts loads a signed-in player's stored blocks into their live set at
// join. Additive: it never clears a block made earlier in this session, because
// a reconnect must not quietly hand somebody back their audience.
func (b *chatBlocks) seedAccounts(blocker PlayerID, accounts []string) {
	if len(accounts) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.setLocked(blocker)
	for _, account := range accounts {
		if account != "" {
			set.accounts[account] = true
		}
	}
}

// set records (or lifts) one block. The target's PlayerID is always keyed on, so
// a block takes effect immediately for the connection in front of the player;
// the accountID is keyed on as well when there is one, which is what outlives
// the connection. It reports whether the block is durable — the caller has to
// tell the blocker, since a guest target cannot be blocked past this session.
func (b *chatBlocks) set(blocker, target PlayerID, targetAccount string, blocked bool) (durable bool) {
	if target == 0 || target == blocker {
		// Nothing addressable, or the player themselves. Refused at the write
		// rather than tolerated and skipped at the read: a set holding id 0 would
		// suppress every history line whose id the loader cleared, and one
		// holding the blocker's own account would persist it into their stored
		// blocks — where it would outlive the mistake.
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.setLocked(blocker)
	if blocked {
		set.players[target] = true
		if targetAccount != "" {
			set.accounts[targetAccount] = true
		}
	} else {
		delete(set.players, target)
		if targetAccount != "" {
			delete(set.accounts, targetAccount)
		}
	}
	return targetAccount != ""
}

// suppresses answers the only question the fan-out asks: may this recipient be
// sent this line? A zero sender id and an empty sender account are both
// unaddressable and can never be suppressed — which is deliberate, because that
// is the shape of a server announcement, and a shutdown warning is not chat.
func (b *chatBlocks) suppresses(recipient, sender PlayerID, senderAccount string) bool {
	if recipient == sender {
		return false // nobody blocks themselves out of their own chat window
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.blockers[recipient]
	if set == nil {
		return false
	}
	if sender != 0 && set.players[sender] {
		return true
	}
	return senderAccount != "" && set.accounts[senderAccount]
}

// blockedAccounts is what gets persisted for a signed-in blocker: the durable
// half of their set, and only that half. Sorted by the caller if order matters;
// nothing here depends on it.
func (b *chatBlocks) blockedAccounts(blocker PlayerID) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.blockers[blocker]
	if set == nil {
		return nil
	}
	accounts := make([]string, 0, len(set.accounts))
	for account := range set.accounts {
		accounts = append(accounts, account)
	}
	return accounts
}

// forget drops a departed player's set. Their PlayerID is never minted again in
// this process, so keeping it would be a leak rather than a memory — and a
// guest's block was only ever promised for the session.
func (b *chatBlocks) forget(blocker PlayerID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.blockers, blocker)
}
