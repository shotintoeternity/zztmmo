package zztgo

// M32.1 — daily challenge runs with replay-verified leaderboards and ghosts.
//
// The claims these tests exist to keep, in the order the spec's three boundaries
// make them:
//
//  1. Only server-created runs count. A challenge is resolvable only through the
//     committed catalogue, its world only through /play's identity path, and the
//     run's instance key is a string SanitizeSaveName refuses — so there is no
//     `?world=` that reaches a run and no HTTP verb that posts a time.
//  2. The clock is service time only at the choice of row. A run's stored result
//     is ticks and in-sim counters; the same recording verifies to the same
//     evidence however long the wall clock took.
//  3. Ghosts are presentation. A track is positions read out of a finished
//     recording; extracting one adds no player, no stat and no state.
//
// And the isolation the spec spells out: no play count, no autosave, no
// friends-here presence, no account sidecar, no `.HI`, no cheat prompt, no save,
// no transit out — each asserted rather than argued.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func m321WorldBytes(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "fixtures", "gemdash.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/gemdash.zwd: %v", err)
	}
	data, err := CompileZWD(string(src))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/gemdash.zwd): %v", err)
	}
	validateCompiledZWD(t, data)
	return data
}

type m321Harness struct {
	server    *WebSocketServer
	wsURL     string
	baseURL   string
	worldsDir string
	savesDir  string
	recordDir string
	db        ChatDatabase
	ctx       context.Context
}

// m321Server hosts GEMDASH out of a temp directory with recording on, no tick
// goroutine (every tick in these tests is one the test asked for) and a signed
// cookie seam for accounts.
func m321Server(t *testing.T) *m321Harness {
	t.Helper()
	// The shipped catalogue is empty, so the harness brings the row these tests
	// were written against (challenge_catalogue_fixture_test.go).
	withChallengeCatalogue(t, gemDashFixture())
	data := m321WorldBytes(t)
	root := t.TempDir()
	worldsDir := filepath.Join(root, "worlds")
	savesDir := filepath.Join(root, "saves")
	recordDir := filepath.Join(root, "recordings")
	for _, dir := range []string{worldsDir, savesDir, recordDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(worldsDir, ChallengeWorldName+".ZZT"), data, 0o644); err != nil {
		t.Fatalf("write %s.ZZT: %v", ChallengeWorldName, err)
	}

	// The server's own default world is deliberately NOT the challenge course:
	// the course is hosted only out of the worlds directory, so "is the source
	// world hosted as an ordinary instance" is a question with a meaningful
	// answer below.
	defaultWorld := testEmptyWorld(t)
	defaultWorld.Info.Name = "HOME"
	server := NewWebSocketServer(defaultWorld, 1)
	server.WorldsDir = worldsDir
	server.SavesDir = savesDir
	server.ChatDB = NewMemChatDatabase()
	server.Auth = NewAuthService("client-id", "", "", []byte("m32-1-cookie-secret"))
	store, err := NewChallengeStore(ChallengeStorePath(savesDir))
	if err != nil {
		t.Fatalf("NewChallengeStore: %v", err)
	}
	server.ChallengeStore = store
	if err := server.EnableRecording(recordDir); err != nil {
		t.Fatalf("EnableRecording: %v", err)
	}
	server.ReplayDir = recordDir

	httpServer := httptestServer(t, server)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(server.CloseRecorders)
	return &m321Harness{
		server:    server,
		wsURL:     "ws" + strings.TrimPrefix(httpServer.URL, "http"),
		baseURL:   httpServer.URL,
		worldsDir: worldsDir,
		savesDir:  savesDir,
		recordDir: recordDir,
		db:        server.ChatDB,
		ctx:       ctx,
	}
}

var m321Ada = AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
var m321Bo = AuthenticatedAccount{ID: "google:bo", Email: "bo@example.test", Name: "Bo"}

// m321Run starts a challenge run over the real join path and returns the run's
// instance with its first frame.
func (h *m321Harness) run(t *testing.T, query string, account *AuthenticatedAccount) (*websocket.Conn, SnapshotMessage) {
	t.Helper()
	var cookie *http.Cookie
	if account != nil {
		cookie = signedAuthCookie(t, h.server.Auth, *account)
	}
	return dialJoinWithCookie(t, h.ctx, h.wsURL+query, JoinMessage{Type: MessageTypeJoin, Name: "Guest"}, cookie)
}

// m321Finish walks the run east until the server observes the goal met, and
// returns the run and how many ticks the test spent. The inputs are set on the
// instance rather than sent over the wire so the tick that consumes each one is
// the tick this test took — the socket path itself is exercised by the join
// above and by the browser journey.
func m321Finish(t *testing.T, h *m321Harness, inst *WorldInstance, playerID PlayerID) *ChallengeRun {
	t.Helper()
	run := inst.Challenge
	for i := 0; i < 200; i++ {
		inst.setInput(playerID, PlayerInput{DeltaX: 1})
		h.server.Tick(h.ctx)
		inst.mu.Lock()
		finished := run.Finished
		inst.mu.Unlock()
		if finished {
			return run
		}
	}
	t.Fatalf("the run never completed: %+v", run)
	return nil
}

func TestM321ChallengeWorldCompilesShipsIsCanonicalAndListed(t *testing.T) {
	data := m321WorldBytes(t)
	if err := os.WriteFile(filepath.Join("..", "fixtures", ChallengeWorldName+".ZZT"), data, 0o644); err != nil {
		t.Fatalf("write fixtures/%s.ZZT: %v", ChallengeWorldName, err)
	}
	if err := os.WriteFile(ChallengeWorldName+".ZZT", data, 0o644); err != nil {
		t.Fatalf("write engine/%s.ZZT: %v", ChallengeWorldName, err)
	}
	if !WorldIsCanonical(ChallengeWorldName) {
		t.Fatalf("WorldIsCanonical(%s) = false; the challenge course must be protected from dreams and publishes", ChallengeWorldName)
	}
	if err := refuseIfCanonical(ChallengeWorldName); !errors.Is(err, ErrGeneratedWorldIsCanonical) {
		t.Fatalf("dream canonical guard for %s = %v, want ErrGeneratedWorldIsCanonical", ChallengeWorldName, err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ChallengeWorldName+".ZZT"), data, 0o644); err != nil {
		t.Fatalf("write temp world: %v", err)
	}
	entries := WorldListEntriesInDir(dir, ListWorlds(dir), nil)
	if len(entries) != 1 || entries[0].World != ChallengeWorldName || entries[0].Title != "ZZTMMO Gem Dash" {
		t.Fatalf("entries = %+v, want the one canonical %s row", entries, ChallengeWorldName)
	}
}

func TestM321CatalogueResolutionAndRefusals(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	def, ok := ChallengeByID("gem-dash")
	if !ok || def.World != ChallengeWorldName || def.Board != 1 || def.Goal.Kind != ChallengeGoalGems {
		t.Fatalf("ChallengeByID(gem-dash) = %+v, %v", def, ok)
	}
	if upper, ok := ChallengeByID("  GEM-DASH "); !ok || upper.ID != def.ID {
		t.Fatalf("a pasted id did not resolve: %+v %v", upper, ok)
	}
	for _, bad := range []string{"", "nope", "../gem-dash", "gem-dash/extra"} {
		if _, ok := ChallengeByID(bad); ok {
			t.Fatalf("ChallengeByID(%q) resolved; the catalogue is the only namer of challenges", bad)
		}
	}

	// Date addressing: the same UTC day always names the same row, and the row
	// is one of the catalogue's — never something derived from the request.
	day := time.Date(2026, 8, 9, 23, 59, 0, 0, time.UTC)
	first, ok := ChallengeForDate(day)
	if !ok {
		t.Fatal("ChallengeForDate returned nothing")
	}
	again, _ := ChallengeForDate(day.Add(-12 * time.Hour))
	if again.ID != first.ID {
		t.Fatalf("the same UTC day named %q and %q", first.ID, again.ID)
	}
	if _, ok := ChallengeByID(first.ID); !ok {
		t.Fatalf("ChallengeForDate named %q, which is not in the catalogue", first.ID)
	}

	// The isolation argument's foundation: a run key can never be a world name.
	key := challengeRunKey(def.ID, 7)
	if _, err := SanitizeSaveName(key); err == nil {
		t.Fatalf("run key %q sanitizes to a hostable world name; `?world=` would reach a run", key)
	}
	if !isChallengeRunKey(key) || isChallengeRunKey("TOWN") {
		t.Fatalf("isChallengeRunKey misidentifies keys: %q", key)
	}
	if _, err := sanitizeReplayID(challengeRecordingID(def.ID, 7, "20260809-101010")); err != nil {
		t.Fatalf("recording id is not servable by /replay: %v", err)
	}
}

func TestM321UnlistedMissingAndOwnedWorldsAreRefused(t *testing.T) {
	h := m321Server(t)

	if _, _, err := h.server.resolveChallengeJoin("no-such-challenge", "", AuthenticatedAccount{}, false); !errors.Is(err, ErrUnknownChallenge) {
		t.Fatalf("an unlisted challenge id started a run: %v", err)
	}

	missing := ChallengeDefinition{ID: "gem-dash", World: "NOPE", Board: 1, Version: 1}
	if _, _, err := h.server.ResolveChallengeWorld(missing); !errors.Is(err, ErrChallengeWorldUnavailable) {
		t.Fatalf("a missing world resolved: %v", err)
	}
	unhostable := ChallengeDefinition{ID: "gem-dash", World: "../etc/passwd", Board: 1, Version: 1}
	if _, _, err := h.server.ResolveChallengeWorld(unhostable); !errors.Is(err, ErrChallengeWorldUnavailable) {
		t.Fatalf("a traversal name resolved: %v", err)
	}
	corrupt := filepath.Join(h.worldsDir, "BROKEN.ZZT")
	if err := os.WriteFile(corrupt, []byte("not a world"), 0o644); err != nil {
		t.Fatalf("write corrupt world: %v", err)
	}
	if _, _, err := h.server.ResolveChallengeWorld(ChallengeDefinition{ID: "gem-dash", World: "BROKEN", Board: 1, Version: 1}); !errors.Is(err, ErrChallengeWorldUnavailable) {
		t.Fatalf("a corrupt world resolved: %v", err)
	}

	// A world a player owns is refused: a challenge measured against content
	// somebody can rewrite is a leaderboard of incomparable runs.
	if err := writeWorldAccess(h.worldsDir, ChallengeWorldName, WorldAccess{OwnerAccountID: m321Bo.ID, OwnerName: "Bo"}); err != nil {
		t.Fatalf("write access sidecar: %v", err)
	}
	def, _ := ChallengeByID("gem-dash")
	if _, _, err := h.server.ResolveChallengeWorld(def); !errors.Is(err, ErrChallengeWorldUnavailable) {
		t.Fatalf("an owned world was accepted as a challenge course: %v", err)
	}
	if err := os.Remove(worldAccessPath(h.worldsDir, ChallengeWorldName)); err != nil {
		t.Fatalf("remove access sidecar: %v", err)
	}

	// And a server that records nothing refuses to start a run at all, rather
	// than hosting one whose result nothing could verify.
	h.server.mu.Lock()
	h.server.RecordDir = ""
	h.server.mu.Unlock()
	if _, _, err := h.server.StartChallengeRun(def, "", "Guest"); !errors.Is(err, ErrChallengeRecordingDisabled) {
		t.Fatalf("an unrecorded challenge run started: %v", err)
	}
}

func TestM321RunIsIsolatedRecordedAndLeavesTheSourceWorldAlone(t *testing.T) {
	h := m321Server(t)
	activity, err := NewWorldActivityStore(filepath.Join(h.savesDir, "world_activity.json"))
	if err != nil {
		t.Fatalf("NewWorldActivityStore: %v", err)
	}
	h.server.Activity = activity
	h.server.AutosaveEveryTicks = 1

	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	defer conn.Close(websocket.StatusNormalClosure, "")

	if snap.Challenge != "gem-dash" || snap.ChallengeRun == "" {
		t.Fatalf("the join frame did not name the run: %+v", snap)
	}
	if snap.World != ChallengeWorldName {
		t.Fatalf("snapshot world = %q, want the source world %q", snap.World, ChallengeWorldName)
	}
	if snap.BoardID != 1 {
		t.Fatalf("the run started on board %d, want the catalogue's board 1", snap.BoardID)
	}
	inst, ok := h.server.ChallengeRunInstance(snap.ChallengeRun)
	if !ok {
		t.Fatalf("no instance for run %q", snap.ChallengeRun)
	}
	if inst.RoomManager.HighScorePath != "" {
		t.Fatalf("the run has a high-score path (%q); it would write the source world's .HI", inst.RoomManager.HighScorePath)
	}
	if inst.RoomManager.recorder == nil {
		t.Fatal("recording was not forced on for the run")
	}

	// The source world is untouched: it is not hosted, not counted, not
	// occupied, and not visible to anything that walks worlds by name.
	h.server.mu.Lock()
	_, sourceHosted := h.server.Instances[ChallengeWorldName]
	h.server.mu.Unlock()
	if sourceHosted {
		t.Fatal("starting a challenge hosted the source world as an ordinary instance")
	}
	if counts := activity.Counts(); counts[ChallengeWorldName] != 0 {
		t.Fatalf("play counts = %+v, want no play of the source world", counts)
	}
	if h.server.WorldIsOccupied(ChallengeWorldName) {
		t.Fatal("the source world reports occupancy from a challenge run")
	}
	api := &WebAPI{Server: h.server, SavesDir: h.savesDir}
	for _, entry := range api.WatchLiveLineup() {
		if entry.World == ChallengeWorldName || strings.Contains(entry.Path, "challenge") {
			t.Fatalf("a challenge run reached the ZZT TV lineup: %+v", entry)
		}
	}

	// Ticking with autosave due writes nothing for the run.
	for i := 0; i < 3; i++ {
		h.server.Tick(h.ctx)
	}
	autosaves, _ := os.ReadDir(filepath.Join(h.savesDir, "autosave"))
	for _, entry := range autosaves {
		if strings.Contains(entry.Name(), "challenge") || strings.Contains(strings.ToUpper(entry.Name()), ChallengeWorldName) {
			t.Fatalf("a challenge run was autosaved: %s", entry.Name())
		}
	}

	// Friends-here presence cannot see the run either.
	if err := h.db.PutAccountPreferences(m321Ada.ID, AccountPreferences{ShareLocationWithFollowers: true}); err != nil {
		t.Fatalf("seed ada preferences: %v", err)
	}
	if err := h.db.PutAccountPreferences(m321Bo.ID, AccountPreferences{FollowedAccounts: []string{m321Ada.ID}}); err != nil {
		t.Fatalf("seed bo follows: %v", err)
	}
	if presence := h.server.friendPresenceByWorld(m321Bo.ID); len(presence) != 0 {
		t.Fatalf("friends-here presence found a challenge run: %+v", presence)
	}

	// And no account sidecar is read or written for a run.
	if _, ok := h.server.loadAccountPlayerState(m321Ada.ID, inst.Name); ok {
		t.Fatal("a run read an account sidecar")
	}
	h.server.persistAccountPlayerState(m321Ada.ID, inst.Name, PlayerState{Gems: 99})
	if _, ok, _ := h.db.GetPlayerState(m321Ada.ID, inst.Name); ok {
		t.Fatal("a run wrote an account sidecar")
	}
}

func TestM321CheatSaveAndTransitAreRefusedInsideARun(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, ok := h.server.ChallengeRunInstance(snap.ChallengeRun)
	if !ok {
		t.Fatalf("no instance for run %q", snap.ChallengeRun)
	}

	// The vanilla `?` cheat prompt is a server-created stimulus that REPLAYS, so
	// a cheated run would verify. It is refused before it is applied.
	before, _ := inst.RoomManager.PlayerState(snap.You.ID)
	beforeGems := before.Gems
	h.server.submitDebugCommandInInstance(inst, snap.You.ID, "GEMS")
	h.server.Tick(h.ctx)
	after, _ := inst.RoomManager.PlayerState(snap.You.ID)
	if after.Gems != beforeGems {
		t.Fatalf("the cheat prompt changed gems inside a run: %d -> %d", beforeGems, after.Gems)
	}

	// A save is a mid-run checkpoint by another door.
	h.server.submitSaveFilenameInInstance(h.ctx, inst, snap.You.ID, "CHEAT")
	if _, err := os.Stat(filepath.Join(h.savesDir, "CHEAT.SAV")); err == nil {
		t.Fatal("a challenge run wrote a save")
	}

	// A transit gate out of a run is refused; the player stays where they are.
	h.server.completeWorldTransit(h.ctx, inst, WorldTransit{PlayerID: snap.You.ID, DestinationWorld: "TOWN"})
	if _, _, ok := inst.RoomManager.PlayerLocation(snap.You.ID); !ok {
		t.Fatal("a transit took the player out of their own challenge run")
	}
	h.server.mu.Lock()
	_, leaked := h.server.Instances["TOWN"]
	h.server.mu.Unlock()
	if leaked {
		t.Fatal("a challenge run's transit hosted another world")
	}
}

func TestM321CompletionWritesOneDurableRowAndVerifiesFromItsRecording(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, _ := h.server.ChallengeRunInstance(snap.ChallengeRun)
	run := m321Finish(t, h, inst, snap.You.ID)

	if run.Result.Ticks <= 0 || run.Result.Gems < 3 {
		t.Fatalf("result = %+v, want a positive tick count and the three gems", run.Result)
	}
	if run.Result.AccountKey != m321Ada.ID || run.Result.Evidence == "" {
		t.Fatalf("result = %+v, want the account key and hash evidence", run.Result)
	}

	rows := h.server.ChallengeStore.Leaderboard("gem-dash", m321Ada.ID)
	if len(rows) != 1 || rows[0].Rank != 1 || !rows[0].You || rows[0].Ticks != run.Result.Ticks {
		t.Fatalf("leaderboard = %+v, want one row for this run", rows)
	}

	// The public row carries no account id, whatever the viewer.
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	if strings.Contains(string(data), m321Ada.ID) || strings.Contains(string(data), "google:") {
		t.Fatalf("a marshaled leaderboard row leaks an account id: %s", data)
	}

	// A finished run does not complete twice, however long it keeps ticking.
	for i := 0; i < 5; i++ {
		h.server.Tick(h.ctx)
	}
	if rows := h.server.ChallengeStore.Leaderboard("gem-dash", ""); len(rows) != 1 {
		t.Fatalf("leaderboard grew after completion: %+v", rows)
	}

	// The recording is servable by /replay AND it verifies: replaying the stored
	// stimuli reproduces the exact evidence the row claims.
	path := filepath.Join(h.recordDir, run.RecordingID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the run's recording is not on disk: %v", err)
	}
	ok, err := h.server.VerifyChallengeResult(run.Result)
	if err != nil {
		t.Fatalf("VerifyChallengeResult: %v", err)
	}
	if !ok {
		t.Fatal("the stored result does not verify against its own recording")
	}
	tampered := run.Result
	tampered.Ticks = run.Result.Ticks - 1
	if ok, _ := h.server.VerifyChallengeResult(tampered); ok {
		t.Fatal("a result claiming a faster time verified against the same recording")
	}
	if _, err := h.server.GetOrCreateReplayInstance(run.RecordingID); err != nil {
		t.Fatalf("the run's recording is not watchable at /replay/%s: %v", run.RecordingID, err)
	}
}

func TestM321GuestRunsPlayButDoNotPersist(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", nil)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, _ := h.server.ChallengeRunInstance(snap.ChallengeRun)
	run := m321Finish(t, h, inst, snap.You.ID)

	if run.Result.Ticks <= 0 {
		t.Fatalf("a guest run produced no result: %+v", run.Result)
	}
	if rows := h.server.ChallengeStore.Leaderboard("gem-dash", ""); len(rows) != 0 {
		t.Fatalf("a guest run created a public row: %+v", rows)
	}
	if _, err := h.server.ChallengeStore.Submit(run.Result); !errors.Is(err, ErrChallengeResultNotDurable) {
		t.Fatalf("the store accepted an account-less result: %v", err)
	}
}

func TestM321ReconnectReclaimsTheRunAndAStrangerCannot(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	inst, _ := h.server.ChallengeRunInstance(snap.ChallengeRun)
	h.server.Tick(h.ctx)
	conn.Close(websocket.StatusNormalClosure, "")

	// Inside the grace, the run key plus the resume token reclaims THIS attempt:
	// the same player, the same instance, the same tick count.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		inst.mu.Lock()
		detached := len(inst.Detached)
		inst.mu.Unlock()
		if detached > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	back, resumed := dialJoinWithCookie(t, h.ctx, h.wsURL+"?run="+snap.ChallengeRun,
		JoinMessage{Type: MessageTypeJoin, ResumeToken: snap.ResumeToken},
		signedAuthCookie(t, h.server.Auth, m321Ada))
	defer back.Close(websocket.StatusNormalClosure, "")
	if resumed.You.ID != snap.You.ID {
		t.Fatalf("the reconnect spawned a new player (%d, was %d)", resumed.You.ID, snap.You.ID)
	}
	if resumed.ChallengeRun != snap.ChallengeRun {
		t.Fatalf("the reconnect landed in run %q, want %q", resumed.ChallengeRun, snap.ChallengeRun)
	}
	if got, _ := h.server.ChallengeRunInstance(snap.ChallengeRun); got != inst {
		t.Fatal("the reconnect created a second instance for one run")
	}

	// Another account cannot reclaim it, and neither can a guest.
	if _, _, err := h.server.resolveChallengeJoin("", snap.ChallengeRun, m321Bo, true); err == nil {
		t.Fatal("another account reclaimed a run that was not theirs")
	}
	if _, _, err := h.server.resolveChallengeJoin("", snap.ChallengeRun, AuthenticatedAccount{}, false); err == nil {
		t.Fatal("a guest reclaimed a signed-in player's run")
	}
	if _, _, err := h.server.resolveChallengeJoin("", "!challenge:gem-dash:999", m321Ada, true); err == nil {
		t.Fatal("a run key nobody minted was accepted")
	}
}

func TestM321ARunKeyWithoutItsTokenJoinsNobody(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", nil)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, ok := h.server.ChallengeRunInstance(snap.ChallengeRun)
	if !ok {
		t.Fatalf("no instance for run %q", snap.ChallengeRun)
	}

	// Run keys are sequential, so they can be guessed. A `?run=` join with no
	// valid resume token must reclaim nothing and START nothing: without this a
	// stranger walks a second player into somebody's measured attempt, and the
	// run's result is written under the original player's account.
	intruder, _, err := websocket.Dial(h.ctx, h.wsURL+"?run="+snap.ChallengeRun, nil)
	if err != nil {
		t.Fatalf("dial intruder: %v", err)
	}
	defer intruder.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(h.ctx, intruder, JoinMessage{Type: MessageTypeJoin, Name: "Intruder"}); err != nil {
		t.Fatalf("write intruder join: %v", err)
	}
	var refusal ChallengeErrorMessage
	if err := wsjson.Read(h.ctx, intruder, &refusal); err != nil {
		t.Fatalf("read intruder reply: %v", err)
	}
	if refusal.Type != MessageTypeChallengeError {
		t.Fatalf("the intruder was answered with %+v, want a challenge refusal", refusal)
	}

	h.server.Tick(h.ctx)
	inst.mu.Lock()
	players := len(inst.RoomManager.players)
	owner := inst.Challenge.PlayerID
	inst.mu.Unlock()
	if players != 1 {
		t.Fatalf("the run holds %d players, want only the player who started it", players)
	}
	if owner != snap.You.ID {
		t.Fatalf("the run's player is now %d, want the one who started it (%d)", owner, snap.You.ID)
	}
}

func TestM321AbandonedAndQuitRunsScoreNothing(t *testing.T) {
	h := m321Server(t)

	// Abandon: the socket goes away and the grace runs out. Nothing is scored,
	// and the run's instance is evicted rather than left ticking forever.
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	inst, _ := h.server.ChallengeRunInstance(snap.ChallengeRun)
	h.server.Tick(h.ctx)
	conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		inst.mu.Lock()
		detached := len(inst.Detached)
		inst.mu.Unlock()
		if detached > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.server.InstanceEvictIdleTicks = 2
	for i := 0; i < ReconnectGraceTicks+8; i++ {
		h.server.Tick(h.ctx)
	}
	if rows := h.server.ChallengeStore.Leaderboard("gem-dash", ""); len(rows) != 0 {
		t.Fatalf("an abandoned run was scored: %+v", rows)
	}
	if _, alive := h.server.ChallengeRunInstance(snap.ChallengeRun); alive {
		t.Fatal("an abandoned run's instance was never evicted")
	}

	// Quit: vanilla's Q is the abandon a player makes on purpose. It is allowed,
	// it takes the player out of the room, and it scores nothing.
	quitConn, quitSnap := h.run(t, "?challenge=gem-dash", &m321Ada)
	defer quitConn.Close(websocket.StatusNormalClosure, "")
	quitInst, _ := h.server.ChallengeRunInstance(quitSnap.ChallengeRun)
	h.server.submitQuitReplyInInstance(quitInst, quitSnap.You.ID, true)
	for i := 0; i < 4; i++ {
		h.server.Tick(h.ctx)
	}
	quitInst.mu.Lock()
	_, stillThere := quitInst.RoomManager.PlayerState(quitSnap.You.ID)
	quitInst.mu.Unlock()
	if stillThere {
		t.Fatal("quitting left the player in their challenge run")
	}
	// And the abandoned run cannot be completed by anything that happens next.
	for i := 0; i < 20; i++ {
		h.server.Tick(h.ctx)
	}
	if rows := h.server.ChallengeStore.Leaderboard("gem-dash", ""); len(rows) != 0 {
		t.Fatalf("a quit run was scored: %+v", rows)
	}
}

func TestM321RecordingHeaderNamesItsChallenge(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, _ := h.server.ChallengeRunInstance(snap.ChallengeRun)
	run := m321Finish(t, h, inst, snap.You.ID)

	data, err := os.ReadFile(filepath.Join(h.recordDir, run.RecordingID+".jsonl"))
	if err != nil {
		t.Fatalf("read the run's recording: %v", err)
	}
	line := data
	if idx := strings.IndexByte(string(data), '\n'); idx > 0 {
		line = data[:idx]
	}
	var header recHeader
	if err := json.Unmarshal(line, &header); err != nil {
		t.Fatalf("unmarshal recording header: %v", err)
	}
	if header.ChallengeID != "gem-dash" || header.ChallengeVersion != run.Def.Version {
		t.Fatalf("recording header = %+v, want the challenge it recorded", header)
	}
	if header.World != ChallengeWorldName {
		t.Fatalf("recording header world = %q, want the source world so /replay can resolve it", header.World)
	}
	// The version is NOT bumped by those fields: a recording written without
	// them still replays, which is what keeps every existing file and fixture
	// readable.
	if header.V != recordVersion {
		t.Fatalf("recording version = %d, want %d", header.V, recordVersion)
	}
}

func TestM321LeaderboardSortingTiesStalenessAndRestart(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	dir := t.TempDir()
	path := filepath.Join(dir, "challenge_scores.json")
	store, err := NewChallengeStore(path)
	if err != nil {
		t.Fatalf("NewChallengeStore: %v", err)
	}
	def, _ := ChallengeByID("gem-dash")
	row := func(account string, ticks, score int) ChallengeResult {
		return ChallengeResult{
			ChallengeID: def.ID,
			Version:     def.Version,
			AccountKey:  account,
			Name:        account,
			Ticks:       ticks,
			Score:       score,
			RecordingID: "chal-" + account,
			Evidence:    "1:0000000000000001",
		}
	}
	for _, r := range []ChallengeResult{
		row("ada", 40, 30),
		row("bo", 30, 30),
		row("cy", 30, 40), // same ticks as bo, higher score: ranks above
		row("di", 30, 40), // identical to cy: the opaque server order breaks it
	} {
		if _, err := store.Submit(r); err != nil {
			t.Fatalf("Submit(%+v): %v", r, err)
		}
	}
	want := []string{"cy", "di", "bo", "ada"}
	got := store.Leaderboard(def.ID, "")
	if len(got) != len(want) {
		t.Fatalf("leaderboard = %+v, want %d rows", got, len(want))
	}
	for i, name := range want {
		if got[i].Name != name || got[i].Rank != i+1 {
			t.Fatalf("row %d = %+v, want %s at rank %d", i, got[i], name, i+1)
		}
	}

	// A player's own second attempt replaces their row rather than adding one,
	// and only when it is better.
	if _, err := store.Submit(row("ada", 20, 30)); err != nil {
		t.Fatalf("Submit faster ada: %v", err)
	}
	if _, err := store.Submit(row("ada", 90, 30)); err != nil {
		t.Fatalf("Submit slower ada: %v", err)
	}
	rows := store.Leaderboard(def.ID, "")
	if len(rows) != 4 || rows[0].Name != "ada" || rows[0].Ticks != 20 {
		t.Fatalf("after two more attempts: %+v", rows)
	}

	// A run recorded against an older definition is refused, not mixed in.
	stale := row("ed", 1, 999)
	stale.Version = def.Version - 1
	if _, err := store.Submit(stale); !errors.Is(err, ErrChallengeResultStale) {
		t.Fatalf("a stale result was accepted: %v", err)
	}
	unknown := row("ed", 1, 999)
	unknown.ChallengeID = "no-such-challenge"
	if _, err := store.Submit(unknown); !errors.Is(err, ErrUnknownChallenge) {
		t.Fatalf("a result for an uncatalogued challenge was accepted: %v", err)
	}

	// The store is bounded per challenge.
	for i := 0; i < challengeLeaderboardMax*2; i++ {
		if _, err := store.Submit(row("filler"+strings.Repeat("x", i%3)+string(rune('a'+i%26)), 1000+i, 1)); err != nil {
			t.Fatalf("Submit filler %d: %v", i, err)
		}
	}
	if n := len(store.Leaderboard(def.ID, "")); n > challengeLeaderboardMax {
		t.Fatalf("leaderboard holds %d rows, want at most %d", n, challengeLeaderboardMax)
	}

	// And it survives a restart byte for byte in the order it renders.
	before := store.Leaderboard(def.ID, "")
	reopened, err := NewChallengeStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	after := reopened.Leaderboard(def.ID, "")
	if len(before) != len(after) {
		t.Fatalf("restart changed the row count: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("restart changed row %d: %+v -> %+v", i, before[i], after[i])
		}
	}

	// A file whose rows are stale is dropped on load rather than shown.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var file challengeStoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("unmarshal store: %v", err)
	}
	for id := range file.Results {
		for i := range file.Results[id] {
			file.Results[id][i].Version = 999
		}
	}
	bumped, _ := json.Marshal(file)
	if err := os.WriteFile(path, bumped, 0o600); err != nil {
		t.Fatalf("rewrite store: %v", err)
	}
	staleStore, err := NewChallengeStore(path)
	if err != nil {
		t.Fatalf("reopen bumped store: %v", err)
	}
	if rows := staleStore.Leaderboard(def.ID, ""); len(rows) != 0 {
		t.Fatalf("rows from another definition survived a load: %+v", rows)
	}
}

func TestM321GhostIsALocalTrackOfAStoredRunOnly(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, _ := h.server.ChallengeRunInstance(snap.ChallengeRun)
	run := m321Finish(t, h, inst, snap.You.ID)

	track, err := h.server.ChallengeGhost("gem-dash", run.RecordingID)
	if err != nil {
		t.Fatalf("ChallengeGhost: %v", err)
	}
	if len(track.Points) == 0 || track.Version != run.Result.Version || track.RecordingID != run.RecordingID {
		t.Fatalf("ghost track = %+v", track)
	}
	moved := false
	for _, point := range track.Points {
		if point.Board != 1 {
			t.Fatalf("ghost left the challenge board: %+v", point)
		}
		if point.X != track.Points[0].X {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("the ghost never moved: %+v", track.Points[:min(len(track.Points), 5)])
	}

	// Extracting a ghost adds nobody to anything: the live run still has one
	// player, and no second instance was created.
	inst.mu.Lock()
	players := len(inst.RoomManager.players)
	inst.mu.Unlock()
	if players != 1 {
		t.Fatalf("the run holds %d players after a ghost extraction, want 1", players)
	}

	// A recording the leaderboard does not cite is not a ghost, however real it
	// is: the endpoint is not a reader for arbitrary recordings.
	if _, err := h.server.ChallengeGhost("gem-dash", "chal-not-a-row"); err == nil {
		t.Fatal("a ghost was extracted from a recording no row cites")
	}
	if _, err := h.server.ChallengeGhost("no-such-challenge", run.RecordingID); err == nil {
		t.Fatal("a ghost was extracted for an uncatalogued challenge")
	}

	// The second extraction is served from cache: same track, no re-replay.
	again, err := h.server.ChallengeGhost("gem-dash", run.RecordingID)
	if err != nil || len(again.Points) != len(track.Points) {
		t.Fatalf("cached ghost = %+v (%v)", again, err)
	}
}

func TestM321APIRoutesAndPrecedence(t *testing.T) {
	h := m321Server(t)
	api := &WebAPI{Server: h.server, SavesDir: h.savesDir, Auth: h.server.Auth}
	handler := api.Handler()

	get := func(path string, cookie *http.Cookie) (int, ChallengeResponse, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp ChallengeResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec.Code, resp, rec.Body.String()
	}

	status, resp, body := get("/api/challenge", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/challenge = %d: %s", status, body)
	}
	if resp.Challenge.ID == "" || !resp.Challenge.Today || resp.Challenge.Date == "" {
		t.Fatalf("today's challenge = %+v", resp.Challenge)
	}
	if resp.CanSubmit {
		t.Fatal("a signed-out visitor was told they can post a time")
	}
	if !resp.Challenge.Available || resp.Challenge.GoalLine == "" {
		t.Fatalf("challenge availability = %+v", resp.Challenge)
	}

	status, byID, body := get("/api/challenge/gem-dash", signedAuthCookie(t, h.server.Auth, m321Ada))
	if status != http.StatusOK {
		t.Fatalf("GET /api/challenge/gem-dash = %d: %s", status, body)
	}
	if byID.Challenge.ID != "gem-dash" || !byID.CanSubmit {
		t.Fatalf("by-id landing = %+v, canSubmit=%v", byID.Challenge, byID.CanSubmit)
	}
	if status, _, _ := get("/api/challenge/nope", nil); status != http.StatusNotFound {
		t.Fatalf("an unlisted id answered %d, want 404", status)
	}
	if status, _, _ := get("/api/challenge/gem-dash/extra", nil); status != http.StatusNotFound {
		t.Fatalf("a deeper path answered %d, want 404", status)
	}
	if status, _, _ := get("/api/challenge/ghost?challenge=gem-dash&recording=chal-nope", nil); status != http.StatusNotFound {
		t.Fatalf("an unknown ghost answered %d, want 404", status)
	}
	// A traversal is stripped to its base name by sanitizeReplayID and then
	// refused as a row nobody stored — it never reaches a path.
	if status, _, _ := get("/api/challenge/ghost?challenge=gem-dash&recording=../../etc/passwd", nil); status != http.StatusNotFound {
		t.Fatalf("a traversal recording id answered %d, want a plain refusal", status)
	}
	if status, _, _ := get("/api/challenge/ghost?challenge=gem-dash&recording=not%20an%20id%21", nil); status != http.StatusBadRequest {
		t.Fatalf("an unencodable recording id answered %d, want 400", status)
	}

	// There is no way to POST a time.
	req, _ := http.NewRequest(http.MethodPost, "/api/challenge", strings.NewReader(`{"ticks":1}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/challenge = %d, want 405 — a client must never be able to report a time", rec.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
