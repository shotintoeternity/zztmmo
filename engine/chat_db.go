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
	Close() error
}

type MemChatDatabase struct {
	mu       sync.Mutex
	messages []ChatRecord
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

func (db *MemChatDatabase) Close() error {
	return nil
}

type FileChatDatabase struct {
	mu       sync.Mutex
	file     *os.File
	messages []ChatRecord
}

func NewFileChatDatabase(filepath string) (*FileChatDatabase, error) {
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}

	db := &FileChatDatabase{
		file: file,
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

func (db *FileChatDatabase) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.file.Close()
}
