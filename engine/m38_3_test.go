package zztgo

// M38.3: the assisted division. Live and assisted rows share ChallengeStore and
// its file, rank only within their own division, and never move each other.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func m383Row(def ChallengeDefinition, account, name, division string, ticks int) ChallengeResult {
	return ChallengeResult{
		ChallengeID: def.ID, Version: def.Version, AccountKey: account, Name: CP437String(name),
		Ticks: ticks, Score: 30, Gems: 3, RecordingID: "rec-" + name + "-" + division, Division: division,
	}
}

func TestM383TheTwoDivisionsNeverMoveEachOther(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	def := gemDashFixture()
	path := filepath.Join(t.TempDir(), "challenge_scores.json")
	store, err := NewChallengeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(r ChallengeResult) int {
		t.Helper()
		rank, err := store.Submit(r)
		if err != nil {
			t.Fatalf("submit %+v: %v", r, err)
		}
		return rank
	}
	submit(m383Row(def, "google:ada", "ada", ChallengeDivisionLive, 120))
	submit(m383Row(def, "google:bob", "bob", ChallengeDivisionLive, 150))
	liveBefore := store.Leaderboard(def.ID, "")

	// A far faster assisted run ranks first on its own board, and the live
	// board does not change at all.
	if rank := submit(m383Row(def, "google:bob", "bob", ChallengeDivisionAssisted, 40)); rank != 1 {
		t.Fatalf("assisted rank = %d", rank)
	}
	if got := store.Leaderboard(def.ID, ""); !reflect.DeepEqual(got, liveBefore) {
		t.Fatalf("an assisted submission moved the live board:\n%+v\n%+v", liveBefore, got)
	}
	// And the reverse: a live run does not touch the assisted board, even from
	// the account that holds an assisted row.
	assistedBefore := store.LeaderboardIn(def.ID, ChallengeDivisionAssisted, "")
	if rank := submit(m383Row(def, "google:bob", "bob", ChallengeDivisionLive, 100)); rank != 1 {
		t.Fatalf("bob's faster live run ranks %d", rank)
	}
	if got := store.LeaderboardIn(def.ID, ChallengeDivisionAssisted, ""); !reflect.DeepEqual(got, assistedBefore) {
		t.Fatalf("a live submission moved the assisted board:\n%+v\n%+v", assistedBefore, got)
	}
	// One row per account per division: bob is on both boards, once each.
	if live, assisted := store.Leaderboard(def.ID, "google:bob"), store.LeaderboardIn(def.ID, ChallengeDivisionAssisted, "google:bob"); len(live) != 2 || len(assisted) != 1 || !live[0].You || !assisted[0].You {
		t.Fatalf("live=%+v assisted=%+v", live, assisted)
	}
	// A recording is found only on its own board.
	if _, ok := store.ResultForRecording(def.ID, "rec-bob-assisted"); ok {
		t.Fatal("an assisted recording was found on the live board")
	}
	if row, ok := store.ResultForRecordingIn(def.ID, ChallengeDivisionAssisted, "rec-bob-assisted"); !ok || row.Ticks != 40 {
		t.Fatalf("the assisted row = %+v (%v)", row, ok)
	}

	// Both boards come back from the file as they were.
	reloaded, err := NewChallengeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Leaderboard(def.ID, ""); !reflect.DeepEqual(got, store.Leaderboard(def.ID, "")) {
		t.Fatalf("live after reload = %+v", got)
	}
	if got := reloaded.LeaderboardIn(def.ID, ChallengeDivisionAssisted, ""); !reflect.DeepEqual(got, assistedBefore) {
		t.Fatalf("assisted after reload = %+v", got)
	}
}

func TestM383AStoreWrittenBeforeM38ReadsBackAsLive(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	def := gemDashFixture()
	path := filepath.Join(t.TempDir(), "challenge_scores.json")
	// M32.1's file, exactly: no division anywhere.
	old := `{"version":1,"seq":2,"results":{"gem-dash":[` +
		`{"challengeId":"gem-dash","version":1,"accountKey":"google:ada","name":"ada","ticks":120,"score":30,"gems":3,"recordingId":"rec-a","seq":1},` +
		`{"challengeId":"gem-dash","version":1,"accountKey":"google:bob","name":"bob","ticks":150,"score":30,"gems":3,"recordingId":"rec-b","seq":2}]}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewChallengeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if live := store.Leaderboard(def.ID, ""); len(live) != 2 || live[0].Name != "ada" {
		t.Fatalf("old rows did not read back as live: %+v", live)
	}
	if assisted := store.LeaderboardIn(def.ID, ChallengeDivisionAssisted, ""); len(assisted) != 0 {
		t.Fatalf("old rows reached the assisted board: %+v", assisted)
	}
	// A row naming a division the store does not keep is dropped on load.
	bad := `{"version":1,"seq":1,"results":{"gem-dash":[{"challengeId":"gem-dash","version":1,"accountKey":"google:x","name":"x","ticks":5,"score":1,"gems":3,"division":"speedy","seq":1}]}}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err = NewChallengeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Leaderboard(def.ID, "")) != 0 || len(store.LeaderboardIn(def.ID, "speedy", "")) != 0 {
		t.Fatal("a row in an unknown division was kept")
	}
}

func TestM383StaleAndUnknownAreRefusedInBothDivisions(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	def := gemDashFixture()
	store, _ := NewChallengeStore("")
	for _, division := range []string{ChallengeDivisionLive, ChallengeDivisionAssisted} {
		stale := m383Row(def, "google:ada", "ada", division, 100)
		stale.Version = def.Version + 1
		if _, err := store.Submit(stale); !errors.Is(err, ErrChallengeResultStale) {
			t.Fatalf("%q: a stale row: %v", division, err)
		}
		guest := m383Row(def, "", "guest", division, 100)
		if _, err := store.Submit(guest); !errors.Is(err, ErrChallengeResultNotDurable) {
			t.Fatalf("%q: a guest row: %v", division, err)
		}
	}
	if _, err := store.Submit(m383Row(def, "google:ada", "ada", "speedy", 100)); !errors.Is(err, ErrChallengeDivision) {
		t.Fatalf("an unknown division: %v", err)
	}
}

func TestM383TheLandingCarriesBothBoardsAndTheGhostRouteTakesADivision(t *testing.T) {
	// A real assisted row: a live run's script, judged, with the judge's own
	// recording on disk where the ghost route looks.
	h, _, script := m382LiveRun(t)
	def := gemDashFixture()
	var rec bytes.Buffer
	judgment := m382Judge(t, h, script, &rec)
	id := "chal-gem-dash-assisted-1"
	if err := os.WriteFile(filepath.Join(h.recordDir, id+".jsonl"), rec.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	row := judgment.Result
	row.AccountKey, row.Name, row.RecordingID, row.Division = "google:bob", "bob", id, ChallengeDivisionAssisted
	if _, err := h.server.ChallengeStore.Submit(row); err != nil {
		t.Fatal(err)
	}

	api := &WebAPI{RoomManager: h.server.RoomManager, Server: h.server, Auth: h.server.Auth}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/challenge/" + def.ID)
	if err != nil {
		t.Fatal(err)
	}
	var landing ChallengeResponse
	if err := json.NewDecoder(resp.Body).Decode(&landing); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(landing.Leaderboard) != 1 || len(landing.Assisted) != 1 || landing.Assisted[0].RecordingID != id {
		t.Fatalf("landing live=%+v assisted=%+v", landing.Leaderboard, landing.Assisted)
	}

	for query, want := range map[string]int{
		"&division=assisted": http.StatusOK,
		"":                   http.StatusNotFound,
		"&division=speedy":   http.StatusBadRequest,
	} {
		resp, err := http.Get(srv.URL + "/api/challenge/ghost?challenge=" + def.ID + "&recording=" + id + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("ghost of the assisted row%s = %d, want %d", query, resp.StatusCode, want)
		}
	}
}
