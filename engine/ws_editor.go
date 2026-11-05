package zztgo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// serveEditor owns an editor-only WebSocket. Unlike a game connection it never
// joins RoomManager, so an editor cannot affect ticks, players, or live state.
func (s *WebSocketServer) serveEditor(ctx context.Context, conn *websocket.Conn, enter EditorEnterMessage, account AuthenticatedAccount, authenticated bool) {
	worldName := enter.World
	if worldName == "" {
		worldName = s.DefaultInstance.Name
	}
	safeWorld, err := SanitizeSaveName(worldName)
	if err != nil {
		return
	}
	inst, err := s.GetOrCreateInstance(safeWorld)
	if err != nil {
		return
	}

	client := newWebSocketClient(conn, safeWorld)
	defer client.stop()
	if authenticated {
		client.accountID = account.ID
		client.name = admitPlayerName(account.DisplayName())
	}
	session := s.editorSessionForWorld(safeWorld, inst.SourceWorld)
	presence, memberToken, displaced, err := session.EnterResuming(client, client.name, enter.ResumeToken)
	if err != nil {
		return
	}
	// A reconnect that arrived before the server noticed the drop takes the
	// membership over; the socket it took it from is closed here rather than
	// left to be discovered, so the session never fans out to a connection
	// nobody is reading (M16.14f).
	//
	// CloseNow, not Close: this is the RESUMING collaborator's own entry path,
	// and a graceful close waits up to five seconds for a close frame from the
	// peer least likely to send one (M18.17). Nothing is lost by dropping it —
	// the browser's close listener takes no event, so neither the code nor the
	// reason was ever read (web/src/main.ts).
	if displaced != nil && displaced != client {
		_ = displaced.conn.CloseNow()
	}
	session.SetMemberReadOnly(client, !s.editorCanEdit(safeWorld, client.accountID))
	client.name = string(presence.Name)
	s.mu.Lock()
	if s.EditorSessions == nil {
		s.EditorSessions = make(map[*webSocketClient]*EditorSession)
	}
	s.EditorSessions[client] = session
	s.mu.Unlock()
	defer func() {
		session.Exit(client)
		s.mu.Lock()
		delete(s.EditorSessions, client)
		s.mu.Unlock()
		s.broadcastEditorPresence(context.Background(), session)
	}()

	// The cursor belongs to the browser. These are only the initial inspection
	// coordinates sent with its full frame, never session state — the middle of
	// the board for a first entry, and for a resumed membership the cell that
	// membership was last inspecting, so a reconnect does not drag the cursor
	// back to the centre.
	snapshot, err := session.Snapshot(client, presence.X, presence.Y)
	if err != nil {
		return
	}
	// The F1/F2/F3 element menus are static, so they ride the entry snapshot
	// once rather than a request per keypress (M5.8).
	snapshot.Menus = editorElementMenus()
	// The token this connection must present if it has to come back (M16.14f).
	snapshot.ResumeToken = memberToken
	if client.write(ctx, snapshot) != nil {
		return
	}
	s.broadcastEditorPresence(ctx, session)

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}
		switch envelope.Type {
		case MessageTypeEditorExit:
			return
		case MessageTypeEditorInspect:
			var inspect EditorInspectMessage
			if json.Unmarshal(raw, &inspect) != nil {
				continue
			}
			session.UpdatePresence(client, inspect.X, inspect.Y)
			reply, err := session.Inspect(client, inspect.X, inspect.Y)
			if err != nil || client.write(ctx, reply) != nil {
				return
			}
			s.broadcastEditorPresence(ctx, session)
		case MessageTypeEditorEdit:
			var edit EditorEditMessage
			if json.Unmarshal(raw, &edit) != nil {
				continue
			}
			// The diff goes out through the session's fan-out gate, in the order
			// the session applied the edits (M16.14b). Broadcasting after the
			// session lock was released left the two orders independent, so two
			// members writing one cell could leave a third screen holding the
			// tile the session threw away. The gate holds no session lock while
			// it writes, and since M16.14e each write only queues the message
			// for that member's own writer, so a stalled browser delays nobody.
			reply, err := session.EditAndFanOut(client, edit, func(diff EditorDiffMessage) {
				if diff.Type == "" {
					return
				}
				session.UpdatePresence(client, edit.X, edit.Y)
				// Only members viewing the edited board get its cells (M17.12).
				s.broadcastEditorBoard(ctx, session, diff.BoardID, diff)
				s.broadcastEditorPresence(ctx, session)
			})
			if err != nil {
				return
			}
			if reply.Type == "" {
				continue
			}
		case MessageTypeEditorLease:
			var lease EditorLeaseMessage
			if json.Unmarshal(raw, &lease) != nil {
				continue
			}
			switch lease.Op {
			case "request":
				reply, err := session.AcquireLease(client, lease)
				if err != nil {
					return
				}
				if reply.Type != "" && client.write(ctx, reply) != nil {
					return
				}
			case "release":
				session.ReleaseLease(client, lease)
			}
		case MessageTypeEditorProperty:
			var property EditorPropertyMessage
			if json.Unmarshal(raw, &property) != nil {
				continue
			}
			reply, err := session.SetProperty(client, property)
			if err != nil {
				return
			}
			if reply.Type == "" {
				continue
			}
			// Board Information and the world name are not the acting member's
			// private business (M16.14a (a)): the members watching that board get
			// the repaint, and everyone else the world-scoped half, so a rename
			// reaches every switcher rather than one screen.
			s.broadcastEditorProperties(ctx, session, reply)
		case MessageTypeEditorStat:
			var stat EditorStatMessage
			if json.Unmarshal(raw, &stat) != nil {
				continue
			}
			reply, err := session.SetStat(client, stat)
			if err != nil {
				return
			}
			if reply.Type != "" && client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorProgram:
			var req EditorProgramRequestMessage
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			reply, err := session.ProgramText(client, req.StatID)
			if err != nil {
				return
			}
			if reply.Type != "" && client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorProgramSave:
			var save EditorProgramSaveMessage
			if json.Unmarshal(raw, &save) != nil {
				continue
			}
			reply, err := session.SaveProgram(client, save.StatID, save.Lines)
			if err != nil {
				return
			}
			if reply.Type != "" && client.write(ctx, reply) != nil {
				return
			}
		case MessageTypeEditorBoard:
			var board EditorBoardMessage
			if json.Unmarshal(raw, &board) != nil {
				continue
			}
			if s.serveEditorBoard(ctx, client, session, board) != nil {
				return
			}
		case MessageTypeEditorWorld:
			var world EditorWorldMessage
			if json.Unmarshal(raw, &world) != nil {
				continue
			}
			if s.serveEditorWorld(ctx, client, session, world) != nil {
				return
			}
		case MessageTypeEditorTestPlay:
			world, err := s.startEditorTestPlay(client, session)
			reply := EditorTestPlayMessage{Type: MessageTypeEditorTestPlay}
			if err != nil {
				reply.Error = err.Error()
			} else {
				reply.World = world
			}
			s.broadcastEditor(ctx, session, reply)
		}
	}
}

// EditorCounts reports how many people are editing each world (M17.11), the
// editor-side counterpart to the per-instance client counts handleWorlds
// gathers for Players. Takes the server lock to walk the session map, then each
// session's own lock via MemberCount — never reads EditorSession.Members
// directly, which is guarded by that session's mutex.
func (s *WebSocketServer) EditorCounts() map[string]int {
	s.mu.Lock()
	sessions := make(map[string]*EditorSession, len(s.EditorWorldSessions))
	for name, session := range s.EditorWorldSessions {
		sessions[name] = session
	}
	s.mu.Unlock()

	counts := make(map[string]int, len(sessions))
	for name, session := range sessions {
		if session == nil {
			continue
		}
		if n := session.MemberCount(); n > 0 {
			counts[name] = n
		}
	}
	return counts
}

func (s *WebSocketServer) editorSessionForWorld(worldName string, world TWorld) *EditorSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.EditorWorldSessions == nil {
		s.EditorWorldSessions = make(map[string]*EditorSession)
	}
	session := s.EditorWorldSessions[worldName]
	if session == nil {
		session = NewEditorSession(worldName, world)
		s.EditorWorldSessions[worldName] = session
	}
	return session
}

func (s *WebSocketServer) broadcastEditor(ctx context.Context, session *EditorSession, message interface{}) {
	for _, member := range session.MemberClients() {
		_ = member.write(ctx, message)
	}
}

// broadcastEditorBoard sends a board-shaped message only to the members viewing
// that board (M17.12). An edit diff carries the dirty cells of one board; a
// member editing another board would paint them onto the screen they are
// actually looking at, which is the corruption half of the owner's report.
// Session-wide messages (presence, test play) keep using broadcastEditor.
func (s *WebSocketServer) broadcastEditorBoard(ctx context.Context, session *EditorSession, boardID int16, message interface{}) {
	for _, member := range session.MemberClientsOnBoard(boardID) {
		_ = member.write(ctx, message)
	}
}

// broadcastEditorSnapshot fans a board-scoped repaint out to the whole session
// (M16.14a (a)). Clear board, Add board, Import board and New world used to
// reply to the acting client alone, so a collaborator watching a board somebody
// else cleared kept every tile that was no longer there.
//
// A snapshot carries the ACTING member's id, cursor, inspect and read-only flag
// alongside the shared board frame, so it only goes to the members viewing that
// board — the client takes the board half of it and leaves the cursor half
// alone (applyEditorSnapshot's forMe, M17.9). Everybody else gets the
// world-scoped half on its own: the switcher's board list and the world name,
// which change when a board is added or renamed no matter who is looking where.
func (s *WebSocketServer) broadcastEditorSnapshot(ctx context.Context, session *EditorSession, snapshot EditorSnapshotMessage) {
	s.fanOutEditorBoardChange(ctx, session, snapshot.BoardID, snapshot,
		EditorPropertiesMessage{Type: MessageTypeEditorProperties, Properties: snapshot.Properties})
}

// broadcastEditorProperties is the same fan-out for an accepted Board
// Information or world-name change (M16.14a (a)). The frame rides only the copy
// sent to the members viewing that board; the rest get the properties alone, of
// which the client takes only what is world-scoped.
func (s *WebSocketServer) broadcastEditorProperties(ctx context.Context, session *EditorSession, reply EditorPropertiesMessage) {
	elsewhere := reply
	elsewhere.Screen = nil
	s.fanOutEditorBoardChange(ctx, session, reply.Properties.BoardID, reply, elsewhere)
}

// fanOutEditorBoardChange sends onBoard to the members viewing boardID and
// elsewhere to every other member of the session.
func (s *WebSocketServer) fanOutEditorBoardChange(ctx context.Context, session *EditorSession, boardID int16, onBoard, elsewhere interface{}) {
	viewing := make(map[*webSocketClient]bool)
	for _, member := range session.MemberClientsOnBoard(boardID) {
		viewing[member] = true
		_ = member.write(ctx, onBoard)
	}
	for _, member := range session.MemberClients() {
		if !viewing[member] {
			_ = member.write(ctx, elsewhere)
		}
	}
}

func (s *WebSocketServer) broadcastEditorPresence(ctx context.Context, session *EditorSession) {
	s.broadcastEditor(ctx, session, EditorPresenceMessage{
		Type:    MessageTypeEditorPresence,
		Members: session.Presence(),
	})
}

func (s *WebSocketServer) editorCanEdit(worldName, accountID string) bool {
	dir := s.worldsDir()
	if dir == "" {
		return true
	}
	access, ok, err := loadWorldAccess(dir, worldName)
	if err != nil {
		log.Printf("zztgo: failed to load access metadata for %q: %v", worldName, err)
		return false
	}
	if !ok {
		return true
	}
	return access.CanEdit(accountID)
}

// serveEditorWorld routes an editorWorld operation (M5.6). save publishes the
// session world and hosts it; download replies with the world's .ZZT bytes;
// upload replaces the session world with client bytes after the M7.5 gate. A
// malformed base64 payload or unknown op is ignored, not fatal. It returns the
// write error only.
func (s *WebSocketServer) serveEditorWorld(ctx context.Context, client *webSocketClient, session *EditorSession, world EditorWorldMessage) error {
	switch world.Op {
	case "save":
		if !session.CanEdit(client) {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: "world is read-only for this account"})
		}
		name, err := s.saveEditorWorld(client, session, world.Name)
		reply := EditorSaveResultMessage{Type: MessageTypeEditorSaveResult}
		if err != nil {
			reply.Error = err.Error()
		} else {
			reply.World = name
		}
		return client.write(ctx, reply)
	case "download":
		data, err := session.WorldBytes(client, "")
		if err != nil || data == nil {
			return nil
		}
		name, nameErr := SanitizeSaveName(session.Name())
		if nameErr != nil {
			name = "WORLD"
		}
		return client.write(ctx, EditorWorldDataMessage{
			Type: MessageTypeEditorWorldData,
			Name: name,
			Data: base64.StdEncoding.EncodeToString(data),
		})
	case "upload":
		if !session.CanEdit(client) {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: "world is read-only for this account"})
		}
		data, decErr := base64.StdEncoding.DecodeString(world.Data)
		if decErr != nil {
			return nil
		}
		snapshot, gate, err := session.UploadWorld(client, data)
		if err != nil {
			return nil
		}
		if gate != "" {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: gate})
		}
		return client.write(ctx, snapshot)
	case "invite":
		if !session.CanEdit(client) {
			return client.write(ctx, EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, Error: "world is read-only for this account"})
		}
		reply := EditorSaveResultMessage{Type: MessageTypeEditorSaveResult, World: session.Name()}
		if err := s.inviteEditorCollaborator(ctx, client, session, world.AccountID); err != nil {
			reply.Error = err.Error()
		}
		return client.write(ctx, reply)
	}
	return nil
}

// serveEditorBoard routes an editorBoard operation (M5.5). add/switch/import
// reply with a full editor snapshot; export replies with the board's .BRD bytes.
// A malformed base64 payload or unknown op is ignored, not fatal, so a bad
// message never drops the editor connection. It returns the write error only.
func (s *WebSocketServer) serveEditorBoard(ctx context.Context, client *webSocketClient, session *EditorSession, board EditorBoardMessage) error {
	switch board.Op {
	case "add":
		reply, err := session.AddBoard(client, string(board.Name))
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		// The acting member is alone on the board they just made, so the frame
		// reaches only them; everyone else learns of the board through the
		// world-scoped half of the same fan-out, and of where its author went
		// through presence.
		s.broadcastEditorSnapshot(ctx, session, reply)
		s.broadcastEditorPresence(ctx, session)
		return nil
	case "switch":
		reply, err := session.SwitchBoard(client, board.BoardID)
		if err != nil {
			return nil
		}
		// A switch changes nothing but where this member is looking, so it stays
		// a private repaint — but every other screen filters cursors by board and
		// the legend says who is elsewhere (M17.10, M17.12), so they are told.
		if err := client.write(ctx, reply); err != nil {
			return err
		}
		s.broadcastEditorPresence(ctx, session)
		return nil
	case "export":
		reply, err := session.ExportBoard(client)
		if err != nil {
			return nil
		}
		return client.write(ctx, reply)
	case "import":
		data, decErr := base64.StdEncoding.DecodeString(board.Data)
		if decErr != nil {
			return nil
		}
		reply, err := session.ImportBoard(client, data)
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		s.broadcastEditorSnapshot(ctx, session, reply)
		return nil
	case "clear":
		reply, err := session.ClearBoard(client)
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		s.broadcastEditorSnapshot(ctx, session, reply)
		return nil
	case "new":
		reply, err := session.NewWorld(client)
		if err != nil {
			return nil
		}
		if reply.Type == "" {
			return nil
		}
		// NewWorld has already moved every member onto the only board the new
		// world has, so this repaints all of them.
		s.broadcastEditorSnapshot(ctx, session, reply)
		s.broadcastEditorPresence(ctx, session)
		return nil
	}
	return nil
}

// saveEditorWorld publishes an editor session's world as dir/<NAME>.ZZT and hosts
// it so the world picker lists it and a second client can join and play it (M5.6).
// The name comes from a client, so SanitizeSaveName is the whole defense: path
// separators, '.', '..' and absolute paths all fail its charset. A world of the
// same name that anyone is currently playing is never overwritten — the same
// occupancy refusal RestoreSnapshot uses.
func (s *WebSocketServer) saveEditorWorld(client *webSocketClient, session *EditorSession, name string) (string, error) {
	safe, err := SanitizeSaveName(name)
	if err != nil {
		return "", err
	}

	// Refuse before writing anything if the target world is occupied.
	s.mu.Lock()
	existing := s.Instances[safe]
	occupied := false
	if existing != nil {
		existing.mu.Lock()
		occupied = len(existing.Clients) != 0
		existing.mu.Unlock()
	}
	s.mu.Unlock()
	if occupied {
		return "", fmt.Errorf("world %q is being played and cannot be overwritten", safe)
	}

	dir := s.worldsDir()
	if dir == "" {
		return "", ErrSavesDisabled
	}
	// M18.11: the canonical Museum worlds are the main world and player-authored
	// content never overwrites one. They carry no .access.json — to the check
	// below they are unowned and open, which is the population M16.17b meant to
	// keep open — so the carve-out has to be asked first and separately.
	if WorldIsCanonical(safe) {
		return "", fmt.Errorf("world %q is a Museum of ZZT classic and cannot be replaced", safe)
	}
	access, hasAccess, err := loadWorldAccess(dir, safe)
	if err != nil {
		return "", err
	}
	if hasAccess && !access.CanEdit(client.accountID) {
		return "", fmt.Errorf("world %q is read-only for this account", safe)
	}
	if !hasAccess && client.accountID != "" {
		access = WorldAccess{
			OwnerAccountID: client.accountID,
			OwnerName:      client.name,
		}
		hasAccess = true
	}

	// M14.4: read the authored title before WorldBytes writes the save stem over
	// it. The dialog lets an author call a world "The Salt Cellar"; publishing it
	// as SALTCELL used to throw that away, because the stem was the only name a
	// world had.
	title := session.WorldTitle()

	data, err := session.WorldBytes(client, safe)
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", fmt.Errorf("could not serialize the editor world")
	}

	path := filepath.Join(dir, safe+".ZZT")
	// Belt and braces: SanitizeSaveName cannot emit a separator, so this can only
	// fire if that charset is ever loosened.
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", ErrInvalidSaveName
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// M36.21: temp + rename, so a failed or interrupted write leaves the
	// previously published world loadable instead of truncated.
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return "", err
	}
	if hasAccess {
		if err := writeWorldAccess(dir, safe, access); err != nil {
			return "", err
		}
	}
	// The title only earns a sidecar when it says something the stem does not —
	// and a republish that takes the title back off removes the stale one, so
	// the picker never shows a name the author has stopped using.
	if title != "" && title != safe {
		if err := writeWorldMeta(dir, safe, WorldMeta{Title: title, Author: client.name}); err != nil {
			return "", err
		}
	} else if err := os.Remove(worldMetaPath(dir, safe)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	world, err := LoadWorldBytes(data)
	if err != nil {
		return "", err
	}
	if err := s.HostGeneratedWorld(safe, world); err != nil {
		return "", err
	}
	session.SetWorldName(safe)
	return safe, nil
}

func (s *WebSocketServer) inviteEditorCollaborator(ctx context.Context, client *webSocketClient, session *EditorSession, accountID string) error {
	if client.accountID == "" {
		return fmt.Errorf("authentication required")
	}
	if strings.TrimSpace(accountID) == "" {
		return fmt.Errorf("collaborator account required")
	}
	dir := s.worldsDir()
	if dir == "" {
		return ErrSavesDisabled
	}
	worldName, err := SanitizeSaveName(session.Name())
	if err != nil {
		return err
	}
	access, ok, err := loadWorldAccess(dir, worldName)
	if err != nil {
		return err
	}
	if !ok || !access.IsOwner(client.accountID) {
		return fmt.Errorf("only the world owner can invite collaborators")
	}
	access.AddCollaborator(strings.TrimSpace(accountID))
	if err := writeWorldAccess(dir, worldName, access); err != nil {
		return err
	}
	// M16.14a (b): tell the invitee, if they are sitting in the session. Their
	// browser's editorReadOnly comes from a snapshot and from nowhere else, and
	// every editor key consults it before it sends anything, so an invite the
	// client never hears about leaves the new collaborator refused by their own
	// browser — with a "Read-only" window — until they leave and come back. The
	// snapshot is addressed to them and carries the cursor they last reported,
	// so being told does not move it.
	for _, member := range session.SetAccountReadOnly(strings.TrimSpace(accountID), false) {
		x, y := session.MemberCursor(member)
		snapshot, err := session.Snapshot(member, x, y)
		if err != nil {
			continue
		}
		_ = member.write(ctx, snapshot)
	}
	return nil
}

func (s *WebSocketServer) startEditorTestPlay(client *webSocketClient, session *EditorSession) (string, error) {
	data, err := session.WorldBytes(client, "")
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", fmt.Errorf("could not serialize the editor world")
	}
	world, err := LoadWorldBytes(data)
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		name, err := randomTestPlayWorldName()
		if err != nil {
			return "", err
		}
		// Private: a test-play copy is nobody's business but the session's (M34.1).
		if err := s.hostGeneratedWorld(name, world, true); err != nil {
			if strings.Contains(err.Error(), "occupied") {
				continue
			}
			return "", err
		}
		return name, nil
	}
	return "", fmt.Errorf("could not allocate test play world")
}

func randomTestPlayWorldName() (string, error) {
	var nonce [3]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return "TP" + strings.ToUpper(hex.EncodeToString(nonce[:])), nil
}
