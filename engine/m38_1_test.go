package zztgo

// M38.1: the TAS input script and its admission. One test per refusal class,
// a fuzz test that admission never panics and never admits a script it cannot
// re-encode identically, and a round trip from a live challenge recording.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nhooyr.io/websocket"
)

func m381Def() ChallengeDefinition {
	def := gemDashFixture()
	return def
}

func m381Script(def ChallengeDefinition, ticks ...TASTick) TASScript {
	for i := range ticks {
		ticks[i].Tick = i
	}
	return TASScript{Challenge: def.ID, Version: def.Version, Ticks: ticks}
}

func m381Encode(t *testing.T, s TASScript) []byte {
	t.Helper()
	data, err := EncodeTASScript(s)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func m381WantRefusal(t *testing.T, data []byte, def ChallengeDefinition, class string) {
	t.Helper()
	_, err := AdmitTASScript(data, def)
	var refusal *TASRefusal
	if !errors.As(err, &refusal) || refusal.Class != class {
		t.Fatalf("admitting %s: err = %v, want a %q refusal", m381Head(data), err, class)
	}
}

func m381Head(data []byte) string {
	if len(data) > 160 {
		return string(data[:160]) + "…"
	}
	return string(data)
}

func TestM381AnOrdinaryScriptIsAdmitted(t *testing.T) {
	def := m381Def()
	script := m381Script(def,
		TASTick{DeltaX: 1, Key: KEY_RIGHT},
		TASTick{DeltaY: -1, Key: KEY_UP, Shift: true},
		TASTick{Key: ' ', Shift: true},
		TASTick{Key: 'T'},
		TASTick{Key: 'p'},
		TASTick{Key: 'B'},
		TASTick{Key: '\r'},
		TASTick{Replies: []TASReply{{StatID: 3, Label: "yes"}}},
		TASTick{},
	)
	got, err := AdmitTASScript(m381Encode(t, script), def)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if !reflect.DeepEqual(got, script) {
		t.Fatalf("admitted %+v, want %+v", got, script)
	}
	if got.Ticks[1].Input() != (PlayerInput{DeltaY: -1, Key: KEY_UP, Shift: true}) {
		t.Fatalf("Input() = %+v", got.Ticks[1].Input())
	}
}

func TestM381EveryRefusalClass(t *testing.T) {
	def := m381Def()
	ok := func(ticks ...TASTick) TASScript { return m381Script(def, ticks...) }
	enc := func(s TASScript) []byte { return m381Encode(t, s) }

	t.Run("too-large", func(t *testing.T) {
		m381WantRefusal(t, bytes.Repeat([]byte(" "), TASByteCeiling+1), def, TASRefuseTooLarge)
	})
	t.Run("malformed", func(t *testing.T) {
		for _, data := range []string{
			`not json`,
			`{"challenge":"gem-dash","version":1,"ticks":[{"tick":0}],"ticks_claimed":300}`,
			`{"challenge":"gem-dash","version":1,"ticks":[{"tick":0,"evidence":"x"}]}`,
			`{"challenge":"gem-dash","version":1,"ticks":[{"tick":0,"op":"join"}]}`,
			`{"challenge":"gem-dash","version":1,"ticks":[{"tick":0}]} {"more":1}`,
			`{"challenge":"gem-dash","version":1,"ticks":[{"tick":0,"key":300}]}`,
		} {
			m381WantRefusal(t, []byte(data), def, TASRefuseMalformed)
		}
	})
	t.Run("wrong-challenge", func(t *testing.T) {
		s := ok(TASTick{})
		s.Challenge = "another"
		m381WantRefusal(t, enc(s), def, TASRefuseChallenge)
	})
	t.Run("stale-version", func(t *testing.T) {
		s := ok(TASTick{})
		s.Version = def.Version + 1
		m381WantRefusal(t, enc(s), def, TASRefuseVersion)
	})
	t.Run("empty", func(t *testing.T) {
		m381WantRefusal(t, enc(ok()), def, TASRefuseEmpty)
	})
	t.Run("tick-order", func(t *testing.T) {
		gap := ok(TASTick{}, TASTick{}, TASTick{})
		gap.Ticks[2].Tick = 3
		m381WantRefusal(t, enc(gap), def, TASRefuseTickOrder)
		late := ok(TASTick{})
		late.Ticks[0].Tick = 1
		m381WantRefusal(t, enc(late), def, TASRefuseTickOrder)
	})
	t.Run("too-long", func(t *testing.T) {
		m381WantRefusal(t, enc(ok(make([]TASTick, TASTickCeiling+1)...)), def, TASRefuseTooLong)
		if _, err := AdmitTASScript(enc(ok(make([]TASTick, TASTickCeiling)...)), def); err != nil {
			t.Fatalf("a script exactly at the ceiling: %v", err)
		}
	})
	t.Run("bad-delta", func(t *testing.T) {
		for _, tick := range []TASTick{{DeltaX: 2}, {DeltaY: -2}, {DeltaX: 300}} {
			m381WantRefusal(t, enc(ok(TASTick{}, tick)), def, TASRefuseDelta)
		}
	})
	t.Run("refused-key", func(t *testing.T) {
		for _, key := range []byte{'S', 's', 'Q', 'q', 27, 'H', 'h', '?', 'X', 1} {
			m381WantRefusal(t, enc(ok(TASTick{Key: key})), def, TASRefuseKey)
		}
	})
	t.Run("bad-reply", func(t *testing.T) {
		for _, replies := range [][]TASReply{
			{{StatID: 1, Label: ""}},
			{{StatID: -1, Label: "yes"}},
			{{StatID: 1, Label: strings.Repeat("x", TASMaxLabel+1)}},
			{{StatID: 1, Label: "a\nb"}},
			{{StatID: 1, Label: "a"}, {StatID: 1, Label: "b"}, {StatID: 1, Label: "c"}, {StatID: 1, Label: "d"}, {StatID: 1, Label: "e"}},
		} {
			m381WantRefusal(t, enc(ok(TASTick{Replies: replies})), def, TASRefuseReply)
		}
	})
}

// FuzzAdmitTASScript: admission never panics, and whatever it admits re-encodes
// to bytes that admit to the same script and encode the same again.
func FuzzAdmitTASScript(f *testing.F) {
	def := m381Def()
	seed := func(s TASScript) {
		data, err := EncodeTASScript(s)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	seed(m381Script(def, TASTick{DeltaX: 1, Key: KEY_RIGHT}, TASTick{Replies: []TASReply{{StatID: 2, Label: "go"}}}))
	seed(m381Script(def, TASTick{Key: 'S'}))
	f.Add([]byte(`{"challenge":"gem-dash","version":1,"ticks":[{"tick":0,"dx":-1,"shift":true}]}`))
	f.Add([]byte(`{"challenge":"gem-dash","version":1,"ticks":[{"tick":0,"replies":[{"stat":0,"label":"é"}]}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[`))
	f.Fuzz(func(t *testing.T, data []byte) {
		script, err := AdmitTASScript(data, def)
		if err != nil {
			return
		}
		again, err := EncodeTASScript(script)
		if err != nil {
			t.Fatalf("an admitted script does not encode: %v", err)
		}
		readmitted, err := AdmitTASScript(again, def)
		if err != nil {
			t.Fatalf("an admitted script's encoding is refused: %v\n%s", err, again)
		}
		if !reflect.DeepEqual(readmitted, script) {
			t.Fatalf("re-admitted %+v, want %+v", readmitted, script)
		}
		third, _ := EncodeTASScript(readmitted)
		if !bytes.Equal(third, again) {
			t.Fatalf("the encoding is not stable:\n%s\n%s", again, third)
		}
	})
}

// A live challenge run, recorded by the real server, projects to a script that
// carries its inputs tick for tick, and that script admits and round-trips.
func TestM381ALiveRecordingProjectsToAnAdmittedScript(t *testing.T) {
	h := m321Server(t)
	conn, snap := h.run(t, "?challenge=gem-dash", &m321Ada)
	inst, ok := h.server.ChallengeRunInstance(snap.ChallengeRun)
	if !ok {
		t.Fatal("no run instance")
	}
	run := m321Finish(t, h, inst, snap.You.ID)
	conn.Close(websocket.StatusNormalClosure, "")
	h.server.CloseRecorders()

	path := filepath.Join(h.recordDir, run.RecordingID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	script, err := ProjectRecordingToTAS(f)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	def := m381Def()
	if script.Challenge != def.ID || script.Version != def.Version {
		t.Fatalf("projected %s v%d", script.Challenge, script.Version)
	}
	moving := 0
	for _, tick := range script.Ticks {
		if tick.DeltaX == 1 {
			moving++
		}
		if tick.DeltaY != 0 || tick.DeltaX < 0 {
			t.Fatalf("tick %d moves %+v; the run only walked east", tick.Tick, tick)
		}
	}
	if moving == 0 || len(script.Ticks) < run.Result.Ticks {
		t.Fatalf("projected %d ticks (%d moving) from a %d-tick run", len(script.Ticks), moving, run.Result.Ticks)
	}

	data := m381Encode(t, script)
	admitted, err := AdmitTASScript(data, def)
	if err != nil {
		t.Fatalf("the projected live run is refused: %v", err)
	}
	if !reflect.DeepEqual(admitted, script) {
		t.Fatal("the projected script does not round-trip through admission")
	}

	// A world's ordinary recording is not a challenge run.
	var plain bytes.Buffer
	plain.WriteString(`{"v":2,"world":"TOWN","worldHash":0,"worldBytes":"","seed":1}` + "\n")
	var refusal *TASRefusal
	if _, err := ProjectRecordingToTAS(&plain); !errors.As(err, &refusal) || refusal.Class != TASRefuseNotChallenge {
		t.Fatalf("projecting a non-challenge recording: %v", err)
	}
}
