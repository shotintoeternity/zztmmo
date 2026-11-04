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
// encoded the way chat and high-score names are (encodeTypedCP437, M37.5):
// printable ASCII passes, typographic punctuation folds, a character with a
// CP437 form becomes that byte, and anything else is dropped, so "José" is
// stored as "Jos\x82" and drawn as "José". A name with nothing printable left
// becomes "Player" rather than an empty string that every caller would have to
// replace. It is the identity on any name the client's own prompt can produce.
//
// Admit a name once. Its output is CP437, not UTF-8, so a name that has been
// admitted is set with setAdmittedPlayerName rather than passed back in here.
func admitPlayerName(raw string) string {
	name := strings.TrimSpace(encodeTypedCP437(raw))
	if len(name) > playerNameMax {
		name = strings.TrimSpace(name[:playerNameMax]) // one byte per glyph: a byte cut is a character cut
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
