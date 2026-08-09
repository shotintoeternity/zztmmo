package zztgo

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func m301ArenaWorldBytes(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "fixtures", "arena.zwd"))
	if err != nil {
		t.Fatalf("read fixtures/arena.zwd: %v", err)
	}
	data, err := CompileZWD(string(src))
	if err != nil {
		t.Fatalf("CompileZWD(fixtures/arena.zwd): %v", err)
	}
	validateCompiledZWD(t, data)
	return data
}

func m301ArenaWorld(t *testing.T) TWorld {
	t.Helper()
	world, err := CompileZWDWorld(string(mustRead(t, "../fixtures/arena.zwd")))
	if err != nil {
		t.Fatalf("CompileZWDWorld(fixtures/arena.zwd): %v", err)
	}
	return world
}

func TestM301ArenaWorldCompilesShipsIsCanonicalAndListed(t *testing.T) {
	data := m301ArenaWorldBytes(t)
	if err := os.WriteFile(filepath.Join("..", "fixtures", "ARENA.ZZT"), data, 0o644); err != nil {
		t.Fatalf("write fixtures/ARENA.ZZT: %v", err)
	}
	if err := os.WriteFile("ARENA.ZZT", data, 0o644); err != nil {
		t.Fatalf("write engine/ARENA.ZZT: %v", err)
	}
	if !WorldIsCanonical(ArenaWorldName) {
		t.Fatal("WorldIsCanonical(ARENA) = false; the first-party arena must be protected")
	}
	if err := refuseIfCanonical(ArenaWorldName); !errors.Is(err, ErrGeneratedWorldIsCanonical) {
		t.Fatalf("dream canonical guard for ARENA = %v, want ErrGeneratedWorldIsCanonical", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ARENA.ZZT"), data, 0o644); err != nil {
		t.Fatalf("write temp ARENA: %v", err)
	}
	entries := WorldListEntriesInDir(dir, ListWorlds(dir), nil)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly ARENA", entries)
	}
	if got := entries[0]; got.World != ArenaWorldName || got.Title != "ZZTMMO Arena" || got.Author != "ZZTMMO" || got.Kind != WorldKindClassic {
		t.Fatalf("ARENA metadata = %+v", got)
	}
	shelves := buildWorldShelves(entries, false, nil)
	found := false
	for _, shelf := range shelves {
		if shelf.ID == "arena" && len(shelf.Worlds) == 1 && shelf.Worlds[0] == ArenaWorldName {
			found = true
		}
	}
	if !found {
		t.Fatalf("shelves = %+v, want ARENA on the arena shelf", shelves)
	}
}

func TestM301FriendlyFirePolicyComesOnlyFromResolvedIdentity(t *testing.T) {
	authored := testEmptyWorld(t)
	authored.Info.Name = ArenaWorldName
	authored.Info.Flags[0] = ArenaWorldName

	cases := []struct {
		identity string
		want     bool
	}{
		{ArenaWorldName, true},
		{"arena", true},
		{"TOWN", false},
		{LobbyWorldName, false},
		{"WELCOME", false},
		{"LOCAL", false},
		{"GEN123", false},
		{"RESTORE", false},
	}
	for _, tc := range cases {
		rm := NewRoomManagerForWorld(authored, tc.identity)
		if rm.FriendlyFire != tc.want {
			t.Errorf("FriendlyFire for identity %q = %v, want %v", tc.identity, rm.FriendlyFire, tc.want)
		}
		player := rm.JoinPlayer(1, 0, 0)
		board, _, ok := rm.PlayerLocation(player)
		if !ok {
			t.Fatalf("player did not join %q", tc.identity)
		}
		room, ok := rm.Room(board)
		if !ok {
			t.Fatalf("room missing for %q", tc.identity)
		}
		if room.Engine.FriendlyFire != tc.want {
			t.Errorf("room engine FriendlyFire for identity %q = %v, want %v", tc.identity, room.Engine.FriendlyFire, tc.want)
		}
	}
}

func TestM301FriendlyFireSurvivesTransferFreezeThawAndReplay(t *testing.T) {
	world := twoBoardPassageWorld(t)
	rm := NewRoomManagerForWorld(world, ArenaWorldName)
	player := rm.JoinPlayer(1, 9, 12)
	room, ok := rm.Room(1)
	if !ok || !room.Engine.FriendlyFire {
		t.Fatalf("ARENA board 1 room FriendlyFire = %v ok=%v, want true", room != nil && room.Engine.FriendlyFire, ok)
	}

	for i := 0; i < 4; i++ {
		rm.StepDiffs(map[PlayerID]PlayerInput{player: {DeltaX: 1, Key: KEY_RIGHT}})
		if board, _, _ := rm.PlayerLocation(player); board == 2 {
			break
		}
	}
	if board, _, ok := rm.PlayerLocation(player); !ok || board != 2 {
		t.Fatalf("player location after passage = board %d ok=%v, want board 2", board, ok)
	}
	room, ok = rm.Room(2)
	if !ok || !room.Engine.FriendlyFire {
		t.Fatalf("ARENA board 2 room FriendlyFire = %v ok=%v, want true", room != nil && room.Engine.FriendlyFire, ok)
	}

	if !rm.LeavePlayer(player) {
		t.Fatal("LeavePlayer failed")
	}
	if rm.ActiveRoomCount() != 0 {
		t.Fatalf("rooms after leave = %d, want frozen world with no live rooms", rm.ActiveRoomCount())
	}
	rejoined := rm.JoinPlayer(2, 0, 0)
	if room, ok = rm.Room(2); !ok || !room.Engine.FriendlyFire {
		t.Fatalf("thawed ARENA room FriendlyFire = %v ok=%v, want true", room != nil && room.Engine.FriendlyFire, ok)
	}
	rm.LeavePlayer(rejoined)

	var buf bytes.Buffer
	header, _, err := newSessionHeader(ArenaWorldName, world)
	if err != nil {
		t.Fatalf("newSessionHeader: %v", err)
	}
	rec, err := NewSessionRecorder(&buf, header)
	if err != nil {
		t.Fatalf("NewSessionRecorder: %v", err)
	}
	recorded := NewRoomManagerForWorld(world, ArenaWorldName)
	recorded.SetRecorder(rec)
	recorded.JoinPlayerWithID(7, 1, 9, 12)
	recorded.StepDiffs(nil)
	rec.Close()

	playback, err := NewReplayPlayback(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReplayPlayback: %v", err)
	}
	if playback.RoomManager() == nil || !playback.RoomManager().FriendlyFire {
		t.Fatal("replay playback did not reconstruct ARENA friendly-fire policy")
	}
}

func TestM301ArenaPolicyControlsPointBlankAndMovingBullets(t *testing.T) {
	for _, tc := range []struct {
		identity string
		wantHit  bool
	}{
		{ArenaWorldName, true},
		{"TOWN", false},
	} {
		rm := NewRoomManagerForWorld(testEmptyWorld(t), tc.identity)
		e, target := pointBlankSetup(t)
		e.FriendlyFire = rm.FriendlyFire
		e.PlayerFor(target).EnergizerTicks = 0
		start := e.PlayerFor(target).Health
		hit := e.BoardShoot(E_BULLET, 10, 10, 1, 0, SHOT_SOURCE_PLAYER_BASE)
		if hit != tc.wantHit {
			t.Errorf("%s point-blank hit = %v, want %v", tc.identity, hit, tc.wantHit)
		}
		if got, want := e.PlayerFor(target).Health, start; tc.wantHit {
			want = start - 10
			if got != want {
				t.Errorf("%s point-blank target health = %d, want %d", tc.identity, got, want)
			}
		} else if got != want {
			t.Errorf("%s point-blank target health = %d, want %d", tc.identity, got, want)
		}

		e = movingBulletSetup(t, rm.FriendlyFire)
		target = e.GetStatIdAt(12, 10)
		start = e.PlayerFor(target).Health
		if ok := e.BoardShoot(E_BULLET, 10, 10, 1, 0, SHOT_SOURCE_PLAYER_BASE); !ok {
			t.Fatalf("%s moving bullet was not placed", tc.identity)
		}
		bullet := e.GetStatIdAt(11, 10)
		if bullet <= 0 {
			t.Fatalf("%s bullet stat not found at (11,10)", tc.identity)
		}
		shooterStart := e.PlayerFor(0).Health
		e.ElementBulletTick(bullet)
		if got, want := e.PlayerFor(target).Health, start; tc.wantHit {
			want = start - 10
			if got != want {
				t.Errorf("%s moving bullet target health = %d, want %d", tc.identity, got, want)
			}
		} else if got != want {
			t.Errorf("%s moving bullet target health = %d, want %d", tc.identity, got, want)
		}
		if shooter := e.PlayerFor(0).Health; shooter != shooterStart {
			t.Errorf("%s shooter health = %d, want untouched %d", tc.identity, shooter, shooterStart)
		}
	}
}

func movingBulletSetup(t *testing.T, friendlyFire bool) *Engine {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.FriendlyFire = friendlyFire
	e.WorldCreate()
	e.BoardCreate()

	e.Board.Tiles[10][10] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	e.Board.Stats[0].X = 10
	e.Board.Stats[0].Y = 10
	e.AddStat(12, 10, E_PLAYER, int16(ElementDefs[E_PLAYER].Color), 1, StatTemplateDefault)
	target := e.Board.StatCount
	e.Board.Tiles[12][10] = TTile{Element: E_PLAYER, Color: ElementDefs[E_PLAYER].Color}
	e.PlayerFor(target).EnergizerTicks = 0
	return e
}

func TestM301ArenaBrowserJourney(t *testing.T) {
	m169RequireBrowserHarness(t)

	arenaBytes := m301ArenaWorldBytes(t)
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
			"ARENA.ZZT":   arenaBytes,
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

	nodeCmd := exec.Command("node", filepath.Join("test", "arena_journey.test.mjs"))
	nodeCmd.Dir = "web"
	nodeCmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	out, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("arena browser journey failed: %v\n--- script output ---\n%s\n--- server log ---\n%s",
			err, string(out), logBuf.String())
	}
	t.Logf("arena browser journey output:\n%s", string(out))
}
