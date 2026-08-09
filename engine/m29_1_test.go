package zztgo

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func m291LobbyWorldBytes(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "fixtures", "lobby.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/lobby.zwd: %v", err)
	}
	data, err := CompileZWD(string(src))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/lobby.zwd): %v", err)
	}
	validateCompiledZWD(t, data)
	return data
}

func m291LobbyWorld(t *testing.T) TWorld {
	t.Helper()
	world, err := CompileZWDWorld(string(mustRead(t, "../fixtures/lobby.zwd")))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/lobby.zwd): %v", err)
	}
	return world
}

func TestM291LobbyWorldCompilesShipsIsCanonicalAndListed(t *testing.T) {
	data := m291LobbyWorldBytes(t)
	if err := os.WriteFile(filepath.Join("..", "fixtures", "LOBBY.ZZT"), data, 0o644); err != nil {
		t.Fatalf("write fixtures/LOBBY.ZZT: %v", err)
	}
	if err := os.WriteFile("LOBBY.ZZT", data, 0o644); err != nil {
		t.Fatalf("write engine/LOBBY.ZZT: %v", err)
	}
	if !WorldIsCanonical(LobbyWorldName) {
		t.Fatal("WorldIsCanonical(LOBBY) = false; the first-party lobby must be protected")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LOBBY.ZZT"), data, 0o644); err != nil {
		t.Fatalf("write temp LOBBY: %v", err)
	}
	entries := WorldListEntriesInDir(dir, ListWorlds(dir), nil)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly LOBBY", entries)
	}
	if got := entries[0]; got.World != LobbyWorldName || got.Title != "ZZTMMO Lobby" || got.Author != "ZZTMMO" || got.Kind != WorldKindClassic {
		t.Fatalf("LOBBY metadata = %+v", got)
	}

	lobby := m291LobbyWorld(t)
	server := NewWebSocketServer(lobby, 1)
	if server.DefaultInstance.Name != LobbyWorldName {
		t.Fatalf("LOBBY startup world was named %q, want LOBBY", server.DefaultInstance.Name)
	}
}

func TestM291ServerSubprocessDefaultWorldIsLobby(t *testing.T) {
	sp, cleanup := startServerSubprocess(t)
	defer cleanup()

	resp, err := http.Get(sp.baseURL + "/api/metrics")
	if err != nil {
		t.Fatalf("GET /api/metrics: %v", err)
	}
	defer resp.Body.Close()
	var metrics ServiceStatus
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		t.Fatalf("decode /api/metrics: %v", err)
	}
	for _, inst := range metrics.Instances {
		if inst.Name == LobbyWorldName {
			return
		}
	}
	t.Fatalf("default subprocess instances = %+v, want LOBBY", metrics.Instances)
}

func TestM291OrdinaryPassagesStayInsideTheirWorld(t *testing.T) {
	rm := NewRoomManager(testMultiplayerSmokeWorld(t))
	player := rm.JoinPlayer(1, 10, 12)
	for i := 0; i < 12; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{player: {DeltaX: 1, Key: KEY_RIGHT}})
	}
	if transits := rm.DrainWorldTransits(); len(transits) != 0 {
		t.Fatalf("ordinary passage produced cross-world transit: %+v", transits)
	}
}

func TestM291LobbyGateTransfersSignedInPlayerToTown(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	ada := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	bo := AuthenticatedAccount{ID: "google:bo", Email: "bo@example.test", Name: "Bo"}
	if err := db.PutAccountPreferences(ada.ID, AccountPreferences{
		Color:            "#112233",
		BlockedAccounts:  []string{bo.ID},
		FollowedAccounts: []string{bo.ID},
		Profile:          AccountProfilePreferences{Handle: "ada", DisplayName: "Ada", About: []string{"Lobby regular"}},
	}); err != nil {
		t.Fatalf("seed Ada prefs: %v", err)
	}
	if err := db.PutAccountPreferences(bo.ID, AccountPreferences{
		Profile: AccountProfilePreferences{Handle: "bob", DisplayName: "Bo"},
	}); err != nil {
		t.Fatalf("seed Bo prefs: %v", err)
	}
	if err := db.PutPlayerState(ada.ID, "TOWN", PlayerState{Health: 77, Gems: 3}); err != nil {
		t.Fatalf("seed Ada TOWN state: %v", err)
	}

	server := NewWebSocketServer(m291LobbyWorld(t), 1)
	server.ChatDB = db
	server.Auth = NewAuthService("client-id", "", "", secret)
	server.Activity = &WorldActivityStore{counts: make(map[string]int)}
	server.WorldsDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(server.WorldsDir, "LOBBY.ZZT"), m291LobbyWorldBytes(t), 0o644); err != nil {
		t.Fatalf("write LOBBY: %v", err)
	}
	town := testEmptyWorld(t)
	town.Info.Name = "TOWN"
	m1616WriteWorldFile(t, town, filepath.Join(server.WorldsDir, "TOWN.ZZT"))

	httpServer := httptestServer(t, server)
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	boConn, boSnap := dialJoinWithCookie(t, ctx, wsURL+"?world=TOWN", JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, bo))
	defer boConn.Close(websocket.StatusNormalClosure, "")
	adaConn, adaSnap := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Board: 1}, signedAuthCookie(t, server.Auth, ada))
	defer adaConn.Close(websocket.StatusNormalClosure, "")
	if adaSnap.World != LobbyWorldName || adaSnap.You.ID == boSnap.You.ID {
		t.Fatalf("Ada joined %+v, Bo joined %+v", adaSnap, boSnap)
	}
	if got := server.Activity.Counts()[LobbyWorldName]; got != 1 {
		t.Fatalf("LOBBY play count after join = %d, want 1", got)
	}

	for i := 0; i < 4; i++ {
		m291SendInputAndTick(t, ctx, server, server.DefaultInstance, adaSnap.You.ID, adaConn, InputMessage{Type: MessageTypeInput, Keymask: InputMaskUp})
	}
	townSnap := m291ReadPlayerSnapshot(t, ctx, adaConn, "TOWN")
	if townSnap.You.ID != adaSnap.You.ID {
		t.Fatalf("transit minted player id %d, want existing %d", townSnap.You.ID, adaSnap.You.ID)
	}
	if townSnap.HUD.Health != 77 || townSnap.HUD.Gems != 3 {
		t.Fatalf("TOWN sidecar state was not restored: %+v", townSnap.HUD)
	}
	if color := m193MyColor(t, townSnap); color != "#112233" {
		t.Fatalf("Ada color after transit = %q, want #112233", color)
	}
	if !containsPlayerID(townSnap.BlockedPlayers, boSnap.You.ID) {
		t.Fatalf("blocked roster after transit = %+v, want Bo %d", townSnap.BlockedPlayers, boSnap.You.ID)
	}
	if !containsPlayerID(townSnap.FollowedPlayers, boSnap.You.ID) {
		t.Fatalf("followed roster after transit = %+v, want Bo %d", townSnap.FollowedPlayers, boSnap.You.ID)
	}
	if got := server.Activity.Counts(); got[LobbyWorldName] != 1 || got["TOWN"] != 2 {
		t.Fatalf("play counts after transit = %+v, want LOBBY=1 TOWN=2", got)
	}

	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	req, _ := http.NewRequest(http.MethodGet, "/api/worlds", nil)
	req.AddCookie(signedAuthCookie(t, server.Auth, ada))
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	var worlds struct {
		Worlds []WorldListEntry `json:"worlds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &worlds); err != nil {
		t.Fatalf("decode /api/worlds: %v", err)
	}
	counts := map[string]int{}
	for _, entry := range worlds.Worlds {
		counts[entry.World] = entry.Players
	}
	if counts[LobbyWorldName] != 0 || counts["TOWN"] != 2 {
		t.Fatalf("presence after transit = %+v, want LOBBY=0 TOWN=2", counts)
	}
}

func TestM291MissingLobbyGateTargetLeavesPlayerInLobbyWithScroll(t *testing.T) {
	server := NewWebSocketServer(m291LobbyWorld(t), 1)
	server.DefaultInstance.RoomManager.TransitGates[TransitGateKey{BoardID: 1, X: 30, Y: 9}] = "MISSING"
	server.WorldsDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(server.WorldsDir, "LOBBY.ZZT"), m291LobbyWorldBytes(t), 0o644); err != nil {
		t.Fatalf("write LOBBY: %v", err)
	}
	httpServer := httptestServer(t, server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, snap := dialJoinWithCookie(t, ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), JoinMessage{Type: MessageTypeJoin, Name: "Gate", Board: 1}, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	for i := 0; i < 4; i++ {
		m291SendInputAndTick(t, ctx, server, server.DefaultInstance, snap.You.ID, conn, InputMessage{Type: MessageTypeInput, Keymask: InputMaskUp})
	}
	event := m291ReadEvent(t, ctx, conn, "scroll")
	if !strings.Contains(strings.Join(event.Lines, "\n"), "not joinable") {
		t.Fatalf("missing target event = %+v", event)
	}
	board, _, ok := server.DefaultInstance.RoomManager.PlayerLocation(snap.You.ID)
	if !ok || board != 1 {
		t.Fatalf("player location after refused transit = board %d ok=%v, want still in LOBBY board 1", board, ok)
	}
}

func TestM291LobbyGateBrowserJourney(t *testing.T) {
	m169RequireBrowserHarness(t)

	lobbyBytes := m291LobbyWorldBytes(t)
	welcomeBytes := m232WelcomeWorldBytes(t)
	townBytes := committedTownBytes(t)

	m169RequireClientBuild(t)
	binPath := getM1619ServerBinary(t)

	rootDir := t.TempDir()
	webDir, err := filepath.Abs(m169ClientDir())
	if err != nil {
		t.Fatalf("resolve %s: %v", m169ClientDir(), err)
	}
	savesDir := filepath.Join(rootDir, "saves")
	worldsDir := filepath.Join(rootDir, "worlds")
	for _, dir := range []string{savesDir, worldsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, dir := range []string{worldsDir, rootDir} {
		for name, data := range map[string][]byte{
			"LOBBY.ZZT":   lobbyBytes,
			"TOWN.ZZT":    townBytes,
			"WELCOME.ZZT": welcomeBytes,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatalf("write %s into %s: %v", name, dir, err)
			}
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cmd := exec.Command(binPath,
		"-world", LobbyWorldName,
		"-addr", addr,
		"-web", webDir,
		"-saves", savesDir,
		"-worlds", worldsDir,
		"-help", ".",
		"-shutdown-grace", "0s",
		"-fresh",
	)
	cmd.Dir = rootDir
	var logBuf bytes.Buffer
	cmd.Stdout = &logBuf
	cmd.Stderr = &logBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start zzt-server: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_ = cmd.Wait()
		}
	}()

	baseURL := "http://" + addr
	ready := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		resp, err := http.Get(baseURL + "/api/worlds")
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("server on %s failed to become ready. Logs:\n%s", addr, logBuf.String())
	}

	nodeCmd := exec.Command("node", filepath.Join("test", "lobby_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lobby browser journey failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("lobby browser journey output:\n%s", string(out))
}

func m291SendInputAndTick(t *testing.T, ctx context.Context, server *WebSocketServer, inst *WorldInstance, playerID PlayerID, conn *websocket.Conn, input InputMessage) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, input); err != nil {
		t.Fatalf("write input: %v", err)
	}
	waitFor(t, "input queued", func() bool {
		inst.mu.Lock()
		defer inst.mu.Unlock()
		_, ok := inst.Inputs[playerID]
		return ok
	})
	server.Tick(ctx)
}

func m291ReadPlayerSnapshot(t *testing.T, ctx context.Context, conn *websocket.Conn, world string) SnapshotMessage {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}
		switch envelope.Type {
		case MessageTypeSnapshot:
			var snap SnapshotMessage
			if err := json.Unmarshal(raw, &snap); err != nil {
				t.Fatalf("decode snapshot: %v", err)
			}
			if snap.World == world {
				return snap
			}
		case MessageTypeBoardChange:
			var msg BoardChangeMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("decode boardChange: %v", err)
			}
			if msg.Snapshot.World == world {
				return msg.Snapshot
			}
		}
	}
}

func m291ReadEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, eventType string) ProtocolEvent {
	t.Helper()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			t.Fatalf("read event: %v", err)
		}
		var msg EventMessage
		if json.Unmarshal(raw, &msg) == nil && msg.Type == MessageTypeEvent && msg.Event.Type == eventType {
			return msg.Event
		}
	}
}

func containsPlayerID(ids []PlayerID, want PlayerID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
