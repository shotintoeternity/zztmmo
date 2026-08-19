// editor_input.ts — optimistic local echo for editor typing.
//
// Typing a character or pressing delete draws the result immediately rather
// than waiting for the server to confirm it, so the editor feels like a text
// field instead of a telegraph. The server's own reply follows and wins; these
// functions only decide what to paint in the gap.
//
// Pure by design, so ../test/editor_input.test.mjs can check the painted cell
// without a DOM. The EditorCursor type is declared here as well as in
// editor_cursor.ts so neither module has to import the other.

export type EditorCursor = {
  x: number;
  y: number;
};

export type EditorScreenCell = {
  x: number;
  y: number;
  ch: number;
  color: number;
};

export function editorTextRenderColor(cursorColor: number): number {
  let fg = cursorColor & 0x0f;
  if (fg < 9) {
    fg = 9;
  } else if (fg > 15) {
    fg = 15;
  }
  if (fg === 15) {
    return 0x0f;
  }
  return ((fg - 9 + 1) << 4) + 0x0f;
}

export function optimisticEditorTextCell(cursor: EditorCursor, char: number, cursorColor: number): EditorScreenCell | null {
  if (char < 0x20 || char >= 0x80) {
    return null;
  }
  return {
    x: cursor.x - 1,
    y: cursor.y - 1,
    ch: char,
    color: editorTextRenderColor(cursorColor),
  };
}

export function optimisticEditorEraseCell(cursor: EditorCursor): EditorScreenCell | null {
  if (cursor.x <= 1) {
    return null;
  }
  return {
    x: cursor.x - 2,
    y: cursor.y - 1,
    ch: 0x20,
    color: 0x0f,
  };
}
