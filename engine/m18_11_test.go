package zztgo

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// M18.11 — the canonical Museum of ZZT worlds are the main world, and
// player-authored content never overwrites one (owner decision 2026-07-31).
//
// The hole this closes was left open on purpose by M16.17b: a world with no
// .access.json "belongs to nobody and stays open", which is what keeps the ~100
// shipped worlds, every pre-M16.17b dream and every guest reachable. The
// shipped classics are exactly that population — no access file — so to the
// ownership guard they read as unowned and writable, and a dream or an editor
// publish that landed on TOWN replaced the canonical bytes as soon as nobody
// was standing in that world. The backups (M18.6/M18.10) archive player-created
// worlds only, also on purpose, so the loss came back from a re-fetch or a
// redeploy and from nothing else.
//
// The one path that must NOT be gated is the Museum's own: writing a classic's
// canonical bytes into the hosting directory is that cache doing its job.
// TestMuseumServicePlayDownloadsCachesValidatesAndHosts already hosts TEEN — a
// classic — so it fails if this guard ever reaches that path; the assertion
// below says so on purpose rather than leaving it to another file's test.

// TestM1811WorldIsCanonicalKnowsTheShippedWorlds pins the predicate itself. It
// answers off the embedded manifest, so it is true of a classic this server has
// never downloaded as well as one sitting in the hosting directory, and it
// cannot be defeated by deleting a sidecar.
func TestM1811WorldIsCanonicalKnowsTheShippedWorlds(t *testing.T) {
	for _, name := range []string{"TOWN", "town", "ToWn", "CAVES", "DUNGEONS", "TEEN"} {
		if !WorldIsCanonical(name) {
			t.Errorf("WorldIsCanonical(%q) = false, want true — the manifest knows it", name)
		}
	}
	// A multi-.ZZT archive: the manifest is keyed by every world file it ships,
	// not just the archive id, so both halves of a two-disk release are covered.
	if !WorldIsCanonical("TP2DISC1") {
		t.Error("WorldIsCanonical(TP2DISC1) = false; a classic's second world file is still a classic")
	}
	for _, name := range []string{"DREAM", "GEN0A3F", "MYWORLD", "NEWWORLD"} {
		if WorldIsCanonical(name) {
			t.Errorf("WorldIsCanonical(%q) = true, want false — player-made worlds must stay writable", name)
		}
	}
	// A name this server could never host is not a name that can collide.
	for _, name := range []string{"", "../TOWN", "TOOLONGNAME"} {
		if WorldIsCanonical(name) {
			t.Errorf("WorldIsCanonical(%q) = true; an unhostable name has nothing to protect", name)
		}
	}
}

// TestM1811DreamNeverOverwritesACanonicalWorld covers both halves of the
// generation guard: a name the player typed is refused, and a name the PLAN
// chose falls back instead (M16.17d), because the player did not choose that
// one and cannot fix it.
func TestM1811DreamNeverOverwritesACanonicalWorld(t *testing.T) {
	outDir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)

	// The canonical world, as a redeploy leaves it: bytes, and no access file.
	before := []byte("the canonical TOWN as shipped")
	path := filepath.Join(outDir, "TOWN.ZZT")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := loadWorldAccess(outDir, "TOWN"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("this test's premise is a classic with no .access.json")
	}

	// --- a typed name is refused, before a single board is painted ---------
	typed := m1617ScriptedDream(t)
	typedService := m1617Service(t, typed, outDir, 1)
	_, err := typedService.GenerateRequest(context.Background(), GenerationRequest{
		Client: "client-tester", Premise: m1617Premise, Name: "TOWN", Server: server,
	})
	if err == nil {
		t.Fatal("a dream aimed at TOWN succeeded; the canonical world is gone")
	}
	if !errors.Is(err, ErrGeneratedWorldIsCanonical) {
		t.Fatalf("dream over TOWN = %v, want ErrGeneratedWorldIsCanonical", err)
	}
	if after, readErr := os.ReadFile(path); readErr != nil {
		t.Fatal(readErr)
	} else if !bytes.Equal(after, before) {
		t.Fatal("the refusal still rewrote TOWN.ZZT")
	}
	for _, suffix := range []string{".zwd", ".plan.md", ".prompt.txt"} {
		if _, statErr := os.Stat(filepath.Join(outDir, "TOWN"+suffix)); statErr == nil {
			t.Errorf("the refusal wrote the sidecar TOWN%s", suffix)
		}
	}
	if got := typed.callsFor("board", ""); got != 0 {
		t.Errorf("the refusal painted %d board(s); a name conflict costs no model spend", got)
	}

	// --- a plan-derived name falls back (M16.17d) --------------------------
	derived := m1617NewModel(t)
	derived.planReplies(m1617PlanNamed("TOWN"))
	derived.boardReplies("start", generatedBoard("Start", false))
	derived.boardReplies("title", generatedBoard("Title", false))
	derivedService := m1617Service(t, derived, outDir, 1)

	var progress []GenerationProgress
	result, err := derivedService.GenerateRequest(context.Background(), GenerationRequest{
		Client: "client-tester", Premise: m1617Premise, Server: server,
		Progress: func(event GenerationProgress) { progress = append(progress, event) },
	})
	if err != nil {
		t.Fatalf("a dream whose plan named TOWN = %v, want it to land elsewhere", err)
	}
	if result.Name == "TOWN" {
		t.Fatal("the dream took TOWN after all")
	}
	// M14.4 changed the SHAPE of that name and not the protection above it.
	// Until then the fallback was generatedFallbackSaveName's GEN%05X hash;
	// now the plan's title only seeds a family and minting takes the first free
	// member, so a plan naming TOWN lands on TOWN2 — checked against the
	// directory rather than hashed and hoped. TOWN's bytes are what matters, and
	// they are asserted either way.
	if !strings.HasPrefix(result.Name, "TOWN") || result.Name == "TOWN" {
		t.Errorf("minted name = %q, want a free member of TOWN's family", result.Name)
	}
	if after, readErr := os.ReadFile(path); readErr != nil {
		t.Fatal(readErr)
	} else if !bytes.Equal(after, before) {
		t.Fatal("the fallback still rewrote TOWN.ZZT")
	}
	named := ""
	for _, event := range progress {
		if event.Stage == "naming" {
			named = event.Detail
		}
	}
	if named != result.Name {
		t.Errorf("the progress log said %q, want the world's real name %q", named, result.Name)
	}
}

// TestM1811PublishNeverOverwritesACanonicalWorld is the editor's half. The
// publish path asks its own occupancy and ownership questions; neither can
// speak for a world with no access file, so the carve-out is asked separately.
func TestM1811PublishNeverOverwritesACanonicalWorld(t *testing.T) {
	dir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = dir

	before := []byte("the canonical TOWN as shipped")
	path := filepath.Join(dir, "TOWN.ZZT")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}

	session := NewEditorSession("EMPTY", testMultiplayerSmokeWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	if _, err := server.saveEditorWorld(member, session, "TOWN"); err == nil {
		t.Fatal("an editor publish over TOWN succeeded; the canonical world is gone")
	} else if !strings.Contains(err.Error(), "classic") {
		t.Fatalf("publish over TOWN = %v, want a refusal that says why", err)
	}
	if after, readErr := os.ReadFile(path); readErr != nil {
		t.Fatal(readErr)
	} else if !bytes.Equal(after, before) {
		t.Fatal("the refused publish still rewrote TOWN.ZZT")
	}

	// A world the manifest does not know is untouched by all of this: the
	// editor's own worlds publish exactly as they did before.
	name, err := server.saveEditorWorld(member, session, "myworld")
	if err != nil {
		t.Fatalf("publish of a player's own world = %v, want it to succeed", err)
	}
	if name != "MYWORLD" {
		t.Fatalf("published name = %q, want MYWORLD", name)
	}
	if _, err := os.Stat(filepath.Join(dir, "MYWORLD.ZZT")); err != nil {
		t.Fatalf("the published world is missing: %v", err)
	}
}

// TestM1811MuseumCacheStillWritesCanonicalWorlds is the guard's boundary, and
// the reason it is scoped to player-authored writes rather than to the name.
// The Museum writes a classic's own canonical bytes into the hosting directory;
// that is the cache doing its job, not a player overwriting anything. A guard
// that only asked "is this name canonical" would break the very feature that
// puts the classics on this server in the first place.
func TestM1811MuseumCacheStillWritesCanonicalWorlds(t *testing.T) {
	if !WorldIsCanonical("TEEN") {
		t.Skip("TEEN is no longer in the manifest; pick another classic for this boundary")
	}
	worldData := museumTestWorldBytes(t, "TEEN")
	zipData := museumTestZip(t, map[string][]byte{"TEEN.ZZT": worldData})
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	defer files.Close()

	dir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = dir
	museum := NewMuseumService(server)
	museum.FilesBaseURL = files.URL + "/zgames"
	museum.Client = files.Client()
	museum.CacheDir = filepath.Join(dir, ".museum-cache")
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	result, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "teen.zip"})
	if err != nil {
		t.Fatalf("Museum Play of a canonical world = %v, want it to host — the guard has caught the cache", err)
	}
	if result.World != "TEEN" {
		t.Fatalf("hosted world = %q, want TEEN", result.World)
	}
	if _, err := os.Stat(filepath.Join(dir, "TEEN.ZZT")); err != nil {
		t.Fatalf("the Museum's own copy of a classic did not land: %v", err)
	}
	// And playing it a second time still overwrites its own bytes, which is how
	// a re-download repairs a corrupt cache.
	if _, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "t", Filename: "teen.zip"}); err != nil {
		t.Fatalf("second Museum Play of the same classic = %v", err)
	}
}
