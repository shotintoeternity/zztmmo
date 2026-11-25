package zztgo

// M38.7: an assisted run says so where it is watched. The judge's recording
// carries the division in its header, and a replay of it says so on its
// arrival snapshot; a live run's says nothing.

import (
	"net/http"
	"testing"

	"nhooyr.io/websocket"
)

func TestM387AnAssistedReplaySaysSo(t *testing.T) {
	m := m384Setup(t)
	m.h.server.ReplayDir = m.h.recordDir
	cookie := signedAuthCookie(t, m.h.server.Auth, m321Bo)
	status, got := m.post(t, m381Encode(t, m.script), cookie, "")
	if status != http.StatusOK || !got.Met {
		t.Fatalf("submit = %d %+v", status, got)
	}
	conn, snap := m223WatchReplay(t, m.h.ctx, m.h.wsURL, got.RecordingID)
	conn.Close(websocket.StatusNormalClosure, "")
	if snap.ReplayDivision != ChallengeDivisionAssisted {
		t.Fatalf("an assisted replay's snapshot says division %q", snap.ReplayDivision)
	}

	live := m.h.server.ChallengeStore.Leaderboard(gemDashFixture().ID, m321Ada.ID)
	if len(live) != 1 {
		t.Fatalf("live board = %+v", live)
	}
	conn, snap = m223WatchReplay(t, m.h.ctx, m.h.wsURL, live[0].RecordingID)
	conn.Close(websocket.StatusNormalClosure, "")
	if snap.ReplayDivision != "" {
		t.Fatalf("a live replay's snapshot says division %q", snap.ReplayDivision)
	}
}
