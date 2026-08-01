package zztgo

// M16.16 — the auth, chat, and Museum service journey.
//
// WHAT THIS IS. Everything a stranger touches on their way in — Google sign-in,
// the identity their chat carries, the world picker, and the Museum of ZZT
// import — driven through the SAME mux cmd/zzt-server mounts (`/ws` on the
// WebSocketServer, `/api/` on the WebAPI) against two hermetic fakes: an
// identity provider and a Museum, both served by this test binary. Nothing here
// reaches accounts.google.com or museumofzzt.com, and nothing is injected past
// a handler: the session cookie a test uses is the one HandleCallback set, and
// the archive bytes a world is hosted from arrive over HTTP.
//
// WHY THE FAKES ARE CHECKERS, NOT RUBBER STAMPS. An identity provider that
// hands out a token for any request would let a broken PKCE implementation
// certify itself. m1616IdP refuses an authorize call without the harness client
// id or an S256 challenge, remembers the challenge each code was bound to, and
// its token endpoint refuses a verifier that does not hash to it. The Museum
// fake counts every request it serves, per field and per archive, so "the
// second Play was a cache hit" and "the refused download never left the
// process" are counted rather than assumed.
//
// THE THREE THINGS EVERY REFUSAL ROW ASSERTS. A security refusal is only
// certified if it changed nothing: no session cookie, no cache entry, no hosted
// .ZZT, no new instance, and — for the auth rows — a WebSocket join carrying
// whatever cookie the refusal left behind is still a guest. Those are checked
// from the server's own maps and from the filesystem, never from the response
// body alone.
//
// WHAT IS DELIBERATELY NOT REPEATED HERE. M16.16a already pins the chat
// admission boundary (120 printable CP437 bytes, the rolling 5-per-10s window)
// and the Museum post-validation cache commit at the service level, with the
// clock injected. This file certifies the halves those tests do not reach: the
// identity a chat line is attributed to, the routes in front of the service,
// and the browser journey (TestM1616BrowserAuthAndMuseumJourney).

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// ---------------------------------------------------------------------------
// The hermetic identity provider
// ---------------------------------------------------------------------------

const (
	m1616ClientID     = "zztmmo-m1616-client"
	m1616ClientSecret = "zztmmo-m1616-secret"
	// m1616BadToken is issued for m1616BadCode: a well-formed exchange whose
	// id_token the verifier rejects, which is the only way to reach
	// HandleCallback's 401 branch without breaking the exchange itself.
	m1616BadCode  = "code-unverifiable"
	m1616BadToken = "token-unverifiable"
)

// m1616Account is one fake person the provider can sign in.
type m1616Account struct {
	Key   string
	ID    string
	Email string
	Name  string
	Code  string
	Token string
}

var m1616Accounts = []m1616Account{
	{Key: "ada", ID: "google:m1616-ada", Email: "ada@example.test", Name: "Ada Lovelace", Code: "code-m1616-ada", Token: "token-m1616-ada"},
	{Key: "bob", ID: "google:m1616-bob", Email: "bob@example.test", Name: "Bob Bones", Code: "code-m1616-bob", Token: "token-m1616-bob"},
}

func m1616AccountByKey(t *testing.T, key string) m1616Account {
	t.Helper()
	for _, account := range m1616Accounts {
		if account.Key == key {
			return account
		}
	}
	t.Fatalf("unknown fake account %q", key)
	return m1616Account{}
}

// m1616Verifier maps an id_token the fake token endpoint issued back to its
// account. Anything else — including m1616BadToken — is refused, so a callback
// that skipped or faked the exchange cannot authenticate.
type m1616Verifier struct{}

func (m1616Verifier) VerifyIDToken(ctx context.Context, token, clientID string) (AuthenticatedAccount, error) {
	if clientID != m1616ClientID {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	for _, account := range m1616Accounts {
		if account.Token == token {
			return AuthenticatedAccount{ID: account.ID, Email: account.Email, Name: account.Name}, nil
		}
	}
	return AuthenticatedAccount{}, ErrInvalidAuth
}

// m1616IdP is the provider's state: who the next authorize call signs in, the
// PKCE challenge each issued code was bound to, and what it was asked.
type m1616IdP struct {
	mu         sync.Mutex
	next       string
	challenges map[string]string
	authorizes []url.Values
	tokenForms []url.Values
	server     *httptest.Server
}

func m1616NewIdP(t *testing.T) *m1616IdP {
	t.Helper()
	idp := &m1616IdP{challenges: map[string]string{}, next: "ada"}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", idp.handleAuthorize)
	mux.HandleFunc("/token", idp.handleToken)
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (p *m1616IdP) signInNext(key string) {
	p.mu.Lock()
	p.next = key
	p.mu.Unlock()
}

func (p *m1616IdP) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	p.mu.Lock()
	p.authorizes = append(p.authorizes, query)
	next := p.next
	p.mu.Unlock()

	if query.Get("client_id") != m1616ClientID {
		http.Error(w, "wrong client_id", http.StatusBadRequest)
		return
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		http.Error(w, "missing PKCE challenge", http.StatusBadRequest)
		return
	}
	redirect, state := query.Get("redirect_uri"), query.Get("state")
	if redirect == "" || state == "" {
		http.Error(w, "missing redirect_uri or state", http.StatusBadRequest)
		return
	}
	code := m1616BadCode
	for _, account := range m1616Accounts {
		if account.Key == next {
			code = account.Code
		}
	}
	p.mu.Lock()
	p.challenges[code] = query.Get("code_challenge")
	p.mu.Unlock()
	http.Redirect(w, r, redirect+"?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), http.StatusFound)
}

func (p *m1616IdP) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.tokenForms = append(p.tokenForms, r.Form)
	challenge, known := p.challenges[r.Form.Get("code")]
	p.mu.Unlock()

	if r.Form.Get("grant_type") != "authorization_code" ||
		r.Form.Get("client_id") != m1616ClientID ||
		r.Form.Get("client_secret") != m1616ClientSecret {
		writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	if !known {
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		writeJSON(w, map[string]string{"error": "invalid_grant", "error_description": "PKCE verifier does not match"})
		return
	}
	code := r.Form.Get("code")
	if code == m1616BadCode {
		// A real exchange that yields an id_token nobody can verify.
		writeJSON(w, map[string]string{"id_token": m1616BadToken})
		return
	}
	for _, account := range m1616Accounts {
		if account.Code == code {
			writeJSON(w, map[string]string{"id_token": account.Token})
			return
		}
	}
	writeJSON(w, map[string]string{"error": "invalid_grant"})
}

func (p *m1616IdP) tokenCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.tokenForms)
}

// ---------------------------------------------------------------------------
// The hermetic Museum of ZZT
// ---------------------------------------------------------------------------

// m1616MuseumFake answers both halves of the Museum API the service uses: the
// per-field file search and the zgames archive download. It counts what it was
// asked so the tests can prove a cache hit, or prove a refused request never
// reached the network at all.
type m1616MuseumFake struct {
	mu sync.Mutex
	// archives is keyed "<letter>/<filename>"; a missing key answers 404.
	archives map[string][]byte
	// results is keyed by search field ("title", "author", "filename", "genre",
	// "year") and holds the raw result objects that field's query returns.
	results map[string][]string
	// searchStatus, when non-zero, is returned instead of a result set — the
	// Museum being down.
	searchStatus int

	searchFields []string
	searchTerms  []string
	downloads    []string

	server *httptest.Server
}

func m1616NewMuseumFake(t *testing.T) *m1616MuseumFake {
	t.Helper()
	fake := &m1616MuseumFake{
		archives: map[string][]byte{},
		results:  map[string][]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/search/files", fake.handleSearch)
	mux.HandleFunc("/zgames/", fake.handleDownload)
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (m *m1616MuseumFake) handleSearch(w http.ResponseWriter, r *http.Request) {
	field, term := "", ""
	for _, name := range []string{"title", "author", "filename", "genre", "year"} {
		if value := r.URL.Query().Get(name); value != "" {
			field, term = name, value
		}
	}
	m.mu.Lock()
	m.searchFields = append(m.searchFields, field)
	m.searchTerms = append(m.searchTerms, term)
	status := m.searchStatus
	results := m.results[field]
	m.mu.Unlock()

	if status != 0 {
		http.Error(w, "the Museum is unwell", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"SUCCESS","count":%d,"data":{"results":[%s]}}`,
		len(results), strings.Join(results, ","))
}

func (m *m1616MuseumFake) handleDownload(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/zgames/")
	m.mu.Lock()
	m.downloads = append(m.downloads, key)
	data, ok := m.archives[key]
	m.mu.Unlock()
	if !ok {
		http.Error(w, "no such archive", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	_, _ = w.Write(data)
}

func (m *m1616MuseumFake) counts() (searches, downloads int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.searchFields), len(m.downloads)
}

func (m *m1616MuseumFake) fieldsAsked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	fields := append([]string(nil), m.searchFields...)
	sort.Strings(fields)
	return fields
}

// m1616Result renders one search hit in the Museum's own JSON shape.
func m1616Result(letter, filename, title, author, released, archive string) string {
	return fmt.Sprintf(`{"letter":%q,"filename":%q,"title":%q,"author":[%q],"release_date":%q,"archive_name":%q,"genres":["Adventure"],"playable_boards":12,"total_boards":20}`,
		letter, filename, title, author, released, archive)
}

// ---------------------------------------------------------------------------
// The service under test: the production mux, with both fakes wired in
// ---------------------------------------------------------------------------

const m1616World = "M1616"

type m1616Service struct {
	t         *testing.T
	server    *WebSocketServer
	api       *WebAPI
	app       *httptest.Server
	museum    *MuseumService
	auth      *AuthService
	idp       *m1616IdP
	fake      *m1616MuseumFake
	chatDB    *MemChatDatabase
	rootDir   string
	worldsDir string
	savesDir  string
	cacheDir  string
}

// m1616NewService stands up the same objects cmd/zzt-server's main() does, with
// the Museum and identity provider replaced by the fakes above. Auth is enabled
// unless withAuth is false, which is how the guest-only server is certified.
func m1616NewService(t *testing.T, withAuth bool) *m1616Service {
	t.Helper()

	rootDir := t.TempDir()
	worldsDir := filepath.Join(rootDir, "worlds")
	savesDir := filepath.Join(rootDir, "saves")
	cacheDir := filepath.Join(worldsDir, ".museum-cache")
	for _, dir := range []string{worldsDir, savesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	world := m1616TestWorld(t)
	m1616WriteWorldFile(t, world, filepath.Join(worldsDir, m1616World+".ZZT"))

	server := NewWebSocketServer(world, 1)
	server.WorldsDir = worldsDir
	server.SavesDir = savesDir
	server.RoomManager.HighScorePath = filepath.Join(rootDir, m1616World+".HI")
	chatDB := NewMemChatDatabase()
	server.ChatDB = chatDB

	fake := m1616NewMuseumFake(t)
	museum := NewMuseumService(server)
	museum.APIBaseURL = fake.server.URL + "/api/v1"
	museum.FilesBaseURL = fake.server.URL + "/zgames"
	museum.Client = fake.server.Client()
	museum.CacheDir = cacheDir
	// The Museum's own politeness delay is wall-clock pacing between outbound
	// requests, not behavior under test; start the clock already elapsed so the
	// first request does not sit out 300ms for nothing.
	museum.lastRequest = time.Now().Add(-museumRequestDelay)

	svc := &m1616Service{
		t: t, server: server, museum: museum, fake: fake, chatDB: chatDB,
		rootDir: rootDir, worldsDir: worldsDir, savesDir: savesDir, cacheDir: cacheDir,
	}

	if withAuth {
		idp := m1616NewIdP(t)
		auth := NewAuthService(m1616ClientID, m1616ClientSecret, "", []byte("m16-16-cookie-secret"))
		auth.Verifier = m1616Verifier{}
		auth.AuthEndpoint = idp.server.URL + "/authorize"
		auth.TokenEndpoint = idp.server.URL + "/token"
		server.Auth = auth
		svc.auth = auth
		svc.idp = idp
	}

	api := &WebAPI{
		RoomManager: server.RoomManager,
		World:       world,
		SavesDir:    savesDir,
		Server:      server,
		Museum:      museum,
		Auth:        server.Auth,
	}
	svc.api = api

	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	mux.Handle("/api/", api.Handler())
	// The redirect a completed sign-in lands on. cmd/zzt-server serves the built
	// client here; a 200 is all this file needs, and it keeps the redirect chain
	// from ending in a 404 that would read like a failure.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("client"))
	})

	svc.app = httptest.NewServer(mux)
	t.Cleanup(svc.app.Close)
	t.Cleanup(server.CloseRecorders)
	return svc
}

func (s *m1616Service) wsURL() string {
	return "ws" + strings.TrimPrefix(s.app.URL, "http") + "/ws"
}

// browser is an http.Client with a cookie jar: it keeps the session cookie the
// callback sets and follows redirects, which is what makes the sign-in test a
// journey rather than four disconnected handler calls.
func (s *m1616Service) browser() *http.Client {
	s.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 20 * time.Second}
}

// noRedirect is a client that stops at the first hop, for the tests that need to
// read a Location header or a Set-Cookie rather than follow it.
func (s *m1616Service) noRedirect() *http.Client {
	return &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type m1616Me struct {
	Enabled       bool   `json:"enabled"`
	Authenticated bool   `json:"authenticated"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
}

func (s *m1616Service) me(client *http.Client, cookies ...*http.Cookie) m1616Me {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.app.URL+"/api/auth/me", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		s.t.Fatalf("GET /api/auth/me: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("GET /api/auth/me status=%d", resp.StatusCode)
	}
	var me m1616Me
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		s.t.Fatalf("decode /api/auth/me: %v", err)
	}
	return me
}

// sessionCookie returns the session cookie the jar is holding, or nil.
func (s *m1616Service) sessionCookie(client *http.Client) *http.Cookie {
	s.t.Helper()
	appURL, err := url.Parse(s.app.URL)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(appURL) {
		if cookie.Name == authSessionCookie {
			return cookie
		}
	}
	return nil
}

// joinIdentity opens a real WebSocket, joins, and reports the identity the
// server gave that connection. It is the authority behind every "signed in as"
// and "still a guest" claim in this file.
func (s *m1616Service) joinIdentity(ctx context.Context, name string, cookie *http.Cookie) (accountID, playerName string, id PlayerID, conn *websocket.Conn) {
	s.t.Helper()
	conn, snapshot := dialJoinWithCookie(s.t, ctx, s.wsURL(), JoinMessage{Type: MessageTypeJoin, Name: name, Board: 1}, cookie)
	inst := s.server.DefaultInstance
	inst.mu.Lock()
	accountID, playerName, _ = inst.RoomManager.PlayerIdentity(snapshot.You.ID)
	inst.mu.Unlock()
	return accountID, playerName, snapshot.You.ID, conn
}

// mutationSnapshot is everything a refusal must leave alone.
type m1616Mutations struct {
	worldFiles int
	saveFiles  int
	cacheFiles int
	instances  int
	chatRecs   int
}

func (s *m1616Service) mutations() m1616Mutations {
	s.t.Helper()
	s.server.mu.Lock()
	instances := len(s.server.Instances)
	s.server.mu.Unlock()
	recs, err := s.chatDB.GetRecentMessages(1000)
	if err != nil {
		s.t.Fatalf("GetRecentMessages: %v", err)
	}
	return m1616Mutations{
		worldFiles: countFilesUnder(s.t, s.worldsDir),
		saveFiles:  countFilesUnder(s.t, s.savesDir),
		cacheFiles: countFilesUnder(s.t, s.cacheDir),
		instances:  instances,
		chatRecs:   len(recs),
	}
}

// m1616TestWorld is the world the service hosts: a playable board 1, and a
// title board (board 0) carrying a spinning gun so /api/title/stream has frames
// to push. A still title board would make that route look dead when it is only
// quiet.
func m1616TestWorld(t *testing.T) TWorld {
	t.Helper()
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.World.Info.Name = m1616World
	setup.World.Info.CurrentBoard = 1
	setup.World.BoardCount = 1
	setup.BoardCreate()
	for ix := int16(1); ix <= BOARD_WIDTH; ix++ {
		for iy := int16(1); iy <= BOARD_HEIGHT; iy++ {
			setup.Board.Tiles[ix][iy] = TTile{Element: E_EMPTY}
		}
	}
	setup.Board.Tiles[setup.Board.Stats[0].X][setup.Board.Stats[0].Y] = TTile{Element: E_EMPTY}
	setup.Board.StatCount = -1
	setup.Board.Info.StartPlayerX = 10
	setup.Board.Info.StartPlayerY = 12
	setup.Board.Name = "Arrivals"
	setup.BoardClose()

	// Clear of (30,12): BoardCreate puts stat 0 there, and the title engine
	// paints E_MONITOR over stat 0's tile (GAME.PAS:1604). A gun under the
	// monitor would redraw the monitor's unchanging glyph and the stream would
	// look dead.
	setup.BoardOpen(0)
	setup.Board.Tiles[20][10] = TTile{Element: E_SPINNING_GUN, Color: 0x0F}
	setup.AddStat(20, 10, E_SPINNING_GUN, 0x0F, 1, StatTemplateDefault)
	setup.BoardClose()

	setup.World.Info.CurrentBoard = 1
	return setup.World
}

func m1616WriteWorldFile(t *testing.T, world TWorld, path string) {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(1)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := e.worldWriteTo(f); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// Auth: the sign-in journey
// ---------------------------------------------------------------------------

// TestM1616SignInJourneyThroughTheRealServer walks the whole OIDC round trip on
// the production routes with a cookie jar, and then proves the thing sign-in is
// FOR: the identity a WebSocket join is given. A guest is whoever they typed; a
// signed-in player is their Google display name, whatever they typed.
func TestM1616SignInJourneyThroughTheRealServer(t *testing.T) {
	svc := m1616NewService(t, true)
	ada := m1616AccountByKey(t, "ada")
	svc.idp.signInNext(ada.Key)

	client := svc.browser()

	// Before: the client is told auth exists and that nobody is signed in.
	if me := svc.me(client); !me.Enabled || me.Authenticated {
		t.Fatalf("/api/auth/me before sign-in = %+v, want enabled and unauthenticated", me)
	}

	// The whole redirect chain: start → the provider → back through the real
	// callback → the return path. Nothing is injected; the jar carries the state
	// cookie to the callback exactly as a browser would.
	resp, err := client.Get(svc.app.URL + "/api/auth/google/start?return=/play")
	if err != nil {
		t.Fatalf("GET /api/auth/google/start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in chain ended with status %d", resp.StatusCode)
	}
	if got := resp.Request.URL.Path; got != "/play" {
		t.Fatalf("sign-in landed on %q, want the /play the start call asked to return to", got)
	}

	// The provider is a checker: it only redirected because the request carried
	// this server's client id and a real S256 challenge. Assert what it saw.
	svc.idp.mu.Lock()
	authorizes := append([]url.Values(nil), svc.idp.authorizes...)
	tokenForms := append([]url.Values(nil), svc.idp.tokenForms...)
	svc.idp.mu.Unlock()
	if len(authorizes) != 1 || len(tokenForms) != 1 {
		t.Fatalf("provider saw %d authorize and %d token calls, want one of each", len(authorizes), len(tokenForms))
	}
	authorize := authorizes[0]
	if authorize.Get("response_type") != "code" || authorize.Get("scope") != "openid email profile" {
		t.Errorf("authorize request = %v, want an OIDC code request", authorize)
	}
	if !strings.HasSuffix(authorize.Get("redirect_uri"), "/api/auth/google/callback") {
		t.Errorf("redirect_uri = %q, want this server's callback", authorize.Get("redirect_uri"))
	}
	// PKCE end to end: the verifier the token call sent hashes to the challenge
	// the authorize call declared. The provider enforces this too — this asserts
	// it was exercised rather than skipped.
	sum := sha256.Sum256([]byte(tokenForms[0].Get("code_verifier")))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != authorize.Get("code_challenge") {
		t.Errorf("code_verifier hashes to %q, want the declared challenge %q", got, authorize.Get("code_challenge"))
	}

	session := svc.sessionCookie(client)
	if session == nil {
		t.Fatal("the completed sign-in left no session cookie in the jar")
	}
	me := svc.me(client)
	if !me.Authenticated || me.ID != ada.ID || me.Name != ada.Name || me.Email != ada.Email {
		t.Fatalf("/api/auth/me after sign-in = %+v, want %s/%s/%s", me, ada.ID, ada.Name, ada.Email)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The identity a join is given. The signed-in player deliberately sends a
	// DIFFERENT name: the account wins, or the display name in chat would be
	// whatever the client felt like claiming.
	accountID, name, _, signedIn := svc.joinIdentity(ctx, "not-my-real-name", session)
	defer signedIn.Close(websocket.StatusNormalClosure, "")
	if accountID != ada.ID || name != ada.Name {
		t.Fatalf("signed-in join identity = (%q, %q), want (%q, %q)", accountID, name, ada.ID, ada.Name)
	}

	guestAccount, guestName, _, guest := svc.joinIdentity(ctx, "Guesty", nil)
	defer guest.Close(websocket.StatusNormalClosure, "")
	if guestAccount != "" || guestName != "Guesty" {
		t.Fatalf("guest join identity = (%q, %q), want (\"\", \"Guesty\")", guestAccount, guestName)
	}

	// Signing out is a real logout: the cookie is cleared and the next join is a
	// guest again.
	logoutResp, err := client.Post(svc.app.URL+"/api/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/auth/logout: %v", err)
	}
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status=%d", logoutResp.StatusCode)
	}
	if svc.sessionCookie(client) != nil {
		t.Error("the session cookie survived logout")
	}
	if me := svc.me(client); me.Authenticated {
		t.Fatalf("/api/auth/me after logout = %+v, want unauthenticated", me)
	}
	afterAccount, afterName, _, after := svc.joinIdentity(ctx, "Guesty Again", nil)
	defer after.Close(websocket.StatusNormalClosure, "")
	if afterAccount != "" || afterName != "Guesty Again" {
		t.Fatalf("post-logout join identity = (%q, %q), want a guest", afterAccount, afterName)
	}
}

// m1616BeginAuth runs the start half only, returning the state cookie the
// server set and the `state` value it put in the provider URL. The refusal rows
// below start from a legitimate start call and then break exactly one thing.
func m1616BeginAuth(t *testing.T, svc *m1616Service) (*http.Cookie, string) {
	t.Helper()
	resp, err := svc.noRedirect().Get(svc.app.URL + "/api/auth/google/start?return=/")
	if err != nil {
		t.Fatalf("GET /api/auth/google/start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("start status=%d, want 302", resp.StatusCode)
	}
	var stateCookie *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == authStateCookie {
			stateCookie = cookie
		}
	}
	if stateCookie == nil {
		t.Fatal("start set no OAuth state cookie")
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse start redirect: %v", err)
	}
	return stateCookie, location.Query().Get("state")
}

// TestM1616AuthRefusalsAdmitNobodyAndChangeNothing is the callback's refusal
// matrix. Every row breaks one thing about an otherwise legitimate sign-in, and
// every row is held to three claims: the documented status, no session cookie,
// and a WebSocket join carrying whatever the refusal left behind is a guest.
// Nothing on disk or in the instance table moves for any of them.
func TestM1616AuthRefusalsAdmitNobodyAndChangeNothing(t *testing.T) {
	svc := m1616NewService(t, true)
	before := svc.mutations()

	// A state cookie whose payload is signed for a *different* server.
	foreign := NewCookieSigner([]byte("some-other-servers-secret"))
	foreignState, err := foreign.Encode(oauthState{State: "abc", CodeVerifier: "v", ReturnTo: "/", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	expiredState, err := svc.auth.Signer.Encode(oauthState{State: "abc", CodeVerifier: "v", ReturnTo: "/", ExpiresAt: time.Now().Add(-time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		query func(state string) string
		// cookie chooses the state cookie: "" is the legitimate one from the
		// start call, anything else is used verbatim.
		cookie string
		omit   bool
		want   int
	}{
		{name: "no-state-cookie", query: func(s string) string { return "code=x&state=" + s }, omit: true, want: http.StatusBadRequest},
		{name: "foreign-signed-state", query: func(s string) string { return "code=x&state=abc" }, cookie: foreignState, want: http.StatusBadRequest},
		{name: "expired-state", query: func(s string) string { return "code=x&state=abc" }, cookie: expiredState, want: http.StatusBadRequest},
		{name: "state-mismatch", query: func(s string) string { return "code=x&state=not-the-one" }, want: http.StatusBadRequest},
		{name: "provider-error", query: func(s string) string { return "error=access_denied&state=" + s }, want: http.StatusBadRequest},
		{name: "missing-code", query: func(s string) string { return "state=" + s }, want: http.StatusBadRequest},
		{name: "unknown-code", query: func(s string) string { return "code=never-issued&state=" + s }, want: http.StatusBadGateway},
		{name: "unverifiable-id-token", query: func(s string) string { return "code=" + m1616BadCode + "&state=" + s }, want: http.StatusUnauthorized},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateCookie, state := m1616BeginAuth(t, svc)
			// The unverifiable row is the only one that has to reach the token
			// endpoint, so the provider must really have bound its code to THIS
			// sign-in's PKCE challenge. The verifier is read out of the state
			// cookie the server just minted, with the server's own signer — no
			// forgery, just the test looking at state it owns — and the
			// authorize hop is driven with the challenge that verifier hashes
			// to. The exchange that follows is the server's own.
			if tc.name == "unverifiable-id-token" {
				svc.idp.signInNext("nobody")
				m1616ArmProviderCode(t, svc, stateCookie, state)
			}
			if tc.cookie != "" {
				stateCookie = &http.Cookie{Name: authStateCookie, Value: tc.cookie}
			}

			req, err := http.NewRequest(http.MethodGet, svc.app.URL+"/api/auth/google/callback?"+tc.query(state), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.omit {
				req.AddCookie(stateCookie)
			}
			resp, err := svc.noRedirect().Do(req)
			if err != nil {
				t.Fatalf("callback: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				body := make([]byte, 200)
				n, _ := resp.Body.Read(body)
				t.Fatalf("callback status=%d, want %d (%s)", resp.StatusCode, tc.want, strings.TrimSpace(string(body[:n])))
			}

			var leaked *http.Cookie
			for _, cookie := range resp.Cookies() {
				if cookie.Name == authSessionCookie && cookie.Value != "" && cookie.MaxAge >= 0 {
					leaked = cookie
				}
			}
			if leaked != nil {
				t.Fatalf("a refused callback set a session cookie: %q", leaked.Value)
			}
			// Whatever it did leave behind, a join with it is still a guest.
			accountID, name, _, conn := svc.joinIdentity(ctx, "Refused", firstSessionCookie(resp))
			defer conn.Close(websocket.StatusNormalClosure, "")
			if accountID != "" || name != "Refused" {
				t.Fatalf("a refused sign-in produced identity (%q, %q), want a guest", accountID, name)
			}
		})
	}

	if after := svc.mutations(); after != before {
		t.Errorf("the refusal matrix changed server state: %+v, want %+v", after, before)
	}
}

// m1616ArmProviderCode drives the authorization hop the way the browser would,
// binding the provider's next code to the PKCE challenge THIS sign-in declared.
// The verifier comes out of the state cookie the server just minted, decoded
// with the server's own signer.
func m1616ArmProviderCode(t *testing.T, svc *m1616Service, stateCookie *http.Cookie, state string) {
	t.Helper()
	var decoded oauthState
	if err := svc.auth.Signer.Decode(stateCookie.Value, &decoded); err != nil {
		t.Fatalf("decode state cookie: %v", err)
	}
	sum := sha256.Sum256([]byte(decoded.CodeVerifier))

	q := url.Values{}
	q.Set("client_id", m1616ClientID)
	q.Set("redirect_uri", svc.app.URL+"/api/auth/google/callback")
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))

	resp, err := svc.noRedirect().Get(svc.auth.AuthEndpoint + "?" + q.Encode())
	if err != nil {
		t.Fatalf("provider authorize: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("provider authorize status=%d, want 302", resp.StatusCode)
	}
}

func firstSessionCookie(resp *http.Response) *http.Cookie {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == authSessionCookie {
			return cookie
		}
	}
	return nil
}

// TestM1616ForgedSessionCookiesAuthenticateNobody is the other half of the
// rejection surface: not the sign-in dance but the cookie a later request
// carries. Each row is asked twice — of /api/auth/me, and of a real WebSocket
// join — because they are two independent readers of the same cookie
// (AccountFromRequest via HandleMe, and via WebSocketServer.authAccount).
func TestM1616ForgedSessionCookiesAuthenticateNobody(t *testing.T) {
	svc := m1616NewService(t, true)
	ada := m1616AccountByKey(t, "ada")

	valid, err := svc.auth.Signer.Encode(authSession{
		Account:   AuthenticatedAccount{ID: ada.ID, Email: ada.Email, Name: ada.Name},
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := svc.auth.Signer.Encode(authSession{
		Account:   AuthenticatedAccount{ID: ada.ID, Name: ada.Name},
		ExpiresAt: time.Now().Add(-time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	noAccount, err := svc.auth.Signer.Encode(authSession{
		Account:   AuthenticatedAccount{Name: "Nameless"},
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := NewCookieSigner([]byte("some-other-servers-secret")).Encode(authSession{
		Account:   AuthenticatedAccount{ID: "google:intruder", Name: "Intruder"},
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A real cookie with one payload byte changed: the signature no longer
	// covers it. This is the forgery an attacker would actually attempt.
	tampered := m1616FlipPayload(t, valid)
	// The right payload with a signature lifted from a different payload.
	crossSigned := strings.SplitN(valid, ".", 2)[0] + "." + strings.SplitN(expired, ".", 2)[1]

	cases := []struct {
		name   string
		cookie *http.Cookie
	}{
		{"no-cookie", nil},
		{"empty", &http.Cookie{Name: authSessionCookie, Value: ""}},
		{"garbage", &http.Cookie{Name: authSessionCookie, Value: "not-a-cookie"}},
		{"unsigned-payload", &http.Cookie{Name: authSessionCookie, Value: base64.RawURLEncoding.EncodeToString([]byte(`{"account":{"id":"google:intruder","name":"Intruder"},"exp":9999999999}`))}},
		{"foreign-secret", &http.Cookie{Name: authSessionCookie, Value: foreign}},
		{"tampered-payload", &http.Cookie{Name: authSessionCookie, Value: tampered}},
		{"cross-signed", &http.Cookie{Name: authSessionCookie, Value: crossSigned}},
		{"expired", &http.Cookie{Name: authSessionCookie, Value: expired}},
		{"no-account-id", &http.Cookie{Name: authSessionCookie, Value: noAccount}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	before := svc.mutations()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Timeout: 10 * time.Second}
			var cookies []*http.Cookie
			if tc.cookie != nil {
				cookies = append(cookies, tc.cookie)
			}
			if me := svc.me(client, cookies...); me.Authenticated {
				t.Fatalf("/api/auth/me accepted %s: %+v", tc.name, me)
			}
			accountID, name, _, conn := svc.joinIdentity(ctx, "Impostor", tc.cookie)
			defer conn.Close(websocket.StatusNormalClosure, "")
			if accountID != "" || name != "Impostor" {
				t.Fatalf("a join with %s got identity (%q, %q), want a guest", tc.name, accountID, name)
			}
		})
	}

	// And the control: the untouched cookie those forgeries were cut from does
	// authenticate. Without this the rows above would also pass if the server
	// rejected everything.
	if me := svc.me(&http.Client{Timeout: 10 * time.Second}, &http.Cookie{Name: authSessionCookie, Value: valid}); !me.Authenticated || me.ID != ada.ID {
		t.Fatalf("the valid cookie the forgeries were cut from did not authenticate: %+v", me)
	}
	if after := svc.mutations(); after != before {
		t.Errorf("the forged-cookie matrix changed server state: %+v, want %+v", after, before)
	}
}

// m1616FlipPayload changes one character of a signed cookie's payload, leaving
// its signature in place.
func m1616FlipPayload(t *testing.T, signed string) string {
	t.Helper()
	parts := strings.SplitN(signed, ".", 2)
	if len(parts) != 2 || len(parts[0]) < 10 {
		t.Fatalf("cannot tamper with %q", signed)
	}
	payload := []byte(parts[0])
	if payload[5] == 'A' {
		payload[5] = 'B'
	} else {
		payload[5] = 'A'
	}
	return string(payload) + "." + parts[1]
}

// TestM1616GuestOnlyServerOffersNoSignIn certifies the other deployment: no
// Google credentials configured. /api/auth/me must say so (the client hides the
// G row on it), the OAuth routes must refuse rather than half-work, and joins
// must still succeed as guests.
func TestM1616GuestOnlyServerOffersNoSignIn(t *testing.T) {
	svc := m1616NewService(t, false)

	if me := svc.me(&http.Client{Timeout: 10 * time.Second}); me.Enabled || me.Authenticated {
		t.Fatalf("/api/auth/me on a guest-only server = %+v, want enabled=false", me)
	}
	for _, path := range []string{"/api/auth/google/start", "/api/auth/google/callback"} {
		resp, err := svc.noRedirect().Get(svc.app.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("GET %s status=%d, want 503 on a server without Google credentials", path, resp.StatusCode)
		}
	}
	// Logout is deliberately still OK: the client calls it unconditionally.
	logout, err := svc.app.Client().Post(svc.app.URL+"/api/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/auth/logout: %v", err)
	}
	logout.Body.Close()
	if logout.StatusCode != http.StatusOK {
		t.Errorf("logout on a guest-only server status=%d, want 200", logout.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	accountID, name, _, conn := svc.joinIdentity(ctx, "Guesty", nil)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if accountID != "" || name != "Guesty" {
		t.Fatalf("guest-only join identity = (%q, %q), want (\"\", \"Guesty\")", accountID, name)
	}
}

// ---------------------------------------------------------------------------
// Chat: the identity a message carries
// ---------------------------------------------------------------------------

// TestM1616ChatIdentityIsTheAccountAndSurvivesRestart ties the two halves of the
// chat contract together on the real socket: WHO a message is from (M16.16a
// pinned WHAT may be said), and that the record with that attribution is what a
// restarted server replays. A signed-in player's line is attributed to their
// Google display name even though their client asked for another; a guest's is
// the name they typed.
func TestM1616ChatIdentityIsTheAccountAndSurvivesRestart(t *testing.T) {
	svc := m1616NewService(t, true)
	ada := m1616AccountByKey(t, "ada")

	dbPath := filepath.Join(svc.rootDir, "chat.jsonl")
	fileDB, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewFileChatDatabase: %v", err)
	}
	svc.server.ChatDB = fileDB

	session, err := svc.auth.Signer.Encode(authSession{
		Account:   AuthenticatedAccount{ID: ada.ID, Email: ada.Email, Name: ada.Name},
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: authSessionCookie, Value: session}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	signedIn, _ := dialJoinWithCookie(t, ctx, svc.wsURL(), JoinMessage{Type: MessageTypeJoin, Name: "totally-ada-honest", Board: 1}, cookie)
	defer signedIn.Close(websocket.StatusNormalClosure, "")
	guest, _ := dialJoinWithCookie(t, ctx, svc.wsURL(), JoinMessage{Type: MessageTypeJoin, Name: "Guesty", Board: 1}, nil)
	defer guest.Close(websocket.StatusNormalClosure, "")

	m1616SendChat(t, ctx, signedIn, "the account speaks")
	if chat := readChatMessage(t, ctx, guest); chat.From != ada.Name || chat.Text != "the account speaks" {
		t.Fatalf("broadcast to the guest = %q/%q, want %q/%q", chat.From, chat.Text, ada.Name, "the account speaks")
	}
	_ = readChatMessage(t, ctx, signedIn)

	m1616SendChat(t, ctx, guest, "and so does the guest")
	if chat := readChatMessage(t, ctx, signedIn); chat.From != "Guesty" || chat.Text != "and so does the guest" {
		t.Fatalf("guest broadcast = %q/%q, want Guesty/and so does the guest", chat.From, chat.Text)
	}

	// The persisted record carries the same attribution the wire did.
	recs, err := fileDB.GetRecentMessages(50)
	if err != nil {
		t.Fatalf("GetRecentMessages: %v", err)
	}
	if len(recs) != 2 || recs[0].From != ada.Name || recs[1].From != "Guesty" {
		t.Fatalf("chat records = %+v, want one from %q and one from Guesty", recs, ada.Name)
	}

	// Restart: a brand-new server over the same file replays both lines, with
	// their identities, to somebody who was never there.
	signedIn.Close(websocket.StatusNormalClosure, "")
	guest.Close(websocket.StatusNormalClosure, "")
	if err := fileDB.Close(); err != nil {
		t.Fatalf("close chat db: %v", err)
	}
	reopened, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatalf("reopen chat db: %v", err)
	}
	defer reopened.Close()

	restarted := NewWebSocketServer(m1616TestWorld(t), 1)
	restarted.ChatDB = reopened
	restartedApp := httptest.NewServer(restarted)
	defer restartedApp.Close()
	newcomer, _ := dialJoinWithCookie(t, ctx, "ws"+strings.TrimPrefix(restartedApp.URL, "http"),
		JoinMessage{Type: MessageTypeJoin, Name: "Newcomer", Board: 1}, nil)
	defer newcomer.Close(websocket.StatusNormalClosure, "")
	for _, want := range []struct{ from, text string }{
		{ada.Name, "the account speaks"},
		{"Guesty", "and so does the guest"},
	} {
		chat := readChatMessage(t, ctx, newcomer)
		if chat.From != want.from || chat.Text != want.text {
			t.Fatalf("history replay = %q/%q, want %q/%q", chat.From, chat.Text, want.from, want.text)
		}
	}
}

func m1616SendChat(t *testing.T, ctx context.Context, conn *websocket.Conn, text string) {
	t.Helper()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"chat","text":`+mustJSONString(text)+`}`)); err != nil {
		t.Fatalf("write chat %q: %v", text, err)
	}
}

func mustJSONString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// Museum: search
// ---------------------------------------------------------------------------

// TestM1616MuseumSearchDedupesAcrossFieldsAndReportsNoMatches drives
// /api/museum/search on the real route. The Museum has no single "search
// everything" endpoint, so the service fans one query across four fields and
// merges; the fake answers each field differently so the merge is observable.
func TestM1616MuseumSearchDedupesAcrossFieldsAndReportsNoMatches(t *testing.T) {
	svc := m1616NewService(t, false)

	teen := m1616Result("t", "teen.zip", "Teen Priest", "Draco", "1998-08-31", "zzt_teen")
	yapok := m1616Result("y", "yapok.zip", "Amazing Yapok", "Yapok Jr.", "1995-10-22", "zzt_yapok")
	svc.fake.mu.Lock()
	// The same file found by three different fields, plus one found by one.
	svc.fake.results["title"] = []string{teen}
	svc.fake.results["author"] = []string{teen, yapok}
	svc.fake.results["genre"] = []string{teen}
	svc.fake.results["filename"] = nil
	svc.fake.mu.Unlock()

	search := m1616Search(t, svc, "teen")
	if search.Count != 2 || len(search.Results) != 2 {
		t.Fatalf("search count=%d results=%d, want 2 deduped hits: %+v", search.Count, len(search.Results), search.Results)
	}
	// Sorted by title, case-insensitively — the order the picker shows.
	if search.Results[0].Title != "Amazing Yapok" || search.Results[1].Title != "Teen Priest" {
		t.Fatalf("search order = %q,%q, want Amazing Yapok then Teen Priest", search.Results[0].Title, search.Results[1].Title)
	}
	got := search.Results[1]
	if got.ID != "zzt_teen" || got.Letter != "t" || got.Filename != "teen.zip" ||
		len(got.Author) != 1 || got.Author[0] != "Draco" || got.ReleaseDate != "1998-08-31" {
		t.Fatalf("the deduped hit lost its metadata: %+v", got)
	}
	if got.PlayableBoards != 12 || got.TotalBoards != 20 {
		t.Errorf("board counts = %d/%d, want 12/20", got.PlayableBoards, got.TotalBoards)
	}
	if fields := svc.fake.fieldsAsked(); strings.Join(fields, ",") != "author,filename,genre,title" {
		t.Fatalf("fields queried = %v, want the four text fields", fields)
	}

	// Nothing matches: an empty result set, not an error and not a stale list.
	svc.fake.mu.Lock()
	svc.fake.results = map[string][]string{}
	svc.fake.searchFields = nil
	svc.fake.mu.Unlock()
	if none := m1616Search(t, svc, "nosuchworldanywhere"); none.Count != 0 || len(none.Results) != 0 {
		t.Fatalf("no-match search = %+v, want an empty result set", none)
	}

	// An empty query never leaves the process: the picker types one character at
	// a time, and a blank box must not be a request.
	svc.fake.mu.Lock()
	svc.fake.searchFields = nil
	svc.fake.mu.Unlock()
	if blank := m1616Search(t, svc, "   "); blank.Count != 0 || len(blank.Results) != 0 {
		t.Fatalf("blank search = %+v, want an empty result set", blank)
	}
	if searches, downloads := svc.fake.counts(); searches != 0 || downloads != 0 {
		t.Fatalf("a blank query made %d searches and %d downloads, want none", searches, downloads)
	}

	// A four-digit query is also a year, so the year field joins the fan-out.
	svc.fake.mu.Lock()
	svc.fake.searchFields = nil
	svc.fake.mu.Unlock()
	m1616Search(t, svc, "1998")
	if fields := svc.fake.fieldsAsked(); strings.Join(fields, ",") != "author,filename,genre,title,year" {
		t.Fatalf("fields queried for a year = %v, want the year field included", fields)
	}
}

// TestM1616MuseumSearchFailureIsABadGatewayAndCachesNothing: the Museum being
// down is the service's problem, not the player's world list. It must be
// reported as an upstream failure and must not leave anything behind.
func TestM1616MuseumSearchFailureIsABadGatewayAndCachesNothing(t *testing.T) {
	svc := m1616NewService(t, false)
	before := svc.mutations()

	svc.fake.mu.Lock()
	svc.fake.searchStatus = http.StatusInternalServerError
	svc.fake.mu.Unlock()

	resp, err := svc.app.Client().Get(svc.app.URL + "/api/museum/search?q=teen")
	if err != nil {
		t.Fatalf("GET /api/museum/search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("search status=%d, want 502 when the Museum answers 500", resp.StatusCode)
	}
	if after := svc.mutations(); after != before {
		t.Errorf("a failed search changed server state: %+v, want %+v", after, before)
	}

	// Method contract: the picker only ever GETs.
	post, err := svc.app.Client().Post(svc.app.URL+"/api/museum/search", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /api/museum/search: %v", err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/museum/search status=%d, want 405", post.StatusCode)
	}
}

func m1616Search(t *testing.T, svc *m1616Service, query string) MuseumSearchResponse {
	t.Helper()
	resp, err := svc.app.Client().Get(svc.app.URL + "/api/museum/search?q=" + url.QueryEscape(query))
	if err != nil {
		t.Fatalf("GET /api/museum/search?q=%s: %v", query, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/museum/search?q=%s status=%d", query, resp.StatusCode)
	}
	var search MuseumSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&search); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	return search
}

// ---------------------------------------------------------------------------
// Museum: play — refusals, then the journey
// ---------------------------------------------------------------------------

type m1616PlayResult struct {
	status int
	body   string
	play   MuseumPlayResponse
}

func m1616Play(t *testing.T, svc *m1616Service, req MuseumPlayRequest) m1616PlayResult {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.app.Client().Post(svc.app.URL+"/api/museum/play", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /api/museum/play: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	result := m1616PlayResult{status: resp.StatusCode, body: strings.TrimSpace(string(raw))}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &result.play); err != nil {
			t.Fatalf("decode play response %q: %v", result.body, err)
		}
	}
	return result
}

// TestM1616MuseumRefusalsThroughTheRouteMutateNothing is the security half of
// the DoD, driven through /api/museum/play rather than through the service
// (which is where M16.16a pinned it). Each row states what it refuses AND that
// the refusal left no cache entry, no hosted .ZZT, no instance, and — where the
// name itself is the problem — that the Museum was never contacted at all.
func TestM1616MuseumRefusalsThroughTheRouteMutateNothing(t *testing.T) {
	svc := m1616NewService(t, false)

	valid := museumTestWorldBytes(t, "M1616A")
	svc.fake.mu.Lock()
	svc.fake.archives["t/corrupt.zip"] = []byte("PK not really")
	svc.fake.archives["t/traversal.zip"] = museumTestZip(t, map[string][]byte{"../EVIL.ZZT": valid})
	svc.fake.archives["t/docs.zip"] = museumTestZip(t, map[string][]byte{"README.TXT": []byte("just docs")})
	svc.fake.archives["t/one.zip"] = museumTestZip(t, map[string][]byte{"M1616A.ZZT": valid})
	svc.fake.archives["t/junk.zip"] = museumTestZip(t, map[string][]byte{"M1616B.ZZT": []byte("garbage world bytes")})
	svc.fake.mu.Unlock()

	cases := []struct {
		name string
		req  MuseumPlayRequest
		// offline rows must never reach the Museum: the name is refused before
		// a byte moves.
		offline bool
		want    int
	}{
		{name: "traversal-filename", req: MuseumPlayRequest{Letter: "t", Filename: "../teen.zip"}, offline: true, want: http.StatusUnprocessableEntity},
		{name: "absolute-filename", req: MuseumPlayRequest{Letter: "t", Filename: "/etc/passwd.zip"}, offline: true, want: http.StatusUnprocessableEntity},
		{name: "backslash-filename", req: MuseumPlayRequest{Letter: "t", Filename: `..\teen.zip`}, offline: true, want: http.StatusUnprocessableEntity},
		{name: "not-a-zip", req: MuseumPlayRequest{Letter: "t", Filename: "teen.tar"}, offline: true, want: http.StatusUnprocessableEntity},
		{name: "empty-letter", req: MuseumPlayRequest{Letter: "", Filename: "teen.zip"}, offline: true, want: http.StatusUnprocessableEntity},
		{name: "letter-is-a-path", req: MuseumPlayRequest{Letter: "..", Filename: "teen.zip"}, offline: true, want: http.StatusUnprocessableEntity},
		{name: "missing-archive", req: MuseumPlayRequest{Letter: "t", Filename: "nosuch.zip"}, want: http.StatusUnprocessableEntity},
		{name: "corrupt-zip", req: MuseumPlayRequest{Letter: "t", Filename: "corrupt.zip"}, want: http.StatusUnprocessableEntity},
		{name: "traversal-entry", req: MuseumPlayRequest{Letter: "t", Filename: "traversal.zip"}, want: http.StatusUnprocessableEntity},
		{name: "no-zzt-worlds", req: MuseumPlayRequest{Letter: "t", Filename: "docs.zip"}, want: http.StatusUnprocessableEntity},
		{name: "missing-selection", req: MuseumPlayRequest{Letter: "t", Filename: "one.zip", ZZTFile: "NOPE.ZZT"}, want: http.StatusUnprocessableEntity},
		{name: "invalid-selected-world", req: MuseumPlayRequest{Letter: "t", Filename: "junk.zip"}, want: http.StatusUnprocessableEntity},
	}

	before := svc.mutations()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, downloadsBefore := svc.fake.counts()
			result := m1616Play(t, svc, tc.req)
			if result.status != tc.want {
				t.Fatalf("status=%d, want %d (%s)", result.status, tc.want, result.body)
			}
			if result.body == "" {
				t.Error("a refusal must say why: the client shows this text")
			}
			_, downloadsAfter := svc.fake.counts()
			if tc.offline && downloadsAfter != downloadsBefore {
				t.Errorf("an unsafe name reached the Museum: %d downloads", downloadsAfter-downloadsBefore)
			}
			if after := svc.mutations(); after != before {
				t.Errorf("a refusal changed server state: %+v, want %+v", after, before)
			}
		})
	}

	// Route-level contracts, which no service test can reach.
	get, err := svc.app.Client().Get(svc.app.URL + "/api/museum/play")
	if err != nil {
		t.Fatalf("GET /api/museum/play: %v", err)
	}
	get.Body.Close()
	if get.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/museum/play status=%d, want 405", get.StatusCode)
	}
	junk, err := svc.app.Client().Post(svc.app.URL+"/api/museum/play", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("POST junk: %v", err)
	}
	junk.Body.Close()
	if junk.StatusCode != http.StatusBadRequest {
		t.Errorf("POST junk body status=%d, want 400", junk.StatusCode)
	}
	if after := svc.mutations(); after != before {
		t.Errorf("the route-level refusals changed server state: %+v, want %+v", after, before)
	}
}

// TestM1616MuseumSearchSelectHostJoin is the service half of the DoD's browser
// journey, asserted where the browser cannot see: search finds a multi-world
// archive, the first Play answers with the choice list, the selection hosts one
// world from the CACHED archive, the world picker lists it, and a real
// WebSocket joins it and is counted.
func TestM1616MuseumSearchSelectHostJoin(t *testing.T) {
	svc := m1616NewService(t, false)

	worldA := museumTestWorldBytes(t, "M1616A")
	worldB := museumTestWorldBytes(t, "M1616B")
	svc.fake.mu.Lock()
	svc.fake.archives["m/m1616.zip"] = museumTestZip(t, map[string][]byte{
		"M1616A.ZZT": worldA,
		"M1616B.ZZT": worldB,
		"README.TXT": []byte("docs travel with the worlds"),
	})
	svc.fake.results["title"] = []string{m1616Result("m", "m1616.zip", "The M1616 Collection", "A Tester", "1996-01-01", "zzt_m1616")}
	svc.fake.mu.Unlock()

	search := m1616Search(t, svc, "m1616")
	if len(search.Results) != 1 {
		t.Fatalf("search results=%+v, want the one archive", search.Results)
	}
	hit := search.Results[0]
	if hit.Title != "The M1616 Collection" || hit.Author[0] != "A Tester" || hit.ReleaseDate != "1996-01-01" {
		t.Fatalf("search metadata = %+v, want the archive's title/author/date", hit)
	}

	// One archive, two worlds: the service asks rather than guessing.
	choices := m1616Play(t, svc, MuseumPlayRequest{Letter: hit.Letter, Filename: hit.Filename})
	if choices.status != http.StatusOK {
		t.Fatalf("choices status=%d (%s)", choices.status, choices.body)
	}
	if len(choices.play.Choices) != 2 || choices.play.Choices[0].Name != "M1616A.ZZT" || choices.play.Choices[1].Name != "M1616B.ZZT" {
		t.Fatalf("choices=%+v, want both .ZZT worlds and not the README", choices.play.Choices)
	}
	if choices.play.World != "" {
		t.Fatalf("a choices response also named a world (%q); nothing may be hosted yet", choices.play.World)
	}
	svc.server.mu.Lock()
	hostedEarly := len(svc.server.Instances)
	svc.server.mu.Unlock()
	if hostedEarly != 1 {
		t.Fatalf("instances after the choices response=%d, want only the default world", hostedEarly)
	}

	hosted := m1616Play(t, svc, MuseumPlayRequest{Letter: hit.Letter, Filename: hit.Filename, ZZTFile: "M1616B.ZZT"})
	if hosted.status != http.StatusOK {
		t.Fatalf("selection status=%d (%s)", hosted.status, hosted.body)
	}
	if hosted.play.World != "M1616B" {
		t.Fatalf("hosted world=%q, want M1616B", hosted.play.World)
	}
	if _, downloads := svc.fake.counts(); downloads != 1 {
		t.Fatalf("the Museum was asked for the archive %d times, want one (the selection is a cache hit)", downloads)
	}
	if _, err := os.Stat(filepath.Join(svc.worldsDir, "M1616B.ZZT")); err != nil {
		t.Fatalf("the selected world was not written to the hosting directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.worldsDir, "M1616A.ZZT")); err == nil {
		t.Error("the unselected world was hosted too; only the selection may be")
	}

	// The world picker is how a player reaches it, so the picker must list it.
	worlds := m1616Worlds(t, svc)
	entry, ok := m1616WorldEntry(worlds, "M1616B")
	if !ok {
		t.Fatalf("the picker does not list the hosted world: %+v", worlds)
	}
	if entry.Title != "M1616B" || entry.Author != "Local" || entry.Kind != WorldKindLocal {
		t.Fatalf("picker entry=%+v, want a local world named after its file", entry)
	}
	if entry.Players != 0 {
		t.Errorf("picker says %d players before anybody joined", entry.Players)
	}
	if _, ok := m1616WorldEntry(worlds, m1616World); !ok {
		t.Errorf("the picker lost the server's own world: %+v", worlds)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, snapshot := dialJoinWithCookie(t, ctx, svc.wsURL()+"?world=M1616B",
		JoinMessage{Type: MessageTypeJoin, Name: "Importer", Board: 1}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")
	if snapshot.You.ID == 0 {
		t.Fatal("joined the imported world with no player")
	}
	svc.server.mu.Lock()
	inst := svc.server.Instances["M1616B"]
	joined := 0
	if inst != nil {
		inst.mu.Lock()
		joined = len(inst.Clients)
		inst.mu.Unlock()
	}
	svc.server.mu.Unlock()
	if joined != 1 {
		t.Fatalf("the imported world has %d clients, want the one that joined", joined)
	}
	// And the picker's live occupancy follows.
	if entry, ok := m1616WorldEntry(m1616Worlds(t, svc), "M1616B"); !ok || entry.Players != 1 {
		t.Fatalf("picker occupancy for the joined world = %+v, want players=1", entry)
	}
}

func m1616Worlds(t *testing.T, svc *m1616Service) []WorldListEntry {
	t.Helper()
	resp, err := svc.app.Client().Get(svc.app.URL + "/api/worlds")
	if err != nil {
		t.Fatalf("GET /api/worlds: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/worlds status=%d", resp.StatusCode)
	}
	var body struct {
		Worlds []WorldListEntry `json:"worlds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /api/worlds: %v", err)
	}
	return body.Worlds
}

func m1616WorldEntry(worlds []WorldListEntry, name string) (WorldListEntry, bool) {
	for _, entry := range worlds {
		if entry.World == name {
			return entry, true
		}
	}
	return WorldListEntry{}, false
}

// ---------------------------------------------------------------------------
// The routes the title screen reads
// ---------------------------------------------------------------------------

// TestM1616TitleScreenRoutesServeAndRefuse covers the read-only routes a browser
// hits before it ever has a socket: the title board and its animation stream,
// the high-score list, and the help files — each with the traversal/bad-name
// refusal that guards it.
func TestM1616TitleScreenRoutesServeAndRefuse(t *testing.T) {
	svc := m1616NewService(t, false)
	oldHelpDir := HelpDir
	HelpDir = "."
	t.Cleanup(func() { HelpDir = oldHelpDir })

	// /api/title — the first paint.
	var title struct {
		World    string       `json:"world"`
		Filename string       `json:"filename"`
		Screen   []ScreenCell `json:"screen"`
	}
	m1616GetJSON(t, svc, "/api/title?world="+m1616World, &title)
	if title.Filename != m1616World || len(title.Screen) == 0 {
		t.Fatalf("/api/title = %q/%d cells, want %s and a painted board", title.Filename, len(title.Screen), m1616World)
	}
	m1616ExpectStatus(t, svc, "/api/title?world=../../etc/passwd", http.StatusBadRequest)

	// /api/highscores — the H window's contents.
	var scores struct {
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	}
	m1616GetJSON(t, svc, "/api/highscores?world="+m1616World, &scores)
	if !strings.HasPrefix(scores.Title, "High scores for ") {
		t.Errorf("high-score title=%q, want the vanilla heading", scores.Title)
	}
	m1616ExpectStatus(t, svc, "/api/highscores?world=..%2Fsecret", http.StatusBadRequest)

	// /api/help — a .HLP file, by basename only.
	var help struct {
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	}
	m1616GetJSON(t, svc, "/api/help?file=ABOUT.HLP&title=About+ZZT", &help)
	if help.Title != "About ZZT" || len(help.Lines) == 0 {
		t.Fatalf("/api/help = %q/%d lines, want the About text", help.Title, len(help.Lines))
	}
	for _, bad := range []string{
		"/api/help?file=../TASKS.md",
		"/api/help?file=" + url.QueryEscape("../ABOUT.HLP"),
		"/api/help?file=TOWN.ZZT",
		"/api/help?file=",
	} {
		m1616ExpectStatus(t, svc, bad, http.StatusBadRequest)
	}
	m1616ExpectStatus(t, svc, "/api/help?file=NOSUCHFILE.HLP", http.StatusNotFound)

	// /api/title/stream — the title board's animation, as Server-Sent Events.
	// It idles until somebody subscribes, so the frames only start once this
	// request is open; the ticker below is this test standing in for the server
	// loop cmd/zzt-server runs.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.app.URL+"/api/title/stream?world="+m1616World, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.app.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/title/stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/title/stream status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("/api/title/stream content-type=%q, want text/event-stream", got)
	}

	ticking := make(chan struct{})
	go func() {
		defer close(ticking)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			svc.server.Tick(ctx)
			time.Sleep(2 * time.Millisecond)
		}
	}()

	lines := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: ") {
				select {
				case lines <- strings.TrimSpace(strings.TrimPrefix(line, "data: ")):
				default:
				}
				return
			}
		}
	}()

	select {
	case payload := <-lines:
		var cells []ScreenCell
		if err := json.Unmarshal([]byte(payload), &cells); err != nil {
			t.Fatalf("title stream frame is not a cell list: %v (%q)", err, payload)
		}
		if len(cells) == 0 {
			t.Fatal("title stream pushed an empty frame")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the title stream pushed no frame while the board was ticking")
	}
	cancel()
	<-ticking

	// A bad world name is refused on the stream too.
	m1616ExpectStatus(t, svc, "/api/title/stream?world=../secret", http.StatusBadRequest)
}

// ---------------------------------------------------------------------------
// The browser journey
// ---------------------------------------------------------------------------

// museumControlRoutes adds M16.16's endpoint to the harness control listener
// (engine/m16_9_test.go's controlMux calls it). The browser can see the chat
// line it was sent; only the server can say what it PERSISTED under which name,
// and that is the half the DoD's "correct chat identity" turns on.
func (h *m169Harness) museumControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/control/chat", func(w http.ResponseWriter, r *http.Request) {
		records := []ChatRecord{}
		if h.server.ChatDB != nil {
			recs, err := h.server.ChatDB.GetRecentMessages(50)
			if err != nil {
				http.Error(w, "chat database: "+err.Error(), http.StatusInternalServerError)
				return
			}
			records = append(records, recs...)
		}
		writeJSON(w, records)
	})
}

// The archive the browser imports. Two worlds, so the journey goes through the
// selection window rather than around it, and a README so "only .ZZT files are
// offered" is visible from the browser.
const (
	m1616Archive      = "cavecoll.zip"
	m1616ArchiveTitle = "The Cavern Collection"
	m1616HostedWorld  = "CAVERN1"
)

// m1616NewBrowserHarness is M16.9's harness with M16.16's two fakes wired into
// the same production objects: M16.14's hermetic identity provider (already
// served by the control listener) and a Museum of ZZT served by this test.
func m1616NewBrowserHarness(t *testing.T) (*m169Harness, *m1616MuseumFake) {
	t.Helper()

	m1614Provider.mu.Lock()
	m1614Provider.next = ""
	m1614Provider.challenges = map[string]string{}
	m1614Provider.logins = nil
	m1614Provider.mu.Unlock()

	auth := NewAuthService(m1614ClientID, m1614ClientSecret, "", []byte("m16-16-cookie-secret"))
	auth.Verifier = m1614Verifier{}

	fake := m1616NewMuseumFake(t)
	fake.mu.Lock()
	fake.archives["c/"+m1616Archive] = museumTestZip(t, map[string][]byte{
		"CAVERN1.ZZT": museumTestWorldBytes(t, "CAVERN1"),
		"CAVERN2.ZZT": museumTestWorldBytes(t, "CAVERN2"),
		"README.TXT":  []byte("two caverns and a readme"),
	})
	fake.results["title"] = []string{
		m1616Result("c", m1616Archive, m1616ArchiveTitle, "Ada Tester", "1996-05-04", "zzt_cavecoll"),
	}
	fake.mu.Unlock()

	h := m169NewHarnessFor(t, m1616World, m1616TestWorld(t),
		func(h *m169Harness, server *WebSocketServer, api *WebAPI) {
			server.Auth = auth
			api.Auth = auth
			h.auth = auth
			// Absolute, because the browser is redirected to them; the harness
			// binds both ports before running this option, so writing them here
			// happens before any handler goroutine exists (M16.14c).
			auth.AuthEndpoint = h.controlURL + "/idp/authorize"
			auth.TokenEndpoint = h.controlURL + "/idp/token"

			museum := NewMuseumService(server)
			museum.APIBaseURL = fake.server.URL + "/api/v1"
			museum.FilesBaseURL = fake.server.URL + "/zgames"
			museum.Client = fake.server.Client()
			museum.CacheDir = filepath.Join(h.worldsDir, ".museum-cache")
			museum.lastRequest = time.Now().Add(-museumRequestDelay)
			api.Museum = museum
		})
	return h, fake
}

// TestM1616BrowserAuthAndMuseumJourney is the DoD's browser clause: one real
// Chromium signs in through the identity provider, searches the Museum from the
// world picker, sees the archive's metadata, selects a world out of it, and is
// hosted into that world — where its chat carries the Google display name.
//
// The browser proves what a browser can see (the sidebar, the picker rows, the
// selection window, the chat line). Everything else is checked here, from the
// server's own state: the archive really was fetched once and cached, the world
// really was written into the hosting directory, the instance really has that
// one client, and the persisted chat record really is attributed to the account.
func TestM1616BrowserAuthAndMuseumJourney(t *testing.T) {
	h, fake := m1616NewBrowserHarness(t)
	out := h.runBrowserScript("museum_journey.test.mjs")
	t.Logf("M16.16 browser journey:\n%s", out)

	if _, downloads := fake.counts(); downloads != 1 {
		t.Errorf("the Museum served the archive %d times, want exactly one (the selection is a cache hit)", downloads)
	}
	if searches, _ := fake.counts(); searches == 0 {
		t.Error("the browser never searched the Museum")
	}
	if _, err := os.Stat(filepath.Join(h.worldsDir, m1616HostedWorld+".ZZT")); err != nil {
		t.Errorf("the imported world is not in the hosting directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.worldsDir, "CAVERN2.ZZT")); err == nil {
		t.Error("the world the player did not select was hosted too")
	}
	if n := countFilesUnder(t, filepath.Join(h.worldsDir, ".museum-cache")); n != 1 {
		t.Errorf("museum cache holds %d files, want the one validated archive", n)
	}

	// That the instance held exactly this one client WHILE the browser was in it
	// is the script's claim (/control/instances, act 6) — by the time this runs
	// the browser has closed and the socket with it. What survives the browser is
	// the instance itself, which is what an import is.
	h.server.mu.Lock()
	_, hosted := h.server.Instances[m1616HostedWorld]
	h.server.mu.Unlock()
	if !hosted {
		t.Fatalf("%s was never hosted as an instance", m1616HostedWorld)
	}

	recs, err := h.server.ChatDB.GetRecentMessages(50)
	if err != nil {
		t.Fatalf("GetRecentMessages: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("chat records = %+v, want the one line the browser sent", recs)
	}
	ada, _ := m1614AccountByKey("ada")
	if recs[0].From != ada.Name {
		t.Errorf("the persisted chat line is from %q, want the signed-in account's display name %q", recs[0].From, ada.Name)
	}
}

func m1616GetJSON(t *testing.T, svc *m1616Service, path string, out interface{}) {
	t.Helper()
	resp, err := svc.app.Client().Get(svc.app.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func m1616ExpectStatus(t *testing.T, svc *m1616Service, path string, want int) {
	t.Helper()
	resp, err := svc.app.Client().Get(svc.app.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Errorf("GET %s status=%d, want %d", path, resp.StatusCode, want)
	}
}
