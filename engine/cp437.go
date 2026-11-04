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
// does it apply to text a person typed — chat, player names, high-score names —
// which arrives as genuine UTF-8. That is encoded to CP437 where it is admitted
// (encodeTypedCP437, M37.5), and from then on it is CP437 bytes like any other
// ZZT text and travels as CP437String.
package zztgo

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// cp437ByteForRune is cp437HighRunes inverted: the CP437 byte that draws a
// Unicode character, for the characters at 0x80-0xFF that have one.
var cp437ByteForRune = func() map[rune]byte {
	m := make(map[rune]byte, len(cp437HighRunes))
	for i, r := range cp437HighRunes {
		m[r] = byte(0x80 + i)
	}
	return m
}()

// foldTypographic is the punctuation fold foldWordmark has always applied:
// characters CP437 cannot draw that have an obvious ASCII stand-in.
func foldTypographic(r rune) (string, bool) {
	switch r {
	case '—', '–', '―', '−':
		return "-", true // em/en/horizontal-bar dash, minus sign
	case '‘', '’', '‚':
		return "'", true // curly single quotes
	case '“', '”', '„':
		return `"`, true // curly double quotes
	case '…':
		return "...", true // ellipsis
	case '×':
		return "x", true // multiplication sign
	}
	return "", false
}

// encodeTypedCP437 turns text a person typed into the CP437 bytes the client
// draws one glyph per byte (M37.5). It replaces foldWordmark for chat, player
// names and high-score names, which used to lose every accent ("José" became
// "Jos"):
//
//   - printable ASCII is its own CP437 byte and passes through;
//   - the typographic punctuation foldWordmark folds is folded the same way;
//   - a character CP437 has (é, ü, ñ, £, ß, the box-drawing set...) becomes its
//     CP437 byte, so "José" is stored as "Jos\x82";
//   - a no-break space becomes a space, so it cannot make a blank-looking name;
//   - anything else, including control characters, is dropped, as before.
//
// Admit text ONCE. The old fold was idempotent because its output was ASCII;
// this one's output is CP437, which is not UTF-8. A lone high byte read back
// as UTF-8 is an invalid one-byte sequence, and that is kept as CP437 already
// (text decoded from JSON is always valid UTF-8, where a literal U+FFFD is
// three bytes), so re-admitting Latin text such as "Jos\x82" changes nothing.
// That pass-through is a safety net, not a contract: a CP437 pair that happens
// to be valid UTF-8 is decoded as something else. ╞é is C6 82, which is also
// U+0182, and re-admitting it drops both glyphs. So text that has been
// admitted is set through setAdmittedPlayerName, setAdmittedPlayerIdentity or
// recordAdmittedHighScore, never passed back in here
// (TestM375RecordedNameReplaysAsTheSameBytes pins the replay path).
func encodeTypedCP437(s string) string {
	var b []byte
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			if c := s[i-1]; c >= 0x80 && c != 0xFF {
				b = append(b, c) // already CP437 (0xFF is the no-break space)
			}
		case r >= 0x20 && r < 0x7F:
			b = append(b, byte(r))
		case r == 0xA0:
			b = append(b, ' ')
		default:
			if folded, ok := foldTypographic(r); ok {
				b = append(b, folded...)
			} else if c, ok := cp437ByteForRune[r]; ok {
				b = append(b, c)
			}
			// Unrepresentable rune: drop it rather than emit a mystery byte.
		}
	}
	return string(b)
}

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
