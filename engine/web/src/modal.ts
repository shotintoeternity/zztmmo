// M4.1 — the modal text-window layer.
//
// One renderer (renderModal) and one input router (handleModalKey) for every
// modal ZZT puts on screen: read-only text, selectable "!label;text" links,
// paged help, yes/no prompts, and text entry. While a modal is open the router
// consumes every key, so gameplay input cannot leak through to the board.
//
// Geometry is transcribed from the engine, which was converted from the Pascal.
// Do not "tidy" these coordinates — they are what ZZT draws:
//   * yes/no      game.go SidebarPromptYesNo:  message at 63,5 in 0x1F, 0x9E cursor
//   * text entry  game.go SidebarPromptString: label right-aligned to col 75 on
//                 row 3; the field itself is PromptString at 63,5
//   * debug '?'   game.go GameDebugPrompt:     the same field, width 11, no label

import {
  renderTextWindow,
  clampLinePos,
  TEXT_WINDOW_PAGE,
  type TextWindowState,
} from "./textwindow";

export type WriteText = (x: number, y: number, color: number, text: string) => void;

// TextWindowSelect's title prompts, shown once the cursor sits on a "!" line.
const SELECT_PROMPT = "\xaePress ENTER to select this\xaf";
const MORE_INFO_PROMPT = "\xaePress ENTER for more info\xaf";

const PROMPT_X = 63;
const PROMPT_Y = 5;
const PROMPT_ARROW_COLOR = 0x1e;
const PROMPT_TEXT_COLOR = 0x0f;
const LABEL_RIGHT_EDGE = 75;
const LABEL_Y = 3;
const YESNO_COLOR = 0x1f;
const YESNO_CURSOR_COLOR = 0x9e;

/** Read-only text, selectable links, and paged help all share this shape. */
export type TextModal = {
  kind: "text";
  state: TextWindowState;
  baseTitle: string;
  /** TextWindowSelect only swaps the title after the cursor has moved. */
  moved: boolean;
  /** True for scrolls: Enter on a "!label" line sends a reply. */
  selectable: boolean;
  onSelect: (label: string) => void;
};

export type YesNoModal = {
  kind: "yesno";
  message: string;
  onAnswer: (yes: boolean) => void;
};

export type EntryModal = {
  kind: "entry";
  /** Right-aligned label on row 3, or "" for the bare debug field. */
  label: string;
  /** Suffix drawn after the field, e.g. ".SAV". */
  suffix: string;
  width: number;
  buffer: string;
  /** PROMPT_ALPHANUM restricts to uppercase A-Z0-9, as vanilla does. */
  charset: "any" | "alphanum";
  /** null means cancelled (Escape). */
  onSubmit: (text: string | null) => void;
};

export type Modal = TextModal | YesNoModal | EntryModal;

/** What the caller should do after routing a key. */
export type KeyResult = "close" | "redraw" | "ignore";

/**
 * hyperlinkOf extracts the ZZT-OOP label from a "!label;text" line, or "" if the
 * line is not a hyperlink. "!-FILE;text" jumps to another help file — not wired
 * up (it needs a client→server "open help file" request, see TASKS.md M4.1
 * notes and NOTES.md M3.10), so it is reported as "" and Enter just closes.
 */
export function hyperlinkOf(line: string): string {
  if (!line.startsWith("!")) {
    return "";
  }
  let pointer = line.slice(1);
  const semi = pointer.indexOf(";");
  if (semi >= 0) {
    pointer = pointer.slice(0, semi);
  }
  if (pointer.startsWith("-")) {
    return "";
  }
  return pointer;
}

function resolveTitle(m: TextModal) {
  const line = m.state.lines[m.state.linePos - 1] ?? "";
  if (m.moved && line.startsWith("!")) {
    m.state.title = m.selectable ? SELECT_PROMPT : MORE_INFO_PROMPT;
  } else {
    m.state.title = m.baseTitle;
  }
}

/** renderModal repaints the modal layer from scratch; it never restores cells. */
export function renderModal(write: WriteText, m: Modal) {
  switch (m.kind) {
    case "text":
      resolveTitle(m);
      renderTextWindow(write, m.state);
      return;
    case "yesno":
      write(PROMPT_X, PROMPT_Y, YESNO_COLOR, m.message);
      write(PROMPT_X + m.message.length, PROMPT_Y, YESNO_CURSOR_COLOR, "_");
      return;
    case "entry":
      renderEntry(write, m);
      return;
  }
}

// renderEntry is PromptString's redraw. The arrow row is PROMPT_Y - 1.
function renderEntry(write: WriteText, m: EntryModal) {
  if (m.label) {
    write(LABEL_RIGHT_EDGE - m.label.length, LABEL_Y, YESNO_COLOR, m.label);
  }
  for (let i = 0; i <= m.width - 1; i += 1) {
    write(PROMPT_X + i, PROMPT_Y, PROMPT_TEXT_COLOR, " ");
    write(PROMPT_X + i, PROMPT_Y - 1, PROMPT_ARROW_COLOR, " ");
  }
  write(PROMPT_X + m.width, PROMPT_Y - 1, PROMPT_ARROW_COLOR, " ");
  if (m.suffix) {
    write(PROMPT_X + m.width, PROMPT_Y, PROMPT_TEXT_COLOR, m.suffix);
  }
  const cursorColor = Math.trunc(PROMPT_ARROW_COLOR / 0x10) * 16 + 0x0f;
  write(PROMPT_X + m.buffer.length, PROMPT_Y - 1, cursorColor, "\x1f");
  write(PROMPT_X, PROMPT_Y, PROMPT_TEXT_COLOR, m.buffer);
}

/**
 * handleModalKey is the single input router. It returns "close" when the modal
 * is finished (callbacks have already fired), "redraw" when the modal changed,
 * and "ignore" when the key means nothing here.
 *
 * Every key reaching this function is consumed by the modal. The caller must not
 * fall through to gameplay input on "ignore".
 */
export function handleModalKey(m: Modal, event: KeyboardEvent): KeyResult {
  switch (m.kind) {
    case "text":
      return textKey(m, event);
    case "yesno":
      return yesNoKey(m, event);
    case "entry":
      return entryKey(m, event);
  }
}

function textKey(m: TextModal, event: KeyboardEvent): KeyResult {
  let next = m.state.linePos;
  switch (event.code) {
    case "ArrowUp":
      next -= 1;
      break;
    case "ArrowDown":
      next += 1;
      break;
    case "PageUp":
      next -= TEXT_WINDOW_PAGE;
      break;
    case "PageDown":
      next += TEXT_WINDOW_PAGE;
      break;
    case "Escape":
      return "close";
    case "Enter": {
      const label = hyperlinkOf(m.state.lines[m.state.linePos - 1] ?? "");
      if (label && m.selectable) {
        m.onSelect(label);
      }
      return "close";
    }
    default:
      return "ignore";
  }
  const clamped = clampLinePos(next, m.state.lines.length);
  if (clamped !== m.state.linePos) {
    m.state.linePos = clamped;
    m.moved = true;
  }
  return "redraw";
}

// yesNoKey mirrors SidebarPromptYesNo: only Y, N and Escape end the prompt.
function yesNoKey(m: YesNoModal, event: KeyboardEvent): KeyResult {
  const key = event.key.toUpperCase();
  if (key === "Y") {
    m.onAnswer(true);
    return "close";
  }
  if (key === "N" || event.code === "Escape") {
    m.onAnswer(false);
    return "close";
  }
  return "ignore";
}

// entryKey is PromptString's editing loop.
function entryKey(m: EntryModal, event: KeyboardEvent): KeyResult {
  if (event.code === "Enter") {
    m.onSubmit(m.buffer);
    return "close";
  }
  if (event.code === "Escape") {
    m.onSubmit(null);
    return "close";
  }
  if (event.code === "Backspace" || event.code === "ArrowLeft") {
    if (m.buffer.length === 0) {
      return "ignore";
    }
    m.buffer = m.buffer.slice(0, -1);
    return "redraw";
  }
  if (event.key.length !== 1 || event.key < " " || event.key.charCodeAt(0) >= 0x80) {
    return "ignore";
  }
  if (m.buffer.length >= m.width) {
    return "ignore";
  }
  const ch = m.charset === "alphanum" ? event.key.toUpperCase() : event.key;
  if (m.charset === "alphanum" && !/[A-Z0-9]/.test(ch)) {
    return "ignore";
  }
  m.buffer += ch;
  return "redraw";
}
