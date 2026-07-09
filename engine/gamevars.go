package zztgo // unit: GameVars

const (
	MAX_STAT         = 150
	MAX_ELEMENT      = 53
	MAX_BOARD        = 100
	MAX_FLAG         = 10
	BOARD_WIDTH      = 60
	BOARD_HEIGHT     = 25
	HIGH_SCORE_COUNT = 30
	TORCH_DURATION   = 200
	TORCH_DX         = 8
	TORCH_DY         = 5
	TORCH_DIST_SQR   = 50
)

const (
	SizeOfBoardName        = 50 + 1
	SizeOfRleTile          = 3
	SizeOfBoardInfo        = 86
	SizeOfStat             = 33
	SizeOfBoardInfoMessage = 58 + 1
	SizeOfWorldInfo        = 275
	SizeOfHighScoreList    = 53 * HIGH_SCORE_COUNT
)

type (
	TString50 string
	TCoord    struct {
		X int16
		Y int16
	}
	TTile struct {
		Element byte
		Color   byte
	}
	TElementDrawProc  func(e *Engine, x, y int16, ch *byte)
	TElementTickProc  func(e *Engine, statId int16)
	TElementTouchProc func(e *Engine, x, y int16, sourceStatId int16, deltaX, deltaY *int16)
	TElementDef       struct {
		Character           byte
		Color               byte
		Destructible        bool
		Pushable            bool
		VisibleInDark       bool
		PlaceableOnTop      bool
		Walkable            bool
		HasDrawProc         bool
		DrawProc            TElementDrawProc
		Cycle               int16
		TickProc            TElementTickProc
		TouchProc           TElementTouchProc
		EditorCategory      int16
		EditorShortcut      byte
		Name                string
		CategoryName        string
		Param1Name          string
		Param2Name          string
		ParamBulletTypeName string
		ParamBoardName      string
		ParamDirName        string
		ParamTextName       string
		ScoreValue          int16
	}
	TStat struct {
		X, Y         byte
		StepX, StepY int16
		Cycle        int16
		P1, P2, P3   byte
		Follower     int16
		Leader       int16
		Under        TTile
		Data         string // TODO: this should probably be []byte as ZAP/RESTORE modify it
		DataPos      int16
		DataLen      int16
		unk1, unk2   *uintptr
	}
	TRleTile struct {
		Count byte
		Tile  TTile
	}
	TBoardInfo struct {
		MaxShots          byte
		IsDark            bool
		NeighborBoards    [4]byte
		ReenterWhenZapped bool
		Message           string
		StartPlayerX      byte
		StartPlayerY      byte
		TimeLimitSec      int16
		unk1              [16]byte
	}
	TWorldInfo struct {
		Ammo           int16
		Gems           int16
		Keys           [7]bool
		Health         int16
		CurrentBoard   int16
		Torches        int16
		TorchTicks     int16
		EnergizerTicks int16
		padding1       int16 // TODO: remove
		Score          int16
		Name           string
		Flags          [MAX_FLAG]string
		BoardTimeSec   int16
		BoardTimeHsec  int16
		IsSave         bool
		padding2       [14]byte // TODO: remove
	}
	TEditorStatSetting struct {
		P1, P2, P3   byte
		StepX, StepY int16
	}
	TBoard struct {
		Name      string
		Tiles     [BOARD_WIDTH + 1 + 1][BOARD_HEIGHT + 1 + 1]TTile
		StatCount int16
		Stats     [MAX_STAT + 1 + 1]TStat
		Info      TBoardInfo
	}
	TWorld struct {
		BoardCount         int16
		BoardData          [MAX_BOARD + 1][]byte
		BoardLen           [MAX_BOARD + 1]int16
		Info               TWorldInfo
		EditorStatSettings [MAX_ELEMENT + 1]TEditorStatSetting
	}
	THighScoreEntry struct {
		Name  string
		Score int16
	}
	THighScoreList [HIGH_SCORE_COUNT]THighScoreEntry
	TIoTmpBuf      [20000]byte
	Engine         struct {
		unkVar_0476            int16
		unkVar_0478            int16
		TransitionTable        [80 * 25]TCoord
		LoadedGameFileName     string
		SavedGameFileName      string
		SavedBoardFileName     string
		StartupWorldFileName   string
		Board                  TBoard
		World                  TWorld
		Players                map[int16]*PlayerState
		unkVar_4ABA            [15]byte
		GameTitleExitRequested bool
		GamePlayExitRequested  bool
		GameStateElement       int16
		ReturnBoardId          int16
		TransitionTableSize    int16
		TickSpeed              byte
		IoTmpBuf               TIoTmpBuf
		EditorPatternCount     int16
		EditorPatterns         [10]byte
		TickTimeDuration       int16
		CurrentTick            int16
		CurrentStatTicked      int16
		TickTimeCounter        int16
		ForceDarknessOff       bool
		OopChar                byte
		OopWord                string
		OopValue               int16
		DebugEnabled           bool
		HighScoreList          THighScoreList
		ConfigRegistration     string
		ConfigWorldFile        string
		EditorEnabled          bool
		JustStarted            bool
		WorldFileDescCount     int16
		WorldFileDescKeys      [10]string
		WorldFileDescValues    [10]string
		Screen                 [80][25]struct{ Ch, Color byte }
		Headless               bool
		videoDirty             []dirtyCell
		screenDirty            []dirtyCell
		ActiveInput            InputSource
		RandSeed               uint32
		InputDeltaX            int16
		InputDeltaY            int16
		InputShiftPressed      bool
		InputKeyPressed        byte
		InputLastDeltaX        int16
		InputLastDeltaY        int16
		InputKeyBuffer         string
		Events                 []Event
		// PendingScrollReplies queues hyperlink selections from clients. Several
		// players can close a scroll on the same tick, so this is a queue rather
		// than a single slot: the terminal client submits through the same queue.
		PendingScrollReplies []PendingScrollReply
		// ScrollAudience[objectStatId] = triggering player's stat id + 1, or 0
		// for none. An object runs its #TOUCH code on its own later tick, by
		// which point the toucher is long gone from the call stack, so the touch
		// procs record it here. Not simulation state: it only decides which
		// client is shown the scroll. Kept in step with the stat array by
		// reindexScrollAudienceAfterStatRemoval, like Follower/Leader.
		ScrollAudience [MAX_STAT + 2]int16
		// PendingDebugCommands holds debug-prompt text submitted by clients,
		// applied at the top of the next GameStepWithInputs. The '?' prompt was
		// modal (PromptString blocks on InputReadWaitKey), so like scrolls it
		// becomes an event out plus a reply in.
		PendingDebugCommands []PendingDebugCommand
		// PendingSaveFilenames holds save-prompt replies submitted by callers,
		// applied at the top of the next GameStepWithInputs, exactly as
		// PendingDebugCommands are.
		PendingSaveFilenames []PendingSaveFilename
		// PendingQuitReplies holds quit-prompt answers submitted by callers,
		// applied at the top of the next GameStepWithInputs like the others.
		PendingQuitReplies []PendingQuitReply
		SoundBlockQueueing bool
		// FriendlyFire controls whether player-owned bullets can damage other
		// players. True (default) = players can damage each other; false = player
		// bullets pass through other player stats without effect. This is a
		// server/engine configuration flag and is not accessible from ZZT-OOP.
		FriendlyFire bool
		// MultiRoom, when true, causes passage and board-edge touches to emit
		// TransferEvent instead of swapping the board in-place. The caller
		// (RoomManager / server) handles the actual player transfer between engines.
		// When false (default, single-player), the engine swaps the board as usual.
		MultiRoom bool
	}
	Event       interface{}
	ScrollEvent struct {
		Title string
		Lines []string
		// StatId is the OBJECT running the code — the target a hyperlink reply
		// must be sent back to, not the player who read it.
		StatId int16
		// PlayerStatId is the player who triggered the scroll, or -1 if unknown.
		// Room events broadcast to the whole board; the client filters on this.
		PlayerStatId int16
	}
	// PendingScrollReply is one queued hyperlink selection: send Label to the
	// object at StatId.
	PendingScrollReply struct {
		StatId int16
		Label  string
	}
	// QuitPromptEvent is emitted when a player presses 'Q' or Escape. The caller
	// shows "End this game? " and answers via Engine.SubmitQuitReply.
	//
	// StatId is the player who asked. Room events are broadcast to everyone on
	// the board, so without it a quit prompt belongs to nobody: the client
	// cannot tell whose modal to open (TASKS.md M4.3).
	QuitPromptEvent struct {
		StatId int16
	}
	// PendingQuitReply is one queued answer to a QuitPromptEvent.
	PendingQuitReply struct {
		StatId int16
		Quit   bool
	}
	// QuitEvent reports that a player CONFIRMED the quit prompt. Only the
	// multi-room caller sees it: single-player sets GamePlayExitRequested and
	// falls out of GamePlayLoop instead, exactly as vanilla does. The engine
	// cannot remove the player itself — RoomManager owns the roster — so it
	// announces the decision the same way TransferEvent announces a passage.
	QuitEvent struct {
		StatId int16
	}
	HelpEvent struct {
		Filename string
		Title    string
		// StatId is the player who asked for help. Room events are broadcast to
		// everyone on the board, so the client filters on this.
		StatId int16
	}
	// DebugPromptEvent is emitted when a player presses '?'. The caller shows
	// the 11-character sidebar prompt (PromptString at 63,5 in vanilla ZZT) and
	// feeds the typed text back via Engine.SubmitDebugCommand.
	DebugPromptEvent struct {
		StatId int16
	}
	// PendingDebugCommand is one queued reply to a DebugPromptEvent.
	PendingDebugCommand struct {
		StatId int16
		Text   string
	}
	// SavePromptEvent is emitted when a player presses 'S'. The sim never
	// blocks: it emits and returns. The caller shows the save prompt and feeds
	// the filename back via Engine.SubmitSaveFilename. The terminal answers
	// with a real modal prompt; the server currently refuses (empty name), see
	// NOTES.md 2026-07-09 "save policy for a shared world" and TASKS.md M4.3a.
	SavePromptEvent struct {
		StatId int16
	}
	// PendingSaveFilename is one queued reply to a SavePromptEvent. An empty
	// Name means the prompt was cancelled or refused: save nothing.
	PendingSaveFilename struct {
		StatId int16
		Name   string
	}
	// PauseEvent reports a change to one player's paused state, so the client
	// can draw (or clear) vanilla's blinking "Pausing..." at 64,5. Pausing is
	// per-player: the room keeps running for everyone else.
	PauseEvent struct {
		StatId int16
		Paused bool
	}
	// HighScoreEntryEvent offers one player's score to the high-score list.
	// StatId is whose score it is: on a server the list is shared by every
	// player in the world, so the entry must say who earned it.
	HighScoreEntryEvent struct {
		StatId  int16
		Score   int16
		ListPos int16
	}
	SoundEvent struct {
		Notes    string
		Priority int16
	}
	// DeathEvent is emitted when a player's health reaches 0. The engine will
	// respawn the player automatically after RESPAWN_TICKS; this event lets the
	// server notify the client (show death screen, etc.).
	DeathEvent struct {
		StatId int16
	}
	// RespawnEvent is emitted when the respawn countdown expires and the player
	// is placed back on the board at StartPlayerX/Y.
	RespawnEvent struct {
		StatId int16
		X, Y   int16
	}
	// TransferEvent is emitted when a player touches a passage or board edge and
	// the engine is in MultiRoom mode (Engine.MultiRoom=true) or more than one
	// player is present on the board. The engine does NOT swap the board; the
	// caller (RoomManager / server) is responsible for despawning the player from
	// this engine and spawning them on the destination engine at (EntryX, EntryY).
	TransferEvent struct {
		StatId  int16 // the player stat being transferred
		ToBoard int16 // destination board index (into World.BoardData)
		EntryX  int16 // entry tile X on the destination board
		EntryY  int16 // entry tile Y on the destination board
	}
	PlayerState struct {
		Health         int16
		Ammo           int16
		Gems           int16
		Torches        int16
		TorchTicks     int16
		EnergizerTicks int16
		Score          int16
		Keys           [7]bool
		BoardTimeSec   int16
		BoardTimeHsec  int16
		// DirX/DirY is the player's last shoot direction (formerly Engine.PlayerDirX/Y).
		// Stored per-player so each player retains their own aim independently.
		DirX int16
		DirY int16
		// RespawnTicks counts down after death. When it reaches 0 the player
		// is placed back at Board.Info.StartPlayerX/Y with RESPAWN_INVULN_TICKS
		// of invulnerability. Zero means the player is alive (not waiting to respawn).
		RespawnTicks                int16
		MessageAmmoNotShown         bool
		MessageOutOfAmmoNotShown    bool
		MessageNoShootingNotShown   bool
		MessageTorchNotShown        bool
		MessageOutOfTorchesNotShown bool
		MessageRoomNotDarkNotShown  bool
		MessageHintTorchNotShown    bool
		MessageForestNotShown       bool
		MessageFakeNotShown         bool
		MessageGemNotShown          bool
		MessageEnergizerNotShown    bool
		// Paused replaces vanilla's Engine-wide GamePaused. Pausing must never
		// freeze the room: a paused player's stat tick and input are skipped
		// while everyone else keeps running (ANALYSIS.md §3i).
		Paused bool
		// SoundEnabled replaces the process-global sounds.go SoundEnabled, so
		// one player muting does not silence every player in every room.
		// Defaults to true in PlayerFor; deliberately NOT reset by
		// ResetPlayerState, since dying should not un-mute you.
		SoundEnabled bool
		// ReenterX/Y is where THIS player returns to on a ReenterWhenZapped hit
		// or a death respawn: the square they entered the board on.
		//
		// Vanilla keeps one Board.Info.StartPlayerX/Y and rewrites it in
		// BoardEnter every time its single player enters a board, so the value
		// stored in the world file is never used. RoomManager never calls
		// BoardEnter, so on the server that stale file value survived — and on
		// TOWN board 19 ("The Mixer") it is (30,25), an E_NORMAL wall tile.
		// Re-entering teleported the player inside the wall.
		//
		// Zero means unset; callers fall back to Board.Info.StartPlayerX/Y.
		ReenterX byte
		ReenterY byte
	}
	// PlayerInput holds one tick of input for a single player.
	// DirX/DirY are the player's aimed shoot direction from the previous tick
	// (the player fires in the last direction moved while shooting).
	PlayerInput struct {
		DeltaX int16
		DeltaY int16
		Shift  bool
		Key    byte
	}
)

var (
	E           = NewEngine()
	ElementDefs [MAX_ELEMENT + 1]TElementDef
)

func NewEngine() *Engine {
	return &Engine{
		ActiveInput:  TcellInput{},
		Players:      make(map[int16]*PlayerState),
		FriendlyFire: true,
	}
}

func (e *Engine) PlayerFor(statId int16) *PlayerState {
	ps := e.Players[statId]
	if ps == nil {
		if e.Players == nil {
			e.Players = make(map[int16]*PlayerState)
		}
		ps = &PlayerState{
			Health:       15,
			SoundEnabled: true,
		}
		e.Players[statId] = ps
	}
	return ps
}

// ReenterPoint is where statId returns to on a ReenterWhenZapped hit or a death
// respawn. Prefers the player's own entry square (set wherever a player is
// placed on a board), falling back to the board's stored start and then the
// board centre.
//
// The fallback is a last resort: a world file's stored StartPlayerX/Y can be a
// wall, because vanilla overwrites it in BoardEnter before ever reading it.
func (e *Engine) ReenterPoint(statId int16) (int16, int16) {
	pState := e.PlayerFor(statId)
	x, y := int16(pState.ReenterX), int16(pState.ReenterY)
	if x == 0 || y == 0 {
		x = int16(e.Board.Info.StartPlayerX)
		y = int16(e.Board.Info.StartPlayerY)
	}
	if x == 0 || y == 0 {
		x = BOARD_WIDTH / 2
		y = BOARD_HEIGHT / 2
	}
	return x, y
}

// SetReenterPoint records where a player entered the board. Called from every
// place a player is put on a board, so ReenterPoint never has to trust the
// world file's stale StartPlayerX/Y.
func (e *Engine) SetReenterPoint(statId int16, x, y int16) {
	pState := e.PlayerFor(statId)
	pState.ReenterX = byte(x)
	pState.ReenterY = byte(y)
}

func (e *Engine) SpawnPlayer() int16 {
	spawnX := int16(e.Board.Info.StartPlayerX)
	spawnY := int16(e.Board.Info.StartPlayerY)
	if spawnX == 0 || spawnY == 0 {
		spawnX = BOARD_WIDTH / 2
		spawnY = BOARD_HEIGHT / 2
	}

	e.AddStat(spawnX, spawnY, E_PLAYER, int16(ElementDefs[E_PLAYER].Color), 1, StatTemplateDefault)
	statId := e.Board.StatCount

	e.Board.Tiles[spawnX][spawnY].Element = E_PLAYER
	e.Board.Tiles[spawnX][spawnY].Color = ElementDefs[E_PLAYER].Color

	e.ResetPlayerState(statId)
	e.SetReenterPoint(statId, spawnX, spawnY)
	e.BoardDrawTile(spawnX, spawnY)
	return statId
}

func (e *Engine) ResetPlayerState(statId int16) {
	pState := e.PlayerFor(statId)
	pState.Health = 100
	pState.Ammo = 0
	pState.Gems = 0
	pState.Torches = 0
	pState.TorchTicks = 0
	pState.EnergizerTicks = 0
	pState.Score = 0
	pState.BoardTimeSec = 0
	pState.BoardTimeHsec = 0
	pState.MessageAmmoNotShown = true
	pState.MessageOutOfAmmoNotShown = true
	pState.MessageNoShootingNotShown = true
	pState.MessageTorchNotShown = true
	pState.MessageOutOfTorchesNotShown = true
	pState.MessageRoomNotDarkNotShown = true
	pState.MessageHintTorchNotShown = true
	pState.MessageForestNotShown = true
	pState.MessageFakeNotShown = true
	pState.MessageGemNotShown = true
	pState.MessageEnergizerNotShown = true
	pState.Paused = false
	for i := 1; i <= 7; i++ {
		pState.Keys[i-1] = false
	}
}

func (e *Engine) RemovePlayer(statId int16) {
	if statId < 0 || statId > e.Board.StatCount {
		return
	}
	stat := &e.Board.Stats[statId]
	if e.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
		e.Board.Tiles[stat.X][stat.Y].Element = E_EMPTY
		e.Board.Tiles[stat.X][stat.Y].Color = 0
		e.BoardDrawTile(int16(stat.X), int16(stat.Y))
	}
	e.RemoveStat(statId)
}

func (e *Engine) NearestPlayer(x, y int16) int16 {
	var (
		bestId   int16 = 0
		bestDist int32 = 999999
	)
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		if e.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
			dist := int32(Sqr(int16(stat.X)-x)) + int32(Sqr(int16(stat.Y)-y))
			if dist < bestDist {
				bestDist = dist
				bestId = i
			}
		}
	}
	return bestId
}

// PlayerCount returns the number of E_PLAYER stats currently on the board.
func (e *Engine) PlayerCount() int16 {
	var count int16
	for i := int16(0); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		if e.Board.Tiles[stat.X][stat.Y].Element == E_PLAYER {
			count++
		}
	}
	return count
}

const (
	E_EMPTY                = 0
	E_BOARD_EDGE           = 1
	E_MESSAGE_TIMER        = 2
	E_MONITOR              = 3
	E_PLAYER               = 4
	E_AMMO                 = 5
	E_TORCH                = 6
	E_GEM                  = 7
	E_KEY                  = 8
	E_DOOR                 = 9
	E_SCROLL               = 10
	E_PASSAGE              = 11
	E_DUPLICATOR           = 12
	E_BOMB                 = 13
	E_ENERGIZER            = 14
	E_STAR                 = 15
	E_CONVEYOR_CW          = 16
	E_CONVEYOR_CCW         = 17
	E_BULLET               = 18
	E_WATER                = 19
	E_FOREST               = 20
	E_SOLID                = 21
	E_NORMAL               = 22
	E_BREAKABLE            = 23
	E_BOULDER              = 24
	E_SLIDER_NS            = 25
	E_SLIDER_EW            = 26
	E_FAKE                 = 27
	E_INVISIBLE            = 28
	E_BLINK_WALL           = 29
	E_TRANSPORTER          = 30
	E_LINE                 = 31
	E_RICOCHET             = 32
	E_BLINK_RAY_EW         = 33
	E_BEAR                 = 34
	E_RUFFIAN              = 35
	E_OBJECT               = 36
	E_SLIME                = 37
	E_SHARK                = 38
	E_SPINNING_GUN         = 39
	E_PUSHER               = 40
	E_LION                 = 41
	E_TIGER                = 42
	E_BLINK_RAY_NS         = 43
	E_CENTIPEDE_HEAD       = 44
	E_CENTIPEDE_SEGMENT    = 45
	E_TEXT_BLUE            = 47
	E_TEXT_GREEN           = 48
	E_TEXT_CYAN            = 49
	E_TEXT_RED             = 50
	E_TEXT_PURPLE          = 51
	E_TEXT_YELLOW          = 52
	E_TEXT_WHITE           = 53
	E_TEXT_MIN             = E_TEXT_BLUE
	CATEGORY_ITEM          = 1
	CATEGORY_CREATURE      = 2
	CATEGORY_TERRAIN       = 3
	COLOR_SPECIAL_MIN      = 0xF0
	COLOR_CHOICE_ON_BLACK  = 0xFF
	COLOR_WHITE_ON_CHOICE  = 0xFE
	COLOR_CHOICE_ON_CHOICE = 0xFD
	SHOT_SOURCE_PLAYER     = 0
	SHOT_SOURCE_ENEMY      = 1
	// SHOT_SOURCE_PLAYER_BASE is added to a player's statId to form the P1
	// value stored in a bullet/star stat, encoding the owning player.
	// Values 0 and 1 are reserved for SHOT_SOURCE_PLAYER and SHOT_SOURCE_ENEMY
	// respectively; player statIds start at 0 so we offset by 2.
	// Owner statId = stat.P1 - SHOT_SOURCE_PLAYER_BASE, valid when stat.P1 >= SHOT_SOURCE_PLAYER_BASE.
	SHOT_SOURCE_PLAYER_BASE = 2

	// Respawn timing and penalty constants (M2.4).
	RESPAWN_TICKS         = 30  // ticks before a dead player reappears (~3.3s at 110ms/tick)
	RESPAWN_INVULN_TICKS  = 50  // post-respawn invulnerability ticks (reuses EnergizerTicks)
	RESPAWN_SCORE_PENALTY = 100 // score lost on death (floored at 0)
)
