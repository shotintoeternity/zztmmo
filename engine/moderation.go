package zztgo

// Operator actions (M21.2): mute, kick and refuse — the half of moderation that
// acts on a person on behalf of everybody, where M21.1's block acts on a person
// on behalf of one recipient.
//
// Four decisions shape this file, and each of them was a decision rather than an
// implementation detail:
//
//  1. **An operator is deployment configuration, not a role in the game.**
//     ZZT_MODERATOR_ACCOUNTS is an allowlist of account ids read from the
//     environment (the auth.go pattern), so operator status cannot be granted,
//     escalated or stolen from inside the game, and an unset or empty variable
//     means there are no operators at all — never "everybody" (owner decision
//     2026-08-05).
//  2. **Refusal binds to an accountID and to nothing else, and it is honest
//     about it.** A guest has no durable identity, so a refused guest returns by
//     reloading the page. The alternatives are IP-based (we hold no IPs today,
//     and holding them is its own decision) or account-only admission (a product
//     decision, not a moderation one). The limit is stated in the operator's own
//     UI and asserted by a test rather than discovered later.
//  3. **A refusal outlives the process; a mute does not.** Refusals are written
//     to their own JSON document (owner decision 2026-08-05) because the beta
//     host restarts routinely and a sanction that a reboot lifts is not a
//     sanction. A mute is the lighter, correctable sanction and is deliberately
//     process-scoped — see moderationMutes.
//  4. **Every action is audited.** An unlogged moderation power is one nobody can
//     review afterwards, so the audit is written before the operator is told the
//     action succeeded, and refusals of an action (a non-operator asking) are
//     recorded too — an attempt to moderate is exactly as interesting as an
//     action.
//
// All of this is service state: no lock here is ever held with inst.mu, nothing
// is recorded into a session recording, and nothing reaches StateHash.

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// The things an operator can ask for, in increasing severity. Mute has an
// inverse because it is the correctable sanction; kick needs none, since the
// kicked player returns by reconnecting; refuse is lifted by editing the store
// (see RefusalStore) rather than over the wire.
const (
	ModerationActionMute   = "mute"
	ModerationActionUnmute = "unmute"
	ModerationActionKick   = "kick"
	ModerationActionRefuse = "refuse"
)

// ModeratorAccountsEnv is the one place operator status comes from.
const ModeratorAccountsEnv = "ZZT_MODERATOR_ACCOUNTS"

// ModeratorAccountsFromEnv reads the allowlist. An unset or empty variable
// yields an empty set, which makes every operator action fail closed.
func ModeratorAccountsFromEnv() map[string]bool {
	return parseModeratorAccounts(os.Getenv(ModeratorAccountsEnv))
}

// parseModeratorAccounts splits an allowlist on commas and whitespace, so a
// deployment can write it either way, and drops empty entries — a trailing comma
// must not put "" in the set, where it would match the empty accountID every
// guest carries.
func parseModeratorAccounts(raw string) map[string]bool {
	accounts := make(map[string]bool)
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if id := strings.TrimSpace(field); id != "" {
			accounts[id] = true
		}
	}
	return accounts
}

// moderationMutes is who may not speak, keyed both ways for the same reason
// chatBlockSet is: a guest has no account, and an account may be reconnecting
// under a new connection id.
//
// It is deliberately NOT persisted. A mute is the correctable sanction — the one
// an operator reaches for first and lifts when the room settles — and a process
// that restarts is a natural expiry for it. Refusal is the durable one. A guest's
// mute is weaker still: their PlayerID is never minted again, so it ends with
// their connection, and the operator's UI says so.
type moderationMutes struct {
	mu       sync.Mutex
	accounts map[string]bool
	players  map[PlayerID]bool
}

// set records or lifts a mute and reports whether it will survive this player's
// connection — which is true exactly when there is an account to key it on.
func (m *moderationMutes) set(target PlayerID, account string, muted bool) (keyedToAccount bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.players == nil {
		m.players = make(map[PlayerID]bool)
		m.accounts = make(map[string]bool)
	}
	if muted {
		if target != 0 {
			m.players[target] = true
		}
		if account != "" {
			m.accounts[account] = true
		}
	} else {
		delete(m.players, target)
		delete(m.accounts, account)
	}
	return account != ""
}

// muted answers the question the chat handler asks on every line.
func (m *moderationMutes) muted(id PlayerID, account string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != 0 && m.players[id] {
		return true
	}
	return account != "" && m.accounts[account]
}

// forget drops a departed connection's mute. The account half stays, so a muted
// signed-in player who reconnects is still muted; a guest's id is never minted
// again, so keeping it would be a leak rather than a memory (chatBlocks.forget).
func (m *moderationMutes) forget(id PlayerID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.players, id)
}

// RefusedAccount is one standing refusal, and it carries who imposed it: a
// refusal read back a month later has to answer "who did this, and when", or it
// is a mystery rather than a record.
type RefusedAccount struct {
	Account string    `json:"account"`
	Name    string    `json:"name,omitempty"`
	By      string    `json:"by,omitempty"`
	ByName  string    `json:"byName,omitempty"`
	World   string    `json:"world,omitempty"`
	At      time.Time `json:"at"`
}

// RefusalStore is the durable half of moderation: the accounts that may not
// rejoin. It is its own JSON document rather than a field on AccountPreferences,
// because a refusal is not a preference of the refused player's — it is a server
// fact ABOUT them, and storing it in a document they can otherwise cause writes
// to would be putting the sanction inside the reach of the sanctioned.
//
// An empty path is a memory-only store: that is what tests get, and what a server
// started without a saves directory gets.
//
// There is deliberately no lift-from-inside-the-game: a refusal is addressed by
// accountID, an account id never reaches another player's browser (M21.1), and a
// refused account is by definition not connected to be picked from a roster. So
// a refusal is lifted the way operator status is granted — by editing deployment
// configuration (this document) and restarting. An operator console that can list
// and lift refusals without a restart is filed as M21.6 rather than improvised
// here.
type RefusalStore struct {
	mu       sync.Mutex
	path     string
	accounts map[string]RefusedAccount
}

// NewRefusalStore opens (and loads) the document. A missing file is an empty
// store, not an error; an unreadable or corrupt one is logged and treated as
// empty, because a server that will not start is a worse outcome than one whose
// refusals need re-imposing — and the file is the operator's own to inspect.
func NewRefusalStore(path string) *RefusalStore {
	store := &RefusalStore{path: path, accounts: make(map[string]RefusedAccount)}
	if path == "" {
		return store
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("zztgo: cannot read refusals from %s: %v", path, err)
		}
		return store
	}
	var loaded map[string]RefusedAccount
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("zztgo: ignoring unreadable refusals in %s: %v", path, err)
		return store
	}
	for id, rec := range loaded {
		if id != "" {
			store.accounts[id] = rec
		}
	}
	return store
}

// Refuses is the question the connect path asks of every authenticated account,
// before it joins anything. A guest arrives with an empty id and can never match:
// that is the honest limit, not an oversight.
func (s *RefusalStore) Refuses(accountID string) bool {
	if s == nil || accountID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Membership, not a field: the documented way to lift a refusal today is to
	// hand-edit this document, and a hand-written entry that omits the redundant
	// "account" field must still refuse rather than silently readmit.
	_, refused := s.accounts[accountID]
	return refused
}

// Refuse adds a standing refusal and writes the document. A write failure is
// reported to the caller, which must not tell the operator the refusal held: an
// operator who believes somebody is gone, when a restart will bring them back, is
// worse off than one who is told to try again.
func (s *RefusalStore) Refuse(rec RefusedAccount) error {
	if s == nil || rec.Account == "" {
		return ErrNoAccountID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts == nil {
		s.accounts = make(map[string]RefusedAccount)
	}
	s.accounts[rec.Account] = rec
	return s.writeLocked()
}

// writeLocked persists through a temporary file and a rename, the shape
// FileChatDatabase already uses: a half-written refusals document that loaded as
// empty would silently readmit everyone.
func (s *RefusalStore) writeLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.accounts, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ModerationAuditEntry is one line of the record: who, whom, which action, which
// world, and the tick.
//
// Result is part of it because a refused attempt is worth as much as a completed
// one: "who tried to moderate and was told no" is the question an audit gets read
// for after an incident.
type ModerationAuditEntry struct {
	At            time.Time `json:"at"`
	Action        string    `json:"action"`
	Result        string    `json:"result"`
	Operator      string    `json:"operator,omitempty"`
	OperatorID    PlayerID  `json:"operatorId,omitempty"`
	OperatorName  string    `json:"operatorName,omitempty"`
	Target        PlayerID  `json:"target,omitempty"`
	TargetAccount string    `json:"targetAccount,omitempty"`
	TargetName    string    `json:"targetName,omitempty"`
	World         string    `json:"world,omitempty"`
	Tick          int16     `json:"tick"`
	Detail        string    `json:"detail,omitempty"`
}

// The results an entry can carry.
const (
	ModerationResultApplied = "applied"
	ModerationResultDenied  = "denied"
	ModerationResultNoOp    = "noop"
	ModerationResultFailed  = "failed"
)

// moderationAuditTail is how many entries are kept in memory. The FILE is the
// record; this is a tail so a test — and, later, an operator-facing view — can
// read the last actions without parsing the document, and it is bounded so a
// client hammering the wire cannot grow the server without limit.
const moderationAuditTail = 256

// ModerationAudit appends one JSON line per action. An empty path keeps the tail
// only, which is what tests get; a configured path is append-only, because an
// audit an operator can rewrite in place is not an audit.
type ModerationAudit struct {
	mu      sync.Mutex
	path    string
	entries []ModerationAuditEntry
}

func NewModerationAudit(path string) *ModerationAudit {
	return &ModerationAudit{path: path}
}

// Record writes the entry. A file that cannot be appended to is logged and the
// entry is still kept in the tail and in the process log: losing the audit must
// never be the reason a moderation action does not happen, but it must never be
// silent either.
func (a *ModerationAudit) Record(entry ModerationAuditEntry) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.entries = append(a.entries, entry)
	if len(a.entries) > moderationAuditTail {
		a.entries = a.entries[len(a.entries)-moderationAuditTail:]
	}
	path := a.path
	a.mu.Unlock()

	log.Printf("zztgo: moderation %s %s operator=%q target=%d/%q world=%q tick=%d %s",
		entry.Action, entry.Result, entry.Operator, entry.Target, entry.TargetAccount,
		entry.World, entry.Tick, entry.Detail)

	if path == "" {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("zztgo: cannot encode moderation audit entry: %v", err)
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("zztgo: cannot open moderation audit %s: %v", path, err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		log.Printf("zztgo: cannot append to moderation audit %s: %v", path, err)
	}
}

// Entries returns the in-memory tail, oldest first.
func (a *ModerationAudit) Entries() []ModerationAuditEntry {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ModerationAuditEntry, len(a.entries))
	copy(out, a.entries)
	return out
}
