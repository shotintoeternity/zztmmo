package zztgo

// M38.4: POST /api/challenge/<id>/assisted. Route tests for every refusal, a
// pass that serves its replay and its row, and a load test that ten
// concurrent full-ceiling submissions are never judged more than two at once.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type m384Harness struct {
	h      *m321Harness
	script TASScript
	url    string
}

func m384Setup(t *testing.T) *m384Harness {
	t.Helper()
	h, _, script := m382LiveRun(t)
	api := &WebAPI{RoomManager: h.server.RoomManager, Server: h.server, Auth: h.server.Auth}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &m384Harness{h: h, script: script, url: srv.URL + "/api/challenge/" + gemDashFixture().ID + "/assisted"}
}

func (m *m384Harness) post(t *testing.T, body []byte, cookie *http.Cookie, from string) (int, AssistedSubmitResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, m.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if from != "" {
		req.Header.Set("X-Forwarded-For", from)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out AssistedSubmitResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestM384ASignedInPassIsARowAndAReplay(t *testing.T) {
	m := m384Setup(t)
	cookie := signedAuthCookie(t, m.h.server.Auth, m321Bo)
	status, got := m.post(t, m381Encode(t, m.script), cookie, "")
	if status != http.StatusOK || !got.Met || !got.Durable || got.Rank != 1 || got.RecordingID == "" || got.Replay != "/replay/"+got.RecordingID {
		t.Fatalf("pass = %d %+v", status, got)
	}
	rows := m.h.server.ChallengeStore.LeaderboardIn(gemDashFixture().ID, ChallengeDivisionAssisted, m321Bo.ID)
	if len(rows) != 1 || !rows[0].You || rows[0].RecordingID != got.RecordingID || rows[0].Ticks != got.Ticks {
		t.Fatalf("the assisted board = %+v", rows)
	}
	if live := m.h.server.ChallengeStore.Leaderboard(gemDashFixture().ID, m321Bo.ID); len(live) != 1 || live[0].You {
		t.Fatalf("the live board changed: %+v", live)
	}
	row, ok := m.h.server.ChallengeStore.ResultForRecordingIn(gemDashFixture().ID, ChallengeDivisionAssisted, got.RecordingID)
	if !ok {
		t.Fatal("no stored row for the recording")
	}
	if ok, err := m.h.server.VerifyChallengeResult(row); err != nil || !ok {
		t.Fatalf("the server-authored recording does not verify: %v %v", ok, err)
	}
	if _, err := m.h.server.GetOrCreateReplayInstance(got.RecordingID); err != nil {
		t.Fatalf("the run is not watchable at /replay/%s: %v", got.RecordingID, err)
	}
}

func TestM384AGuestPassIsAReplayWithNoRow(t *testing.T) {
	m := m384Setup(t)
	status, got := m.post(t, m381Encode(t, m.script), nil, "")
	if status != http.StatusOK || !got.Met || got.Durable || got.Rank != 0 || got.RecordingID == "" {
		t.Fatalf("guest pass = %d %+v", status, got)
	}
	if rows := m.h.server.ChallengeStore.LeaderboardIn(gemDashFixture().ID, ChallengeDivisionAssisted, ""); len(rows) != 0 {
		t.Fatalf("a guest's run made a row: %+v", rows)
	}
	if _, err := m.h.server.GetOrCreateReplayInstance(got.RecordingID); err != nil {
		t.Fatalf("the guest's replay link does not serve: %v", err)
	}
}

func TestM384EveryRefusal(t *testing.T) {
	m := m384Setup(t)
	def := gemDashFixture()

	// Wrong method, unknown challenge.
	resp, err := http.Get(m.url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", resp.StatusCode)
	}
	resp, err = http.Post(strings.Replace(m.url, def.ID, "no-such", 1), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown challenge = %d", resp.StatusCode)
	}

	// Too large, before admission.
	if status, got := m.post(t, bytes.Repeat([]byte(" "), TASByteCeiling+1), nil, "10.0.0.1"); status != http.StatusRequestEntityTooLarge || got.Refusal != TASRefuseTooLarge {
		t.Fatalf("too large = %d %+v", status, got)
	}
	// Admission's own refusal, named.
	bad := m.script
	bad.Ticks = append([]TASTick(nil), m.script.Ticks...)
	bad.Ticks[0].Key = 'S'
	if status, got := m.post(t, m381Encode(t, bad), nil, "10.0.0.2"); status != http.StatusBadRequest || got.Refusal != TASRefuseKey {
		t.Fatalf("a save key = %d %+v", status, got)
	}
	// A goal not met names the tick the judge stopped at, and keeps nothing.
	short := TASScript{Challenge: def.ID, Version: def.Version, Ticks: []TASTick{{Tick: 0}, {Tick: 1}, {Tick: 2}}}
	status, got := m.post(t, m381Encode(t, short), nil, "10.0.0.3")
	if status != http.StatusUnprocessableEntity || got.Refusal != "goal-not-met" || got.StoppedAt != 3 || !strings.Contains(got.Reason, "tick 3") {
		t.Fatalf("goal not met = %d %+v", status, got)
	}
	// The rate: a submitter's seventh in a minute is refused.
	for i := 0; i < tasSubmitsPerWindow; i++ {
		if status, _ := m.post(t, m381Encode(t, short), nil, "10.0.0.4"); status != http.StatusUnprocessableEntity {
			t.Fatalf("submission %d = %d", i+1, status)
		}
	}
	if status, got := m.post(t, m381Encode(t, short), nil, "10.0.0.4"); status != http.StatusTooManyRequests || got.Refusal != "rate" {
		t.Fatalf("over the rate = %d %+v", status, got)
	}
	// Another submitter is not held to the first one's rate.
	if status, _ := m.post(t, m381Encode(t, short), nil, "10.0.0.5"); status != http.StatusUnprocessableEntity {
		t.Fatalf("a second submitter = %d", status)
	}
	// Nothing refused left a recording behind.
	entries, _ := os.ReadDir(m.h.recordDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "assisted") {
			t.Fatalf("a refused submission left %s", e.Name())
		}
	}
}

// Ten concurrent full-ceiling submissions are refused or judged within the
// budget: never more than two judgments at once.
func TestM384TenAtOnceStayWithinTheJudgeBudget(t *testing.T) {
	m := m384Setup(t)
	def := gemDashFixture()
	idle := TASScript{Challenge: def.ID, Version: def.Version, Ticks: make([]TASTick, TASTickCeiling)}
	for i := range idle.Ticks {
		idle.Ticks[i].Tick = i
	}
	body := m381Encode(t, idle)

	var running, peak int32
	gate := m.h.server.tasGate()
	gate.onJudge = func() {
		n := atomic.AddInt32(&running, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // hold the slot long enough to overlap
		atomic.AddInt32(&running, -1)
	}

	var wg sync.WaitGroup
	statuses := make([]int, 10)
	start := time.Now()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _ = m.post(t, body, nil, fmt.Sprintf("10.1.0.%d", i))
		}(i)
	}
	wg.Wait()
	judged, busy := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusUnprocessableEntity:
			judged++
		case http.StatusServiceUnavailable:
			busy++
		default:
			t.Fatalf("a concurrent submission got %d", s)
		}
	}
	if peak > tasJudgeLimit {
		t.Fatalf("%d judgments ran at once; the budget is %d", peak, tasJudgeLimit)
	}
	if judged == 0 || judged+busy != 10 {
		t.Fatalf("judged %d, busy %d", judged, busy)
	}
	t.Logf("10 concurrent full-ceiling submissions: %d judged, %d told the judges were busy, peak %d at once, %v in all", judged, busy, peak, time.Since(start))
}
