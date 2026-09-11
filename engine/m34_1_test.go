package zztgo

// M34.1 — the ZZT Gazette's ledger: what the service saw happen today.
//
// The claims these tests exist to keep, in the order M34's preamble makes them:
//
//  1. The ledger is not the simulation. Recording a deed moves no StateHash, and
//     the wall clock it needs is injected at its own boundary.
//  2. The day is UTC, and it is the injected clock's day, not the machine's.
//  3. A name is a profile. The ledger stores an account key and never a name;
//     the consent rule is applied when an edition is read, and an account that
//     claimed neither a display name nor a handle is a stranger in print.
//  4. A private run is not news — including the editor test-play copy, which is
//     the one private instance kind that sanitizes as cleanly as TOWN does.

import (
	"context"
	"encoding/json"
	"fmt"
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

// m341Clock is a hand-cranked UTC clock. Every test that files a deed states the
// day it means rather than inheriting the machine's.
type m341Clock struct{ at time.Time }

func (c *m341Clock) now() time.Time { return c.at }

func (c *m341Clock) addDays(days int) { c.at = c.at.AddDate(0, 0, days) }

func m341Ledger(t *testing.T, path string) (*GazetteLedger, *m341Clock) {
	t.Helper()
	clock := &m341Clock{at: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)}
	ledger, err := NewGazetteLedger(path, clock.now)
	if err != nil {
		t.Fatalf("NewGazetteLedger(%q): %v", path, err)
	}
	return ledger, clock
}

func m341Record(t *testing.T, ledger *GazetteLedger, kind, subject, account string) {
	t.Helper()
	if err := ledger.Record(GazetteHappening{Kind: kind, Subject: subject, AccountKey: account}); err != nil {
		t.Fatalf("Record(%s, %s, %q): %v", kind, subject, account, err)
	}
}

// itemFor finds the one row for a (kind, subject, name) triple. It returns a
// count of zero when there is no such row, so a test can assert absence.
func m341Count(edition GazetteEdition, kind, subject, name string) int {
	for _, item := range edition.Items {
		if item.Kind == kind && item.Subject == subject && item.Name == name {
			return item.Count
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// The ledger itself
// ---------------------------------------------------------------------------

// One row per (kind, subject, account) per day, with a count: a player who dies
// forty times in TOWN is one row saying forty. Two different accounts dying in
// the same world are two rows, and a guest is a third.
func TestM341LedgerAggregatesByKindSubjectAndAccount(t *testing.T) {
	ledger, _ := m341Ledger(t, "")
	for i := 0; i < 40; i++ {
		m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:ada")
	}
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:bo")
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "")
	m341Record(t, ledger, GazetteKindDeath, "CAVES", "google:ada")
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")

	edition := ledger.Edition("", func(key string) string { return key })
	if got := len(edition.Items); got != 5 {
		t.Fatalf("edition has %d rows, want 5: %+v", got, edition.Items)
	}
	if got := m341Count(edition, GazetteKindDeath, "TOWN", "google:ada"); got != 40 {
		t.Errorf("ada's TOWN deaths = %d, want 40 in ONE row", got)
	}
	if got := m341Count(edition, GazetteKindDeath, "TOWN", "google:bo"); got != 1 {
		t.Errorf("bo's TOWN deaths = %d, want 1; a second account is a second row", got)
	}
	if got := m341Count(edition, GazetteKindDeath, "TOWN", ""); got != 1 {
		t.Errorf("the guest's TOWN deaths = %d, want 1", got)
	}
	if got := m341Count(edition, GazetteKindDream, "TOWN", "google:ada"); got != 1 {
		t.Errorf("ada's TOWN dream = %d, want 1; a second kind is a second row", got)
	}
}

// The day comes from the injected clock and is UTC. A deed filed at 23:30 UTC-6
// belongs to the next day's paper, which is the whole reason the service picks
// one calendar rather than a player's.
func TestM341DayIsTheInjectedClocksUTCDay(t *testing.T) {
	clock := &m341Clock{at: time.Date(2026, 8, 10, 23, 30, 0, 0, time.FixedZone("UTC-6", -6*60*60))}
	ledger, err := NewGazetteLedger("", clock.now)
	if err != nil {
		t.Fatalf("NewGazetteLedger: %v", err)
	}
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "")
	if got := ledger.Edition("", nil).Day; got != "2026-08-11" {
		t.Fatalf("day = %q, want 2026-08-11 (05:30 UTC), not the local calendar", got)
	}
}

// The caps refuse new rows; retention evicts whole days. Both are asserted
// against the exact boundary rather than "some number", and a row that already
// exists keeps counting after its day is full — which is the point of refusing
// rather than evicting.
func TestM341BoundsRefuseRowsAndRetentionEvictsDays(t *testing.T) {
	ledger, clock := m341Ledger(t, "")
	for i := 0; i < gazetteRowsPerKind+5; i++ {
		m341Record(t, ledger, GazetteKindDeath, fmt.Sprintf("W%05d", i), "")
	}
	edition := ledger.Edition("", nil)
	if got := len(edition.Items); got != gazetteRowsPerKind {
		t.Fatalf("deaths in one day = %d rows, want the cap of %d", got, gazetteRowsPerKind)
	}
	// The first row still counts after the day is full: the cap refuses NEW
	// subjects, it does not stop the paper from tracking the ones it has.
	m341Record(t, ledger, GazetteKindDeath, "W00000", "")
	if got := m341Count(ledger.Edition("", nil), GazetteKindDeath, "W00000", ""); got != 2 {
		t.Errorf("an existing row's count after the cap = %d, want 2", got)
	}
	// A different kind has its own cap, so a busy death day cannot silence dreams.
	m341Record(t, ledger, GazetteKindDream, "TOWN", "")
	if got := m341Count(ledger.Edition("", nil), GazetteKindDream, "TOWN", ""); got != 1 {
		t.Errorf("a dream after the death cap = %d, want 1; the caps are per kind", got)
	}

	for day := 0; day < gazetteRetentionDays+3; day++ {
		clock.addDays(1)
		m341Record(t, ledger, GazetteKindDeath, "TOWN", "")
	}
	days := ledger.Days()
	if len(days) != gazetteRetentionDays {
		t.Fatalf("retained %d days, want %d: %v", len(days), gazetteRetentionDays, days)
	}
	if days[0] != "2026-08-20" {
		t.Errorf("newest retained day = %q, want 2026-08-20", days[0])
	}
	if got := ledger.Edition("2026-08-10", nil); len(got.Items) != 0 {
		t.Errorf("the evicted day still renders %d rows; retention must remove it", len(got.Items))
	}
}

// A day outside the window renders empty rather than failing: "no paper that
// day" is the honest answer, and an error would make the caller invent one.
func TestM341UnknownDayRendersAnEmptyEdition(t *testing.T) {
	ledger, _ := m341Ledger(t, "")
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "")
	edition := ledger.Edition("1991-01-01", nil)
	if edition.Day != "1991-01-01" || len(edition.Items) != 0 {
		t.Fatalf("edition for a day with no paper = %+v, want that day and no rows", edition)
	}
}

// Ordering is deterministic and never map iteration order, so two marshals of
// one day are byte-identical — which is what lets M34.2 hand an edition to a
// generator and get the same paper twice.
func TestM341EditionOrderingIsDeterministic(t *testing.T) {
	ledger, _ := m341Ledger(t, "")
	for i := 0; i < 12; i++ {
		m341Record(t, ledger, GazetteKindDeath, fmt.Sprintf("W%05d", i), fmt.Sprintf("google:%d", i%3))
		m341Record(t, ledger, GazetteKindDream, fmt.Sprintf("W%05d", i), "")
	}
	for i := 0; i < 5; i++ {
		m341Record(t, ledger, GazetteKindDeath, "W00007", "google:1")
	}
	first, err := json.Marshal(ledger.Edition("", nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for attempt := 0; attempt < 20; attempt++ {
		again, err := json.Marshal(ledger.Edition("", nil))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("edition %d differs from the first render:\n%s\n%s", attempt, first, again)
		}
	}
	// And the order is the documented one: kind, then count descending.
	items := ledger.Edition("", nil).Items
	if items[0].Kind != GazetteKindDeath || items[0].Subject != "W00007" || items[0].Count != 6 {
		t.Fatalf("first row = %+v, want the busiest death row", items[0])
	}
}

// Recording never writes; Flush does, atomically, and only when something
// changed. A reload sees every count.
func TestM341FlushIsTheOnlyWriterAndReloadsExactly(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	path := filepath.Join(t.TempDir(), "gazette.json")
	ledger, _ := m341Ledger(t, path)
	for i := 0; i < 3; i++ {
		m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:ada")
	}
	m341Record(t, ledger, GazetteKindChallenge, "gem-dash", "google:ada")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Record wrote a file (stat err = %v); recording must be memory-only", err)
	}
	if err := ledger.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// A second flush with nothing new must not rewrite: the ledger is dirty-gated.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after flush: %v", err)
	}
	if err := ledger.Flush(); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after second flush: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Errorf("a clean Flush rewrote the file (%v/%d -> %v/%d)",
			before.ModTime(), before.Size(), after.ModTime(), after.Size())
	}

	// Nothing but the temp file and the ledger is left behind, so a crash
	// mid-write cannot leave a half-written paper in place of yesterday's.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "gazette.json" {
		t.Errorf("directory after flush = %v, want only gazette.json", entries)
	}

	clock := &m341Clock{at: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)}
	reloaded, err := NewGazetteLedger(path, clock.now)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	edition := reloaded.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindDeath, "TOWN", "google:ada"); got != 3 {
		t.Errorf("reloaded TOWN deaths = %d, want 3", got)
	}
	if got := m341Count(edition, GazetteKindChallenge, "gem-dash", "google:ada"); got != 1 {
		t.Errorf("reloaded challenge row = %d, want 1", got)
	}
}

// A file from a future version is refused rather than half-read, the same rule
// ChallengeStore keeps.
func TestM341FutureEnvelopeVersionIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gazette.json")
	body := fmt.Sprintf(`{"version":%d,"seq":1,"days":{"2026-08-10":[{"kind":"death","subject":"TOWN","count":1,"seq":1}]}}`,
		gazetteStoreVersion+1)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewGazetteLedger(path, nil); err == nil {
		t.Fatal("a future-version ledger loaded; it must be refused rather than half-read")
	}
}

// ---------------------------------------------------------------------------
// Admission: what may be news at all
// ---------------------------------------------------------------------------

func TestM341AdmissionRefusesUnknownKindsAndSubjects(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	ledger, _ := m341Ledger(t, "")
	refused := []GazetteHappening{
		{Kind: "gossip", Subject: "TOWN"},
		{Kind: GazetteKindDeath, Subject: ""},
		{Kind: GazetteKindDeath, Subject: "../../etc/passwd"},
		{Kind: GazetteKindDeath, Subject: "A VERY LONG WORLD NAME"},
		{Kind: GazetteKindChallenge, Subject: "no-such-challenge"},
	}
	for _, happening := range refused {
		if err := ledger.Record(happening); err == nil {
			t.Errorf("Record(%+v) was admitted; it must be refused", happening)
		}
	}
	if got := len(ledger.Edition("", nil).Items); got != 0 {
		t.Fatalf("the ledger holds %d rows after only refusals", got)
	}
	// A challenge id is normalized to the catalogue's own casing, so one
	// challenge is one row however the caller spelled it.
	def := ChallengeCatalogue()[0]
	m341Record(t, ledger, GazetteKindChallenge, strings.ToUpper(def.ID), "google:ada")
	m341Record(t, ledger, GazetteKindChallenge, def.ID, "google:ada")
	if got := m341Count(ledger.Edition("", func(k string) string { return k }), GazetteKindChallenge, def.ID, "google:ada"); got != 2 {
		t.Errorf("challenge row after two spellings = %d, want 2 in one row", got)
	}
}

// A nil ledger is a supported configuration: a server without one records
// nothing rather than making every call site branch.
func TestM341NilLedgerRecordsNothing(t *testing.T) {
	var ledger *GazetteLedger
	if err := ledger.Record(GazetteHappening{Kind: GazetteKindDeath, Subject: "TOWN"}); err != nil {
		t.Fatalf("nil ledger Record: %v", err)
	}
	if err := ledger.Flush(); err != nil {
		t.Fatalf("nil ledger Flush: %v", err)
	}
	if got := ledger.Edition("2026-08-10", nil); len(got.Items) != 0 {
		t.Fatalf("nil ledger rendered %d rows", len(got.Items))
	}
}

// ---------------------------------------------------------------------------
// Consent
// ---------------------------------------------------------------------------

// All three states of the rule: a profile that named itself is named, a
// signed-in account that named nothing is a stranger, and so is a guest.
func TestM341ConsentNamesOnlyProfilesThatAskedToBeNamed(t *testing.T) {
	cases := []struct {
		name  string
		prefs AccountPreferences
		found bool
		want  string
	}{
		{"display name wins", AccountPreferences{Profile: AccountProfilePreferences{DisplayName: "Ada L", Handle: "ada"}}, true, "Ada L"},
		{"handle when there is no display name", AccountPreferences{Profile: AccountProfilePreferences{Handle: "ada"}}, true, "@ada"},
		{"signed in, nothing claimed", AccountPreferences{Color: "#ff0000"}, true, ""},
		{"no stored preferences at all", AccountPreferences{}, false, ""},
	}
	for _, tc := range cases {
		if got := GazetteConsentedName(tc.prefs, tc.found); got != tc.want {
			t.Errorf("%s: GazetteConsentedName = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The ledger stores an account key and never a name, so nothing a profile did
// not consent to can reach the disk.
func TestM341LedgerFileCarriesNoName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gazette.json")
	ledger, _ := m341Ledger(t, path)
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:ada")
	if err := ledger.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, forbidden := range []string{"Ada", "ada@example.test", "displayName", "name"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("the ledger file contains %q; only the account key may be written:\n%s", forbidden, data)
		}
	}
}

// ---------------------------------------------------------------------------
// The read route
// ---------------------------------------------------------------------------

func m341API(t *testing.T) (*WebAPI, *WebSocketServer, *GazetteLedger) {
	t.Helper()
	world := testEmptyWorld(t)
	world.Info.Name = "GAZWORLD"
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour
	server.ChatDB = NewMemChatDatabase()
	ledger, _ := m341Ledger(t, "")
	server.Gazette = ledger
	return &WebAPI{RoomManager: server.RoomManager, Server: server}, server, ledger
}

func TestM341GazetteRouteNamesByConsentAndLeaksNoAccountID(t *testing.T) {
	api, server, ledger := m341API(t)
	if err := server.ChatDB.PutAccountPreferences("google:ada", AccountPreferences{
		Profile: AccountProfilePreferences{DisplayName: "Ada L"},
	}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	if err := server.ChatDB.PutAccountPreferences("google:bo", AccountPreferences{Color: "#00ff00"}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:ada")
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:bo")
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "")

	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gazette", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/gazette = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "google:ada") || strings.Contains(body, "google:bo") || strings.Contains(body, "accountKey") {
		t.Fatalf("the projection leaks an account id:\n%s", body)
	}
	var payload struct {
		Day   string        `json:"day"`
		Days  []string      `json:"days"`
		Items []GazetteItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Day != "2026-08-10" || len(payload.Days) != 1 {
		t.Fatalf("payload day/days = %q/%v, want 2026-08-10 and one retained day", payload.Day, payload.Days)
	}
	named, strangers := 0, 0
	for _, item := range payload.Items {
		if item.Name == "Ada L" {
			named++
		}
		if item.Name == "" {
			strangers++
		}
	}
	if named != 1 {
		t.Errorf("named rows = %d, want exactly Ada's", named)
	}
	if strangers != 2 {
		t.Errorf("unnamed rows = %d, want 2 (the guest and the account that claimed nothing)", strangers)
	}
}

func TestM341GazetteRouteRefusesBadInputAndSaysWhenItIsUnavailable(t *testing.T) {
	api, server, _ := m341API(t)
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/gazette", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/gazette = %d, want 405", rec.Code)
	}
	rec = httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gazette?day=yesterday", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET /api/gazette?day=yesterday = %d, want 400", rec.Code)
	}
	server.Gazette = nil
	rec = httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gazette", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /api/gazette with no ledger = %d, want 503", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// The four record paths
// ---------------------------------------------------------------------------

// A death is filed AND still reaches the wire. The forward is the load-bearing
// half: the room drain's new DeathEvent arm replaced the default arm that used
// to carry the event into roomEvents, and a arm that only recorded would have
// deleted the client's death handling and M23.1's death hint in silence.
func TestM341DeathIsRecordedAndStillReachesTheWire(t *testing.T) {
	world := testEmptyWorld(t)
	world.Info.Name = "GAZWORLD"
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour
	ledger, _ := m341Ledger(t, "")
	server.Gazette = ledger

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpServer := httptestServer(t, server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(ServerReadLimit)
	if err := wsjson.Write(ctx, conn, JoinMessage{Type: MessageTypeJoin, Name: "victim", Board: 1}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	var snapshot SnapshotMessage
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	server.mu.Lock()
	room, ok := server.RoomManager.Room(1)
	if !ok {
		server.mu.Unlock()
		t.Fatal("board 1 room missing")
	}
	_, statID, _ := server.RoomManager.PlayerLocation(snapshot.You.ID)
	room.Engine.PlayerFor(statID).Health = 0
	room.Engine.GameUpdateSidebar()
	room.Engine.killPlayer(statID)
	server.mu.Unlock()

	if _, ok := readUntilProtocolEvent(ctx, t, conn, server, "death", 20); !ok {
		t.Fatal("no wire death event; the drain arm must forward as well as record")
	}
	if got := m341Count(ledger.Edition("", nil), GazetteKindDeath, world.Info.Name, ""); got != 1 {
		t.Fatalf("ledger death rows for %s = %d, want 1", world.Info.Name, got)
	}
}

// Recording a deed changes no simulation state: the same death in the same room
// leaves the same StateHash with a ledger and without one.
func TestM341RecordingMovesNoStateHash(t *testing.T) {
	hashAfterDeath := func(withLedger bool) uint64 {
		world := testEmptyWorld(t)
		world.Info.Name = "GAZWORLD"
		server := NewWebSocketServer(world, 1)
		server.TickDuration = time.Hour
		if withLedger {
			ledger, _ := m341Ledger(t, "")
			server.Gazette = ledger
		} else {
			server.Gazette = nil
		}
		ctx := context.Background()
		playerID := server.RoomManager.JoinPlayer(1, 30, 12)
		room, ok := server.RoomManager.Room(1)
		if !ok {
			t.Fatal("board 1 room missing")
		}
		_, statID, _ := server.RoomManager.PlayerLocation(playerID)
		room.Engine.PlayerFor(statID).Health = 0
		room.Engine.GameUpdateSidebar()
		room.Engine.killPlayer(statID)
		for i := 0; i < 10; i++ {
			server.Tick(ctx)
		}
		return StateHash(room.Engine)
	}
	with, without := hashAfterDeath(true), hashAfterDeath(false)
	if with != without {
		t.Fatalf("StateHash with a ledger = %d, without = %d; the ledger must not touch the simulation", with, without)
	}
}

// A challenge run is not a world anyone can visit, so its deaths are not news —
// and the run's own outcome is news only once it is a durable public row. Both
// halves are asserted against a real run rather than a hand-written string.
func TestM341ChallengeRunIsNewsOnlyAsADurableRow(t *testing.T) {
	h := m321Server(t)
	ledger, _ := m341Ledger(t, "")
	h.server.Gazette = ledger

	conn, snapshot := h.run(t, "?challenge=gem-dash", &m321Ada)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, ok := h.server.ChallengeRunInstance(snapshot.ChallengeRun)
	if !ok || inst.Challenge == nil {
		t.Fatalf("no challenge instance for run %q", snapshot.ChallengeRun)
	}
	runInstanceKey := inst.Name

	// A death inside the run, drained through the same path an ordinary room's
	// is, must leave no row at all.
	inst.mu.Lock()
	room, ok := inst.RoomManager.Room(inst.Challenge.Def.Board)
	if !ok {
		inst.mu.Unlock()
		t.Fatalf("challenge board %d missing", inst.Challenge.Def.Board)
	}
	_, statID, _ := inst.RoomManager.PlayerLocation(snapshot.You.ID)
	room.Engine.PlayerFor(statID).Health = 0
	room.Engine.GameUpdateSidebar()
	room.Engine.killPlayer(statID)
	inst.mu.Unlock()
	h.server.Tick(h.ctx)

	for _, subject := range []string{runInstanceKey, inst.Challenge.Def.World} {
		if got := m341Count(ledger.Edition("", nil), GazetteKindDeath, subject, ""); got != 0 {
			t.Errorf("a death inside a challenge run was printed as news about %q (%d rows)", subject, got)
		}
	}

	run := m321Finish(t, h, inst, snapshot.You.ID)
	edition := ledger.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindChallenge, run.Def.ID, m321Ada.ID); got != 1 {
		t.Fatalf("challenge rows for %s = %d, want 1 durable row: %+v", run.Def.ID, got, edition.Items)
	}
	for _, item := range edition.Items {
		if item.Subject == runInstanceKey {
			t.Errorf("the run's private instance key reached the paper: %+v", item)
		}
	}
}

// A guest completes the same run: a real result, no public leaderboard row, and
// so no news either — the ledger follows the leaderboard's boundary rather than
// inventing a second one.
func TestM341GuestChallengeRunIsNotNews(t *testing.T) {
	h := m321Server(t)
	ledger, _ := m341Ledger(t, "")
	h.server.Gazette = ledger

	conn, snapshot := h.run(t, "?challenge=gem-dash", nil)
	defer conn.Close(websocket.StatusNormalClosure, "")
	inst, ok := h.server.ChallengeRunInstance(snapshot.ChallengeRun)
	if !ok {
		t.Fatalf("no challenge instance for run %q", snapshot.ChallengeRun)
	}
	run := m321Finish(t, h, inst, snapshot.You.ID)
	if got := m341Count(ledger.Edition("", nil), GazetteKindChallenge, run.Def.ID, ""); got != 0 {
		t.Fatalf("a guest run posted %d gazette rows; it posts no public row either", got)
	}
}

// An editor test-play copy is the one private instance kind that sanitizes as
// cleanly as TOWN does, so it needs the explicit mark — without which a
// play-test death is printed as news about a world nobody can visit.
func TestM341TestPlayCopyIsPrivateAndNotNews(t *testing.T) {
	world := testEmptyWorld(t)
	world.Info.Name = "GAZWORLD"
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour
	ledger, _ := m341Ledger(t, "")
	server.Gazette = ledger

	name, err := randomTestPlayWorldName()
	if err != nil {
		t.Fatalf("randomTestPlayWorldName: %v", err)
	}
	// The name itself sanitizes: this is exactly why the mark exists.
	if _, err := SanitizeSaveName(name); err != nil {
		t.Fatalf("SanitizeSaveName(%q) = %v; the premise of the private mark is that it passes", name, err)
	}
	if err := server.hostGeneratedWorld(name, world, true); err != nil {
		t.Fatalf("hostGeneratedWorld: %v", err)
	}
	server.mu.Lock()
	inst := server.Instances[name]
	server.mu.Unlock()
	if inst == nil || !inst.Private {
		t.Fatalf("test-play instance %q is not marked private", name)
	}

	playerID := inst.RoomManager.JoinPlayer(1, 30, 12)
	room, ok := inst.RoomManager.Room(1)
	if !ok {
		t.Fatal("board 1 room missing")
	}
	_, statID, _ := inst.RoomManager.PlayerLocation(playerID)
	room.Engine.PlayerFor(statID).Health = 0
	room.Engine.GameUpdateSidebar()
	room.Engine.killPlayer(statID)
	server.Tick(context.Background())

	if got := len(ledger.Edition("", nil).Items); got != 0 {
		t.Fatalf("a play-test death produced %d gazette rows: %+v", got, ledger.Edition("", nil).Items)
	}
	// And the notable was drained rather than accumulated, so switching the mark
	// off later cannot publish a backlog of private deeds.
	inst.mu.Lock()
	pending := len(inst.RoomManager.pendingNotables)
	inst.mu.Unlock()
	if pending != 0 {
		t.Errorf("%d notables are still queued on a private instance; they must be drained and dropped", pending)
	}
}

// A high score is news the moment the player puts a name on it.
func TestM341NamedHighScoreIsRecorded(t *testing.T) {
	world := testEmptyWorld(t)
	world.Info.Name = "GAZWORLD"
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour
	server.Auth = NewAuthService("client-id", "", "", []byte("m34-1-cookie-secret"))
	ledger, _ := m341Ledger(t, "")
	server.Gazette = ledger

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpServer := httptestServer(t, server)
	defer httpServer.Close()

	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	conn, snapshot := dialJoinWithCookie(t, ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"),
		JoinMessage{Type: MessageTypeJoin, Name: "Ada", Board: 1}, signedAuthCookie(t, server.Auth, account))
	defer conn.Close(websocket.StatusNormalClosure, "")

	inst := server.DefaultInstance
	inst.mu.Lock()
	inst.RoomManager.pendingScores = map[PlayerID]QuitResult{
		snapshot.You.ID: {PlayerID: snapshot.You.ID, Score: 500, ListPos: 1},
	}
	inst.mu.Unlock()
	server.submitHighScoreNameInInstance(ctx, inst, snapshot.You.ID, "ADA")

	edition := ledger.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindScore, world.Info.Name, account.ID); got != 1 {
		t.Fatalf("score rows = %d, want 1 for %s: %+v", got, account.ID, edition.Items)
	}
}

// A dream is filed against the world it produced and the account that asked for
// it — which is why the hook cannot live in finishGenerationJob, the one site
// with the result and not the requester.
func TestM341DreamRecordsTheWorldAndTheDreamer(t *testing.T) {
	api, _, ledger := m341API(t)
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}

	api.recordDream(account, GenerationResult{Name: "DREAM1"})
	api.recordDream(AuthenticatedAccount{}, GenerationResult{Name: "DREAM2"})
	// A generation that produced no world is not news.
	api.recordDream(account, GenerationResult{})

	edition := ledger.Edition("", func(key string) string { return key })
	if got := m341Count(edition, GazetteKindDream, "DREAM1", account.ID); got != 1 {
		t.Errorf("ada's dream rows = %d, want 1: %+v", got, edition.Items)
	}
	if got := m341Count(edition, GazetteKindDream, "DREAM2", ""); got != 1 {
		t.Errorf("the guest's dream rows = %d, want 1 unnamed row", got)
	}
	if got := len(edition.Items); got != 2 {
		t.Errorf("edition has %d rows, want 2; a nameless result must record nothing", got)
	}
}

// The flush cadence is driven off the tick clock, like the autosave beside it,
// and it is off by default so a test never writes a paper it did not ask for.
func TestM341FlushCadenceIsTickDrivenAndOffByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gazette.json")
	world := testEmptyWorld(t)
	world.Info.Name = "GAZWORLD"
	server := NewWebSocketServer(world, 1)
	server.TickDuration = time.Hour
	ledger, _ := m341Ledger(t, path)
	server.Gazette = ledger
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "")

	ctx := context.Background()
	if server.GazetteFlushEveryTicks != 0 {
		t.Fatalf("GazetteFlushEveryTicks = %d on a new server, want 0", server.GazetteFlushEveryTicks)
	}
	for i := 0; i < 5; i++ {
		server.Tick(ctx)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the ledger was written with the cadence off (stat err = %v)", err)
	}

	server.GazetteFlushEveryTicks = 3
	server.Tick(ctx)
	server.Tick(ctx)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the ledger was written before its cadence came due (stat err = %v)", err)
	}
	server.Tick(ctx)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the ledger was not written when its cadence came due: %v", err)
	}
}
