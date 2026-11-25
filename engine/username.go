package zztgo

// M37.10: a signed-in player picks a unique username and is shown by it.
//
// The username is the M24.1 profile handle (3–16 of a–z 0–9 _), which the
// ChatDatabase already holds unique per account. With RequireUsername on, which
// zzt-server sets whenever sign-in is, a signed-in account must have one before
// it plays, edits or runs a challenge, and every name another player sees is
// the username: the name on the Google account (often a real full name) is
// shown to nobody else. With it off, the library keeps its pre-M37.10 naming,
// which is what the older suites that build servers by hand still certify.

import "errors"

const usernameNeededText = "Choose a username before you play."

// ErrUsernameRequired refuses a profile write that would remove the username.
var ErrUsernameRequired = errors.New("zztgo: a username can be changed but not removed")

// accountUsername is the account's claimed username, or "" if it has none.
func (s *WebSocketServer) accountUsername(accountID string) string {
	prefs, ok := s.loadAccountPreferences(accountID)
	if !ok {
		return ""
	}
	return prefs.Profile.Handle
}

// accountPublicName is the name a signed-in account is shown by to others.
func (s *WebSocketServer) accountPublicName(account AuthenticatedAccount) string {
	if s != nil && s.RequireUsername {
		// Never the Google name: an account with no username yet (the door
		// turns it away from play and the editor, but a dream can be asked
		// for from the title screen) is shown as a plain "Player".
		if username := s.accountUsername(account.ID); username != "" {
			return username
		}
		return "Player"
	}
	return account.DisplayName()
}

// needsUsername is the door's second question, after refusedAtTheDoor: a
// signed-in account without a username is turned away before anything is
// joined or spawned, with a notice the client shows on the title screen.
func (s *WebSocketServer) needsUsername(account AuthenticatedAccount, authenticated bool) (ModerationNoticeMessage, bool) {
	if !authenticated || !s.RequireUsername || s.accountUsername(account.ID) != "" {
		return ModerationNoticeMessage{}, false
	}
	return ModerationNoticeMessage{
		Type:   MessageTypeModerationNotice,
		Action: "username",
		Text:   usernameNeededText,
		Ended:  true,
	}, true
}
