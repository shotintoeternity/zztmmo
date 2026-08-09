package zztgo

import (
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	watchLiveMaxLineupEntries  = 8
	watchLiveReplayScanLimit   = 32
	watchLiveReplayTickScanCap = 600
)

type WatchLiveLineupEntry struct {
	Kind      string `json:"kind"`
	World     string `json:"world,omitempty"`
	ReplayID  string `json:"replayId,omitempty"`
	Title     string `json:"title"`
	Path      string `json:"path"`
	Board     int16  `json:"board,omitempty"`
	Players   int    `json:"players,omitempty"`
	Watchers  int    `json:"watchers,omitempty"`
	StartTick int    `json:"startTick,omitempty"`
	Ticks     int    `json:"ticks,omitempty"`
}

func (a *WebAPI) handleWatchLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if a.Server == nil {
		http.Error(w, "watch live is unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, struct {
		Entries []WatchLiveLineupEntry `json:"entries"`
	}{Entries: a.WatchLiveLineup()})
}

func (a *WebAPI) WatchLiveLineup() []WatchLiveLineupEntry {
	if a == nil || a.Server == nil {
		return nil
	}
	dir, worlds := a.worldDirectoryAndNames()
	lineup := a.watchLiveRooms(dir, worlds)
	if len(lineup) < watchLiveMaxLineupEntries {
		lineup = append(lineup, a.watchLiveReplays(watchLiveMaxLineupEntries-len(lineup))...)
	}
	if len(lineup) > watchLiveMaxLineupEntries {
		lineup = lineup[:watchLiveMaxLineupEntries]
	}
	return lineup
}

func (a *WebAPI) watchLiveRooms(dir string, worlds []string) []WatchLiveLineupEntry {
	entries := make([]WatchLiveLineupEntry, 0, len(worlds))
	for _, world := range worlds {
		if _, err := LoadPristineWorld(dir, world); err != nil {
			continue
		}
		players, watchers, board := a.watchLiveRoomCounts(world)
		if players <= 0 {
			continue
		}
		title := world
		if meta, ok := museumMetadataForWorld(world); ok && meta.Title != "" {
			title = meta.Title
		} else if meta, _, err := loadWorldMeta(dir, world); err == nil && meta.Title != "" {
			title = meta.Title
		}
		entries = append(entries, WatchLiveLineupEntry{
			Kind:     "live",
			World:    world,
			Title:    title,
			Path:     "/watch/" + world,
			Board:    board,
			Players:  players,
			Watchers: watchers,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Players != entries[j].Players {
			return entries[i].Players > entries[j].Players
		}
		return entries[i].World < entries[j].World
	})
	return entries
}

func (a *WebAPI) watchLiveRoomCounts(world string) (players, watchers int, board int16) {
	if a == nil || a.Server == nil {
		return 0, 0, 0
	}
	a.Server.mu.Lock()
	inst := a.Server.Instances[world]
	if inst == nil {
		a.Server.mu.Unlock()
		return 0, 0, 0
	}
	inst.mu.Lock()
	a.Server.mu.Unlock()
	board = a.Server.resolveJoinBoard(inst, 0)
	players = len(inst.Clients)
	watchers = inst.watcherCountsLocked()[board]
	inst.mu.Unlock()
	return players, watchers, board
}

func (a *WebAPI) watchLiveReplays(limit int) []WatchLiveLineupEntry {
	if limit <= 0 || a == nil || a.Server == nil || a.Server.ReplayDir == "" {
		return nil
	}
	files, err := os.ReadDir(a.Server.ReplayDir)
	if err != nil {
		return nil
	}
	type replayFile struct {
		name string
		mod  int64
	}
	candidates := make([]replayFile, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, replayFile{name: file.Name(), mod: info.ModTime().UnixNano()})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].mod != candidates[j].mod {
			return candidates[i].mod > candidates[j].mod
		}
		return candidates[i].name < candidates[j].name
	})
	if len(candidates) > watchLiveReplayScanLimit {
		candidates = candidates[:watchLiveReplayScanLimit]
	}

	entries := make([]WatchLiveLineupEntry, 0, limit)
	for _, candidate := range candidates {
		id, err := sanitizeReplayID(candidate.name)
		if err != nil {
			continue
		}
		entry, ok := a.watchLiveReplayEntry(id)
		if !ok {
			continue
		}
		entries = append(entries, entry)
		if len(entries) >= limit {
			break
		}
	}
	return entries
}

func (a *WebAPI) watchLiveReplayEntry(id string) (WatchLiveLineupEntry, bool) {
	path, err := replayPath(a.Server.ReplayDir, id)
	if err != nil {
		return WatchLiveLineupEntry{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return WatchLiveLineupEntry{}, false
	}
	defer f.Close()
	playback, err := NewReplayPlayback(f)
	if err != nil {
		return WatchLiveLineupEntry{}, false
	}
	world := playback.WorldName()
	if _, err := LoadPristineWorld(a.Server.worldsDir(), world); err != nil {
		return WatchLiveLineupEntry{}, false
	}
	last := -1
	for scanned := 0; scanned < watchLiveReplayTickScanCap; scanned++ {
		tick, _, done, err := playback.Step()
		if err != nil {
			return WatchLiveLineupEntry{}, false
		}
		if done {
			break
		}
		last = tick
	}
	if last < 0 || playback.PlayerCountEver() > 1 {
		return WatchLiveLineupEntry{}, false
	}
	ticks := PostcardDefaultTicks
	start := last - ticks + 1
	if start < 0 {
		start = 0
		ticks = last + 1
	}
	title := "Replay: " + world
	return WatchLiveLineupEntry{
		Kind:      "replay",
		World:     world,
		ReplayID:  id,
		Title:     title,
		Path:      "/replay/" + id,
		Board:     1,
		StartTick: start,
		Ticks:     ticks,
	}, true
}
