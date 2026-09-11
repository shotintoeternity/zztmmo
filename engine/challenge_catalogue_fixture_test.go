package zztgo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The challenge catalogue this repository ships is EMPTY (challenge.go): Gem
// Dash came off the board before beta. The machinery stayed, so the tests that
// prove it still need a challenge to point at — and they bring their own rather
// than reading one out of the product.
//
// gemDashFixture is that row. It is the course the M32/M34 suites were written
// against, kept here so those suites keep measuring the same game.

func gemDashFixture() ChallengeDefinition {
	return ChallengeDefinition{
		ID:    "gem-dash",
		Title: "Gem Dash",
		Summary: []string{
			"Collect all three gems.",
			"Fewest ticks wins.",
		},
		World:   ChallengeWorldName,
		Board:   1,
		Goal:    ChallengeGoal{Kind: ChallengeGoalGems, Amount: 3},
		Version: 1,
	}
}

// withChallengeCatalogue installs a catalogue for one test and puts the shipped
// one back afterwards. A package-level swap is safe here because no test in this
// package calls t.Parallel; if one ever does, this is what it has to answer to.
func withChallengeCatalogue(t *testing.T, defs ...ChallengeDefinition) {
	t.Helper()
	previous := challengeCatalogue
	challengeCatalogue = defs
	t.Cleanup(func() { challengeCatalogue = previous })
}

// writeChallengeCatalogueFile writes a catalogue where a SUBPROCESS can read it
// — the real-browser suite runs the production binary, which a package-level
// swap cannot reach, so it is handed -challenges instead.
func writeChallengeCatalogueFile(t *testing.T, dir string, defs ...ChallengeDefinition) string {
	t.Helper()
	data, err := json.MarshalIndent(defs, "", "  ")
	if err != nil {
		t.Fatalf("marshal challenge catalogue: %v", err)
	}
	path := filepath.Join(dir, "challenges.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestLoadChallengeCatalogue covers the loader the flag calls: a good file lands,
// and every way a bad row could become a path or an ambiguous id is refused by
// name rather than accepted and dealt with later.
func TestLoadChallengeCatalogue(t *testing.T) {
	dir := t.TempDir()

	t.Run("an empty path leaves the shipped catalogue alone", func(t *testing.T) {
		withChallengeCatalogue(t)
		if err := LoadChallengeCatalogue(""); err != nil {
			t.Fatalf("LoadChallengeCatalogue(\"\") = %v", err)
		}
		if got := ChallengeCatalogue(); len(got) != 0 {
			t.Fatalf("catalogue = %+v, want none", got)
		}
	})

	t.Run("a good file loads and is addressable", func(t *testing.T) {
		withChallengeCatalogue(t)
		path := writeChallengeCatalogueFile(t, dir, gemDashFixture())
		if err := LoadChallengeCatalogue(path); err != nil {
			t.Fatalf("LoadChallengeCatalogue: %v", err)
		}
		def, ok := ChallengeByID("gem-dash")
		if !ok || def.World != ChallengeWorldName || def.Goal.Amount != 3 {
			t.Fatalf("ChallengeByID(gem-dash) = %+v, %v", def, ok)
		}
	})

	t.Run("a missing file is an error, not an empty catalogue", func(t *testing.T) {
		withChallengeCatalogue(t, gemDashFixture())
		if err := LoadChallengeCatalogue(filepath.Join(dir, "nosuch.json")); err == nil {
			t.Fatal("a missing catalogue file must be reported")
		}
	})

	bad := []struct {
		name string
		def  ChallengeDefinition
	}{
		{"a traversing id", ChallengeDefinition{ID: "../gem-dash", Title: "T", World: ChallengeWorldName, Goal: ChallengeGoal{Kind: ChallengeGoalGems}, Version: 1}},
		{"an upper-case id", ChallengeDefinition{ID: "GemDash", Title: "T", World: ChallengeWorldName, Goal: ChallengeGoal{Kind: ChallengeGoalGems}, Version: 1}},
		{"an empty id", ChallengeDefinition{ID: "", Title: "T", World: ChallengeWorldName, Goal: ChallengeGoal{Kind: ChallengeGoalGems}, Version: 1}},
		{"no title", ChallengeDefinition{ID: "x", Title: "  ", World: ChallengeWorldName, Goal: ChallengeGoal{Kind: ChallengeGoalGems}, Version: 1}},
		{"a world that is a path", ChallengeDefinition{ID: "x", Title: "T", World: "../TOWN", Goal: ChallengeGoal{Kind: ChallengeGoalGems}, Version: 1}},
		{"a world with a dot", ChallengeDefinition{ID: "x", Title: "T", World: "TOWN.ZZT", Goal: ChallengeGoal{Kind: ChallengeGoalGems}, Version: 1}},
		{"an unknown goal", ChallengeDefinition{ID: "x", Title: "T", World: ChallengeWorldName, Goal: ChallengeGoal{Kind: "vibes"}, Version: 1}},
		{"no version", ChallengeDefinition{ID: "x", Title: "T", World: ChallengeWorldName, Goal: ChallengeGoal{Kind: ChallengeGoalGems}}},
	}
	for _, row := range bad {
		t.Run(row.name+" is refused", func(t *testing.T) {
			withChallengeCatalogue(t)
			path := writeChallengeCatalogueFile(t, t.TempDir(), row.def)
			if err := LoadChallengeCatalogue(path); err == nil {
				t.Fatalf("LoadChallengeCatalogue admitted %+v", row.def)
			}
			if got := ChallengeCatalogue(); len(got) != 0 {
				t.Fatalf("a refused file still changed the catalogue: %+v", got)
			}
		})
	}

	t.Run("a duplicate id is refused", func(t *testing.T) {
		withChallengeCatalogue(t)
		path := writeChallengeCatalogueFile(t, t.TempDir(), gemDashFixture(), gemDashFixture())
		if err := LoadChallengeCatalogue(path); err == nil {
			t.Fatal("two rows with one id must be refused")
		}
	})
}

// TestShippedCatalogueOffersNoChallenge is the product decision, asserted so it
// cannot be undone by accident: nothing is offered, and every path that reads
// the catalogue says so calmly rather than panicking or inventing a row.
func TestShippedCatalogueOffersNoChallenge(t *testing.T) {
	if got := ChallengeCatalogue(); len(got) != 0 {
		t.Fatalf("the shipped catalogue offers %+v; it must offer nothing", got)
	}
	if _, ok := ChallengeByID("gem-dash"); ok {
		t.Fatal("gem-dash is still addressable in the shipped catalogue")
	}
	if def, ok := ChallengeForDate(time.Unix(1_757_000_000, 0).UTC()); ok {
		t.Fatalf("ChallengeForDate returned %+v; with no rows there is no challenge today", def)
	}
}
