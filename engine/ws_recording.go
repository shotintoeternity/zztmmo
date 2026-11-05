package zztgo

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// maxRecordingNameAttempts bounds the search for an unused recording name.
const maxRecordingNameAttempts = 1000

// EnableRecording turns on session recording for every current and future
// instance, writing one JSONL file per instance under dir (M14.2). It is
// best-effort: it creates the directory and attaches recorders to the instances
// that already exist (the boot world). Instances created later attach their own
// recorder from GetOrCreateInstance / HostGeneratedWorld.
func (s *WebSocketServer) EnableRecording(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RecordDir = dir
	s.recordStamp = time.Now().UTC().Format("20060102-150405")
	for _, inst := range s.Instances {
		s.attachRecorderLocked(inst)
	}
	return nil
}

// attachRecorderLocked opens a recording for one instance if recording is on and
// the instance is not already recording. Caller holds s.mu. Failures are logged,
// never fatal: a debugging feature must not stop a world from hosting.
func (s *WebSocketServer) attachRecorderLocked(inst *WorldInstance) {
	if s.RecordDir == "" || inst.RoomManager.recorder != nil {
		return
	}
	// FrozenWorld is the manager's authoritative world; before the first client
	// or tick it is the pristine starting state playback must begin from.
	//
	// The header names a WORLD and the file is named by the instance, and for a
	// challenge run those are two different strings (M32.1): the run's key is not
	// a name any directory answers to, and /replay/<id> refuses a recording whose
	// header world it cannot load.
	headerWorld := inst.Name
	if inst.RecordWorld != "" {
		headerWorld = inst.RecordWorld
	}
	header, _, err := newSessionHeader(headerWorld, inst.RoomManager.FrozenWorld())
	if err != nil {
		log.Printf("zztgo: session recording disabled for %q: %v", inst.Name, err)
		return
	}
	if inst.Challenge != nil {
		// The recording says what it is a recording OF (M32.1). These are added
		// fields rather than a recordVersion bump: an unknown key is ignored on
		// unmarshal, so every existing recording and fixture still reads, and a
		// bump would refuse them all to record something nothing old needs.
		header.ChallengeID = inst.Challenge.Def.ID
		header.ChallengeVersion = inst.Challenge.Def.Version
	}
	// Friendly fire is set on the manager rather than derived from the world
	// name, so the recording has to carry it or playback silently diverges.
	header.FriendlyFire = inst.RoomManager.FriendlyFire
	f, path, err := s.createRecordingFileLocked(inst)
	if err != nil {
		log.Printf("zztgo: session recording disabled for %q: %v", inst.Name, err)
		return
	}
	rec, err := NewSessionRecorder(f, header)
	if err != nil {
		_ = f.Close()
		log.Printf("zztgo: session recording disabled for %q: %v", inst.Name, err)
		return
	}
	inst.RoomManager.SetRecorder(rec)
	log.Printf("zztgo: recording session for %q to %s", inst.Name, path)
}

// createRecordingFileLocked opens a new, empty recording file for inst. It
// never opens a file that already exists (M36.19): the stem is
// <instance>-<boot stamp>, and a world hosted again in the same process (after
// an eviction, a republish or a generation over it) used to os.Create that same
// name and zero the earlier recording under its live /replay/ links. The first
// recording keeps the bare stem; later ones take -2, -3, and so on. A fixed
// RecordID (a challenge run's, already unique by its sequence) gets exactly one
// exclusive attempt. Caller holds s.mu.
func (s *WebSocketServer) createRecordingFileLocked(inst *WorldInstance) (*os.File, string, error) {
	open := func(stem string) (*os.File, string, error) {
		path := filepath.Join(s.RecordDir, stem+".jsonl")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		return f, path, err
	}
	if inst.RecordID != "" {
		return open(inst.RecordID)
	}
	stem := inst.Name + "-" + s.recordStamp
	for n := 1; n <= maxRecordingNameAttempts; n++ {
		candidate := stem
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d", stem, n)
		}
		f, path, err := open(candidate)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return f, path, err
	}
	return nil, "", fmt.Errorf("no unused recording name for %q", stem)
}

// CloseRecorders flushes and closes every instance's session recorder. It runs
// when Run's context is cancelled so a clean shutdown does not lose the buffered
// tail of a recording. An abrupt kill can still lose buffered lines — acceptable
// for a debugging feature.
func (s *WebSocketServer) CloseRecorders() {
	s.mu.Lock()
	var recorders []*SessionRecorder
	for _, inst := range s.Instances {
		if inst.RoomManager.recorder != nil {
			recorders = append(recorders, inst.RoomManager.recorder)
			inst.RoomManager.SetRecorder(nil)
		}
	}
	s.mu.Unlock()
	for _, rec := range recorders {
		rec.Close()
	}
}
