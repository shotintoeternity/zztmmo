package zztgo

// The ZZT Gazette's ledger (M34.1): what the service saw happen today, in the
// shape a daily edition can be written from.
//
// Three properties are load-bearing and each one is a decision recorded in
// TASKS.md's M34 preamble rather than an accident of this file:
//
//   - Nothing here is simulation. The ledger is written from the server layer
//     only, is never read by sim code, and never enters StateHash. A newspaper
//     needs a wall clock and the simulation may not have one (CLAUDE.md rule 2),
//     so the clock is injected HERE, at the ledger's boundary, and lives nowhere
//     else in the feature.
//
//   - No name reaches disk. An entry stores the account key the way
//     ChallengeStore does and nothing else about the person; the consented name
//     is resolved when an edition is READ. That keeps the tick goroutine out of
//     the preferences store on the death path, and it means the only name that
//     is ever written down is one a profile consented to (M24.1) rather than
//     whatever display name an OAuth provider handed us.
//
//   - Recording never writes a file. Deaths arrive on the tick goroutine, so
//     Record mutates memory and marks the ledger dirty; Flush is driven on a
//     cadence beside maybeAutosave, which is where this server already chose to
//     pay for file work (M16.14e: a tick waits on nothing).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The four kinds of happening a v1 edition is written from. "Worlds beaten" is
// deliberately absent: this fork replaced vanilla's game-over with mp-respawn
// (PARITY.md §4), so there is no win signal to read and inventing one would be
// a simulation change.
const (
	// GazetteKindDream is a generated world that finished. Subject is the world.
	GazetteKindDream = "dream"
	// GazetteKindChallenge is a durable leaderboard row (M32.1). Subject is the
	// challenge id, which is why admission here is the catalogue rather than a
	// world identity.
	GazetteKindChallenge = "challenge"
	// GazetteKindScore is a high-score entry the player named. Subject is the
	// world it was set in.
	GazetteKindScore = "score"
	// GazetteKindDeath is one player's death. Subject is the world.
	GazetteKindDeath = "death"
)

const (
	// gazetteStoreVersion is the on-disk envelope version. A file from a future
	// version is refused rather than half-read, exactly as ChallengeStore does.
	gazetteStoreVersion = 1
	// gazetteRetentionDays is how many day-editions are kept, today included.
	// Older days are evicted; the Gazette is a newspaper, not an audit log.
	gazetteRetentionDays = 7
	// gazetteRowsPerKind bounds one kind within one day, and gazetteRowsPerDay
	// bounds the day. These REFUSE new rows rather than evicting old ones: a row
	// that exists keeps counting, so a busy day degrades into "the first forty
	// worlds anyone died in, with honest counts" rather than into a churning
	// window that under-reports everybody.
	gazetteRowsPerKind = 40
	gazetteRowsPerDay  = 120
	// gazetteDayFormat is UTC, because a newspaper needs one calendar and the
	// service has no player timezone to prefer (the call M32.1 made for choosing
	// today's challenge). ISO order is also lexical order, which is what lets
	// retention sort day keys as strings.
	gazetteDayFormat = "2006-01-02"
	// DefaultGazetteFlushSeconds is how often the running service writes the
	// ledger. Thirty seconds of counts is what a crash costs, which is the right
	// price for a newspaper and the wrong price for a tick spent on disk.
	DefaultGazetteFlushSeconds = 30
)

// ErrInvalidGazetteHappening refuses a happening that has no kind, no subject,
// or a subject its kind does not admit.
var ErrInvalidGazetteHappening = errors.New("zztgo: invalid gazette happening")

// GazetteHappening is one deed as the server observed it. AccountKey is the
// durable account id or empty for a guest; it never leaves this process.
type GazetteHappening struct {
	Kind       string
	Subject    string
	AccountKey string
}

// GazetteEntry is one stored row: one (kind, subject, account) per day, with a
// count. A player who dies forty times in TOWN is one row saying forty, which
// is both the flood control and the better copy.
type GazetteEntry struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	// AccountKey is server-side only. GazetteItem, the public projection, has no
	// field for it.
	AccountKey string `json:"accountKey,omitempty"`
	Count      int    `json:"count"`
	// Seq is the opaque server order a row was first seen in. It is the last
	// tie-break in a rendered edition and the order rows fill their day's bound
	// in, and it is never an account id: a tie-break that reads one leaks one.
	Seq int64 `json:"seq"`
}

// GazetteItem is the public projection — everything M34.2 needs to write an
// edition from and nothing else. Name is the consented name or empty, and empty
// means "a stranger", not "unknown".
type GazetteItem struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Name    string `json:"name,omitempty"`
	Count   int    `json:"count"`
}

// GazetteEdition is one day's ledger as it is served.
type GazetteEdition struct {
	Day   string        `json:"day"`
	Items []GazetteItem `json:"items"`
}

type gazetteFile struct {
	Version int                       `json:"version"`
	Seq     int64                     `json:"seq"`
	Days    map[string][]GazetteEntry `json:"days"`
}

// GazetteLedger is the store. An empty path is memory-only, which is what tests
// and a server without a saves directory get.
type GazetteLedger struct {
	mu    sync.Mutex
	path  string
	now   func() time.Time
	seq   int64
	days  map[string][]GazetteEntry
	dirty bool
}

// NewGazetteLedger loads the ledger at path, or returns an empty memory-only
// one when path is empty. now may be nil, in which case time.Now is used — it
// is a parameter so that every test of this file states the day it means.
func NewGazetteLedger(path string, now func() time.Time) (*GazetteLedger, error) {
	if now == nil {
		now = time.Now
	}
	ledger := &GazetteLedger{path: path, now: now, days: make(map[string][]GazetteEntry)}
	if path == "" {
		return ledger, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ledger, nil
		}
		return nil, err
	}
	var file gazetteFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("gazette ledger %s: %w", path, err)
	}
	if file.Version > gazetteStoreVersion {
		return nil, fmt.Errorf("gazette ledger %s: unsupported version %d", path, file.Version)
	}
	ledger.seq = file.Seq
	for day, entries := range file.Days {
		kept := make([]GazetteEntry, 0, len(entries))
		for _, entry := range entries {
			if _, ok := admitGazetteSubject(entry.Kind, entry.Subject); !ok || entry.Count <= 0 {
				continue
			}
			kept = append(kept, entry)
		}
		if len(kept) > 0 {
			ledger.days[day] = kept
		}
	}
	ledger.pruneLocked()
	return ledger, nil
}

// Record files one happening against today. It is nil-safe — a server without a
// ledger records nothing rather than branching at every call site — and it never
// touches disk.
func (g *GazetteLedger) Record(happening GazetteHappening) error {
	if g == nil {
		return nil
	}
	subject, ok := admitGazetteSubject(happening.Kind, happening.Subject)
	if !ok {
		return ErrInvalidGazetteHappening
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	day := g.now().UTC().Format(gazetteDayFormat)
	if g.days == nil {
		g.days = make(map[string][]GazetteEntry)
	}
	entries := g.days[day]
	for i := range entries {
		if entries[i].Kind == happening.Kind && entries[i].Subject == subject &&
			entries[i].AccountKey == happening.AccountKey {
			entries[i].Count++
			g.dirty = true
			return nil
		}
	}
	if len(entries) >= gazetteRowsPerDay {
		return nil
	}
	kindRows := 0
	for i := range entries {
		if entries[i].Kind == happening.Kind {
			kindRows++
		}
	}
	if kindRows >= gazetteRowsPerKind {
		return nil
	}
	g.seq++
	g.days[day] = append(entries, GazetteEntry{
		Kind:       happening.Kind,
		Subject:    subject,
		AccountKey: happening.AccountKey,
		Count:      1,
		Seq:        g.seq,
	})
	g.pruneLocked()
	g.dirty = true
	return nil
}

// admitGazetteSubject is the one admission gate, and it is deliberately the
// boundary the rest of the service already uses rather than a new list to keep
// in sync. A world-shaped subject must pass SanitizeSaveName — which is exactly
// what a challenge run's instance key is a refusal of (M32.1), so a private run
// is excluded by the existing rule. A challenge-shaped subject must name a
// challenge the server's own catalogue knows.
func admitGazetteSubject(kind, subject string) (string, bool) {
	switch kind {
	case GazetteKindDream, GazetteKindScore, GazetteKindDeath:
		safe, err := SanitizeSaveName(subject)
		if err != nil {
			return "", false
		}
		return safe, true
	case GazetteKindChallenge:
		def, ok := ChallengeByID(subject)
		if !ok {
			return "", false
		}
		// The catalogue's own id, not the caller's casing — otherwise "GEMDASH"
		// and "gemdash" are two rows about one challenge.
		return def.ID, true
	default:
		return "", false
	}
}

// Edition renders one day. day may be empty for today; a day outside the
// retention window renders empty rather than failing, because "no paper that
// day" is the honest answer and an error would make the caller invent one.
//
// resolveName is the consent rule's other half: it is handed an account key and
// returns the name that account has consented to be known by, or "". A nil
// resolver names nobody, which is what a server with no preferences store owes.
func (g *GazetteLedger) Edition(day string, resolveName func(accountKey string) string) GazetteEdition {
	if g == nil {
		return GazetteEdition{Day: day}
	}
	day, entries := g.dayEntries(day)

	items := make([]GazetteItem, 0, len(entries))
	for _, entry := range entries {
		item := GazetteItem{Kind: entry.Kind, Subject: entry.Subject, Count: entry.Count}
		if entry.AccountKey != "" && resolveName != nil {
			item.Name = resolveName(entry.AccountKey)
		}
		items = append(items, item)
	}
	return GazetteEdition{Day: day, Items: items}
}

// dayEntries is one day's rows in the order every reader of this ledger sees
// them, with the account keys still attached. Edition names them; the edition
// writer (M34.2) tokenizes them instead, which is why the ordering lives here
// rather than in either caller: two papers about one day must not disagree
// about what the day looked like.
func (g *GazetteLedger) dayEntries(day string) (string, []GazetteEntry) {
	if g == nil {
		return day, nil
	}
	g.mu.Lock()
	if day == "" {
		day = g.now().UTC().Format(gazetteDayFormat)
	}
	entries := append([]GazetteEntry(nil), g.days[day]...)
	g.mu.Unlock()

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		if entries[i].Subject != entries[j].Subject {
			return entries[i].Subject < entries[j].Subject
		}
		return entries[i].Seq < entries[j].Seq
	})
	return day, entries
}

// editionsPath is where the day's written-up editions live: beside the ledger,
// in their own file, because they have their own writer, their own mutex and
// their own cadence (M34.2).
func (g *GazetteLedger) editionsPath() string {
	if g == nil || g.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(g.path), "gazette-editions.json")
}

// Days lists the retained day keys, newest first.
func (g *GazetteLedger) Days() []string {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	days := make([]string, 0, len(g.days))
	for day := range g.days {
		days = append(days, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	return days
}

// Flush writes the ledger if anything has changed since the last write. It is
// the only path that touches disk, and it is driven on a cadence rather than
// from Record.
func (g *GazetteLedger) Flush() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.dirty || g.path == "" {
		g.dirty = false
		return nil
	}
	if err := g.writeLocked(); err != nil {
		return err
	}
	g.dirty = false
	return nil
}

// pruneLocked evicts every day outside the retention window. ISO day keys sort
// lexically, so this needs no date parsing and cannot be confused by a clock
// that moved.
func (g *GazetteLedger) pruneLocked() {
	if len(g.days) <= gazetteRetentionDays {
		return
	}
	days := make([]string, 0, len(g.days))
	for day := range g.days {
		days = append(days, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	for _, day := range days[gazetteRetentionDays:] {
		delete(g.days, day)
	}
}

func (g *GazetteLedger) writeLocked() error {
	file := gazetteFile{Version: gazetteStoreVersion, Seq: g.seq, Days: g.days}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(g.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gazette-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, g.path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// GazetteConsentedName is the consent rule itself, in one place so the ledger,
// the API and any later edition writer cannot drift apart on it. An account is
// named only by the deliberate public subset M24.1 built: a display name, or
// failing that a claimed handle. An account that set neither has not asked to be
// visible, and its deeds are printed without a name.
func GazetteConsentedName(prefs AccountPreferences, found bool) string {
	if !found {
		return ""
	}
	if prefs.Profile.DisplayName != "" {
		return prefs.Profile.DisplayName
	}
	if prefs.Profile.Handle != "" {
		return "@" + prefs.Profile.Handle
	}
	return ""
}
