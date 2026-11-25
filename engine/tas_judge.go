package zztgo

// M38.2: the judge. It re-simulates an admitted TAS script against the
// canonical world and measures it with the same rule a live run uses
// (measureChallengeTick), so a live 300 and an assisted 300 are the same
// number. It never reads a claimed result: a script carries none (TASKS.md
// M38, boundary 2).
//
// The join is made exactly as a live run's is: in a room manager built by
// newChallengeRoomManager, the one player joins the challenge's start board
// with JoinPlayerWithID(id, def.Board, 0, 0) before tick 0 is stepped, which is
// where a live run's join lands (its ops are applied ahead of the tick they
// are recorded under). Tick 0 is the join tick in both (advanceChallengeLocked
// counts from the join), and a recording's empty ticks before the join are
// dropped when it is projected (ProjectRecordingToTAS).
//
// If asked, the judge writes the recording it produced through the same
// SessionRecorder a live run uses, so /replay/<id>, ghosts and
// VerifyChallengeResult read it unchanged.

import (
	"fmt"
	"io"
)

// tasJudgePlayer is the judged run's one player.
const tasJudgePlayer PlayerID = 1

// TASJudgment is what the judge found. Result is set only when Met; it carries
// no account, name or recording id, which the caller supplies.
type TASJudgment struct {
	Met       bool
	Result    ChallengeResult
	StoppedAt int // ticks stepped: the completing tick, or the script's end
}

// JudgeTASScript steps script against world and measures it. name is the
// player name the recording shows. record, when non-nil, receives the
// recording; it is complete when JudgeTASScript returns.
func JudgeTASScript(def ChallengeDefinition, worldName string, world TWorld, script TASScript, name string, record io.Writer) (TASJudgment, error) {
	if err := CheckTASScript(script, def); err != nil {
		return TASJudgment{}, err
	}
	rm := newChallengeRoomManager(world, worldName)
	var recorder *SessionRecorder
	if record != nil {
		header, _, err := newSessionHeader(worldName, rm.FrozenWorld())
		if err != nil {
			return TASJudgment{}, fmt.Errorf("tas: the recording header: %w", err)
		}
		header.ChallengeID = def.ID
		header.ChallengeVersion = def.Version
		header.FriendlyFire = rm.FriendlyFire
		// The judge measures for the assisted board, and says so where every
		// reader of the recording, the replay viewer included, can see it.
		header.Division = ChallengeDivisionAssisted
		recorder, err = NewSessionRecorder(record, header)
		if err != nil {
			return TASJudgment{}, fmt.Errorf("tas: start the recording: %w", err)
		}
		recorder.lossless = true
		rm.SetRecorder(recorder)
	}
	finish := func(j TASJudgment) (TASJudgment, error) {
		if recorder != nil {
			rm.SetRecorder(nil)
			recorder.Close()
		}
		return j, nil
	}

	ticks := 0
	for i, tick := range script.Ticks {
		if i == 0 {
			if rm.JoinPlayerWithID(tasJudgePlayer, def.Board, 0, 0) == 0 {
				if recorder != nil {
					rm.SetRecorder(nil)
					recorder.Close()
				}
				return TASJudgment{}, fmt.Errorf("tas: the challenge's start board has no room for a player")
			}
			rm.SetPlayerName(tasJudgePlayer, name)
		}
		for _, reply := range tick.Replies {
			rm.SubmitScrollReply(tasJudgePlayer, reply.StatID, reply.Label)
		}
		rm.StepDiffs(map[PlayerID]PlayerInput{tasJudgePlayer: tick.Input()})
		ticks++
		if result, met := measureChallengeTick(def, ticks, tasJudgePlayer, rm); met {
			return finish(TASJudgment{Met: true, Result: result, StoppedAt: ticks})
		}
	}
	return finish(TASJudgment{StoppedAt: ticks})
}
