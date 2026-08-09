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
	header      recHeader
	scanner     bufioScanner
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

func NewReplayPlayback(r io.Reader) (*ReplayPlayback, error) {
	scanner := newRecordingScanner(r)
	header, world, err := readRecordingStart(scanner)
	if err != nil {
		return nil, err
	}
	return &ReplayPlayback{
		header:      header,
		scanner:     scanner,
		rm:          NewRoomManagerForWorld(world, header.World),
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
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			p.done = true
			return p.last, nil, true, err
		}
		p.done = true
		return p.last, nil, true, nil
	}

	var rec recTick
	if err := json.Unmarshal(p.scanner.Bytes(), &rec); err != nil {
		p.done = true
		return p.last, nil, true, fmt.Errorf("bad tick line: %w", err)
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
