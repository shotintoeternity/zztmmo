// The wire format between the Go server and the browser client.
//
// Every message the client can send and every message the server can push is
// declared here as a JSON struct, and this file is the single place the two
// sides agree. The client's mirror of these shapes lives in web/src; changing a
// field on one side without the other is the failure this file exists to make
// obvious, and the browser suites under web/test are what catch it.
//
// The traffic is deliberately asymmetric. Clients send intent — a keymask, a
// command byte, a menu choice — and never game state. The server sends what
// changed: board diffs, HUD updates, sounds, modal events. A client that
// invents state is wrong by construction, because it has no simulation to
// invent it from.

package zztgo

import (
	"path/filepath"
	"sync"
)

const (
	MessageTypeJoin               = "join"
	MessageTypeInput              = "input"
	MessageTypeSnapshot           = "snapshot"
	MessageTypeDiff               = "diff"
	MessageTypeEvent              = "event"
	MessageTypeBoardChange        = "boardChange"
	MessageTypeDebugCommand       = "debugCommand"
	MessageTypeScrollReply        = "scrollReply"
	MessageTypeQuitReply          = "quitReply"
	MessageTypeHighScoreName      = "highScoreName"
	MessageTypeSaveFilename       = "saveFilename"
	MessageTypeEditorEnter        = "editorEnter"
	MessageTypeEditorExit         = "editorExit"
	MessageTypeEditorInspect      = "editorInspect"
	MessageTypeEditorPresence     = "editorPresence"
	MessageTypeEditorLease        = "editorLease"
	MessageTypeEditorSnapshot     = "editorSnapshot"
	MessageTypeEditorEdit         = "editorEdit"
	MessageTypeEditorDiff         = "editorDiff"
	MessageTypeEditorProperty     = "editorProperty"
	MessageTypeEditorProperties   = "editorProperties"
	MessageTypeEditorStat         = "editorStat"
	MessageTypeEditorStatSettings = "editorStatSettings"
	MessageTypeEditorProgram      = "editorProgram"
	MessageTypeEditorProgramText  = "editorProgramText"
	MessageTypeEditorProgramSave  = "editorProgramSave"
	MessageTypeEditorBoard        = "editorBoard"
	MessageTypeEditorBoardData    = "editorBoardData"
	MessageTypeEditorWorld        = "editorWorld"
	MessageTypeEditorWorldData    = "editorWorldData"
	MessageTypeEditorSaveResult   = "editorSaveResult"
	MessageTypeEditorTestPlay     = "editorTestPlay"
	// MessageTypeBlock is a player asking not to hear another player's chat,
	// and MessageTypeBlockResult is what they are told back. Only the blocker
	// ever receives the result: the blocked player is never told.
	MessageTypeBlock       = "block"
	MessageTypeBlockResult = "blockResult"
	// MessageTypeModerate is an operator acting on a person, and
	// MessageTypeModerateResult is what the operator is told back. The TARGET of
	// a mute is told too, through MessageTypeModerationNotice: a mute is a
	// sanction, not a preference, so it is never silent.
	MessageTypeModerate         = "moderate"
	MessageTypeModerateResult   = "moderateResult"
	MessageTypeModerationNotice = "moderationNotice"
	MessageTypeProfileRequest   = "profileRequest"
	MessageTypeProfileResult    = "profileResult"
	MessageTypePrivateMessage   = "privateMessage"
	MessageTypeChat             = "chat"
	MessageTypeAnnounce         = "announce"
	MessageTypePrivateResult    = "privateMessageResult"
	MessageTypeFollow           = "follow"
	MessageTypeFollowResult     = "followResult"
	MessageTypeReplayControl    = "replayControl"
	MessageTypeReplayError      = "replayError"
	// MessageTypeChallengeResult is the server's verdict on a challenge run. It
	// is sent when the SERVER observed the goal met, never on a client's
	// request, and says whether the result became a durable leaderboard row.
	MessageTypeChallengeResult = "challengeResult"
	// MessageTypeChallengeError is an honest refusal on the challenge path — an
	// unknown id, an unavailable world, a run that no longer exists, or a server
	// with recording switched off.
	MessageTypeChallengeError = "challengeError"
)

// ChallengeResultMessage is what a finished run tells its own player.
//
// Rank and Durable are separate deliberately: a guest completes a run and sees
// their ticks (Durable false, Rank 0), and a signed-in player sees where the row
// landed. Nothing here is taken from the client — Ticks and the counters are the
// server's own count of the run it hosted.
type ChallengeResultMessage struct {
	Type        string `json:"type"`
	ChallengeID string `json:"challengeId"`
	Version     int    `json:"version"`
	Ticks       int    `json:"ticks"`
	Score       int    `json:"score"`
	Gems        int    `json:"gems"`
	Durable     bool   `json:"durable"`
	Rank        int    `json:"rank,omitempty"`
	RecordingID string `json:"recordingId,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// ChallengeErrorMessage is a refusal the browser can show as a window.
type ChallengeErrorMessage struct {
	Type        string `json:"type"`
	ChallengeID string `json:"challengeId,omitempty"`
	Reason      string `json:"reason"`
}

// ModerateMessage is an operator's request. Like BlockMessage it names its
// target by PlayerID — the only addressable thing on a chat line — and states
// the action rather than toggling one, so the server never has to guess which
// way a disagreement should resolve.
type ModerateMessage struct {
	Type     string   `json:"type"`
	Action   string   `json:"action"`
	PlayerID PlayerID `json:"playerId"`
}

// ModerateResultMessage is the operator's confirmation, and only ever reaches
// the operator.
//
// Durable is the honest limit made visible: a refusal binds to an accountID, so
// refusing a guest kicks them and nothing more, and the operator is told that in
// the same breath rather than discovering it when the guest walks back in.
type ModerateResultMessage struct {
	Type     string      `json:"type"`
	Action   string      `json:"action"`
	PlayerID PlayerID    `json:"playerId"`
	Name     CP437String `json:"name,omitempty"`
	Applied  bool        `json:"applied"`
	Durable  bool        `json:"durable"`
	// Text is the one line the client shows; the server writes it because the
	// server is what knows which outcome happened.
	Text CP437String `json:"text"`
}

// ModerationNoticeMessage is what a moderated PLAYER is told. Ended marks the
// sanctions that finish this session (kick, refuse, and a refused account's
// rejected reconnect), which is what stops the browser's reconnect backoff from
// quietly undoing a kick half a second after it lands.
type ModerationNoticeMessage struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	Text   string `json:"text"`
	Ended  bool   `json:"ended,omitempty"`
}

type ReplayControlMessage struct {
	Type string `json:"type"`
	Op   string `json:"op"`
}

type ReplayErrorMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ProfileRequestMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
}

type ProfileResultMessage struct {
	Type     string      `json:"type"`
	PlayerID PlayerID    `json:"playerId"`
	Name     CP437String `json:"name,omitempty"`
	Handle   string      `json:"handle,omitempty"`
	Lines    CP437Lines  `json:"lines"`
}

// PrivateSubmitMessage is what a client sends for MessageTypePrivateMessage:
// the target and the text as typed, UTF-8. The server's delivery is a
// PrivateMessage, whose names and text are CP437.
type PrivateSubmitMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId,omitempty"`
	Text     string   `json:"text"`
}

// PrivateMessage is an in-session, roster-addressed whisper as the server
// delivers it, with the author fields filled in. It is session-only: a
// PlayerID names a live connection, not an account.
type PrivateMessage struct {
	Type     string      `json:"type"`
	PlayerID PlayerID    `json:"playerId,omitempty"`
	From     CP437String `json:"from,omitempty"`
	FromID   PlayerID    `json:"fromId,omitempty"`
	To       CP437String `json:"to,omitempty"`
	ToID     PlayerID    `json:"toId,omitempty"`
	Text     CP437String `json:"text"`
	Outgoing bool        `json:"outgoing,omitempty"`
}

// ChatSubmitMessage is what a client sends for MessageTypeChat: the text as
// typed, UTF-8, which admitChatText encodes. The wire shape is ChatMessage's;
// only the decoding differs.
type ChatSubmitMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ChatMessage is one global chat line as the server sends it: From is the
// admitted player name and PlayerID the live connection. From and Text are
// CP437 once admitted. History marks a line replayed from the backlog on join.
// It is a named type so the encoding test (m36_6_test.go) can see its strings.
type ChatMessage struct {
	Type     string      `json:"type"`
	From     CP437String `json:"from,omitempty"`
	PlayerID PlayerID    `json:"playerId,omitempty"`
	Text     CP437String `json:"text"`
	History  bool        `json:"history,omitempty"`
}

// AnnounceMessage is the server-wide shutdown warning (AnnounceShutdown):
// server-authored text and the seconds left. Named for the same reason as
// ChatMessage.
type AnnounceMessage struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Seconds int    `json:"seconds"`
}

type PrivateResultMessage struct {
	Type      string   `json:"type"`
	PlayerID  PlayerID `json:"playerId,omitempty"`
	Delivered bool     `json:"delivered"`
	Text      string   `json:"text"`
}

type FollowMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	Follow   bool     `json:"follow"`
}

type FollowResultMessage struct {
	Type     string      `json:"type"`
	PlayerID PlayerID    `json:"playerId,omitempty"`
	Name     CP437String `json:"name,omitempty"`
	Followed bool        `json:"followed"`
	Text     CP437String `json:"text"`
}

// BlockMessage is the client's block/unblock request. The target is named by the
// PlayerID that rides every chat line and every roster row — a display name
// would not do, since names are neither unique nor claimed.
//
// Blocked is explicit rather than a toggle: a toggle computed on the client
// would invert the wrong way whenever the two disagreed, and the recipient of a
// mis-toggled block never finds out that they stopped hearing somebody.
type BlockMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	Blocked  bool     `json:"blocked"`
}

// BlockResultMessage is the blocker's own confirmation. Durable says whether the
// block outlives this session, which the UI must say out loud: a block on a
// guest cannot be made durable — there is no id to key it on — and a player who
// thinks they are done with someone should not be surprised tomorrow.
type BlockResultMessage struct {
	Type     string      `json:"type"`
	PlayerID PlayerID    `json:"playerId"`
	Name     CP437String `json:"name,omitempty"`
	Blocked  bool        `json:"blocked"`
	Durable  bool        `json:"durable"`
	// Text is the one line the client shows. The server writes it because the
	// server is what knows which of the four outcomes happened (blocked or
	// lifted, durable or session-only, or a player who has already gone).
	Text CP437String `json:"text"`
}

// HelpDir is where HelpFileLines looks for .HLP files. The terminal client
// resolves them relative to the working directory; the server may run from
// elsewhere.
var HelpDir = "."

var (
	helpCacheMu sync.Mutex
	helpCache   = map[string][]string{}
)

// HelpFileLines reads a .HLP file into text-window lines. It runs on the
// protocol boundary, never in the simulation: the sim only emits the filename.
func HelpFileLines(filename string) []string {
	if filename == "" {
		return nil
	}

	helpCacheMu.Lock()
	defer helpCacheMu.Unlock()
	if lines, ok := helpCache[filename]; ok {
		return lines
	}

	var state TTextWindowState
	TextWindowOpenFile(filepath.Join(HelpDir, filename), &state)
	lines := make([]string, 0, state.LineCount)
	for i := int16(0); i < state.LineCount; i++ {
		lines = append(lines, state.Lines[i])
	}
	helpCache[filename] = lines
	return lines
}

const (
	InputMaskUp uint16 = 1 << iota
	InputMaskDown
	InputMaskLeft
	InputMaskRight
	InputMaskShift
	InputMaskShoot
)

type JoinMessage struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	World string `json:"world,omitempty"`
	Board int16  `json:"board,omitempty"`
	// Color is the "#RRGGBB" background this player's ☻ is drawn on in other
	// browsers. Presentation only: it never reaches Board.Tiles, StateHash or a
	// recording. Untrusted like Name; SanitizePlayerColor validates it before
	// it is stored. Absent means vanilla white-on-blue.
	Color string `json:"color,omitempty"`
	// ResumeToken, when it names a detached (or live) player in the joined
	// instance, reclaims that run instead of spawning a new player. An unknown
	// or expired token falls through to a normal fresh join.
	ResumeToken string `json:"resumeToken,omitempty"`
	// Spectate asks to WATCH the world rather than play it. A watcher gets
	// what a player gets, minus a stat, so it is a join mode, not a message.
	//
	// A spectating connection never reaches RoomManager: no player, no stat,
	// no input, and no entry in inst.Clients, which keeps it out of the roster,
	// the picker's occupancy, the chat fan-out and the resume-token table.
	// Board is the only other field it reads (0 defaults as for a player);
	// Name, Color and ResumeToken are ignored.
	Spectate bool `json:"spectate,omitempty"`
}

// EditorEnterMessage opens an isolated editing copy of World. It is the first
// message on an editor WebSocket, instead of JoinMessage, so editor users never
// become live-room players.
type EditorEnterMessage struct {
	Type  string `json:"type"`
	World string `json:"world"`
	// ResumeToken is the editor's counterpart to JoinMessage.ResumeToken: the
	// token the entry snapshot carried. If the session still holds that member,
	// this connection takes the membership over (same id, color, board, cursor
	// and leases) and the stale socket is closed, so one person never holds two
	// members. An unknown or already-exited token enters fresh.
	ResumeToken string `json:"resumeToken,omitempty"`
}

// EditorInspectMessage reports a client-local cursor position. The server does
// not retain that position: it only reads the isolated session to build the
// sidebar inspection result.
type EditorInspectMessage struct {
	Type    string            `json:"type"`
	X       int16             `json:"x,omitempty"`
	Y       int16             `json:"y,omitempty"`
	Inspect EditorTileInspect `json:"inspect,omitempty"`
}

type EditorPresence struct {
	ID    string      `json:"id"`
	Name  CP437String `json:"name"`
	Color byte        `json:"color"`
	// BoardID is the board this member is editing. A cursor is only meaningful
	// to viewers on the same board. Presentation only: edit authorisation stays
	// with the lease system, never with cursor visibility.
	BoardID int16 `json:"boardId"`
	X       int16 `json:"x"`
	Y       int16 `json:"y"`
}

type EditorPresenceMessage struct {
	Type    string           `json:"type"`
	Members []EditorPresence `json:"members"`
}

// EditorLeaseMessage coordinates modal/code-editor exclusivity in collaborative
// editor sessions. Clients request/release "board" or "stat" leases; the server
// replies with Op "granted" or "refused". A refusal includes the holder's name
// for the browser's "being edited by" dialog.
type EditorLeaseMessage struct {
	Type       string      `json:"type"`
	Op         string      `json:"op"`
	Kind       string      `json:"kind"`
	BoardID    int16       `json:"boardId,omitempty"`
	StatID     int16       `json:"statId,omitempty"`
	HolderID   string      `json:"holderId,omitempty"`
	HolderName CP437String `json:"holderName,omitempty"`
	Error      string      `json:"error,omitempty"`
}

type EditorTileInspect struct {
	X                   int16       `json:"x"`
	Y                   int16       `json:"y"`
	ElementID           byte        `json:"elementId"`
	Element             string      `json:"element"`
	Character           byte        `json:"character"`
	Color               byte        `json:"color"`
	HasStat             bool        `json:"hasStat"`
	StatID              int16       `json:"statId,omitempty"`
	P1                  byte        `json:"p1,omitempty"`
	P2                  byte        `json:"p2,omitempty"`
	P3                  byte        `json:"p3,omitempty"`
	StepX               int16       `json:"stepX,omitempty"`
	StepY               int16       `json:"stepY,omitempty"`
	Cycle               int16       `json:"cycle,omitempty"`
	Param1Name          string      `json:"param1Name,omitempty"`
	Param2Name          string      `json:"param2Name,omitempty"`
	ParamBulletTypeName string      `json:"paramBulletTypeName,omitempty"`
	ParamBoardName      CP437String `json:"paramBoardName,omitempty"`
	ParamDirName        string      `json:"paramDirName,omitempty"`
	ParamTextName       CP437String `json:"paramTextName,omitempty"`
}

// EditorElementItem is one placeable element in an F1/F2/F3 category menu,
// derived from ElementDefs exactly as EditorLoop's category listing is
// (EDITOR.PAS:702-726): its EditorShortcut key, its display glyph, and the
// section header (CategoryName) that precedes it, if any.
type EditorElementItem struct {
	ElementID    byte   `json:"elementId"`
	Name         string `json:"name"`
	Shortcut     string `json:"shortcut"`
	Character    byte   `json:"character"`
	Color        byte   `json:"color"`
	CategoryName string `json:"categoryName,omitempty"`
}

// EditorElementMenu is one editor category (Item/Creature/Terrain) as F1/F2/F3
// present it. The menus are static (ElementDefs is immutable after init), so
// they ride the entry snapshot once rather than a message per keypress.
type EditorElementMenu struct {
	Category int16               `json:"category"`
	Key      string              `json:"key"`
	Title    string              `json:"title"`
	Items    []EditorElementItem `json:"items"`
}

// EditorSnapshotMessage intentionally uses ScreenCell, the same full-frame
// board representation as SnapshotMessage. It has no player/HUD because an
// editor session is not a room and never simulates. Menus is populated only on
// the entry snapshot: the F1/F2/F3 category tables the client renders.
type EditorSnapshotMessage struct {
	Type       string              `json:"type"`
	MemberID   string              `json:"memberId,omitempty"`
	ReadOnly   bool                `json:"readOnly,omitempty"`
	BoardID    int16               `json:"boardId"`
	Screen     []ScreenCell        `json:"screen"`
	Inspect    EditorTileInspect   `json:"inspect"`
	Properties EditorProperties    `json:"properties"`
	Menus      []EditorElementMenu `json:"menus,omitempty"`
	Presence   []EditorPresence    `json:"presence,omitempty"`
	// ResumeToken is set only on the entry snapshot, like the join snapshot's.
	// The browser presents it when its editor socket closes under it; see
	// EditorEnterMessage.ResumeToken.
	ResumeToken string `json:"resumeToken,omitempty"`
}

// EditorEditMessage is one browser editor operation. Selection and cursor
// state remain client-local; the session validates and applies this operation
// through its serialized Apply boundary. Op is "place"/"erase"/"fill" for the
// pattern brush, or "element": place the F1/F2/F3 menu element in Element,
// resolving its colour against the client's cursor colour in Color the way
// EditorLoop does (EDITOR.PAS:736-772) and adding a stat when it needs one.
// Op "text" is F4 text entry: Char is the typed printable byte and Color
// is the cursor foreground colour, which together pick the text tile the way
// EditorLoop's text branch does (EDITOR.PAS:459-467).
type EditorEditMessage struct {
	Type    string `json:"type"`
	Op      string `json:"op"`
	X       int16  `json:"x"`
	Y       int16  `json:"y"`
	Element byte   `json:"element,omitempty"`
	Color   byte   `json:"color,omitempty"`
	Copied  bool   `json:"copied,omitempty"`
	Char    byte   `json:"char,omitempty"`
}

// EditorDiffMessage is the editor counterpart of DiffMessage. It carries only
// cells dirtied by an edit plus the refreshed inspection panel for the browser
// cursor, never a live-room/player snapshot.
type EditorDiffMessage struct {
	Type     string `json:"type"`
	MemberID string `json:"memberId,omitempty"`
	// BoardID is the board the edit landed on. The server sends the diff only
	// to members viewing that board, and the client re-checks it before
	// painting, since another board's cells would overwrite the one on screen.
	BoardID int16             `json:"boardId"`
	Cells   []ScreenCell      `json:"cells"`
	Inspect EditorTileInspect `json:"inspect"`
}

// EditorBoardOption is one legal target for a board edge. Board zero is the
// vanilla "None" choice; the editor's "Add new board" choice is a client-side
// menu entry, not a board option, and travels as an EditorBoardMessage.
type EditorBoardOption struct {
	ID   int16       `json:"id"`
	Name CP437String `json:"name"`
}

// EditorProperties is the data rendered by the editor's Board Information
// dialog. It deliberately carries values, not presentation strings, so the
// browser can use the same dialog for future collaborative-edit leases.
type EditorProperties struct {
	BoardID           int16               `json:"boardId"`
	BoardName         CP437String         `json:"boardName"`
	WorldName         CP437String         `json:"worldName"`
	MaxShots          byte                `json:"maxShots"`
	IsDark            bool                `json:"isDark"`
	NeighborBoards    [4]byte             `json:"neighborBoards"`
	ReenterWhenZapped bool                `json:"reenterWhenZapped"`
	TimeLimitSec      int16               `json:"timeLimitSec"`
	Boards            []EditorBoardOption `json:"boards"`
}

// EditorPropertyMessage mutates one board/world property. Values are
// validated by EditorSession: browser controls are not an authority boundary.
// Field is one of boardTitle, worldName, maxShots, dark, exit, reenter, or
// timeLimit. Exit selects NeighborBoards[Exit], and Value is the target board.
type EditorPropertyMessage struct {
	Type  string `json:"type"`
	Field string `json:"field"`
	// Text is CP437: a name arrives as the widened runes it was sent in
	// (EditorProperties.BoardName/WorldName), never as UTF-8.
	Text  CP437String `json:"text,omitempty"`
	Value int16       `json:"value,omitempty"`
	Bool  bool        `json:"bool,omitempty"`
	Exit  int16       `json:"exit,omitempty"`
}

// EditorPropertiesMessage is returned after every property change. Screen is
// a complete board frame because toggling darkness changes more than a local
// tile; it also makes each property edit a self-contained browser repaint.
//
// Screen is omitted for members looking at a DIFFERENT board: the message
// still reaches them, because the board list and world name are
// world-scoped, but the frame is not theirs to paint.
type EditorPropertiesMessage struct {
	Type       string           `json:"type"`
	Properties EditorProperties `json:"properties"`
	Screen     []ScreenCell     `json:"screen,omitempty"`
}

// EditorStatMessage changes one stat setting. The server validates the stat
// still exists at StatID and that Field is meaningful for its element. Value
// is p1, the low seven bits of p2, bulletType, p3, direction (0..3), or cycle.
// Object program data is outside this message: see EditorProgramSaveMessage.
type EditorStatMessage struct {
	Type   string `json:"type"`
	StatID int16  `json:"statId"`
	Field  string `json:"field"`
	Value  int16  `json:"value"`
}

// EditorStatSettingsMessage is the authoritative result of a stat change.
// Cells covers character changes to objects, whose P1 affects rendering. It is
// also the reply to editorProgramSave, whose text edit never changes the tile
// but keeps the browser's inspection panel authoritative.
type EditorStatSettingsMessage struct {
	Type    string            `json:"type"`
	Inspect EditorTileInspect `json:"inspect"`
	Cells   []ScreenCell      `json:"cells"`
}

// EditorProgramRequestMessage asks for a stat's ZZT-OOP program so the browser's
// code editor can open it. Only text-backed elements (objects and scrolls,
// whose ElementDefs entry has a ParamTextName) carry a program.
type EditorProgramRequestMessage struct {
	Type   string `json:"type"`
	StatID int16  `json:"statId"`
}

// EditorProgramMessage carries one object/scroll program to the browser, split
// into lines exactly as CopyStatDataToTextWindow does (on carriage returns).
// Prompt is the element's ParamTextName ("Edit Program" / "Edit text of scroll")
// so the editor window titles itself the way vanilla does.
// Labels and Warnings are authoring aids: the object's :labels (for
// navigation) and advisory diagnostics from OopAnalyze. They are informational
// only — a program with warnings still saves.
type EditorProgramMessage struct {
	Type     string         `json:"type"`
	StatID   int16          `json:"statId"`
	Prompt   string         `json:"prompt"`
	Lines    CP437Lines     `json:"lines"`
	Labels   []OopLabelInfo `json:"labels,omitempty"`
	Warnings []OopWarning   `json:"warnings,omitempty"`
}

// EditorProgramSaveMessage writes an edited program back. The server rebuilds
// Data/DataLen the way EditorEditStatText does — a carriage return after every
// line — so the text round-trips through the vanilla serializer.
type EditorProgramSaveMessage struct {
	Type   string     `json:"type"`
	StatID int16      `json:"statId"`
	Lines  CP437Lines `json:"lines"`
}

// EditorBoardMessage manages the boards of an editor session. Op is:
//
//	"add"    — EditorAppendBoard: append a new board named Name, make it current
//	"switch" — BoardChange to BoardID (0..BoardCount), keeping session edits
//	"export" — EditorTransferBoard export: reply with the current board's .BRD
//	"import" — EditorTransferBoard import: replace the current board with Data,
//	           base64-encoded .BRD bytes (2-byte length prefix + board data)
//	"clear"  — EditorLoop 'Z': empty the current board (EDITOR.PAS:591)
//	"new"    — EditorLoop 'N': reset to a fresh one-board world (EDITOR.PAS:600)
//
// add/switch/import/clear/new reply with a full EditorSnapshotMessage because a
// board change repaints the whole frame; export replies with EditorBoardDataMessage.
type EditorBoardMessage struct {
	Type    string      `json:"type"`
	Op      string      `json:"op"`
	Name    CP437String `json:"name,omitempty"` // CP437, like every board name on the wire (M36.2)
	BoardID int16       `json:"boardId,omitempty"`
	Data    string      `json:"data,omitempty"`
}

// EditorBoardDataMessage is the reply to an "export": the current board as
// vanilla .BRD bytes, base64-encoded, plus a SanitizeSaveName filename stem the
// browser uses for the download. The format is BlockWrite's: a 2-byte
// little-endian length followed by that many bytes of serialized board data,
// so the file loads in DOS ZZT and re-imports here alike.
type EditorBoardDataMessage struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
}

// EditorWorldMessage saves, downloads, or uploads the whole editor session
// world. Op is:
//
//	"save"     — serialize the session world and write it to the hosted worlds
//	             directory as Name.ZZT (SanitizeSaveName), then host it so the
//	             world picker sees it. Refused if a world of that name is being
//	             played (RestoreSnapshot's occupancy rule). Replies
//	             EditorSaveResultMessage.
//	"download" — reply EditorWorldDataMessage with the session world's vanilla
//	             .ZZT bytes, so a creator owns a portable file.
//	"upload"   — replace the session world with Data (base64 .ZZT bytes from a
//	             client file) after the upload gate (headless load + 200 steps,
//	             no panic). Replies a full EditorSnapshotMessage, or an
//	             EditorSaveResultMessage carrying the gate error on refusal.
//	"invite"   — owner-only collaborator invite by AccountID.
type EditorWorldMessage struct {
	Type      string `json:"type"`
	Op        string `json:"op"`
	Name      string `json:"name,omitempty"`
	Data      string `json:"data,omitempty"`
	AccountID string `json:"accountId,omitempty"`
}

// EditorWorldDataMessage is the reply to a "download": the whole session world as
// vanilla .ZZT bytes, base64-encoded, plus a SanitizeSaveName filename stem. The
// bytes come straight from worldWriteTo, so the file loads in DOS ZZT/zeta and
// through WorldLoad here alike.
type EditorWorldDataMessage struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
}

// EditorSaveResultMessage reports the outcome of a "save" or a refused "upload".
// World is the hosted world name on success; Error explains a refusal.
type EditorSaveResultMessage struct {
	Type  string `json:"type"`
	World string `json:"world,omitempty"`
	Error string `json:"error,omitempty"`
}

// EditorTestPlayMessage starts or announces a private play-test room.
// The request is just Type; the reply carries a private hosted World name, or
// Error if the session copy could not be created.
type EditorTestPlayMessage struct {
	Type  string `json:"type"`
	World string `json:"world,omitempty"`
	Error string `json:"error,omitempty"`
}

type InputMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	Seq      uint64   `json:"seq"`
	DeltaX   int16    `json:"dx"`
	DeltaY   int16    `json:"dy"`
	Shift    bool     `json:"shift"`
	Key      byte     `json:"key"`
	Keymask  uint16   `json:"keymask,omitempty"`
}

// DebugCommandMessage is the client's reply to a debugPrompt event: the text
// typed into the sidebar prompt. Empty text (a cancelled prompt) is a no-op
// that still matches vanilla behavior, where Escape restores the old buffer.
type DebugCommandMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	Text     string   `json:"text"`
}

// ScrollReplyMessage is the client's hyperlink selection from a scroll window.
// StatID is the object that showed the scroll, and Label is the text between
// '!' and ';' — i.e. the ZZT-OOP label to send it.
type ScrollReplyMessage struct {
	Type     string      `json:"type"`
	PlayerID PlayerID    `json:"playerId"`
	StatID   int16       `json:"statId"`
	Label    CP437String `json:"label"`
}

// QuitReplyMessage is the client's answer to a quitPrompt event. Quit=false
// (the player said no, or pressed Escape) is a no-op the engine still drains.
type QuitReplyMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	Quit     bool     `json:"quit"`
}

// HighScoreNameMessage carries the name typed into the "Congratulations!" entry
// that follows a qualifying highScoreEntry event. The server, not the sim, owns
// the list: see RoomManager.RecordHighScore.
type HighScoreNameMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	Name     string   `json:"name"`
}

// SaveFilenameMessage answers a savePrompt event with the name the player
// typed. The server sanitizes it (SanitizeSaveName) before it reaches a path.
type SaveFilenameMessage struct {
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	Name     string   `json:"name"`
}

type SnapshotMessage struct {
	Type    string           `json:"type"`
	World   string           `json:"world,omitempty"`
	BoardID int16            `json:"boardId"`
	Tick    int16            `json:"tick"`
	Seed    uint32           `json:"seed"`
	Hash    uint64           `json:"hash"`
	You     PlayerSnapshot   `json:"you"`
	Players []PlayerSnapshot `json:"players"`
	HUD     HUDSnapshot      `json:"hud"`
	Screen  []ScreenCell     `json:"screen"`
	Events  []ProtocolEvent  `json:"events,omitempty"`
	// ResumeToken is set on the arrival snapshots: a join, a resume, and the
	// snapshot a world transit lands with. The client stores it keyed by world
	// name and presents it to reclaim a dropped run.
	ResumeToken string `json:"resumeToken,omitempty"`
	// BlockedPlayers lists which players in THIS snapshot's roster the
	// recipient has blocked. Set on the arrival snapshots. It carries
	// PlayerIDs, never the stored accountIDs, so it reveals nothing the
	// recipient cannot already see. The client merges it and never clears from
	// it: a roster-scoped list can add knowledge but never withdraw it.
	BlockedPlayers []PlayerID `json:"blockedPlayers,omitempty"`
	// FollowedPlayers is BlockedPlayers for follows: which visible players the
	// recipient follows, as live PlayerIDs so account ids stay server-only.
	FollowedPlayers []PlayerID `json:"followedPlayers,omitempty"`
	// Operator says this account is on the moderator allowlist, so the Players
	// window offers mute, kick and refuse. Presentation only: the server
	// re-checks the allowlist on every action. Set on the arrival snapshots;
	// a frame that omits it is silence, not a revocation.
	Operator bool `json:"operator,omitempty"`
	// Spectator marks a frame addressed to a WATCHER, which puts the client in
	// read-only mode. It is explicit rather than inferred from an empty `you`,
	// so a malformed frame never makes a browser send input into a room it is
	// not in. A spectator frame carries no `you`, HUD or events; see
	// RoomManager.SpectatorSnapshot.
	Spectator bool `json:"spectator,omitempty"`
	// Watchers is how many people are watching THIS board, counting the
	// recipient. Set on every snapshot and diff, to players and watchers alike,
	// so absent means zero. Presentation only: never in Board.Tiles, StateHash
	// or a recording.
	Watchers int `json:"watchers,omitempty"`
	// Challenge and ChallengeRun are set on the join/resume snapshot of a
	// challenge run. ChallengeRun is the instance key a reconnect presents
	// to reclaim THIS run; it is not a world name, so `?world=` cannot reach it.
	Challenge    string `json:"challenge,omitempty"`
	ChallengeRun string `json:"challengeRun,omitempty"`
}

type DiffMessage struct {
	Type    string           `json:"type"`
	BoardID int16            `json:"boardId"`
	Tick    int16            `json:"tick"`
	Hash    uint64           `json:"hash"`
	Cells   []ScreenCell     `json:"cells,omitempty"`
	Players []PlayerSnapshot `json:"players,omitempty"`
	HUD     *HUDSnapshot     `json:"hud,omitempty"`
	Events  []ProtocolEvent  `json:"events,omitempty"`
	// Watchers is SnapshotMessage.Watchers on the per-tick frame. Both a
	// player's diff and a watcher's carry it.
	Watchers int `json:"watchers,omitempty"`
}

type EventMessage struct {
	Type    string        `json:"type"`
	Event   ProtocolEvent `json:"event"`
	BoardID int16         `json:"boardId,omitempty"`
	Tick    int16         `json:"tick,omitempty"`
}

type BoardChangeMessage struct {
	Type     string          `json:"type"`
	Snapshot SnapshotMessage `json:"snapshot"`
}

type ScreenCell struct {
	X     int16 `json:"x"`
	Y     int16 `json:"y"`
	Ch    byte  `json:"ch"`
	Color byte  `json:"color"`
	// Element is the element the screen is SHOWING here, or 0 when the board
	// is holding it back. See (*Engine).disclosedElement. Omitted when zero,
	// so a client that does not know the field is unaffected.
	Element byte `json:"element,omitempty"`
}

type PlayerSnapshot struct {
	ID     PlayerID `json:"id"`
	StatID int16    `json:"statId"`
	X      int16    `json:"x"`
	Y      int16    `json:"y"`
	Health int16    `json:"health"`
	// Name and Color are presentation fields on every snapshot and diff,
	// never read by the simulation. An absent Color means vanilla
	// white-on-blue, which is what an old client, a replayed session and a
	// player who has not picked one all send.
	Name       CP437String `json:"name,omitempty"`
	Color      string      `json:"color,omitempty"`
	Handle     string      `json:"handle,omitempty"`
	HasProfile bool        `json:"hasProfile,omitempty"`
}

// SanitizePlayerColor accepts exactly "#" plus six hexadecimal digits and
// returns it unchanged; everything else becomes the empty string, which the
// client renders as vanilla white-on-blue. The value comes off the wire and is
// broadcast to every other player's browser, so it is untrusted input: nothing
// but a fixed-length hex triple may ever reach another client's fillStyle.
func SanitizePlayerColor(color string) string {
	if len(color) != 7 || color[0] != '#' {
		return ""
	}
	for i := 1; i < 7; i++ {
		c := color[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return ""
		}
	}
	return color
}

// HUDSnapshot carries everything the client needs to draw the 20x25 ZZT
// sidebar itself. TimeLimitSec and SoundEnabled are board/engine state rather
// than player state, but the sidebar reads them, so they ride along here.
type HUDSnapshot struct {
	Health         int16   `json:"health"`
	Ammo           int16   `json:"ammo"`
	Gems           int16   `json:"gems"`
	Torches        int16   `json:"torches"`
	TorchTicks     int16   `json:"torchTicks"`
	EnergizerTicks int16   `json:"energizerTicks"`
	Score          int16   `json:"score"`
	Keys           [7]bool `json:"keys"`
	BoardTimeSec   int16   `json:"boardTimeSec"`
	BoardTimeHsec  int16   `json:"boardTimeHsec"`
	TimeLimitSec   int16   `json:"timeLimitSec"`
	SoundEnabled   bool    `json:"soundEnabled"`
}

type ProtocolEvent struct {
	Type   string `json:"type"`
	StatID int16  `json:"statId,omitempty"`
	// PlayerStatID is the player a scroll belongs to (-1 = nobody). Explicitly
	// not omitempty: stat 0 is a real player and -1 must survive the wire.
	PlayerStatID int16       `json:"playerStatId"`
	Title        CP437String `json:"title,omitempty"`
	Lines        CP437Lines  `json:"lines,omitempty"`
	Filename     string      `json:"filename,omitempty"`
	// Error carries a refusal back to the client on a "saveResult" event. Empty
	// means the save succeeded, so no extra bool rides on every other event.
	Error    string   `json:"error,omitempty"`
	Score    int16    `json:"score,omitempty"`
	ListPos  int16    `json:"listPos,omitempty"`
	Notes    []uint16 `json:"notes,omitempty"`
	Priority int16    `json:"priority,omitempty"`
	X        int16    `json:"x,omitempty"`
	Y        int16    `json:"y,omitempty"`
	ToBoard  int16    `json:"toBoard,omitempty"`
	EntryX   int16    `json:"entryX,omitempty"`
	EntryY   int16    `json:"entryY,omitempty"`
	// FreqHz is the raw tone frequency on a "walkClick" event — unlike "sound"'s
	// Notes/Priority, a walk click bypasses SoundQueue entirely.
	FreqHz uint16 `json:"freqHz,omitempty"`
	// Paused is the new paused state on a "pause" event. Explicitly not
	// omitempty: false is the unpause signal and must survive the wire.
	Paused bool `json:"paused"`
}

func NewSnapshotMessage(e *Engine, boardID int16, playerID PlayerID, statID int16, players []PlayerSnapshot) SnapshotMessage {
	return SnapshotMessage{
		Type:    MessageTypeSnapshot,
		BoardID: boardID,
		Tick:    e.CurrentTick,
		Seed:    e.RandSeed,
		Hash:    StateHash(e),
		You:     playerSnapshot(e, playerID, statID),
		Players: players,
		HUD:     hudSnapshot(e, statID),
		Screen:  screenCells(e),
		Events:  ProtocolEvents(e.Events),
	}
}

func ProtocolEvents(events []Event) []ProtocolEvent {
	var out []ProtocolEvent
	for _, event := range events {
		switch ev := event.(type) {
		case ScrollEvent:
			out = append(out, ProtocolEvent{Type: "scroll", StatID: ev.StatId, PlayerStatID: ev.PlayerStatId, Title: CP437String(ev.Title), Lines: ev.Lines})
		case QuitPromptEvent:
			out = append(out, ProtocolEvent{Type: "quitPrompt", StatID: ev.StatId})
		case QuitEvent:
			out = append(out, ProtocolEvent{Type: "quit", StatID: ev.StatId})
		case HelpEvent:
			out = append(out, ProtocolEvent{Type: "help", StatID: ev.StatId, Filename: ev.Filename, Title: CP437String(ev.Title), Lines: HelpFileLines(ev.Filename)})
		case DebugPromptEvent:
			out = append(out, ProtocolEvent{Type: "debugPrompt", StatID: ev.StatId})
		case SavePromptEvent:
			out = append(out, ProtocolEvent{Type: "savePrompt", StatID: ev.StatId})
		case PauseEvent:
			out = append(out, ProtocolEvent{Type: "pause", StatID: ev.StatId, Paused: ev.Paused})
		case HighScoreEntryEvent:
			out = append(out, ProtocolEvent{Type: "highScoreEntry", StatID: ev.StatId, Score: ev.Score, ListPos: ev.ListPos})
		case SoundEvent:
			event := ProtocolEvent{Type: "sound", Notes: soundNoteBytes(ev.Notes), Priority: ev.Priority}
			if ev.StatId >= 0 {
				event.StatID = ev.StatId
			}
			out = append(out, event)
		case WalkClickEvent:
			out = append(out, ProtocolEvent{Type: "walkClick", StatID: ev.StatId, FreqHz: ev.FreqHz})
		case DeathEvent:
			out = append(out, ProtocolEvent{Type: "death", StatID: ev.StatId})
		case RespawnEvent:
			out = append(out, ProtocolEvent{Type: "respawn", StatID: ev.StatId, X: ev.X, Y: ev.Y})
		case TransferEvent:
			out = append(out, ProtocolEvent{Type: "transfer", StatID: ev.StatId, ToBoard: ev.ToBoard, EntryX: ev.EntryX, EntryY: ev.EntryY})
		}
	}
	return out
}

func soundNoteBytes(notes string) []uint16 {
	if notes == "" {
		return nil
	}
	out := make([]uint16, len(notes))
	for i := range notes {
		out[i] = uint16(notes[i])
	}
	return out
}

// disclosedElement is the element a client is allowed to know sits at a screen
// position: what the screen is SHOWING there, never what the board is holding
// back.
//
// It exists because two elements can be drawn with the same byte. A fake wall
// is E_FAKE drawn with the normal wall's 0xB2 -- ElementDefs gives 22 and 27
// the same Character on purpose -- so no terminal can tell them apart from the
// glyph, and a client that draws the board in three dimensions has to stand a
// walkable floor up as a wall. Naming the element lets it draw a fake as the
// floor it behaves like.
//
// What must not leak is everything the board hides deliberately:
//
//   - A dark room. TileToColorAndChar draws an unlit square as 0xB0 on 0x07,
//     and that fog is all any client may know; naming what stands underneath
//     would make a torch pointless. The fog is read back off the drawn screen
//     rather than by re-running the darkness test, because that test is per
//     player -- it asks NearestPlayer for a torch -- while DrainScreenDirty
//     produces one frame broadcast to a whole room.
//   - Anything drawn as a blank that is not empty: the invisible wall, which
//     draws ' ' until it is touched and then becomes E_NORMAL. The rule is
//     written in terms of what was drawn rather than as a list of elements, so
//     an element that hides itself the same way is covered without anyone
//     having to remember this function exists.
func (e *Engine) disclosedElement(sx, sy int16) byte {
	if sx < 0 || sx >= BOARD_WIDTH || sy < 0 || sy >= BOARD_HEIGHT {
		return 0
	}
	screen := e.Screen[sx][sy]
	if screen.Ch == '\xb0' && screen.Color == 0x07 {
		return 0
	}
	element := e.Board.Tiles[sx+1][sy+1].Element
	if element != E_EMPTY && screen.Ch == ' ' {
		return 0
	}
	return element
}

func screenCells(e *Engine) []ScreenCell {
	width := e.netScreenWidth()
	cells := make([]ScreenCell, 0, int(width)*25)
	for y := int16(0); y < 25; y++ {
		for x := int16(0); x < width; x++ {
			cell := e.Screen[x][y]
			cells = append(cells, ScreenCell{X: x, Y: y, Ch: cell.Ch, Color: cell.Color, Element: e.disclosedElement(x, y)})
		}
	}
	return cells
}

func playerSnapshot(e *Engine, playerID PlayerID, statID int16) PlayerSnapshot {
	stat := &e.Board.Stats[statID]
	return PlayerSnapshot{
		ID:     playerID,
		StatID: statID,
		X:      int16(stat.X),
		Y:      int16(stat.Y),
		Health: e.PlayerFor(statID).Health,
	}
}

func hudSnapshot(e *Engine, statID int16) HUDSnapshot {
	pState := e.PlayerFor(statID)
	return HUDSnapshot{
		TimeLimitSec:   e.Board.Info.TimeLimitSec,
		SoundEnabled:   pState.SoundEnabled,
		Health:         pState.Health,
		Ammo:           pState.Ammo,
		Gems:           pState.Gems,
		Torches:        pState.Torches,
		TorchTicks:     pState.TorchTicks,
		EnergizerTicks: pState.EnergizerTicks,
		Score:          pState.Score,
		Keys:           pState.Keys,
		BoardTimeSec:   pState.BoardTimeSec,
		BoardTimeHsec:  pState.BoardTimeHsec,
	}
}
