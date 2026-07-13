import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle transition.ts under Node so M9.1's board-change fade order + reveal
// logic can be exercised as pure logic, the same way resume.test.mjs covers the
// resume state machine. transition.ts is deliberately DOM-free for this.
const output = await build({
  entryPoints: ["src/transition.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const {
  boardCellIndices,
  shuffle,
  createTransition,
  cellSource,
  transitionSteps,
  isComplete,
  TRANSITION_FILL_CH,
  TRANSITION_FILL_COLOR,
} = await import(`data:text/javascript;base64,${source}`);

const COLS = 80;
const BOARD_COLS = 60;
const ROWS = 25;
const N = BOARD_COLS * ROWS; // 1500

// boardCellIndices covers exactly the 60x25 viewport, row-major, no sidebar.
{
  const idx = boardCellIndices(COLS, BOARD_COLS, ROWS);
  assert.equal(idx.length, N);
  assert.equal(new Set(idx).size, N, "indices must be unique");
  for (const i of idx) {
    const x = i % COLS;
    assert.ok(x < BOARD_COLS, `cell x=${x} must be inside the board`);
  }
  // Row-major order: first cell is (0,0), last is (59,24).
  assert.equal(idx[0], 0);
  assert.equal(idx[N - 1], 24 * COLS + 59);
}

// shuffle is a permutation (same multiset) and does not mutate its input.
{
  const base = boardCellIndices(COLS, BOARD_COLS, ROWS);
  let seed = 12345;
  const rng = () => {
    // deterministic LCG so the test never flakes
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  const shuffled = shuffle(base, rng);
  assert.equal(shuffled.length, base.length);
  assert.deepEqual([...shuffled].sort((a, b) => a - b), [...base].sort((a, b) => a - b));
  // input untouched
  assert.equal(base[0], 0);
  assert.equal(base[N - 1], 24 * COLS + 59);
}

// The fade constants match TransitionDrawToFill('\xdb', 0x05).
{
  assert.equal(TRANSITION_FILL_CH, 0xdb);
  assert.equal(TRANSITION_FILL_COLOR, 0x05);
}

// Full run of the fill/reveal decision: at the start every board cell is the old
// board; at the end of fill every cell is purple; at the end of reveal every
// cell is the new board — the complete-reveal invariant (nothing left purple or
// stale).
{
  const order = boardCellIndices(COLS, BOARD_COLS, ROWS); // identity order is fine here
  const t = createTransition(order);
  const total = t.total;

  // step 0 — nothing touched yet.
  t.step = 0;
  for (const idx of order) {
    assert.equal(cellSource(t.orderPos.get(idx), t.step, total), "old");
  }

  // end of fill — whole viewport is purple.
  t.step = total;
  for (const idx of order) {
    assert.equal(cellSource(t.orderPos.get(idx), t.step, total), "purple");
  }

  // end of reveal — whole viewport is the new board, none left purple/old.
  t.step = transitionSteps(total);
  let purpleOrOld = 0;
  for (const idx of order) {
    const src = cellSource(t.orderPos.get(idx), t.step, total);
    if (src !== "new") purpleOrOld += 1;
  }
  assert.equal(purpleOrOld, 0, "every board cell must end revealed");
  assert.ok(isComplete(t));
}

// The SAME order drives fill and reveal: a cell filled at fill-position k is
// revealed at reveal-position k. Check the first cell in order is the first to
// go purple and the first to be revealed.
{
  const order = shuffle(boardCellIndices(COLS, BOARD_COLS, ROWS), () => 0.5);
  const t = createTransition(order);
  const total = t.total;
  const first = order[0];
  const last = order[total - 1];

  // one fill step: only the first-order cell is purple; the last is still old.
  t.step = 1;
  assert.equal(cellSource(t.orderPos.get(first), t.step, total), "purple");
  assert.equal(cellSource(t.orderPos.get(last), t.step, total), "old");

  // one reveal step (total + 1): only the first-order cell is new; last purple.
  t.step = total + 1;
  assert.equal(cellSource(t.orderPos.get(first), t.step, total), "new");
  assert.equal(cellSource(t.orderPos.get(last), t.step, total), "purple");
}

// A cell not in the order (e.g. a sidebar index) is never masked.
{
  assert.equal(cellSource(undefined, 0, N), "new");
  assert.equal(cellSource(undefined, N, N), "new");
}

// isComplete only trips at 2*total.
{
  const t = createTransition([0, 1, 2]);
  t.step = 5;
  assert.equal(isComplete(t), false);
  t.step = 6;
  assert.equal(isComplete(t), true);
}

console.log("transition.test.mjs: all assertions passed");
