package zztgo

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// M36.13 — a Museum play cannot overwrite a published world.
//
// POST /api/museum/play is anonymous and names the world it hosts after the
// .ZZT inside the archive, so any archive that ships a CAVES.ZZT used to land
// on worldsDir/CAVES.ZZT whatever was there: a player's published world (and
// its .access.json then claimed the foreign bytes), a canonical classic, or a
// world people were standing in, which HostGeneratedWorld refused only after
// the file had already been replaced. Play now asks the same three questions
// the editor's publish and generation ask, before the first byte is written.

// m3613Museum is the m18_11 Museum harness: one archive served for every
// request, a fresh hosting directory, and the rate limiter already satisfied.
func m3613Museum(t *testing.T, zipData []byte) (*MuseumService, *WebSocketServer, string) {
	t.Helper()
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	t.Cleanup(files.Close)

	dir := t.TempDir()
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = dir
	museum := NewMuseumService(server)
	museum.FilesBaseURL = files.URL + "/zgames"
	museum.Client = files.Client()
	museum.CacheDir = filepath.Join(dir, ".museum-cache")
	museum.lastRequest = time.Now().Add(-museumRequestDelay)
	return museum, server, dir
}

// m3613AssertUntouched checks that a refused Play left the target exactly as it
// was, left no temp file behind, and committed nothing to the archive cache.
func m3613AssertUntouched(t *testing.T, dir, name string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name+".ZZT"))
	if err != nil {
		t.Fatalf("read %s.ZZT after the refused play: %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s.ZZT changed: %d bytes, want the original %d", name, len(got), len(want))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("refused play left %s behind", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".museum-cache")); err == nil {
		cached, _ := os.ReadDir(filepath.Join(dir, ".museum-cache", "x"))
		if len(cached) != 0 {
			t.Errorf("refused play committed the archive to the cache")
		}
	}
}

// TestM3613MuseumPlayRefusesAnOwnedWorld is the DoD: a Museum archive whose
// world name collides with an owned world is refused and the file is unchanged,
// and so is the ownership record that would otherwise vouch for foreign bytes.
func TestM3613MuseumPlayRefusesAnOwnedWorld(t *testing.T) {
	owned := museumTestWorldBytes(t, "MYCAVE")
	foreign := museumTestWorldBytes(t, "OTHER")
	if bytes.Equal(owned, foreign) {
		t.Fatal("test worlds must differ")
	}
	museum, _, dir := m3613Museum(t, museumTestZip(t, map[string][]byte{"MYCAVE.ZZT": foreign}))
	if err := os.WriteFile(filepath.Join(dir, "MYCAVE.ZZT"), owned, 0o644); err != nil {
		t.Fatal(err)
	}
	access := WorldAccess{OwnerAccountID: "acct-owner", OwnerName: "Owner"}
	if err := writeWorldAccess(dir, "MYCAVE", access); err != nil {
		t.Fatal(err)
	}
	accessBefore, _ := os.ReadFile(worldAccessPath(dir, "MYCAVE"))

	_, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "x", Filename: "xcave.zip"})
	if err == nil {
		t.Fatal("Museum Play over an owned world succeeded; want it refused")
	}
	m3613AssertUntouched(t, dir, "MYCAVE", owned)
	accessAfter, _ := os.ReadFile(worldAccessPath(dir, "MYCAVE"))
	if !bytes.Equal(accessBefore, accessAfter) {
		t.Error("the owned world's .access.json changed")
	}

	// Through the route, the refusal is a name conflict (409), as it is for
	// generation, not a broken archive (422).
	api := &WebAPI{Server: museum.Server, Museum: museum}
	museum.lastRequest = time.Now().Add(-museumRequestDelay)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/museum/play", strings.NewReader(`{"letter":"x","filename":"xcave.zip"}`))
	api.handleMuseumPlay(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("POST /api/museum/play over an owned world: status %d (%s), want 409", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	m3613AssertUntouched(t, dir, "MYCAVE", owned)
}

// TestM3613MuseumPlayRefusesAnOccupiedWorld: a world somebody is standing in is
// refused before the write, not after it — the hosted instance is the same one
// and the bytes on disk are the bytes it was loaded from.
func TestM3613MuseumPlayRefusesAnOccupiedWorld(t *testing.T) {
	original := museumTestWorldBytes(t, "BUSY")
	foreign := museumTestWorldBytes(t, "OTHER")
	museum, server, dir := m3613Museum(t, museumTestZip(t, map[string][]byte{"BUSY.ZZT": foreign}))
	if err := os.WriteFile(filepath.Join(dir, "BUSY.ZZT"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	world, err := LoadWorldBytes(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.HostGeneratedWorld("BUSY", world); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	inst := server.Instances["BUSY"]
	server.mu.Unlock()
	inst.mu.Lock()
	inst.Clients[PlayerID(1)] = &webSocketClient{}
	inst.mu.Unlock()

	if _, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "x", Filename: "xbusy.zip"}); err == nil {
		t.Fatal("Museum Play over an occupied world succeeded; want it refused")
	}
	m3613AssertUntouched(t, dir, "BUSY", original)
	server.mu.Lock()
	still := server.Instances["BUSY"]
	server.mu.Unlock()
	if still != inst {
		t.Error("the occupied world's instance was replaced")
	}
}

// TestM3613MuseumPlayRefusesAForeignClassic: a classic's name may be written by
// that classic's own archive (M18.11's TEEN boundary, which still passes) and by
// no other. TOWN ships with the server and has no Museum archive at all, so no
// archive may write it; TEEN comes from teen.zip, so another archive carrying a
// TEEN.ZZT may not.
func TestM3613MuseumPlayRefusesAForeignClassic(t *testing.T) {
	for _, tc := range []struct{ world, archive string }{
		{"TOWN", "nottown.zip"},
		{"TEEN", "notteen.zip"},
	} {
		t.Run(tc.world, func(t *testing.T) {
			if !WorldIsCanonical(tc.world) {
				t.Skipf("%s is no longer in the manifest", tc.world)
			}
			original := museumTestWorldBytes(t, tc.world)
			foreign := museumTestWorldBytes(t, "IMPOSTOR")
			museum, _, dir := m3613Museum(t, museumTestZip(t, map[string][]byte{tc.world + ".ZZT": foreign}))
			if err := os.WriteFile(filepath.Join(dir, tc.world+".ZZT"), original, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "x", Filename: tc.archive}); err == nil {
				t.Fatalf("Museum Play of %s carrying %s.ZZT succeeded; want it refused", tc.archive, tc.world)
			}
			m3613AssertUntouched(t, dir, tc.world, original)
		})
	}
}

// TestM3613MuseumPlayStillHostsAnUnownedWorld pins the other side: a world with
// no ownership record, nobody in it and no classic's name stays open, as it is
// for generation — a replay of an already-hosted Museum world must keep working.
func TestM3613MuseumPlayStillHostsAnUnownedWorld(t *testing.T) {
	stale := museumTestWorldBytes(t, "OPEN")
	fresh := museumTestWorldBytes(t, "OPENER")
	museum, _, dir := m3613Museum(t, museumTestZip(t, map[string][]byte{"OPEN.ZZT": fresh}))
	if err := os.WriteFile(filepath.Join(dir, "OPEN.ZZT"), stale, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := museum.Play(context.Background(), MuseumPlayRequest{Letter: "x", Filename: "xopen.zip"})
	if err != nil {
		t.Fatalf("Museum Play of an unowned world = %v", err)
	}
	if result.World != "OPEN" {
		t.Fatalf("hosted %q, want OPEN", result.World)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "OPEN.ZZT"))
	if !bytes.Equal(got, fresh) {
		t.Error("OPEN.ZZT does not hold the archive's bytes")
	}
	info, err := os.Stat(filepath.Join(dir, "OPEN.ZZT"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("OPEN.ZZT mode %v, want 0644", info.Mode().Perm())
	}
}
