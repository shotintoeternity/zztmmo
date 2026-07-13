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
