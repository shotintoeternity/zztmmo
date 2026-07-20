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
  x: number;
  y: number;
};

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
