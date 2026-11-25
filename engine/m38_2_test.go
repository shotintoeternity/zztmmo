package zztgo

// M38.2: the judge re-simulates a script and measures it with the live run's
// own rule. The load-bearing test: a live run, projected to a script and
// judged, measures exactly what the live row says.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// m382LiveRun finishes one signed-in Gem Dash run on the real server and
// returns the harness, the run, and its projected script.
func m382LiveRun(t *testing.T) (*m321Harness, *ChallengeRun, TASScript) {
	t.Helper()
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	inst, ok := h.server.ChallengeRunInstance(snap.ChallengeRun)
	if !ok {
		t.Fatal("no run instance")
	}
	run := m321Finish(t, h, inst, snap.You.ID)
	conn.Close(websocket.StatusNormalClosure, "")
	h.server.CloseRecorders()
	f, err := os.Open(filepath.Join(h.recordDir, run.RecordingID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	script, err := ProjectRecordingToTAS(f)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return h, run, script
}

func m382Judge(t *testing.T, h *m321Harness, script TASScript, record *bytes.Buffer) TASJudgment {
	t.Helper()
	def := gemDashFixture()
	worldName, world, err := h.server.ResolveChallengeWorld(def)
	if err != nil {
		t.Fatal(err)
	}
	var w *bytes.Buffer
	if record != nil {
		w = record
	}
	var judgment TASJudgment
	if w != nil {
		judgment, err = JudgeTASScript(def, worldName, world, script, "ada", w)
	} else {
		judgment, err = JudgeTASScript(def, worldName, world, script, "ada", nil)
	}
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	return judgment
}

func TestM382AJudgedLiveRunMeasuresWhatTheLiveRowSays(t *testing.T) {
	h, run, script := m382LiveRun(t)
	judgment := m382Judge(t, h, script, nil)
	if !judgment.Met {
		t.Fatalf("the judge did not see the goal met; stopped at %d", judgment.StoppedAt)
	}
	live, judged := run.Result, judgment.Result
	if judged.Ticks != live.Ticks || judged.Score != live.Score || judged.Gems != live.Gems || judged.Evidence != live.Evidence {
		t.Fatalf("judged %d ticks / %d score / %d gems / %q, live row %d / %d / %d / %q",
			judged.Ticks, judged.Score, judged.Gems, judged.Evidence, live.Ticks, live.Score, live.Gems, live.Evidence)
	}
	if judged.ChallengeID != live.ChallengeID || judged.Version != live.Version {
		t.Fatalf("judged %s v%d, live %s v%d", judged.ChallengeID, judged.Version, live.ChallengeID, live.Version)
	}
	if judged.AccountKey != "" || judged.Name != "" || judged.RecordingID != "" {
		t.Fatalf("the judge filled in what its caller supplies: %+v", judged)
	}
}

func TestM382JudgingIsDeterministicAndItsRecordingVerifies(t *testing.T) {
	h, _, script := m382LiveRun(t)
	var first, second bytes.Buffer
	a := m382Judge(t, h, script, &first)
	b := m382Judge(t, h, script, &second)
	c := m382Judge(t, h, script, nil)
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(a, c) {
		t.Fatalf("three judgments differ:\n%+v\n%+v\n%+v", a, b, c)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("two judgments wrote different recordings")
	}

	// The recording the judge wrote is one VerifyChallengeResult accepts.
	id := "chal-gem-dash-judged-1"
	if err := os.WriteFile(filepath.Join(h.recordDir, id+".jsonl"), first.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	result := a.Result
	result.RecordingID = id
	if ok, err := h.server.VerifyChallengeResult(result); err != nil || !ok {
		t.Fatalf("the judge's recording does not verify: ok=%v err=%v", ok, err)
	}
	// And it projects back to the script that was judged, up to the tick the
	// goal was met (the judge stops there).
	back, err := ProjectRecordingToTAS(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	want := script
	want.Ticks = script.Ticks[:a.StoppedAt]
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("the judge's recording projects to %d ticks, want the %d judged", len(back.Ticks), len(want.Ticks))
	}
}

func TestM382TheJudgeStopsAtTheCeiling(t *testing.T) {
	h := m321Server(t)
	def := gemDashFixture()
	idle := TASScript{Challenge: def.ID, Version: def.Version, Ticks: make([]TASTick, TASTickCeiling)}
	for i := range idle.Ticks {
		idle.Ticks[i].Tick = i
	}
	start := time.Now()
	judgment := m382Judge(t, h, idle, nil)
	elapsed := time.Since(start)
	if judgment.Met || judgment.StoppedAt != TASTickCeiling {
		t.Fatalf("an idle full-ceiling script: %+v", judgment)
	}
	// The measurement M38.0's budget and M38.5's seek are sized by.
	t.Logf("judging one full-ceiling (%d-tick) script took %v", TASTickCeiling, elapsed)

	// One tick past the ceiling is not judged at all.
	over := idle
	over.Ticks = append(append([]TASTick(nil), idle.Ticks...), TASTick{Tick: TASTickCeiling})
	worldName, world, _ := h.server.ResolveChallengeWorld(def)
	if _, err := JudgeTASScript(def, worldName, world, over, "x", nil); err == nil {
		t.Fatal("a script over the ceiling was judged")
	}
}

// A live run counts from its join: ticks that reach the instance first are
// not the player's.
func TestM382ALiveRunCountsFromItsJoin(t *testing.T) {
	inst := &WorldInstance{Challenge: &ChallengeRun{Def: gemDashFixture()}, RoomManager: NewRoomManagerForWorld(TWorld{}, "X")}
	for i := 0; i < 5; i++ {
		if inst.advanceChallengeLocked() != nil {
			t.Fatal("a run with no player completed")
		}
	}
	if inst.Challenge.Ticks != 0 {
		t.Fatalf("a run counted %d ticks before its player joined", inst.Challenge.Ticks)
	}
}
