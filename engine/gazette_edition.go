package zztgo

// The ZZT Gazette's edition (M34.2): the day's ledger written up in ZZT's own
// terse, slightly wrong register, in the shape a board can post.
//
// Four properties are load-bearing, and each is a decision in TASKS.md's M34.2
// spec rather than an accident of this file:
//
//   - The model is never shown a name, and the edition does not store one. The
//     prompt and the stored edition carry opaque actor tokens ("[P1]"); the
//     consented name is substituted when an edition is READ, through the same
//     resolver /api/gazette already uses. That keeps both invariants M34.1
//     established — no name reaches disk, and a player who sets a display name
//     this afternoon is named in this morning's deeds — and it means a
//     player-supplied string never leaves this process to a third party's API.
//     What is left in the prompt is server-owned text, a catalogue id, a count,
//     and an 8-character SanitizeSaveName survivor: no attacker-controlled byte.
//
//   - A refused edition is a fallback, never an outage. The server can always
//     write the day itself, deterministically and without a model, so a missing
//     API key, a failed call, or a reply this file refuses all still produce a
//     paper. A failure does not poison the cache: the fingerprint is only
//     recorded on success, so the next eligible refresh tries again.
//
//   - Renderability is proven by the ZWD text machinery rather than by a
//     private copy of its rules (zwd.go:955). A rendered line is wrapped by
//     wrapZWDText and can never begin with one of OOP's significant bytes, no
//     matter how long the substituted name is or where the wrapper split it.
//
//   - Spend is bounded four ways and never on the tick goroutine: an unchanged
//     day costs nothing (item fingerprint), a changed day is refreshed no more
//     often than MinRefresh, a day is attempted at most DailyWrites times, and
//     a past day is written once and never rewritten. The HTTP handler serves
//     what is cached and kicks a single-flight background refresh.
//
// The prose itself is deliberately NOT fact-checked. "Slightly wrong" is the
// register the product asked for; the guarantees here are structural — which
// names may appear, how long a line may be, which byte it may start with, and
// what it may cost — not whether the copy is accurate about the day.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// gazetteEditionStoreVersion is the on-disk envelope version, refused from
	// the future exactly as the ledger's is.
	gazetteEditionStoreVersion = 1
	// gazetteEditionMaxStories bounds a reply. A newspaper posted on a ZZT board
	// is a scroll somebody reads standing up.
	gazetteEditionMaxStories = 6
	// gazetteEditionMaxRenderedLines bounds what wrapping can produce, so a day
	// full of long names cannot grow the board without limit.
	gazetteEditionMaxRenderedLines = 16
	// gazetteEditionPromptRows bounds what a day hands the author.
	gazetteEditionPromptRows = 20
	// gazetteEditionStoryWidth reserves one column of the text window's 42 so
	// that a line which would otherwise begin with an OOP-significant byte can
	// be given a leading space and still fit.
	gazetteEditionStoryWidth    = zztTextWindowLineWidth - 1
	gazetteEditionHeadlineWidth = zztTextWindowTitleMax
	// gazetteEditionNameMax is the longest substitution: a display name, or a
	// handle with the '@' GazetteConsentedName puts in front of it.
	gazetteEditionNameMax = ProfileDisplayNameMax + 1
	// gazetteUnnamedActor is what a token becomes when its account has not
	// consented to a name. It is deliberately a phrase and not "unknown": the
	// deed is real, the person is simply not being named.
	gazetteUnnamedActor = "a stranger"

	// GazetteEditionSourceModel and GazetteEditionSourceServer say who wrote the
	// edition being served. The board (M34.3) may care; a reader certainly does.
	GazetteEditionSourceModel  = "model"
	GazetteEditionSourceServer = "server"

	// GazetteEditionMaxTokens is the author's own ceiling. It is NOT the
	// world-painting budget callBlocks otherwise uses: an edition is a headline
	// and six short lines, and paying a board's ceiling for it is a bug.
	GazetteEditionMaxTokens = 700
	// DefaultGazetteEditionMinRefresh is the shortest gap between two attempts
	// at one day, and DefaultGazetteEditionDailyWrites is how many attempts a
	// single day may ever cost. Both survive a restart in the second case only:
	// the counter is persisted, the clock is not, which forgives the interval
	// after a deploy and never forgives the cap.
	DefaultGazetteEditionMinRefresh  = 15 * time.Minute
	DefaultGazetteEditionDailyWrites = 6
)

// gazetteOOPLeadingBytes are the bytes ZZT-OOP reads as markup at the start of
// a line (zwd.go's wrapZWDTextWindowLines switches on exactly these). A
// rendered Gazette line may never begin with one, or a story about a player
// called "@ada" becomes a text-window title.
const gazetteOOPLeadingBytes = "@#:'/?$!"

// ErrGazetteAuthorUnavailable is a refresh asked of an editor with no author.
// It is not a server failure: the fallback edition is still served.
var ErrGazetteAuthorUnavailable = errors.New("zztgo: no gazette author configured")

// ErrGazetteEditionRefused is a reply this file would not print.
var ErrGazetteEditionRefused = errors.New("zztgo: gazette edition refused")

// GazetteAuthor writes an edition from a prompt. It is deliberately narrow —
// two strings in, one string out — so the Gazette can be tested without an
// HTTP stub and so nothing about world generation leaks into the newspaper.
type GazetteAuthor interface {
	WriteGazetteEdition(ctx context.Context, system, user string) (string, error)
}

// WriteGazetteEdition makes the generation service the Gazette's author. It
// reuses the one place in this repo that talks to the API, with the edition's
// own small token ceiling rather than the world-painting one, and it does not
// pass through Generate's DailyMax admission, which counts worlds.
func (g *GenerationService) WriteGazetteEdition(ctx context.Context, system, user string) (string, error) {
	if g == nil {
		return "", ErrGazetteAuthorUnavailable
	}
	return g.callBlocksWithTokens(ctx, []systemBlock{ephemeralBlock(system)}, user, GazetteEditionMaxTokens)
}

// GazetteRenderedEdition is one day's paper as it is served: names already
// substituted, lines already wrapped, nothing server-side left in it.
type GazetteRenderedEdition struct {
	Day      string   `json:"day"`
	Headline string   `json:"headline"`
	Lines    []string `json:"lines"`
	Source   string   `json:"source"`
}

// gazetteStoredEdition is the tokenized edition, which is the only form that is
// ever written down. Actors maps a token to the account key the ledger already
// stores; no name appears here.
type gazetteStoredEdition struct {
	Day         string            `json:"day"`
	Headline    string            `json:"headline"`
	Lines       []string          `json:"lines"`
	Actors      map[string]string `json:"actors,omitempty"`
	Source      string            `json:"source"`
	Fingerprint string            `json:"fingerprint"`
}

type gazetteEditionFile struct {
	Version  int                             `json:"version"`
	Editions map[string]gazetteStoredEdition `json:"editions"`
	// Attempts is how many times each day has cost an author call, successful
	// or not. It is persisted so a restart cannot buy a second day's budget.
	Attempts map[string]int `json:"attempts,omitempty"`
}

// GazetteEditor turns days into editions and remembers the ones it bought.
type GazetteEditor struct {
	mu     sync.Mutex
	path   string
	ledger *GazetteLedger
	author GazetteAuthor
	now    func() time.Time

	// MinRefresh and DailyWrites are the spend bounds; both are fields so a
	// test states the budget it means instead of waiting fifteen minutes.
	MinRefresh  time.Duration
	DailyWrites int

	editions    map[string]gazetteStoredEdition
	attempts    map[string]int
	lastAttempt map[string]time.Time
	inflight    map[string]bool
}

// NewGazetteEditor loads the edition cache at path, or keeps it in memory when
// path is empty. A nil author is a supported configuration: the editor then
// only ever serves the server-written edition, which is exactly what a server
// without Anthropic credentials owes its lobby.
func NewGazetteEditor(ledger *GazetteLedger, author GazetteAuthor, path string) (*GazetteEditor, error) {
	now := time.Now
	if ledger != nil && ledger.now != nil {
		now = ledger.now
	}
	editor := &GazetteEditor{
		path:        path,
		ledger:      ledger,
		author:      author,
		now:         now,
		MinRefresh:  DefaultGazetteEditionMinRefresh,
		DailyWrites: DefaultGazetteEditionDailyWrites,
		editions:    make(map[string]gazetteStoredEdition),
		attempts:    make(map[string]int),
		lastAttempt: make(map[string]time.Time),
		inflight:    make(map[string]bool),
	}
	if path == "" {
		return editor, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return editor, nil
		}
		return nil, err
	}
	var file gazetteEditionFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("gazette editions %s: %w", path, err)
	}
	if file.Version > gazetteEditionStoreVersion {
		return nil, fmt.Errorf("gazette editions %s: unsupported version %d", path, file.Version)
	}
	for day, edition := range file.Editions {
		if edition.Headline == "" || len(edition.Lines) == 0 {
			continue
		}
		editor.editions[day] = edition
	}
	for day, count := range file.Attempts {
		if count > 0 {
			editor.attempts[day] = count
		}
	}
	editor.pruneLocked()
	return editor, nil
}

// Edition serves the best paper available for a day: the edition an author
// wrote if one has been bought, and otherwise the deterministic one the server
// writes from the same rows. resolveName is the consent rule's other half,
// exactly as GazetteLedger.Edition takes it.
func (e *GazetteEditor) Edition(day string, resolveName func(accountKey string) string) GazetteRenderedEdition {
	if e == nil {
		return GazetteRenderedEdition{Day: day, Source: GazetteEditionSourceServer}
	}
	day, entries := e.ledger.dayEntries(day)
	e.mu.Lock()
	stored, ok := e.editions[day]
	e.mu.Unlock()
	if !ok || stored.Fingerprint == "" {
		stored = composeGazetteFallbackEdition(day, entries)
	}
	return renderGazetteEdition(stored, resolveName)
}

// Refresh writes the day's edition if it is eligible, synchronously. It is the
// seam a test drives; the service reaches it through RefreshAsync.
func (e *GazetteEditor) Refresh(ctx context.Context, day string) error {
	if e == nil {
		return nil
	}
	if e.author == nil {
		return ErrGazetteAuthorUnavailable
	}
	day, entries := e.ledger.dayEntries(day)
	rows, actors := gazetteEditionRows(entries)
	if len(rows) == 0 {
		// A day with nothing in it is not worth a call: the server's own empty
		// edition says "no news" better than a paid sentence would.
		return nil
	}
	fingerprint := gazetteEditionFingerprint(entries)
	today := e.now().UTC().Format(gazetteDayFormat)

	e.mu.Lock()
	if e.inflight[day] {
		e.mu.Unlock()
		return nil
	}
	stored, have := e.editions[day]
	switch {
	case have && stored.Fingerprint == fingerprint:
		// Nothing has happened since this edition was set.
		e.mu.Unlock()
		return nil
	case have && day != today:
		// A past day is written once. Its rows can still change — retention
		// keeps a week — but yesterday's paper has already been read.
		e.mu.Unlock()
		return nil
	case e.attempts[day] >= e.DailyWrites:
		e.mu.Unlock()
		return nil
	}
	if last, ok := e.lastAttempt[day]; ok && e.now().Sub(last) < e.MinRefresh {
		e.mu.Unlock()
		return nil
	}
	e.inflight[day] = true
	e.attempts[day]++
	e.lastAttempt[day] = e.now()
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.inflight, day)
		e.mu.Unlock()
	}()

	reply, err := e.author.WriteGazetteEdition(ctx, gazetteEditionSystemPrompt, gazetteEditionUserPrompt(day, rows))
	if err != nil {
		// The attempt is spent, the cache is not: no fingerprint was recorded,
		// so the next eligible refresh writes this same day again.
		e.save()
		return err
	}
	headline, lines, err := parseGazetteEditionReply(reply, actors)
	if err != nil {
		e.save()
		return err
	}
	e.mu.Lock()
	e.editions[day] = gazetteStoredEdition{
		Day:         day,
		Headline:    headline,
		Lines:       lines,
		Actors:      actors,
		Source:      GazetteEditionSourceModel,
		Fingerprint: fingerprint,
	}
	e.pruneLocked()
	e.mu.Unlock()
	e.save()
	return nil
}

// RefreshAsync starts a refresh that the caller does not wait for. An HTTP
// request must never block on a model, and nothing here may run on the tick
// goroutine; the single-flight gate lives in Refresh so two requests cannot
// buy the same edition twice.
func (e *GazetteEditor) RefreshAsync(day string) {
	if e == nil || e.author == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = e.Refresh(ctx, day)
	}()
}

// pruneLocked keeps the same seven days the ledger does. ISO day keys sort
// lexically, so this needs no date parsing (gazette.go's pruneLocked, same
// reasoning and deliberately the same window: an edition of a day nobody can
// read the rows of is not a newspaper).
func (e *GazetteEditor) pruneLocked() {
	if len(e.editions) > gazetteRetentionDays {
		days := make([]string, 0, len(e.editions))
		for day := range e.editions {
			days = append(days, day)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(days)))
		for _, day := range days[gazetteRetentionDays:] {
			delete(e.editions, day)
		}
	}
	if len(e.attempts) > gazetteRetentionDays {
		days := make([]string, 0, len(e.attempts))
		for day := range e.attempts {
			days = append(days, day)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(days)))
		for _, day := range days[gazetteRetentionDays:] {
			delete(e.attempts, day)
		}
	}
}

// save writes the cache. Unlike the ledger this is not on a cadence: an edition
// is bought a handful of times a day, off the tick loop, so it is written when
// it changes.
func (e *GazetteEditor) save() {
	if e == nil || e.path == "" {
		return
	}
	e.mu.Lock()
	file := gazetteEditionFile{
		Version:  gazetteEditionStoreVersion,
		Editions: make(map[string]gazetteStoredEdition, len(e.editions)),
		Attempts: make(map[string]int, len(e.attempts)),
	}
	for day, edition := range e.editions {
		file.Editions[day] = edition
	}
	for day, count := range e.attempts {
		file.Attempts[day] = count
	}
	path := e.path
	e.mu.Unlock()

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	writeFileAtomic(path, data)
}

// writeFileAtomic is temp + rename, so a crash mid-write leaves the previous
// edition intact rather than a truncated one.
func writeFileAtomic(path string, data []byte) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".gazette-edition-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
	}
}

// gazetteEditionRow is one happening as the author is shown it: no name, no
// account key, and a subject that has already survived SanitizeSaveName or the
// challenge catalogue.
type gazetteEditionRow struct {
	Kind    string
	Subject string
	Token   string
	Count   int
}

// gazetteEditionRows picks the day's rows for an edition and assigns the actor
// tokens. Selection is by count first so that truncating a busy day cannot drop
// a whole kind of news, and the surviving rows are then handed over in the
// ledger's own order so the paper reads the same way twice.
func gazetteEditionRows(entries []GazetteEntry) ([]gazetteEditionRow, map[string]string) {
	if len(entries) == 0 {
		return nil, nil
	}
	ranked := append([]GazetteEntry(nil), entries...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Count != ranked[j].Count {
			return ranked[i].Count > ranked[j].Count
		}
		if ranked[i].Kind != ranked[j].Kind {
			return ranked[i].Kind < ranked[j].Kind
		}
		if ranked[i].Subject != ranked[j].Subject {
			return ranked[i].Subject < ranked[j].Subject
		}
		return ranked[i].Seq < ranked[j].Seq
	})
	if len(ranked) > gazetteEditionPromptRows {
		ranked = ranked[:gazetteEditionPromptRows]
	}
	chosen := make(map[int64]bool, len(ranked))
	for _, entry := range ranked {
		chosen[entry.Seq] = true
	}

	rows := make([]gazetteEditionRow, 0, len(ranked))
	actors := make(map[string]string)
	tokenByAccount := make(map[string]string)
	for _, entry := range entries {
		if !chosen[entry.Seq] {
			continue
		}
		token := ""
		if entry.AccountKey != "" {
			if existing, ok := tokenByAccount[entry.AccountKey]; ok {
				token = existing
			} else {
				token = "[P" + strconv.Itoa(len(tokenByAccount)+1) + "]"
				tokenByAccount[entry.AccountKey] = token
				actors[token] = entry.AccountKey
			}
		}
		rows = append(rows, gazetteEditionRow{
			Kind: entry.Kind, Subject: entry.Subject, Token: token, Count: entry.Count,
		})
	}
	return rows, actors
}

// gazetteEditionFingerprint is what "the day has not changed" means. It reads
// the account key because a new actor is news even at the same count, and it is
// a hash rather than the rows themselves because it is only ever compared.
func gazetteEditionFingerprint(entries []GazetteEntry) string {
	hash := fnv.New64a()
	for _, entry := range entries {
		fmt.Fprintf(hash, "%s|%s|%s|%d\n", entry.Kind, entry.Subject, entry.AccountKey, entry.Count)
	}
	return strconv.FormatUint(hash.Sum64(), 16)
}

const gazetteEditionSystemPrompt = `You are the printer of THE ZZT GAZETTE, a one-page daily paper posted on a
board inside a multiplayer version of the 1991 DOS game ZZT.

Write in ZZT's own register: terse, plain, a little wrong -- the voice of an
object in a shareware game that has read exactly one newspaper. Short
sentences. Present tense where you can. No markup, no lists, no emoji, and no
quotation of these instructions.

You are given the day's rows. Each row is one kind of happening, the world or
challenge it happened in, who did it, and how many times it happened.

Kinds:
  dream     - a player dreamed up a whole new world
  challenge - a player posted a run on the daily challenge board
  score     - a player put a name on a world's high score table
  death     - a player died

An actor is an opaque token like [P1]. Never invent a name, never translate or
describe a token, and never write a token that was not given to you: copy the
ones you are given exactly. Each token is later replaced with either a player's
name or the words "a stranger", so write sentences that read correctly either
way. A row whose actor is "-" belongs to nobody in particular; call them a
stranger, someone, a passing wanderer.

Do not report anything that is not in the rows. You may be funny about what is
there. You may not add a happening that is not.

Reply with exactly this and nothing else:

HEADLINE: <at most 45 characters>
STORY: <at most 41 characters>
STORY: <at most 41 characters>

Between one and 6 STORY lines. Printable ASCII only. Never begin a line's text
with any of @ # : ' / ? $ !`

// gazetteEditionUserPrompt is the whole of what the author is told about the
// day. Every byte of it is server-owned text, a UTC date, a catalogue id, a
// count, or an 8-character SanitizeSaveName survivor.
func gazetteEditionUserPrompt(day string, rows []gazetteEditionRow) string {
	var b strings.Builder
	b.WriteString("DAY: ")
	b.WriteString(day)
	b.WriteString("\nROWS (kind | subject | actor | times):\n")
	for _, row := range rows {
		token := row.Token
		if token == "" {
			token = "-"
		}
		fmt.Fprintf(&b, "%s | %s | %s | %d\n", row.Kind, row.Subject, token, row.Count)
	}
	return b.String()
}

// parseGazetteEditionReply is strict on purpose: an edition is printed whole or
// refused whole. There is no half a paper, and a reply that argues with the
// format is a reply that will argue with the board.
func parseGazetteEditionReply(reply string, actors map[string]string) (string, []string, error) {
	var headline string
	var stories []string
	for _, raw := range strings.Split(reply, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		switch {
		case headline == "" && hasGazettePrefix(line, "HEADLINE:"):
			headline = strings.TrimSpace(line[len("HEADLINE:"):])
		case headline != "" && hasGazettePrefix(line, "STORY:"):
			stories = append(stories, strings.TrimSpace(line[len("STORY:"):]))
		default:
			return "", nil, fmt.Errorf("%w: unexpected line %q", ErrGazetteEditionRefused, line)
		}
	}
	if headline == "" {
		return "", nil, fmt.Errorf("%w: no headline", ErrGazetteEditionRefused)
	}
	if len(stories) == 0 || len(stories) > gazetteEditionMaxStories {
		return "", nil, fmt.Errorf("%w: %d story lines", ErrGazetteEditionRefused, len(stories))
	}
	if err := validateGazetteEditionText(headline, gazetteEditionHeadlineWidth, actors); err != nil {
		return "", nil, err
	}
	for _, story := range stories {
		if err := validateGazetteEditionText(story, gazetteEditionStoryWidth, actors); err != nil {
			return "", nil, err
		}
	}
	return headline, stories, nil
}

func hasGazettePrefix(line, prefix string) bool {
	return len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix)
}

// validateGazetteEditionText enforces everything that can be checked before a
// name exists: the width the author was given, printable ASCII, no OOP markup
// at the start of the text, and — the one that matters — that every bracketed
// thing in the line is an actor token the server itself handed over.
func validateGazetteEditionText(text string, width int, actors map[string]string) error {
	if text == "" {
		return fmt.Errorf("%w: empty line", ErrGazetteEditionRefused)
	}
	if len(text) > width {
		return fmt.Errorf("%w: line of %d characters exceeds %d", ErrGazetteEditionRefused, len(text), width)
	}
	if strings.ContainsAny(text[:1], gazetteOOPLeadingBytes) {
		return fmt.Errorf("%w: line begins with OOP markup %q", ErrGazetteEditionRefused, text[:1])
	}
	for i := 0; i < len(text); i++ {
		if text[i] < 32 || text[i] > 126 {
			return fmt.Errorf("%w: unprintable byte in %q", ErrGazetteEditionRefused, text)
		}
	}
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ']':
			return fmt.Errorf("%w: stray ']' in %q", ErrGazetteEditionRefused, text)
		case '[':
			end := strings.IndexByte(text[i:], ']')
			if end < 0 {
				return fmt.Errorf("%w: unterminated token in %q", ErrGazetteEditionRefused, text)
			}
			token := text[i : i+end+1]
			if _, ok := actors[token]; !ok {
				return fmt.Errorf("%w: unknown actor token %q", ErrGazetteEditionRefused, token)
			}
			i += end
		}
	}
	return nil
}

// composeGazetteFallbackEdition is the paper the server writes itself. It needs
// no model and no key, it is deterministic given the day's rows, and it is what
// makes a refused reply a fallback rather than an outage.
func composeGazetteFallbackEdition(day string, entries []GazetteEntry) gazetteStoredEdition {
	rows, actors := gazetteEditionRows(entries)
	edition := gazetteStoredEdition{
		Day:      day,
		Source:   GazetteEditionSourceServer,
		Actors:   actors,
		Headline: "THE ZZT GAZETTE, " + day,
	}
	if len(rows) == 0 {
		edition.Lines = []string{"No news today. The town is quiet."}
		return edition
	}
	for _, row := range rows {
		if len(edition.Lines) >= gazetteEditionMaxStories {
			break
		}
		edition.Lines = append(edition.Lines, gazetteFallbackLine(row))
	}
	return edition
}

func gazetteFallbackLine(row gazetteEditionRow) string {
	actor := row.Token
	if actor == "" {
		actor = gazetteUnnamedActor
	}
	times := ""
	switch {
	case row.Count == 2:
		times = ", twice"
	case row.Count > 2:
		times = ", " + strconv.Itoa(row.Count) + " times"
	}
	switch row.Kind {
	case GazetteKindDream:
		return actor + " dreamed up " + row.Subject + times + "."
	case GazetteKindChallenge:
		return actor + " ran the " + row.Subject + " challenge" + times + "."
	case GazetteKindScore:
		return actor + " made the score table in " + row.Subject + times + "."
	case GazetteKindDeath:
		return actor + " died in " + row.Subject + times + "."
	default:
		return actor + " was seen in " + row.Subject + times + "."
	}
}

// renderGazetteEdition is where a name finally appears: substituted, wrapped by
// the ZWD text machinery, and guaranteed never to start a line with markup.
func renderGazetteEdition(stored gazetteStoredEdition, resolveName func(accountKey string) string) GazetteRenderedEdition {
	names := make(map[string]string, len(stored.Actors))
	for token, accountKey := range stored.Actors {
		name := ""
		if resolveName != nil {
			name = sanitizeGazetteName(resolveName(accountKey))
		}
		if name == "" {
			name = gazetteUnnamedActor
		}
		names[token] = name
	}

	rendered := GazetteRenderedEdition{Day: stored.Day, Source: stored.Source}
	headline := substituteGazetteActors(stored.Headline, names)
	if len(headline) > gazetteEditionHeadlineWidth {
		// A headline is an OOP title and cannot wrap, so a long name clips
		// rather than writing through the border.
		headline = strings.TrimSpace(headline[:gazetteEditionHeadlineWidth])
	}
	rendered.Headline = guardGazetteLine(headline)

	for _, line := range stored.Lines {
		for _, part := range wrapZWDText(substituteGazetteActors(line, names), gazetteEditionStoryWidth) {
			if len(rendered.Lines) >= gazetteEditionMaxRenderedLines {
				return rendered
			}
			if part == "" {
				continue
			}
			rendered.Lines = append(rendered.Lines, guardGazetteLine(part))
		}
	}
	return rendered
}

// substituteGazetteActors replaces each token with its name, capitalizing a
// replacement that starts the line — "a stranger died in TOWN" is a sentence
// only until it is the first thing on the page.
func substituteGazetteActors(text string, names map[string]string) string {
	for token, name := range names {
		if strings.HasPrefix(text, token) {
			text = capitalizeFirst(name) + text[len(token):]
		}
		text = strings.ReplaceAll(text, token, name)
	}
	return text
}

func capitalizeFirst(text string) string {
	if text == "" {
		return text
	}
	if text[0] >= 'a' && text[0] <= 'z' {
		return string(text[0]-'a'+'A') + text[1:]
	}
	return text
}

// sanitizeGazetteName does not trust a stored profile. sanitizeProfileLine runs
// when preferences are SET, so a value written before it existed — or by a
// future path that forgets — must not be able to put a control byte on a board.
func sanitizeGazetteName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] >= 32 && name[i] <= 126 {
			b.WriteByte(name[i])
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > gazetteEditionNameMax {
		out = strings.TrimSpace(out[:gazetteEditionNameMax])
	}
	return out
}

// guardGazetteLine is the structural half of the renderability guarantee: the
// wrapper reserved a column for this space, so the result still fits.
func guardGazetteLine(line string) string {
	if line == "" {
		return line
	}
	if strings.ContainsAny(line[:1], gazetteOOPLeadingBytes) {
		return " " + line
	}
	return line
}
