package zztgo

// The challenge leaderboard's durable store (M32.1).
//
// What it holds is deliberately narrow: one best run per account per challenge,
// bounded, with the account key kept server-side and only a display summary ever
// marshaled outward. There are no IPs, no timestamps of when somebody played,
// and no per-input log — the run's inputs already exist exactly once, in the
// recording the row cites, which is what makes the row verifiable at all.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

// challengeLeaderboardMax bounds one challenge's stored rows. Rows are already
// one-per-account, so this is the second bound: a challenge cannot grow without
// limit however many accounts play it.
const challengeLeaderboardMax = 25

// challengeStoreVersion is the on-disk envelope version. A file from a future
// version is refused rather than half-read.
const challengeStoreVersion = 1

var (
	// ErrChallengeResultNotDurable is a result that may not be stored: a guest
	// run, or a run whose challenge/version no longer matches the catalogue.
	ErrChallengeResultNotDurable = errors.New("zztgo: challenge result is not durable")
	// ErrChallengeResultStale is a result recorded against an older definition
	// of the challenge than the one this server is running.
	ErrChallengeResultStale = errors.New("zztgo: challenge result is stale")
)

// ChallengeResult is one stored run. AccountKey never leaves this process: the
// public projection is ChallengeLeaderboardRow, which has no field for it.
type ChallengeResult struct {
	ChallengeID string `json:"challengeId"`
	Version     int    `json:"version"`
	AccountKey  string `json:"accountKey"`
	Name        string `json:"name"`
	Ticks       int    `json:"ticks"`
	Score       int    `json:"score"`
	Gems        int    `json:"gems"`
	RecordingID string `json:"recordingId,omitempty"`
	// Evidence is the run's final room-state hash at completion, hex encoded.
	// It is what a verification pass compares a replay of RecordingID against:
	// replaying the stimuli must reproduce this exact hash or the row is not
	// what it claims to be.
	Evidence string `json:"evidence,omitempty"`
	// Seq is the opaque server order — the last tie-break, and the only one.
	// Account ids must not decide a tie, because a tie-break that reads them is
	// a tie-break that leaks them.
	Seq int64 `json:"seq"`
}

// ChallengeLeaderboardRow is the public projection. Every field here is safe to
// hand a browser; there is no account key, and `You` is computed per viewer
// rather than stored.
type ChallengeLeaderboardRow struct {
	Rank        int    `json:"rank"`
	Name        string `json:"name"`
	Ticks       int    `json:"ticks"`
	Score       int    `json:"score"`
	RecordingID string `json:"recordingId,omitempty"`
	You         bool   `json:"you,omitempty"`
}

type challengeStoreFile struct {
	Version int                          `json:"version"`
	Seq     int64                        `json:"seq"`
	Results map[string][]ChallengeResult `json:"results"`
}

// ChallengeStore is the leaderboard. A store with an empty path is memory-only,
// which is what tests and a server without a saves directory get; the write path
// is otherwise atomic (temp file + rename), so a crash mid-write leaves the
// previous leaderboard intact rather than a truncated one.
type ChallengeStore struct {
	mu      sync.Mutex
	path    string
	seq     int64
	results map[string][]ChallengeResult
}

func NewChallengeStore(path string) (*ChallengeStore, error) {
	store := &ChallengeStore{path: path, results: make(map[string][]ChallengeResult)}
	if path == "" {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	var file challengeStoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("challenge store %s: %w", path, err)
	}
	if file.Version > challengeStoreVersion {
		return nil, fmt.Errorf("challenge store %s: unsupported version %d", path, file.Version)
	}
	store.seq = file.Seq
	for id, rows := range file.Results {
		def, ok := ChallengeByID(id)
		if !ok {
			// A challenge that has left the catalogue takes its rows with it:
			// they cannot be shown, replayed against a definition, or compared.
			continue
		}
		kept := make([]ChallengeResult, 0, len(rows))
		for _, row := range rows {
			if row.Version != def.Version || row.AccountKey == "" || row.Ticks <= 0 {
				continue
			}
			if row.Seq > store.seq {
				store.seq = row.Seq
			}
			kept = append(kept, row)
		}
		if len(kept) == 0 {
			continue
		}
		sortChallengeResults(kept)
		if len(kept) > challengeLeaderboardMax {
			kept = kept[:challengeLeaderboardMax]
		}
		store.results[def.ID] = kept
	}
	return store, nil
}

// sortChallengeResults is the documented ranking: fewest ticks first, then the
// higher score, then the opaque server order. Every comparison is total and
// deterministic, so two servers holding the same rows render the same table.
func sortChallengeResults(rows []ChallengeResult) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Ticks != rows[j].Ticks {
			return rows[i].Ticks < rows[j].Ticks
		}
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].Seq < rows[j].Seq
	})
}

// Submit stores a completed run and returns its rank, or an error explaining why
// it was not durable. A guest run never reaches here (the caller keeps it local),
// and a run recorded against a stale definition is refused rather than mixed in
// with runs of the current one.
func (s *ChallengeStore) Submit(result ChallengeResult) (int, error) {
	if s == nil {
		return 0, ErrChallengeResultNotDurable
	}
	if result.AccountKey == "" || result.Ticks <= 0 {
		return 0, ErrChallengeResultNotDurable
	}
	def, ok := ChallengeByID(result.ChallengeID)
	if !ok {
		return 0, ErrUnknownChallenge
	}
	if result.Version != def.Version {
		return 0, ErrChallengeResultStale
	}
	result.ChallengeID = def.ID

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.results == nil {
		s.results = make(map[string][]ChallengeResult)
	}
	s.seq++
	result.Seq = s.seq

	rows := s.results[def.ID]
	replaced := false
	for i, row := range rows {
		if row.AccountKey != result.AccountKey {
			continue
		}
		// One row per account: a later slower attempt does not push the
		// player's own best off the board, and a faster one replaces it.
		if challengeResultBetter(result, row) {
			rows[i] = result
		}
		replaced = true
		break
	}
	if !replaced {
		rows = append(rows, result)
	}
	sortChallengeResults(rows)
	if len(rows) > challengeLeaderboardMax {
		rows = rows[:challengeLeaderboardMax]
	}
	s.results[def.ID] = rows

	rank := 0
	for i, row := range rows {
		if row.AccountKey == result.AccountKey {
			rank = i + 1
			break
		}
	}
	if err := s.writeLocked(); err != nil {
		return rank, err
	}
	return rank, nil
}

func challengeResultBetter(candidate, existing ChallengeResult) bool {
	if candidate.Ticks != existing.Ticks {
		return candidate.Ticks < existing.Ticks
	}
	return candidate.Score > existing.Score
}

// Leaderboard is the public table for one challenge. viewerAccount marks the
// viewer's own row and is never echoed back.
func (s *ChallengeStore) Leaderboard(challengeID, viewerAccount string) []ChallengeLeaderboardRow {
	if s == nil {
		return nil
	}
	def, ok := ChallengeByID(challengeID)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.results[def.ID]
	out := make([]ChallengeLeaderboardRow, 0, len(rows))
	for i, row := range rows {
		out = append(out, ChallengeLeaderboardRow{
			Rank:        i + 1,
			Name:        row.Name,
			Ticks:       row.Ticks,
			Score:       row.Score,
			RecordingID: row.RecordingID,
			You:         viewerAccount != "" && row.AccountKey == viewerAccount,
		})
	}
	return out
}

// ResultForRecording returns the stored row a recording id belongs to. It is how
// a ghost request proves the recording it was handed is a leaderboard run of the
// challenge it claims, rather than any recording on the server.
func (s *ChallengeStore) ResultForRecording(challengeID, recordingID string) (ChallengeResult, bool) {
	if s == nil || recordingID == "" {
		return ChallengeResult{}, false
	}
	def, ok := ChallengeByID(challengeID)
	if !ok {
		return ChallengeResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.results[def.ID] {
		if row.RecordingID == recordingID {
			return row, true
		}
	}
	return ChallengeResult{}, false
}

func (s *ChallengeStore) writeLocked() error {
	if s.path == "" {
		return nil
	}
	file := challengeStoreFile{Version: challengeStoreVersion, Seq: s.seq, Results: s.results}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
