package zztgo

import (
	"path/filepath"
	"sync"
)

const (
	MessageTypeJoin           = "join"
	MessageTypeInput          = "input"
	MessageTypeSnapshot       = "snapshot"
	MessageTypeDiff           = "diff"
	MessageTypeEvent          = "event"
	MessageTypeBoardChange    = "boardChange"
	MessageTypeDebugCommand   = "debugCommand"
	MessageTypeScrollReply    = "scrollReply"
	MessageTypeQuitReply      = "quitReply"
	MessageTypeHighScoreName  = "highScoreName"
	MessageTypeSaveFilename   = "saveFilename"
	MessageTypeEditorEnter    = "editorEnter"
	MessageTypeEditorExit     = "editorExit"
	MessageTypeEditorInspect  = "editorInspect"
	MessageTypeEditorSnapshot = "editorSnapshot"
)

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
}

// EditorEnterMessage opens an isolated editing copy of World. It is the first
// message on an editor WebSocket, instead of JoinMessage, so editor users never
// become live-room players.
type EditorEnterMessage struct {
	Type  string `json:"type"`
	World string `json:"world"`
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

type EditorTileInspect struct {
	X       int16  `json:"x"`
	Y       int16  `json:"y"`
	Element string `json:"element"`
	Color   byte   `json:"color"`
	HasStat bool   `json:"hasStat"`
	StatID  int16  `json:"statId,omitempty"`
	P1      byte   `json:"p1,omitempty"`
	P2      byte   `json:"p2,omitempty"`
	P3      byte   `json:"p3,omitempty"`
}

// EditorSnapshotMessage intentionally uses ScreenCell, the same full-frame
// board representation as SnapshotMessage. It has no player/HUD because an
// editor session is not a room and never simulates.
type EditorSnapshotMessage struct {
	Type    string            `json:"type"`
	BoardID int16             `json:"boardId"`
	Screen  []ScreenCell      `json:"screen"`
	Inspect EditorTileInspect `json:"inspect"`
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
	Type     string   `json:"type"`
	PlayerID PlayerID `json:"playerId"`
	StatID   int16    `json:"statId"`
	Label    string   `json:"label"`
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
	BoardID int16            `json:"boardId"`
	Tick    int16            `json:"tick"`
	Seed    uint32           `json:"seed"`
	Hash    uint64           `json:"hash"`
	You     PlayerSnapshot   `json:"you"`
	Players []PlayerSnapshot `json:"players"`
	HUD     HUDSnapshot      `json:"hud"`
	Screen  []ScreenCell     `json:"screen"`
	Events  []ProtocolEvent  `json:"events,omitempty"`
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
}

type PlayerSnapshot struct {
	ID     PlayerID `json:"id"`
	StatID int16    `json:"statId"`
	X      int16    `json:"x"`
	Y      int16    `json:"y"`
	Health int16    `json:"health"`
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
	PlayerStatID int16    `json:"playerStatId"`
	Title        string   `json:"title,omitempty"`
	Lines        []string `json:"lines,omitempty"`
	Filename     string   `json:"filename,omitempty"`
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
			out = append(out, ProtocolEvent{Type: "scroll", StatID: ev.StatId, PlayerStatID: ev.PlayerStatId, Title: ev.Title, Lines: ev.Lines})
		case QuitPromptEvent:
			out = append(out, ProtocolEvent{Type: "quitPrompt", StatID: ev.StatId})
		case QuitEvent:
			out = append(out, ProtocolEvent{Type: "quit", StatID: ev.StatId})
		case HelpEvent:
			out = append(out, ProtocolEvent{Type: "help", StatID: ev.StatId, Filename: ev.Filename, Title: ev.Title, Lines: HelpFileLines(ev.Filename)})
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

func screenCells(e *Engine) []ScreenCell {
	width := e.netScreenWidth()
	cells := make([]ScreenCell, 0, int(width)*25)
	for y := int16(0); y < 25; y++ {
		for x := int16(0); x < width; x++ {
			cell := e.Screen[x][y]
			cells = append(cells, ScreenCell{X: x, Y: y, Ch: cell.Ch, Color: cell.Color})
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
