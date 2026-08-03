package zztgo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// M14.4 — a world's identity is separate from its display name.
//
// The 8-character stem was the primary key AND the name a player reads. Three
// defects came out of that one conflation (M16.17b, M16.17d, M18.11) and each
// was closed with its own patch on the same seam. This suite pins the seam
// itself, on the shape the owner chose 2026-08-02: the identity stays the stem
// but is MINTED collision-free, and the title moves to a NAME.meta.json sidecar.

// --------------------------------------------------------------------------
// Minting
// --------------------------------------------------------------------------

// TestM144MintingIsCheckedAgainstTheDirectoryNotHashedAndHoped is the DoD's
// "minting is collision-checked, not hash-and-hope".
//
// generatedFallbackSaveName is GEN%05X — 20 bits, which at ~1000 worlds is a
// near-even-odds birthday collision. It survives as a SEED. What must not
// survive is taking its word for it: every name minting returns is checked
// against the hosting directory first.
func TestM144MintingIsCheckedAgainstTheDirectoryNotHashedAndHoped(t *testing.T) {
	dir := t.TempDir()

	// A free stem is taken as-is: minting is not gratuitous renaming.
	name, err := mintWorldName(dir, "CASTLE", nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "CASTLE" {
		t.Fatalf("minted %q over a free stem, want CASTLE", name)
	}
	releaseWorldName(dir, name)

	// An occupied stem yields the next member of its family, not a refusal and
	// not a hash.
	if err := os.WriteFile(filepath.Join(dir, "CASTLE.ZZT"), []byte("someone's world"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err = mintWorldName(dir, "CASTLE", nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "CASTLE2" {
		t.Fatalf("minted %q beside CASTLE.ZZT, want CASTLE2", name)
	}
	releaseWorldName(dir, name)

	// A sidecar with no world beside it still blocks the stem: it names an
	// account, and inheriting it would hand a stranger's world to whoever mints
	// the name next.
	if err := writeWorldAccess(dir, "CASTLE2", WorldAccess{OwnerAccountID: "acct-ada", OwnerName: "Ada"}); err != nil {
		t.Fatal(err)
	}
	name, err = mintWorldName(dir, "CASTLE", nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "CASTLE3" {
		t.Fatalf("minted %q past an orphaned .access.json, want CASTLE3", name)
	}
	releaseWorldName(dir, name)

	// M18.11 holds inside minting, so the classics are simply not in the
	// namespace rather than carved out of it after the fact.
	name, err = mintWorldName(dir, "TOWN", nil)
	if err != nil {
		t.Fatal(err)
	}
	if name == "TOWN" {
		t.Fatal("minting took a canonical Museum world's name")
	}
	if _, err := os.Stat(filepath.Join(dir, "TOWN.ZZT")); err == nil {
		t.Fatal("minting a name near TOWN wrote something")
	}
	releaseWorldName(dir, name)
}

// TestM144MintedNamesStayInsideTheDOSNamespace is the constraint the owner's
// "keep the 8-character stem" choice rests on: nothing on disk moves, so every
// minted name has to be a legal .ZZT/.SAV/.HI stem and a legal ?world= value.
// The family truncates the stem rather than extending it.
func TestM144MintedNamesStayInsideTheDOSNamespace(t *testing.T) {
	dir := t.TempDir()
	// Eight characters already: every variant has to give a character back.
	for i := 0; i < 12; i++ {
		name, err := mintWorldName(dir, "LIGHTHOU", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(name) > SaveNameMaxLength {
			t.Fatalf("minted %q (%d chars), want at most %d", name, len(name), SaveNameMaxLength)
		}
		if _, err := SanitizeSaveName(name); err != nil {
			t.Fatalf("minted %q, which is not a name any path can resolve: %v", name, err)
		}
		if !hasAlphanumeric(name) {
			t.Fatalf("minted %q, which ListWorlds drops — a world nobody can see or join", name)
		}
		// Hold it, so the next pass has to find a different one.
		if err := os.WriteFile(filepath.Join(dir, name+".ZZT"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		releaseWorldName(dir, name)
	}
}

// TestM144ConcurrentMintsNeverAgree is the case the birthday problem was only
// half of. Two dreams painting at once (the shipped ZZT_GENERATION_CONCURRENCY=2)
// can carry the same plan title, and neither writes a file for the other to see
// for the minutes they spend painting — so checking the directory alone would
// still hand both the same name and let the second overwrite the first.
func TestM144ConcurrentMintsNeverAgree(t *testing.T) {
	dir := t.TempDir()
	const racers = 16

	var wg sync.WaitGroup
	names := make([]string, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			names[i], errs[i] = mintWorldName(dir, "HARBOUR", nil)
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[string]int, racers)
	for i, name := range names {
		if errs[i] != nil {
			t.Fatalf("racer %d: %v", i, errs[i])
		}
		if prev, dup := seen[name]; dup {
			t.Fatalf("racers %d and %d both minted %q — one dream would overwrite the other", prev, i, name)
		}
		seen[name] = i
	}
	for _, name := range names {
		releaseWorldName(dir, name)
	}

	// And a reservation is not a leak: released names come back.
	if !reserveWorldName(dir, names[0]) {
		t.Errorf("%q is still held after it was released", names[0])
	}
	releaseWorldName(dir, names[0])
}

// --------------------------------------------------------------------------
// The title
// --------------------------------------------------------------------------

// TestM144TitleAndIdentityAreSeparatelyAddressable is the first half of the
// DoD. A title is metadata: free to collide, free of the DOS charset, and read
// by the picker rather than by any path.
func TestM144TitleAndIdentityAreSeparatelyAddressable(t *testing.T) {
	dir := t.TempDir()
	for _, world := range []string{"HARBOUR", "HARBOUR2"} {
		if err := os.WriteFile(filepath.Join(dir, world+".ZZT"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// The same title on both: identities collide, titles do not.
		if err := writeWorldMeta(dir, world, WorldMeta{Title: "The Lighthouse Keeper's Diary", Author: "Ada"}); err != nil {
			t.Fatal(err)
		}
	}

	entries := WorldListEntriesInDir(dir, ListWorlds(dir), nil)
	if len(entries) != 2 {
		t.Fatalf("picker listed %d world(s), want 2: %+v", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.Title != "The Lighthouse Keeper's Diary" {
			t.Errorf("%s: title = %q, want the sidecar's", entry.World, entry.Title)
		}
		if entry.Author != "Ada" {
			t.Errorf("%s: author = %q, want Ada", entry.World, entry.Author)
		}
		// The identity is untouched by any of that — it is still what a path,
		// a ?world= parameter and the Instances map are keyed on.
		if _, err := SanitizeSaveName(entry.World); err != nil {
			t.Errorf("identity %q stopped being resolvable: %v", entry.World, err)
		}
	}
	if entries[0].World == entries[1].World {
		t.Fatal("two worlds share one identity")
	}
}

// TestM144AClassicKeepsTheMuseumsTitle guards the direction the sidecar must
// not travel. A classic's title belongs to the Museum manifest; a sidecar
// dropped beside it — by a restore, or by hand — must not be able to relabel
// shipped content.
func TestM144AClassicKeepsTheMuseumsTitle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "TOWN.ZZT"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeWorldMeta(dir, "TOWN", WorldMeta{Title: "Not Town At All", Author: "Intruder"}); err != nil {
		t.Fatal(err)
	}
	entries := WorldListEntriesInDir(dir, []string{"TOWN"}, nil)
	if len(entries) != 1 {
		t.Fatalf("want one entry, got %+v", entries)
	}
	if entries[0].Kind != WorldKindClassic {
		t.Fatalf("TOWN's kind = %q, want classic", entries[0].Kind)
	}
	if entries[0].Title == "Not Town At All" || entries[0].Author == "Intruder" {
		t.Errorf("a sidecar relabelled a canonical world: %+v", entries[0])
	}
}

// TestM144AnExistingWorldResolvesUnderItsOldName is the migration half of the
// DoD, and the reason the owner chose to keep the stem. A world that predates
// M14.4 has no meta sidecar at all; it must list, keep its name, and keep the
// title fallback it has always had.
func TestM144AnExistingWorldResolvesUnderItsOldName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "OLDWORLD.ZZT"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := WorldListEntriesInDir(dir, ListWorlds(dir), nil)
	if len(entries) != 1 || entries[0].World != "OLDWORLD" {
		t.Fatalf("a pre-M14.4 world stopped resolving: %+v", entries)
	}
	if entries[0].Title != "OLDWORLD" {
		t.Errorf("untitled world's title = %q, want the stem it has always shown", entries[0].Title)
	}
	if _, ok, err := loadWorldMeta(dir, "OLDWORLD"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("reading an absent sidecar invented one")
	}
	// An empty meta writes no file: absent and empty mean the same thing, so a
	// world without a title does not acquire a sidecar full of nothing.
	if err := writeWorldMeta(dir, "OLDWORLD", WorldMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worldMetaPath(dir, "OLDWORLD")); err == nil {
		t.Error("an empty title wrote a sidecar")
	}
}

// --------------------------------------------------------------------------
// The dream, end to end
// --------------------------------------------------------------------------

// TestM144ADreamNamingAClassicNeedsNoRefusalToExplain is the DoD's headline:
// "a dream whose plan names a classic keeps the classic's bytes and needs no
// refusal to explain".
//
// Before M14.4 this was M18.11's refusal followed by M16.17d's hashed fallback
// — two patches on one seam, and a name the player could not predict. Now the
// plan's title is a title: it seeds a stem, minting moves off the classic on
// its own, and the world the player reads about in the picker is called what
// the model called it.
func TestM144ADreamNamingAClassicNeedsNoRefusalToExplain(t *testing.T) {
	model := m1617NewModel(t)
	model.planReplies(m1617PlanNamed("TOWN"))
	model.boardReplies("start", generatedBoard("Start", false))
	model.boardReplies("title", generatedBoard("Title", false))

	outDir := t.TempDir()
	service := m1617Service(t, model, outDir, 1)
	server := NewWebSocketServer(testEmptyWorld(t), 1)

	// The classic, sitting in the hosting directory with bytes worth keeping.
	before := []byte("the shipped TOWN")
	townPath := filepath.Join(outDir, "TOWN.ZZT")
	if err := os.WriteFile(townPath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := service.GenerateRequest(context.Background(), GenerationRequest{
		Client: "client-tester", Premise: m1617Premise, Server: server,
		Account: AuthenticatedAccount{ID: "acct-ada", Name: "Ada"},
	})
	if err != nil {
		t.Fatalf("a dream whose plan named TOWN = %v, want it to land beside it", err)
	}
	if result.Name == "TOWN" {
		t.Fatal("the dream took the classic's identity")
	}
	if after, err := os.ReadFile(townPath); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(after, before) {
		t.Fatal("the classic's bytes were rewritten")
	}
	if _, err := os.Stat(filepath.Join(outDir, result.Name+".ZZT")); err != nil {
		t.Fatalf("the dream did not persist under its minted name %q: %v", result.Name, err)
	}

	// The title is what the plan said, recorded beside the world and shown by
	// the picker — so the minted stem costs the player nothing to read.
	meta, ok, err := loadWorldMeta(outDir, result.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || meta.Title != "TOWN" {
		t.Fatalf("the dream's title = %+v (present=%v), want the plan's TOWN", meta, ok)
	}
	if meta.Author != "Ada" {
		t.Errorf("the dream's author = %q, want the dreamer", meta.Author)
	}
	entries := WorldListEntriesInDir(outDir, []string{result.Name}, nil)
	if len(entries) != 1 || entries[0].Title != "TOWN" {
		t.Fatalf("the picker does not show the dreamed world's title: %+v", entries)
	}
	if entries[0].World != result.Name {
		t.Errorf("the picker's identity = %q, want the minted %q", entries[0].World, result.Name)
	}

	// A title that collides is not an error anywhere: TOWN the classic and the
	// dream that wanted to be called TOWN both list, under two identities.
	if err := os.WriteFile(filepath.Join(outDir, "TOWN.ZZT"), before, 0o644); err != nil {
		t.Fatal(err)
	}
	both := WorldListEntriesInDir(outDir, []string{"TOWN", result.Name}, nil)
	if len(both) != 2 {
		t.Fatalf("want both worlds listed, got %+v", both)
	}
	if both[0].World == both[1].World {
		t.Fatal("the classic and the dream share an identity")
	}

	// And the minted name is not held after the dream finished: reserving it
	// again succeeds, which is what keeps a failed dream from leaking a name
	// until the process exits.
	if !reserveWorldName(outDir, result.Name) {
		t.Errorf("the finished dream is still holding %q", result.Name)
	}
	releaseWorldName(outDir, result.Name)
}

// TestM144ATypedNameStillRefuses is the half M14.4 deliberately does not
// change. Minting answers a name nobody chose. A name the PLAYER typed is
// still refused, because they chose it, the conflict is legible to them, and
// quietly handing them a different world under a different name is worse than
// saying so (M16.17b/M16.17d, owner decision 2026-07-31).
func TestM144ATypedNameStillRefuses(t *testing.T) {
	outDir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)

	before := []byte("Ada's published world")
	path := filepath.Join(outDir, "OWNED.ZZT")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	ada := AuthenticatedAccount{ID: "acct-ada", Name: "Ada"}
	if err := writeWorldAccess(outDir, "OWNED", WorldAccess{OwnerAccountID: ada.ID, OwnerName: ada.Name}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		typed   string
		wantErr error
	}{
		{name: "another account's world", typed: "OWNED", wantErr: ErrGeneratedWorldNotYours},
		{name: "a canonical classic", typed: "TOWN", wantErr: ErrGeneratedWorldIsCanonical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := m1617ScriptedDream(t)
			service := m1617Service(t, model, outDir, 1)
			_, err := service.GenerateRequest(context.Background(), GenerationRequest{
				Client: "client-intruder", Premise: m1617Premise, Server: server,
				Name:    tc.typed,
				Account: AuthenticatedAccount{ID: "acct-intruder", Name: "Intruder"},
			})
			if err == nil {
				t.Fatalf("a dream that ASKED for %q succeeded; a typed name is still refused", tc.typed)
			}
			if !strings.Contains(err.Error(), tc.wantErr.Error()) {
				t.Fatalf("typed %q = %v, want %v", tc.typed, err, tc.wantErr)
			}
			if got := model.callsFor("board", ""); got != 0 {
				t.Errorf("the refusal painted %d board(s); a name conflict costs no model spend", got)
			}
		})
	}
	if after, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(after, before) {
		t.Fatal("a refused dream rewrote Ada's world")
	}
}

// TestM144AnEditorPublishKeepsTheAuthoredTitle is the other creation path. The
// world-property dialog lets an author call a world "The Salt Cellar"; the
// publish stem can only be SALTCELL, and vanilla's GameWorldSave then writes
// that stem over World.Info.Name inside the .ZZT (game.go:833, faithfully
// mirrored by WorldBytes). So before M14.4 the authored title was destroyed by
// the act of publishing. It is now captured on the way past.
func TestM144AnEditorPublishKeepsTheAuthoredTitle(t *testing.T) {
	dir := t.TempDir()
	world := m1613EditorWorld(t)
	server := NewWebSocketServer(world, 0)
	server.WorldsDir = dir

	session := server.editorSessionForWorld("SALTCELL", world)
	author := &webSocketClient{accountID: "google:ada", name: "Ada Lovelace"}
	if err := session.Enter(author); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(author)

	// The author names the world in the property dialog, as the editor allows.
	if err := session.Apply(author, func(e *Engine) {
		e.World.Info.Name = "The Salt Cellar"
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.saveEditorWorld(author, session, "SALTCELL"); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	meta, ok, err := loadWorldMeta(dir, "SALTCELL")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || meta.Title != "The Salt Cellar" {
		t.Fatalf("published title = %+v (present=%v), want the authored one", meta, ok)
	}
	if meta.Author != "Ada Lovelace" {
		t.Errorf("published author = %q, want Ada Lovelace", meta.Author)
	}
	entries := WorldListEntriesInDir(dir, []string{"SALTCELL"}, nil)
	if len(entries) != 1 || entries[0].Title != "The Salt Cellar" || entries[0].World != "SALTCELL" {
		t.Fatalf("the picker does not show title and identity separately: %+v", entries)
	}

	// The .ZZT bytes are untouched by any of this: vanilla writes the stem into
	// Info.Name and builds the .HI path from it, and M14.4 does not deviate.
	data, err := os.ReadFile(filepath.Join(dir, "SALTCELL.ZZT"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWorldBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Info.Name != "SALTCELL" {
		t.Errorf("the published .ZZT carries Info.Name %q, want the save stem — "+
			"M14.4 must not change what vanilla writes into the file", loaded.Info.Name)
	}

	// Taking the title back off removes the sidecar, so the picker never shows
	// a name the author has stopped using.
	if err := session.Apply(author, func(e *Engine) {
		e.World.Info.Name = ""
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.saveEditorWorld(author, session, "SALTCELL"); err != nil {
		t.Fatalf("republish failed: %v", err)
	}
	if _, ok, err := loadWorldMeta(dir, "SALTCELL"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("a republish with no title left the stale sidecar behind")
	}
}

// TestM144BackupsCarryTheTitleSidecar closes the loop the DoD asks for
// ("backups and recordings still round-trip"): a world's title now lives
// outside its .ZZT, so a backup that copies the world and not its sidecar
// restores a world nobody can name. Recordings and saves are unaffected by
// construction — they key on the stem, which did not move — so this asserts the
// one file that is new.
func TestM144BackupsCarryTheTitleSidecar(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "deploy", "zztmmo-backup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), ".meta.json") {
		t.Error("zztmmo-backup.sh does not collect NAME.meta.json, so a restore loses every world's title")
	}
}
