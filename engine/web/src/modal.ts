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
  TEXT_WINDOW_HEIGHT,
  TEXT_WINDOW_PAGE,
  TEXT_WINDOW_WIDTH,
  TEXT_WINDOW_X,
  TEXT_WINDOW_Y,
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

/**
 * ProgramEditorModal is the M5.4 object/scroll code editor: a faithful port of
 * TextWindowEdit (engine/txtwind.go, from TXTWIND.PAS) on top of the M4.1 window
 * layer. Lines render raw (withoutFormatting), a block cursor tracks charPos, and
 * Escape saves — vanilla's EditorEditStatText always rebuilds Data on exit, so
 * there is no cancel. onSubmit receives the final lines to send as a save.
 */
// OopLabel / OopWarning mirror the server's M5.7 authoring aids: the object's
// :labels and advisory diagnostics, both tagged with a 0-based program line.
export type OopLabel = { name: string; line: number };
export type OopWarning = { line: number; message: string };

export type ProgramEditorModal = {
  kind: "programEditor";
  title: string;
  lines: string[];
  linePos: number; // 1-based, as in the Pascal
  charPos: number; // 1-based
  insertMode: boolean;
  // M5.7 authoring aids, computed server-side by the real ZZT-OOP tokenizer and
  // shown in the right margin. Advisory only; they never block a save.
  labels: OopLabel[];
  warnings: OopWarning[];
  onSubmit: (lines: string[]) => void;
};

export type WorldSearchEntry = {
  world: string;
  id: string;
  title: string;
  author: string;
  created: string;
  players?: number;
  source?: "local" | "museum";
  letter?: string;
  filename?: string;
  zztFile?: string;
};

export type WorldSearchModal = {
  kind: "worldSearch";
  title: string;
  query: string;
  selected: number;
  entries: WorldSearchEntry[];
  onSelect: (entry: WorldSearchEntry) => void;
  onQuery?: (query: string) => void;
};

// TextWindowEdit's derived limits with TextWindowWidth == 50 (game.go's
// TextWindowInit(5, 3, 50, 18)). A line may hold TextWindowWidth-8 characters;
// the caret guard is charPos < TextWindowWidth-7.
const PROGRAM_LINE_MAX = TEXT_WINDOW_WIDTH - 8;
const PROGRAM_CHAR_MAX = TEXT_WINDOW_WIDTH - 7;
const PROGRAM_PAGE = TEXT_WINDOW_HEIGHT - 4;
const PROGRAM_MAX_LINES = 1024;

/** POPUP_ROWS tall, centered in the 25-row screen. */
export const POPUP_Y_CENTERED = Math.floor((25 - 6) / 2);

export type Modal =
  | TextModal
  | YesNoModal
  | EntryModal
  | PopupEntryModal
  | MultiLineEntryModal
  | ProgramEditorModal
  | WorldSearchModal;

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
    case "programEditor":
      renderProgramEditor(write, m);
      return;
    case "worldSearch":
      renderWorldSearch(write, m);
      return;
  }
}

const WORLD_SEARCH_LIMIT = 50;
const WORLD_TITLE_WIDTH = 38;
const WORLD_DETAIL_WIDTH = 42;
const WORLD_SEARCH_ROW = TEXT_WINDOW_Y + TEXT_WINDOW_HEIGHT - 2;

function fitText(text: string, width: number): string {
  if (text.length <= width) {
    return text;
  }
  if (width <= 1) {
    return text.slice(0, width);
  }
  return text.slice(0, width - 1) + "\x1a";
}

function worldSearchMatches(m: WorldSearchModal): WorldSearchEntry[] {
	const terms = m.query.toLowerCase().trim().split(/\s+/).filter(Boolean);
	const lobby = m.entries.filter((entry) => entry.world.toUpperCase() === "TOWN");
	if (terms.length === 0) {
		const occupied = m.entries.filter((entry) => entry.world.toUpperCase() !== "TOWN" && (entry.players ?? 0) > 0);
		return [...lobby, ...occupied].slice(0, WORLD_SEARCH_LIMIT);
	}
	const matches = m.entries.filter((entry) => {
		if (entry.world.toUpperCase() === "TOWN") {
			return false;
		}
		const haystack = [entry.world, entry.id, entry.title, entry.author, entry.created].join(" ").toLowerCase();
		return terms.every((term) => haystack.includes(term));
	});
	return [...matches, ...lobby].slice(0, WORLD_SEARCH_LIMIT);
}

function worldSearchLines(matches: WorldSearchEntry[]): string[] {
	const count = matches.length === 1 ? "1 match" : `${matches.length} matches`;
	const lines = [
		`$${count}  Type below to search the Museum`,
		"",
  ];
  if (matches.length === 0) {
    lines.push("  No matching worlds.");
    lines.push("  Try a title, author, year, or id.");
    return lines;
  }
  for (let i = 0; i < matches.length; i += 1) {
    const entry = matches[i];
    const playerText = worldSearchPlayerText(entry.players ?? 0);
    const sourceText = entry.source === "museum" ? "  Museum" : "";
    lines.push(`!${String(i)};${fitText(entry.title || entry.world, WORLD_TITLE_WIDTH)}`);
    lines.push(fitText(`  by ${entry.author || "Unknown"}  ${entry.created || "????"}${sourceText}`, WORLD_DETAIL_WIDTH));
    if (playerText) {
      lines.push(fitText(`  ${playerText}`, WORLD_DETAIL_WIDTH));
    }
  }
  return lines;
}

function worldSearchPlayerText(players: number): string {
  if (players <= 0) {
    return "";
  }
  return ` (${players} ${players === 1 ? "player" : "players"} currently online)`;
}

function worldSearchLinePos(selected: number, matches: WorldSearchEntry[]): number {
  if (matches.length === 0) {
    return 6;
  }
  const clamped = Math.min(Math.max(0, selected), matches.length - 1);
  let pos = 3;
  for (let i = 0; i < clamped; i += 1) {
    pos += matches[i].players ? 3 : 2;
  }
  return pos;
}

function renderWorldSearch(write: WriteText, m: WorldSearchModal) {
  const matches = worldSearchMatches(m);
  if (matches.length > 0 && m.selected >= matches.length) {
    m.selected = matches.length - 1;
  }
  renderTextWindow(write, {
    title: m.title,
    lines: worldSearchLines(matches),
    linePos: worldSearchLinePos(m.selected, matches),
    viewingFile: false,
  });
  renderWorldSearchInput(write, m.query);
}

function renderWorldSearchInput(write: WriteText, query: string) {
  const label = "Type to search: ";
  const input = query.length === 0 ? "\xdb" : `${query}\xdb`;
  const maxInput = WORLD_DETAIL_WIDTH - label.length;
  const text = label + fitText(input, maxInput).padEnd(maxInput, " ");
  write(TEXT_WINDOW_X + 4, WORLD_SEARCH_ROW, 0x70, text);
}

// renderProgramEditor is TextWindowEdit's screen: the raw lines, plus the block
// caret ZZT paints at (charPos+X+3) on the center row in color 0x70.
function renderProgramEditor(write: WriteText, m: ProgramEditorModal) {
  renderTextWindow(write, { title: m.title, lines: m.lines, linePos: m.linePos, viewingFile: false }, true);
  const line = m.lines[m.linePos - 1] ?? "";
  const charPos = Math.min(Math.max(1, m.charPos), line.length + 1);
  const cursorY = TEXT_WINDOW_Y + Math.floor(TEXT_WINDOW_HEIGHT / 2) + 1;
  const glyph = charPos <= line.length ? line[charPos - 1] : " ";
  write(charPos + TEXT_WINDOW_X + 3, cursorY, 0x70, glyph);
  renderProgramAidsPanel(write, m);
}

// renderProgramAidsPanel is the M5.7 right-margin panel: the object's :labels
// (with 1-based line numbers, for navigation) over the tokenizer's advisory
// warnings. It lives in the free columns beside the editor window and is purely
// informational — the aid never blocks a save.
const AIDS_PANEL_X = TEXT_WINDOW_X + TEXT_WINDOW_WIDTH + 1; // 56
const AIDS_PANEL_W = 80 - AIDS_PANEL_X; // 24
function renderProgramAidsPanel(write: WriteText, m: ProgramEditorModal) {
  const fit = (s: string) => (s.length > AIDS_PANEL_W ? s.slice(0, AIDS_PANEL_W) : s.padEnd(AIDS_PANEL_W, " "));
  const bottom = TEXT_WINDOW_Y + TEXT_WINDOW_HEIGHT;
  for (let y = TEXT_WINDOW_Y; y < bottom; y += 1) {
    write(AIDS_PANEL_X, y, 0x10, " ".repeat(AIDS_PANEL_W));
  }
  let y = TEXT_WINDOW_Y;
  write(AIDS_PANEL_X, y, 0x1f, fit(" Labels"));
  y += 1;
  if (m.labels.length === 0) {
    write(AIDS_PANEL_X, y, 0x18, fit("  (none)"));
    y += 1;
  }
  for (const label of m.labels) {
    if (y >= bottom - 3) break;
    write(AIDS_PANEL_X, y, 0x1e, fit(`  ${label.line + 1}: ${label.name}`));
    y += 1;
  }
  y += 1;
  if (y < bottom) {
    write(AIDS_PANEL_X, y, m.warnings.length > 0 ? 0x1c : 0x1f, fit(" Warnings"));
    y += 1;
  }
  if (m.warnings.length === 0 && y < bottom) {
    write(AIDS_PANEL_X, y, 0x18, fit("  (none)"));
    y += 1;
  }
  for (const warning of m.warnings) {
    if (y >= bottom) break;
    write(AIDS_PANEL_X, y, 0x1c, fit(`  L${warning.line + 1} ${warning.message}`));
    y += 1;
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
    case "programEditor":
      return programEditorKey(m, event);
    case "worldSearch":
      return worldSearchKey(m, event);
  }
}

function worldSearchKey(m: WorldSearchModal, event: KeyboardEvent): KeyResult {
  const matches = worldSearchMatches(m);
  switch (event.code) {
    case "Escape":
      return "close";
    case "Enter":
      if (matches.length === 0) {
        return "ignore";
      }
      m.onSelect(matches[Math.min(Math.max(0, m.selected), matches.length - 1)]);
      return "close";
    case "ArrowUp":
      m.selected = Math.max(0, m.selected - 1);
      return "redraw";
    case "ArrowDown":
      m.selected = Math.min(Math.max(0, matches.length - 1), m.selected + 1);
      return "redraw";
    case "PageUp":
      m.selected = Math.max(0, m.selected - TEXT_WINDOW_PAGE);
      return "redraw";
    case "PageDown":
      m.selected = Math.min(Math.max(0, matches.length - 1), m.selected + TEXT_WINDOW_PAGE);
      return "redraw";
    case "Backspace":
      if (m.query.length === 0) {
        return "ignore";
      }
      m.query = m.query.slice(0, -1);
      m.selected = 0;
      m.onQuery?.(m.query);
      return "redraw";
    default:
      if (event.ctrlKey || event.metaKey || event.altKey) {
        return "ignore";
      }
      if (event.key.length !== 1 || event.key < " " || event.key.charCodeAt(0) >= 0x80) {
        return "ignore";
      }
      if (m.query.length >= 30) {
        return "ignore";
      }
      m.query += event.key;
      m.selected = 0;
      m.onQuery?.(m.query);
      return "redraw";
  }
}

// Pascal Copy(s, index, count), 1-based and clamped, matching lib.go.
function pascalCopy(s: string, index: number, count: number): string {
  if (index < 1) {
    index = 1;
  }
  if (count < 0 || count > s.length - index + 1) {
    count = s.length - index + 1;
  }
  if (count <= 0) {
    return "";
  }
  return s.slice(index - 1, index - 1 + count);
}

// programEditorKey is TextWindowEdit's key loop (txtwind.go:255), one keypress at
// a time. charPos is clamped to the current line at entry, exactly as the Pascal
// clamps it at the top of its loop before acting.
function programEditorKey(m: ProgramEditorModal, event: KeyboardEvent): KeyResult {
  if (m.lines.length === 0) {
    m.lines.push("");
  }
  const line = () => m.lines[m.linePos - 1];
  m.charPos = Math.min(Math.max(1, m.charPos), line().length + 1);
  let newLinePos = m.linePos;

  const deleteCurrLine = () => {
    if (m.lines.length > 1) {
      m.lines.splice(m.linePos - 1, 1);
      if (m.linePos > m.lines.length) {
        newLinePos = m.lines.length;
      }
    } else {
      m.lines[0] = "";
    }
  };

  switch (event.code) {
    case "Escape":
      m.onSubmit(m.lines);
      return "close";
    case "ArrowUp":
      newLinePos = m.linePos - 1;
      break;
    case "ArrowDown":
      newLinePos = m.linePos + 1;
      break;
    case "PageUp":
      newLinePos = m.linePos - PROGRAM_PAGE;
      break;
    case "PageDown":
      newLinePos = m.linePos + PROGRAM_PAGE;
      break;
    case "ArrowRight":
      m.charPos += 1;
      if (m.charPos > line().length + 1) {
        m.charPos = 1;
        newLinePos = m.linePos + 1;
      }
      break;
    case "ArrowLeft":
      m.charPos -= 1;
      if (m.charPos < 1) {
        m.charPos = TEXT_WINDOW_WIDTH;
        newLinePos = m.linePos - 1;
      }
      break;
    case "Enter":
      if (m.lines.length < PROGRAM_MAX_LINES) {
        const rest = pascalCopy(line(), m.charPos, line().length - m.charPos + 1);
        m.lines[m.linePos - 1] = pascalCopy(line(), 1, m.charPos - 1);
        m.lines.splice(m.linePos, 0, rest);
        newLinePos = m.linePos + 1;
        m.charPos = 1;
      }
      break;
    case "Backspace":
      if (m.charPos > 1) {
        m.lines[m.linePos - 1] =
          pascalCopy(line(), 1, m.charPos - 2) + pascalCopy(line(), m.charPos, line().length - m.charPos + 1);
        m.charPos -= 1;
      } else if (line().length === 0) {
        deleteCurrLine();
        newLinePos = m.linePos - 1;
        m.charPos = TEXT_WINDOW_WIDTH;
      }
      break;
    case "Insert":
      m.insertMode = !m.insertMode;
      break;
    case "Delete":
      m.lines[m.linePos - 1] =
        pascalCopy(line(), 1, m.charPos - 1) + pascalCopy(line(), m.charPos + 1, line().length - m.charPos);
      break;
    default:
      if (event.ctrlKey && (event.code === "KeyY" || event.key.toLowerCase() === "y")) {
        deleteCurrLine();
        break;
      }
      // Any other modifier chord (Ctrl+C, Cmd+R, …) is not text input.
      if (event.ctrlKey || event.metaKey || event.altKey) {
        return "ignore";
      }
      if (event.key.length !== 1 || event.key < " " || event.key.charCodeAt(0) >= 0x80) {
        return "ignore";
      }
      if (m.charPos >= PROGRAM_CHAR_MAX) {
        return "ignore";
      }
      if (!m.insertMode) {
        m.lines[m.linePos - 1] =
          pascalCopy(line(), 1, m.charPos - 1) + event.key + pascalCopy(line(), m.charPos + 1, line().length - m.charPos);
        m.charPos += 1;
      } else if (line().length < PROGRAM_LINE_MAX) {
        m.lines[m.linePos - 1] =
          pascalCopy(line(), 1, m.charPos - 1) + event.key + pascalCopy(line(), m.charPos, line().length - m.charPos + 1);
        m.charPos += 1;
      }
      break;
  }

  if (newLinePos < 1) {
    newLinePos = 1;
  } else if (newLinePos > m.lines.length) {
    newLinePos = m.lines.length;
  }
  m.linePos = newLinePos;
  return "redraw";
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
