// The ZZT CP437 text window: help screens now, scrolls (M3.10) next.
//
// Transcribed from engine/txtwind.go (TextWindowInit / TextWindowDrawOpen /
// TextWindowDraw / TextWindowDrawLine), itself converted from TXTWIND.PAS.
// Geometry comes from the engine's own TextWindowInit(5, 3, 50, 18) call.
//
// One deliberate difference from the original: ZZT stashes the screen cells
// under the window and blits them back on close. We can't, because the
// simulation no longer freezes while a window is open (the M1.3 de-modal
// deviation) — the board keeps moving underneath. So the caller renders this
// window as an overlay on top of the live board each frame instead.

import type { WriteText } from "./sidebar";

export const TEXT_WINDOW_X = 5;
export const TEXT_WINDOW_Y = 3;
export const TEXT_WINDOW_WIDTH = 50;
export const TEXT_WINDOW_HEIGHT = 18;

export type TextWindowState = {
  title: string;
  lines: string[];
  linePos: number; // 1-based, as in the Pascal
  viewingFile: boolean;
};

const repeat = (s: string, n: number) => (n > 0 ? s.repeat(n) : "");

// TextWindowInit's derived strings.
const innerEmpty = repeat(" ", TEXT_WINDOW_WIDTH - 5);
const innerLine = repeat("\xcd", TEXT_WINDOW_WIDTH - 5);
const strTop = `\xc6\xd1${innerLine}\xd1\xb5`;
const strBottom = `\xc6\xcf${innerLine}\xcf\xb5`;
const strSep = ` \xc6${innerLine}\xb5 `;
const strText = ` \xb3${innerEmpty}\xb3 `;
const strInnerArrows = `\xaf${innerEmpty.slice(1, innerEmpty.length - 1)}\xae`;
const strInnerSep = buildInnerSep();

function buildInnerSep(): string {
  const b = innerEmpty.split("");
  for (let i = 1; i < Math.floor(TEXT_WINDOW_WIDTH / 5); i += 1) {
    b[i * 5 + Math.floor((TEXT_WINDOW_WIDTH % 5) / 2) - 1] = "\x07";
  }
  return b.join("");
}

// Pascal Pos: 1-based index of b in s, or 0.
function pos(ch: string, s: string): number {
  return s.indexOf(ch) + 1;
}

// Pascal Copy, matching lib.go's clamping.
function copy(s: string, index: number, count: number): string {
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

function drawTitle(write: WriteText, color: number, title: string) {
  write(TEXT_WINDOW_X + 2, TEXT_WINDOW_Y + 1, color, innerEmpty);
  write(TEXT_WINDOW_X + Math.trunc((TEXT_WINDOW_WIDTH - title.length) / 2), TEXT_WINDOW_Y + 1, color, title);
}

// drawFrame is TextWindowDrawOpen's settled state — the same writes, with the
// open animation's Delay(25) dropped.
function drawFrame(write: WriteText, title: string) {
  for (let iy = Math.floor(TEXT_WINDOW_HEIGHT / 2); iy >= 0; iy -= 1) {
    write(TEXT_WINDOW_X, TEXT_WINDOW_Y + iy + 1, 0x0f, strText);
    write(TEXT_WINDOW_X, TEXT_WINDOW_Y + TEXT_WINDOW_HEIGHT - iy - 1, 0x0f, strText);
    write(TEXT_WINDOW_X, TEXT_WINDOW_Y + iy, 0x0f, strTop);
    write(TEXT_WINDOW_X, TEXT_WINDOW_Y + TEXT_WINDOW_HEIGHT - iy, 0x0f, strBottom);
  }
  write(TEXT_WINDOW_X, TEXT_WINDOW_Y + 2, 0x0f, strSep);
  drawTitle(write, 0x1e, title);
}

// drawLine is TextWindowDrawLine with withoutFormatting = false.
function drawLine(write: WriteText, state: TextWindowState, lpos: number) {
  const lineCount = state.lines.length;
  const lineY = TEXT_WINDOW_Y + lpos - state.linePos + Math.floor(TEXT_WINDOW_HEIGHT / 2) + 1;

  if (lpos === state.linePos) {
    write(TEXT_WINDOW_X + 2, lineY, 0x1c, strInnerArrows);
  } else {
    write(TEXT_WINDOW_X + 2, lineY, 0x1e, innerEmpty);
  }

  if (lpos > 0 && lpos <= lineCount) {
    const line = state.lines[lpos - 1];
    let textOffset = 1;
    let textColor = 0x1e;
    let textX = TEXT_WINDOW_X + 4;
    if (line.length > 0) {
      switch (line[0]) {
        case "!":
          textOffset = pos(";", line) + 1;
          write(textX + 2, lineY, 0x1d, "\x10");
          textX += 5;
          textColor = 0x1f;
          break;
        case ":":
          textOffset = pos(";", line) + 1;
          textColor = 0x1f;
          break;
        case "$":
          textOffset = 2;
          textColor = 0x1f;
          textX = textX - 4 + Math.trunc((TEXT_WINDOW_WIDTH - line.length) / 2);
          break;
      }
    }
    if (textOffset > 0) {
      write(textX, lineY, textColor, copy(line, textOffset, line.length - textOffset + 1));
    }
  } else if (lpos === 0 || lpos === lineCount + 1) {
    write(TEXT_WINDOW_X + 2, lineY, 0x1e, strInnerSep);
  } else if (lpos === -4 && state.viewingFile) {
    write(TEXT_WINDOW_X + 2, lineY, 0x1a, "   Use            to view text,");
    write(TEXT_WINDOW_X + 2 + 7, lineY, 0x1f, "\x18 \x19, Enter");
  }
}

// renderTextWindow paints frame + lines, i.e. TextWindowDrawOpen followed by
// TextWindowDraw, which is what the player sees once a window has opened.
export function renderTextWindow(write: WriteText, state: TextWindowState) {
  drawFrame(write, state.title);
  for (let i = 0; i <= TEXT_WINDOW_HEIGHT - 4; i += 1) {
    drawLine(write, state, state.linePos - Math.floor(TEXT_WINDOW_HEIGHT / 2) + i + 2);
  }
  drawTitle(write, 0x1e, state.title);
}

// clampLinePos mirrors TextWindowSelect's bounds check.
export function clampLinePos(linePos: number, lineCount: number): number {
  if (linePos < 1) {
    return 1;
  }
  if (linePos > lineCount) {
    return lineCount;
  }
  return linePos;
}

export const TEXT_WINDOW_PAGE = TEXT_WINDOW_HEIGHT - 4;
