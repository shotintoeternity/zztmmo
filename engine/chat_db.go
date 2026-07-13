package zztgo

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

type ChatRecord struct {
	From      string    `json:"from"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

type ChatDatabase interface {
	AddMessage(from, text string) (ChatRecord, error)
	GetRecentMessages(limit int) ([]ChatRecord, error)
	PutPlayerState(accountID, worldName string, state PlayerState) error
	GetPlayerState(accountID, worldName string) (PlayerState, bool, error)
	Close() error
}

type MemChatDatabase struct {
	mu           sync.Mutex
	messages     []ChatRecord
	playerStates map[string]PlayerState
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

func (db *MemChatDatabase) Close() error {
	return nil
}

type FileChatDatabase struct {
	mu           sync.Mutex
	file         *os.File
	statePath    string
	messages     []ChatRecord
	playerStates map[string]PlayerState
}

func NewFileChatDatabase(filepath string) (*FileChatDatabase, error) {
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}

	db := &FileChatDatabase{
		file:         file,
		statePath:    filepath + ".playerstate.json",
		playerStates: make(map[string]PlayerState),
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
