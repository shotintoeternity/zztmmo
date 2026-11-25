package zztgo

// M38.1: the TAS input script, and its admission.
//
// A submission is an input script, not a recording (TASKS.md M38, boundary 1):
// per tick, one player's input and the scroll replies that player gave. The
// server supplies everything else — the canonical world, the join at the
// challenge start, the player's name — and writes the recording itself after
// judging. So admission parses a narrow schema rather than filtering a hostile
// file: anything the schema does not name cannot enter.
//
// Admission is a pure function from bytes and a ChallengeDefinition to an
// admitted script or a named refusal. It never reads a claimed result: a script
// has no field for ticks, score, gems or evidence (boundary 2).
//
// Keys. The in-play key switch is ElementPlayerTick (elements.go). Movement and
// shooting are DeltaX/DeltaY and Shift, and the client sends the arrow and space
// key bytes alongside them, so those are allowed. T (torch) and P (pause) are
// play. Enter is allowed because the live client sends it during play and the
// switch ignores it. S (save), Q and Escape (quit), H (help) and ? (the cheat
// prompt) are refused: none has a place in a measured run.
//
// B (sound) is allowed. Checked for M38.1: B flips PlayerState.SoundEnabled,
// which hashPlayers deliberately leaves out of StateHash as presentation
// (state_hash.go), and clears the process sound queue, which a headless judge
// does not have. It cannot change what a run measures, and a live run that
// pressed it still projects to an admissible script.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// TASTickCeiling is the most ticks a script may hold: about ten minutes of play
// at the server tick rate (M38.0's budget default), until M38.2's measurement
// of a full-ceiling judge says otherwise.
const TASTickCeiling = int(10 * time.Minute / ServerTickDuration)

// TASByteCeiling bounds a submission before it is decoded. A full-ceiling
// script of moving ticks is well under a third of this.
const TASByteCeiling = 1 << 20

// TASMaxReplies bounds the scroll replies on one tick, and TASMaxLabel a
// reply's label. ZZT answers one scroll at a time; a few allow for a reply and
// the scroll it opened in the same tick.
const (
	TASMaxReplies = 4
	TASMaxLabel   = 64
)

// TASScript is the submitted shape. Its encoding (json.Marshal) is canonical:
// an admitted script re-encodes to bytes that admit to the same script.
type TASScript struct {
	Challenge string    `json:"challenge"`
	Version   int       `json:"version"`
	Ticks     []TASTick `json:"ticks"`
}

// TASTick is one tick: the input the player held, and the scroll replies given
// just before the tick was stepped (as a recording's ops are applied).
type TASTick struct {
	Tick    int        `json:"tick"`
	DeltaX  int16      `json:"dx,omitempty"`
	DeltaY  int16      `json:"dy,omitempty"`
	Shift   bool       `json:"shift,omitempty"`
	Key     byte       `json:"key,omitempty"`
	Replies []TASReply `json:"replies,omitempty"`
}

// TASReply answers the scroll an object opened, by the object's stat and the
// label chosen. Only its shape is checked here: SubmitScrollReply refuses a
// label the open scroll did not offer (M36.16), so the simulation decides.
type TASReply struct {
	StatID int16  `json:"stat"`
	Label  string `json:"label"`
}

// Input is the tick's PlayerInput.
func (t TASTick) Input() PlayerInput {
	return PlayerInput{DeltaX: t.DeltaX, DeltaY: t.DeltaY, Shift: t.Shift, Key: t.Key}
}

// TASRefusal names why a script was not admitted. Its Class is stable, for
// tests and for the route (M38.4); its Detail is for the person who wrote it.
type TASRefusal struct {
	Class  string
	Detail string
}

func (r *TASRefusal) Error() string { return "tas: " + r.Class + ": " + r.Detail }

// The refusal classes.
const (
	TASRefuseTooLarge     = "too-large"
	TASRefuseMalformed    = "malformed"
	TASRefuseChallenge    = "wrong-challenge"
	TASRefuseVersion      = "stale-version"
	TASRefuseEmpty        = "empty"
	TASRefuseTickOrder    = "tick-order"
	TASRefuseTooLong      = "too-long"
	TASRefuseDelta        = "bad-delta"
	TASRefuseKey          = "refused-key"
	TASRefuseReply        = "bad-reply"
	TASRefuseNotChallenge = "not-a-challenge-recording"
	TASRefusePlayers      = "not-one-player"
)

func tasRefuse(class, format string, args ...interface{}) *TASRefusal {
	return &TASRefusal{Class: class, Detail: fmt.Sprintf(format, args...)}
}

// tasAllowedKeys is the in-play key allowlist (see the file comment).
var tasAllowedKeys = map[byte]bool{
	0:         true,
	KEY_UP:    true,
	KEY_DOWN:  true,
	KEY_LEFT:  true,
	KEY_RIGHT: true,
	' ':       true,
	'\r':      true,
	'T':       true, 't': true,
	'P': true, 'p': true,
	'B': true, 'b': true,
}

// AdmitTASScript decodes and checks a submitted script against def.
func AdmitTASScript(data []byte, def ChallengeDefinition) (TASScript, error) {
	if len(data) > TASByteCeiling {
		return TASScript{}, tasRefuse(TASRefuseTooLarge, "%d bytes is over the %d-byte ceiling", len(data), TASByteCeiling)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var script TASScript
	if err := dec.Decode(&script); err != nil {
		return TASScript{}, tasRefuse(TASRefuseMalformed, "%v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return TASScript{}, tasRefuse(TASRefuseMalformed, "there is more after the script")
	}
	// The canonical encoding leaves an empty reply list out, so an empty one
	// is admitted as none (FuzzAdmitTASScript found "replies":[]). Field names
	// match case-insensitively, as encoding/json always does: still only the
	// schema's fields, and the encoding an admitted script gets is canonical.
	for i := range script.Ticks {
		if len(script.Ticks[i].Replies) == 0 {
			script.Ticks[i].Replies = nil
		}
	}
	if err := CheckTASScript(script, def); err != nil {
		return TASScript{}, err
	}
	return script, nil
}

// CheckTASScript applies admission's rules to a script already in hand: the
// workbench (M38.5) checks each edit with it as the edit is applied.
func CheckTASScript(script TASScript, def ChallengeDefinition) error {
	if script.Challenge != def.ID {
		return tasRefuse(TASRefuseChallenge, "the script is for %q, not %q", script.Challenge, def.ID)
	}
	if script.Version != def.Version {
		return tasRefuse(TASRefuseVersion, "the script is for version %d of %q; the challenge is at version %d", script.Version, def.ID, def.Version)
	}
	if len(script.Ticks) == 0 {
		return tasRefuse(TASRefuseEmpty, "the script has no ticks")
	}
	if len(script.Ticks) > TASTickCeiling {
		return tasRefuse(TASRefuseTooLong, "%d ticks is over the %d-tick ceiling", len(script.Ticks), TASTickCeiling)
	}
	for i, tick := range script.Ticks {
		if err := checkTASTick(i, tick); err != nil {
			return err
		}
	}
	return nil
}

func checkTASTick(want int, tick TASTick) error {
	if tick.Tick != want {
		return tasRefuse(TASRefuseTickOrder, "tick %d is numbered %d: ticks run from 0 with none missing", want, tick.Tick)
	}
	if tick.DeltaX < -1 || tick.DeltaX > 1 || tick.DeltaY < -1 || tick.DeltaY > 1 {
		return tasRefuse(TASRefuseDelta, "tick %d moves (%d,%d); each direction is -1, 0 or 1", want, tick.DeltaX, tick.DeltaY)
	}
	if !tasAllowedKeys[tick.Key] {
		return tasRefuse(TASRefuseKey, "tick %d presses key %d (%q), which a measured run may not use", want, tick.Key, rune(tick.Key))
	}
	if len(tick.Replies) > TASMaxReplies {
		return tasRefuse(TASRefuseReply, "tick %d has %d scroll replies; at most %d", want, len(tick.Replies), TASMaxReplies)
	}
	for _, reply := range tick.Replies {
		if reply.StatID < 0 {
			return tasRefuse(TASRefuseReply, "tick %d answers stat %d", want, reply.StatID)
		}
		if reply.Label == "" || len(reply.Label) > TASMaxLabel {
			return tasRefuse(TASRefuseReply, "tick %d has a reply label of %d bytes; 1 to %d", want, len(reply.Label), TASMaxLabel)
		}
		for i := 0; i < len(reply.Label); i++ {
			if c := reply.Label[i]; c < 0x20 || c == 0x7f {
				return tasRefuse(TASRefuseReply, "tick %d has a control character in a reply label", want)
			}
		}
	}
	return nil
}

// EncodeTASScript is the canonical encoding.
func EncodeTASScript(script TASScript) ([]byte, error) {
	return json.Marshal(script)
}

// ProjectRecordingToTAS keeps a challenge recording's one player's inputs and
// scroll replies, tick for tick from the recording's tick 0: it is how a live
// run is imported into the workbench, and what M38.2's equivalence test
// judges. Everything else in the recording (the world, the join, names,
// state) is the server's to supply again. The result is not admitted here:
// a live run that pressed a refused key projects faithfully and is refused
// when it is admitted.
func ProjectRecordingToTAS(r io.Reader) (TASScript, error) {
	dec := json.NewDecoder(r)
	var header recHeader
	if err := dec.Decode(&header); err != nil {
		return TASScript{}, fmt.Errorf("read the recording header: %w", err)
	}
	if header.ChallengeID == "" {
		return TASScript{}, tasRefuse(TASRefuseNotChallenge, "the recording is of %q, not of a challenge run", header.World)
	}
	script := TASScript{Challenge: header.ChallengeID, Version: header.ChallengeVersion}
	var player PlayerID
	havePlayer := false
	for {
		var tick recTick
		if err := dec.Decode(&tick); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return TASScript{}, fmt.Errorf("read tick %d: %w", len(script.Ticks), err)
		}
		if tick.Tick != len(script.Ticks) {
			return TASScript{}, fmt.Errorf("the recording skips from tick %d to %d", len(script.Ticks)-1, tick.Tick)
		}
		out := TASTick{Tick: tick.Tick}
		for _, op := range tick.Ops {
			switch {
			case op.Op == "join":
				if havePlayer && op.Player != player {
					return TASScript{}, tasRefuse(TASRefusePlayers, "players %d and %d both joined", player, op.Player)
				}
				player, havePlayer = op.Player, true
			case op.Op == "submit" && op.Kind == "scroll" && havePlayer && op.Player == player:
				out.Replies = append(out.Replies, TASReply{StatID: op.StatID, Label: op.Label})
			}
		}
		if havePlayer {
			if in, ok := tick.Inputs[player]; ok {
				out.DeltaX, out.DeltaY, out.Shift, out.Key = in.DeltaX, in.DeltaY, in.Shift, in.Key
			}
		}
		script.Ticks = append(script.Ticks, out)
	}
	if !havePlayer {
		return TASScript{}, tasRefuse(TASRefusePlayers, "nobody joined the recorded run")
	}
	return script, nil
}
