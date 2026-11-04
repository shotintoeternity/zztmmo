// CP437 text on the wire.
//
// ZZT's text is bytes, not characters. A scroll, an object's program and a
// board name are CP437: byte 0x82 is é, 0xEC is ∞, 0xDB is a solid block. The
// browser client renders them by indexing the CP437 font atlas with
// `charCodeAt(i) & 0xff`, so what it needs back from a JSON string is the
// original byte.
//
// Go strings are nominally UTF-8, and encoding/json enforces that: it replaces
// every byte that is not valid UTF-8 with U+FFFD rather than failing. Send a
// scroll line holding 0x82 and the client receives U+FFFD, masks it to 0xFD,
// and draws ² — which is how "Café de L'Enfer" became "Caf² de L'Enfer" and the
// poster's ∞ became ². Worse, the editor sends those lines back, so a program
// containing a high byte lost it permanently on the round trip.
//
// The fix is to widen rather than translate: CP437 byte b travels as the rune
// with the same numeric value, which is always valid UTF-8, and the client's
// existing mask recovers b exactly. No table, no client change, and a client
// built before this still behaves identically on ASCII.
//
// Note what this deliberately is NOT. It does not translate CP437 to the
// Unicode characters a human would read (0x82 to U+00E9 "é"): the client wants
// an atlas index, not a character, and every deployed client would break. Nor
// does it apply to text a person typed — chat, account names, world titles —
// which is genuinely UTF-8 and gets folded to CP437 by admitChatText instead.
package zztgo

import (
	"encoding/json"
	"strings"
)

// cp437Encode widens each byte to the rune of the same value, so the result is
// valid UTF-8 that json.Marshal will not rewrite.
func cp437Encode(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	// One allocation, sized up front: a byte below 0x80 is one UTF-8 byte and
	// one at or above it is two, so 2*len(s) always fits (M36.5). The []rune
	// this replaces cost four bytes per input byte and a second allocation for
	// the string it was then copied into.
	var out strings.Builder
	out.Grow(2 * len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x80 {
			out.WriteByte(c)
		} else {
			out.WriteRune(rune(c))
		}
	}
	return out.String()
}

// cp437Decode narrows each rune back to one byte. A rune above 0xFF cannot be
// a CP437 byte — it means a client sent text this codec never produced — so it
// becomes '?' rather than silently contributing its low byte, which would
// desynchronise every column after it.
func cp437Decode(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			out = append(out, '?')
			continue
		}
		out = append(out, byte(r))
	}
	return string(out)
}

// CP437String is a single line of ZZT text: CP437 bytes in Go, widened runes on
// the wire. It converts to and from string for free, so producers assign it
// with a conversion and nothing else changes.
type CP437String string

func (s CP437String) MarshalJSON() ([]byte, error) {
	return json.Marshal(cp437Encode(string(s)))
}

func (s *CP437String) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = CP437String(cp437Decode(raw))
	return nil
}

// CP437Lines is the same for the many-line fields: scroll bodies, help files,
// an object's program.
type CP437Lines []string

func (l CP437Lines) MarshalJSON() ([]byte, error) {
	out := make([]string, len(l))
	for i, line := range l {
		out[i] = cp437Encode(line)
	}
	return json.Marshal(out)
}

func (l *CP437Lines) UnmarshalJSON(data []byte) error {
	var raw []string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(CP437Lines, len(raw))
	for i, line := range raw {
		out[i] = cp437Decode(line)
	}
	*l = out
	return nil
}
