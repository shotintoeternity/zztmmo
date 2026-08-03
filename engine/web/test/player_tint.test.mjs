import assert from "node:assert/strict";
import { build } from "esbuild";

// M19.1 — the tint rule, tested as pure logic under Node (the editor_cursor.ts
// pattern). The rule is the whole correctness of the task: a cell is tinted only
// where the roster places a player AND the cell the server drew there is still
// char 2 in attribute 0x1F. Everything below is a case where those two disagree.

const output = await build({
  entryPoints: ["src/player_tint.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { playerTintCells, playerTintForeground, isPlayerColor, PLAYER_TINT_CHAR, PLAYER_TINT_COLOR } = await import(
  `data:text/javascript;base64,${source}`
);

const BOARD_COLS = 60;
const COLS = 80;
const ROWS = 25;

/** An 80x25 screen of blank cells, as main.ts holds it. */
function blankScreen() {
  return Array.from({ length: COLS * ROWS }, (_, i) => ({
    x: i % COLS,
    y: Math.floor(i / COLS),
    ch: 32,
    color: 0x1f,
  }));
}

/** Draw what the SERVER draws for a player: board (x,y) is screen (x-1,y-1). */
function drawCell(cells, boardX, boardY, ch, color) {
  const cell = cells[(boardY - 1) * COLS + (boardX - 1)];
  cell.ch = ch;
  cell.color = color;
  return cell;
}

const tint = (roster, cells) => playerTintCells({ roster, cells, boardCols: BOARD_COLS });

// --- the happy path ---------------------------------------------------------

{
  const cells = blankScreen();
  drawCell(cells, 10, 12, PLAYER_TINT_CHAR, PLAYER_TINT_COLOR);
  drawCell(cells, 20, 6, PLAYER_TINT_CHAR, PLAYER_TINT_COLOR);
  const out = tint(
    [
      { id: 1, x: 10, y: 12, color: "#ff0000" },
      { id: 2, x: 20, y: 6, color: "#00ff00" },
    ],
    cells,
  );
  assert.deepEqual(
    out,
    [
      { x: 19, y: 5, rgb: "#00ff00" },
      { x: 9, y: 11, rgb: "#ff0000" },
    ],
    "two players on one board each tint their own square, at screen x-1,y-1",
  );
}

// A player who has picked no color is the vanilla white-on-blue smiley: there
// is nothing to paint, and the roster row must not become a black square.
{
  const cells = blankScreen();
  drawCell(cells, 10, 12, PLAYER_TINT_CHAR, PLAYER_TINT_COLOR);
  assert.deepEqual(tint([{ id: 1, x: 10, y: 12 }], cells), [], "no color means no tint");
  assert.deepEqual(tint([{ id: 1, x: 10, y: 12, color: "" }], cells), [], "an empty color means no tint");
}

// --- the three yield cases, which are the point of deriving from the screen --

// 1. A DARK ROOM. The server sent something else for that square (vanilla draws
//    E_EMPTY there), so the player is invisible — and stays invisible, without
//    this module knowing anything about darkness or torches.
{
  const cells = blankScreen();
  drawCell(cells, 10, 12, 0xb0, 0x07); // what a dark board shows
  assert.deepEqual(
    tint([{ id: 1, x: 10, y: 12, color: "#ff0000" }], cells),
    [],
    "a player the server did not draw is not tinted: darkness wins",
  );
}

// 2. THE ENERGIZER BLINK. ElementPlayerTick writes 0x0F and then a cycling
//    attribute, so the char is right and the attribute is not. The blink must
//    win — it is how you can see you are invincible.
{
  const cells = blankScreen();
  for (const blinkColor of [0x0f, 0x2f, 0x3f, 0xef, PLAYER_TINT_COLOR]) {
    drawCell(cells, 10, 12, PLAYER_TINT_CHAR, blinkColor);
    const out = tint([{ id: 1, x: 10, y: 12, color: "#ff0000" }], cells);
    if (blinkColor === PLAYER_TINT_COLOR) {
      assert.equal(out.length, 1, "0x1F is the un-energized attribute and IS tinted");
    } else {
      assert.deepEqual(out, [], `the energizer blink attribute 0x${blinkColor.toString(16)} beats the tint`);
    }
  }
}

// 3. THE ROSTER IS AHEAD OF THE SCREEN. A dead player mid-respawn, or a roster
//    that arrived a tick before the cells did, claims a square that holds
//    something else. Painting it would put a floating colored block on the
//    board.
{
  const cells = blankScreen();
  drawCell(cells, 10, 12, 0x02, 0x1f);
  const out = tint([{ id: 1, x: 11, y: 12, color: "#ff0000" }], cells);
  assert.deepEqual(out, [], "a player absent from the cell the roster claims is not tinted");
}

// --- bounds and untrusted input ---------------------------------------------

// The sidebar is never tinted, whatever a roster claims: only the board is.
{
  const cells = blankScreen();
  const sidebar = cells[11 * COLS + 65];
  sidebar.ch = PLAYER_TINT_CHAR;
  sidebar.color = PLAYER_TINT_COLOR;
  assert.deepEqual(tint([{ id: 1, x: 66, y: 12, color: "#ff0000" }], cells), [], "the sidebar is out of bounds");
}

// A roster is other players' input. Anything that is not a six-digit hex triple
// is dropped rather than handed to a canvas fillStyle.
{
  const cells = blankScreen();
  drawCell(cells, 10, 12, PLAYER_TINT_CHAR, PLAYER_TINT_COLOR);
  for (const bad of ["red", "#fff", "#12345g", "rgb(1,2,3)", "#1234567", "#123456; background: url(x)", null, 42]) {
    assert.deepEqual(
      tint([{ id: 1, x: 10, y: 12, color: bad }], cells),
      [],
      `a malformed color (${JSON.stringify(bad)}) never reaches the canvas`,
    );
  }
  assert.equal(isPlayerColor("#A1b2C3"), true, "either case is a color");
  assert.equal(isPlayerColor("#a1b2c"), false);
  assert.equal(isPlayerColor(undefined), false);
}

// Off-board coordinates (a 0 or a negative, which board coordinates are never)
// must not index before the start of the screen.
{
  const cells = blankScreen();
  assert.deepEqual(tint([{ id: 1, x: 0, y: 12, color: "#ff0000" }], cells), []);
  assert.deepEqual(tint([{ id: 1, x: 10, y: 0, color: "#ff0000" }], cells), []);
}

// --- the glyph's color ------------------------------------------------------

// The glyph is ALWAYS white, whatever it is standing on (owner decision
// 2026-08-03, reversing M19.1's auto-contrast). This is about identification
// rather than contrast: a white ☻ is the player and only the player, and a black
// one reads as some other element on boards that are full of dark-on-bright
// tiles. The background says WHICH player; the glyph says that it is a player.
for (const rgb of ["#000000", "#0000aa", "#ffffff", "#ffff00", "#ff0000", "#55ff55", "#7f3fbf"]) {
  assert.equal(playerTintForeground(rgb), 0x0f, `the smiley stays white on ${rgb}`);
}
assert.equal(playerTintForeground("garbage"), 0x0f, "and an unusable color is the vanilla white too");

console.log("player_tint tests passed");
