package zztgo

// M37.3: a signed-in player can download everything the server keeps about
// their account (GET /api/account/export). The owner's decisions (2026-11-26)
// are recorded in TASKS.md M37.3; deletion is M37.3a.
//
// The export is the account holder's own data and nothing of anyone else's:
// the accounts they follow or block are given by username, never by account
// id, because another player's id is not theirs to see.

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// accountDataStore is the part of a ChatDatabase that can list one account's
// data. Both stores implement it; a store that does not cannot export.
type accountDataStore interface {
	MessagesByAccount(accountID string) []ChatRecord
	PlayerStatesByAccount(accountID string) map[string]PlayerState
}

func messagesByAccount(messages []ChatRecord, accountID string) []ChatRecord {
	var out []ChatRecord
	for _, rec := range messages {
		if accountID != "" && rec.AccountID == accountID {
			out = append(out, rec)
		}
	}
	return out
}

func playerStatesByAccount(states map[string]PlayerState, accountID string) map[string]PlayerState {
	out := make(map[string]PlayerState)
	for key, state := range states {
		tab := strings.IndexByte(key, '\t')
		if tab >= 0 && accountID != "" && key[tab+1:] == accountID {
			out[key[:tab]] = state
		}
	}
	return out
}

// MessagesByAccount lists the chat lines this account posted, oldest first.
func (db *MemChatDatabase) MessagesByAccount(accountID string) []ChatRecord {
	db.mu.Lock()
	defer db.mu.Unlock()
	return messagesByAccount(db.messages, accountID)
}

// PlayerStatesByAccount is this account's saved progress, by world.
func (db *MemChatDatabase) PlayerStatesByAccount(accountID string) map[string]PlayerState {
	db.mu.Lock()
	defer db.mu.Unlock()
	return playerStatesByAccount(db.playerStates, accountID)
}

// MessagesByAccount lists the chat lines this account posted, oldest first.
func (db *FileChatDatabase) MessagesByAccount(accountID string) []ChatRecord {
	db.mu.Lock()
	defer db.mu.Unlock()
	return messagesByAccount(db.messages, accountID)
}

// PlayerStatesByAccount is this account's saved progress, by world.
func (db *FileChatDatabase) PlayerStatesByAccount(accountID string) map[string]PlayerState {
	db.mu.Lock()
	defer db.mu.Unlock()
	return playerStatesByAccount(db.playerStates, accountID)
}

// ResultsFor lists one account's stored challenge runs, by challenge then order.
func (s *ChallengeStore) ResultsFor(accountKey string) []ChallengeResult {
	if s == nil || accountKey == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ChallengeResult
	for _, rows := range s.results {
		for _, row := range rows {
			if row.AccountKey == accountKey {
				out = append(out, row)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChallengeID != out[j].ChallengeID {
			return out[i].ChallengeID < out[j].ChallengeID
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// accountWorldRoles lists the worlds in dir this account owns or collaborates on.
func accountWorldRoles(dir, accountID string) []AccountExportWorld {
	if dir == "" || accountID == "" {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.access.json"))
	sort.Strings(paths)
	var out []AccountExportWorld
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".access.json")
		access, ok, err := loadWorldAccess(dir, name)
		if err != nil || !ok {
			continue
		}
		switch {
		case access.OwnerAccountID == accountID:
			out = append(out, AccountExportWorld{World: name, Role: "owner"})
		case accountListHas(access.CollaboratorAccountIDs, accountID):
			out = append(out, AccountExportWorld{World: name, Role: "collaborator"})
		}
	}
	return out
}

func accountListHas(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// AccountExport is the download. Its field names are the contract a player
// reads, so they say what each thing is.
type AccountExport struct {
	ExportedAt  time.Time                 `json:"exportedAt"`
	AccountID   string                    `json:"accountId"`
	Email       string                    `json:"email,omitempty"`
	GoogleName  string                    `json:"googleName,omitempty"`
	FirstSignIn *time.Time                `json:"firstSignIn,omitempty"`
	LastSignIn  *time.Time                `json:"lastSignIn,omitempty"`
	SignIns     int                       `json:"signIns,omitempty"`
	Username    string                    `json:"username,omitempty"`
	Profile     AccountProfilePreferences `json:"profile"`
	Settings    AccountExportSettings     `json:"settings"`
	Following   []string                  `json:"following,omitempty"`
	Blocking    []string                  `json:"blocking,omitempty"`
	Progress    map[string]PlayerState    `json:"progressByWorld,omitempty"`
	Chat        []AccountExportChat       `json:"chat,omitempty"`
	Challenges  []AccountExportRun        `json:"challengeRuns,omitempty"`
	Worlds      []AccountExportWorld      `json:"worlds,omitempty"`
}

// AccountExportSettings is the account's own choices, minus its social lists
// (which are given by username in Following and Blocking).
type AccountExportSettings struct {
	Color                      string                 `json:"color,omitempty"`
	Hints                      AccountHintPreferences `json:"hints"`
	ShareLocationWithFollowers bool                   `json:"shareLocationWithFollowers"`
	FavoriteWorlds             []string               `json:"favoriteWorlds,omitempty"`
	Comfort                    ComfortPreferences     `json:"comfort"`
}

type AccountExportChat struct {
	At   time.Time   `json:"at"`
	Name CP437String `json:"name"`
	Text CP437String `json:"text"`
}

type AccountExportRun struct {
	ChallengeID string      `json:"challengeId"`
	Name        CP437String `json:"name"`
	Ticks       int         `json:"ticks"`
	Score       int         `json:"score"`
	Gems        int         `json:"gems"`
	RecordingID string      `json:"recordingId,omitempty"`
}

type AccountExportWorld struct {
	World string `json:"world"`
	Role  string `json:"role"`
}

// noUsernameLabel stands in, in Following and Blocking, for an account that
// has no username: its id is not this player's to see.
const noUsernameLabel = "(an account without a username)"

// exportAccount gathers everything kept about one account.
func (s *WebSocketServer) exportAccount(account AuthenticatedAccount, records *AccountStore) AccountExport {
	out := AccountExport{
		ExportedAt: s.clockNow().UTC().Truncate(time.Second),
		AccountID:  account.ID,
		Email:      account.Email,
		GoogleName: account.Name,
	}
	if rec, ok := records.Get(account.ID); ok {
		first, last := rec.FirstSignIn, rec.LastSignIn
		out.Email, out.GoogleName = rec.Email, rec.GoogleName
		out.FirstSignIn, out.LastSignIn, out.SignIns = &first, &last, rec.SignIns
	}
	if prefs, ok := s.loadAccountPreferences(account.ID); ok {
		out.Username = prefs.Profile.Handle
		out.Profile = prefs.Profile
		out.Settings = AccountExportSettings{
			Color:                      prefs.Color,
			Hints:                      prefs.Hints,
			ShareLocationWithFollowers: prefs.ShareLocationWithFollowers,
			FavoriteWorlds:             prefs.FavoriteWorlds,
			Comfort:                    prefs.Comfort,
		}
		out.Following = s.usernamesFor(prefs.FollowedAccounts)
		out.Blocking = s.usernamesFor(prefs.BlockedAccounts)
	}
	if store, ok := s.ChatDB.(accountDataStore); ok {
		if progress := store.PlayerStatesByAccount(account.ID); len(progress) > 0 {
			out.Progress = progress
		}
		for _, rec := range store.MessagesByAccount(account.ID) {
			out.Chat = append(out.Chat, AccountExportChat{At: rec.Timestamp.UTC(), Name: rec.From, Text: rec.Text})
		}
	}
	for _, run := range s.ChallengeStore.ResultsFor(account.ID) {
		out.Challenges = append(out.Challenges, AccountExportRun{
			ChallengeID: run.ChallengeID, Name: run.Name, Ticks: run.Ticks, Score: run.Score, Gems: run.Gems, RecordingID: run.RecordingID,
		})
	}
	out.Worlds = accountWorldRoles(s.WorldsDir, account.ID)
	return out
}

func (s *WebSocketServer) usernamesFor(accountIDs []string) []string {
	var out []string
	for _, id := range accountIDs {
		if name := s.accountUsername(id); name != "" {
			out = append(out, name)
		} else {
			out = append(out, noUsernameLabel)
		}
	}
	return out
}

// exportFileName is the download's name: the username when there is one.
func exportFileName(username string) string {
	if username == "" {
		username = "account"
	}
	return "zztmmo-" + username + "-data.json"
}
