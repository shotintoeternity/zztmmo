package zztgo

// Ghost tracks (M32.1).
//
// A ghost is presentation, and this file is where that boundary is enforced. A
// track is extracted by REPLAYING a finished recording on the server — the same
// deterministic playback /replay/<id> uses — and reduced to a list of positions.
// It never joins a room: no PlayerID is minted, no stat is spawned, nothing
// enters StateHash, nothing is recorded, and the live run a player races against
// it is not told it exists. The browser draws the positions over its own canvas
// and sends nothing back.

import (
	"os"
	"sort"
	"sync"
)

// challengeGhostTickCap bounds one extraction. A recording is a file on this
// server, but it is a file whose length nobody promised, and an unbounded scan
// on an HTTP path is a stall waiting to be asked for.
const challengeGhostTickCap = 3000

// challengeGhostCacheMax bounds the cache, which is keyed by recording id. Runs
// are short and few, so this is generous; it exists so a hot leaderboard row is
// not replayed once per viewer.
const challengeGhostCacheMax = 16

// ChallengeGhostPoint is one tick of the ghost's path.
type ChallengeGhostPoint struct {
	Tick  int   `json:"tick"`
	Board int16 `json:"board"`
	X     int16 `json:"x"`
	Y     int16 `json:"y"`
}

// ChallengeGhostTrack is what the browser is handed. It carries the challenge id
// and version it was extracted for, so a client holding a track from a previous
// definition can be refused rather than raced against a different game.
type ChallengeGhostTrack struct {
	ChallengeID string                `json:"challengeId"`
	Version     int                   `json:"version"`
	RecordingID string                `json:"recordingId"`
	Name        string                `json:"name,omitempty"`
	Ticks       int                   `json:"ticks"`
	Truncated   bool                  `json:"truncated,omitempty"`
	Points      []ChallengeGhostPoint `json:"points"`
}

type challengeGhostCache struct {
	mu     sync.Mutex
	tracks map[string]ChallengeGhostTrack
	order  []string
}

func (c *challengeGhostCache) get(id string) (ChallengeGhostTrack, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	track, ok := c.tracks[id]
	return track, ok
}

func (c *challengeGhostCache) put(id string, track ChallengeGhostTrack) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tracks == nil {
		c.tracks = make(map[string]ChallengeGhostTrack)
	}
	if _, exists := c.tracks[id]; !exists {
		c.order = append(c.order, id)
	}
	c.tracks[id] = track
	for len(c.order) > challengeGhostCacheMax {
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.tracks, evict)
	}
}

// ChallengeGhost returns the ghost track for a stored leaderboard row.
//
// The row is looked up in the store first, deliberately: a ghost request names a
// challenge and a recording id, and only a recording THIS leaderboard cites may
// be turned into a track. Otherwise the endpoint would be a way to read the
// positions out of any recording on the server, challenge or not.
func (s *WebSocketServer) ChallengeGhost(challengeID, recordingID string) (ChallengeGhostTrack, error) {
	def, ok := ChallengeByID(challengeID)
	if !ok {
		return ChallengeGhostTrack{}, ErrUnknownChallenge
	}
	if s.ChallengeStore == nil {
		return ChallengeGhostTrack{}, ErrUnknownChallenge
	}
	result, ok := s.ChallengeStore.ResultForRecording(def.ID, recordingID)
	if !ok {
		return ChallengeGhostTrack{}, ErrUnknownChallenge
	}
	if result.Version != def.Version {
		// A row from an older definition is not a ghost of this challenge. It is
		// refused rather than drawn, which is what keeps "race the record" from
		// meaning "race a run of a different course".
		return ChallengeGhostTrack{}, ErrChallengeResultStale
	}
	if track, ok := s.ghostCache.get(result.RecordingID); ok {
		return track, nil
	}
	track, err := s.extractChallengeGhost(def, result)
	if err != nil {
		return ChallengeGhostTrack{}, err
	}
	s.ghostCache.put(result.RecordingID, track)
	return track, nil
}

func (s *WebSocketServer) extractChallengeGhost(def ChallengeDefinition, result ChallengeResult) (ChallengeGhostTrack, error) {
	path, err := s.challengeRecordingPath(result.RecordingID)
	if err != nil {
		return ChallengeGhostTrack{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ChallengeGhostTrack{}, err
	}
	defer f.Close()

	playback, err := NewReplayPlayback(f)
	if err != nil {
		return ChallengeGhostTrack{}, err
	}
	track := ChallengeGhostTrack{
		ChallengeID: def.ID,
		Version:     def.Version,
		RecordingID: result.RecordingID,
		Name:        result.Name,
	}
	rm := playback.RoomManager()
	for scanned := 0; scanned < challengeGhostTickCap; scanned++ {
		tick, _, done, err := playback.Step()
		if err != nil {
			return ChallengeGhostTrack{}, err
		}
		if done {
			break
		}
		if point, ok := ghostPointAt(rm, tick); ok {
			track.Points = append(track.Points, point)
		}
		if len(track.Points) > 0 && tick+1 >= result.Ticks {
			// The stored result's tick count is where the run finished; past it
			// the ghost has nothing left to show.
			break
		}
		if scanned == challengeGhostTickCap-1 {
			track.Truncated = true
		}
	}
	track.Ticks = len(track.Points)
	return track, nil
}

// ghostPointAt reads the single recorded player's position out of a replayed
// room. It READS the manager and mutates nothing — the same discipline the
// recorder itself follows.
func ghostPointAt(rm *RoomManager, tick int) (ChallengeGhostPoint, bool) {
	if rm == nil {
		return ChallengeGhostPoint{}, false
	}
	ids := rm.playerIDs()
	if len(ids) == 0 {
		return ChallengeGhostPoint{}, false
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	playerID := ids[0]
	boardID, statID, ok := rm.PlayerLocation(playerID)
	if !ok {
		return ChallengeGhostPoint{}, false
	}
	room, ok := rm.Room(boardID)
	if !ok || room.Engine == nil {
		return ChallengeGhostPoint{}, false
	}
	if statID < 0 || statID > room.Engine.Board.StatCount {
		return ChallengeGhostPoint{}, false
	}
	stat := room.Engine.Board.Stats[statID]
	return ChallengeGhostPoint{Tick: tick, Board: boardID, X: int16(stat.X), Y: int16(stat.Y)}, true
}
