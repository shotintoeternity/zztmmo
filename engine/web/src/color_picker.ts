// color_picker.ts — M19.2: the window where a player picks the 24-bit background
// their ☻ is drawn on (M19.1 draws it; this chooses it).
//
// Pure in the player_tint.ts / editor_cursor.ts shape: no DOM, no canvas, no
// storage and no socket. It takes a state object and a KeyboardEvent-shaped
// value and returns what changed, so every rule below is unit-testable under
// Node. main.ts owns the storage key and the join; modal.ts routes the keys.
//
// THE WINDOW IS THE M4.1 CP437 FURNITURE (textwindow.ts), like every other
// window this client draws, but it lays out its own interior instead of feeding
// renderTextWindow a line list: the 16 quick picks are a two-column grid and
// each row has to be drawn in the color it offers, which a text-window line
// cannot say.
//
// THE SWATCH IS THE COLOUR AS FOREGROUND, NOT AS BACKGROUND. Drawn as a filled
// square (0xFE) in attribute `i`, its background nibble is black — so one write
// per row stands for all sixteen colors, and the black cells either side keep
// the one color that IS the window's background (dark blue) from vanishing
// into it.
//
// WHY THE 16 QUICK PICKS AT ALL. The owner's 2026-07-10 design names purists:
// somebody who wants to be DOS light-cyan should not have to know that is
// #55ffff. They are ordinary picks — each one submits its hex like any other, so
// nothing downstream has to know a quick pick from a typed one.

import type { WriteText } from "./sidebar";
import {
  renderTextWindowFrame,
  TEXT_WINDOW_HEIGHT,
  TEXT_WINDOW_WIDTH,
  TEXT_WINDOW_X,
  TEXT_WINDOW_Y,
} from "./textwindow";

/** modal.ts's KeyResult, restated here so this module does not import its router. */
export type ColorPickerKeyResult = "close" | "redraw" | "ignore";

/** The subset of KeyboardEvent this module reads, so a test can drive it. */
export type ColorKeyLike = {
  code: string;
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
};

export type ColorPick = { name: string; hex: string };

/**
 * The DOS 16, in palette order, with the EGA hex main.ts already paints them
 * with (`ega`) — a quick pick has to be the color the player has been looking
 * at since 1991, not a fresh approximation of it.
 *
 * The names for 9..15 are ZZT's own (ColorNames, GAME.PAS:92), which is why the
 * bright half carries the plain names and the dark half is qualified: in ZZT,
 * "Blue" has always meant the bright one.
 */
export const DOS_PICKS: readonly ColorPick[] = [
  { name: "Black", hex: "#000000" },
  { name: "Dark Blue", hex: "#0000aa" },
  { name: "Dark Green", hex: "#00aa00" },
  { name: "Dark Cyan", hex: "#00aaaa" },
  { name: "Dark Red", hex: "#aa0000" },
  { name: "Dark Purple", hex: "#aa00aa" },
  { name: "Brown", hex: "#aa5500" },
  { name: "Grey", hex: "#aaaaaa" },
  { name: "Dark Grey", hex: "#555555" },
  { name: "Blue", hex: "#5555ff" },
  { name: "Green", hex: "#55ff55" },
  { name: "Cyan", hex: "#55ffff" },
  { name: "Red", hex: "#ff5555" },
  { name: "Purple", hex: "#ff55ff" },
  { name: "Yellow", hex: "#ffff55" },
  { name: "White", hex: "#ffffff" },
];

/** The two rows past the grid: the typed color, then the way back to vanilla. */
export const CUSTOM_INDEX = DOS_PICKS.length;
export const VANILLA_INDEX = DOS_PICKS.length + 1;

const GRID_ROWS = 8;
const HEX_DIGITS = 6;

export type ColorPickerModal = {
  kind: "colorPicker";
  title: string;
  /** 0..15 a quick pick, CUSTOM_INDEX the typed field, VANILLA_INDEX no color. */
  selected: number;
  /** The typed hex digits, without the '#' — the window draws that. */
  custom: string;
  /**
   * What is picked now, so the window can say so and the player can leave
   * without changing anything. "" is the vanilla white-on-blue player.
   */
  current: string;
  /** null is Escape (nothing changes); "" is a deliberate return to vanilla. */
  onSubmit: (color: string | null) => void;
};

const HEX_COLOR = /^#[0-9a-fA-F]{6}$/;
const HEX_DIGIT = /^[0-9a-fA-F]$/;

/** newColorPickerModal opens the window on whatever the player is wearing. */
export function newColorPickerModal(
  current: string,
  onSubmit: (color: string | null) => void,
  title = "Your Player Color",
): ColorPickerModal {
  const normalized = HEX_COLOR.test(current) ? current.toLowerCase() : "";
  const quick = DOS_PICKS.findIndex((pick) => pick.hex === normalized);
  return {
    kind: "colorPicker",
    title,
    // Open on the current pick: a quick pick if it is one, otherwise the custom
    // row already holding it, and the vanilla row when nothing is picked.
    selected: quick >= 0 ? quick : normalized ? CUSTOM_INDEX : VANILLA_INDEX,
    custom: quick >= 0 || !normalized ? "" : normalized.slice(1),
    current: normalized,
    onSubmit,
  };
}

/**
 * The color the selected row stands for: a quick pick's hex, the typed field
 * once it holds six digits, "" on the vanilla row, and null while a half-typed
 * hex is not yet a color (which is also what makes Enter refuse it).
 */
export function colorPickerValue(m: ColorPickerModal): string | null {
  if (m.selected === VANILLA_INDEX) {
    return "";
  }
  if (m.selected === CUSTOM_INDEX) {
    return m.custom.length === HEX_DIGITS ? "#" + m.custom.toLowerCase() : null;
  }
  const pick = DOS_PICKS[m.selected];
  return pick ? pick.hex : null;
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

/**
 * colorPickerKey is the window's whole input rule.
 *
 * ↑↓ walk a column, ←→ change column, and the two rows below the grid are the
 * bottom of it — so the d-pad alone reaches every row, which is what makes the
 * window work on a phone (M16.18a's controls send exactly these codes).
 *
 * A hex digit typed anywhere lands in the custom field and moves the cursor
 * there, the worldSearch precedent: a player who knows their hex should not have
 * to find the row first. Nothing else here is a shortcut, so no keystroke means
 * two things at once.
 *
 * Every key is consumed by the caller whatever this returns — a window that let
 * 'P' through would start the game underneath it (the M16.18a Fire lesson).
 */
export function colorPickerKey(m: ColorPickerModal, event: ColorKeyLike): ColorPickerKeyResult {
  switch (event.code) {
    case "Escape":
      m.onSubmit(null);
      return "close";
    case "Enter": {
      const value = colorPickerValue(m);
      if (value === null) {
        // A half-typed hex is not a color; refusing here keeps a window open
        // that the player is plainly still filling in.
        return "ignore";
      }
      m.onSubmit(value);
      return "close";
    }
    case "ArrowUp":
      m.selected = moveUp(m.selected);
      return "redraw";
    case "ArrowDown":
      m.selected = moveDown(m.selected);
      return "redraw";
    case "ArrowLeft":
      m.selected = moveLeft(m.selected);
      return "redraw";
    case "ArrowRight":
      m.selected = moveRight(m.selected);
      return "redraw";
    case "Backspace":
      if (m.custom.length === 0) {
        return "ignore";
      }
      m.custom = m.custom.slice(0, -1);
      m.selected = CUSTOM_INDEX;
      return "redraw";
    default: {
      if (event.ctrlKey || event.metaKey || event.altKey) {
        return "ignore";
      }
      // '#' is drawn by the window rather than stored, so typing one is taken as
      // the start of a hex — a pasted "#ff8800" types the same as "ff8800".
      if (event.key === "#") {
        m.custom = "";
        m.selected = CUSTOM_INDEX;
        return "redraw";
      }
      if (!HEX_DIGIT.test(event.key)) {
        return "ignore";
      }
      m.selected = CUSTOM_INDEX;
      // A seventh digit restarts rather than being dropped in silence: someone
      // correcting a typo is retyping the color, not appending to it.
      m.custom = (m.custom.length >= HEX_DIGITS ? "" : m.custom) + event.key.toLowerCase();
      return "redraw";
    }
  }
}

// The rows top to bottom are: the vanilla default, the 16-color grid, the hex
// field. The default is FIRST because it is where the window opens and where a
// player who has not picked anything already is.
function moveUp(selected: number): number {
  if (selected === VANILLA_INDEX) {
    return VANILLA_INDEX; // the top row is the top row
  }
  if (selected === CUSTOM_INDEX) {
    return GRID_ROWS - 1; // the foot of the left column
  }
  return selected % GRID_ROWS === 0 ? VANILLA_INDEX : selected - 1;
}

function moveDown(selected: number): number {
  if (selected === VANILLA_INDEX) {
    return 0; // into the grid, top-left
  }
  if (selected === CUSTOM_INDEX) {
    return CUSTOM_INDEX;
  }
  return selected % GRID_ROWS === GRID_ROWS - 1 ? CUSTOM_INDEX : selected + 1;
}

function moveLeft(selected: number): number {
  if (selected >= CUSTOM_INDEX) {
    return selected;
  }
  return selected >= GRID_ROWS ? selected - GRID_ROWS : selected;
}

function moveRight(selected: number): number {
  if (selected >= CUSTOM_INDEX) {
    return selected;
  }
  return selected < GRID_ROWS ? selected + GRID_ROWS : selected;
}

// ---------------------------------------------------------------------------
// The window
// ---------------------------------------------------------------------------

// Interior geometry, derived from the window rather than typed in: the frame
// occupies TEXT_WINDOW_Y..+HEIGHT with its title on +1 and the separator on +2,
// so the first free interior row is +3 and the last is +HEIGHT-1.
const FIRST_ROW = TEXT_WINDOW_Y + 3;
const LAST_ROW = TEXT_WINDOW_Y + TEXT_WINDOW_HEIGHT - 1;
const COLUMN_X = [TEXT_WINDOW_X + 4, TEXT_WINDOW_X + 25];
const VANILLA_ROW = FIRST_ROW + 1;
const GRID_TOP = VANILLA_ROW + 2;
const CUSTOM_ROW = GRID_TOP + GRID_ROWS;
const PREVIEW_ROW = LAST_ROW - 1;
const HINT_ROW = LAST_ROW;

// TextWindowInit's interior width, the same string drawLine fills a line with.
const INNER_EMPTY = " ".repeat(TEXT_WINDOW_WIDTH - 5);

const NORMAL_COLOR = 0x1e;
const SELECTED_COLOR = 0x1f;
const CURSOR_COLOR = 0x1c;
const HINT_COLOR = 0x1a;
// A small filled square in a black tile: one cell, so the grid reads as a list
// of colors rather than two black stripes, and the tile is what keeps BOTH ends
// of the palette visible — black shows against the window's blue, dark blue
// shows against the tile's black.
const SWATCH = "\xfe";
const CURSOR = "\x10";

/** The player glyph the preview draws, and the attribute the M19.1 tint gates on. */
export const PREVIEW_CHAR = 0x02;
export const PREVIEW_COLOR = 0x1f;

/**
 * Where the preview ☻ is drawn, and what color it should be tinted. main.ts
 * feeds this to the same per-cell RGB override the board uses, so the smiley in
 * this window is painted by the code that paints the real one — the preview
 * cannot drift from the thing it previews.
 *
 * Returns null while the selection has no color (a half-typed hex, or vanilla),
 * where the untinted 0x1F cell is already the right answer.
 */
export function colorPickerPreview(m: ColorPickerModal): { x: number; y: number; rgb: string } | null {
  const value = colorPickerValue(m);
  if (!value) {
    return null;
  }
  return { x: previewX(), y: PREVIEW_ROW, rgb: value };
}

function previewX(): number {
  return TEXT_WINDOW_X + 4 + PREVIEW_LABEL.length;
}

const PREVIEW_LABEL = "This is you:  ";

/** renderColorPicker draws the window: frame, grid, typed field, and preview. */
export function renderColorPicker(write: WriteText, m: ColorPickerModal) {
  renderTextWindowFrame(write, m.title);
  // The interior, in the window's own blue. renderTextWindow gets this for free
  // from drawLine, which fills every line before writing it; a window that lays
  // out its own interior has to fill it, or the frame's black shows between the
  // text and the picker is the one window on screen that does not match.
  for (let y = FIRST_ROW; y <= LAST_ROW; y += 1) {
    write(TEXT_WINDOW_X + 2, y, NORMAL_COLOR, INNER_EMPTY);
  }

  write(TEXT_WINDOW_X + 4, FIRST_ROW, HINT_COLOR, "Pick a color for your smiley:");

  // The default, at the top and where the window opens: the vanilla ZZT player,
  // shown as the thing itself — char 2 in 0x1F, white on blue, untinted.
  drawCursor(write, COLUMN_X[0], VANILLA_ROW, m.selected === VANILLA_INDEX);
  write(COLUMN_X[0] + 2, VANILLA_ROW, PREVIEW_COLOR, String.fromCharCode(PREVIEW_CHAR));
  write(
    COLUMN_X[0] + 4,
    VANILLA_ROW,
    m.selected === VANILLA_INDEX ? SELECTED_COLOR : NORMAL_COLOR,
    "Default (white on blue)",
  );

  for (let i = 0; i < DOS_PICKS.length; i += 1) {
    const column = Math.floor(i / GRID_ROWS);
    const x = COLUMN_X[column];
    const y = GRID_TOP + (i % GRID_ROWS);
    drawCursor(write, x, y, m.selected === i);
    // The swatch is the color as FOREGROUND (attribute `i`, so its background
    // nibble is black): a filled square in the color, with a black cell either
    // side of it so that dark blue — the window's own background — is still a
    // square you can see rather than a hole in the window.
    write(x + 2, y, i, SWATCH);
    write(x + 4, y, m.selected === i ? SELECTED_COLOR : NORMAL_COLOR, DOS_PICKS[i].name);
  }

  drawCursor(write, COLUMN_X[0], CUSTOM_ROW, m.selected === CUSTOM_INDEX);
  const typed = "#" + m.custom.padEnd(HEX_DIGITS, "\xfa");
  write(COLUMN_X[0] + 2, CUSTOM_ROW, m.selected === CUSTOM_INDEX ? SELECTED_COLOR : NORMAL_COLOR, "Any color: ");
  write(COLUMN_X[0] + 14, CUSTOM_ROW, m.selected === CUSTOM_INDEX ? 0x70 : NORMAL_COLOR, typed);

  // The preview is the real thing: char 2 in 0x1F, which is exactly what the
  // server draws for a player and exactly what the M19.1 tint gate accepts.
  write(TEXT_WINDOW_X + 4, PREVIEW_ROW, NORMAL_COLOR, PREVIEW_LABEL);
  write(previewX(), PREVIEW_ROW, PREVIEW_COLOR, String.fromCharCode(PREVIEW_CHAR));

  const hint = "\x18\x19\x1b\x1a move   Enter picks   Esc cancels";
  write(TEXT_WINDOW_X + Math.trunc((TEXT_WINDOW_WIDTH - hint.length) / 2), HINT_ROW, HINT_COLOR, hint);
}

function drawCursor(write: WriteText, x: number, y: number, selected: boolean) {
  write(x, y, selected ? CURSOR_COLOR : NORMAL_COLOR, selected ? CURSOR : " ");
}
