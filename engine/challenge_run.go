package zztgo

// Challenge runs (M32.1): an isolated, always-recorded play instance whose only
// product is a server-observed result.
//
// A run is a WorldInstance like any other — it ticks, it has reconnect grace, it
// is evicted when it goes idle, it is closed on shutdown — but it is keyed by
// challengeRunKey, which SanitizeSaveName rejects. That single fact is what
// isolates it: `?world=` cannot name it, no file resolves to it, and the
// surfaces that walk hosted worlds BY DIRECTORY NAME (the picker and its
// occupancy, ZZT TV's busy rooms, favorites, title thumbnails) cannot see it at
// all. The three places that walk s.Instances and would otherwise pick a run up
// — autosave, friends-here presence, and the join path's play counter — exclude
// it explicitly, and M32.1's tests assert each of those exclusions.
//
// What a run deliberately does NOT do to the source world: no play count, no
// favorite, no presence, no autosave, no `.HI` high-score file, no account
// sidecar read or write. A challenge starts from the world's pristine bytes for
// everyone, which is the only way two runs are comparable.

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// challengeMaxConcurrentRuns caps live runs across the server. Each start mints
// an instance and a recording file, so an uncapped start is a disk-and-memory
// amplifier for anyone who can open a socket.
const challengeMaxConcurrentRuns = 24

// ChallengeRun is the server's record of one run in progress. Every field is
// written on the tick goroutine or under the instance lock; nothing here is ever
// read from a client message.
type ChallengeRun struct {
	Key         string
	Def         ChallengeDefinition
	RecordingID string
	// AccountID is empty for a guest. A guest plays the same run and sees the
	// same result; what they do not get is a durable public row.
	AccountID string
	Name      string

	// Ticks is the measured quantity: how many simulation ticks this run has
	// been stepped. It is incremented on the tick goroutine under inst.mu, the
	// same lock the step itself holds, so it cannot drift from the simulation.
	Ticks int
	// PlayerID is the one player of the run, set at the join.
	PlayerID PlayerID
	// Finished is set the moment the server observes the goal met. A finished
	// run keeps ticking (the player may walk around) but can never produce a
	// second result: the row is written once.
	Finished bool
	Result   ChallengeResult
}

// challengeCompletion is handed out of the locked tick section so the file
// writes, the leaderboard write and the client message all happen outside
// inst.mu — the same discipline the rest of Tick follows.
type challengeCompletion struct {
	run    *ChallengeRun
	result ChallengeResult
}

// ResolveChallengeWorld resolves a catalogue row's world through the SAME
// identity path /play uses — SanitizeSaveName, then LoadPristineWorld out of the
// hosting directory — and refuses honestly.
//
// A world that is missing or corrupt fails here rather than at the first tick,
// and a world some account OWNS (an .access.json written by the editor, M16.17b)
// is refused too: a challenge is measured against content the service ships, and
// a catalogue row pointing at a world a player can rewrite would make every
// stored result incomparable the moment they did.
func (s *WebSocketServer) ResolveChallengeWorld(def ChallengeDefinition) (string, TWorld, error) {
	safe, err := SanitizeSaveName(def.World)
	if err != nil {
		return "", TWorld{}, fmt.Errorf("%w: %q is not a hostable name", ErrChallengeWorldUnavailable, def.World)
	}
	dir := s.worldsDir()
	if access, ok, err := loadWorldAccess(dir, safe); err == nil && ok && access.OwnerAccountID != "" {
		return "", TWorld{}, fmt.Errorf("%w: %s is owned by a player", ErrChallengeWorldUnavailable, safe)
	}
	world, err := LoadPristineWorld(dir, safe)
	if err != nil {
		return "", TWorld{}, fmt.Errorf("%w: %s: %v", ErrChallengeWorldUnavailable, safe, err)
	}
	return safe, world, nil
}

// StartChallengeRun creates the isolated instance for one attempt.
//
// Recording is not optional here: the leaderboard row cites a recording id and
// verification replays it, so a server with nowhere to record refuses to start a
// challenge rather than hosting a run whose result nothing can check.
func (s *WebSocketServer) StartChallengeRun(def ChallengeDefinition, accountID, name string) (*WorldInstance, *ChallengeRun, error) {
	if _, ok := ChallengeByID(def.ID); !ok {
		return nil, nil, ErrUnknownChallenge
	}
	worldName, world, err := s.ResolveChallengeWorld(def)
	if err != nil {
		return nil, nil, err
	}

	s.mu.Lock()
	if s.RecordDir == "" {
		s.mu.Unlock()
		return nil, nil, ErrChallengeRecordingDisabled
	}
	live := 0
	for key := range s.Instances {
		if isChallengeRunKey(key) {
			live++
		}
	}
	if live >= challengeMaxConcurrentRuns {
		s.mu.Unlock()
		return nil, nil, ErrChallengeBusy
	}
	s.challengeSeq++
	seq := s.challengeSeq
	key := challengeRunKey(def.ID, seq)
	run := &ChallengeRun{
		Key:         key,
		Def:         def,
		RecordingID: challengeRecordingID(def.ID, seq, s.recordStamp),
		AccountID:   accountID,
		Name:        name,
	}

	rm := NewRoomManagerForWorld(world, worldName)
	// Deliberately NO HighScorePath: a challenge run must not write the source
	// world's .HI, which is exactly the source-world mutation the spec forbids.
	inst := &WorldInstance{
		Name:        key,
		SourceWorld: cloneWorld(world),
		RoomManager: rm,
		Clients:     make(map[PlayerID]*webSocketClient),
		Inputs:      make(map[PlayerID]PlayerInput),
		// No Title sim: a run is entered directly on its start board, and a
		// title animation nobody watches is a second engine per attempt.
		Detached:       make(map[PlayerID]int),
		ResumeTokens:   make(map[string]PlayerID),
		TokensByPlayer: make(map[PlayerID]string),
		Spectators:     make(map[*webSocketClient]*spectator),
		Challenge:      run,
		// The recording is the run's own file, but its HEADER names the source
		// world: /replay/<id> resolves that name with LoadPristineWorld, and the
		// instance key is not a name any directory answers to.
		RecordWorld: worldName,
		RecordID:    run.RecordingID,
	}
	s.Instances[key] = inst
	s.attachRecorderLocked(inst)
	recorded := inst.RoomManager.recorder != nil
	s.mu.Unlock()

	if !recorded {
		// Recording is forced on, so a recorder that failed to open takes the
		// run with it rather than leaving an unverifiable attempt running.
		s.discardChallengeRun(key)
		return nil, nil, ErrChallengeRecordingDisabled
	}
	return inst, run, nil
}

// discardChallengeRun removes a run's instance. It is used when a run could not
// be started and when its only client failed to receive its first frame.
func (s *WebSocketServer) discardChallengeRun(key string) {
	if !isChallengeRunKey(key) {
		return
	}
	s.mu.Lock()
	inst := s.Instances[key]
	delete(s.Instances, key)
	var recorder *SessionRecorder
	if inst != nil && inst.RoomManager != nil {
		recorder = inst.RoomManager.recorder
		inst.RoomManager.SetRecorder(nil)
	}
	s.mu.Unlock()
	if recorder != nil {
		recorder.Close()
	}
	if inst != nil {
		s.metrics.forgetInstance(key)
	}
}

// publicWorldName is the world name a frame tells its client it is in. For an
// ordinary instance that is its key; for a challenge run the key is not a world
// at all, so the run reports the world it is being played on — which is what the
// browser shows, and what a share link would name.
func (inst *WorldInstance) publicWorldName() string {
	if inst == nil {
		return ""
	}
	if inst.Challenge != nil {
		return inst.Challenge.Def.World
	}
	return inst.Name
}

// resolveChallengeJoin is the socket-side half of starting or reclaiming a run.
//
// A run key reclaims THIS browser's attempt: the key is minted by the server and
// only ever handed to the one connection that started the run, and reclaiming
// one is refused unless the account matches — a guest's run is reclaimable by
// the guest connection that holds the key, and a signed-in player's run is not
// reclaimable by anyone else at all.
func (s *WebSocketServer) resolveChallengeJoin(challengeID, runKey string, account AuthenticatedAccount, authenticated bool) (*WorldInstance, *ChallengeRun, error) {
	accountID := ""
	name := "player"
	if authenticated {
		accountID = account.ID
		name = account.DisplayName()
	}
	if runKey != "" {
		inst, ok := s.ChallengeRunInstance(runKey)
		if !ok {
			return nil, nil, fmt.Errorf("%w: that run has ended", ErrUnknownChallenge)
		}
		if inst.Challenge.AccountID != accountID {
			return nil, nil, fmt.Errorf("%w: that run belongs to another player", ErrUnknownChallenge)
		}
		return inst, inst.Challenge, nil
	}
	def, ok := ChallengeByID(challengeID)
	if !ok {
		return nil, nil, ErrUnknownChallenge
	}
	if authenticated {
		if handle, has := s.accountProfileSummary(accountID); has && handle != "" {
			name = handle
		}
	}
	return s.StartChallengeRun(def, accountID, name)
}

// challengeRefusalText turns a start failure into one line a CP437 window can
// show. Each is specific: "unavailable" that could mean four things is what
// sends a player to look for a bug in their own browser.
func challengeRefusalText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrChallengeRecordingDisabled):
		return "Challenges are off: no runs are recorded here."
	case errors.Is(err, ErrChallengeBusy):
		return "Too many challenge runs right now. Try again shortly."
	case errors.Is(err, ErrChallengeWorldUnavailable):
		return "That challenge's world is unavailable."
	case errors.Is(err, ErrUnknownChallenge):
		return "No such challenge: " + strings.TrimPrefix(err.Error(), "zztgo: unknown challenge: ")
	default:
		return "Challenge unavailable."
	}
}

// ChallengeRunInstance returns a live run's instance by its key.
func (s *WebSocketServer) ChallengeRunInstance(key string) (*WorldInstance, bool) {
	if !isChallengeRunKey(key) {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst := s.Instances[key]
	if inst == nil || inst.Challenge == nil {
		return nil, false
	}
	return inst, true
}

// advanceChallengeLocked counts this run's tick and asks whether the goal is
// met. Caller holds inst.mu, inside WorldInstance.Tick, so the count and the
// step it counts cannot interleave.
//
// The rules the spec asked to be explicit are all here:
//   - death and respawn keep the run going; ticks keep counting.
//   - a finished run never completes twice, however long the player lingers.
//   - a run whose player has left (abandoned, quit, or dropped past the
//     reconnect grace) completes never: there is nobody to have finished it.
func (inst *WorldInstance) advanceChallengeLocked() *challengeCompletion {
	run := inst.Challenge
	if run == nil {
		return nil
	}
	run.Ticks++
	if run.Finished || run.PlayerID == 0 {
		return nil
	}
	state, ok := inst.RoomManager.PlayerState(run.PlayerID)
	if !ok || state == nil {
		return nil
	}
	boardID, _, located := inst.RoomManager.PlayerLocation(run.PlayerID)
	if !located {
		return nil
	}
	if !challengeGoalMet(run.Def.Goal, *state, boardID) {
		return nil
	}
	run.Finished = true
	run.Result = ChallengeResult{
		ChallengeID: run.Def.ID,
		Version:     run.Def.Version,
		AccountKey:  run.AccountID,
		Name:        run.Name,
		Ticks:       run.Ticks,
		Score:       int(state.Score),
		Gems:        int(state.Gems),
		RecordingID: run.RecordingID,
		Evidence:    challengeEvidence(inst.RoomManager),
	}
	return &challengeCompletion{run: run, result: run.Result}
}

// challengeEvidence is the run's final state fingerprint: every live room's
// StateHash, in board order, hex encoded. Replaying the cited recording must
// reproduce it exactly — that is what makes a row checkable rather than merely
// stored.
func challengeEvidence(rm *RoomManager) string {
	if rm == nil {
		return ""
	}
	hashes := rm.RoomStateHashes()
	boards := make([]int16, 0, len(hashes))
	for boardID := range hashes {
		boards = append(boards, boardID)
	}
	for i := 1; i < len(boards); i++ {
		for j := i; j > 0 && boards[j] < boards[j-1]; j-- {
			boards[j], boards[j-1] = boards[j-1], boards[j]
		}
	}
	var b strings.Builder
	for _, boardID := range boards {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d:%016x", boardID, hashes[boardID])
	}
	return b.String()
}

// finishChallengeRun closes the recording, writes the leaderboard row when the
// player is signed in, and tells the run's own client. It runs outside inst.mu.
//
// The recorder is closed HERE rather than at eviction because the two things
// that make a result useful — replaying it and watching it — both need the file
// flushed, and the player is offered both the moment they finish.
func (s *WebSocketServer) finishChallengeRun(inst *WorldInstance, completion *challengeCompletion) ChallengeResultMessage {
	run := completion.run
	result := completion.result

	// The swap happens under inst.mu — the lock every join, submit and step of
	// this instance already takes — so a recorder cannot be detached out from
	// under a goroutine that is recording into it. Close runs after the unlock:
	// it drains a writer goroutine and flushes a file, and no tick waits on that.
	inst.mu.Lock()
	recorder := inst.RoomManager.recorder
	inst.RoomManager.SetRecorder(nil)
	inst.mu.Unlock()
	if recorder != nil {
		recorder.Close()
	}

	msg := ChallengeResultMessage{
		Type:        MessageTypeChallengeResult,
		ChallengeID: result.ChallengeID,
		Version:     result.Version,
		Ticks:       result.Ticks,
		Score:       result.Score,
		Gems:        result.Gems,
		RecordingID: result.RecordingID,
	}
	if run.AccountID == "" {
		// A guest played a real run and gets a real result — it is just not a
		// public row. Said out loud, because a silently missing row reads as a
		// bug rather than as the sign-in boundary it is.
		msg.Reason = "Sign in to post a time."
		return msg
	}
	if s.ChallengeStore == nil {
		msg.Reason = "Leaderboard unavailable."
		return msg
	}
	rank, err := s.ChallengeStore.Submit(result)
	if err != nil {
		log.Printf("zztgo: challenge %s result not stored: %v", result.ChallengeID, err)
		msg.Reason = "Result not stored."
		return msg
	}
	msg.Durable = true
	msg.Rank = rank
	return msg
}

// challengeRecordingPath is where a run's recording landed, used by the
// verification helper below.
func (s *WebSocketServer) challengeRecordingPath(id string) (string, error) {
	dir := s.ReplayDir
	if dir == "" {
		dir = s.RecordDir
	}
	return replayPath(dir, id)
}

// VerifyChallengeResult replays a stored result's recording and reports whether
// it reproduces the evidence the row carries. It is the honest meaning of
// "replay-verified": the row is not trusted because the server wrote it, it is
// checkable because the stimuli that produced it were kept.
func (s *WebSocketServer) VerifyChallengeResult(result ChallengeResult) (bool, error) {
	path, err := s.challengeRecordingPath(result.RecordingID)
	if err != nil {
		return false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var evidence string
	var ticks int
	rm, err := ReplaySession(f, func(tick int, rm *RoomManager) {
		ticks = tick + 1
		if ticks == result.Ticks {
			evidence = challengeEvidence(rm)
		}
	})
	if err != nil {
		return false, err
	}
	if evidence == "" && rm != nil && ticks == result.Ticks {
		evidence = challengeEvidence(rm)
	}
	return evidence != "" && evidence == result.Evidence, nil
}

// ChallengeStorePath is where the leaderboard lives beside the other durable
// service state. An empty saves directory means memory-only.
func ChallengeStorePath(savesDir string) string {
	if savesDir == "" {
		return ""
	}
	return filepath.Join(savesDir, "challenge_scores.json")
}
