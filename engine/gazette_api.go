package zztgo

// The Gazette's read side (M34.1).
//
// This is where the consent rule from TASKS.md's M34 preamble is actually
// applied: the ledger stores an account key and no name, and an edition is
// named here, at read time, from the deliberate public subset of a profile
// (M24.1). Two things follow that are worth saying out loud, because they are
// the reason the rule lives here and not at the point of recording. The tick
// goroutine never reads the preferences store to write down a death. And a
// player who sets a display name this afternoon is named in this morning's
// deeds — which is the right answer, because the name is the consent, and
// nothing was published before they gave it.

import (
	"net/http"
	"strings"
)

// gazetteDayParamLen is the length of an ISO day. The parameter is compared
// against the days the ledger actually retains rather than parsed, so a
// malformed value renders an empty edition instead of an error.
const gazetteDayParamLen = len("2006-01-02")

func (a *WebAPI) handleGazette(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if a == nil || a.Server == nil || a.Server.Gazette == nil {
		http.Error(w, "the gazette is unavailable", http.StatusServiceUnavailable)
		return
	}
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day != "" && len(day) != gazetteDayParamLen {
		http.Error(w, "invalid day", http.StatusBadRequest)
		return
	}
	edition := a.Server.Gazette.Edition(day, a.gazetteNameResolver())
	writeJSON(w, struct {
		Day   string        `json:"day"`
		Days  []string      `json:"days"`
		Items []GazetteItem `json:"items"`
	}{Day: edition.Day, Days: a.Server.Gazette.Days(), Items: edition.Items})
}

// handleGazetteEdition serves the day written up (M34.2). It answers from the
// cache — the author's edition if one has been bought, the server's own if not
// — and then kicks a single-flight background refresh. A request never waits on
// a model, and the refresh is bounded by the editor's own spend rules rather
// than by how often somebody reloads the page.
func (a *WebAPI) handleGazetteEdition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if a == nil || a.Server == nil || a.Server.Gazette == nil {
		http.Error(w, "the gazette is unavailable", http.StatusServiceUnavailable)
		return
	}
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day != "" && len(day) != gazetteDayParamLen {
		http.Error(w, "invalid day", http.StatusBadRequest)
		return
	}
	editor := a.gazetteEditor()
	edition := editor.Edition(day, a.gazetteNameResolver())
	editor.RefreshAsync(edition.Day)
	writeJSON(w, edition)
}

// gazetteEditor answers with the process's single edition writer. The lazy
// construction itself moved to the server in M34.3 — the lobby's newsstand
// reaches it from the tick loop, which cannot see a WebAPI, and two editors
// would be two caches racing one file and two spend budgets. What stays here
// is this route's preference: the generator cmd/ already configured is offered
// as the author rather than making the server discover its own.
//
// The explicit GazetteEditor field still wins, because a test that sets one
// means it.
func (a *WebAPI) gazetteEditor() *GazetteEditor {
	a.gazetteMu.Lock()
	defer a.gazetteMu.Unlock()
	if a.GazetteEditor != nil {
		return a.GazetteEditor
	}
	// A nil *GenerationService in a GazetteAuthor interface is not a nil
	// interface, and an editor that thinks it has an author will spend a
	// refresh discovering otherwise on every request.
	var writer GazetteAuthor
	if a.Generator != nil {
		writer = a.Generator
	}
	a.GazetteEditor = a.Server.GazetteEditions(writer)
	return a.GazetteEditor
}

// gazetteNameResolver reads each account's consented name once per key it is
// asked about. The cache is per-request and deliberately local: one day's
// edition names the same account in several rows, and this is a read of the
// preferences store, not a hot loop worth a shared cache to keep coherent.
func (a *WebAPI) gazetteNameResolver() func(string) string {
	seen := make(map[string]string)
	return func(accountKey string) string {
		if accountKey == "" {
			return ""
		}
		if name, ok := seen[accountKey]; ok {
			return name
		}
		prefs, found := a.storedPreferences(accountKey)
		name := GazetteConsentedName(prefs, found)
		seen[accountKey] = name
		return name
	}
}
