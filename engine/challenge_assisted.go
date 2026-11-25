package zztgo

// M38.4: submitting a TAS script. POST /api/challenge/<id>/assisted takes an
// M38.1 script as its body. The gates run in this order:
//
//  1. who: anyone may submit (M38.0.2). A signed-in account's pass is a durable
//     assisted row; a guest's is a judged result and a replay link, no row.
//  2. the byte ceiling, before the body is read whole.
//  3. a per-submitter rate: the account, or for a guest the client address.
//  4. the concurrent-judge semaphore (M38.0.3: two at once). A full one is a
//     503 to try again, never a queue that grows without bound.
//  5. admission (M38.1), then the judge (M38.2), off the request goroutine.
//
// A pass writes the server-authored recording, then the M38.3 row, and answers
// with the rank and the replay id. A goal not met is a 422 naming the tick the
// judge stopped at.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// tasJudgeLimit is M38.0's concurrent-judge budget.
	tasJudgeLimit = 2
	// tasSubmitWindow and tasSubmitsPerWindow pace one submitter.
	tasSubmitWindow     = time.Minute
	tasSubmitsPerWindow = 6
	// tasJudgeTimeout bounds one judgment. A full-ceiling script on Gem Dash
	// takes about 80 ms (M38.2), so this is generous.
	tasJudgeTimeout = 30 * time.Second
)

// tasSubmitGate holds the rate and the semaphore. Its clock is the server's
// non-simulation clock.
type tasSubmitGate struct {
	mu     sync.Mutex
	recent map[string][]time.Time
	slots  chan struct{}
	// onJudge, when set, runs inside a judge slot: the load test uses it to
	// count how many judgments are running at once.
	onJudge func()
}

func (s *WebSocketServer) tasGate() *tasSubmitGate {
	s.tasGateOnce.Do(func() {
		s.tasSubmit = &tasSubmitGate{recent: make(map[string][]time.Time), slots: make(chan struct{}, tasJudgeLimit)}
	})
	return s.tasSubmit
}

// allow records one submission by key at now, or refuses it.
func (g *tasSubmitGate) allow(key string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	kept := g.recent[key][:0]
	for _, at := range g.recent[key] {
		if now.Sub(at) < tasSubmitWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) >= tasSubmitsPerWindow {
		g.recent[key] = kept
		return false
	}
	g.recent[key] = append(kept, now)
	return true
}

// AssistedSubmitResponse is the route's answer.
type AssistedSubmitResponse struct {
	Met         bool   `json:"met"`
	Ticks       int    `json:"ticks,omitempty"`
	Score       int    `json:"score,omitempty"`
	Gems        int    `json:"gems,omitempty"`
	Durable     bool   `json:"durable"`
	Rank        int    `json:"rank,omitempty"`
	RecordingID string `json:"recordingId,omitempty"`
	Replay      string `json:"replay,omitempty"`
	StoppedAt   int    `json:"stoppedAt,omitempty"`
	Refusal     string `json:"refusal,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func (a *WebAPI) handleAssistedSubmit(w http.ResponseWriter, r *http.Request, challengeID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	if a.Server == nil {
		http.Error(w, "challenges are unavailable", http.StatusServiceUnavailable)
		return
	}
	def, ok := ChallengeByID(challengeID)
	if !ok {
		http.Error(w, "no such challenge", http.StatusNotFound)
		return
	}
	s := a.Server

	// 1. Who.
	account, signedIn := s.authAccount(r)
	key := "guest:" + generationClientKey(r)
	name := "Guest"
	if signedIn {
		key = "account:" + account.ID
		name = s.accountPublicName(account)
	}

	// 2. The byte ceiling, before the body is read whole.
	body, err := io.ReadAll(io.LimitReader(r.Body, TASByteCeiling+1))
	if err != nil {
		http.Error(w, "could not read the script", http.StatusBadRequest)
		return
	}
	if len(body) > TASByteCeiling {
		writeAssistedRefusal(w, http.StatusRequestEntityTooLarge, TASRefuseTooLarge, fmt.Sprintf("the script is over the %d-byte ceiling", TASByteCeiling))
		return
	}

	// 3. The rate.
	gate := s.tasGate()
	if !gate.allow(key, s.clockNow()) {
		writeAssistedRefusal(w, http.StatusTooManyRequests, "rate", fmt.Sprintf("at most %d submissions a minute", tasSubmitsPerWindow))
		return
	}

	// 4. A judge slot, or try again.
	select {
	case gate.slots <- struct{}{}:
	default:
		writeAssistedRefusal(w, http.StatusServiceUnavailable, "busy", "the judges are busy; try again in a moment")
		return
	}
	defer func() { <-gate.slots }()

	// 5. Admission, then the judge, off the request goroutine.
	script, err := AdmitTASScript(body, def)
	if err != nil {
		var refusal *TASRefusal
		if errors.As(err, &refusal) {
			writeAssistedRefusal(w, http.StatusBadRequest, refusal.Class, refusal.Detail)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	worldName, world, err := s.ResolveChallengeWorld(def)
	if err != nil {
		http.Error(w, "the challenge's world is unavailable", http.StatusServiceUnavailable)
		return
	}
	recordingID, path, file, err := s.createAssistedRecording(def)
	if err != nil {
		http.Error(w, "there is nowhere to record the run", http.StatusServiceUnavailable)
		return
	}

	type outcome struct {
		judgment TASJudgment
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		if gate.onJudge != nil {
			gate.onJudge()
		}
		j, err := JudgeTASScript(def, worldName, world, script, name, file)
		done <- outcome{j, err}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), tasJudgeTimeout)
	defer cancel()
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		// The judge keeps its slot until it ends: the deferred release above
		// waits for the request, and the request waits for the judge.
		result = <-done
		_ = os.Remove(path)
		http.Error(w, "the judge ran out of time", http.StatusServiceUnavailable)
		return
	}
	_ = file.Close()
	if result.err != nil {
		_ = os.Remove(path)
		http.Error(w, "the run could not be judged", http.StatusInternalServerError)
		return
	}
	if !result.judgment.Met {
		_ = os.Remove(path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(w, AssistedSubmitResponse{
			StoppedAt: result.judgment.StoppedAt,
			Refusal:   "goal-not-met",
			Reason:    fmt.Sprintf("the goal was not met by tick %d, where the script ends", result.judgment.StoppedAt),
		})
		return
	}

	row := result.judgment.Result
	row.Name = CP437String(admitPlayerName(name))
	row.RecordingID = recordingID
	row.Division = ChallengeDivisionAssisted
	resp := AssistedSubmitResponse{
		Met: true, Ticks: row.Ticks, Score: row.Score, Gems: row.Gems,
		RecordingID: recordingID, Replay: "/replay/" + recordingID,
	}
	if signedIn {
		row.AccountKey = account.ID
		rank, err := s.ChallengeStore.Submit(row)
		if err == nil {
			resp.Durable, resp.Rank = true, rank
		}
	}
	writeJSON(w, resp)
}

// createAssistedRecording opens the file the judge will write, under a fresh
// id in the challenge recording namespace. It never opens an existing file.
func (s *WebSocketServer) createAssistedRecording(def ChallengeDefinition) (string, string, *os.File, error) {
	s.mu.Lock()
	if s.RecordDir == "" && s.ReplayDir == "" {
		s.mu.Unlock()
		return "", "", nil, ErrChallengeRecordingDisabled
	}
	s.challengeSeq++
	seq := s.challengeSeq
	stamp := s.recordStamp
	s.mu.Unlock()
	id := challengeRecordingID(def.ID+"-assisted", seq, stamp)
	path, err := s.challengeRecordingPath(id)
	if err != nil {
		return "", "", nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", "", nil, err
	}
	return id, path, file, nil
}

func writeAssistedRefusal(w http.ResponseWriter, status int, class, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, AssistedSubmitResponse{Refusal: class, Reason: detail})
}

// isAssistedPath reports whether an /api/challenge sub-path is <id>/assisted.
func isAssistedPath(rest string) (string, bool) {
	id := strings.TrimSuffix(rest, "/assisted")
	return id, id != rest && id != "" && !strings.Contains(id, "/")
}
