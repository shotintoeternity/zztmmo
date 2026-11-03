package zztgo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// M36.20: the post-login redirect stays on this site. Each query is sent
// already encoded, exactly as an attacker's link would carry it, and walked
// through start and callback so the assertion is on the Location the browser
// is finally sent to.
func TestM3620PostLoginRedirectStaysOnThisSite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rawQuery string
		want     string
	}{
		{"a backslash after the slash", `return=/\evil.example`, "/"},
		{"an encoded backslash", "return=/%5Cevil.example", "/"},
		{"a lower-case encoded backslash", "return=/%5cevil.example", "/"},
		{"a backslash pair", "return=%5C%5Cevil.example", "/"},
		{"slash then encoded backslash pair", "return=/%5C%5Cevil.example/play", "/"},
		{"an encoded tab browsers strip", "return=/%09/evil.example", "/"},
		{"an encoded newline browsers strip", "return=/%0A/evil.example", "/"},
		{"an encoded protocol-relative URL", "return=%2F%2Fevil.example", "/"},
		{"a backslash later in the path", "return=/play/%5Cevil", "/"},
		// Still a path on this site: a double-encoded backslash arrives as the
		// literal "%5C", which browsers do not turn back into a separator.
		{"a double-encoded backslash stays a path", "return=/%255Cevil.example", "/%5Cevil.example"},
		{"a deep link still works", "return=/play/TOWN", "/play/TOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			auth := NewAuthService("client-id", "client-secret", "http://example.test/api/auth/google/callback", []byte("m36-20-cookie-secret"))
			auth.Now = func() time.Time { return now }
			auth.Verifier = fakeIDTokenVerifier{
				token:   "id-token",
				account: AuthenticatedAccount{ID: "google:m3620", Email: "r@example.test", Name: "R"},
			}
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "id-token"})
			}))
			defer tokenServer.Close()
			auth.TokenEndpoint = tokenServer.URL

			startReq := httptest.NewRequest(http.MethodGet, "/api/auth/google/start", nil)
			startReq.URL.RawQuery = tc.rawQuery
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
