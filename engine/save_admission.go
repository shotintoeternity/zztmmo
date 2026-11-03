package zztgo

import (
	"sync"
	"time"
)

// A player may write at most saveRateLimitMax saves in any rolling
// saveRateLimitWindow (M36.17). Each save serializes the whole world and puts a
// file on disk, so without a bound a client answering prompt after prompt
// fills the disk. Three a minute is more than a person saving by hand needs.
const (
	saveRateLimitMax    = 3
	saveRateLimitWindow = time.Minute
)

// saveRateLimiter is the rolling save window per player, on the injected
// server clock. It has its own lock and is never held with inst.mu.
//
// Unlike chat's window it is not forgotten when a read loop exits: a player
// reconnecting keeps their id, and a reconnect must not buy a fresh burst.
// Instead an id whose saves have all expired is dropped on the next allow, so
// the map holds only players who saved within the last window.
type saveRateLimiter struct {
	mu       sync.Mutex
	accepted map[PlayerID][]time.Time
}

// allow reports whether id may save at now, and records the save when it may.
func (l *saveRateLimiter) allow(id PlayerID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.accepted == nil {
		l.accepted = make(map[PlayerID][]time.Time)
	}
	for other, times := range l.accepted {
		kept := times[:0]
		for _, t := range times {
			if now.Sub(t) < saveRateLimitWindow {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.accepted, other)
		} else {
			l.accepted[other] = kept
		}
	}
	if len(l.accepted[id]) >= saveRateLimitMax {
		return false
	}
	l.accepted[id] = append(l.accepted[id], now)
	return true
}
