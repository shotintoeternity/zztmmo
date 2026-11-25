package zztgo

// M37.3a: a signed-in player can delete their account. The owner's decisions
// are recorded in TASKS.md M37.3: deletion is self-serve and confirmed by the
// username; chat lines stay but read "(deleted player)"; owned worlds,
// challenge times and replays stay, unowned and shown as "(deleted player)";
// moderation records survive, so deleting an account cannot lift a ban.
//
// The steps run in a fixed order. Live connections are ended first and their
// identity is cleared before anything is deleted, because a connection's
// disconnect path writes the account's progress back, and would otherwise put
// back what the steps below remove.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DeletedPlayerName is what a deleted account's lines, rows and replays show.
const DeletedPlayerName = "(deleted player)"

// accountDeleteStore is the part of a ChatDatabase that can forget an account.
type accountDeleteStore interface {
	// ForgetAccount deletes the account's preferences (freeing its username)
	// and progress, removes it from every other account's follows and blocks,
	// and redacts its chat lines. It returns how many lines it redacted.
	ForgetAccount(accountID string) (int, error)
}

func forgetAccountLocked(accountID string, prefs map[string]AccountPreferences, handleOwners map[string]string, states map[string]PlayerState, messages []ChatRecord) int {
	if old, ok := prefs[accountID]; ok {
		if old.Profile.Handle != "" && handleOwners[old.Profile.Handle] == accountID {
			delete(handleOwners, old.Profile.Handle)
		}
		delete(prefs, accountID)
	}
	for id, p := range prefs {
		followed := withoutAccount(p.FollowedAccounts, accountID)
		blocked := withoutAccount(p.BlockedAccounts, accountID)
		if len(followed) != len(p.FollowedAccounts) || len(blocked) != len(p.BlockedAccounts) {
			p.FollowedAccounts, p.BlockedAccounts = followed, blocked
			prefs[id] = p
		}
	}
	for key := range states {
		if tab := strings.IndexByte(key, '\t'); tab >= 0 && key[tab+1:] == accountID {
			delete(states, key)
		}
	}
	redacted := 0
	for i := range messages {
		if messages[i].AccountID == accountID {
			messages[i].From = CP437String(DeletedPlayerName)
			messages[i].AccountID = ""
			redacted++
		}
	}
	return redacted
}

func withoutAccount(list []string, accountID string) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, 0, len(list))
	for _, id := range list {
		if id != accountID {
			out = append(out, id)
		}
	}
	return out
}

// ForgetAccount implements accountDeleteStore.
func (db *MemChatDatabase) ForgetAccount(accountID string) (int, error) {
	if accountID == "" {
		return 0, nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.accountPrefs == nil {
		db.accountPrefs = make(map[string]AccountPreferences)
	}
	if db.handleOwners == nil {
		db.handleOwners = make(map[string]string)
		for id, p := range db.accountPrefs {
			if p.Profile.Handle != "" {
				db.handleOwners[p.Profile.Handle] = id
			}
		}
	}
	if db.playerStates == nil {
		db.playerStates = make(map[string]PlayerState)
	}
	return forgetAccountLocked(accountID, db.accountPrefs, db.handleOwners, db.playerStates, db.messages), nil
}

// ForgetAccount implements accountDeleteStore. The chat log is an append-only
// file, so a redaction rewrites it whole and reopens the append handle: after
// the rename the old handle would point at the unlinked file, and every later
// line would be lost.
func (db *FileChatDatabase) ForgetAccount(accountID string) (int, error) {
	if accountID == "" {
		return 0, nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return 0, ErrChatDatabaseClosed
	}
	redacted := forgetAccountLocked(accountID, db.accountPrefs, db.handleOwners, db.playerStates, db.messages)
	if err := db.writeAccountPreferencesLocked(); err != nil {
		return redacted, err
	}
	if err := db.writePlayerStatesLocked(); err != nil {
		return redacted, err
	}
	if redacted == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	for _, rec := range db.messages {
		data, err := json.Marshal(rec)
		if err != nil {
			return redacted, err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := writeFileAtomic(db.chatPath, buf.Bytes(), 0o600); err != nil {
		return redacted, err
	}
	reopened, err := os.OpenFile(db.chatPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return redacted, err
	}
	_ = db.file.Close()
	db.file = reopened
	return redacted, nil
}

// Delete removes one account's sign-in record.
func (s *AccountStore) Delete(accountID string) error {
	if s == nil || accountID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[accountID]; !ok {
		return nil
	}
	delete(s.accounts, accountID)
	return s.writeLocked()
}

// Anonymize gives every row of one account a single fresh tombstone key and
// the deleted name, and returns the recording ids those rows cite. The rows
// stay on the board: a tombstone keeps them valid (a row with no key is dropped
// on load) and still one runner's, and no viewer can ever match it.
func (s *ChallengeStore) Anonymize(accountKey string) (int, []string, error) {
	if s == nil || accountKey == "" {
		return 0, nil, nil
	}
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, nil, err
	}
	tombstone := "deleted:" + hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	var recordings []string
	rowsChanged := 0
	for id, rows := range s.results {
		for i := range rows {
			if rows[i].AccountKey == accountKey {
				rows[i].AccountKey = tombstone
				rows[i].Name = CP437String(DeletedPlayerName)
				if rows[i].RecordingID != "" {
					recordings = append(recordings, rows[i].RecordingID)
				}
				rowsChanged++
			}
		}
		s.results[id] = rows
	}
	if rowsChanged == 0 {
		return 0, nil, nil
	}
	sort.Strings(recordings)
	return rowsChanged, recordings, s.writeLocked()
}

// renameInRecording rewrites a recording's join and name ops that carry name,
// so a replay shows DeletedPlayerName. Names are presentation: no simulation
// state reads them, so a rewritten recording still replays to the evidence its
// leaderboard row carries (m37_3a_test.go checks exactly that).
func renameInRecording(path, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	want := []byte(fmt.Sprintf("%q", name))
	if !bytes.Contains(data, want) {
		return false, nil
	}
	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<26)
	changed := false
	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if first {
			// The header is not a tick.
			first = false
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		var tick recTick
		if bytes.Contains(line, want) && json.Unmarshal(line, &tick) == nil {
			touched := false
			for i := range tick.Ops {
				op := &tick.Ops[i]
				if (op.Op == "join" || op.Op == "name") && string(op.Name) == name {
					op.Name = CP437String(DeletedPlayerName)
					touched = true
				}
			}
			if touched {
				encoded, err := json.Marshal(tick)
				if err != nil {
					return false, err
				}
				out.Write(encoded)
				out.WriteByte('\n')
				changed = true
				continue
			}
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	return true, writeFileAtomic(path, out.Bytes(), 0o644)
}

// releaseWorlds clears the account as owner and collaborator in every
// world access sidecar in dir, and as the author of every dream there.
func releaseWorlds(dir, accountID, username string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	released := 0
	paths, _ := filepath.Glob(filepath.Join(dir, "*.access.json"))
	sort.Strings(paths)
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".access.json")
		access, ok, err := loadWorldAccess(dir, name)
		if err != nil || !ok {
			continue
		}
		collaborators := withoutAccount(access.CollaboratorAccountIDs, accountID)
		if access.OwnerAccountID != accountID && len(collaborators) == len(access.CollaboratorAccountIDs) {
			continue
		}
		if access.OwnerAccountID == accountID {
			access.OwnerAccountID, access.OwnerName = "", ""
		}
		access.CollaboratorAccountIDs = collaborators
		if err := writeWorldAccess(dir, name, access); err != nil {
			return released, err
		}
		released++
	}
	if username == "" {
		return released, nil
	}
	metas, _ := filepath.Glob(filepath.Join(dir, "*.meta.json"))
	sort.Strings(metas)
	for _, path := range metas {
		name := strings.TrimSuffix(filepath.Base(path), ".meta.json")
		meta, ok, err := loadWorldMeta(dir, name)
		if err != nil || !ok || meta.Author != username {
			continue
		}
		meta.Author = DeletedPlayerName
		if err := writeWorldMeta(dir, name, meta); err != nil {
			return released, err
		}
	}
	return released, nil
}

// removeSaveSidecars deletes the save-game progress sidecars that belong to
// the account. One sidecar carries one account (snapshot.go).
func removeSaveSidecars(dir, accountID string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.playerstate.json"))
	removed := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sidecar snapshotPlayerStateSidecar
		if json.Unmarshal(data, &sidecar) != nil || sidecar.AccountID != accountID {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// endAccountConnections takes the account off every live connection: its
// players lose the account and are renamed, and each of its play and editor
// connections is told and disconnected. Clearing the room's identity first is
// what stops a disconnect from writing the account's progress back afterwards.
func (s *WebSocketServer) endAccountConnections(accountID string) int {
	var ended []*webSocketClient
	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	if s.DefaultInstance != nil {
		instances = append(instances, s.DefaultInstance)
	}
	// A client's accountID is set before it is registered and only read after,
	// so it is read here and never written: the room's copy of the identity,
	// cleared below under inst.mu, is the one the disconnect path persists from.
	for client := range s.EditorSessions {
		if client.accountID == accountID {
			ended = append(ended, client)
		}
	}
	s.mu.Unlock()
	seen := make(map[*WorldInstance]bool)
	for _, inst := range instances {
		if inst == nil || seen[inst] {
			continue
		}
		seen[inst] = true
		inst.mu.Lock()
		// In PlayerID order: the rename is recorded, and a recording's op
		// order must not come from map iteration.
		var renamed []PlayerID
		for playerID := range inst.RoomManager.players {
			if id, _, ok := inst.RoomManager.PlayerIdentity(playerID); ok && id == accountID {
				renamed = append(renamed, playerID)
			}
		}
		sort.Slice(renamed, func(i, j int) bool { return renamed[i] < renamed[j] })
		for _, playerID := range renamed {
			inst.RoomManager.SetPlayerIdentity(playerID, "", DeletedPlayerName)
		}
		for _, client := range inst.Clients {
			if client != nil && client.accountID == accountID {
				ended = append(ended, client)
			}
		}
		inst.mu.Unlock()
	}
	for _, client := range ended {
		s.tellPlayer(context.Background(), client, "deleted", "This account has been deleted.", true)
		s.endConnection(client)
	}
	return len(ended)
}

// AccountDeletion reports what a deletion did, for the log and the tests.
type AccountDeletion struct {
	Connections   int
	ChatRedacted  int
	SaveSidecars  int
	ChallengeRows int
	Recordings    int
	Worlds        int
}

// DeleteAccount forgets one account, in the order the file comment explains.
func (s *WebSocketServer) DeleteAccount(account AuthenticatedAccount, records *AccountStore) (AccountDeletion, error) {
	var report AccountDeletion
	if account.ID == "" {
		return report, fmt.Errorf("no account to delete")
	}
	username := s.accountUsername(account.ID)
	report.Connections = s.endAccountConnections(account.ID)

	if store, ok := s.ChatDB.(accountDeleteStore); ok {
		n, err := store.ForgetAccount(account.ID)
		report.ChatRedacted = n
		if err != nil {
			return report, fmt.Errorf("forget the account's preferences, progress and chat: %w", err)
		}
	} else if s.ChatDB != nil {
		return report, fmt.Errorf("this chat store cannot delete an account")
	}

	n, err := removeSaveSidecars(s.SavesDir, account.ID)
	report.SaveSidecars = n
	if err != nil {
		return report, fmt.Errorf("remove save sidecars: %w", err)
	}

	rows, recordings, err := s.ChallengeStore.Anonymize(account.ID)
	report.ChallengeRows = rows
	if err != nil {
		return report, fmt.Errorf("anonymize challenge rows: %w", err)
	}
	if username != "" {
		live := s.liveRecordings()
		for _, id := range recordings {
			path, err := s.challengeRecordingPath(id)
			if err != nil || live[path] {
				continue
			}
			if changed, err := renameInRecording(path, username); err != nil {
				log.Printf("zztgo: could not anonymize recording %s: %v", id, err)
			} else if changed {
				report.Recordings++
			}
		}
		report.Recordings += s.renameInSessionRecordings(username, recordings)
	}

	n, err = releaseWorlds(s.WorldsDir, account.ID, username)
	report.Worlds = n
	if err != nil {
		return report, fmt.Errorf("release worlds: %w", err)
	}

	if err := records.Delete(account.ID); err != nil {
		return report, fmt.Errorf("delete the sign-in record: %w", err)
	}
	log.Printf("zztgo: deleted account %s: %+v", account.ID, report)
	return report, nil
}

// liveRecordings are the files a recorder is still appending to. They are left
// alone: rewriting one under its recorder would send the rest of the session
// to an unlinked file.
func (s *WebSocketServer) liveRecordings() map[string]bool {
	live := make(map[string]bool)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inst := range s.Instances {
		if inst != nil && inst.RoomManager.recorder != nil && inst.recordingPath != "" {
			live[inst.recordingPath] = true
		}
	}
	return live
}

// renameInSessionRecordings anonymizes the account's username in every other
// session recording on disk (a world's /replay/ recordings), skipping the
// challenge recordings already done.
func (s *WebSocketServer) renameInSessionRecordings(username string, done []string) int {
	skip := s.liveRecordings()
	for _, id := range done {
		if path, err := s.challengeRecordingPath(id); err == nil {
			skip[path] = true
		}
	}
	dirs := []string{s.RecordDir}
	if s.ReplayDir != "" && s.ReplayDir != s.RecordDir {
		dirs = append(dirs, s.ReplayDir)
	}
	changed := 0
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		paths, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		sort.Strings(paths)
		for _, path := range paths {
			if skip[path] {
				continue
			}
			if ok, err := renameInRecording(path, username); err != nil {
				log.Printf("zztgo: could not anonymize recording %s: %v", path, err)
			} else if ok {
				changed++
			}
		}
	}
	return changed
}
