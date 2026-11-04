package zztgo

import "strings"

// playerNameMax is the launch prompt's field width (POPUP_FIELD_WIDTH in the
// client, PopupPromptString's TextWindowWidth-16), the longest name a guest can
// type; an account's display name is held to the same.
const playerNameMax = highScoreNameMax

// admitPlayerName is the one fold every player name goes through on its way in
// (M36.6): a guest's typed name and an account's Google display name alike. A
// name is text a person typed, so it arrives as UTF-8, but the client draws it
// one glyph per byte against the CP437 atlas — the editor's presence list, the
// moderation and profile popups — so a raw "José" came out as "JosΘ". It is
// folded the way chat and high-score names are (foldWordmark): printable ASCII
// passes, typographic punctuation folds, anything else is dropped, so "José"
// is stored as "Jos". A name with nothing printable left becomes "Player"
// rather than an empty string that every caller would have to replace. It is
// the identity on any name the client's own prompt can produce.
func admitPlayerName(raw string) string {
	name := strings.TrimSpace(foldWordmark(raw))
	if len(name) > playerNameMax {
		name = strings.TrimSpace(name[:playerNameMax]) // all ASCII: a byte cut is a character cut
	}
	if name == "" && strings.TrimSpace(raw) != "" {
		return "Player"
	}
	return name
}

// admitDebugCommandText folds the `?` prompt's text the same way (M36.6). It
// is typed text that becomes simulation state — `+flag` writes World.Info.Flags,
// which StateHash covers — so a multi-byte flag name must not be stored as
// UTF-8 bytes the board's OOP could never spell.
func admitDebugCommandText(raw string) string {
	return foldWordmark(raw)
}
