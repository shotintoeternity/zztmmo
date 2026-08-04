package zztgo

// M20.1 — the one server-side claim the deep link makes.
//
// The client round-trips window.location.pathname through the OAuth start
// endpoint (main.ts handleTitleKey's "login" case), so signing in from a
// deep-linked title screen now sends a `/play/<world>` return path where it used
// to send `/`. That path travels inside the signed state cookie and comes back
// out of HandleCallback's redirect, and nothing else in the tree asserts it
// survives — TestM62GoogleOAuthCallbackSetsSignedSession checks the session
// cookie, not where the callback sends the browser.
//
// The rest of M20.1 is client-side and is proved in the browser
// (TestM201DeepLinkJourney) and under Node (web/test/deep_link.test.mjs).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestM201SPAFileServerServesTheAppForADeepLink pins the property that makes
// /play/<world> a URL at all: the path is not a file and never will be, so the
// file server has to answer it with the app. Everyday-run coverage on purpose —
// the browser suite proves the same thing end to end, but it is opt-in, and a
// deep link that 404s is the kind of break that should redden the fast gate.
func TestM201SPAFileServerServesTheAppForADeepLink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<canvas data-screen></canvas>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("// the bundle"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}

	server := httptest.NewServer(SPAFileServer(http.Dir(dir)))
	defer server.Close()

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/", "<canvas"},
		{"/play/TOWN", "<canvas"},
		{"/play/town", "<canvas"},
		// A name with a slash in it is still not a file: it must reach the app,
		// which refuses it in a window, rather than 404 in the browser's own UI.
		{"/play/TOWN/1", "<canvas"},
		// A real file is still served as itself, or the fallback would swallow
		// the bundle and every deep link would render a blank page.
		{"/assets/app.js", "the bundle"},
	} {
		resp, err := http.Get(server.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status=%d, want 200", tc.path, resp.StatusCode)
			continue
		}
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("GET %s served %q, want it to contain %q", tc.path, body, tc.want)
		}
	}
}

func TestM201SignInReturnsToTheDeepLinkedTitleScreen(t *testing.T) {
	// A deep link is a path with a segment, which is the case that matters: the
	// guard HandleStart applies is prefix-based, so "/play/TOWN" and the hostile
	// "//evil.test/play" differ by one character.
	for _, tc := range []struct {
		name     string
		returnTo string
		want     string
	}{
		{"a deep-linked title screen", "/play/TOWN", "/play/TOWN"},
		{"an escaped world name", "/play/MY%20WORLD", "/play/MY%20WORLD"},
		{"the app root", "/", "/"},
		{"a protocol-relative URL is not a path", "//evil.test/play/TOWN", "/"},
		{"an absolute URL is not a path", "https://evil.test/play/TOWN", "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			auth := NewAuthService("client-id", "client-secret", "http://example.test/api/auth/google/callback", []byte("m20-1-cookie-secret"))
			auth.Now = func() time.Time { return now }
			auth.Verifier = fakeIDTokenVerifier{
				token:   "id-token",
				account: AuthenticatedAccount{ID: "google:m201", Email: "linker@example.test", Name: "Linker"},
			}
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "id-token"})
			}))
			defer tokenServer.Close()
			auth.TokenEndpoint = tokenServer.URL

			startReq := httptest.NewRequest(http.MethodGet, "/api/auth/google/start?return="+url.QueryEscape(tc.returnTo), nil)
			startRec := httptest.NewRecorder()
			auth.HandleStart(startRec, startReq)
			if startRec.Code != http.StatusFound {
				t.Fatalf("start status=%d, want %d", startRec.Code, http.StatusFound)
			}
			var stateCookie *http.Cookie
			for _, cookie := range startRec.Result().Cookies() {
				if cookie.Name == authStateCookie {
					stateCookie = cookie
				}
			}
			if stateCookie == nil {
				t.Fatal("start did not set the OAuth state cookie")
			}
			authURL, err := url.Parse(startRec.Result().Header.Get("Location"))
			if err != nil {
				t.Fatalf("parse redirect: %v", err)
			}

			callbackReq := httptest.NewRequest(http.MethodGet,
				"/api/auth/google/callback?code=abc&state="+url.QueryEscape(authURL.Query().Get("state")), nil)
			callbackReq.AddCookie(stateCookie)
			callbackRec := httptest.NewRecorder()
			auth.HandleCallback(callbackRec, callbackReq)
			if callbackRec.Code != http.StatusFound {
				t.Fatalf("callback status=%d body=%q", callbackRec.Code, callbackRec.Body.String())
			}
			if got := callbackRec.Result().Header.Get("Location"); got != tc.want {
				t.Errorf("callback sent the browser to %q, want %q", got, tc.want)
			}
		})
	}
}
