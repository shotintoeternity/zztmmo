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
