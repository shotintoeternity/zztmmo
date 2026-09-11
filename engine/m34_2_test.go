package zztgo

// M34.2 — the edition: the day's ledger written up, in a register the board
// can post.
//
// The claims these tests exist to keep, in the order the spec makes them:
//
//  1. The model is never shown a name, and no name is written down. The prompt
//     and the stored edition carry opaque actor tokens; the consented name is
//     substituted when the edition is READ, so M34.1's two invariants survive.
//  2. A refused edition is a fallback, never an outage — and a failure does not
//     poison the cache.
//  3. Renderability is proven by the ZWD text machinery: no rendered line is
//     wider than the text window and none begins with OOP markup, no matter how
//     long the substituted name is.
//  4. Spend is bounded four ways — fingerprint, interval, per-day cap, and a
//     past day written once — and an empty day costs nothing at all.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// m342Author is the fake newspaper writer. It records every prompt it is given
// — which is what the "no name reaches the model" claim is asserted against —
// and hands back canned replies in order.
type m342Author struct {
	replies  []string
	errs     []error
	prompts  []string
	systems  []string
	calls    int
	fallback string
}

func (a *m342Author) WriteGazetteEdition(_ context.Context, system, user string) (string, error) {
	a.calls++
	a.systems = append(a.systems, system)
	a.prompts = append(a.prompts, user)
	if len(a.errs) > 0 {
		err := a.errs[0]
		a.errs = a.errs[1:]
		if err != nil {
			return "", err
		}
	}
	if len(a.replies) > 0 {
		reply := a.replies[0]
		a.replies = a.replies[1:]
		return reply, nil
	}
	if a.fallback != "" {
		return a.fallback, nil
	}
	return "", errors.New("m342Author: no reply queued")
}

const m342GoodReply = "HEADLINE: A QUIET DAY, MOSTLY\n" +
	"STORY: [P1] dreamed up TOWN in the night.\n" +
	"STORY: The gate to CAVES is still shut.\n"

func m342Editor(t *testing.T, path string, author GazetteAuthor) (*GazetteEditor, *GazetteLedger, *m341Clock) {
	t.Helper()
	ledger, clock := m341Ledger(t, "")
	editor, err := NewGazetteEditor(ledger, author, path)
	if err != nil {
		t.Fatalf("NewGazetteEditor(%q): %v", path, err)
	}
	// Tests state the budget they mean rather than waiting a quarter of an hour.
	editor.MinRefresh = 0
	return editor, ledger, clock
}

func m342Refresh(t *testing.T, editor *GazetteEditor, day string) error {
	t.Helper()
	return editor.Refresh(context.Background(), day)
}

func m342Names(key string) string {
	switch key {
	case "google:ada":
		return "Ada L"
	case "google:bo":
		return "@bogart"
	}
	return ""
}

// ---------------------------------------------------------------------------
// The edition the server writes itself
// ---------------------------------------------------------------------------

// The fallback needs no model and no key, it covers every shape a day can take,
// and it says the same thing twice. It is what makes a refused reply a fallback
// rather than an outage — and what a server with no credentials posts.
func TestM342ServerWrittenEditionCoversEveryDayShapeDeterministically(t *testing.T) {
	editor, ledger, _ := m342Editor(t, "", nil)

	empty := editor.Edition("", m342Names)
	if empty.Source != GazetteEditionSourceServer || empty.Headline == "" || len(empty.Lines) != 1 {
		t.Fatalf("empty day = %+v, want one server-written line of no-news", empty)
	}
	if empty.Day != "2026-08-10" {
		t.Errorf("empty day = %q, want the clock's UTC day", empty.Day)
	}

	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	one := editor.Edition("", m342Names)
	if len(one.Lines) != 1 || !strings.Contains(one.Lines[0], "TOWN") || !strings.Contains(one.Lines[0], "Ada L") {
		t.Fatalf("one-row day = %+v, want a line naming Ada and TOWN", one.Lines)
	}

	for i := 0; i < 12; i++ {
		m341Record(t, ledger, GazetteKindDeath, fmt.Sprintf("W%d", i), "google:bo")
	}
	full := editor.Edition("", m342Names)
	if len(full.Lines) > gazetteEditionMaxRenderedLines {
		t.Fatalf("full day rendered %d lines, want at most %d", len(full.Lines), gazetteEditionMaxRenderedLines)
	}
	again := editor.Edition("", m342Names)
	if fmt.Sprint(full) != fmt.Sprint(again) {
		t.Fatalf("the server's own edition is not deterministic:\n%v\n%v", full, again)
	}
}

// ---------------------------------------------------------------------------
// The model never sees a name, and no name is written down
// ---------------------------------------------------------------------------

// Everything in the prompt is server-owned text, a UTC date, a catalogue id, a
// count, or an 8-character SanitizeSaveName survivor. A display name is an
// arbitrary player-supplied string and never leaves this process.
func TestM342AuthorPromptCarriesTokensAndNeitherNameNorAccountKey(t *testing.T) {
	withChallengeCatalogue(t, gemDashFixture())
	author := &m342Author{replies: []string{m342GoodReply}}
	editor, ledger, _ := m342Editor(t, "", author)
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	m341Record(t, ledger, GazetteKindDeath, "CAVES", "")
	m341Record(t, ledger, GazetteKindChallenge, "gem-dash", "google:ada")

	if err := m342Refresh(t, editor, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if author.calls != 1 {
		t.Fatalf("author calls = %d, want 1", author.calls)
	}
	prompt := author.prompts[0]
	for _, forbidden := range []string{"Ada L", "google:ada", "bogart", "@"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("the prompt contains %q; the author is never shown a person:\n%s", forbidden, prompt)
		}
	}
	if !strings.Contains(prompt, "[P1]") {
		t.Errorf("the prompt has no actor token:\n%s", prompt)
	}
	if strings.Contains(prompt, "[P2]") {
		t.Errorf("one account got two tokens; a person is one actor all day:\n%s", prompt)
	}
	if !strings.Contains(prompt, "TOWN") || !strings.Contains(prompt, "gem-dash") {
		t.Errorf("the prompt is missing the day's subjects:\n%s", prompt)
	}
	if !strings.Contains(prompt, "| - | 1") {
		t.Errorf("the guest's row has no actor marker:\n%s", prompt)
	}
}

// The stored edition is tokenized too. The account key is written down — the
// ledger beside it already stores exactly that — but a name is not, which is
// the invariant M34.1 established one task ago.
func TestM342StoredEditionCarriesNoName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gazette-editions.json")
	author := &m342Author{replies: []string{m342GoodReply}}
	editor, ledger, _ := m342Editor(t, path, author)
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	if err := m342Refresh(t, editor, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, forbidden := range []string{"Ada L", "bogart", "displayName"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("the editions file contains %q:\n%s", forbidden, data)
		}
	}
	if !strings.Contains(string(data), "[P1]") {
		t.Errorf("the editions file stores no token, so it cannot be named at read time:\n%s", data)
	}
}

// The consent rule is applied when the edition is read, not when it is written,
// so a player who sets a display name this afternoon is named in an edition
// that was printed this morning — and it costs no second call to the author.
func TestM342NameIsSubstitutedAtReadTime(t *testing.T) {
	author := &m342Author{replies: []string{m342GoodReply}}
	editor, ledger, _ := m342Editor(t, "", author)
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	if err := m342Refresh(t, editor, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	unnamed := editor.Edition("", func(string) string { return "" })
	if !strings.Contains(strings.ToLower(strings.Join(unnamed.Lines, "\n")), gazetteUnnamedActor) {
		t.Fatalf("an account with no consented name reads as %+v, want %q", unnamed.Lines, gazetteUnnamedActor)
	}
	named := editor.Edition("", m342Names)
	if !strings.Contains(strings.Join(named.Lines, "\n"), "Ada L") {
		t.Fatalf("after consent the edition reads %+v, want Ada named", named.Lines)
	}
	if named.Source != GazetteEditionSourceModel {
		t.Errorf("source = %q, want %q", named.Source, GazetteEditionSourceModel)
	}
	if author.calls != 1 {
		t.Errorf("author calls = %d, want 1: naming is a read, not a purchase", author.calls)
	}
	if strings.Contains(strings.Join(named.Lines, "\n"), "[P1]") {
		t.Errorf("an unreplaced token reached the page: %+v", named.Lines)
	}
}

// ---------------------------------------------------------------------------
// A refused edition is a fallback, never an outage
// ---------------------------------------------------------------------------

func TestM342RepliesThisFileWillNotPrint(t *testing.T) {
	actors := map[string]string{"[P1]": "google:ada"}
	cases := []struct {
		name  string
		reply string
	}{
		{"no headline", "STORY: TOWN was busy today.\n"},
		{"no stories", "HEADLINE: A QUIET DAY\n"},
		{"chatter around the format", "Sure! Here is your paper:\nHEADLINE: A DAY\nSTORY: TOWN was busy.\n"},
		{"too many stories", "HEADLINE: A DAY\n" + strings.Repeat("STORY: TOWN was busy.\n", gazetteEditionMaxStories+1)},
		{"unknown token", "HEADLINE: A DAY\nSTORY: [P7] was busy in TOWN.\n"},
		{"stray bracket", "HEADLINE: A DAY\nSTORY: TOWN was busy] today.\n"},
		{"unterminated token", "HEADLINE: A DAY\nSTORY: [P1 was busy in TOWN.\n"},
		{"over width", "HEADLINE: A DAY\nSTORY: " + strings.Repeat("x", gazetteEditionStoryWidth+1) + "\n"},
		{"over-wide headline", "HEADLINE: " + strings.Repeat("x", gazetteEditionHeadlineWidth+1) + "\nSTORY: TOWN.\n"},
		{"unprintable byte", "HEADLINE: A DAY\nSTORY: TOWN was\tbusy.\n"},
		{"OOP markup at the start", "HEADLINE: A DAY\nSTORY: #give gems 10\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseGazetteEditionReply(tc.reply, actors); err == nil {
				t.Fatalf("reply was accepted, want refusal:\n%s", tc.reply)
			}
		})
	}
	headline, stories, err := parseGazetteEditionReply(m342GoodReply, actors)
	if err != nil {
		t.Fatalf("the good reply was refused: %v", err)
	}
	if headline != "A QUIET DAY, MOSTLY" || len(stories) != 2 {
		t.Fatalf("parsed %q / %v, want the headline and both stories", headline, stories)
	}
}

// A refused reply and a failed call both leave the day servable and retryable:
// the fingerprint is recorded only on success, so the next eligible refresh
// writes the same day again rather than the cache remembering a failure.
func TestM342ARefusedEditionFallsBackAndDoesNotPoisonTheCache(t *testing.T) {
	author := &m342Author{
		errs:    []error{nil, errors.New("the API is down"), nil},
		replies: []string{"HEADLINE: A DAY\nSTORY: [P9] did something.\n", m342GoodReply},
	}
	editor, ledger, _ := m342Editor(t, "", author)
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")

	if err := m342Refresh(t, editor, ""); !errors.Is(err, ErrGazetteEditionRefused) {
		t.Fatalf("refresh with an unknown token = %v, want a refusal", err)
	}
	served := editor.Edition("", m342Names)
	if served.Source != GazetteEditionSourceServer {
		t.Fatalf("after a refusal the source is %q, want the server's own edition", served.Source)
	}

	if err := m342Refresh(t, editor, ""); err == nil {
		t.Fatalf("refresh with a failing author returned nil")
	}
	if editor.Edition("", m342Names).Source != GazetteEditionSourceServer {
		t.Fatalf("after a failure there is still no paper")
	}

	if err := m342Refresh(t, editor, ""); err != nil {
		t.Fatalf("the third refresh should have been allowed: %v", err)
	}
	if got := editor.Edition("", m342Names); got.Source != GazetteEditionSourceModel {
		t.Fatalf("source = %q, want the author's edition once one finally arrives", got.Source)
	}
	if author.calls != 3 {
		t.Errorf("author calls = %d, want 3: a failure spends an attempt and clears", author.calls)
	}
}

// An editor with no author is a supported configuration, not an error the
// lobby has to render.
func TestM342NoAuthorStillHasAPaper(t *testing.T) {
	editor, ledger, _ := m342Editor(t, "", nil)
	m341Record(t, ledger, GazetteKindDeath, "TOWN", "")
	if err := m342Refresh(t, editor, ""); !errors.Is(err, ErrGazetteAuthorUnavailable) {
		t.Fatalf("Refresh with no author = %v, want ErrGazetteAuthorUnavailable", err)
	}
	edition := editor.Edition("", m342Names)
	if edition.Source != GazetteEditionSourceServer || len(edition.Lines) == 0 {
		t.Fatalf("edition = %+v, want the server's own paper", edition)
	}
}

// ---------------------------------------------------------------------------
// Renderability, proven by the ZWD text machinery
// ---------------------------------------------------------------------------

// The longest name a profile can carry, spliced into the longest line the
// author may write, still cannot write through the text window's border — and a
// claimed handle, which GazetteConsentedName returns as "@bogart", cannot start
// a line and turn a story into an OOP title.
func TestM342ASubstitutedNameCannotOverflowOrStartALineWithMarkup(t *testing.T) {
	stored := gazetteStoredEdition{
		Day:      "2026-08-10",
		Headline: "[P1] AND [P2] IN ONE VERY LONG HEADLINE",
		Lines: []string{
			"[P1] and " + strings.Repeat("x", gazetteEditionStoryWidth-16) + " [P2]",
			"[P2] took the gate.",
		},
		Actors: map[string]string{"[P1]": "google:ada", "[P2]": "google:bo"},
		Source: GazetteEditionSourceModel,
	}
	longest := strings.Repeat("W", ProfileDisplayNameMax)
	rendered := renderGazetteEdition(stored, func(key string) string {
		if key == "google:ada" {
			return longest
		}
		return "@bogart"
	})
	if len(rendered.Headline) > gazetteEditionHeadlineWidth {
		t.Errorf("headline is %d characters, want at most %d: %q",
			len(rendered.Headline), gazetteEditionHeadlineWidth, rendered.Headline)
	}
	if len(rendered.Lines) == 0 {
		t.Fatal("nothing rendered")
	}
	for _, line := range rendered.Lines {
		if len(line) > zztTextWindowLineWidth {
			t.Errorf("line is %d characters, want at most %d: %q", len(line), zztTextWindowLineWidth, line)
		}
		if strings.ContainsAny(line[:1], gazetteOOPLeadingBytes) {
			t.Errorf("line begins with OOP markup: %q", line)
		}
	}
	if !strings.Contains(strings.Join(rendered.Lines, "\n"), "@bogart") {
		t.Errorf("the handle never appeared: %v", rendered.Lines)
	}
	// The whole point of the reserved column: the guard's space still fits.
	if got := guardGazetteLine(strings.Repeat("x", gazetteEditionStoryWidth)); len(got) > zztTextWindowLineWidth {
		t.Errorf("a guarded full-width line is %d characters", len(got))
	}
}

// A stored profile is not trusted to have been sanitized when it was set.
func TestM342SubstitutedNamesAreSanitizedAndCapitalizedAtLineStart(t *testing.T) {
	stored := gazetteStoredEdition{
		Day:      "2026-08-10",
		Headline: "THE PAPER",
		Lines:    []string{"[P1] walked into TOWN."},
		Actors:   map[string]string{"[P1]": "google:ada"},
		Source:   GazetteEditionSourceModel,
	}
	rendered := renderGazetteEdition(stored, func(string) string { return "Ada\x07\nL" })
	if got := rendered.Lines[0]; got != "AdaL walked into TOWN." {
		t.Errorf("line = %q, want the control bytes stripped", got)
	}
	unnamed := renderGazetteEdition(stored, func(string) string { return "" })
	if got := unnamed.Lines[0]; !strings.HasPrefix(got, "A stranger") {
		t.Errorf("line = %q, want a capitalized substitution at the start of a line", got)
	}
}

// ---------------------------------------------------------------------------
// Spend
// ---------------------------------------------------------------------------

func TestM342SpendIsBoundedFourWays(t *testing.T) {
	t.Run("an empty day costs nothing", func(t *testing.T) {
		author := &m342Author{fallback: m342GoodReply}
		editor, _, _ := m342Editor(t, "", author)
		if err := m342Refresh(t, editor, ""); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if author.calls != 0 {
			t.Errorf("author calls = %d on a day with no rows, want 0", author.calls)
		}
	})

	t.Run("an unchanged day costs nothing", func(t *testing.T) {
		author := &m342Author{fallback: m342GoodReply}
		editor, ledger, _ := m342Editor(t, "", author)
		m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
		for i := 0; i < 3; i++ {
			if err := m342Refresh(t, editor, ""); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
		}
		if author.calls != 1 {
			t.Fatalf("author calls = %d, want 1: the fingerprint has not moved", author.calls)
		}
		m341Record(t, ledger, GazetteKindDeath, "CAVES", "google:bo")
		if err := m342Refresh(t, editor, ""); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if author.calls != 2 {
			t.Errorf("author calls = %d, want 2: a new row is news", author.calls)
		}
	})

	t.Run("a changed day waits out the interval", func(t *testing.T) {
		author := &m342Author{fallback: m342GoodReply}
		editor, ledger, clock := m342Editor(t, "", author)
		editor.MinRefresh = time.Hour
		m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
		if err := m342Refresh(t, editor, ""); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		m341Record(t, ledger, GazetteKindDream, "CAVES", "google:ada")
		if err := m342Refresh(t, editor, ""); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if author.calls != 1 {
			t.Fatalf("author calls = %d inside the interval, want 1", author.calls)
		}
		clock.at = clock.at.Add(2 * time.Hour)
		if err := m342Refresh(t, editor, ""); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if author.calls != 2 {
			t.Errorf("author calls = %d after the interval, want 2", author.calls)
		}
	})

	t.Run("a day has a ceiling", func(t *testing.T) {
		author := &m342Author{fallback: m342GoodReply}
		editor, ledger, _ := m342Editor(t, "", author)
		editor.DailyWrites = 2
		for i := 0; i < 6; i++ {
			m341Record(t, ledger, GazetteKindDeath, fmt.Sprintf("W%d", i), "google:ada")
			if err := m342Refresh(t, editor, ""); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
		}
		if author.calls != 2 {
			t.Errorf("author calls = %d, want the ceiling of 2", author.calls)
		}
	})

	t.Run("a past day is written once", func(t *testing.T) {
		author := &m342Author{fallback: m342GoodReply}
		editor, ledger, clock := m342Editor(t, "", author)
		m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
		yesterday := clock.at.UTC().Format(gazetteDayFormat)
		clock.addDays(1)
		if err := m342Refresh(t, editor, yesterday); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if author.calls != 1 {
			t.Fatalf("author calls = %d, want 1: a past day with no paper still gets one", author.calls)
		}
		m341Record(t, ledger, GazetteKindDeath, "CAVES", "google:bo")
		if err := m342Refresh(t, editor, yesterday); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if author.calls != 1 {
			t.Errorf("author calls = %d, want 1: yesterday's paper has already been read", author.calls)
		}
	})
}

// ---------------------------------------------------------------------------
// The cache on disk
// ---------------------------------------------------------------------------

func TestM342EditionCacheSurvivesRestartAndKeepsItsBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gazette-editions.json")
	author := &m342Author{fallback: m342GoodReply}
	editor, ledger, clock := m342Editor(t, path, author)
	editor.DailyWrites = 1
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	if err := m342Refresh(t, editor, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	reopened, err := NewGazetteEditor(ledger, author, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened.MinRefresh = 0
	reopened.DailyWrites = 1
	edition := reopened.Edition("", m342Names)
	if edition.Source != GazetteEditionSourceModel || !strings.Contains(edition.Headline, "QUIET") {
		t.Fatalf("edition after restart = %+v, want the one that was already bought", edition)
	}
	m341Record(t, ledger, GazetteKindDeath, "CAVES", "google:bo")
	if err := reopened.Refresh(context.Background(), ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if author.calls != 1 {
		t.Errorf("author calls = %d, want 1: the daily ceiling survives a restart", author.calls)
	}

	// Only the file it renamed into place is left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "gazette-editions.json" {
		t.Errorf("directory after a write = %v, want only the editions file", entries)
	}

	// Retention keeps the same week the ledger does.
	for day := 0; day < gazetteRetentionDays+3; day++ {
		clock.addDays(1)
		reopened.DailyWrites = 1
		reopened.attempts = make(map[string]int)
		m341Record(t, ledger, GazetteKindDeath, "TOWN", "google:ada")
		if err := reopened.Refresh(context.Background(), ""); err != nil {
			t.Fatalf("Refresh on day %d: %v", day, err)
		}
	}
	reopened.mu.Lock()
	kept := len(reopened.editions)
	reopened.mu.Unlock()
	if kept > gazetteRetentionDays {
		t.Errorf("kept %d editions, want at most %d", kept, gazetteRetentionDays)
	}
}

func TestM342EditionCacheRefusesAFutureVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gazette-editions.json")
	data := []byte(fmt.Sprintf(`{"version":%d,"editions":{}}`, gazetteEditionStoreVersion+1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ledger, _ := m341Ledger(t, "")
	if _, err := NewGazetteEditor(ledger, nil, path); err == nil {
		t.Fatal("a future editions file was read; it must be refused rather than half-read")
	}
}

// ---------------------------------------------------------------------------
// The route
// ---------------------------------------------------------------------------

func TestM342EditionRouteServesAPaperAndLeaksNoAccountKey(t *testing.T) {
	api, server, ledger := m341API(t)
	if err := server.ChatDB.PutAccountPreferences("google:ada", AccountPreferences{
		Profile: AccountProfilePreferences{DisplayName: "Ada L"},
	}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")

	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gazette/edition", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/gazette/edition = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "google:ada") || strings.Contains(body, "accountKey") || strings.Contains(body, "[P1]") {
		t.Fatalf("the edition leaks server-side state:\n%s", body)
	}
	var payload GazetteRenderedEdition
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Day != "2026-08-10" || payload.Source != GazetteEditionSourceServer || len(payload.Lines) == 0 {
		t.Fatalf("payload = %+v, want the server's own edition of today", payload)
	}
	if !strings.Contains(strings.Join(payload.Lines, "\n"), "Ada L") {
		t.Errorf("Ada consented to a name and is not in the paper: %v", payload.Lines)
	}

	rec = httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/gazette/edition", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
	rec = httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gazette/edition?day=yesterday", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET ?day=yesterday = %d, want 400", rec.Code)
	}
	server.Gazette = nil
	rec = httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/gazette/edition", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET with no ledger = %d, want 503", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// The real author
// ---------------------------------------------------------------------------

// The generation service is the Gazette's author, and it spends the edition's
// own small ceiling rather than the budget sized for painting a board.
func TestM342GenerationServiceWritesWithTheEditionsOwnTokenCeiling(t *testing.T) {
	var seen struct {
		MaxTokens int `json:"max_tokens"`
		System    []struct {
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]interface{}{"content": []map[string]string{{"type": "text", "text": m342GoodReply}}})
	}))
	defer stub.Close()

	service, err := NewGenerationService(GenerationConfig{
		APIURL: stub.URL, APIKey: "test-key", Model: "test-model", MaxTokens: 6144,
		MaxConcurrent: 1, RateLimit: -1, OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewGenerationService: %v", err)
	}
	editor, ledger, _ := m342Editor(t, "", service)
	m341Record(t, ledger, GazetteKindDream, "TOWN", "google:ada")
	if err := m342Refresh(t, editor, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if seen.MaxTokens != GazetteEditionMaxTokens {
		t.Errorf("max_tokens = %d, want the edition's own ceiling of %d (not the world budget)",
			seen.MaxTokens, GazetteEditionMaxTokens)
	}
	if len(seen.Messages) != 1 || !strings.Contains(seen.Messages[0].Content, "TOWN") {
		t.Errorf("the request carried no day: %+v", seen.Messages)
	}
	if len(seen.System) == 0 || !strings.Contains(seen.System[0].Text, "ZZT GAZETTE") {
		t.Errorf("the request carried no system prompt: %+v", seen.System)
	}
	if got := editor.Edition("", m342Names); got.Source != GazetteEditionSourceModel {
		t.Errorf("source = %q, want the author's edition", got.Source)
	}
}
