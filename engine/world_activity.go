package zztgo

import (
	"encoding/json"
	"os"
	"sync"
)

// WorldActivityStore is M27.1's deliberately narrow popularity store. It keeps
// only aggregate play counts by canonical world identity: no account, no IP, no
// connection id, and no timestamped player log.
type WorldActivityStore struct {
	mu     sync.Mutex
	path   string
	counts map[string]int
}

func NewWorldActivityStore(path string) (*WorldActivityStore, error) {
	store := &WorldActivityStore{path: path, counts: make(map[string]int)}
	if path == "" {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(data, &store.counts)
	if store.counts == nil {
		store.counts = make(map[string]int)
	}
	for world, count := range store.counts {
		safe, err := SanitizeSaveName(world)
		if err != nil || count <= 0 {
			delete(store.counts, world)
			continue
		}
		if safe != world {
			store.counts[safe] += count
			delete(store.counts, world)
		}
	}
	return store, nil
}

func (s *WorldActivityStore) IncrementPlay(world string) error {
	if s == nil {
		return nil
	}
	safe, err := SanitizeSaveName(world)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts == nil {
		s.counts = make(map[string]int)
	}
	s.counts[safe]++
	return s.writeLocked()
}

func (s *WorldActivityStore) Counts() map[string]int {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.counts))
	for world, count := range s.counts {
		out[world] = count
	}
	return out
}

func (s *WorldActivityStore) writeLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.counts, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
