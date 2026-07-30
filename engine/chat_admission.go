package zztgo

// Chat admission (M16.16a): every chat message is normalized and rate-limited
// before it is persisted or broadcast. Refusals create no chat record and no
// broadcast. All state here is server-side presentation/service state — it
// never touches the simulation or the replay hash.

import (
	"strings"
	"sync"
	"time"
)

const (
	// chatMaxTextBytes caps an admitted message at 120 printable CP437 bytes.
	chatMaxTextBytes = 120
	// chatRateLimitMax admitted messages per player in any rolling
	// chatRateLimitWindow; the sixth in-window message is refused.
	chatRateLimitMax    = 5
	chatRateLimitWindow = 10 * time.Second
)

// admitChatText normalizes raw client text to at most chatMaxTextBytes
// printable CP437 bytes, or refuses it (ok=false) if nothing printable
// survives. It shares foldWordmark's byte space: printable ASCII (a subset of
// CP437) passes through, common typographic punctuation folds to its ASCII
// equivalent, and control or unmappable runes are dropped. Extended CP437
// glyphs are deliberately NOT admitted: the client renders chat with
// charCodeAt()&0xff against the CP437 atlas, so only runes whose codepoint
// equals their CP437 byte — printable ASCII — display as the sender intended.
func admitChatText(raw string) (string, bool) {
	text := strings.TrimSpace(foldWordmark(raw))
	if len(text) > chatMaxTextBytes {
		// All surviving bytes are single-byte ASCII, so a byte slice cannot
		// split a rune. Re-trim in case truncation exposed trailing spaces.
		text = strings.TrimSpace(text[:chatMaxTextBytes])
	}
	if text == "" {
		return "", false
	}
	return text, true
}

// chatRateLimiter admits at most chatRateLimitMax messages per player in any
// rolling chatRateLimitWindow, measured on the injected server clock. Only
// admitted messages count toward the window; refused ones (rate or
// normalization) never do.
type chatRateLimiter struct {
	mu       sync.Mutex
	accepted map[PlayerID][]time.Time
}

// allow reports whether a message from id may be admitted at now, and records
// it when admitted. An accepted timestamp expires once now-t >= window, so the
// boundary message exactly window after the first is admitted.
func (l *chatRateLimiter) allow(id PlayerID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.accepted[id][:0]
	for _, t := range l.accepted[id] {
		if now.Sub(t) < chatRateLimitWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= chatRateLimitMax {
		l.accepted[id] = kept
		return false
	}
	if l.accepted == nil {
		l.accepted = make(map[PlayerID][]time.Time)
	}
	l.accepted[id] = append(kept, now)
	return true
}

// forget drops a player's window state; called when their read loop exits so
// the map only holds players with a live connection.
func (l *chatRateLimiter) forget(id PlayerID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.accepted, id)
}
