// editor_controller.ts — the board editor's client-side state and the parts of
// its behaviour that need nothing from main.ts.

// The editor keys that change the board directly. A read-only member is told
// so instead (M16.14). Z (clear) and N (new world) are not here: they ask the
// server for a board lease first, and the server refuses a read-only member.
const MUTATING_KEYS: ReadonlySet<string> = new Set(["Space", "Delete", "Backspace", "KeyX"]);

export function editorKeyMutates(code: string): boolean {
  return MUTATING_KEYS.has(code);
}
