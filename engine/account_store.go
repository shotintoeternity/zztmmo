package zztgo

// M37.9: the server keeps a record of every account that signs in with Google.
// Before this the Google identity (id, email, the name on the Google account)
// lived only in the signed session cookie, so the server had no list of who had
// ever signed in. The owner chose what is kept (2026-11-25): the Google id, the
// email, the Google name, and when the account first and last signed in.
//
// It is private, operator-only data: nothing here is served to a browser or
// shown in a world. The file is written 0600 beside the other durable service
// state under -saves. M37.3 (players export and remove their data) covers it.

import (
	"encoding/json"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AccountRecord is what the server knows about one signed-in account.
type AccountRecord struct {
	ID          string    `json:"id"`
	Email       string    `json:"email,omitempty"`
	GoogleName  string    `json:"googleName,omitempty"`
	FirstSignIn time.Time `json:"firstSignIn"`
	LastSignIn  time.Time `json:"lastSignIn"`
	SignIns     int       `json:"signIns"`
}

// AccountStore is the durable record of signed-in accounts, keyed by account id.
type AccountStore struct {
	mu       sync.Mutex
	path     string
	accounts map[string]AccountRecord
}

// AccountStorePath is where zzt-server keeps the records under -saves.
func AccountStorePath(savesDir string) string {
	return filepath.Join(savesDir, "accounts.json")
}

// NewAccountStore loads the records at path. An empty path keeps them in
// memory. A missing file is an empty store; a corrupt one is set aside (as the
// chat store's documents are) and the store starts empty, because a server that
// will not start is worse than one that has to relearn who signed in.
func NewAccountStore(path string) *AccountStore {
	store := &AccountStore{path: path, accounts: make(map[string]AccountRecord)}
	if path == "" {
		return store
	}
	data, ok := readChatStoreDocument(path)
	if !ok {
		return store
	}
	var loaded map[string]AccountRecord
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("zztgo: setting aside unreadable account records in %s: %v", path, err)
		setAsideCorruptChatStoreDocument(path)
		return store
	}
	for id, rec := range loaded {
		if id != "" {
			rec.ID = id
			store.accounts[id] = rec
		}
	}
	return store
}

// RecordSignIn notes a completed sign-in. The first one sets FirstSignIn; every
// one moves LastSignIn and refreshes the email and Google name, which the
// account holder can change at Google between visits.
func (s *AccountStore) RecordSignIn(account AuthenticatedAccount, at time.Time) (AccountRecord, error) {
	if s == nil || account.ID == "" {
		return AccountRecord{}, nil
	}
	at = at.UTC().Truncate(time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, seen := s.accounts[account.ID]
	if !seen {
		rec = AccountRecord{ID: account.ID, FirstSignIn: at}
	}
	rec.Email = strings.TrimSpace(account.Email)
	rec.GoogleName = strings.TrimSpace(account.Name)
	rec.LastSignIn = at
	rec.SignIns++
	s.accounts[account.ID] = rec
	return rec, s.writeLocked()
}

// Get returns the record for one account id.
func (s *AccountStore) Get(accountID string) (AccountRecord, bool) {
	if s == nil {
		return AccountRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.accounts[accountID]
	return rec, ok
}

// Len is how many accounts have ever signed in.
func (s *AccountStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.accounts)
}

func (s *AccountStore) writeLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.accounts, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, 0o600)
}
