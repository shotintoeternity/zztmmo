package zztgo

import (
	"encoding/json"
	"fmt"
	"io"
)

// ReplayPlayback is a recorded session stepped one tick at a time for the
// browser viewer (M22.3). It is ReplaySession with pacing moved outside the
// simulation: the server tick loop calls Step, so wall-clock stays presentation
// code and the deterministic replay path is still just recorded stimuli in,
// RoomManager out.
type ReplayPlayback struct {
	header recHeader
	// next yields the recording's ticks in order. It reads a file for the
	// replay viewer and an in-memory TAS script for the workbench (M38.5); the
	// stepping below is the same for both.
	next        recTickSource
	rm          *RoomManager
	done        bool
	last        int
	seenPlayers map[PlayerID]struct{}
}

type bufioScanner interface {
	Scan() bool
	Bytes() []byte
	Err() error
}

// recTickSource yields the next recorded tick. ok is false at the end, with
// err set if the end was a failure rather than the last tick.
type recTickSource func() (tick recTick, ok bool, err error)

func scannerTickSource(scanner bufioScanner) recTickSource {
	return func() (recTick, bool, error) {
		if !scanner.Scan() {
			return recTick{}, false, scanner.Err()
		}
		var rec recTick
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return recTick{}, false, fmt.Errorf("bad tick line: %w", err)
		}
		return rec, true, nil
	}
}

func NewReplayPlayback(r io.Reader) (*ReplayPlayback, error) {
	scanner := newRecordingScanner(r)
	header, world, err := readRecordingStart(scanner)
	if err != nil {
		return nil, err
	}
	rm := NewRoomManagerForWorld(world, header.World)
	// The policy travels in the recording, not in the world's name (M30.1's
	// identity rule went with the ARENA world it served). Set before any room is
	// thawed so every engine the playback creates is created under it.
	rm.FriendlyFire = header.FriendlyFire
	return &ReplayPlayback{
		header:      header,
		next:        scannerTickSource(scanner),
		rm:          rm,
		last:        -1,
		seenPlayers: make(map[PlayerID]struct{}),
	}, nil
}

func (p *ReplayPlayback) WorldName() string {
	if p == nil {
		return ""
	}
	return p.header.World
}

func (p *ReplayPlayback) RoomManager() *RoomManager {
	if p == nil {
		return nil
	}
	return p.rm
}

func (p *ReplayPlayback) LastTick() int {
	if p == nil {
		return -1
	}
	return p.last
}

func (p *ReplayPlayback) Done() bool {
	return p == nil || p.done
}

func (p *ReplayPlayback) RoomStateHashes() map[int16]uint64 {
	if p == nil || p.rm == nil {
		return nil
	}
	return p.rm.RoomStateHashes()
}

func (p *ReplayPlayback) PlayerCountEver() int {
	if p == nil {
		return 0
	}
	return len(p.seenPlayers)
}

// Step applies one recorded tick and returns the board-addressed frames for the
// viewer. Once it reports done, later calls are stable no-ops.
func (p *ReplayPlayback) Step() (int, map[int16]DiffMessage, bool, error) {
	if p == nil {
		return -1, nil, true, fmt.Errorf("nil replay playback")
	}
	if p.done {
		return p.last, nil, true, nil
	}
	rec, ok, err := p.next()
	if !ok {
		p.done = true
		return p.last, nil, true, err
	}
	for _, op := range rec.Ops {
		if op.Op == "join" {
			p.seenPlayers[op.Player] = struct{}{}
		}
		applyRecordedOp(p.rm, op)
	}
	_, boardDiffs := p.rm.StepDiffsWithBoards(rec.Inputs)
	p.last = rec.Tick
	return rec.Tick, boardDiffs, false, nil
}
