// editor_controller.ts — the board editor's client-side state and the parts of
// its behaviour that need nothing from main.ts.

import type { EditorInspect } from "./editor";

// EditorElementItem / EditorElementMenu are the F1/F2/F3 category tables the
// server derives from ElementDefs (M5.8). They ride the entry snapshot once.
export type EditorElementItem = {
  elementId: number;
  name: string;
  shortcut: string;
  character: number;
  color: number;
  categoryName?: string;
};

export type EditorElementMenu = {
  category: number;
  key: string;
  title: string;
  items: EditorElementItem[];
};

export type EditorSidebarMenuItem = {
  label: string;
  value?: string;
  shortcut?: string;
  onPick: () => void;
};

export type EditorSidebarMenu = {
  title: string;
  items: EditorSidebarMenuItem[];
  selected: number;
  hint?: string;
  releaseLeaseOnClose?: boolean;
};

export type EditorStatPromptItem =
  | { kind: "slider"; field: "p1" | "p2"; label: string; value: number; startChar?: string; endChar?: string }
  | { kind: "character"; field: "p1"; label: string; value: number }
  | { kind: "choice"; field: "bulletType" | "direction"; label: string; choices: string[]; selected: number; values: number[] }
  | { kind: "board"; label: string; value: number }
  | { kind: "program"; statId: number };

export type EditorStatPrompt = {
  categoryName: string;
  elementName: string;
  items: EditorStatPromptItem[];
  active: number;
};

export type EditorPresence = {
  id: string;
  name: string;
  color: number;
  // M17.12: which board this member is editing; cursors are only drawn for
  // members on the board the viewer is looking at.
  boardId?: number;
  x: number;
  y: number;
};

export type EditorLeaseKind = "board" | "stat";

export type EditorLeaseMessage = {
  type: "editorLease";
  op: "request" | "release" | "granted" | "refused";
  kind: EditorLeaseKind;
  boardId?: number;
  statId?: number;
  holderId?: string;
  holderName?: string;
  error?: string;
};

export type EditorBoardOption = {
  id: number;
  name: string;
};

export type EditorProperties = {
  boardId: number;
  boardName: string;
  worldName: string;
  maxShots: number;
  isDark: boolean;
  neighborBoards: number[];
  reenterWhenZapped: boolean;
  timeLimitSec: number;
  boards: EditorBoardOption[];
};

// The editor's whole client-side state, one object rather than two dozen
// module variables. The session on the server owns the world; this is what the
// browser keeps around it.
export const editor: {
  blink: number;
  blinkTimer: number;
  cursor: { x: number; y: number };
  inspect: EditorInspect;
  properties: EditorProperties;
  brush: { element: number; character: number; color: number; copied: boolean };
  drawing: boolean;
  pointerDrawing: boolean;
  textMode: boolean;
  modified: boolean;
  exitAfterSave: boolean;
  memberId: string;
  presence: EditorPresence[];
  readOnly: boolean;
  pendingLease: { lease: EditorLeaseMessage; onGranted: () => void } | null;
  activeLease: EditorLeaseMessage | null;
  retainLeaseOnClose: boolean;
  restore: { boardId: number; x: number; y: number } | null;
  menus: EditorElementMenu[];
  categoryMenu: EditorElementMenu | null;
  presencePanel: boolean;
  sidebarMenu: EditorSidebarMenu | null;
  statPrompt: EditorStatPrompt | null;
  statEditAfterPlace: boolean;
} = {
  // EditorLoop's idle cursor blink (editor.go:534-551). blink is the 3-phase
  // cursorBlinker; the timer advances it every EDITOR_BLINK_MS so the cross cursor
  // fades to reveal the tile/object underneath, matching the DOS editor cadence.
  blink: 0,
  blinkTimer: 0,
  cursor: { x: 30, y: 12 },
  inspect: {
    x: 30,
    y: 12,
    elementId: 0,
    element: "",
    character: 32,
    color: 0x0f,
    hasStat: false,
  },
  properties: {
    boardId: 0,
    boardName: "",
    worldName: "",
    maxShots: 0,
    isDark: false,
    neighborBoards: [0, 0, 0, 0],
    reenterWhenZapped: false,
    timeLimitSec: 0,
    boards: [{ id: 0, name: "None" }],
  },
  brush: { element: 21, character: 0xdb, color: 0x0e, copied: false },
  drawing: false,
  pointerDrawing: false,
  // F4 text-entry mode (M5.8): while on, printable keys paint text tiles and the
  // cursor advances, exactly as EditorLoop's TextEntry draw mode does.
  textMode: false,
  // Tracks whether the session world has unedited changes since the last save, so
  // leaving the editor can offer EditorAskSaveChanged's "Save first?" prompt.
  modified: false,
  // Set when a save was requested as part of leaving; the saveResult handler then
  // completes the exit (or, on error, keeps the editor open).
  exitAfterSave: false,
  memberId: "",
  presence: [],
  readOnly: false,
  pendingLease: null,
  activeLease: null,
  retainLeaseOnClose: false,
  // What the browser puts back around a reconnect's fresh snapshot (M16.14f), or
  // null when no reconnect is in flight. The session owns the world; these are
  // the things it does not own — the board this browser was looking at and where
  // its cursor was — captured when the socket closed. The brush, text mode and
  // draw mode need no capture: they are client-only and nothing resets them.
  restore: null,
  // The F1/F2/F3 element category tables, delivered once on the entry snapshot.
  menus: [],
  // The category currently open on the sidebar (F1/F2/F3), or null. While set, the
  // next keystroke selects an element by its shortcut instead of driving the board
  // (EDITOR.PAS:808-842).
  categoryMenu: null,
  // M17.10: the W collaborator legend. It overlays the same sidebar rows as the
  // category picker but is not modal — the cursor keeps moving and editing while
  // it is up, so you can watch a coloured cursor and read its name at once.
  presencePanel: false,
  sidebarMenu: null,
  statPrompt: null,
  // Set when a menu selection placed a stat-backed element, so the diff reply
  // carrying the new stat can open its editor — EditorLoop calls EditorEditStat
  // right after AddStat (EDITOR.PAS:766-772).
  statEditAfterPlace: false,
};

// The editor keys that change the board directly. A read-only member is told
// so instead (M16.14). Z (clear) and N (new world) are not here: they ask the
// server for a board lease first, and the server refuses a read-only member.
const MUTATING_KEYS: ReadonlySet<string> = new Set(["Space", "Delete", "Backspace", "KeyX"]);

export function editorKeyMutates(code: string): boolean {
  return MUTATING_KEYS.has(code);
}

export function editorLeaseMatches(a: EditorLeaseMessage | null, b: EditorLeaseMessage): boolean {
  return !!a && a.kind === b.kind && (a.boardId ?? 0) === (b.boardId ?? 0) && (a.statId ?? -1) === (b.statId ?? -1);
}

export function editorBool(value: boolean): string {
  return value ? "Yes" : "No";
}

// editorBoardName is EditorGetBoardName with titleScreenIsNone TRUE (editor.go:
// 854): the board-edge rows and a passage's "Room:" readout call the title board
// "None". The wire list carries board 0 under its real name so the board
// SWITCHER — vanilla's one titleScreenIsNone-false caller — can still offer it.
export function editorBoardName(id: number): string {
  if (id === 0) return "None";
  return editor.properties.boards.find((board) => board.id === id)?.name ?? "None";
}

// editorBoardEntries is EditorSelectBoard's list body (editor.go:875-877): every
// board 0..BoardCount, named by EditorGetBoardName under the caller's own
// titleScreenIsNone. The "id: " prefix is this client's, so a picked entry can
// be resolved back to a board number.
export function editorBoardEntries(titleScreenIsNone: boolean): string[] {
  return editor.properties.boards.map(
    (board) => `${board.id}: ${titleScreenIsNone && board.id === 0 ? "None" : board.name}`,
  );
}

export function editorStatCategoryName(inspect: EditorInspect): string {
  for (const menu of editor.menus) {
    let categoryName = "";
    for (const item of menu.items) {
      if (item.categoryName) categoryName = item.categoryName;
      if (item.elementId === inspect.elementId) return categoryName;
    }
  }
  return "";
}
