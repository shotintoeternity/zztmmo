// text_runs.ts — the words on a board, found in the cells.
//
// Two kinds of text live on a ZZT board and neither reads as a row of standing
// cards. Text elements (E_TEXT_*) draw a white letter on a colored background,
// one per cell: signs like "Palace ->". And the board message is written by
// the engine over the bottom row of the board in a color that cycles 9..15 on
// a black background, one space either side (GAME.PAS BoardDrawBorder /
// game.go). This module finds both so the 3D views can draw them as text at
// the bottom of the screen instead of in the world.
//
// Pure, and tested under node.

export type TextRun = {
  /** 0-based screen column of the first cell. */
  x: number;
  /** 0-based screen row. */
  y: number;
  /** DOS attribute of the run. */
  color: number;
  text: string;
  /** The 0-based cell indices (y * cols + x) the run occupies. */
  cells: number[];
};

export type BoardText = {
  signs: TextRun[];
  message: TextRun | null;
};

/** A sign: the runs of one color that touch, read as lines. */
export type SignGroup = {
  color: number;
  lines: string[];
  /** Center of the group, 0-based screen coordinates. */
  cx: number;
  cy: number;
  cells: number[];
};

type CellLike = { ch: number; color: number };

const MESSAGE_ROW = 24;

function printable(ch: number): boolean {
  return ch >= 0x20 && ch <= 0x7e;
}

function hasLetter(text: string): boolean {
  return /[A-Za-z0-9]/.test(text);
}

function isSignCell(cell: CellLike): boolean {
  const fg = cell.color & 0x0f;
  const bg = (cell.color >> 4) & 0x0f;
  return fg === 0x0f && bg !== 0 && printable(cell.ch);
}

function isMessageCell(cell: CellLike): boolean {
  const fg = cell.color & 0x0f;
  const bg = (cell.color >> 4) & 0x0f;
  return fg >= 0x09 && bg === 0 && printable(cell.ch);
}

/** runs collects maximal horizontal runs of cells that satisfy `keep` and share one attribute. */
function runs(cells: readonly CellLike[], cols: number, boardCols: number, y: number, keep: (cell: CellLike) => boolean): TextRun[] {
  const out: TextRun[] = [];
  let x = 0;
  while (x < boardCols) {
    const start = cells[y * cols + x];
    if (!keep(start)) {
      x += 1;
      continue;
    }
    let end = x;
    while (end < boardCols && keep(cells[y * cols + end]) && cells[y * cols + end].color === start.color) {
      end += 1;
    }
    const indices: number[] = [];
    let text = "";
    for (let i = x; i < end; i += 1) {
      indices.push(y * cols + i);
      text += String.fromCharCode(cells[y * cols + i].ch);
    }
    out.push({ x, y, color: start.color, text, cells: indices });
    x = end;
  }
  return out;
}

/**
 * verticalWords chains single-letter runs down a column into words. A sign
 * written downwards is one letter per row, each usually padded with a space on
 * either side (" B ", " A ", " N ", " K "), so the letters are found as
 * one-letter horizontal runs and joined here where they stack in one column in
 * one color. Runs that chain to nothing are returned as they were.
 */
function verticalWords(singles: readonly TextRun[]): TextRun[] {
  type Single = { run: TextRun; col: number; letter: string };
  const byColumn = new Map<string, Single[]>();
  for (const run of singles) {
    const letter = run.text.trim();
    const col = run.x + run.text.indexOf(letter);
    const key = `${col}:${run.color}`;
    const list = byColumn.get(key) ?? [];
    list.push({ run, col, letter });
    byColumn.set(key, list);
  }
  const out: TextRun[] = [];
  for (const list of byColumn.values()) {
    list.sort((a, b) => a.run.y - b.run.y);
    let i = 0;
    while (i < list.length) {
      let j = i;
      while (j + 1 < list.length && list[j + 1].run.y === list[j].run.y + 1) {
        j += 1;
      }
      if (j === i) {
        out.push(list[i].run);
      } else {
        const chain = list.slice(i, j + 1);
        out.push({
          x: chain[0].col,
          y: chain[0].run.y,
          color: chain[0].run.color,
          text: chain.map((s) => s.letter).join(""),
          cells: chain.flatMap((s) => s.run.cells),
        });
      }
      i = j + 1;
    }
  }
  return out;
}

/**
 * boardText finds the signs on every row and the message on the bottom row.
 * A sign is a run of white-on-color printable cells with at least one letter
 * or digit once its spaces are trimmed, read across, or downwards where a
 * column of single letters spells a word (verticalWords); a message is the longest run of
 * bright-on-black printable cells on row 24 with a letter in it.
 */
export function boardText(cells: readonly CellLike[], cols: number, boardCols: number, rows: number): BoardText {
  const signs: TextRun[] = [];
  const singles: TextRun[] = [];
  for (let y = 0; y < rows; y += 1) {
    for (const run of runs(cells, cols, boardCols, y, isSignCell)) {
      const trimmed = run.text.trim();
      if (trimmed.length === 0) {
        continue;
      }
      // A lone punctuation mark may end a word written downwards ("ZZT!"),
      // so it is kept for chaining; on its own it is not a sign.
      if (trimmed.length === 1) {
        singles.push(run);
      } else if (hasLetter(trimmed)) {
        signs.push(run);
      }
    }
  }
  for (const run of verticalWords(singles)) {
    if (hasLetter(run.text)) {
      signs.push(run);
    }
  }
  signs.sort((a, b) => a.y - b.y || a.x - b.x);
  let message: TextRun | null = null;
  if (rows > MESSAGE_ROW) {
    for (const run of runs(cells, cols, boardCols, MESSAGE_ROW, isMessageCell)) {
      if (!hasLetter(run.text)) {
        continue;
      }
      if (!message || run.text.length > message.text.length) {
        message = run;
      }
    }
  }
  return { signs, message };
}

function bounds(run: TextRun, cols: number): { x0: number; y0: number; x1: number; y1: number } {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const i of run.cells) {
    const x = i % cols;
    const y = Math.floor(i / cols);
    x0 = Math.min(x0, x); x1 = Math.max(x1, x);
    y0 = Math.min(y0, y); y1 = Math.max(y1, y);
  }
  return { x0, y0, x1, y1 };
}

/**
 * groupSigns joins runs of one color whose cells touch (within a cell of each
 * other) into signs. Runs that start on the same row become one line, read
 * left to right; runs on different rows become lines in order.
 */
export function groupSigns(signs: readonly TextRun[], cols: number): SignGroup[] {
  const boxes = signs.map((run) => bounds(run, cols));
  const parent = signs.map((_, i) => i);
  const find = (i: number): number => (parent[i] === i ? i : (parent[i] = find(parent[i])));
  for (let a = 0; a < signs.length; a += 1) {
    for (let b = a + 1; b < signs.length; b += 1) {
      if (signs[a].color !== signs[b].color) continue;
      const A = boxes[a], B = boxes[b];
      // Words on a sign are a space apart: two columns between vertical
      // words, one row between lines.
      const touch = A.x0 <= B.x1 + 2 && B.x0 <= A.x1 + 2 && A.y0 <= B.y1 + 1 && B.y0 <= A.y1 + 1;
      if (touch) parent[find(a)] = find(b);
    }
  }
  const members = new Map<number, TextRun[]>();
  signs.forEach((run, i) => {
    const root = find(i);
    const list = members.get(root) ?? [];
    list.push(run);
    members.set(root, list);
  });
  const out: SignGroup[] = [];
  for (const list of members.values()) {
    list.sort((p, q) => p.y - q.y || p.x - q.x);
    const lines: string[] = [];
    let row = -1;
    for (const run of list) {
      const text = run.text.trim();
      if (run.y === row && lines.length > 0) {
        lines[lines.length - 1] += " " + text;
      } else {
        lines.push(text);
        row = run.y;
      }
    }
    const cells = list.flatMap((run) => run.cells);
    let sx = 0, sy = 0;
    for (const i of cells) { sx += i % cols; sy += Math.floor(i / cols); }
    out.push({ color: list[0].color, lines, cx: sx / cells.length, cy: sy / cells.length, cells });
  }
  return out;
}

/** nearestSigns orders sign groups by distance from a 0-based screen cell. */
export function nearestSigns(groups: readonly SignGroup[], x: number, y: number): SignGroup[] {
  return [...groups].sort((a, b) => (Math.abs(a.cx - x) + Math.abs(a.cy - y) * 2) - (Math.abs(b.cx - x) + Math.abs(b.cy - y) * 2));
}
