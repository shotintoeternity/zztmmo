package zztgo

// Challenge catalogue (M32.1): the server-owned definition of what a daily
// challenge IS.
//
// Three boundaries were decided in the task spec before any of this was written,
// and every rule here exists to hold one of them:
//
//  1. Only server-created runs count. A challenge is named by an id that
//     resolves in THIS table — never by a world file, a metadata sidecar, OOP
//     text, or a route parameter a browser chose. Nothing below reads anything a
//     player can write.
//  2. The challenge clock is service time, not sim time. A UTC date picks
//     WHICH challenge is today's (ChallengeForDate, at the web boundary), but a
//     run's measured result is a deterministic tick count and in-sim counters —
//     see ChallengeResult. No wall-clock latency reaches a leaderboard row.
//  3. Ghosts are presentation. Nothing in this file has any notion of a second
//     player; a ghost is a track extracted from a finished recording
//     (challenge_ghost.go) and drawn by the client.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ChallengeWorldName is the first-party course the shipped catalogue points at.
// It is a canonical world (worlds.manifest.json), so M18.11's guard keeps a
// dream or a publish from replacing the thing runs are measured against.
const ChallengeWorldName = "GEMDASH"

// ChallengeGoalKind is how a run is judged complete. Completion is evaluated by
// the server from the run's own simulation state, never reported by a client.
type ChallengeGoalKind string

const (
	// ChallengeGoalGems completes when the player is holding Amount gems.
	ChallengeGoalGems ChallengeGoalKind = "gems"
	// ChallengeGoalScore completes when the player's score reaches Amount.
	ChallengeGoalScore ChallengeGoalKind = "score"
	// ChallengeGoalBoard completes when the player is standing on Board.
	ChallengeGoalBoard ChallengeGoalKind = "board"
)

// ChallengeGoal is the completion rule. It is deliberately a small closed set of
// in-sim counters rather than an expression language: a rule a player could
// author is a rule a player could satisfy without playing.
type ChallengeGoal struct {
	Kind   ChallengeGoalKind `json:"kind"`
	Amount int16             `json:"amount,omitempty"`
	Board  int16             `json:"board,omitempty"`
}

// ChallengeDefinition is one catalogue row.
//
// Version is the staleness fence: bump it when anything that changes what a run
// MEANS changes (the world, the start board, the goal). A stored result or a
// ghost track carrying an older Version is refused rather than compared against
// runs of a different game — see challengeResultIsStale.
type ChallengeDefinition struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Summary []string      `json:"summary,omitempty"`
	World   string        `json:"world"`
	Board   int16         `json:"board"`
	Goal    ChallengeGoal `json:"goal"`
	Version int           `json:"version"`
}

// challengeCatalogue is the committed catalogue. The first cut ships one entry,
// which the spec allows; the shape is already date/id-addressable, so a calendar
// of seasons is more rows here and no change anywhere else.
var challengeCatalogue = []ChallengeDefinition{
	{
		ID:    "gem-dash",
		Title: "Gem Dash",
		Summary: []string{
			"Collect all three gems.",
			"Fewest ticks wins.",
		},
		World:   ChallengeWorldName,
		Board:   1,
		Goal:    ChallengeGoal{Kind: ChallengeGoalGems, Amount: 3},
		Version: 1,
	},
}

var (
	// ErrUnknownChallenge is what an id nobody catalogued gets. It is returned
	// for a bad id whether the id came from a URL, a socket, or a stored row.
	ErrUnknownChallenge = errors.New("zztgo: unknown challenge")
	// ErrChallengeWorldUnavailable is a catalogued challenge whose world is
	// missing, corrupt, or owned by a player. Reported honestly rather than
	// silently substituting another world.
	ErrChallengeWorldUnavailable = errors.New("zztgo: challenge world unavailable")
	// ErrChallengeRecordingDisabled is a server with nowhere to record. A run
	// that is not recorded cannot back a leaderboard row (the row cites a
	// recording id, and replay verification reads it), so the honest answer to
	// "start a challenge" on such a server is a refusal.
	ErrChallengeRecordingDisabled = errors.New("zztgo: challenge recording is not configured")
	// ErrChallengeBusy is the concurrency cap. Every start mints an instance and
	// a recording file, so an uncapped start is a disk-and-memory amplifier for
	// anyone who can open a socket (M16.19's boundary posture).
	ErrChallengeBusy = errors.New("zztgo: too many challenge runs in progress")
)

// ChallengeCatalogue returns the catalogue in its committed order.
func ChallengeCatalogue() []ChallengeDefinition {
	out := make([]ChallengeDefinition, len(challengeCatalogue))
	copy(out, challengeCatalogue)
	return out
}

// ChallengeByID resolves a challenge id. Ids are matched case-insensitively and
// trimmed, because they travel in URLs people type and paste; everything else
// about the row comes from this table.
func ChallengeByID(id string) (ChallengeDefinition, bool) {
	wanted := strings.ToLower(strings.TrimSpace(id))
	if wanted == "" {
		return ChallengeDefinition{}, false
	}
	for _, def := range challengeCatalogue {
		if def.ID == wanted {
			return def, true
		}
	}
	return ChallengeDefinition{}, false
}

// ChallengeForDate picks the challenge a bare /challenge shows.
//
// This is the ONE place service time touches the layer, and it touches only the
// choice of row: whole UTC days since the Unix epoch, modulo the catalogue. A
// date is a stable, shareable name for "today's challenge" — the same day gives
// the same row on every server and in every test — and it never reaches a
// result, where the measured quantity is ticks.
func ChallengeForDate(now time.Time) (ChallengeDefinition, bool) {
	if len(challengeCatalogue) == 0 {
		return ChallengeDefinition{}, false
	}
	day := now.UTC().Unix() / 86400
	idx := int(day % int64(len(challengeCatalogue)))
	if idx < 0 {
		idx += len(challengeCatalogue)
	}
	return challengeCatalogue[idx], true
}

// ChallengeDateStamp is the UTC day a landing page reports. Presentation only:
// it names which day's challenge is on show and is never compared against a run.
func ChallengeDateStamp(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

// challengeGoalMet is the completion rule, evaluated by the server from the
// run's own simulation state after a tick.
//
// Death and respawn are deliberately NOT completion or failure: a challenge run
// keeps counting ticks across a death, exactly as a speedrun timer does, so the
// only ways out are finishing the goal and abandoning the run.
func challengeGoalMet(goal ChallengeGoal, state PlayerState, boardID int16) bool {
	switch goal.Kind {
	case ChallengeGoalGems:
		return state.Gems >= goal.Amount
	case ChallengeGoalScore:
		return state.Score >= goal.Amount
	case ChallengeGoalBoard:
		return boardID == goal.Board
	default:
		// An uncatalogued goal kind completes never rather than always: an
		// unknown rule must not hand out leaderboard rows.
		return false
	}
}

// ChallengeGoalLine is the one-line human statement of a goal, for the CP437
// landing window and the browser's status text.
func ChallengeGoalLine(goal ChallengeGoal) string {
	switch goal.Kind {
	case ChallengeGoalGems:
		return fmt.Sprintf("Goal: collect %d gems", goal.Amount)
	case ChallengeGoalScore:
		return fmt.Sprintf("Goal: score %d", goal.Amount)
	case ChallengeGoalBoard:
		return fmt.Sprintf("Goal: reach board %d", goal.Board)
	default:
		return "Goal: unavailable"
	}
}

// challengeRunKey is the instance key a challenge run is hosted under.
//
// It is deliberately NOT a name SanitizeSaveName accepts. That is the whole
// isolation argument: `?world=` cannot reach it, no file on disk resolves to it,
// and every surface that walks hosted worlds by their directory names — the
// picker, occupancy, ZZT TV, friends-here — cannot see it without being taught
// to, rather than being kept out by remembering to exclude it. (TestM321... asserts
// the key is unsanitizable, because that property is what the rest rests on.)
func challengeRunKey(challengeID string, seq int64) string {
	return fmt.Sprintf("!challenge:%s:%d", challengeID, seq)
}

// isChallengeRunKey reports whether an instance key belongs to a challenge run.
// The few places that walk s.Instances and must NOT see a run — autosave and
// friends-here presence — ask this.
func isChallengeRunKey(name string) bool {
	return strings.HasPrefix(name, "!challenge:")
}

// challengeRecordingID is the recording file stem for a run. It must pass
// sanitizeReplayID (the /replay/<id> charset), which the instance key
// deliberately does not, so the two are minted separately.
func challengeRecordingID(challengeID string, seq int64, stamp string) string {
	id := fmt.Sprintf("chal-%s-%d", challengeID, seq)
	if stamp != "" {
		id += "-" + stamp
	}
	safe, err := sanitizeReplayID(id)
	if err != nil {
		// The id came from a catalogue row and a counter, so this is
		// unreachable; falling back to the counter alone keeps a bad catalogue
		// row from producing an unservable recording.
		return fmt.Sprintf("chal-%d", seq)
	}
	return safe
}
