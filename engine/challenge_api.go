package zztgo

// The challenge HTTP surface (M32.1): what a browser needs to show the landing,
// the leaderboard, and a ghost — and nothing that could decide a result.
//
// Every write to the leaderboard happens on the tick goroutine, from an observed
// completion (challenge_run.go). There is deliberately no POST here: an endpoint
// that accepted a time would be the hole the whole "only server-created runs
// count" boundary exists to close.

import (
	"net/http"
	"strings"
	"time"
)

// ChallengeSummary is one catalogue row as a browser sees it. It carries no
// world file, no OOP, and no path — just the identity, the human copy, and the
// routes the client already knows how to open.
type ChallengeSummary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Summary     []string `json:"summary,omitempty"`
	World       string   `json:"world"`
	Version     int      `json:"version"`
	GoalLine    string   `json:"goalLine"`
	Path        string   `json:"path"`
	Today       bool     `json:"today,omitempty"`
	Date        string   `json:"date,omitempty"`
	Available   bool     `json:"available"`
	Unavailable string   `json:"unavailable,omitempty"`
}

// ChallengeResponse is the landing payload: which challenge, its leaderboard,
// and whether this viewer may post a time.
type ChallengeResponse struct {
	Challenge   ChallengeSummary          `json:"challenge"`
	Leaderboard []ChallengeLeaderboardRow `json:"leaderboard"`
	CanSubmit   bool                      `json:"canSubmit"`
	Catalogue   []ChallengeSummary        `json:"catalogue,omitempty"`
}

// challengeNow is the injected non-simulation clock (M16.16a). It picks WHICH
// challenge is today's and stamps the landing; it never reaches a result.
func (a *WebAPI) challengeNow() time.Time {
	if a.Server != nil {
		return a.Server.clockNow()
	}
	return time.Now()
}

func (a *WebAPI) handleChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/challenge"), "/")
	switch {
	case rest == "":
		a.writeChallengeLanding(w, r, "")
	case rest == "ghost":
		a.handleChallengeGhost(w, r)
	case strings.Contains(rest, "/"):
		http.NotFound(w, r)
	default:
		a.writeChallengeLanding(w, r, rest)
	}
}

func (a *WebAPI) writeChallengeLanding(w http.ResponseWriter, r *http.Request, id string) {
	now := a.challengeNow()
	todays, hasToday := ChallengeForDate(now)

	var def ChallengeDefinition
	if id == "" {
		if !hasToday {
			http.Error(w, "no challenge is scheduled", http.StatusNotFound)
			return
		}
		def = todays
	} else {
		found, ok := ChallengeByID(id)
		if !ok {
			http.Error(w, "no such challenge", http.StatusNotFound)
			return
		}
		def = found
	}

	account := a.requestAccount(r)
	summary := a.challengeSummary(def, now, hasToday && todays.ID == def.ID)
	resp := ChallengeResponse{
		Challenge: summary,
		CanSubmit: account.ID != "",
	}
	if a.Server != nil {
		resp.Leaderboard = a.Server.ChallengeStore.Leaderboard(def.ID, account.ID)
	}
	for _, entry := range ChallengeCatalogue() {
		resp.Catalogue = append(resp.Catalogue, a.challengeSummary(entry, now, hasToday && todays.ID == entry.ID))
	}
	writeJSON(w, resp)
}

func (a *WebAPI) challengeSummary(def ChallengeDefinition, now time.Time, today bool) ChallengeSummary {
	summary := ChallengeSummary{
		ID:        def.ID,
		Title:     def.Title,
		Summary:   def.Summary,
		World:     def.World,
		Version:   def.Version,
		GoalLine:  ChallengeGoalLine(def.Goal),
		Path:      "/challenge/" + def.ID,
		Today:     today,
		Date:      ChallengeDateStamp(now),
		Available: true,
	}
	if a.Server == nil {
		summary.Available = false
		summary.Unavailable = "Challenges are unavailable."
		return summary
	}
	if _, _, err := a.Server.ResolveChallengeWorld(def); err != nil {
		summary.Available = false
		summary.Unavailable = challengeRefusalText(err)
		return summary
	}
	if a.Server.RecordDir == "" {
		summary.Available = false
		summary.Unavailable = challengeRefusalText(ErrChallengeRecordingDisabled)
	}
	return summary
}

// handleChallengeGhost serves the local overlay track for one leaderboard row.
// It refuses anything that is not a stored row of the named challenge, so it
// cannot be used to read positions out of arbitrary recordings.
func (a *WebAPI) handleChallengeGhost(w http.ResponseWriter, r *http.Request) {
	if a.Server == nil {
		http.Error(w, "challenges are unavailable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	challengeID := strings.TrimSpace(q.Get("challenge"))
	recordingID, err := sanitizeReplayID(q.Get("recording"))
	if err != nil {
		http.Error(w, "invalid recording id", http.StatusBadRequest)
		return
	}
	track, err := a.Server.ChallengeGhost(challengeID, recordingID)
	if err != nil {
		status := http.StatusNotFound
		if err == ErrChallengeResultStale {
			// Said apart from "not found" on purpose: a client holding a ghost
			// from an older definition should be told the ghost is out of date,
			// not that the run never happened.
			status = http.StatusGone
		}
		http.Error(w, challengeGhostRefusalText(err), status)
		return
	}
	writeJSON(w, track)
}

func challengeGhostRefusalText(err error) string {
	switch err {
	case ErrChallengeResultStale:
		return "that ghost is from an older version of this challenge"
	case ErrUnknownChallenge:
		return "no such challenge run"
	default:
		return "ghost unavailable"
	}
}
