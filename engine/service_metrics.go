package zztgo

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

const serviceTimingWindow = 512

type serverMetrics struct {
	mu        sync.Mutex
	startedAt time.Time
	tick      serviceTiming
	instances map[string]serviceInstanceTiming
}

type serviceTiming struct {
	Count   uint64
	Last    time.Duration
	Max     time.Duration
	Total   time.Duration
	Samples []time.Duration
}

type serviceInstanceTiming struct {
	Step serviceTiming
	Tick serviceTiming
}

type ServiceStatus struct {
	Status        string                  `json:"status"`
	StartedAt     string                  `json:"startedAt"`
	UptimeSeconds int64                   `json:"uptimeSeconds"`
	Totals        ServiceTotals           `json:"totals"`
	Tick          ServiceTimingSnapshot   `json:"tick"`
	Memory        ServiceMemorySnapshot   `json:"memory"`
	Instances     []ServiceInstanceStatus `json:"instances"`
	Replays       []ServiceReplayStatus   `json:"replays,omitempty"`
	Editors       []ServiceEditorStatus   `json:"editors,omitempty"`
}

type ServiceTotals struct {
	Instances   int `json:"instances"`
	Rooms       int `json:"rooms"`
	ActiveRooms int `json:"activeRooms"`
	Players     int `json:"players"`
	Detached    int `json:"detached"`
	Spectators  int `json:"spectators"`
	Replays     int `json:"replays"`
	Editors     int `json:"editors"`
}

type ServiceTimingSnapshot struct {
	Count  uint64  `json:"count"`
	LastMS float64 `json:"lastMs"`
	AvgMS  float64 `json:"avgMs"`
	P95MS  float64 `json:"p95Ms"`
	MaxMS  float64 `json:"maxMs"`
}

type ServiceMemorySnapshot struct {
	HeapAllocBytes uint64 `json:"heapAllocBytes"`
	HeapSysBytes   uint64 `json:"heapSysBytes"`
	NumGC          uint32 `json:"numGC"`
	Goroutines     int    `json:"goroutines"`
}

type ServiceInstanceStatus struct {
	Name        string                `json:"name"`
	Rooms       int                   `json:"rooms"`
	ActiveRooms int                   `json:"activeRooms"`
	Players     int                   `json:"players"`
	Detached    int                   `json:"detached"`
	Spectators  int                   `json:"spectators"`
	Step        ServiceTimingSnapshot `json:"step"`
	Tick        ServiceTimingSnapshot `json:"tick"`
}

type ServiceReplayStatus struct {
	ID         string `json:"id"`
	Spectators int    `json:"spectators"`
	Paused     bool   `json:"paused"`
	Done       bool   `json:"done"`
	Error      bool   `json:"error"`
}

type ServiceEditorStatus struct {
	World   string `json:"world"`
	Members int    `json:"members"`
}

func newServerMetrics(startedAt time.Time) *serverMetrics {
	return &serverMetrics{
		startedAt: startedAt,
		instances: make(map[string]serviceInstanceTiming),
	}
}

func (m *serverMetrics) recordServerTick(d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	recordServiceTiming(&m.tick, d)
	m.mu.Unlock()
}

func (m *serverMetrics) recordInstanceTick(name string, step, tick time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	timing := m.instances[name]
	recordServiceTiming(&timing.Step, step)
	recordServiceTiming(&timing.Tick, tick)
	m.instances[name] = timing
	m.mu.Unlock()
}

func (m *serverMetrics) forgetInstance(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.instances, name)
	m.mu.Unlock()
}

func recordServiceTiming(t *serviceTiming, d time.Duration) {
	t.Count++
	t.Last = d
	t.Total += d
	if d > t.Max {
		t.Max = d
	}
	t.Samples = append(t.Samples, d)
	if len(t.Samples) > serviceTimingWindow {
		copy(t.Samples, t.Samples[len(t.Samples)-serviceTimingWindow:])
		t.Samples = t.Samples[:serviceTimingWindow]
	}
}

func (m *serverMetrics) snapshot() (time.Time, ServiceTimingSnapshot, map[string]serviceInstanceTiming) {
	if m == nil {
		return time.Time{}, ServiceTimingSnapshot{}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	instances := make(map[string]serviceInstanceTiming, len(m.instances))
	for name, timing := range m.instances {
		timing.Step.Samples = append([]time.Duration(nil), timing.Step.Samples...)
		timing.Tick.Samples = append([]time.Duration(nil), timing.Tick.Samples...)
		instances[name] = timing
	}
	return m.startedAt, snapshotServiceTiming(m.tick), instances
}

func snapshotServiceTiming(t serviceTiming) ServiceTimingSnapshot {
	out := ServiceTimingSnapshot{
		Count:  t.Count,
		LastMS: durationMS(t.Last),
		MaxMS:  durationMS(t.Max),
	}
	if t.Count > 0 {
		out.AvgMS = durationMS(time.Duration(int64(t.Total) / int64(t.Count)))
	}
	if len(t.Samples) > 0 {
		samples := append([]time.Duration(nil), t.Samples...)
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		idx := len(samples) * 95 / 100
		if idx >= len(samples) {
			idx = len(samples) - 1
		}
		out.P95MS = durationMS(samples[idx])
	}
	return out
}

func durationMS(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func (s *WebSocketServer) ServiceStatus() ServiceStatus {
	now := s.clockNow()
	startedAt, serverTick, timingByInstance := s.metrics.snapshot()
	if startedAt.IsZero() {
		startedAt = now
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	status := ServiceStatus{
		Status:        "ok",
		StartedAt:     startedAt.UTC().Format(time.RFC3339),
		UptimeSeconds: int64(now.Sub(startedAt).Seconds()),
		Tick:          serverTick,
		Memory: ServiceMemorySnapshot{
			HeapAllocBytes: mem.HeapAlloc,
			HeapSysBytes:   mem.HeapSys,
			NumGC:          mem.NumGC,
			Goroutines:     runtime.NumGoroutine(),
		},
	}

	s.mu.Lock()
	instances := make([]*WorldInstance, 0, len(s.Instances))
	for _, inst := range s.Instances {
		instances = append(instances, inst)
	}
	replays := make([]*ReplayInstance, 0, len(s.ReplayInstances))
	for _, replay := range s.ReplayInstances {
		replays = append(replays, replay)
	}
	editors := make(map[string]*EditorSession, len(s.EditorWorldSessions))
	for name, session := range s.EditorWorldSessions {
		editors[name] = session
	}
	s.mu.Unlock()

	sort.Slice(instances, func(i, j int) bool { return instances[i].Name < instances[j].Name })
	for _, inst := range instances {
		inst.mu.Lock()
		rooms, activeRooms := roomManagerCounts(inst.RoomManager)
		timing := timingByInstance[inst.Name]
		item := ServiceInstanceStatus{
			Name:        inst.Name,
			Rooms:       rooms,
			ActiveRooms: activeRooms,
			Players:     len(inst.Clients),
			Detached:    len(inst.Detached),
			Spectators:  len(inst.Spectators),
			Step:        snapshotServiceTiming(timing.Step),
			Tick:        snapshotServiceTiming(timing.Tick),
		}
		inst.mu.Unlock()
		status.Instances = append(status.Instances, item)
		status.Totals.Rooms += item.Rooms
		status.Totals.ActiveRooms += item.ActiveRooms
		status.Totals.Players += item.Players
		status.Totals.Detached += item.Detached
		status.Totals.Spectators += item.Spectators
	}
	status.Totals.Instances = len(status.Instances)

	sort.Slice(replays, func(i, j int) bool { return replays[i].ID < replays[j].ID })
	for _, replay := range replays {
		replay.mu.Lock()
		item := ServiceReplayStatus{
			ID:         replay.ID,
			Spectators: len(replay.Spectators),
			Paused:     replay.Paused,
			Done:       replay.done,
			Error:      replay.err != nil,
		}
		replay.mu.Unlock()
		status.Replays = append(status.Replays, item)
		status.Totals.Spectators += item.Spectators
	}
	status.Totals.Replays = len(status.Replays)

	editorNames := make([]string, 0, len(editors))
	for name := range editors {
		editorNames = append(editorNames, name)
	}
	sort.Strings(editorNames)
	for _, name := range editorNames {
		session := editors[name]
		if session == nil {
			continue
		}
		members := session.MemberCount()
		if members == 0 {
			continue
		}
		status.Editors = append(status.Editors, ServiceEditorStatus{World: name, Members: members})
		status.Totals.Editors += members
	}
	return status
}

func roomManagerCounts(rm *RoomManager) (rooms, activeRooms int) {
	if rm == nil {
		return 0, 0
	}
	rooms = len(rm.rooms)
	for _, room := range rm.rooms {
		if room != nil && len(room.players) > 0 {
			activeRooms++
		}
	}
	return rooms, activeRooms
}
