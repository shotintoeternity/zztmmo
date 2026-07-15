package zztgo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorldListEntriesUsesMuseumMetadata(t *testing.T) {
	entries := WorldListEntries([]string{"TEEN", "TOWN"}, map[string]int{"TEEN": 2})
	if len(entries) != 2 {
		t.Fatalf("len(entries)=%d, want 2", len(entries))
	}
	if entries[0].World != "TOWN" {
		t.Fatalf("first world=%q, want TOWN lobby first", entries[0].World)
	}
	teen := entries[1]
	if teen.World != "TEEN" || teen.ID != "teen" || teen.Title != "Teen Priest" {
		t.Fatalf("TEEN metadata=%+v, want Museum id/title", teen)
	}
	if teen.Author != "Draco" {
		t.Fatalf("TEEN author=%q, want Draco", teen.Author)
	}
	if teen.Created != "1998" {
		t.Fatalf("TEEN created=%q, want 1998", teen.Created)
	}
	if teen.Players != 2 {
		t.Fatalf("TEEN players=%d, want 2", teen.Players)
	}
}

func TestWorldListEntriesFallsBackToFilename(t *testing.T) {
	entries := WorldListEntries([]string{"EDITED"}, nil)
	if len(entries) != 1 {
		t.Fatalf("len(entries)=%d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.World != "EDITED" || entry.ID != "EDITED" || entry.Title != "EDITED" {
		t.Fatalf("fallback metadata=%+v, want filename fields", entry)
	}
	if entry.Author != "Unknown" {
		t.Fatalf("fallback author=%q, want Unknown", entry.Author)
	}
}

// writeHeaderWorld writes a minimal .ZZT file carrying just the header fields
// worldHeaderTitle reads: board count 0 and a stored world Name.
func writeHeaderWorld(t *testing.T, dir, base, name string) {
	t.Helper()
	buf := make([]byte, 100)
	// buf[0:2] board count 0 -> world-info block starts at offset 2; Name is a
	// Pascal string at info offset 25 (file offset 27).
	buf[27] = byte(len(name))
	copy(buf[28:], name)
	if err := os.WriteFile(filepath.Join(dir, base+".ZZT"), buf, 0o644); err != nil {
		t.Fatalf("write %s: %v", base, err)
	}
}

func entriesByWorld(entries []WorldListEntry) map[string]WorldListEntry {
	m := make(map[string]WorldListEntry, len(entries))
	for _, e := range entries {
		m[e.World] = e
	}
	return m
}

func TestListWorldsExcludesJunkAndUnjoinable(t *testing.T) {
	dir := t.TempDir()
	for _, base := range []string{"-", "--", "---", "----", "_DEATH_", "DOG!", "CAT_TRAP", "REALONE", "GAME2"} {
		if err := os.WriteFile(filepath.Join(dir, base+".ZZT"), []byte{0, 0}, 0o644); err != nil {
			t.Fatalf("write %s: %v", base, err)
		}
	}
	got := ListWorlds(dir)
	want := []string{"GAME2", "REALONE"}
	if len(got) != len(want) {
		t.Fatalf("ListWorlds=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListWorlds=%v, want %v", got, want)
		}
	}
}

func TestWorldListEntriesInDirTitleSources(t *testing.T) {
	dir := t.TempDir()
	// A world absent from the manifest: its header Name is the only real title.
	writeHeaderWorld(t, dir, "OBSCURE", "Moonlit Observatory")
	// A manifest world: the curated title must win over the header Name.
	writeHeaderWorld(t, dir, "BURGERJ", "BURGERJ")
	// The lobby: manifest supplies author but no title, so the filename-equal
	// title survives (the client relabels it "(ZZTMMO Lobby)").
	writeHeaderWorld(t, dir, "TOWN", "TOWN")

	byWorld := entriesByWorld(WorldListEntriesInDir(dir, []string{"OBSCURE", "BURGERJ", "TOWN"}, nil))

	if e := byWorld["OBSCURE"]; e.Title != "Moonlit Observatory" || e.Author != "Unknown" {
		t.Fatalf("OBSCURE=%+v, want header title + Unknown author", e)
	}
	if e := byWorld["BURGERJ"]; e.Title != "Burger Joint" || e.Author != "Madguy" {
		t.Fatalf("BURGERJ=%+v, want manifest title/author over header", e)
	}
	if e := byWorld["TOWN"]; e.Title != "TOWN" || e.Author != "Tim Sweeney" {
		t.Fatalf("TOWN=%+v, want filename-equal title (lobby) + Tim Sweeney", e)
	}
}

func TestWorldListEntriesResolvesAddedManifestWorlds(t *testing.T) {
	byWorld := entriesByWorld(WorldListEntries([]string{"RHYG3-1", "CAVES", "ESPFILE3", "ZFILES3"}, nil))
	cases := []struct{ world, title, author, created string }{
		{"RHYG3-1", "Rhygar 3", "Bongo", "1998"},
		{"CAVES", "Caves of ZZT", "Tim Sweeney", "1991"},
		{"ESPFILE3", "ESP (Evil Sorcerers' Party)", "Bob Pragt, Funk, John W. Wells, Zenith Nadir", "2003"},
		{"ZFILES3", "Z-Files 3", "Zenith Nadir", ""}, // year 0 -> no created string
	}
	for _, c := range cases {
		e := byWorld[c.world]
		if e.Title != c.title || e.Author != c.author || e.Created != c.created {
			t.Fatalf("%s=%+v, want title=%q author=%q created=%q", c.world, e, c.title, c.author, c.created)
		}
	}
}
