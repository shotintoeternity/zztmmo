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
// WHAT THIS SWEEP FOUND, AND WHAT M16.14a DID ABOUT IT. Three divergences, all
// filed as a gap task that has since closed them. The tests at the foot of this
// file were the pins; each now asserts the fix from the side it recorded the
// break on.
//
//   a. Board- and world-scoped changes reached only the member who made them.
//      editorDiff was broadcast to everyone viewing the board (M10.1/M17.12),
//      but Clear board, Board Information, Add board, Import board and New world
//      replied to the acting client alone, so a collaborator watching the same
//      board kept the old tiles. They now fan out: the frame to the members
//      watching that board, and the world-scoped half — the switcher's board
//      list and the world name — to everyone else, with no frame that would
//      paint another board over theirs.
//   b. An invited collaborator stayed read-only until they re-entered the
//      editor, because inviteEditorCollaborator cleared the server-side flag
//      (EditorSession.SetAccountReadOnly) and sent the invitee nothing, while
//      the client's own editorReadOnly — consulted by every editor key before it
//      sends anything — is only ever set from a snapshot. The invite now sends
//      them one, addressed to them and carrying the cursor they last reported.
//   c. A stat lease was stranded when another member switched boards first: its
//      key was resolved against the ONE shared engine's current board, which
//      follows whoever acted last, so the holder's release resolved to no key
//      and was dropped. The key is now the board that was asked for, as the
//      board lease's always was. The three-browser arrangement is what made this
//      reachable at all.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
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
	return m169NewHarnessFor(t, m1614World, world,
		func(h *m169Harness, server *WebSocketServer, api *WebAPI) {
			server.Auth = auth
			api.Auth = auth
			h.auth = auth
			// The endpoints have to be absolute — the browser is redirected to
			// them — so they need the control listener's port. The harness binds
			// both ports before it runs this option and only serves afterwards,
			// so these writes happen before any handler goroutine can read them
			// (M16.14c: filling them in after the harness was built raced).
			auth.AuthEndpoint = h.controlURL + "/idp/authorize"
			auth.TokenEndpoint = h.controlURL + "/idp/token"
		})
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

	// M16.14a closed everything this sweep filed. A finding now is a NEW one, and
	// it belongs in a new gap task rather than in a passing run.
	if len(report.Findings) > 0 {
		t.Errorf("the run recorded %d divergence(s) M16.14a does not cover:\n  %s",
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
// What this sweep found, and gap task M16.14a closed
//
// The three tests below replace the pins M16.14 left behind. Each one now
// asserts the fixed behaviour from the same side the pin recorded the break on.
// ---------------------------------------------------------------------------

// m1614EditorConn is one editor WebSocket, read the way a browser reads it.
// Which member is handed which message is the whole claim of M16.14a (a), so
// these tests are written against the wire rather than against the session.
type m1614EditorConn struct {
	t     *testing.T
	ctx   context.Context
	label string
	conn  *websocket.Conn
}

// m1614DialEditor opens one editor connection on worldName, optionally signed
// in, and returns it with its entry snapshot.
func m1614DialEditor(t *testing.T, ctx context.Context, wsURL, label, worldName string, cookie *http.Cookie) (*m1614EditorConn, EditorSnapshotMessage) {
	t.Helper()
	opts := &websocket.DialOptions{}
	if cookie != nil {
		opts.HTTPHeader = http.Header{"Cookie": []string{cookie.String()}}
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("%s: dial editor: %v", label, err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	conn.SetReadLimit(ServerReadLimit)
	editor := &m1614EditorConn{t: t, ctx: ctx, label: label, conn: conn}
	editor.send(EditorEnterMessage{Type: MessageTypeEditorEnter, World: worldName})
	var snapshot EditorSnapshotMessage
	editor.next(MessageTypeEditorSnapshot, &snapshot)
	return editor, snapshot
}

func (c *m1614EditorConn) send(message interface{}) {
	c.t.Helper()
	if err := wsjson.Write(c.ctx, c.conn, message); err != nil {
		c.t.Fatalf("%s: write %T: %v", c.label, message, err)
	}
}

// next reads the next thing this browser is handed, skipping presence — which
// rides along on every collaborator keystroke and says nothing about routing.
// Any OTHER unexpected type fails rather than being skipped: which member is
// told what, and which member is told nothing, is exactly what is being pinned.
func (c *m1614EditorConn) next(wantType string, out interface{}) {
	c.t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(c.ctx, c.conn, &raw); err != nil {
			c.t.Fatalf("%s: waiting for %s: %v", c.label, wantType, err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			c.t.Fatalf("%s: undecodable message %s", c.label, raw)
		}
		if envelope.Type == MessageTypeEditorPresence && wantType != MessageTypeEditorPresence {
			continue
		}
		if envelope.Type != wantType {
			c.t.Fatalf("%s was handed a %q where this run requires a %q: %s",
				c.label, envelope.Type, wantType, m1614Head(raw))
		}
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s: decode %s: %v", c.label, wantType, err)
		}
		return
	}
}

func m1614Head(raw []byte) string {
	if len(raw) > 200 {
		return string(raw[:200]) + "…"
	}
	return string(raw)
}

// m1614BoardRegion is the 60-column board half of a frame, keyed by cell, which
// is what two members watching one board must agree on to the byte. The sidebar
// is drawn locally by each browser and is not part of the claim.
func m1614BoardRegion(cells []ScreenCell) map[[2]int16][2]byte {
	region := make(map[[2]int16][2]byte, len(cells))
	for _, cell := range cells {
		if cell.X >= BOARD_WIDTH {
			continue
		}
		region[[2]int16{cell.X, cell.Y}] = [2]byte{cell.Ch, cell.Color}
	}
	return region
}

func m1614BoardOption(properties EditorProperties, boardID int16) (EditorBoardOption, bool) {
	for _, option := range properties.Boards {
		if option.ID == boardID {
			return option, true
		}
	}
	return EditorBoardOption{}, false
}

// TestM1614aBoardScopedChangesReachEveryMemberWatching is M16.14a (a), inverted.
//
// The pin it replaces recorded that a per-cell edit was broadcast to every
// member viewing its board (M10.1, M17.12) and that nothing else was: Clear
// board, Board Information, Add board, Import board and New world all replied to
// the acting client alone, so a collaborator watching a board somebody else
// cleared kept every tile that was no longer there, and one who was on the board
// when it was renamed kept the old name in their switcher.
//
// Three connections share one session: two watching board 0 and one who has gone
// to the annex. The claim is about ROUTING, so it is made on what each socket is
// actually handed — the members watching the board get the frame, the member who
// is elsewhere gets the world-scoped half and no frame that would paint another
// board over theirs.
func TestM1614aBoardScopedChangesReachEveryMemberWatching(t *testing.T) {
	world := m1613EditorWorld(t)
	// Hosting under the run's own name makes the default instance the one the
	// editor enters, so the session is shared without a file on disk.
	world.Info.Name = m1614World
	server := NewWebSocketServer(world, 0)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, adaEntry := m1614DialEditor(t, ctx, wsURL, "Ada", m1614World, nil)
	bob, bobEntry := m1614DialEditor(t, ctx, wsURL, "Bob", m1614World, nil)
	carol, _ := m1614DialEditor(t, ctx, wsURL, "Carol", m1614World, nil)
	if adaEntry.BoardID != 0 {
		t.Fatalf("the session opened on board %d, want the fixture's board 0", adaEntry.BoardID)
	}

	// Carol leaves for the annex and stays there. Everything below happens on the
	// draft board she is not looking at.
	carol.send(EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "switch", BoardID: 1})
	var carolSwitch EditorSnapshotMessage
	carol.next(MessageTypeEditorSnapshot, &carolSwitch)
	if carolSwitch.BoardID != 1 {
		t.Fatalf("Carol's switch put her on board %d, want the annex", carolSwitch.BoardID)
	}

	// Board-scoped operations need the board's lease once there is more than one
	// member; Ada takes the draft board's.
	ada.send(EditorLeaseMessage{Type: MessageTypeEditorLease, Op: "request", Kind: "board", BoardID: 0})
	var lease EditorLeaseMessage
	ada.next(MessageTypeEditorLease, &lease)
	if lease.Op != "granted" {
		t.Fatalf("Ada's board lease came back %+v, want granted", lease)
	}

	// --- Board Information: the rename every switcher has to follow -----------
	ada.send(EditorPropertyMessage{Type: MessageTypeEditorProperty, Field: "boardTitle", Text: "Ada And Bob"})
	var adaRename, bobRename, carolRename EditorPropertiesMessage
	ada.next(MessageTypeEditorProperties, &adaRename)
	bob.next(MessageTypeEditorProperties, &bobRename)
	carol.next(MessageTypeEditorProperties, &carolRename)

	for _, seen := range []struct {
		who   string
		reply EditorPropertiesMessage
	}{{"Ada", adaRename}, {"Bob", bobRename}} {
		if seen.reply.Properties.BoardName != "Ada And Bob" {
			t.Errorf("%s, watching the renamed board, was told its name is %q, want \"Ada And Bob\"",
				seen.who, seen.reply.Properties.BoardName)
		}
		if len(seen.reply.Screen) == 0 {
			t.Errorf("%s, watching the renamed board, got no frame with the change", seen.who)
		}
	}
	if len(carolRename.Screen) != 0 {
		t.Errorf("Carol, on the annex, was sent %d cells of another board's frame", len(carolRename.Screen))
	}
	for _, seen := range []struct {
		who   string
		reply EditorPropertiesMessage
	}{{"Bob", bobRename}, {"Carol", carolRename}} {
		option, ok := m1614BoardOption(seen.reply.Properties, 0)
		if !ok || option.Name != "Ada And Bob" {
			t.Errorf("%s's board list names board 0 %+v, want the new title — a rename must reach every switcher",
				seen.who, option)
		}
	}
	// Carol is on the annex, and being told about board 0 must not move her there.
	if carolRename.Properties.BoardID != 0 {
		t.Fatalf("the properties Carol was sent are for board %d; this test assumes the acting member's",
			carolRename.Properties.BoardID)
	}

	// --- Clear board: the whole frame, to everybody watching it ---------------
	beforeClear := m1614BoardRegion(bobEntry.Screen)
	ada.send(EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "clear"})
	var adaClear, bobClear EditorSnapshotMessage
	ada.next(MessageTypeEditorSnapshot, &adaClear)
	bob.next(MessageTypeEditorSnapshot, &bobClear)
	var carolClear EditorPropertiesMessage
	carol.next(MessageTypeEditorProperties, &carolClear)

	if bobClear.BoardID != 0 {
		t.Fatalf("Bob's repaint is for board %d, want the board that was cleared", bobClear.BoardID)
	}
	afterClear := m1614BoardRegion(bobClear.Screen)
	if reflect.DeepEqual(beforeClear, afterClear) {
		t.Error("the board a collaborator was watching was cleared and their frame did not change")
	}
	if !reflect.DeepEqual(afterClear, m1614BoardRegion(adaClear.Screen)) {
		t.Error("the two members watching the cleared board were sent different frames")
	}
	if len(carolClear.Screen) != 0 {
		t.Errorf("Carol, on the annex, was sent %d cells of the cleared board", len(carolClear.Screen))
	}
	// The frame Bob is sent is ADDRESSED TO ADA — it carries her id, her cursor
	// and her read-only flag. That is why the client takes only the board half of
	// a snapshot that is not its own (applyEditorSnapshot's forMe, M17.9): a
	// broadcast repaint must not drag a collaborator's cursor.
	if bobClear.MemberID != adaClear.MemberID {
		t.Errorf("the broadcast frame reached Bob as %q and Ada as %q; it is one message",
			bobClear.MemberID, adaClear.MemberID)
	}
	if bobClear.MemberID == bobEntry.MemberID {
		t.Error("the broadcast frame claims to be Bob's own, so his client would adopt Ada's cursor from it")
	}
	// ZZT-QUIRK, and it has to reach the other switchers too: clearing a board
	// runs BoardCreate (EDITOR.PAS:591-598), which resets its NAME along with its
	// tiles. The title Ada gave it two operations ago is gone for everybody.
	for _, seen := range []struct {
		who   string
		reply EditorProperties
	}{{"Ada", adaClear.Properties}, {"Bob", bobClear.Properties}, {"Carol", carolClear.Properties}} {
		if option, ok := m1614BoardOption(seen.reply, 0); !ok || option.Name != "Untitled" {
			t.Errorf("%s's switcher names the cleared board %+v; BoardCreate emptied its name", seen.who, option)
		}
	}

	// --- Add board: the frame to its author, the list to everyone -------------
	ada.send(EditorBoardMessage{Type: MessageTypeEditorBoard, Op: "add", Name: "Ada Annex"})
	var adaAdd EditorSnapshotMessage
	ada.next(MessageTypeEditorSnapshot, &adaAdd)
	var bobAdd, carolAdd EditorPropertiesMessage
	bob.next(MessageTypeEditorProperties, &bobAdd)
	carol.next(MessageTypeEditorProperties, &carolAdd)

	if adaAdd.BoardID != 2 || adaAdd.Properties.BoardName != "Ada Annex" {
		t.Fatalf("the board Ada added came back as %d/%q, want 2/\"Ada Annex\"",
			adaAdd.BoardID, adaAdd.Properties.BoardName)
	}
	for _, seen := range []struct {
		who   string
		reply EditorPropertiesMessage
	}{{"Bob", bobAdd}, {"Carol", carolAdd}} {
		if len(seen.reply.Screen) != 0 {
			t.Errorf("%s was sent %d cells of a board they are not on", seen.who, len(seen.reply.Screen))
		}
		option, ok := m1614BoardOption(seen.reply.Properties, 2)
		if !ok || option.Name != "Ada Annex" {
			t.Errorf("%s's switcher does not offer the board somebody else added: %+v",
				seen.who, seen.reply.Properties.Boards)
		}
		if len(seen.reply.Properties.Boards) != 3 {
			t.Errorf("%s's switcher offers %+v, want all three boards of the world",
				seen.who, seen.reply.Properties.Boards)
		}
	}
}

// TestM1614aInvitedCollaboratorEditsWithoutReentering is M16.14a (b), inverted.
//
// The pin it replaces recorded that inviteEditorCollaborator cleared the
// invitee's server-side flag while they sat in the session and told them
// nothing, so their own browser — whose editorReadOnly is set from a snapshot
// and from nowhere else, and which every editor key consults before it sends —
// kept refusing them with a "Read-only" window until they left and came back.
func TestM1614aInvitedCollaboratorEditsWithoutReentering(t *testing.T) {
	dir := t.TempDir()
	world := m1613EditorWorld(t)
	world.Info.Name = m1614World
	server := NewWebSocketServer(world, 0)
	server.WorldsDir = dir
	server.Auth = NewAuthService(m1614ClientID, "", "", []byte("m16-14-cookie-secret"))
	// The world is Ada's, and Bob is nowhere in it.
	if err := writeWorldAccess(dir, m1614World, WorldAccess{
		OwnerAccountID: "google:ada",
		OwnerName:      "Ada Lovelace",
	}); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ada, adaEntry := m1614DialEditor(t, ctx, wsURL, "Ada", m1614World,
		signedAuthCookie(t, server.Auth, AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada Lovelace"}))
	bob, bobEntry := m1614DialEditor(t, ctx, wsURL, "Bob", m1614World,
		signedAuthCookie(t, server.Auth, AuthenticatedAccount{ID: "google:bob", Email: "bob@example.test", Name: "Bob Bones"}))
	if adaEntry.ReadOnly {
		t.Fatal("the owner entered her own world read-only")
	}
	if !bobEntry.ReadOnly {
		t.Fatal("an uninvited account entered an owned world with edit rights")
	}

	ada.send(EditorWorldMessage{Type: MessageTypeEditorWorld, Op: "invite", AccountID: "google:bob"})

	// The invitee is TOLD, unprompted and without leaving the editor.
	var told EditorSnapshotMessage
	bob.next(MessageTypeEditorSnapshot, &told)
	if told.MemberID != bobEntry.MemberID {
		t.Fatalf("the invite reached Bob as %q, but he is %q — a snapshot he does not recognise as his own "+
			"is one his client will not take a read-only flag from", told.MemberID, bobEntry.MemberID)
	}
	if told.ReadOnly {
		t.Error("the invitee was told he is still read-only")
	}
	// And it does not move him: an unprompted repaint that recentred his cursor
	// would be its own bug.
	if told.Inspect.X != bobEntry.Inspect.X || told.Inspect.Y != bobEntry.Inspect.Y {
		t.Errorf("being invited moved Bob's cursor from (%d,%d) to (%d,%d)",
			bobEntry.Inspect.X, bobEntry.Inspect.Y, told.Inspect.X, told.Inspect.Y)
	}
	if told.BoardID != bobEntry.BoardID {
		t.Errorf("being invited moved Bob from board %d to board %d", bobEntry.BoardID, told.BoardID)
	}

	var saved EditorSaveResultMessage
	ada.next(MessageTypeEditorSaveResult, &saved)
	if saved.Error != "" {
		t.Fatalf("the owner's invite was refused: %s", saved.Error)
	}

	// The server takes his edit, which is what the flag was about.
	bob.send(EditorEditMessage{Type: MessageTypeEditorEdit, Op: "place", X: 45, Y: 3, Element: E_NORMAL, Color: 0x0a})
	var diff EditorDiffMessage
	bob.next(MessageTypeEditorDiff, &diff)
	if len(diff.Cells) == 0 {
		t.Fatal("the invited collaborator's first edit changed nothing")
	}
	if diff.Inspect.Element != ElementDefs[E_NORMAL].Name {
		t.Errorf("the cell the invitee drew on is %q, want %q", diff.Inspect.Element, ElementDefs[E_NORMAL].Name)
	}

	// The client half of the same claim. A snapshot is now broadcast to everyone
	// watching a board (M16.14a (a)), and it carries the ACTING member's
	// read-only flag — so the assignment has to be inside the branch that runs
	// only for a snapshot addressed to us. Without that, a collaborator's edit
	// would hand a read-only guest edit rights in their own UI.
	client := m1613ReadSource(t, filepath.Join("web", "src", "main.ts"))
	if got := strings.Count(client, "editorReadOnly = !!message.readOnly;"); got != 1 {
		t.Fatalf("main.ts takes editorReadOnly from a message %d time(s), want exactly 1", got)
	}
	apply := m1613FuncBody(t, client, "function applyEditorSnapshot(", "editorReadOnly = !!message.readOnly;")
	guard := strings.Index(apply, "if (forMe) {")
	assign := strings.Index(apply, "editorReadOnly = !!message.readOnly;")
	if guard < 0 || assign < guard {
		t.Error("applyEditorSnapshot assigns editorReadOnly outside its forMe guard: a broadcast frame " +
			"carries the acting member's flag, not the recipient's")
	}
}

// TestM1614aStatLeaseSurvivesAnotherMembersBoardSwitch is M16.14a (c), inverted.
//
// The pin it replaces recorded that a "stat" lease key was resolved against
// s.engine.World.Info.CurrentBoard — the board the ONE shared engine happened to
// be focused on, which is whichever member acted last (Apply →
// focusMemberBoardLocked, M17.12) — rather than against the board that was asked
// for. A collaborator switching boards therefore moved the key out from under a
// lease that was already held: the holder's release resolved to no key and was
// dropped, and a fresh request replied with nothing at all, which the client,
// reacting only to "granted" and "refused", answered with silence.
//
// The board lease never had that dependency. This is the stat lease matching it.
func TestM1614aStatLeaseSurvivesAnotherMembersBoardSwitch(t *testing.T) {
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

	statLease := EditorLeaseMessage{Kind: "stat", BoardID: 0, StatID: 1}
	granted, err := session.AcquireLease(ada, statLease)
	if err != nil {
		t.Fatal(err)
	}
	if granted.Op != "granted" {
		t.Fatalf("Ada's stat lease came back %+v, want granted", granted)
	}

	// Bob switches to the annex, which is all it ever took to move the engine.
	if _, err := session.SwitchBoard(bob, 1); err != nil {
		t.Fatal(err)
	}

	// A request from the other side, while the engine is on Bob's board, is
	// answered — with a refusal that names the holder, not with silence.
	refused, err := session.AcquireLease(bob, statLease)
	if err != nil {
		t.Fatal(err)
	}
	if refused.Op != "refused" || refused.HolderName != "Ada" {
		t.Fatalf("a stat lease held by Ada came back %+v for Bob, want a refusal naming her", refused)
	}

	// And closing the dialog always gives it back, wherever the engine is.
	session.ReleaseLease(ada, granted)
	if leases := m1614ReadSession(session).Leases; len(leases) != 0 {
		t.Fatalf("the holder's release left %d lease(s) behind while the engine was on another board: %+v",
			len(leases), leases)
	}
	retaken, err := session.AcquireLease(bob, statLease)
	if err != nil {
		t.Fatal(err)
	}
	if retaken.Op != "granted" || retaken.BoardID != 0 || retaken.StatID != 1 {
		t.Fatalf("the freed stat lease came back %+v for Bob, want granted for board 0 stat 1", retaken)
	}

	// The key is the stat that was asked for, on the board that was asked for:
	// the same stat index on another board is a different lease.
	elsewhere, err := session.AcquireLease(ada, EditorLeaseMessage{Kind: "stat", BoardID: 1, StatID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere.Op != "granted" || elsewhere.BoardID != 1 {
		t.Fatalf("stat 1 of the annex came back %+v while Bob holds stat 1 of the draft board, want granted",
			elsewhere)
	}

	// The board lease is unchanged: its key was always the board that was asked
	// for, and it still is with the engine on board 1.
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

// ---------------------------------------------------------------------------
// M16.14d — the golden library's own clock pause
// ---------------------------------------------------------------------------

// TestM1614dPauseClockCannotLoseItsRace covers the browser harness itself rather
// than the product: web/test/lib/canvas.mjs's pauseClock, which every browser
// suite calls before it reads a single cell.
//
// Freezing the page clock takes two round trips — read it, then pause one
// millisecond later — and Playwright's fake clock re-syncs to real time on a
// timer of at most 100ms, so a slow round trip carried it past the requested
// instant and pauseAt threw "Cannot fast-forward to the past". It reddened
// whichever suite was unlucky whenever the machine was loaded (NOTES.md
// 2026-07-31).
//
// The script FORCES the losing case rather than hoping for it: it stalls every
// clock read by 300ms of real time, requires the old one-shot pause to fail
// under that harness (a test that cannot fail proves nothing about a race), and
// then requires pauseClock to survive it and leave a genuinely stopped clock.
//
// It needs Chromium but no server and no client build, so it stands on its own
// rather than on m169NewHarnessFor.
func TestM1614dPauseClockCannotLoseItsRace(t *testing.T) {
	m169RequireBrowserHarness(t)

	cmd := exec.Command("node", filepath.Join("test", "pause_clock.test.mjs"))
	cmd.Dir = "web"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pause_clock.test.mjs failed: %v\n--- script output ---\n%s", err, out)
	}
	t.Logf("pauseClock:\n%s", out)
}
