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
  strBottom,
  strSep,
  strText,
  strTop,
  TEXT_WINDOW_PAGE,
  TEXT_WINDOW_WIDTH,
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

// PopupPromptString (game.go:1122): a six-row box on rows 18..23 at column 3,
// the question centered on row 19, and the field on row 22.
const POPUP_X = 3;
const POPUP_Y = 18;
const POPUP_COLOR = 0x4f;
const POPUP_FIELD_X = 10;
const POPUP_FIELD_Y = 22;
const POPUP_FIELD_COLOR = 0x4e;
const POPUP_FIELD_WIDTH = TEXT_WINDOW_WIDTH - 16;

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
  /** Pickers whose lines include prose: Enter on a non-link line does nothing
   *  rather than dismissing the window. Escape still closes. Scrolls leave this
   *  unset, because there Enter on any line is how you close them. */
  requireSelection?: boolean;
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

/**
 * PopupEntryModal is PopupPromptString: the centered box ZZT uses for the one
 * question it asks outside the sidebar — "Congratulations!  Enter your name:"
 * after a qualifying score. Editing behaves exactly like EntryModal (the box is
 * drawn around an ordinary PromptString), so the two share entryKey.
 */
export type PopupEntryModal = {
  kind: "popupEntry";
  question: string;
  buffer: string;
  onSubmit: (text: string | null) => void;
  /** Top row of the box. Defaults to vanilla's POPUP_Y; the launch name prompt
   *  centers itself instead, having no board it must avoid covering. */
  y?: number;
};

/** A compact text-window editor used for free-form, multi-line prompts. F2
 * submits the accumulated text; Enter starts a new line. */
export type MultiLineEntryModal = {
  kind: "multilineEntry";
  title: string;
  lines: string[];
  line: number;
  onSubmit: (text: string | null) => void;
};

/** POPUP_ROWS tall, centered in the 25-row screen. */
export const POPUP_Y_CENTERED = Math.floor((25 - 6) / 2);

export type Modal = TextModal | YesNoModal | EntryModal | PopupEntryModal | MultiLineEntryModal;

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
    case "popupEntry":
      renderPopupEntry(write, m);
      return;
    case "multilineEntry":
      renderMultiLineEntry(write, m);
      return;
  }
}

function renderMultiLineEntry(write: WriteText, m: MultiLineEntryModal) {
  const lines = ["$Describe the world you want", "Enter: new line  F2: dream", "", ...m.lines];
  renderTextWindow(write, { title: m.title, lines, linePos: Math.max(1, m.line + 4), viewingFile: false });
}

// promptField is PromptString's redraw: a `width`-wide field at (x, y) with the
// cursor on the arrow row above it.
function promptField(
  write: WriteText,
  x: number,
  y: number,
  arrowColor: number,
  textColor: number,
  width: number,
  buffer: string,
) {
  for (let i = 0; i <= width - 1; i += 1) {
    write(x + i, y, textColor, " ");
    write(x + i, y - 1, arrowColor, " ");
  }
  write(x + width, y - 1, arrowColor, " ");
  const cursorColor = Math.trunc(arrowColor / 0x10) * 16 + 0x0f;
  write(x + buffer.length, y - 1, cursorColor, "\x1f");
  write(x, y, textColor, buffer);
}

// renderEntry is SidebarPromptString: label right-aligned on row 3, field at
// PROMPT_X/PROMPT_Y, optional suffix such as ".SAV" drawn past its end.
function renderEntry(write: WriteText, m: EntryModal) {
  if (m.label) {
    write(LABEL_RIGHT_EDGE - m.label.length, LABEL_Y, YESNO_COLOR, m.label);
  }
  promptField(write, PROMPT_X, PROMPT_Y, PROMPT_ARROW_COLOR, PROMPT_TEXT_COLOR, m.width, m.buffer);
  if (m.suffix) {
    write(PROMPT_X + m.width, PROMPT_Y, PROMPT_TEXT_COLOR, m.suffix);
  }
}

// renderPopupEntry is PopupPromptString: the box, then the same field inside it.
function renderPopupEntry(write: WriteText, m: PopupEntryModal) {
  const top = m.y ?? POPUP_Y;
  const rows = [strTop, strText, strSep, strText, strText, strBottom];
  for (let i = 0; i < rows.length; i += 1) {
    write(POPUP_X, top + i, POPUP_COLOR, rows[i]);
  }
  const centered = POPUP_X + 1 + Math.floor((TEXT_WINDOW_WIDTH - m.question.length) / 2);
  write(centered, top + 1, POPUP_COLOR, m.question);
  promptField(
    write,
    POPUP_FIELD_X,
    top + (POPUP_FIELD_Y - POPUP_Y),
    POPUP_COLOR,
    POPUP_FIELD_COLOR,
    POPUP_FIELD_WIDTH,
    m.buffer,
  );
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
      return entryKey(m, m.width, m.charset, event);
    case "popupEntry":
      // PROMPT_ANY: PopupPromptString takes any printable character.
      return entryKey(m, POPUP_FIELD_WIDTH, "any", event);
    case "multilineEntry":
      return multiLineEntryKey(m, event);
  }
}

function multiLineEntryKey(m: MultiLineEntryModal, event: KeyboardEvent): KeyResult {
  if (event.code === "F2") {
    m.onSubmit(m.lines.join("\n").trim());
    return "close";
  }
  if (event.code === "Escape") {
    m.onSubmit(null);
    return "close";
  }
  if (event.code === "Enter") {
    if (m.lines.length < 12) {
      m.lines.splice(m.line + 1, 0, "");
      m.line += 1;
    }
    return "redraw";
  }
  if (event.code === "ArrowUp") {
    m.line = Math.max(0, m.line - 1);
    return "redraw";
  }
  if (event.code === "ArrowDown") {
    m.line = Math.min(m.lines.length - 1, m.line + 1);
    return "redraw";
  }
  if (event.code === "Backspace") {
    if (m.lines[m.line].length > 0) {
      m.lines[m.line] = m.lines[m.line].slice(0, -1);
    } else if (m.line > 0) {
      m.lines.splice(m.line, 1);
      m.line -= 1;
    }
    return "redraw";
  }
  if (event.key.length !== 1 || event.key < " " || event.key.charCodeAt(0) >= 0x80) {
    return "ignore";
  }
  if (m.lines[m.line].length >= 42) {
    return "ignore";
  }
  m.lines[m.line] += event.key;
  return "redraw";
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
        return "close";
      }
      if (m.requireSelection) {
        return "ignore";
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

// entryKey is PromptString's editing loop, shared by the sidebar field and the
// popup box: the box is only chrome drawn around the same field.
function entryKey(
  m: EntryModal | PopupEntryModal,
  width: number,
  charset: "any" | "alphanum",
  event: KeyboardEvent,
): KeyResult {
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
  if (m.buffer.length >= width) {
    return "ignore";
  }
  const ch = charset === "alphanum" ? event.key.toUpperCase() : event.key;
  if (charset === "alphanum" && !/[A-Z0-9]/.test(ch)) {
    return "ignore";
  }
  m.buffer += ch;
  return "redraw";
}
