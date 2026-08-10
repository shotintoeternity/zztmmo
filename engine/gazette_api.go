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
