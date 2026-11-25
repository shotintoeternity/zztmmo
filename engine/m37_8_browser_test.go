package zztgo

// M37.8's browser half: the launch name box offers Google sign-in on a server
// that has it, by Tab on a keyboard and by the bar's Google button on a phone,
// and a guest who just types a name is untouched
// (web/test/launch_sign_in.test.mjs). It runs on M16.14's harness, whose
// AuthService talks to a hermetic fake Google on the control listener, so the
// redirect chain the box starts is the production one end to end.
//
// M37.10 extends it: the server here requires a username, as zzt-server does
// whenever sign-in is on, so the box back from Google asks for one, refuses a
// malformed one and a taken one, and a later visit skips the box.

import (
	"strings"
	"testing"
)

func TestM378LaunchBoxOffersSignIn(t *testing.T) {
	m169Parallel(t)
	h := m1614NewHarness(t, func(h *m169Harness, server *WebSocketServer, api *WebAPI) {
		server.ChatDB = NewMemChatDatabase()
		server.RequireUsername = true
	})
	out := h.runBrowserScript("launch_sign_in.test.mjs")
	if !strings.Contains(out, "launch_sign_in.test.mjs: M37.8 passed") {
		t.Fatalf("launch_sign_in.test.mjs did not report its pass line:\n%s", out)
	}
	t.Logf("browser output:\n%s", out)
}
