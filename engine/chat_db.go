package zztgo

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type ChatRecord struct {
	From      string    `json:"from"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
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
	AddMessage(from, text string) (ChatRecord, error)
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
}

func NewMemChatDatabase() *MemChatDatabase {
	return &MemChatDatabase{}
}

func (db *MemChatDatabase) AddMessage(from, text string) (ChatRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rec := ChatRecord{
		From:      from,
		Text:      text,
		Timestamp: time.Now(),
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
	db.accountPrefs[key] = prefs
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
		db.messages = append(db.messages, rec)
	}
	db.loadPlayerStates()
	db.loadAccountPreferences()

	return db, nil
}

func (db *FileChatDatabase) AddMessage(from, text string) (ChatRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rec := ChatRecord{
		From:      from,
		Text:      text,
		Timestamp: time.Now(),
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
	db.accountPrefs[key] = prefs
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
