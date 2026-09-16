package zztgo

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCP437WireRoundTrip is the bug from the Cafe scroll and the Yanni poster:
// a CP437 byte went through encoding/json, came back U+FFFD, and the client's
// `charCodeAt(i) & 0xff` drew it as 0xFD — "²". Every case here asserts the
// byte the client actually recovers, which is the only thing that matters.
func TestCP437WireRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"ascii-untouched", "POSTER:"},
		{"e-acute", "Caf\x82 de L'Enfer"},       // 0x82, the reported bug
		{"infinity", "\xec_\xec"},               // 0xEC, the poster
		{"accidental-valid-utf8", "\xc3\xb4"},   // ├┤ — decodes as "ô" and slips
		{"box-drawing-run", "\xc9\xcd\xcd\xbb"}, // ╔══╗
		{"solid-block", "\xdb\xdb\xdb"},         // ▓ walls in a sign
		{"all-256-bytes", allBytes()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(CP437String(tc.in))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// What the browser does with the JSON string it receives.
			var wire string
			if err := json.Unmarshal(b, &wire); err != nil {
				t.Fatalf("unmarshal to string: %v", err)
			}
			var got []byte
			for _, r := range wire {
				got = append(got, byte(r))
			}
			if string(got) != tc.in {
				t.Errorf("client recovers % x, want % x (json was %s)", got, tc.in, b)
			}
			// And the server's own decode, for the inbound direction.
			var back CP437String
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if string(back) != tc.in {
				t.Errorf("server round trip = % x, want % x", back, tc.in)
			}
		})
	}
}

// TestCP437LinesRoundTrip covers the editor's program text, which travels both
// ways: a program holding a high byte used to lose it permanently on save.
func TestCP437LinesRoundTrip(t *testing.T) {
	in := CP437Lines{"@Isaac", "#char \xec", "Caf\x82", ""}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back CP437Lines
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back) != len(in) {
		t.Fatalf("got %d lines, want %d", len(back), len(in))
	}
	for i := range in {
		if back[i] != in[i] {
			t.Errorf("line %d = %q, want %q", i, back[i], in[i])
		}
	}
}

// TestCP437PlainStringStillCorrupts pins the reason this type has to exist. If
// this ever stops failing to round-trip, encoding/json changed and the codec
// can be revisited.
func TestCP437PlainStringStillCorrupts(t *testing.T) {
	b, _ := json.Marshal("Caf\x82")
	if !strings.Contains(string(b), "�") {
		t.Fatalf("encoding/json no longer substitutes U+FFFD: %s", b)
	}
}

// TestCP437DecodeRejectsWideRunes: a rune above 0xFF is not a CP437 byte. It
// becomes one '?' so the column count survives.
func TestCP437DecodeRejectsWideRunes(t *testing.T) {
	if got := cp437Decode("a☺bc"); got != "a?bc" {
		t.Errorf("cp437Decode = %q, want %q", got, "a?bc")
	}
}

func allBytes() string {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return string(b)
}

// TestCP437ScrollEventReachesClientIntact is the reported bug end to end on the
// server side: a ScrollEvent carrying the Cafe line goes through ProtocolEvents
// and out through the same EventMessage the socket writes, and the bytes the
// browser recovers are the bytes ZZT put in the world file.
func TestCP437ScrollEventReachesClientIntact(t *testing.T) {
	const title = "Isaac"
	body := []string{
		"at the Caf\x82 de L'Enfer for drinks, and", // 0x82 = é
		"Live at the House of Blues \xec_\xec",      // 0xEC = ∞
		"\xc9\xcd\xcd\xbb",                          // ╔══╗, the accidental-UTF-8 pair
	}

	events := ProtocolEvents([]Event{ScrollEvent{StatId: 3, PlayerStatId: 0, Title: title, Lines: body}})
	if len(events) != 1 || events[0].Type != "scroll" {
		t.Fatalf("ProtocolEvents = %+v, want one scroll", events)
	}

	raw, err := json.Marshal(EventMessage{Type: MessageTypeEvent, Event: events[0]})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "�") {
		t.Fatalf("U+FFFD on the wire — the scroll is still being corrupted: %s", raw)
	}

	// Decode as the browser does: JSON string, then charCodeAt(i) & 0xff.
	var seen struct {
		Event struct {
			Title string   `json:"title"`
			Lines []string `json:"lines"`
		} `json:"event"`
	}
	if err := json.Unmarshal(raw, &seen); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := browserBytes(seen.Event.Title); got != title {
		t.Errorf("title: browser draws %q, want %q", got, title)
	}
	for i, want := range body {
		if got := browserBytes(seen.Event.Lines[i]); got != want {
			t.Errorf("line %d: browser draws % x, want % x", i, got, want)
		}
	}
}

// browserBytes is main.ts's `text.charCodeAt(i) & 0xff`, which is how every
// character in a text window becomes an index into the CP437 font atlas.
func browserBytes(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return string(out)
}
