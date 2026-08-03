package zztgo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// M18.13 — one picker entry per world a player can actually join.
//
// The picker's identity for a world used to be the filename as written on
// disk; the join path's identity is that name run through SanitizeSaveName.
// Where those disagreed the picker showed two cards that opened one file, or a
// card that opened no file at all. These tests state both halves through
// synthesized name lists, because a temp-directory test cannot express the
// collision on a case-insensitive filesystem (macOS APFS): there TOWN.ZZT and
// town.zzt are the same file, which is why the owner saw this on the Linux
// host and not on a workstation.

// alwaysJoinable stands in for a filesystem where <NAME>.ZZT opens.
func alwaysJoinable(string) bool { return true }

func TestJoinableWorldNamesCollapsesCaseVariantsOntoOneEntry(t *testing.T) {
	got := joinableWorldNames([]string{"TOWN.ZZT", "town.zzt", "Town.Zzt"}, alwaysJoinable, nil)
	if len(got) != 1 || got[0] != "TOWN" {
		t.Fatalf("joinableWorldNames=%v, want exactly [TOWN]", got)
	}
}

// The order the directory is read in must not decide the answer: the identity
// is what the join path resolves, not whichever spelling came first.
func TestJoinableWorldNamesIdentityIsIndependentOfReadOrder(t *testing.T) {
	for _, names := range [][]string{
		{"town.zzt", "TOWN.ZZT"},
		{"TOWN.ZZT", "town.zzt"},
	} {
		got := joinableWorldNames(names, alwaysJoinable, nil)
		if len(got) != 1 || got[0] != "TOWN" {
			t.Fatalf("joinableWorldNames(%v)=%v, want exactly [TOWN]", names, got)
		}
	}
}

// The dead-entry half. On a case-sensitive filesystem a lone town.zzt is not
// openable as TOWN.ZZT, so it must not be offered as joinable — and it must be
// reported rather than dropped in silence.
func TestJoinableWorldNamesDropsAndReportsWhatTheJoinPathCannotOpen(t *testing.T) {
	var reported []string
	got := joinableWorldNames(
		[]string{"town.zzt", "REALONE.ZZT"},
		func(world string) bool { return world == "REALONE" },
		func(world, evidence string) { reported = append(reported, world+" <- "+evidence) },
	)
	if len(got) != 1 || got[0] != "REALONE" {
		t.Fatalf("joinableWorldNames=%v, want exactly [REALONE]", got)
	}
	if len(reported) != 1 || reported[0] != "TOWN <- town.zzt" {
		t.Fatalf("reported=%v, want the dropped name and the file that suggested it", reported)
	}
}

// The no-regression half of the same rule: where the filesystem folds case,
// town.zzt really does answer to TOWN.ZZT, so the world stays listed. Nothing
// joinable before becomes unlistable.
func TestJoinableWorldNamesKeepsWorldsACaseFoldingFilesystemOpens(t *testing.T) {
	got := joinableWorldNames([]string{"town.zzt"}, alwaysJoinable, nil)
	if len(got) != 1 || got[0] != "TOWN" {
		t.Fatalf("joinableWorldNames=%v, want exactly [TOWN]", got)
	}
}

// ListWorlds itself, over a real directory, still lists what it always did.
func TestListWorldsListsEachWorldOnce(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"TOWN.ZZT", "REALONE.ZZT", "GAME2.ZZT"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{0, 0}, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	got := ListWorlds(dir)
	want := []string{"GAME2", "REALONE", "TOWN"}
	if len(got) != len(want) {
		t.Fatalf("ListWorlds=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListWorlds=%v, want %v", got, want)
		}
	}
}

// The picker's own collapse, stated the way the owner saw the defect: two
// entries, same title, same author, same ID, both opening TOWN.ZZT.
func TestWorldListEntriesCollapsesCaseVariantsOntoOneCard(t *testing.T) {
	entries := WorldListEntriesInDir(t.TempDir(), []string{"TOWN", "town"}, map[string]int{"TOWN": 2})
	if len(entries) != 1 {
		t.Fatalf("entries=%+v, want one card for TOWN", entries)
	}
	if entries[0].World != "TOWN" {
		t.Fatalf("World=%q, want TOWN — the name the join path opens", entries[0].World)
	}
	// The surviving name is also the key the occupancy maps are built under
	// (instances are keyed on the sanitized name), which "town" never matched.
	if entries[0].Players != 2 {
		t.Fatalf("Players=%d, want 2", entries[0].Players)
	}
	if entries[0].Kind != WorldKindClassic || entries[0].Author != "Tim Sweeney" {
		t.Fatalf("entry=%+v, want the classic card intact", entries[0])
	}
}

// The surviving name must be the stem the sidecars use, or a dreamed world
// reclassifies as `local` and M14.4's title sidecar stops being found.
func TestWorldListEntriesCollapseKeepsDreamedKindAndSidecarTitle(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"GEN12345.ZZT", "GEN12345.zwd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	meta, err := json.Marshal(WorldMeta{Title: "The Salt Cellar", Author: "Dreamer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "GEN12345.meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	// The lower-case spelling arrives first, so a first-wins collapse would
	// keep it and lose both sidecars.
	entries := WorldListEntriesInDir(dir, []string{"gen12345", "GEN12345"}, nil)
	if len(entries) != 1 {
		t.Fatalf("entries=%+v, want one card", entries)
	}
	got := entries[0]
	if got.World != "GEN12345" {
		t.Fatalf("World=%q, want GEN12345", got.World)
	}
	if got.Kind != WorldKindDreamed {
		t.Fatalf("Kind=%q, want %q", got.Kind, WorldKindDreamed)
	}
	if got.Title != "The Salt Cellar" || got.Author != "Dreamer" {
		t.Fatalf("entry=%+v, want the .meta.json title and author", got)
	}
}

// The other collapse the owner's report names is NOT this bug: a Museum zip
// holding several .ZZT files registers each as an alias for one manifest row,
// so those worlds share a curated title while remaining separately joinable
// files. Dropping them would hide real worlds.
func TestWorldListEntriesKeepsMuseumAliasFanOutSeparatelyListed(t *testing.T) {
	entries := WorldListEntries([]string{"RHYG3-1", "RHYG3-2", "RHYG3-3"}, nil)
	if len(entries) != 3 {
		t.Fatalf("entries=%+v, want one entry per file", entries)
	}
	byWorld := entriesByWorld(entries)
	for _, world := range []string{"RHYG3-1", "RHYG3-2", "RHYG3-3"} {
		e, ok := byWorld[world]
		if !ok {
			t.Fatalf("%s missing from %+v", world, entries)
		}
		if e.Title != "Rhygar 3" {
			t.Fatalf("%s Title=%q, want the shared Museum title", world, e.Title)
		}
	}
}
