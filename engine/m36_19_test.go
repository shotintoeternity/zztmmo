package zztgo

// M36.19 — session recordings are never truncated, and replays are bounded.
//
// (a) A recording was named <instance>-<boot stamp>.jsonl and opened with
// os.Create, so the second instance of a world in one process (after an
// eviction, a republish, or hostGeneratedWorld over it) zeroed the first
// recording under its live /replay/ links. hostGeneratedWorld also dropped
// the superseded instance without closing its recorder, leaking the fd.
//
// (b) Replay instances were keyed by id@start, never removed and ticked
// forever; start was unbounded client input, so one anonymous client could
// mint unlimited instances, and any watcher could spam op:"restart".

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m3619Recordings returns the .jsonl files in dir, sorted, with their bytes.
func m3619Recordings(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read record dir: %v", err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = data
	}
	return out
}

// m3619Ticks counts the tick lines a recording replays.
func m3619Ticks(t *testing.T, name string, data []byte) int {
	t.Helper()
	playback, err := NewReplayPlayback(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s does not open as a recording: %v", name, err)
	}
	ticks := 0
	for {
		_, _, done, err := playback.Step()
		if err != nil {
			t.Fatalf("%s: step: %v", name, err)
		}
		if done {
			return ticks
		}
		ticks++
	}
}

// m3619PlayTicks joins a player into inst and steps it n times, then leaves.
func m3619PlayTicks(t *testing.T, inst *WorldInstance, n int) {
	t.Helper()
	inst.mu.Lock()
	defer inst.mu.Unlock()
	player := inst.RoomManager.JoinPlayer(1, 0, 0)
	inst.RoomManager.Snapshot(player)
	for i := 0; i < n; i++ {
		inst.RoomManager.StepDiffs(map[PlayerID]PlayerInput{player: {DeltaX: 1}})
	}
	inst.RoomManager.LeavePlayer(player)
}

func TestM3619EvictionDoesNotTruncateTheEarlierRecording(t *testing.T) {
	server := m3618Server(t)
	recordDir := t.TempDir()
	if err := server.EnableRecording(recordDir); err != nil {
		t.Fatalf("EnableRecording: %v", err)
	}

	first, err := server.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatalf("GetOrCreateInstance: %v", err)
	}
	m3619PlayTicks(t, first, 5)
	// Evict it the way the tick does, then bring BETA back.
	for i := 0; i < 10 && !first.evicted; i++ {
		server.Tick(context.Background())
	}
	if !first.evicted {
		t.Fatal("BETA was never evicted")
	}
	before := m3619Recordings(t, recordDir)
	var firstName string
	for name := range before {
		if strings.HasPrefix(name, "BETA-") {
			firstName = name
		}
	}
	if firstName == "" {
		t.Fatalf("no BETA recording before eviction: %v", before)
	}
	firstTicks := m3619Ticks(t, firstName, before[firstName])
	if firstTicks < 5 {
		t.Fatalf("first recording has %d ticks, want at least 5", firstTicks)
	}

	second, err := server.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatalf("GetOrCreateInstance again: %v", err)
	}
	if second == first {
		t.Fatal("the evicted instance was handed back")
	}
	m3619PlayTicks(t, second, 2)
	server.CloseRecorders()

	after := m3619Recordings(t, recordDir)
	if got := m3619Ticks(t, firstName, after[firstName]); got != firstTicks {
		t.Errorf("%s replays %d ticks after BETA came back, want the original %d", firstName, got, firstTicks)
	}
	var beta []string
	for name := range after {
		if strings.HasPrefix(name, "BETA-") {
			beta = append(beta, name)
		}
	}
	sort.Strings(beta)
	if len(beta) != 2 {
		t.Fatalf("want two BETA recordings, found %v", beta)
	}
	for _, name := range beta {
		if _, err := sanitizeReplayID(name); err != nil {
			t.Errorf("recording %s is not a servable replay id: %v", name, err)
		}
	}
}

func TestM3619HostingOverAWorldKeepsAndClosesItsRecording(t *testing.T) {
	server := m3618Server(t)
	recordDir := t.TempDir()
	if err := server.EnableRecording(recordDir); err != nil {
		t.Fatalf("EnableRecording: %v", err)
	}
	if err := server.HostGeneratedWorld("DREAM", testEmptyWorld(t)); err != nil {
		t.Fatalf("HostGeneratedWorld: %v", err)
	}
	server.mu.Lock()
	first := server.Instances["DREAM"]
	server.mu.Unlock()
	rec := first.RoomManager.recorder
	if rec == nil {
		t.Fatal("DREAM was hosted without a recorder")
	}
	m3619PlayTicks(t, first, 4)

	if err := server.HostGeneratedWorld("DREAM", testEmptyWorld(t)); err != nil {
		t.Fatalf("HostGeneratedWorld again: %v", err)
	}
	select {
	case <-rec.done:
	default:
		t.Error("the superseded instance's recorder was left open")
	}
	server.mu.Lock()
	second := server.Instances["DREAM"]
	server.mu.Unlock()
	m3619PlayTicks(t, second, 1)
	server.CloseRecorders()

	got := m3619Recordings(t, recordDir)
	counts := []int{}
	for name, data := range got {
		if strings.HasPrefix(name, "DREAM-") {
			counts = append(counts, m3619Ticks(t, name, data))
		}
	}
	sort.Ints(counts)
	if len(counts) != 2 || counts[0] != 1 || counts[1] != 4 {
		t.Fatalf("DREAM recordings replay %v ticks, want one of 4 and one of 1", counts)
	}
}

// A file already on disk under the name a recording wants (a previous boot in
// the same second, say) is not truncated either.
func TestM3619RecordingNeverOpensAnExistingFile(t *testing.T) {
	server := m3618Server(t)
	recordDir := t.TempDir()
	if err := server.EnableRecording(recordDir); err != nil {
		t.Fatalf("EnableRecording: %v", err)
	}
	squatter := filepath.Join(recordDir, "BETA-"+server.recordStamp+".jsonl")
	if err := os.WriteFile(squatter, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write squatter: %v", err)
	}
	inst, err := server.GetOrCreateInstance("BETA")
	if err != nil {
		t.Fatalf("GetOrCreateInstance: %v", err)
	}
	if inst.RoomManager.recorder == nil {
		t.Fatal("BETA was not recorded")
	}
	server.CloseRecorders()
	data, err := os.ReadFile(squatter)
	if err != nil || string(data) != "keep me\n" {
		t.Fatalf("existing file was overwritten: %q, %v", data, err)
	}
}

// --- (b) replays ---

func TestM3619ReplayStartIsSnappedToTheRecording(t *testing.T) {
	server, _, _, id := m367ReplayServer(t)
	// The recording has 48 ticks; every start past its end is the same replay.
	a, err := server.GetOrCreateReplayInstanceAt(id, 1000)
	if err != nil {
		t.Fatalf("start 1000: %v", err)
	}
	b, err := server.GetOrCreateReplayInstanceAt(id, 1<<40)
	if err != nil {
		t.Fatalf("start 1<<40: %v", err)
	}
	if a != b {
		t.Error("two starts past the recording's end made two replay instances")
	}
	server.mu.Lock()
	n := len(server.ReplayInstances)
	server.mu.Unlock()
	if n != 1 {
		t.Errorf("%d replay instances, want 1", n)
	}
}

func TestM3619ReplayInstancesAreCapped(t *testing.T) {
	server, _, _, id := m367ReplayServer(t)
	refused := false
	for start := 0; start < maxReplayInstances+5; start++ {
		if _, err := server.GetOrCreateReplayInstanceAt(id, start); err != nil {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatalf("%d distinct starts were all hosted", maxReplayInstances+5)
	}
	server.mu.Lock()
	n := len(server.ReplayInstances)
	server.mu.Unlock()
	if n > maxReplayInstances {
		t.Fatalf("%d replay instances, cap is %d", n, maxReplayInstances)
	}
}

func TestM3619UnwatchedReplayIsEvictedAndClosed(t *testing.T) {
	server, _, ctx, id := m367ReplayServer(t)
	server.ReplayEvictIdleTicks = 3
	replay, err := server.GetOrCreateReplayInstanceAt(id, 4)
	if err != nil {
		t.Fatalf("GetOrCreateReplayInstanceAt: %v", err)
	}
	for i := 0; i < 5; i++ {
		server.Tick(ctx)
	}
	server.mu.Lock()
	n := len(server.ReplayInstances)
	server.mu.Unlock()
	if n != 0 {
		t.Fatalf("an unwatched replay is still hosted after 5 ticks (%d instances)", n)
	}
	replay.mu.Lock()
	file := replay.file
	replay.mu.Unlock()
	if file != nil {
		t.Error("the evicted replay's file was left open")
	}
	// A watcher arriving afterwards gets a fresh, working replay.
	if _, err := server.GetOrCreateReplayInstanceAt(id, 4); err != nil {
		t.Fatalf("replay after eviction: %v", err)
	}
}

func TestM3619WatchedReplayIsNotEvicted(t *testing.T) {
	server, wsURL, ctx, id := m367ReplayServer(t)
	server.ReplayEvictIdleTicks = 2
	conn, _ := m223WatchReplay(t, ctx, wsURL, id)
	defer conn.Close(websocket.StatusNormalClosure, "")
	// Past the end of the recording too: a watched, finished replay stays.
	for i := 0; i < 60; i++ {
		server.Tick(ctx)
	}
	server.mu.Lock()
	n := len(server.ReplayInstances)
	server.mu.Unlock()
	if n != 1 {
		t.Fatalf("a watched replay was evicted (%d instances)", n)
	}
}

// A replay evicted between being resolved and the watcher registering is not
// joined: the watcher lands on a live one.
func TestM3619WatcherDoesNotJoinAnEvictedReplay(t *testing.T) {
	server, _, _, id := m367ReplayServer(t)
	stale, err := server.GetOrCreateReplayInstanceAt(id, 0)
	if err != nil {
		t.Fatalf("GetOrCreateReplayInstanceAt: %v", err)
	}
	server.mu.Lock()
	delete(server.ReplayInstances, replayInstanceKey(stale.ID, stale.startTick))
	server.mu.Unlock()
	stale.evict()

	client := &webSocketClient{}
	if _, ok := stale.replaySpectatorSnapshot(&spectator{client: client, boardID: 1}); ok {
		t.Fatal("a watcher was registered on an evicted replay")
	}
	live, _, _, err := server.watchReplay(id, 0, client, 1)
	if err != nil {
		t.Fatalf("watchReplay: %v", err)
	}
	if live == stale {
		t.Fatal("watchReplay handed back the evicted replay")
	}
	live.mu.Lock()
	_, registered := live.Spectators[client]
	live.mu.Unlock()
	if !registered {
		t.Fatal("the watcher is not on the live replay")
	}
}

func TestM3619ReplayRestartIsRateLimited(t *testing.T) {
	server, wsURL, ctx, id := m367ReplayServer(t)
	replay, err := server.GetOrCreateReplayInstanceAt(id, 0)
	if err != nil {
		t.Fatalf("GetOrCreateReplayInstanceAt: %v", err)
	}
	conn, _ := m223WatchReplay(t, ctx, wsURL, id)
	defer conn.Close(websocket.StatusNormalClosure, "")
	// Drain the watcher in the background so the server's writes never back up.
	// A timed-out read would close an nhooyr connection, so this one just runs.
	go func() {
		for {
			var raw map[string]interface{}
			if wsjson.Read(ctx, conn, &raw) != nil {
				return
			}
		}
	}()
	send := func(op string) {
		t.Helper()
		if err := wsjson.Write(ctx, conn, ReplayControlMessage{Type: MessageTypeReplayControl, Op: op}); err != nil {
			t.Fatalf("write %s: %v", op, err)
		}
	}
	// waitFor polls the replay until cond holds. The read loop handles one
	// control at a time, so a "pause" sent last is a fence for everything
	// before it.
	waitFor := func(label string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			replay.mu.Lock()
			ok := cond()
			replay.mu.Unlock()
			if ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", label)
	}

	for i := 0; i < 20; i++ {
		send("restart")
	}
	send("pause")
	waitFor("the pause fence", func() bool { return replay.Paused })
	replay.mu.Lock()
	got := replay.restarts
	replay.mu.Unlock()
	if got != 1 {
		t.Fatalf("20 back-to-back restarts reopened the recording %d times, want 1", got)
	}

	// Once the cooldown has passed (paused ticks still count), a restart is
	// honoured again.
	for i := 0; i < replayRestartCooldownTicks; i++ {
		server.Tick(ctx)
	}
	send("restart")
	waitFor("a second honoured restart", func() bool { return replay.restarts == 2 })
}
