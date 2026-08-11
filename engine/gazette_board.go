package zztgo

// The Gazette's board (M34.3): the day's edition posted where people loiter.
//
// The lobby has a newsstand — one ordinary ZZT object, standing on a tile the
// server owns — and touching it opens a scroll the server writes. Three things
// about that are decisions rather than convenience:
//
//   - The window is a scroll like any other. It carries the OBJECT's stat id
//     and the READER's, so the client's ordinary dismissal (closeModal ->
//     scrollReply) is what unfreezes the reader. There is no second freeze
//     protocol to keep in sync, and no client change: a pushed scroll and a
//     touched one are the same message.
//
//   - The stand's own program is a fallback, not the product. It is what a
//     LOBBY.ZZT opened in vanilla shows, and what this file's absence would
//     show. RoomManager suppresses it only when the server is actually going to
//     write something better.
//
//   - Reading the paper in the lobby is what buys tomorrow's. A touch kicks the
//     same single-flight refresh the HTTP route kicks, under the same four
//     spend bounds M34.2 established, so a service whose players never open a
//     browser tab still prints a paper and a busy lobby still pays once.
//
// Composing the paper is deliberately NOT tick work: naming a day's actors is
// one preferences read per row, and the tick loop waits on nothing (M16.14e).
// The drain happens under the step's lock; everything below runs after it.

import (
	"context"
	"log"
)

// gazetteBoardMaxLines bounds what one touch can put on a reader's screen. The
// edition is already bounded twice over (M34.2), so this is the belt to that
// braces: a text window is 25 rows and a reader is standing up.
const gazetteBoardMaxLines = 20

// GazetteEditions returns the process's single edition writer, building it on
// first use the way /api/generate builds its generator: a server with Anthropic
// credentials in its environment gets an author with no further wiring, and one
// without gets an editor that only ever serves the server-written edition.
//
// preferred is the author the caller already has (WebAPI's generator, when
// cmd/ configured one); nil asks the environment. Whoever asks first wins, and
// in production both answers are the same credentials.
func (s *WebSocketServer) GazetteEditions(preferred GazetteAuthor) *GazetteEditor {
	if s == nil || s.Gazette == nil {
		return nil
	}
	s.gazetteEditorMu.Lock()
	defer s.gazetteEditorMu.Unlock()
	if s.gazetteEditor != nil {
		return s.gazetteEditor
	}
	author := preferred
	if author == nil {
		if generator, err := GenerationServiceFromEnv(); err == nil {
			author = generator
		}
	}
	editor, err := NewGazetteEditor(s.Gazette, author, s.Gazette.editionsPath())
	if err != nil {
		log.Printf("gazette editions unavailable: %v", err)
		editor, _ = NewGazetteEditor(s.Gazette, author, "")
	}
	s.gazetteEditor = editor
	return editor
}

// gazetteNameResolver is the consent rule's read half (M34.1), on the server's
// side of the house: an account is named only by the deliberate public subset
// of its profile, and the map is per-call because one day's edition names the
// same account in several rows.
func (s *WebSocketServer) gazetteNameResolver() func(string) string {
	seen := make(map[string]string)
	return func(accountKey string) string {
		if accountKey == "" {
			return ""
		}
		if name, ok := seen[accountKey]; ok {
			return name
		}
		prefs, found := s.loadAccountPreferences(accountKey)
		name := GazetteConsentedName(prefs, found)
		seen[accountKey] = name
		return name
	}
}

// GazetteNoticeKind is the notice a newsstand tile posts. It lived in lobby.go
// beside the tile that placed it until the lobby was removed (owner
// 2026-08-11); the placement is gone, the mechanism is not, so a future world
// can stand a newsstand on a tile by naming this kind in NoticeTiles. Nothing
// populates NoticeTiles today, so postNotice is currently unreachable in
// production and is exercised only by its tests.
const GazetteNoticeKind = "gazette"

// postNotice answers one touch of a server-owned tile. It runs off the tick
// goroutine and is the only thing that ever writes a notice window.
func (s *WebSocketServer) postNotice(ctx context.Context, inst *WorldInstance, notice RoomNotice) {
	if s == nil || inst == nil {
		return
	}
	if notice.Kind != GazetteNoticeKind {
		return
	}
	editor := s.GazetteEditions(nil)
	if editor == nil {
		// No ledger, so no paper. The reader is already frozen by the step that
		// queued this, so they are still owed a window: the stand's own copy is
		// the honest one to send.
		s.writeNoticeScroll(ctx, inst, notice, "The ZZT Gazette", []string{
			"",
			"  Nobody is printing a paper here",
			"  today.",
			"",
		})
		return
	}
	edition := editor.Edition("", s.gazetteNameResolver())
	editor.RefreshAsync(edition.Day)
	title, lines := gazetteBoardWindow(edition)
	s.writeNoticeScroll(ctx, inst, notice, title, lines)
}

// gazetteBoardWindow turns a rendered edition into the window a reader sees.
// The lines arrive already wrapped and already named (M34.2 guarantees both,
// including that none of them begins with a byte ZZT-OOP reads as markup), so
// nothing here re-wraps them — the only text this function adds is its own
// dateline, which is server-written and short.
func gazetteBoardWindow(edition GazetteRenderedEdition) (string, []string) {
	title := edition.Headline
	if title == "" {
		title = "The ZZT Gazette"
	}
	lines := make([]string, 0, gazetteBoardMaxLines)
	lines = append(lines, "")
	for _, line := range edition.Lines {
		if len(lines) >= gazetteBoardMaxLines-2 {
			break
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "The ZZT Gazette, "+edition.Day)
	return title, lines
}

// writeNoticeScroll unicasts the window to the one reader who touched the tile.
// PlayerStatID makes it read as a touch rather than as an announcement to the
// room (the client's isMyScroll), and StatID names the object so the dismissal
// reply lands where the engine expects it and the reader moves again.
func (s *WebSocketServer) writeNoticeScroll(ctx context.Context, inst *WorldInstance, notice RoomNotice, title string, lines []string) {
	inst.mu.Lock()
	client := inst.Clients[notice.PlayerID]
	readerStatID := int16(-1)
	if player := inst.RoomManager.players[notice.PlayerID]; player != nil {
		readerStatID = player.statID
	}
	inst.mu.Unlock()
	if client == nil {
		return
	}
	_ = client.write(ctx, EventMessage{Type: MessageTypeEvent, Event: ProtocolEvent{
		Type:         "scroll",
		StatID:       notice.ObjectStatID,
		PlayerStatID: readerStatID,
		Title:        title,
		Lines:        lines,
	}})
}
