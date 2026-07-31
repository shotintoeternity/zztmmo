package zztgo

// M16.14 — collaborative editor invariants in real browsers.
//
// WHAT THIS IS. Three real browsers open ONE editor session on one world: two
// signed-in accounts and a signed-out guest. Everything the DoD names — live
// diffs and cursors, local echo, out-of-order replies, per-board and per-stat
// leases, release on an abrupt disconnect, ownership/invite/read-only refusal,
// simultaneous cell edits, the publish occupancy rule, and co-op test play — is
// driven through those browsers against the production server objects, on the
// harness M16.9 stood up and M16.13 extended.
//
// THREE AUTHORITIES, DELIBERATELY. A collaborative claim is not provable from
// one place:
//
//   1. THE CANVAS of each browser, decoded to CP437 cells. It is the only place
//      a stale screen, a missing collaborator cursor, or a refusal dialog that
//      never opened can be seen.
//   2. THE SESSION, read out of the running EditorSession through the control
//      listener (/control/editor/session, /control/editor/board). Members,
//      read-only flags, per-member boards and every held lease come from the
//      server's own maps, so "the lease was released" is not inferred from a
//      dialog.
//   3. THE SERIALIZED WORLD, parsed by m1613ReadVanillaWorld — the independent
//      vanilla-format reader M16.13 wrote, which shares no code with this fork's
//      loader. "Both browsers converged on one serialized world" is checked
//      against that parse at every schedule checkpoint, and "an unauthorized
//      operation left the bytes unchanged" is checked as bytes.
//
// SIGNING IN IS REAL. The browser presses G on the title screen, which is the
// product's own sign-in path (title.ts TITLE_CODES), and the whole OAuth
// redirect dance runs against a hermetic identity provider served by this test
// binary: it checks the PKCE challenge it was given before it issues a code, and
// the token endpoint refuses a verifier that does not hash to it. No cookie is
// injected into the browser — the session cookie is the one HandleCallback set.
//
// WHAT THIS SWEEP FOUND, AND DID NOT FIX. Three divergences, all filed as gap
// task M16.14a and pinned from both sides here:
//
//   a. Board- and world-scoped changes reach only the member who made them.
//      editorDiff is broadcast to everyone viewing the board (M10.1/M17.12), but
//      Clear board, Board Information, Add board, Import board and New world all
//      reply to the acting client alone (websocket_server.go serveEditorBoard,
//      and the editorProperty case). A collaborator watching the same board sees
//      the old tiles until something else repaints them.
//   b. An invited collaborator stays read-only until they re-enter the editor.
//      inviteEditorCollaborator clears the server-side flag
//      (EditorSession.SetAccountReadOnly) and sends the invitee nothing, and the
//      client's own editorReadOnly — which every editor key consults before it
//      sends anything — is only ever set from a snapshot.
//   c. A stat lease is stranded when another member switches boards first. Its
//      key is resolved against the ONE shared engine's current board, which
//      follows whoever acted last, so the holder's release resolves to no key
//      and is dropped — leaving a lease held by somebody who closed the dialog.
//      The three-browser arrangement is what makes this reachable at all.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The world the three browsers share. It is M16.13's fixture: one authored
// board with an object, a lion, a spinning gun, a passage and a duplicator, plus
// a second board — which is exactly the shape the per-stat and per-board lease
// claims need.
const m1614World = "COLLAB"

// ---------------------------------------------------------------------------
// The hermetic identity provider
// ---------------------------------------------------------------------------

// m1614Account is one fake person. Code and Token are the authorization code the
// IdP hands back and the id_token its token endpoint exchanges that code for;
// keeping them per-account is what lets the token endpoint stay stateless about
// WHO is logging in.
type m1614Account struct {
	Key   string
	ID    string
	Email string
	Name  string
	Code  string
	Token string
}

// Display names are kept under 14 characters: drawTitleSidebar prints
// accountName.slice(0, 13) at (65,23), and the browser script reads that row as
// its proof the sign-in really happened.
var m1614Accounts = []m1614Account{
	{Key: "ada", ID: "google:ada", Email: "ada@example.test", Name: "Ada Lovelace", Code: "code-ada", Token: "token-ada"},
	{Key: "bob", ID: "google:bob", Email: "bob@example.test", Name: "Bob Bones", Code: "code-bob", Token: "token-bob"},
}

func m1614AccountByKey(key string) (m1614Account, bool) {
	for _, account := range m1614Accounts {
		if account.Key == key {
			return account, true
		}
	}
	return m1614Account{}, false
}

// m1614Verifier is the IDTokenVerifier half: it maps the id_token the token
// endpoint issued back to the account. Anything else is rejected, so a callback
// that skipped the exchange cannot authenticate.
type m1614Verifier struct{}

func (m1614Verifier) VerifyIDToken(ctx context.Context, token, clientID string) (AuthenticatedAccount, error) {
	if clientID != m1614ClientID {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	for _, account := range m1614Accounts {
		if account.Token == token {
			return AuthenticatedAccount{ID: account.ID, Email: account.Email, Name: account.Name}, nil
		}
	}
	return AuthenticatedAccount{}, ErrInvalidAuth
}

const (
	m1614ClientID     = "zztmmo-m1614-client"
	m1614ClientSecret = "zztmmo-m1614-secret"
)

// m1614IdP is the state the identity provider needs: who the next authorize call
// should sign in, and the PKCE challenge each issued code was bound to.
type m1614IdP struct {
	mu         sync.Mutex
	next       string
	challenges map[string]string
	logins     []string
}

var m1614Provider = &m1614IdP{challenges: map[string]string{}}

// collabControlRoutes adds M16.14's endpoints to the harness control listener.
// The /idp routes are the fake Google; the /control routes read the editor
// session, the worlds directory and the hosted instances from outside the
// browser, so no claim in the browser script rests on the browser's own word.
func (h *m169Harness) collabControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/control/auth/next", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("account")
		if _, ok := m1614AccountByKey(key); !ok {
			http.Error(w, "unknown account "+key, http.StatusBadRequest)
			return
		}
		m1614Provider.mu.Lock()
		m1614Provider.next = key
		m1614Provider.mu.Unlock()
		writeJSON(w, map[string]string{"account": key})
	})

	// The authorization endpoint. It is a checker, not a rubber stamp: a request
	// without the harness client id, without S256, or without a challenge is
	// refused, and the challenge is remembered so the token endpoint can hold the
	// exchange to it.
	mux.HandleFunc("/idp/authorize", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("client_id") != m1614ClientID {
			http.Error(w, "wrong client_id", http.StatusBadRequest)
			return
		}
		if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
			http.Error(w, "missing PKCE challenge", http.StatusBadRequest)
			return
		}
		redirect := query.Get("redirect_uri")
		if redirect == "" || query.Get("state") == "" {
			http.Error(w, "missing redirect_uri or state", http.StatusBadRequest)
			return
		}
		m1614Provider.mu.Lock()
		account, ok := m1614AccountByKey(m1614Provider.next)
		if ok {
			m1614Provider.challenges[account.Code] = query.Get("code_challenge")
			m1614Provider.logins = append(m1614Provider.logins, account.Key)
		}
		m1614Provider.mu.Unlock()
		if !ok {
			http.Error(w, "no account armed: POST /control/auth/next?account=... first", http.StatusConflict)
			return
		}
		target := redirect + "?code=" + url.QueryEscape(account.Code) + "&state=" + url.QueryEscape(query.Get("state"))
		http.Redirect(w, r, target, http.StatusFound)
	})

	mux.HandleFunc("/idp/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "authorization_code" ||
			r.Form.Get("client_id") != m1614ClientID ||
			r.Form.Get("client_secret") != m1614ClientSecret {
			writeJSON(w, map[string]string{"error": "invalid_client"})
			return
		}
		code := r.Form.Get("code")
		m1614Provider.mu.Lock()
		challenge, known := m1614Provider.challenges[code]
		m1614Provider.mu.Unlock()
		if !known {
			writeJSON(w, map[string]string{"error": "invalid_grant"})
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			writeJSON(w, map[string]string{"error": "invalid_grant", "error_description": "PKCE verifier does not match"})
			return
		}
		for _, account := range m1614Accounts {
			if account.Code == code {
				writeJSON(w, map[string]string{"id_token": account.Token})
				return
			}
		}
		writeJSON(w, map[string]string{"error": "invalid_grant"})
	})

	// The editor session as the server holds it: who is in it, what each of them
	// may do and is looking at, and every lease. This is the authority behind
	// "the lease was released" and "presence was cleaned up".
	mux.HandleFunc("/control/editor/session", func(w http.ResponseWriter, r *http.Request) {
		session := h.editorSession()
		if session == nil {
			http.Error(w, "no editor session for "+h.worldName, http.StatusNotFound)
			return
		}
		writeJSON(w, m1614ReadSession(session))
	})

	// The worlds directory: what is actually on disk, by content. Publishing
	// claims — and the refusals that must move nothing — are checked here.
	mux.HandleFunc("/control/worlds/files", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, m1614ReadWorldsDir(h.worldsDir))
	})

	// Hold the editor session's own lock for a while. This is how the browser run
	// gets a deterministic window in which a keystroke has been typed and the
	// server has demonstrably not answered it yet — the only way to see a local
	// echo as an echo rather than as a fast round trip. It returns once the lock
	// is held, so the caller knows the window has started.
	mux.HandleFunc("/control/editor/hold", func(w http.ResponseWriter, r *http.Request) {
		session := h.editorSession()
		if session == nil {
			http.Error(w, "no editor session for "+h.worldName, http.StatusNotFound)
			return
		}
		ms, err := strconv.Atoi(r.URL.Query().Get("ms"))
		if err != nil || ms <= 0 || ms > 5000 {
			http.Error(w, "hold ms must be 1..5000", http.StatusBadRequest)
			return
		}
		held := make(chan struct{})
		go func() {
			session.mu.Lock()
			close(held)
			time.Sleep(time.Duration(ms) * time.Millisecond)
			session.mu.Unlock()
		}()
		<-held
		writeJSON(w, map[string]int{"heldMs": ms})
	})

	// Every hosted instance and how many clients it has. Co-op test play is the
	// claim that two browsers land in ONE instance, which no single browser can
	// see for itself.
	mux.HandleFunc("/control/instances", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, h.instanceReport())
	})
}

// ---------------------------------------------------------------------------
// Reading the session from outside
// ---------------------------------------------------------------------------

type m1614MemberReport struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"accountId"`
	Color     byte   `json:"color"`
	ReadOnly  bool   `json:"readOnly"`
	BoardID   int16  `json:"boardId"`
	X         int16  `json:"x"`
	Y         int16  `json:"y"`
}

type m1614LeaseReport struct {
	Kind       string `json:"kind"`
	BoardID    int16  `json:"boardId"`
	StatID     int16  `json:"statId"`
	HolderID   string `json:"holderId"`
	HolderName string `json:"holderName"`
}

type m1614SessionReport struct {
	World   string              `json:"world"`
	Members []m1614MemberReport `json:"members"`
	Leases  []m1614LeaseReport  `json:"leases"`
}

// m1614ReadSession walks the session's own maps under its own lock. Everything
// it returns is sorted: these maps are ranged, and a JSON order that shuffled
// between polls would make the browser script's waits into coin tosses (and
// would be a determinism hole of the test's own making).
func m1614ReadSession(s *EditorSession) m1614SessionReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := m1614SessionReport{
		World:   s.WorldName,
		Members: []m1614MemberReport{},
		Leases:  []m1614LeaseReport{},
	}
	for member := range s.Members {
		presence := s.memberInfo[member]
		report.Members = append(report.Members, m1614MemberReport{
			ID:        presence.ID,
			Name:      presence.Name,
			AccountID: member.accountID,
			Color:     presence.Color,
			ReadOnly:  s.readOnly[member],
			BoardID:   s.memberBoard[member],
			X:         presence.X,
			Y:         presence.Y,
		})
	}
	sort.Slice(report.Members, func(i, j int) bool { return report.Members[i].ID < report.Members[j].ID })
	for key, holder := range s.leases {
		presence := s.memberInfo[holder]
		report.Leases = append(report.Leases, m1614LeaseReport{
			Kind:       key.kind,
			BoardID:    key.boardID,
			StatID:     key.statID,
			HolderID:   presence.ID,
			HolderName: presence.Name,
		})
	}
	sort.Slice(report.Leases, func(i, j int) bool {
		a, b := report.Leases[i], report.Leases[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.BoardID != b.BoardID {
			return a.BoardID < b.BoardID
		}
		return a.StatID < b.StatID
	})
	return report
}

type m1614WorldFile struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

type m1614WorldsReport struct {
	Files  []m1614WorldFile       `json:"files"`
	Access map[string]WorldAccess `json:"access"`
}

func m1614ReadWorldsDir(dir string) m1614WorldsReport {
	report := m1614WorldsReport{Files: []m1614WorldFile{}, Access: map[string]WorldAccess{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return report
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".access.json") {
			var access WorldAccess
			if json.Unmarshal(data, &access) == nil {
				report.Access[strings.TrimSuffix(entry.Name(), ".access.json")] = access
			}
			continue
		}
		sum := sha256.Sum256(data)
		report.Files = append(report.Files, m1614WorldFile{
			Name:   entry.Name(),
			Size:   len(data),
			SHA256: fmt.Sprintf("%x", sum),
		})
	}
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Name < report.Files[j].Name })
	return report
}

type m1614InstanceReport struct {
	Name    string `json:"name"`
	Clients int    `json:"clients"`
}

func (h *m169Harness) instanceReport() []m1614InstanceReport {
	h.server.mu.Lock()
	instances := make([]*WorldInstance, 0, len(h.server.Instances))
	for _, inst := range h.server.Instances {
		instances = append(instances, inst)
	}
	h.server.mu.Unlock()

	out := make([]m1614InstanceReport, 0, len(instances))
	for _, inst := range instances {
		inst.mu.Lock()
		out = append(out, m1614InstanceReport{Name: inst.Name, Clients: len(inst.Clients)})
		inst.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

func m1614NewHarness(t *testing.T) *m169Harness {
	t.Helper()
	m1614Provider.mu.Lock()
	m1614Provider.next = ""
	m1614Provider.challenges = map[string]string{}
	m1614Provider.logins = nil
	m1614Provider.mu.Unlock()

	auth := NewAuthService(m1614ClientID, m1614ClientSecret, "", []byte("m16-14-cookie-secret"))
	auth.Verifier = m1614Verifier{}
	// The world is hosted under its own name so that the instance the browsers
	// reach through the picker is the one built from THIS world value, whose
	// CurrentBoard is the fixture's board 0 (m1613EditorWorld asserts it). A
	// differently-named instance would be loaded back from the file the harness
	// writes, which m169WriteWorldFile leaves open on the harness's start board.
	world := m1613EditorWorld(t)
	world.Info.Name = m1614World
	h := m169NewHarnessFor(t, m1614World, world,
		func(h *m169Harness, server *WebSocketServer, api *WebAPI) {
			server.Auth = auth
			api.Auth = auth
			h.auth = auth
		})
	// The endpoints can only be filled in once the control listener has a port:
	// the browser is redirected to them, so they have to be absolute.
	auth.AuthEndpoint = h.controlURL + "/idp/authorize"
	auth.TokenEndpoint = h.controlURL + "/idp/token"
	return h
}

func m1614OutDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("web", "test-results", "m16-14"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ---------------------------------------------------------------------------
// The run report
// ---------------------------------------------------------------------------

// m1614Landmark is one tile the browsers agreed on at a checkpoint: what the
// canvases showed and what the session said, recorded so the Go side can hold
// the SERIALIZED world to the same claim through an independent reader.
type m1614Landmark struct {
	Board   int    `json:"board"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Element int    `json:"element"`
	Color   int    `json:"color"`
	What    string `json:"what"`
}

// m1614Checkpoint is one schedule: the browsers' screens agreed, the session
// serialized, and these landmarks were what everyone believed.
type m1614Checkpoint struct {
	Label     string          `json:"label"`
	World     string          `json:"world"`
	Landmarks []m1614Landmark `json:"landmarks"`
	// Screens is the file holding the board region every browser was showing,
	// written once because they were required to be identical to record it.
	Screens string `json:"screens"`
	// Browsers names the browsers whose board regions were compared.
	Browsers []string `json:"browsers"`
}

type m1614Report struct {
	Checkpoints []m1614Checkpoint `json:"checkpoints"`
	Findings    []string          `json:"findings"`
	Notes       []string          `json:"notes"`
	// Files maps a role to a file in the out dir (published world bytes, etc.).
	Files map[string]string `json:"files"`
}

// ---------------------------------------------------------------------------
// The sweep
// ---------------------------------------------------------------------------

// TestM1614CollaborativeEditorInBrowsers is the sweep. Three browsers — two
// signed-in accounts and a guest — share one editor session; this test then
// holds every checkpoint the run recorded to the serialized world, read by the
// independent vanilla-format reader rather than by this fork's loader.
func TestM1614CollaborativeEditorInBrowsers(t *testing.T) {
	h := m1614NewHarness(t)
	outDir := m1614OutDir(t)
	out := h.runBrowserScript("editor_collab.test.mjs", "M1614_OUT="+outDir)
	t.Logf("collaborative editor run:\n%s", out)

	reportData, err := os.ReadFile(filepath.Join(outDir, "report.json"))
	if err != nil {
		t.Fatalf("the browser script wrote no run report: %v", err)
	}
	var report m1614Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("parse the run report: %v", err)
	}
	if len(report.Checkpoints) == 0 {
		t.Fatal("the run recorded no checkpoints")
	}

	t.Run("every checkpoint's world parses as vanilla and holds its landmarks", func(t *testing.T) {
		for _, checkpoint := range report.Checkpoints {
			if checkpoint.World == "" {
				t.Errorf("checkpoint %q recorded no serialized world", checkpoint.Label)
				continue
			}
			data := m1613ReadArtifact(t, outDir, checkpoint.World)
			world, err := m1613ReadVanillaWorld(data)
			if err != nil {
				t.Errorf("checkpoint %q: the session did not serialize as a vanilla .ZZT: %v", checkpoint.Label, err)
				continue
			}
			if len(checkpoint.Landmarks) == 0 {
				t.Errorf("checkpoint %q recorded no landmarks", checkpoint.Label)
			}
			for _, landmark := range checkpoint.Landmarks {
				if landmark.Board < 0 || landmark.Board >= len(world.Boards) {
					t.Errorf("checkpoint %q: landmark %q names board %d, which the world does not have",
						checkpoint.Label, landmark.What, landmark.Board)
					continue
				}
				tile, ok := m1614TileAt(world.Boards[landmark.Board], landmark.X, landmark.Y)
				if !ok {
					t.Errorf("checkpoint %q: landmark %q names (%d,%d), which is off the board",
						checkpoint.Label, landmark.What, landmark.X, landmark.Y)
					continue
				}
				if tile[0] != landmark.Element || tile[1] != landmark.Color {
					t.Errorf("checkpoint %q: %s at board %d (%d,%d) is element %d colour %#02x in the serialized "+
						"world, but both browsers and the session agreed on element %d colour %#02x",
						checkpoint.Label, landmark.What, landmark.Board, landmark.X, landmark.Y,
						tile[0], tile[1], landmark.Element, landmark.Color)
				}
			}
		}
	})

	t.Run("the browsers reported no screen divergence", func(t *testing.T) {
		for _, checkpoint := range report.Checkpoints {
			if len(checkpoint.Browsers) < 2 {
				t.Errorf("checkpoint %q compared %d browser screen(s); a convergence checkpoint needs at least two",
					checkpoint.Label, len(checkpoint.Browsers))
			}
			if checkpoint.Screens == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(outDir, checkpoint.Screens)); err != nil {
				t.Errorf("checkpoint %q names screen artifact %s, which was not written: %v",
					checkpoint.Label, checkpoint.Screens, err)
			}
		}
	})

	t.Run("the published world on disk is the session's own bytes", func(t *testing.T) {
		published := m1613ReadArtifact(t, outDir, report.Files["published"])
		onDisk, err := os.ReadFile(filepath.Join(h.worldsDir, m1614World+".ZZT"))
		if err != nil {
			t.Fatalf("read the published world: %v", err)
		}
		if !bytes.Equal(published, onDisk) {
			t.Errorf("the run recorded %d published bytes; %s.ZZT on disk is %d and they differ",
				len(published), m1614World, len(onDisk))
		}
		if _, err := m1613ReadVanillaWorld(onDisk); err != nil {
			t.Errorf("the published world does not parse as a vanilla .ZZT: %v", err)
		}
	})

	t.Run("ownership landed on the world the owner published", func(t *testing.T) {
		access, ok, err := loadWorldAccess(h.worldsDir, m1614World)
		if err != nil || !ok {
			t.Fatalf("access sidecar for %s: (%+v, %v, %v)", m1614World, access, ok, err)
		}
		if access.OwnerAccountID != "google:ada" {
			t.Errorf("owner is %q, want google:ada — the account that published it", access.OwnerAccountID)
		}
		if len(access.CollaboratorAccountIDs) != 1 || access.CollaboratorAccountIDs[0] != "google:bob" {
			t.Errorf("collaborators are %v, want [google:bob] — the account the owner invited",
				access.CollaboratorAccountIDs)
		}
	})

	t.Run("both accounts really signed in through the identity provider", func(t *testing.T) {
		m1614Provider.mu.Lock()
		logins := append([]string(nil), m1614Provider.logins...)
		m1614Provider.mu.Unlock()
		seen := map[string]bool{}
		for _, key := range logins {
			seen[key] = true
		}
		for _, account := range m1614Accounts {
			if !seen[account.Key] {
				t.Errorf("%s never reached the authorization endpoint; logins were %v", account.Key, logins)
			}
		}
	})

	if len(report.Findings) > 0 {
		t.Logf("the run recorded %d divergence(s), filed as M16.14a:\n  %s",
			len(report.Findings), strings.Join(report.Findings, "\n  "))
	}
}

// ---------------------------------------------------------------------------
// The server-side half: a client that does not censor itself
// ---------------------------------------------------------------------------

// TestM1614ReadOnlyMemberCannotMoveAByte is the other half of the DoD's
// "unauthorized operations leave bytes unchanged". The browser proves the
// CLIENT refuses: every editor key consults editorReadOnly before it sends
// anything, so a real browser never puts an unauthorized operation on the wire.
// That is the wrong way round to certify a server, so this drives the session
// directly — the hostile client the product must survive — and requires the
// serialized world to be byte-identical after every mutating operation the
// protocol has.
func TestM1614ReadOnlyMemberCannotMoveAByte(t *testing.T) {
	session := NewEditorSession(m1614World, m1613EditorWorld(t))
	owner := &webSocketClient{accountID: "google:ada", name: "Ada Lovelace"}
	intruder := &webSocketClient{accountID: "google:mallory", name: "Mallory"}
	if err := session.Enter(owner); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(owner)
	if err := session.Enter(intruder); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(intruder)
	session.SetMemberReadOnly(intruder, true)

	before := m1613SessionWorldBytes(session)
	if before == nil {
		t.Fatal("the session would not serialize")
	}

	// Every mutation the editor protocol offers, from the read-only member.
	// A refusal may be an error, an empty reply or a reply with no dirty cells;
	// what none of them may be is a changed world.
	operations := []struct {
		what string
		run  func() error
	}{
		{"place a tile", func() error {
			_, err := session.Edit(intruder, EditorEditMessage{Op: "place", X: 5, Y: 5, Element: E_SOLID, Color: 0x0e})
			return err
		}},
		{"erase a tile", func() error {
			_, err := session.Edit(intruder, EditorEditMessage{Op: "erase", X: 10, Y: 5})
			return err
		}},
		{"flood fill", func() error {
			_, err := session.Edit(intruder, EditorEditMessage{Op: "fill", X: 40, Y: 20, Element: E_SOLID, Color: 0x0e})
			return err
		}},
		{"place an element", func() error {
			_, err := session.Edit(intruder, EditorEditMessage{Op: "element", X: 6, Y: 6, Element: E_LION, Color: 0x0c})
			return err
		}},
		{"type a text tile", func() error {
			_, err := session.Edit(intruder, EditorEditMessage{Op: "text", X: 7, Y: 7, Char: 'X', Color: 0x0e})
			return err
		}},
		{"rename the board", func() error {
			_, err := session.SetProperty(intruder, EditorPropertyMessage{Field: "boardTitle", Text: "Mallory Was Here"})
			return err
		}},
		{"rename the world", func() error {
			_, err := session.SetProperty(intruder, EditorPropertyMessage{Field: "worldName", Text: "STOLEN"})
			return err
		}},
		{"turn the board dark", func() error {
			_, err := session.SetProperty(intruder, EditorPropertyMessage{Field: "dark", Bool: true})
			return err
		}},
		{"set a stat parameter", func() error {
			_, err := session.SetStat(intruder, EditorStatMessage{StatID: 1, Field: "p1", Value: 9})
			return err
		}},
		{"rewrite a program", func() error {
			_, err := session.SaveProgram(intruder, 1, []string{"#end", "'owned"})
			return err
		}},
		{"add a board", func() error {
			_, err := session.AddBoard(intruder, "Mallory Annex")
			return err
		}},
		{"clear the board", func() error {
			_, err := session.ClearBoard(intruder)
			return err
		}},
		{"make a new world", func() error {
			_, err := session.NewWorld(intruder)
			return err
		}},
		{"import a board", func() error {
			exported, err := session.ExportBoard(owner)
			if err != nil {
				return err
			}
			data, decErr := base64.StdEncoding.DecodeString(exported.Data)
			if decErr != nil {
				return decErr
			}
			_, err = session.ImportBoard(intruder, data)
			return err
		}},
		{"upload a world", func() error {
			_, _, err := session.UploadWorld(intruder, before)
			return err
		}},
	}

	for _, operation := range operations {
		// The error is not the assertion — several of these refuse by returning
		// an empty reply rather than an error — but a transport failure would be.
		if err := operation.run(); err != nil && !strings.Contains(err.Error(), "read-only") {
			t.Logf("%s returned %v", operation.what, err)
		}
		after := m1613SessionWorldBytes(session)
		if !bytes.Equal(before, after) {
			t.Fatalf("a read-only member managed to %s: the world went from %d bytes to %d",
				operation.what, len(before), len(after))
		}
	}

	// And the leases: a read-only member may hold none, so they cannot block the
	// people who may edit either.
	reply, err := session.AcquireLease(intruder, EditorLeaseMessage{Kind: "board", BoardID: 0})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Op != "refused" || reply.Error != "read-only" {
		t.Errorf("a read-only member's board lease came back %+v, want a read-only refusal", reply)
	}
	report := m1614ReadSession(session)
	if len(report.Leases) != 0 {
		t.Errorf("a read-only member's refused request left %d lease(s) held: %+v", len(report.Leases), report.Leases)
	}
}

// TestM1614PublishOverAnOccupiedWorldMovesNoBytes pins the occupancy rule the
// browser run drives from the other side: saveEditorWorld refuses BEFORE it
// writes anything when someone is playing the target world, so a refused publish
// cannot leave a half-written or a silently replaced file behind.
func TestM1614PublishOverAnOccupiedWorldMovesNoBytes(t *testing.T) {
	dir := t.TempDir()
	world := m1613EditorWorld(t)
	server := NewWebSocketServer(world, 0)
	server.WorldsDir = dir

	session := server.editorSessionForWorld(m1614World, world)
	author := &webSocketClient{accountID: "google:ada", name: "Ada Lovelace"}
	if err := session.Enter(author); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(author)

	if _, err := server.saveEditorWorld(author, session, m1614World); err != nil {
		t.Fatalf("the first publish failed: %v", err)
	}
	published, err := os.ReadFile(filepath.Join(dir, m1614World+".ZZT"))
	if err != nil {
		t.Fatal(err)
	}

	// Somebody joins the published world, and the author keeps editing.
	inst, err := server.GetOrCreateInstance(m1614World)
	if err != nil {
		t.Fatal(err)
	}
	inst.mu.Lock()
	inst.Clients[PlayerID(1)] = &webSocketClient{}
	inst.mu.Unlock()

	if _, err := session.Edit(author, EditorEditMessage{Op: "place", X: 44, Y: 4, Element: E_SOLID, Color: 0x0e}); err != nil {
		t.Fatal(err)
	}
	_, err = server.saveEditorWorld(author, session, m1614World)
	if err == nil {
		t.Fatal("publishing over an occupied world was allowed")
	}
	if !strings.Contains(err.Error(), "being played") {
		t.Errorf("the refusal reads %q, want the occupancy refusal", err)
	}
	after, readErr := os.ReadFile(filepath.Join(dir, m1614World+".ZZT"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(published, after) {
		t.Errorf("the refused publish still moved %s.ZZT: %d bytes became %d", m1614World, len(published), len(after))
	}

	// And once the room empties, the same publish goes through — so the refusal
	// was about occupancy and nothing else.
	inst.mu.Lock()
	delete(inst.Clients, PlayerID(1))
	inst.mu.Unlock()
	if _, err := server.saveEditorWorld(author, session, m1614World); err != nil {
		t.Fatalf("publishing into an empty world failed: %v", err)
	}
	reopened, err := os.ReadFile(filepath.Join(dir, m1614World+".ZZT"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(published, reopened) {
		t.Error("the second publish wrote the same bytes; the edit between them should have changed the file")
	}
}

// ---------------------------------------------------------------------------
// What this sweep found, and gap task M16.14a will close
// ---------------------------------------------------------------------------

// TestM1614aBoardScopedChangesReachOnlyTheActingMember pins M16.14a's first
// finding, and it is written to FAIL once the fix lands.
//
// A per-cell edit is broadcast to every member viewing that board
// (websocket_server.go MessageTypeEditorEdit → broadcastEditorBoard, M10.1 and
// M17.12). Nothing else is. Clear board, Board Information, Add board, Import
// board and New world all reply to the acting client alone, so a collaborator
// watching the same board keeps the tiles that are no longer there — the exact
// divergence a session with one shared engine is supposed to make impossible.
//
// The observable here is the reply routing itself, because that IS the defect:
// the session state is right, and only the other member's screen is wrong.
func TestM1614aBoardScopedChangesReachOnlyTheActingMember(t *testing.T) {
	source := m1613ReadSource(t, "websocket_server.go")
	body := m1613FuncBody(t, source, "func (s *WebSocketServer) serveEditorBoard(", "session.AddBoard")
	for _, op := range []string{"clear", "new", "import", "add"} {
		marker := fmt.Sprintf("case %q:", op)
		start := strings.Index(body, marker)
		if start < 0 {
			t.Fatalf("serveEditorBoard no longer has a %q case — M16.14a may have landed; invert this pin", op)
		}
		end := len(body)
		for _, next := range []string{"\n\tcase \"", "\n\t}"} {
			if idx := strings.Index(body[start:], next); idx > 0 && start+idx < end {
				end = start + idx
			}
		}
		branch := body[start:end]
		if strings.Contains(branch, "broadcastEditor") {
			t.Fatalf("serveEditorBoard's %q case now broadcasts to the session — M16.14a has landed. "+
				"Invert this pin and let the browser route assert that a collaborator's screen follows.", op)
		}
		if !strings.Contains(branch, "client.write(") {
			t.Fatalf("serveEditorBoard's %q case no longer replies to the acting client at all; "+
				"this pin is looking at code that moved", op)
		}
	}

	editorLoop := m1613FuncBody(t, source, "func (s *WebSocketServer) serveEditor(", "MessageTypeEditorLease")
	property := strings.Index(editorLoop, "case MessageTypeEditorProperty:")
	if property < 0 {
		t.Fatal("the editor loop no longer handles editorProperty; this pin is looking at code that moved")
	}
	next := strings.Index(editorLoop[property+1:], "\n\t\tcase ")
	if next < 0 {
		t.Fatal("could not bound the editorProperty case")
	}
	branch := editorLoop[property : property+1+next]
	if strings.Contains(branch, "broadcastEditor") {
		t.Fatal("editorProperty now broadcasts its repaint to the session — M16.14a has landed; invert this pin.")
	}
	if !strings.Contains(branch, "client.write(") {
		t.Fatal("editorProperty no longer replies to the acting client; this pin is looking at code that moved")
	}
}

// TestM1614aInvitedCollaboratorStaysReadOnlyUntilReentry pins M16.14a's second
// finding, and it too is written to FAIL once the fix lands.
//
// inviteEditorCollaborator clears the invitee's server-side read-only flag while
// they are sitting in the session (EditorSession.SetAccountReadOnly), and tells
// them nothing. The client's own editorReadOnly is set from an editorSnapshot
// and from nowhere else, and every editor key consults it before it sends: so an
// invited collaborator is refused by their own browser, with a "Read-only"
// window, until they leave the editor and come back.
func TestM1614aInvitedCollaboratorStaysReadOnlyUntilReentry(t *testing.T) {
	dir := t.TempDir()
	world := m1613EditorWorld(t)
	server := NewWebSocketServer(world, 0)
	server.WorldsDir = dir
	server.Auth = NewAuthService(m1614ClientID, "", "", []byte("m16-14-cookie-secret"))

	session := server.editorSessionForWorld(m1614World, world)
	owner := &webSocketClient{accountID: "google:ada", name: "Ada Lovelace"}
	guest := &webSocketClient{accountID: "google:bob", name: "Bob Bones"}
	if err := session.Enter(owner); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(owner)
	if _, err := server.saveEditorWorld(owner, session, m1614World); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := session.Enter(guest); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(guest)
	session.SetMemberReadOnly(guest, !server.editorCanEdit(m1614World, guest.accountID))
	if session.CanEdit(guest) {
		t.Fatal("an uninvited account entered an owned world with edit rights")
	}

	if err := server.inviteEditorCollaborator(owner, session, guest.accountID); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if !session.CanEdit(guest) {
		t.Fatal("the invite did not clear the server-side read-only flag")
	}

	// The server would now accept the invitee's edits. The browser will not send
	// them, because nothing told it: no snapshot is sent, and editorReadOnly is
	// only ever assigned from one.
	source := m1613ReadSource(t, "websocket_server.go")
	invite := m1613FuncBody(t, source, "func (s *WebSocketServer) inviteEditorCollaborator(", "SetAccountReadOnly")
	if strings.Contains(invite, "Snapshot(") || strings.Contains(invite, "broadcastEditor") {
		t.Fatal("inviteEditorCollaborator now tells the session about the invite — M16.14a has landed; " +
			"invert this pin and let the browser route edit without re-entering.")
	}
	// The client side of the same claim: editorReadOnly is written in exactly
	// three places — its declaration, the session reset, and the snapshot — so
	// there is nowhere for an invite to land. A fourth write means something now
	// updates it, which is what the fix looks like.
	client := m1613ReadSource(t, filepath.Join("web", "src", "main.ts"))
	writes := strings.Count(client, "editorReadOnly = ")
	fromMessage := strings.Count(client, "editorReadOnly = !!message.readOnly;")
	toFalse := strings.Count(client, "editorReadOnly = false;")
	if writes != 3 || fromMessage != 1 || toFalse != 2 {
		t.Fatalf("main.ts writes editorReadOnly %d time(s): %d from a message and %d to false. This pin expects "+
			"3/1/2 — the declaration, the session reset, and the entry snapshot. A write it does not know about "+
			"means M16.14a may have landed; re-read the pin before changing the numbers.",
			writes, fromMessage, toFalse)
	}
}

// TestM1614aStatLeaseIsStrandedWhenAnotherMemberMovesTheEngine pins M16.14a's
// third finding — the one the three-browser run turned up that no single-browser
// sweep could — and it is written to FAIL once the fix lands.
//
// A "stat" lease key is resolved against s.engine.World.Info.CurrentBoard
// (EditorSession.leaseKeyLocked): the board the ONE shared engine happens to be
// focused on, which is whichever member acted last (Apply →
// focusMemberBoardLocked, M17.12). It is not resolved against the board the
// asker is on. So a collaborator switching boards moves the key out from under a
// lease that is already held, and:
//
//   - the holder's release resolves to no key at all and is dropped, leaving the
//     lease held by somebody who has closed the dialog and walked away;
//   - a fresh request for the same stat replies with nothing, and the client —
//     which only reacts to "granted" and "refused" — silently does nothing.
//
// The board lease has no such dependency: its key is the board that was asked
// for. That asymmetry is the shape of the fix.
func TestM1614aStatLeaseIsStrandedWhenAnotherMemberMovesTheEngine(t *testing.T) {
	session := NewEditorSession(m1614World, m1613EditorWorld(t))
	ada := &webSocketClient{name: "Ada"}
	bob := &webSocketClient{name: "Bob"}
	if _, err := session.EnterNamed(ada, ada.name); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(ada)
	if _, err := session.EnterNamed(bob, bob.name); err != nil {
		t.Fatal(err)
	}
	defer session.Exit(bob)

	// Ada takes the object's stat lease while the engine is on her board.
	granted, err := session.AcquireLease(ada, EditorLeaseMessage{Kind: "stat", BoardID: 0, StatID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if granted.Op != "granted" {
		t.Fatalf("Ada's stat lease came back %+v, want granted", granted)
	}

	// Bob switches to the annex, which is all it takes to move the shared engine.
	if _, err := session.SwitchBoard(bob, 1); err != nil {
		t.Fatal(err)
	}

	session.ReleaseLease(ada, granted)
	if leases := m1614ReadSession(session).Leases; len(leases) != 1 {
		t.Fatalf("Ada's release freed the lease while the engine was on another board — M16.14a (c) has "+
			"landed; invert this pin and let the browser route assert that closing a dialog always gives "+
			"the lease back. Leases: %+v", leases)
	}

	silent, err := session.AcquireLease(ada, EditorLeaseMessage{Kind: "stat", BoardID: 0, StatID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if silent.Type != "" {
		t.Fatalf("a stat lease request for a board the engine is not focused on replied %+v; this pin "+
			"records that it replies with nothing at all", silent)
	}

	// Bob cannot have it either, once the engine is back where the key resolves.
	if _, err := session.SwitchBoard(ada, 0); err != nil {
		t.Fatal(err)
	}
	refused, err := session.AcquireLease(bob, EditorLeaseMessage{Kind: "stat", BoardID: 0, StatID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if refused.Op != "refused" || refused.HolderName != "Ada" {
		t.Fatalf("the stranded lease came back %+v for the other member, want a refusal naming Ada", refused)
	}

	// The board lease is not affected: its key is the board that was asked for.
	if _, err := session.SwitchBoard(bob, 1); err != nil {
		t.Fatal(err)
	}
	board, err := session.AcquireLease(ada, EditorLeaseMessage{Kind: "board", BoardID: 0})
	if err != nil {
		t.Fatal(err)
	}
	if board.Op != "granted" || board.BoardID != 0 {
		t.Fatalf("Ada's board-0 lease came back %+v while the engine was on board 1, want granted for board 0", board)
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// m1614TileAt indexes an independently parsed board by 1-based ZZT coordinates.
// The reader keeps the file's own order — row by row, left to right — which is
// the order BoardClose writes and m1613SessionView reads back.
func m1614TileAt(board m1613VanillaBoard, x, y int) ([2]int, bool) {
	if x < 1 || x > BOARD_WIDTH || y < 1 || y > BOARD_HEIGHT {
		return [2]int{}, false
	}
	index := (y-1)*BOARD_WIDTH + (x - 1)
	if index >= len(board.Tiles) {
		return [2]int{}, false
	}
	return board.Tiles[index], true
}
