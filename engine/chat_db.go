package zztgo

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChatAuthor is who said a line, in the three names a chat line can be
// addressed by (M21.1). Before this, a line carried a display name and nothing
// else — and display names are neither unique nor claimed, so nothing
// addressable travelled with a chat line at all.
//
// Which of the two ids is usable depends on how long you need it for:
//
//   - PlayerID is unique among everyone connected, and only for as long as the
//     process lives (mintPlayerID is an in-memory counter). It is what a guest's
//     block keys on, and it is the id that rides the wire — an account id must
//     never reach another player's browser.
//   - AccountID is durable and exists only for a signed-in player. It is what a
//     signed-in blocker's block keys on, so their block survives a restart and a
//     different browser.
type ChatAuthor struct {
	Name      string
	PlayerID  PlayerID
	AccountID string
}

type ChatRecord struct {
	From      string    `json:"from"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	// PlayerID is the author's connection id (M21.1), so a replayed history line
	// can be suppressed for a recipient who blocked that player. It is
	// process-scoped: the loader below deliberately CLEARS it on records read
	// from disk, because a later process re-mints the same numbers for different
	// people, and a stale id would suppress an innocent line.
	PlayerID PlayerID `json:"playerId,omitempty"`
	// AccountID is the author's durable id when they were signed in, and is the
	// only thing that can filter history across a restart. It is persisted and
	// never sent to a client: a block is per-recipient and silent, and another
	// player's account id is not theirs to see.
	AccountID string `json:"accountId,omitempty"`
}

// AccountPreferences is what one signed-in player has chosen, once, for every
// world they play (M19.3). It is deliberately a struct with named fields rather
// than a map[string]string: the next preference — a profile blurb, a pronoun, a
// default nickname — is a field with a type and an edge validation, not a
// convention two callers have to agree on out of band. Adding one is a line
// here plus a line where it is validated; a document written before the field
// existed loads with its zero value, and a document written after it exists
// still loads into a reader that predates it.
//
// PlayerState is the other per-account store and is keyed by (account, world),
// which is right for an inventory and wrong for everything here: a color is a
// property of the player, not of their run in TOWN.
type AccountPreferences struct {
	// Color is the "#RRGGBB" the player's ☻ is drawn on (M19.1), already
	// through SanitizePlayerColor before it is stored. An empty Color on a
	// document that EXISTS is not "unset": it is the picker's "No color" row,
	// the way back to the vanilla white-on-blue player, and it must beat a
	// stale localStorage pick exactly as a non-empty color does.
	Color string `json:"color,omitempty"`
	// BlockedAccounts are the accounts whose chat this player has asked not to
	// receive (M21.1) — the second caller this store was shaped for, and a field
	// rather than a new store. Only durable identities can live here: a block on
	// a guest has no id to key on and stays in memory for the session.
	//
	// It is per-RECIPIENT, so it never affects what anyone else sees, and the
	// blocked player is never told — a block that announces itself invites the
	// retaliation it exists to prevent.
	BlockedAccounts []string `json:"blockedAccounts,omitempty"`
	// Hints are the one-shot newcomer lessons the browser has already shown
	// this account (M23.1). They live beside Color and BlockedAccounts because
	// they are account-wide presentation state, not simulation state and not a
	// property of any one world run.
	Hints AccountHintPreferences `json:"hints,omitempty"`
	// Profile is the public, account-visible identity opened from the Players
	// window (M24.1). The account id remains private; this is the deliberately
	// public subset, with Handle uniqueness enforced by the ChatDatabase before
	// the document is written.
	Profile AccountProfilePreferences `json:"profile,omitempty"`
	// FollowedAccounts are durable account ids this player follows (M26.1).
	// They are stored here because they are account-wide presentation/social
	// state, but they must never be returned raw to a browser.
	FollowedAccounts []string `json:"followedAccounts,omitempty"`
	// ShareLocationWithFollowers lets followed accounts see the world this
	// account is currently playing. The zero value is deliberately private.
	ShareLocationWithFollowers bool `json:"shareLocationWithFollowers,omitempty"`
}

type AccountHintPreferences struct {
	Players bool `json:"players,omitempty"`
	Death   bool `json:"death,omitempty"`
	Chat    bool `json:"chat,omitempty"`
}

// ErrNoAccountID refuses a preferences read or write that has no account to key
// on. The store is account-wide, so a missing id has no sane default: writing
// under "" would make one shared bucket that every guest overwrites and every
// other guest reads back, which is worse than losing the preference.
var ErrNoAccountID = errors.New("zztgo: account preferences require an account id")

// accountPreferencesKey is the id itself — unlike playerStateKey there is no
// second dimension, which is the whole reason this store exists. It refuses
// rather than substituting a default.
func accountPreferencesKey(accountID string) (string, error) {
	key := strings.TrimSpace(accountID)
	if key == "" {
		return "", ErrNoAccountID
	}
	return key, nil
}

type ChatDatabase interface {
	AddMessage(author ChatAuthor, text string) (ChatRecord, error)
	GetRecentMessages(limit int) ([]ChatRecord, error)
	PutPlayerState(accountID, worldName string, state PlayerState) error
	GetPlayerState(accountID, worldName string) (PlayerState, bool, error)
	PutAccountPreferences(accountID string, prefs AccountPreferences) error
	GetAccountPreferences(accountID string) (AccountPreferences, bool, error)
	Close() error
}

type MemChatDatabase struct {
	mu           sync.Mutex
	messages     []ChatRecord
	playerStates map[string]PlayerState
	accountPrefs map[string]AccountPreferences
	handleOwners map[string]string
}

func NewMemChatDatabase() *MemChatDatabase {
	return &MemChatDatabase{}
}

func (db *MemChatDatabase) AddMessage(author ChatAuthor, text string) (ChatRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rec := ChatRecord{
		From:      author.Name,
		Text:      text,
		Timestamp: time.Now(),
		PlayerID:  author.PlayerID,
		AccountID: author.AccountID,
	}
	db.messages = append(db.messages, rec)
	return rec, nil
}

func (db *MemChatDatabase) GetRecentMessages(limit int) ([]ChatRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	n := len(db.messages)
	if n == 0 {
		return nil, nil
	}
	start := n - limit
	if start < 0 {
		start = 0
	}
	res := make([]ChatRecord, n-start)
	copy(res, db.messages[start:])
	return res, nil
}

func (db *MemChatDatabase) PutPlayerState(accountID, worldName string, state PlayerState) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.playerStates == nil {
		db.playerStates = make(map[string]PlayerState)
	}
	db.playerStates[playerStateKey(accountID, worldName)] = state
	return nil
}

func (db *MemChatDatabase) GetPlayerState(accountID, worldName string) (PlayerState, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.playerStates == nil {
		return PlayerState{}, false, nil
	}
	state, ok := db.playerStates[playerStateKey(accountID, worldName)]
	return state, ok, nil
}

func (db *MemChatDatabase) PutAccountPreferences(accountID string, prefs AccountPreferences) error {
	key, err := accountPreferencesKey(accountID)
	if err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.accountPrefs == nil {
		db.accountPrefs = make(map[string]AccountPreferences)
	}
	if err := putAccountPreferencesLocked(db.accountPrefs, &db.handleOwners, key, prefs); err != nil {
		return err
	}
	return nil
}

func (db *MemChatDatabase) GetAccountPreferences(accountID string) (AccountPreferences, bool, error) {
	key, err := accountPreferencesKey(accountID)
	if err != nil {
		return AccountPreferences{}, false, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.accountPrefs == nil {
		return AccountPreferences{}, false, nil
	}
	prefs, ok := db.accountPrefs[key]
	return prefs, ok, nil
}

func (db *MemChatDatabase) Close() error {
	return nil
}

type FileChatDatabase struct {
	mu           sync.Mutex
	file         *os.File
	statePath    string
	prefsPath    string
	messages     []ChatRecord
	playerStates map[string]PlayerState
	accountPrefs map[string]AccountPreferences
	handleOwners map[string]string
}

func NewFileChatDatabase(filepath string) (*FileChatDatabase, error) {
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}

	db := &FileChatDatabase{
		file:         file,
		statePath:    filepath + ".playerstate.json",
		prefsPath:    filepath + ".accountprefs.json",
		playerStates: make(map[string]PlayerState),
		accountPrefs: make(map[string]AccountPreferences),
		handleOwners: make(map[string]string),
	}

	dec := json.NewDecoder(file)
	for {
		var rec ChatRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			continue
		}
		// A PlayerID does not survive the process that minted it (mintPlayerID is
		// a counter that restarts), so a loaded record's id would name whoever
		// happens to hold that number next. Cleared rather than trusted: history
		// suppression across a restart is the AccountID's job (M21.1).
		rec.PlayerID = 0
		db.messages = append(db.messages, rec)
	}
	db.loadPlayerStates()
	db.loadAccountPreferences()

	return db, nil
}

func (db *FileChatDatabase) AddMessage(author ChatAuthor, text string) (ChatRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rec := ChatRecord{
		From:      author.Name,
		Text:      text,
		Timestamp: time.Now(),
		PlayerID:  author.PlayerID,
		AccountID: author.AccountID,
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return ChatRecord{}, err
	}

	if _, err := db.file.Write(append(data, '\n')); err != nil {
		return ChatRecord{}, err
	}

	db.messages = append(db.messages, rec)
	return rec, nil
}

func (db *FileChatDatabase) GetRecentMessages(limit int) ([]ChatRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	n := len(db.messages)
	if n == 0 {
		return nil, nil
	}
	start := n - limit
	if start < 0 {
		start = 0
	}
	res := make([]ChatRecord, n-start)
	copy(res, db.messages[start:])
	return res, nil
}

func (db *FileChatDatabase) PutPlayerState(accountID, worldName string, state PlayerState) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.playerStates == nil {
		db.playerStates = make(map[string]PlayerState)
	}
	db.playerStates[playerStateKey(accountID, worldName)] = state
	return db.writePlayerStatesLocked()
}

func (db *FileChatDatabase) GetPlayerState(accountID, worldName string) (PlayerState, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	state, ok := db.playerStates[playerStateKey(accountID, worldName)]
	return state, ok, nil
}

func (db *FileChatDatabase) PutAccountPreferences(accountID string, prefs AccountPreferences) error {
	key, err := accountPreferencesKey(accountID)
	if err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.accountPrefs == nil {
		db.accountPrefs = make(map[string]AccountPreferences)
	}
	if err := putAccountPreferencesLocked(db.accountPrefs, &db.handleOwners, key, prefs); err != nil {
		return err
	}
	return db.writeAccountPreferencesLocked()
}

func (db *FileChatDatabase) GetAccountPreferences(accountID string) (AccountPreferences, bool, error) {
	key, err := accountPreferencesKey(accountID)
	if err != nil {
		return AccountPreferences{}, false, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	prefs, ok := db.accountPrefs[key]
	return prefs, ok, nil
}

func (db *FileChatDatabase) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.file.Close()
}

func (db *FileChatDatabase) loadPlayerStates() {
	data, err := os.ReadFile(db.statePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &db.playerStates)
	if db.playerStates == nil {
		db.playerStates = make(map[string]PlayerState)
	}
}

// loadAccountPreferences mirrors loadPlayerStates: a missing or unreadable
// document is an empty store, not a startup failure. The preferences are their
// own JSON document rather than a second key in the player-state one because
// they have a different key (an account, not an account and a world) and a
// different lifetime.
func (db *FileChatDatabase) loadAccountPreferences() {
	data, err := os.ReadFile(db.prefsPath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &db.accountPrefs)
	if db.accountPrefs == nil {
		db.accountPrefs = make(map[string]AccountPreferences)
	}
	db.rebuildHandleOwnersLocked()
}

func (db *FileChatDatabase) rebuildHandleOwnersLocked() {
	db.handleOwners = make(map[string]string)
	accounts := make([]string, 0, len(db.accountPrefs))
	for account := range db.accountPrefs {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)
	for _, account := range accounts {
		prefs := db.accountPrefs[account]
		handle, err := NormalizeProfileHandle(prefs.Profile.Handle)
		if err != nil || handle == "" {
			prefs.Profile.Handle = ""
			db.accountPrefs[account] = prefs
			continue
		}
		if owner := db.handleOwners[handle]; owner != "" && owner != account {
			prefs.Profile.Handle = ""
			db.accountPrefs[account] = prefs
			continue
		}
		prefs.Profile.Handle = handle
		db.accountPrefs[account] = prefs
		db.handleOwners[handle] = account
	}
}

func putAccountPreferencesLocked(prefs map[string]AccountPreferences, handleOwners *map[string]string, accountID string, next AccountPreferences) error {
	if *handleOwners == nil {
		*handleOwners = make(map[string]string)
		for account, pref := range prefs {
			if pref.Profile.Handle != "" {
				(*handleOwners)[pref.Profile.Handle] = account
			}
		}
	}
	profile, err := SanitizeAccountProfile(next.Profile)
	if err != nil {
		return err
	}
	next.Profile = profile
	next.FollowedAccounts = sanitizeFollowedAccounts(next.FollowedAccounts, accountID)
	oldHandle := prefs[accountID].Profile.Handle
	newHandle := next.Profile.Handle
	if newHandle != "" {
		if owner := (*handleOwners)[newHandle]; owner != "" && owner != accountID {
			return ErrProfileHandleTaken
		}
	}
	if oldHandle != "" && oldHandle != newHandle {
		delete(*handleOwners, oldHandle)
	}
	if newHandle != "" {
		(*handleOwners)[newHandle] = accountID
	}
	prefs[accountID] = next
	return nil
}

func sanitizeFollowedAccounts(accounts []string, self string) []string {
	if len(accounts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(accounts))
	out := make([]string, 0, len(accounts))
	for _, account := range accounts {
		account = strings.TrimSpace(account)
		if account == "" || account == self || len(account) > 256 {
			continue
		}
		valid := true
		for _, r := range account {
			if r < 32 || r == 127 {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		if _, ok := seen[account]; ok {
			continue
		}
		seen[account] = struct{}{}
		out = append(out, account)
	}
	sort.Strings(out)
	return out
}

func (db *FileChatDatabase) writeAccountPreferencesLocked() error {
	data, err := json.MarshalIndent(db.accountPrefs, "", "  ")
	if err != nil {
		return err
	}
	tmp := db.prefsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, db.prefsPath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (db *FileChatDatabase) writePlayerStatesLocked() error {
	data, err := json.MarshalIndent(db.playerStates, "", "  ")
	if err != nil {
		return err
	}
	tmp := db.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, db.statePath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func playerStateKey(accountID, worldName string) string {
	return worldName + "\t" + accountID
}
