// M9.1 — board-change transition fade.
//
// Vanilla covers every board change with TransitionDrawBoardChange
// (GAME.PAS / engine game.go:1484): it fills the 60x25 viewport with purple
// '\xdb' blocks in a shuffled order, then reveals the new board in that SAME
// order. This module is the pure, DOM-free half of the browser port — the order
// and the per-cell fill/reveal decision — so a node test can exercise it exactly
// like resume.ts / modal.ts. The rendering half lives in main.ts's drawScreen.
//
// Presentation only: the shuffle uses a caller-supplied rng (Math.random in the
// browser). CLAUDE.md rule 2 governs the simulation, not the client, so the
// order need not match the server's seeded TransitionTable.

// The full-block glyph and its DOS colour, matching TransitionDrawToFill('\xdb',
// 0x05) — purple (fg 5) on black.
export const TRANSITION_FILL_CH = 0xdb;
export const TRANSITION_FILL_COLOR = 0x05;

export interface TransitionState {
  // Board-area cell indices (y*cols + x, x < boardCols) in fill/reveal sequence.
  order: number[];
  // Reverse map: cell index -> its position in `order`.
  orderPos: Map<number, number>;
  // order.length — the number of cells in one phase.
  total: number;
  // Steps processed so far, 0..2*total. [0,total) is the fill phase; [total,
  // 2*total) is the reveal phase.
  step: number;
}

// What a board cell should render as at the current step.
export type CellSource = "old" | "purple" | "new";

// boardCellIndices lists every viewport (non-sidebar) cell index, row-major.
export function boardCellIndices(cols: number, boardCols: number, rows: number): number[] {
  const out: number[] = [];
  for (let y = 0; y < rows; y += 1) {
    for (let x = 0; x < boardCols; x += 1) {
      out.push(y * cols + x);
    }
  }
  return out;
}

// shuffle returns a Fisher–Yates permutation using the supplied rng (0<=r<1).
// It does not mutate the input.
export function shuffle<T>(arr: readonly T[], rng: () => number): T[] {
  const a = arr.slice();
  for (let i = a.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rng() * (i + 1));
    const tmp = a[i];
    a[i] = a[j];
    a[j] = tmp;
  }
  return a;
}

export function createTransition(order: number[]): TransitionState {
  const orderPos = new Map<number, number>();
  order.forEach((idx, i) => orderPos.set(idx, i));
  return { order, orderPos, total: order.length, step: 0 };
}

// cellSource decides how a board cell renders. `pos` is the cell's position in
// the fill/reveal order (undefined for a cell not in the order — treated as
// already-new so nothing outside the viewport is ever masked).
//
//   fill phase   (step <  total): cells reached so far are purple, the rest old.
//   reveal phase (step >= total): cells reached so far are new,    the rest purple.
//
// Because the same order drives both phases, a cell filled early is also
// revealed early — matching vanilla's "same order" guarantee.
export function cellSource(
  pos: number | undefined,
  step: number,
  total: number
): CellSource {
  if (pos === undefined) {
    return "new";
  }
  if (step < total) {
    return pos < step ? "purple" : "old";
  }
  const revealed = step - total;
  return pos < revealed ? "new" : "purple";
}

export function transitionSteps(total: number): number {
  return 2 * total;
}

export function isComplete(state: TransitionState): boolean {
  return state.step >= transitionSteps(state.total);
}
