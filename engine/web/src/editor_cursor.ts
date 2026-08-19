// editor_cursor.ts — the editor's blinking cursor and the presence overlay that
// shows where everybody else's cursor is.
//
// Split out from main.ts because it is pure: given a cursor, a blink phase and
// the other editors' positions, it returns the cells to draw. That makes the
// overlay testable under node without a canvas — see ../test/editor_cursor.test.mjs.
//
// editorReplyMatchesCursor is the interesting one. A cursor move is optimistic,
// so a reply from the server may arrive after the player has moved on; it is
// only applied if it still matches where the cursor actually is.

export type EditorCursor = {
  x: number;
  y: number;
};

export function sameEditorCursor(a: EditorCursor, b: EditorCursor): boolean {
  return a.x === b.x && a.y === b.y;
}

export function editorReplyMatchesCursor(current: EditorCursor, reply: EditorCursor): boolean {
  return sameEditorCursor(current, reply);
}

// EditorLoop's idle cursor blink (editor.go:534-551 / EDITOR.PAS): cursorBlinker
// runs a 3-phase 0,1,2 cycle. Phase 0 redraws the underlying board tile
// (BoardDrawTile), so whatever sits under the cursor — including a stat-backed
// object — stays visible; phases 1 and 2 draw the cross cursor glyph 0xC5 in
// 0x0F over it. The phase advances every SoundHasTimeElapsed(_, 15) = 15
// hundredths of a second, so the browser drives it from a 150ms timer.
export const EDITOR_BLINK_PHASES = 3;
export const EDITOR_CURSOR_CHAR = 0xc5;
export const EDITOR_CURSOR_COLOR = 0x0f;

// True on the "cursor shown" phases (1, 2); false on the "tile shown" phase (0).
// Tolerates any integer blink counter, negative included.
export function editorCursorShown(blink: number): boolean {
  return (((blink % EDITOR_BLINK_PHASES) + EDITOR_BLINK_PHASES) % EDITOR_BLINK_PHASES) !== 0;
}

export type EditorOverlayCell = { x: number; y: number; color: number; text: string };

export type EditorPresenceCursor = {
  id: string;
  name: string;
  color: number;
  // M17.12: the board this member is editing. Members of one session can be on
  // different boards, and a cursor only means anything to viewers on the same
  // one — otherwise you get ghost cursors from boards you cannot see.
  boardId?: number;
  x: number;
  y: number;
};

export type EditorPresenceLegendEntry = {
  name: string;
  // The attribute the viewer actually sees for this member's cursor: their
  // presence colour, or the local white cross for the viewer's own entry.
  color: number;
  self: boolean;
};

// A collaborator legend split by whether the member's cursor is visible to the
// viewer at all. M17.12 draws cursors only for members on the viewer's board, so
// a flat list would name colours that are nowhere on screen.
export type EditorPresenceLegend = {
  here: EditorPresenceLegendEntry[];
  elsewhere: EditorPresenceLegendEntry[];
};

// editorPresenceLegend maps cursor colour back to a name (M17.10). M17.9 removed
// the on-board name label, so colour became a collaborator's only identity, which
// is unambiguous for two people and a guessing game for three or more. This
// builds the legend the sidebar paints; nothing here returns text to the board.
//
// The viewer's own entry is white (EDITOR_CURSOR_COLOR), not the colour the
// server assigned them: the local cursor is always drawn white, so listing the
// server colour would name a cursor that is not on this screen.
//
// Entries are sorted by name because the server builds presence by ranging a map
// (editor_session.go Presence), so its order shuffles between broadcasts and an
// unsorted list would reorder itself under the reader on every cursor move.
export function editorPresenceLegend(opts: {
  presence: EditorPresenceCursor[];
  selfId: string;
  // The board the viewer is on. Left undefined, no member is classed as
  // elsewhere — the pre-M17.12 behaviour, where every cursor was drawn.
  boardId?: number;
}): EditorPresenceLegend {
  const here: EditorPresenceLegendEntry[] = [];
  const elsewhere: EditorPresenceLegendEntry[] = [];
  let self: EditorPresenceLegendEntry | null = null;
  for (const member of opts.presence) {
    if (member.id === opts.selfId) {
      self = { name: member.name, color: EDITOR_CURSOR_COLOR, self: true };
      continue;
    }
    const entry = { name: member.name, color: member.color, self: false };
    const visible =
      opts.boardId === undefined || member.boardId === undefined || member.boardId === opts.boardId;
    (visible ? here : elsewhere).push(entry);
  }
  const byName = (a: EditorPresenceLegendEntry, b: EditorPresenceLegendEntry) =>
    a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  here.sort(byName);
  elsewhere.sort(byName);
  // The viewer heads their own board's list: the first colour anyone needs to
  // place is the cursor they are driving.
  if (self) here.unshift(self);
  return { here, elsewhere };
}

// editorCursorOverlay builds the blink layer for paintOverlay's editor branch.
// Board coordinates are 1-based (cursorX/Y); the screen overlay is 0-based, hence
// the x-1/y-1 shift. On the tile-shown phase it returns nothing so the board cell
// underneath (object glyph and all) renders untouched; on the cursor-shown phases
// it emits the local cross cursor plus each remote collaborator's marker+name.
// Remote cursors blink on the same phase so they never permanently hide the tile
// beneath them either.
export function editorCursorOverlay(opts: {
  blink: number;
  cursor: EditorCursor;
  presence: EditorPresenceCursor[];
  selfId: string;
  boardCols: number;
  rows: number;
  // The board the viewer is looking at (M17.12). Collaborators on other boards
  // are omitted. Left undefined, no board filtering happens, which is the
  // pre-M17.12 behaviour.
  boardId?: number;
}): EditorOverlayCell[] {
  if (!editorCursorShown(opts.blink)) {
    return [];
  }
  const cells: EditorOverlayCell[] = [];
  cells.push({
    x: opts.cursor.x - 1,
    y: opts.cursor.y - 1,
    color: EDITOR_CURSOR_COLOR,
    text: String.fromCharCode(EDITOR_CURSOR_CHAR),
  });
  for (const member of opts.presence) {
    if (member.id === opts.selfId) continue;
    // Only draw collaborators editing the same board as the viewer.
    if (opts.boardId !== undefined && member.boardId !== undefined && member.boardId !== opts.boardId) {
      continue;
    }
    const x = member.x - 1;
    const y = member.y - 1;
    if (x < 0 || x >= opts.boardCols || y < 0 || y >= opts.rows) continue;
    // M17.9: a collaborator is the ordinary editor cursor in their own colour —
    // same glyph as the local cross cursor, never a distinct marker, and no name
    // label (it spilled up to ten cells across the board, hiding tiles). Identity
    // is colour-only here; the collaborator list carries the colour-to-name map.
    cells.push({
      x,
      y,
      color: member.color,
      text: String.fromCharCode(EDITOR_CURSOR_CHAR),
    });
  }
  return cells;
}
