package zztgo

import (
	"log"
	"net/http"
)

func (s *WebSocketServer) authAccount(r *http.Request) (AuthenticatedAccount, bool) {
	if s.Auth == nil {
		return AuthenticatedAccount{}, false
	}
	return s.Auth.AccountFromRequest(r)
}

func (s *WebSocketServer) loadAccountPlayerState(accountID, worldName string) (PlayerState, bool) {
	if s.ChatDB == nil || accountID == "" {
		return PlayerState{}, false
	}
	// M32.1: a challenge run starts from the world's pristine state for
	// everybody. Handing a returning account its saved inventory would make two
	// runs of the same challenge measure different games, so the sidecar is
	// neither read nor written on a run — here rather than at each call site, so
	// a later caller cannot reintroduce it by forgetting.
	if isChallengeRunKey(worldName) {
		return PlayerState{}, false
	}
	state, ok, err := s.ChatDB.GetPlayerState(accountID, worldName)
	if err != nil {
		log.Printf("zztgo: failed to load player state for account %q in %q: %v", accountID, worldName, err)
		return PlayerState{}, false
	}
	return state, ok
}

func (s *WebSocketServer) persistAccountPlayerState(accountID, worldName string, state PlayerState) {
	if s.ChatDB == nil || accountID == "" {
		return
	}
	// The write half of the rule above (M32.1): what a player picked up inside a
	// challenge run is the run's, and must not follow them back into the world
	// the challenge was measured on.
	if isChallengeRunKey(worldName) {
		return
	}
	if err := s.ChatDB.PutPlayerState(accountID, worldName, state); err != nil {
		log.Printf("zztgo: failed to persist player state for account %q in %q: %v", accountID, worldName, err)
	}
}

// resolveAccountPlayerColor decides which color a signed-in player's ☻ is drawn
// on: the account's, if that account has ever expressed one, and otherwise
// whatever this browser sent (M19.3).
//
// The stored preference wins over the join even when the join carries a color,
// which is what "account-wide" means — the same player is the same color in a
// second world and in a second browser, and a pick left behind in some other
// browser's localStorage does not follow them around. It also means an EXISTING
// document whose Color is empty wins: that document is the picker's "No color"
// row, and the way back to the vanilla player has to beat a stale local pick
// the same way a red one does (the M19.2 lesson, one layer down).
//
// The one write here is the adoption of a first pick: a player who chose a
// color before this store existed (or before they signed in) has it in
// localStorage only, and their first signed-in join moves it to the account
// rather than making them pick it again. Every later change comes through
// /api/preferences, where "no color" can be said out loud.
//
// Nothing here reaches the simulation — SetPlayerColor records no op, which is
// the M19.1 invariant this task must not spend.
func (s *WebSocketServer) resolveAccountPlayerColor(accountID, joinColor string) string {
	if s.ChatDB == nil || accountID == "" {
		return joinColor
	}
	// Decided and, for a first pick, written in one step (M36.27): a check
	// for a document followed by a write of a fresh one would overwrite a
	// document /api/preferences created in between.
	resolved := joinColor
	err := s.ChatDB.UpdateAccountPreferences(accountID, func(prefs *AccountPreferences, exists bool) error {
		if exists {
			resolved = prefs.Color
			return errSkipPreferencesWrite
		}
		if joinColor == "" {
			return errSkipPreferencesWrite
		}
		prefs.Color = joinColor
		return nil
	})
	if err != nil {
		log.Printf("zztgo: failed to resolve the color for account %q: %v", accountID, err)
	}
	return resolved
}

func (s *WebSocketServer) loadAccountPreferences(accountID string) (AccountPreferences, bool) {
	if s.ChatDB == nil || accountID == "" {
		return AccountPreferences{}, false
	}
	prefs, ok, err := s.ChatDB.GetAccountPreferences(accountID)
	if err != nil {
		log.Printf("zztgo: failed to load preferences for account %q: %v", accountID, err)
		return AccountPreferences{}, false
	}
	return prefs, ok
}

func (s *WebSocketServer) accountProfileSummary(accountID string) (string, bool) {
	prefs, ok := s.loadAccountPreferences(accountID)
	if !ok {
		return "", false
	}
	return prefs.Profile.Handle, accountProfileHasPublicFields(prefs.Profile)
}

func accountProfileHasPublicFields(profile AccountProfilePreferences) bool {
	return profile.Handle != "" || profile.DisplayName != "" || len(profile.About) > 0
}

func (s *WebSocketServer) refreshAccountProfile(accountID string, profile AccountProfilePreferences) {
	if accountID == "" {
		return
	}
	handle := profile.Handle
	hasProfile := accountProfileHasPublicFields(profile)
	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	s.mu.Unlock()
	for _, inst := range instances {
		inst.mu.Lock()
		for playerID, player := range inst.RoomManager.players {
			if player.accountID == accountID {
				inst.RoomManager.SetPlayerProfileSummary(playerID, handle, hasProfile)
			}
		}
		inst.mu.Unlock()
	}
}
