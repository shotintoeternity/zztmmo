package zztgo

import (
	"encoding/json"
	"testing"
)

// M36.5: the cost of widening CP437 for the wire. A scroll line with high
// bytes, and a whole help file marshalled as CP437Lines (re-encoded on every
// help send).

var m365Line = "\xc9\xcd\xcd\xcd Caf\x82 de L'Enfer \xcd\xcd\xcd\xbb  \xdb\xdb\xb2\xb1\xb0  \xec"

func BenchmarkCP437EncodeHighBytes(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = cp437Encode(m365Line)
	}
}

func BenchmarkCP437LinesMarshalHelp(b *testing.B) {
	HelpDir = "."
	lines := CP437Lines(HelpFileLines("LANGREF.HLP"))
	if len(lines) == 0 {
		b.Fatal("LANGREF.HLP has no lines")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(lines); err != nil {
			b.Fatal(err)
		}
	}
}

// The builder-based encoder must produce exactly what widening each byte to
// a rune did, for every byte value.
func TestM365EncodeMatchesRuneWidening(t *testing.T) {
	var all []byte
	for c := 0; c < 256; c++ {
		all = append(all, byte(c))
	}
	in := string(all)
	want := make([]rune, len(in))
	for i := 0; i < len(in); i++ {
		want[i] = rune(in[i])
	}
	if got := cp437Encode(in); got != string(want) {
		t.Fatalf("cp437Encode differs from rune widening")
	}
	if cp437Decode(cp437Encode(in)) != in {
		t.Fatal("encode/decode does not round-trip every byte")
	}
}
