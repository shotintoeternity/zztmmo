package zztgo

// M18.9 — the world picker's first screen.
//
// Production's picker went from 65 entries to 134 when the host caught up to
// dev, because every .ZZT in the directory is listed and only the ones the
// museum manifest covers have a title and author. This pins the server half:
// each world is classified, and a world this server dreamed is told apart from
// an uncatalogued community file by the .zwd its generator wrote beside it —
// never by a name pattern, which would misread a community world called
// GEN-something.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestM189WorldKindClassification(t *testing.T) {
	dir := t.TempDir()
	// A world the manifest knows. TOWN is the lobby and is catalogued.
	write := func(name, ext string) {
		if err := os.WriteFile(filepath.Join(dir, name+ext), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("TOWN", ".ZZT")
	write("MERC", ".ZZT")    // real community world, not in the manifest
	write("DREAMED", ".ZZT") // generated here...
	write("DREAMED", ".zwd") // ...as its ZWD source proves

	entries := WorldListEntriesInDir(dir, []string{"TOWN", "MERC", "DREAMED"}, nil)
	kinds := map[string]string{}
	authors := map[string]string{}
	for _, entry := range entries {
		kinds[entry.World] = entry.Kind
		authors[entry.World] = entry.Author
	}

	if kinds["TOWN"] != WorldKindClassic {
		t.Errorf("TOWN kind = %q, want %q", kinds["TOWN"], WorldKindClassic)
	}
	if kinds["MERC"] != WorldKindLocal {
		t.Errorf("MERC kind = %q, want %q (no manifest entry, no .zwd)", kinds["MERC"], WorldKindLocal)
	}
	if kinds["DREAMED"] != WorldKindDreamed {
		t.Errorf("DREAMED kind = %q, want %q", kinds["DREAMED"], WorldKindDreamed)
	}
	if authors["DREAMED"] == "Local" {
		t.Error("a dreamed world should not be credited the same as an uncatalogued one")
	}
}

// TestM189DreamedNeedsTheSiblingNotTheName is the point of using the .zwd: a
// community world whose name merely looks generated must not be reclassified,
// and a dream is only a dream because its source sits beside it.
func TestM189DreamedNeedsTheSiblingNotTheName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"GEN6042D", "GENESIS"} {
		if err := os.WriteFile(filepath.Join(dir, name+".ZZT"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	entries := WorldListEntriesInDir(dir, []string{"GEN6042D", "GENESIS"}, nil)
	for _, entry := range entries {
		if entry.Kind == WorldKindDreamed {
			t.Errorf("%s classified as dreamed with no .zwd beside it", entry.World)
		}
	}

	// Now give one of them its source, exactly as persistGeneratedWorld would.
	if err := os.WriteFile(filepath.Join(dir, "GEN6042D.zwd"), []byte("zwd 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entries = WorldListEntriesInDir(dir, []string{"GEN6042D", "GENESIS"}, nil)
	for _, entry := range entries {
		want := WorldKindLocal
		if entry.World == "GEN6042D" {
			want = WorldKindDreamed
		}
		if entry.Kind != want {
			t.Errorf("%s kind = %q, want %q", entry.World, entry.Kind, want)
		}
	}
}

// TestM189ClassificationSurvivesAnAbsentDirectory guards the callers that pass
// no directory (WorldListEntries, used where the caller only has names): they
// must still classify, just without the dreamed distinction, rather than
// panicking or calling everything dreamed.
func TestM189ClassificationSurvivesAnAbsentDirectory(t *testing.T) {
	entries := WorldListEntriesInDir("", []string{"TOWN", "MERC"}, nil)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.Kind == WorldKindDreamed {
			t.Errorf("%s classified dreamed with no directory to check", entry.World)
		}
		if entry.Kind == "" {
			t.Errorf("%s got no kind at all", entry.World)
		}
	}
}
