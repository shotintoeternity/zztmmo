import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({ entryPoints: ["src/view3d/text_runs.ts"], bundle: true, format: "esm", platform: "node", write: false });
const { boardText, groupSigns, signDistance, signInRange } = await import(`data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`);

const COLS = 80, BOARD = 60, ROWS = 25;
const grid = () => Array.from({ length: COLS * ROWS }, () => ({ ch: 0x20, color: 0x0f }));
const put = (cells, x, y, text, color) => { for (let i = 0; i < text.length; i++) cells[y * COLS + x + i] = { ch: text.charCodeAt(i), color }; };

{
  const cells = grid();
  put(cells, 45, 20, "Palace ->", 0x1f); // blue text element: white on blue
  put(cells, 4, 22, "Copyright 1991", 0x6f); // brown text
  put(cells, 10, 5, "###", 0x0e); // a yellow wall is not text
  put(cells, 30, 12, "\x02", 0x1f); // the player is white on blue but not printable
  put(cells, 40, 12, "O", 0x0e); // a centipede segment has no background
  const { signs, message } = boardText(cells, COLS, BOARD, ROWS);
  assert.deepEqual(signs.map((s) => s.text), ["Palace ->", "Copyright 1991"]);
  assert.equal(signs[0].color, 0x1f);
  assert.equal(signs[0].x, 45);
  assert.equal(signs[0].cells.length, 9);
  assert.equal(message, null);
}

{
  // The board message: " You need a key! " centered on row 24, color 9..15 on black.
  const cells = grid();
  const text = " You need a key! ";
  put(cells, Math.floor((60 - text.length) / 2), 24, text, 0x0c);
  put(cells, 0, 24, "####", 0x0e); // a wall on the same row, no letters
  const { message } = boardText(cells, COLS, BOARD, ROWS);
  assert.equal(message.text, text);
  assert.equal(message.color, 0x0c);
  assert.equal(message.y, 24);
}

{
  // Town's bank: three words written downwards in adjacent columns, and a
  // three-line sign. Each becomes one group; the closer one sorts first.
  const cells = grid();
  // Each letter is padded with a space either side, as Town's are.
  put(cells, 10, 7, " B ", 0x2f); put(cells, 10, 8, " A ", 0x2f); put(cells, 10, 9, " N ", 0x2f); put(cells, 10, 10, " K ", 0x2f);
  put(cells, 14, 7, " O ", 0x2f); put(cells, 14, 8, " F ", 0x2f);
  put(cells, 18, 7, "Z", 0x2f); put(cells, 18, 8, "Z", 0x2f); put(cells, 18, 9, "T", 0x2f); put(cells, 18, 10, "!", 0x2f);
  put(cells, 30, 3, " X ", 0x2f); // a lone letter stays a one-letter sign
  put(cells, 40, 20, "The Town of ZZT", 0x6f);
  put(cells, 40, 21, "Copyright 1991", 0x6f);
  put(cells, 40, 22, "Epic MegaGames", 0x6f);
  const { signs } = boardText(cells, COLS, BOARD, ROWS);
  assert.deepEqual(signs.filter((s) => s.color === 0x2f).map((s) => s.text), [" X ", "BANK", "OF", "ZZT!"]);
  const groups = groupSigns(signs, COLS);
  assert.equal(groups.length, 3);
  const bank = groups.find((g) => g.lines[0] === "BANK OF ZZT!");
  assert.ok(bank, "the three downward words are one sign");
  assert.ok(groups.some((g) => g.lines[0] === "X"), "the lone letter is its own sign");
  const sign = groups.find((g) => g.color === 0x6f);
  assert.deepEqual(sign.lines, ["The Town of ZZT", "Copyright 1991", "Epic MegaGames"]);
  // The readout's gate. Distance is to the sign's nearest square, so standing
  // at the tail of a fifteen-column sign is standing at it.
  assert.equal(signDistance(sign, 40, 21, COLS), 0, "standing on the sign's first column");
  assert.equal(signDistance(sign, 54, 20, COLS), 0, "standing on the last column of its longest line");
  assert.equal(signDistance(sign, 56, 21, COLS), 3, "three columns past the end of that row's line");
  assert.equal(signDistance(sign, 45, 18, COLS), 4, "two rows above its top line, counted double");

  assert.equal(signInRange(groups, 45, 21, COLS, 8), sign, "standing in it");
  assert.equal(signInRange(groups, 58, 21, COLS, 8), sign, "five columns off its end");
  assert.equal(signInRange(groups, 45, 17, COLS, 8), sign, "three rows above it");
  assert.equal(signInRange(groups, 45, 15, COLS, 8), null, "five rows above it is too far to read");
  assert.equal(signInRange(groups, 30, 12, COLS, 8), null, "out in the open, nothing is read");
  assert.equal(signInRange(groups, 11, 9, COLS, 8), bank, "the sign you are inside wins");
  assert.equal(signInRange([], 45, 21, COLS, 8), null, "a board with no signs");
}
console.log("text_runs ok");
