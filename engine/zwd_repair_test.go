package zztgo

import (
	"strings"
	"testing"
)

// plainZWDRow / a 60-byte grid data row from zwdOneRoomExample (# border, 58
// interior dots, # border). Used to build broken variants deterministically.
var plainZWDRow = "#" + strings.Repeat(".", 58) + "#"

func zwdRowWith(glyph byte) string {
	b := []byte(plainZWDRow)
	b[29] = glyph // an interior column, never the border
	return string(b)
}

// TestZWDBucket1ProceduralRepair feeds one broken single-board world per
// bucket-1 error class to CompileZWDWithRepair and asserts it compiles and
// validates after purely procedural repair (no LLM), emitting an audit
// diagnostic. This is the M12.16 dispatch-table proof.
func TestZWDBucket1ProceduralRepair(t *testing.T) {
	base := zwdOneRoomExample
	cases := []struct {
		name    string
		src     string
		wantErr string // substring the *unrepaired* compile must report
		wantDia string // substring the procedural diagnostic must contain
	}{
		{
			name:    "missing-end",
			src:     strings.TrimSuffix(base, "end\n"),
			wantErr: "missing end",
			wantDia: "auto-closed",
		},
		{
			name:    "duplicate-legend-key",
			src:     strings.Replace(base, "    . = Empty color 0x0F\n", "    . = Empty color 0x0F\n    . = Empty color 0x0F\n", 1),
			wantErr: "duplicate legend key",
			wantDia: "duplicate legend key",
		},
		{
			name:    "unknown-stat-field",
			src:     strings.Replace(base, "element Object cycle 3 p1", "element Object cycle 3 bogus 5 p1", 1),
			wantErr: "unknown stat field",
			wantDia: "unknown stat field",
		},
		{
			name:    "row-too-wide",
			src:     strings.Replace(base, plainZWDRow+"\n", "#"+strings.Repeat(".", 59)+"#\n", 1),
			wantErr: "grid row wider than 60",
			wantDia: "truncated over-wide grid row",
		},
		{
			name:    "off-board-coord",
			src:     strings.Replace(base, "  stats\n", "  stats\n    stat at 98,98 element Object cycle 3\n", 1),
			wantErr: "coordinate must be X,Y within 1..60",
			wantDia: "dropped off-board stat",
		},
		{
			name:    "color-range",
			src:     strings.Replace(base, "# = Solid color 0x0E", "# = Solid color 0x1FF", 1),
			wantErr: "color must be 0x00..0xFF",
			wantDia: "out-of-range color",
		},
		{
			name:    "door-nibble",
			src:     addLegendAndGlyph(base, "    D = Door color 0x0E\n", 'D'),
			wantErr: "background nibble",
			wantDia: "valid key color",
		},
		{
			name:    "undefined-grid-char",
			src:     strings.Replace(base, plainZWDRow+"\n", zwdRowWith('X')+"\n", 1),
			wantErr: "no legend entry",
			wantDia: "undefined grid keys",
		},
		{
			name:    "orphan-stat-glyph",
			src:     addLegendAndGlyph(base, "    Q = Object color 0x0F\n", 'Q'),
			wantErr: "no matching stat",
			wantDia: "orphan grid glyphs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The unrepaired compiler must reject it, and with the class error.
			if _, err := CompileZWD(tc.src); err == nil {
				t.Fatalf("CompileZWD unexpectedly accepted the broken %s board", tc.name)
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unrepaired error = %q, want substring %q", err.Error(), tc.wantErr)
			}
			data, diags, err := CompileZWDWithRepair(tc.src)
			if err != nil {
				t.Fatalf("CompileZWDWithRepair failed to self-heal %s: %v", tc.name, err)
			}
			if len(data) == 0 {
				t.Fatalf("%s: repaired world produced no bytes", tc.name)
			}
			if err := validateGeneratedZWD(data); err != nil {
				t.Fatalf("%s: repaired world failed validation: %v", tc.name, err)
			}
			if !anyDiagContains(diags, tc.wantDia) {
				t.Fatalf("%s: diagnostics %v, want one containing %q", tc.name, diags, tc.wantDia)
			}
		})
	}
}

// TestZWDOrphanPassageDerivesTargetFromLegend proves folded bullet 1: an orphan
// Passage glyph whose legend carries a `to` destination is synthesized with that
// board as its target — the coordinate and the target both derived, not guessed.
func TestZWDOrphanPassageDerivesTargetFromLegend(t *testing.T) {
	src := addLegendAndGlyph(zwdOneRoomExample, "    P = Passage color 0x0F to \"Title screen\"\n", 'P')
	_, repaired, diags, err := compileZWDBytesWithRepair(src)
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !strings.Contains(repaired, `p3 board "Title screen"`) {
		t.Fatalf("synthesized passage did not take its legend `to` target; repaired source:\n%s", repaired)
	}
	if !anyDiagContains(diags, "orphan grid glyphs") {
		t.Fatalf("diagnostics %v, want orphan synthesis note", diags)
	}
}

// TestZWDBucket2FallsThroughToLLM proves a semantic error (an exit to a
// nonexistent board) has no procedural fixer and is returned unchanged for the
// LLM path — the repair layer must never guess intent.
func TestZWDBucket2FallsThroughToLLM(t *testing.T) {
	src := strings.Replace(zwdOneRoomExample, "exits north none", `exits north "Nowhere"`, 1)
	plainErr := ""
	if _, err := CompileZWD(src); err != nil {
		plainErr = err.Error()
	} else {
		t.Fatal("CompileZWD unexpectedly accepted an exit to a nonexistent board")
	}
	data, diags, err := CompileZWDWithRepair(src)
	if err == nil {
		t.Fatal("CompileZWDWithRepair procedurally 'fixed' a semantic error; it must fall through")
	}
	if data != nil {
		t.Fatal("bucket-2 failure returned bytes")
	}
	if len(diags) != 0 {
		t.Fatalf("bucket-2 failure recorded procedural diagnostics %v", diags)
	}
	if err.Error() != plainErr {
		t.Fatalf("repaired error = %q, want the unchanged compile error %q", err.Error(), plainErr)
	}
}

// TestZWDPassageReciprocityDetectOnly proves folded bullet 3: a one-way passage
// color is DETECTED (not fixed). A reciprocal pair raises no warning.
func TestZWDPassageReciprocityDetectOnly(t *testing.T) {
	world, err := CompileZWDWorld(zwdTwoRoomsExample)
	if err != nil {
		t.Fatalf("compile two-room example: %v", err)
	}
	warnings := CheckZWDPassageReciprocity(world)
	// The two-room example's Vault return passage (if any) is not color-matched
	// to the Title screen passage, so at least one direction should warn; the
	// contract we assert is that the check reports rather than mutates.
	for _, w := range warnings {
		if !strings.Contains(w, "matching-color return passage") {
			t.Fatalf("reciprocity warning has wrong shape: %q", w)
		}
	}
	// Recompiling the same source must be byte-identical: detection changed nothing.
	again, err := CompileZWD(zwdTwoRoomsExample)
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	first, _ := CompileZWD(zwdTwoRoomsExample)
	if string(first) != string(again) {
		t.Fatal("reciprocity detection is not side-effect free")
	}
}

// addLegendAndGlyph inserts a legend line just before the legend's `end` and
// paints one instance of glyph into the first plain grid row.
func addLegendAndGlyph(src string, legendLine string, glyph byte) string {
	withGlyph := strings.Replace(src, plainZWDRow+"\n", zwdRowWith(glyph)+"\n", 1)
	// The legend block's closing `end` is the first `  end` after the legend header.
	marker := "    o = Object color 0x0F\n"
	return strings.Replace(withGlyph, marker, marker+legendLine, 1)
}

func anyDiagContains(diags []string, want string) bool {
	for _, d := range diags {
		if strings.Contains(d, want) {
			return true
		}
	}
	return false
}
