package zztgo

import (
	"bytes"
	"encoding/json"
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

func TestWorldListEntriesOmitsWorldsWithoutMuseumMetadata(t *testing.T) {
	entries := WorldListEntries([]string{"EDITED"}, nil)
	if len(entries) != 0 {
		t.Fatalf("entries=%+v, want no entry without Museum metadata", entries)
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

func TestWorldListEntriesInDirIncludesLocalWorldsWithoutMetadata(t *testing.T) {
	dir := t.TempDir()

	byWorld := entriesByWorld(WorldListEntriesInDir(dir, []string{"OBSCURE", "BURGERJ", "TOWN"}, nil))

	if e := byWorld["OBSCURE"]; e.ID != "obscure" || e.Title != "OBSCURE" || e.Author != "Local" {
		t.Fatalf("OBSCURE=%+v, want local fallback metadata", e)
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

// TestWorldListEntriesReportEditors is the M17.11 claim: editing occupancy
// travels the same path as playing occupancy, for catalogued and local worlds
// alike, and a zero count stays absent from the JSON.
func TestWorldListEntriesReportEditors(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"TOWN", "OBSCURE"} {
		if err := os.WriteFile(filepath.Join(dir, name+".ZZT"), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries := WorldListEntriesInDirWithEditors(dir,
		[]string{"TOWN", "OBSCURE"},
		map[string]int{"TOWN": 3},
		map[string]int{"TOWN": 2, "OBSCURE": 1},
	)
	byWorld := entriesByWorld(entries)

	town, ok := byWorld["TOWN"]
	if !ok {
		t.Fatal("TOWN missing from the list")
	}
	if town.Players != 3 {
		t.Errorf("TOWN Players = %d, want 3", town.Players)
	}
	if town.Editors != 2 {
		t.Errorf("TOWN Editors = %d, want 2", town.Editors)
	}

	// A world with editors but no players still reports them.
	obscure, ok := byWorld["OBSCURE"]
	if !ok {
		t.Fatal("OBSCURE missing from the list")
	}
	if obscure.Players != 0 || obscure.Editors != 1 {
		t.Errorf("OBSCURE = {Players:%d Editors:%d}, want {0 1}", obscure.Players, obscure.Editors)
	}

	// omitempty: an unoccupied world carries neither count in the JSON.
	quiet := WorldListEntriesInDirWithEditors(dir, []string{"TOWN"}, nil, nil)
	blob, err := json.Marshal(quiet)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte(`"editors"`)) || bytes.Contains(blob, []byte(`"players"`)) {
		t.Errorf("quiet world should omit both counts, got %s", blob)
	}
}
