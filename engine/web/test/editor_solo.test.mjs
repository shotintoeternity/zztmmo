// M16.13 — the solo browser editor, end to end, and the files it produces.
//
// Driven by engine/m16_13_test.go (TestM1613BrowserEditorAndPortableOutput),
// which hosts the EDIT world (fixtures/editor.zwd) on the production server
// objects. Unlike M16.9/M16.10 there is no tick lock here and there are no
// ticks: an EditorSession is never stepped, which is exactly why the editor can
// never disturb a live room.
//
// TWO KINDS OF ASSERTION, ON PURPOSE.
//   * Chrome, dialogs and readouts are asserted on the DECODED CANVAS — that is
//     the only place a handler that never registered, or a modal that swallowed
//     a key, shows up.
//   * Every world change is asserted against the EDITOR SESSION ITSELF, through
//     the harness control listener (/control/editor/board). A client that drew a
//     convincing tile it never sent would satisfy the first check and fail this
//     one.
//
// The run has two acts. Act 1 exercises the editing vocabulary on the authored
// draft board. Act 2 presses N for a brand-new world and authors one from
// nothing, so the .ZZT this script downloads was made entirely of keystrokes in
// this browser rather than assembled by the test around it.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import {
  baseURL,
  cellAt,
  controlURL,
  gridToArt,
  hasText,
  idle,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  pauseClock,
  readGrid,
  saveText,
  serverState,
  textAt,
  waitForGrid,
  waitForQuiet,
} from "./lib/canvas.mjs";

const outDir = process.env.M1613_OUT || path.resolve("test-results/m16-13");
fs.mkdirSync(outDir, { recursive: true });

const BOARD_COLS = 60;
const BOARD_ROWS = 25;

// Where act 2 paints. Rows 5 and 7 are the walls that box the creature row in,
// so a world full of live creatures still survives the upload gate's 200
// unattended steps without reaching the player at the board's centre.
const ITEM_ROW = 3;
const CREATURE_ROW = 6;
const TERRAIN_ROW = 9;
const WALL_ROWS = [5, 7];
const SWEEP_X0 = 4;
const SWEEP_DX = 4;
const TEXT_ROW = 14;
const TEXT_X = 4;
const PATTERN_ROW = 17;
const PATTERN_X = 4;
const POINTER_ROW = 20;
const POINTER_X = 40;
const TYPED_TEXT = "M16.13 EDITOR";

const exercised = new Set();
const notes = [];
const files = {};
let exportedBoard = 0;

function record(...ids) {
  for (const id of ids) exercised.add(id);
}

function note(text) {
  notes.push(text);
  console.log(`  · ${text}`);
  // The Go test only sees this script's output when it exits, so a run this
  // long also writes its progress where it can be watched as it happens.
  fs.appendFileSync(path.join(outDir, "progress.log"), `${text}\n`);
}

// ---------------------------------------------------------------------------
// The editor session, read from the server
// ---------------------------------------------------------------------------

async function editorBoard() {
  const response = await fetch(`${controlURL}/control/editor/board`, { signal: AbortSignal.timeout(15000) });
  if (!response.ok) throw new Error(`/control/editor/board failed (${response.status})`);
  return response.json();
}

async function editorMenus() {
  const response = await fetch(`${controlURL}/control/editor/menus`, { signal: AbortSignal.timeout(15000) });
  if (!response.ok) throw new Error(`/control/editor/menus failed (${response.status})`);
  return response.json();
}

async function editorWorldBytes() {
  const response = await fetch(`${controlURL}/control/editor/world`, { signal: AbortSignal.timeout(20000) });
  if (!response.ok) throw new Error(`/control/editor/world failed (${response.status})`);
  const { data } = await response.json();
  return Buffer.from(data, "base64");
}

/** The element/colour the session holds at a 1-based board coordinate. */
function sessionTile(board, x, y) {
  const i = (y - 1) * BOARD_COLS + (x - 1);
  return {
    element: parseInt(board.elements.slice(i * 2, i * 2 + 2), 16),
    color: parseInt(board.colors.slice(i * 2, i * 2 + 2), 16),
  };
}

function sessionStatAt(board, x, y) {
  return board.stats.find((stat) => stat.x === x && stat.y === y) || null;
}

/** Poll the session until `pred(board)` holds. */
async function waitForSession(pred, describe, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  for (;;) {
    last = await editorBoard();
    if (pred(last)) return last;
    if (Date.now() > deadline) {
      saveText(`m1613-timeout-${describe.replace(/\W+/g, "-")}.json`, JSON.stringify(last, null, 2));
      throw new Error(`timed out waiting for ${describe} in the editor session`);
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
}

const waitForTile = (x, y, pred, describe) =>
  waitForSession((board) => pred(sessionTile(board, x, y)), describe);

// ---------------------------------------------------------------------------
// The editor sidebar, read from the canvas
// ---------------------------------------------------------------------------

// drawEditorSidebar writes "  ZZT Editor   " at (62,1) on every draw EXCEPT the
// stat parameter prompt, which clears all 25 rows first. That is the cheapest
// honest probe for "is a stat prompt open".
const isEditorChrome = (cells) => textAt(cells, 62, 1, 15) === "  ZZT Editor   ";
// The command block permanently carries a "W  Who's here" row, so the legend is
// detected by its own header banner at (62,3) — the row it clears and claims.
const presencePanelOpen = (cells) => textAt(cells, 62, 3, 15) === "  Who's here   ";
const statPromptOpen = (cells) => !isEditorChrome(cells);
const modeText = (cells) => textAt(cells, 68, 24, 11);
const colorText = (cells) => textAt(cells, 72, 19, 8).trim();
const elementText = (cells) => textAt(cells, 62, 23, 17).trim();
const readoutRow = (cells) => textAt(cells, 61, 20, 19).trim();
const boardCell = (cells, x, y) => cellAt(cells, x - 1, y - 1);

// Sidebar row 20 is " Pos: x,y" over a plain tile and " x,y Stat n: a/b/c" over
// one with a stat (editor.ts drawEditorSidebar), so the cursor readout is read
// through one regex that accepts both.
function cursorPos(cells) {
  const m = /^(?:Pos: )?(\d+),(\d+)/.exec(readoutRow(cells));
  return m ? [Number(m[1]), Number(m[2])] : null;
}

function cursorAt(cells, x, y) {
  const at = cursorPos(cells);
  return !!at && at[0] === x && at[1] === y;
}

// The F1/F2/F3 picker clears sidebar rows 3-20 and lists the category over the
// command block, so "B  Switch boards" (row 7) disappearing is the picker being
// open. The badge shapes alone would not do: the command block has " S ", " T ",
// " B ", " I " and " W " badges in the same column.
const pickerOpen = (cells) => isEditorChrome(cells) && !hasText(cells, "Switch boards");

/** The F1/F2/F3 picker as the sidebar actually draws it: shortcut -> name. */
function pickerRows(cells) {
  const rows = [];
  for (let row = 3; row <= 20; row += 1) {
    const badge = textAt(cells, 61, row, 3);
    if (badge[0] !== " " || badge[2] !== " " || badge[1] === " ") continue;
    rows.push({ shortcut: badge[1], name: textAt(cells, 65, row, 13).trim() });
  }
  return rows;
}

async function pressKey(page, code) {
  await page.keyboard.press(code);
}

/** The KeyboardEvent.code for an element's one-character editor shortcut. */
function shortcutCode(shortcut) {
  return /[0-9]/.test(shortcut) ? `Digit${shortcut}` : `Key${shortcut}`;
}

/** Move the cursor one cell and wait for the readout to catch up. */
async function moveCursor(page, code, x, y) {
  await pressKey(page, code);
  await waitForGrid(page, (cells) => cursorAt(cells, x, y), `the cursor at ${x},${y} after ${code}`);
}

// ---------------------------------------------------------------------------
// Modal helpers (the editor's dialogs are M4.1 text windows)
// ---------------------------------------------------------------------------

// A select list draws its entries as `!entry;entry` hyperlinks, whose caption
// starts at column 14 (textwindow.ts drawLine: TEXT_WINDOW_X + 4 + 5), and the
// selected line is always screen row 13 — the same probe M16.10 uses.
const WINDOW_CURSOR_ROW = 13;
// 14..50 is the caption region: the window's inner area is columns 7..51, and
// the selected line's own marker arrows (0xAF, 0xAE) sit on 7 and 51.
const windowCursorLine = (cells) => textAt(cells, 14, WINDOW_CURSOR_ROW, 37).trimEnd();

/**
 * Walk a select list to `label` and press Enter.
 *
 * The list is NOT searched for the label first: a text window shows only the
 * lines around its cursor, so an entry further down the list is genuinely not on
 * the screen yet (Board Information's eleven rows do not fit). What is always on
 * the screen is the selected line, so the walk compares that and steps.
 */
async function pickFromList(page, label, describe) {
  let cells = await readGrid(page);
  for (let i = 0; i < 80; i += 1) {
    if (windowCursorLine(cells) === label) {
      await pressKey(page, "Enter");
      return;
    }
    const was = windowCursorLine(cells);
    await pressKey(page, "ArrowDown");
    cells = await waitForGrid(
      page,
      (c) => windowCursorLine(c) !== was,
      `${describe}: the list cursor to move off ${JSON.stringify(was)} on its way to ${JSON.stringify(label)}`,
    );
  }
  saveText("m1613-list.txt", gridToArt(cells));
  throw new Error(`${describe}: never reached ${label}`);
}

/** Type into an entry prompt and submit it. */
async function typeEntry(page, text) {
  if (text.length > 0) await page.keyboard.type(text, { delay: 8 });
  await pressKey(page, "Enter");
}

/** Clear an entry prompt's pre-filled buffer, then type and submit. */
async function replaceEntry(page, text, prefillLength = 24) {
  for (let i = 0; i < prefillLength; i += 1) await pressKey(page, "Backspace");
  await typeEntry(page, text);
}

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
let failed = false;

try {
  await installImageProbe(page);
  await context.tracing.start({ screenshots: true, snapshots: true });
  page.on("response", async (response) => {
    if (response.status() >= 400) consoleErrors.push(`HTTP ${response.status()} ${response.url()}`);
  });

  // =========================================================================
  // Act 0 — reach the editor through the production launch flow
  // =========================================================================
  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served, not the build-me 404 page");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Edna");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("EDIT");
  await waitForGrid(page, (cells) => hasText(cells, "EDIT"), "the picker to match EDIT");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "E  Board editor"), "the title screen for EDIT");

  // From here the page clock is frozen: the editor's idle cursor blink is a
  // 3-phase interval, and a screen that blinks under the assertions would make
  // every canvas read a coin toss.
  await pauseClock(page);

  const roomBefore = await serverState();
  await pressKey(page, "KeyE");
  record("key.title.KeyE");
  await waitForGrid(page, (cells) => isEditorChrome(cells), "the editor sidebar");

  {
    const cells = await waitForGrid(page, (c) => cursorAt(c, 30, 12), "the editor cursor readout");
    assert.equal(modeText(cells), "Drawing off", "a fresh editor starts with draw mode off");
    assert.equal(colorText(cells), "Yellow", "the brush starts on yellow (0x0E)");
    assert.ok(hasText(cells, "S  World"), "the editor sidebar's world command");
    assert.ok(hasText(cells, "Switch boards"), "the editor sidebar's board command");
  }

  // The editor opened on the world's current board — an isolated COPY of it.
  // Nothing joined the room: an EditorSession is not a RoomManager room, and
  // that is the whole isolation claim.
  {
    const board = await editorBoard();
    assert.equal(board.properties.boardName, "Edit Draft", "the editor opened the world's current board");
    assert.equal(board.members, 1, "exactly this browser is in the session");
    assert.equal(sessionTile(board, 10, 5).element, 36, "the draft board's object is where the fixture put it");
    const roomAfter = await serverState();
    assert.equal(roomAfter.players.length, 0, "opening the editor must not join the live room");
    assert.deepEqual(roomAfter.hashes, roomBefore.hashes, "opening the editor must not disturb the live room");
    note("editor opened on an isolated copy of Edit Draft; the live room is untouched");
  }

  // =========================================================================
  // Act 1 — the vocabulary, on the authored draft board
  // =========================================================================

  // --- H: the editor help file -------------------------------------------
  await pressKey(page, "KeyH");
  record("key.editor.KeyH");
  await waitForGrid(page, (cells) => hasText(cells, "World editor help"), "EDITOR.HLP");
  await pressKey(page, "Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "World editor help"), "the help window to close");

  // --- W: the collaborator legend, and Escape closing it ------------------
  await pressKey(page, "KeyW");
  record("key.editor.KeyW");
  await waitForGrid(page, (cells) => presencePanelOpen(cells), "the collaborator legend");
  {
    const cells = await readGrid(page);
    assert.ok(hasText(cells, "(you)"), "the legend marks the viewer's own cursor");
  }
  await pressKey(page, "Escape");
  record("key.editor.Escape");
  await waitForGrid(page, (cells) => !presencePanelOpen(cells), "the legend to close");
  {
    const cells = await readGrid(page);
    assert.ok(isEditorChrome(cells), "Escape closed the legend rather than leaving the editor");
  }

  // --- cursor movement: arrows and the numeric keypad ---------------------
  await moveCursor(page, "ArrowUp", 30, 11);
  await moveCursor(page, "ArrowDown", 30, 12);
  await moveCursor(page, "ArrowLeft", 29, 12);
  await moveCursor(page, "ArrowRight", 30, 12);
  record("key.editor.ArrowUp", "key.editor.ArrowDown", "key.editor.ArrowLeft", "key.editor.ArrowRight");
  await moveCursor(page, "Numpad8", 30, 11);
  await moveCursor(page, "Numpad2", 30, 12);
  await moveCursor(page, "Numpad4", 29, 12);
  await moveCursor(page, "Numpad6", 30, 12);
  record("key.editor.Numpad8", "key.editor.Numpad2", "key.editor.Numpad4", "key.editor.Numpad6");

  // gotoCell is navigation, not evidence: it reads where the cursor actually is
  // (text entry, a drag and the draw-mode runs all move it too) and walks from
  // there. The movement KEYS are proved above by moveCursor, one canvas read per
  // press; doing that for every journey as well would spend most of the run
  // re-decoding a screen nothing is asserted against. The client's key handler
  // is synchronous, so when the last press resolves the cursor is already there.
  async function gotoCell(page, x, y) {
    const at = cursorPos(await readGrid(page));
    assert.ok(at, "the sidebar must show the cursor readout before a journey");
    let [cx, cy] = at;
    while (cx !== x || cy !== y) {
      if (cx < x) { await pressKey(page, "ArrowRight"); cx += 1; }
      else if (cx > x) { await pressKey(page, "ArrowLeft"); cx -= 1; }
      else if (cy < y) { await pressKey(page, "ArrowDown"); cy += 1; }
      else { await pressKey(page, "ArrowUp"); cy -= 1; }
    }
  }

  // gotoTile is gotoCell for the commands that READ the tile they land on —
  // Enter copies it into the brush, Enter over a stat opens its editor. The
  // readout is the session's own editorInspect reply, so waiting for it to name
  // the element is waiting for the client to have the authoritative tile rather
  // than the one it was standing on a moment ago.
  async function gotoTile(page, x, y, elementName) {
    await gotoCell(page, x, y);
    return waitForGrid(page, (c) => elementText(c) === elementName && cursorAt(c, x, y),
      `the readout to name the ${elementName} at ${x},${y}`);
  }

  // --- the readout names what is under the cursor -------------------------
  {
    const cells = await gotoTile(page, 14, 5, "Lion");
    assert.ok(readoutRow(cells).startsWith("14,5 Stat"), `the stat readout, got ${readoutRow(cells)}`);
  }

  // --- Enter on a plain tile copies it into the brush ----------------------
  await gotoTile(page, 12, 15, "Normal");
  await pressKey(page, "Enter");
  record("key.editor.Enter");
  await waitForGrid(page, (cells) => boardCell(cells, 68, 23).ch !== 0x20, "the copied-tile swatch");
  await gotoCell(page, 40, 15);
  await pressKey(page, "Space");
  record("key.editor.Space", "op.edit.place");
  await waitForTile(40, 15, (tile) => tile.element === 22 && tile.color === 0x4e,
    "the copied Normal wall to be placed at 40,15");
  note("Enter copied the coloured wall at 12,15 and Space stamped it at 40,15");

  // --- Delete / Backspace erase -------------------------------------------
  await pressKey(page, "Delete");
  record("key.editor.Delete", "op.edit.erase");
  await waitForTile(40, 15, (tile) => tile.element === 0, "40,15 to be erased by Delete");
  await pressKey(page, "Space");
  await waitForTile(40, 15, (tile) => tile.element === 22, "40,15 to be re-stamped");
  await pressKey(page, "Backspace");
  record("key.editor.Backspace");
  await waitForTile(40, 15, (tile) => tile.element === 0, "40,15 to be erased by Backspace");

  // --- P: the pattern brush, and C: the colour ----------------------------
  // The five patterns are Solid, Normal, Breakable, Empty and Line
  // (EDITOR.PAS:169-186). P leaves the copied tile behind and then cycles in
  // that order. All five are stamped on ONE cell, so each stamp is a real
  // change from the one before it — including Empty, which on a fresh cell
  // would have been indistinguishable from doing nothing at all.
  const PATTERNS = [21, 22, 23, 0, 31];
  await gotoCell(page, 44, 15);
  for (let i = 0; i < PATTERNS.length; i += 1) {
    await pressKey(page, "KeyP");
    await pressKey(page, "Space");
    await waitForTile(44, 15, (tile) => tile.element === PATTERNS[i],
      `pattern ${PATTERNS[i]} stamped at 44,15`);
  }
  record("key.editor.KeyP");
  note("the P brush cycled Solid/Normal/Breakable/Empty/Line and each stamped its own element");

  {
    const before = colorText(await readGrid(page));
    await pressKey(page, "KeyC");
    record("key.editor.KeyC");
    const cells = await waitForGrid(page, (c) => colorText(c) !== before, "the brush colour to change");
    note(`C moved the brush colour from ${before} to ${colorText(cells)}`);
  }

  // --- Tab: draw mode -----------------------------------------------------
  await gotoCell(page, 44, 18);
  await pressKey(page, "Tab");
  record("key.editor.Tab");
  await waitForGrid(page, (cells) => modeText(cells) === "Drawing on ", "draw mode on");
  for (let i = 0; i < 4; i += 1) await pressKey(page, "ArrowRight");
  await waitForTile(48, 18, (tile) => tile.element !== 0, "the last cell of the draw-mode run");
  {
    const board = await editorBoard();
    for (let x = 45; x <= 48; x += 1) {
      assert.notEqual(sessionTile(board, x, 18).element, 0, `draw mode should have painted ${x},18`);
    }
  }
  await pressKey(page, "Tab");
  await waitForGrid(page, (cells) => modeText(cells) === "Drawing off", "draw mode off");

  // --- Shift+arrow paints along the path ----------------------------------
  await gotoCell(page, 44, 21);
  await page.keyboard.down("ShiftLeft");
  for (let i = 0; i < 4; i += 1) await pressKey(page, "ArrowRight");
  await page.keyboard.up("ShiftLeft");
  await waitForTile(47, 21, (tile) => tile.element !== 0, "the last cell of the Shift+arrow line");
  {
    const board = await editorBoard();
    for (let x = 44; x <= 47; x += 1) {
      assert.notEqual(sessionTile(board, x, 21).element, 0, `Shift+Right should have painted ${x},21`);
    }
    assert.equal(sessionTile(board, 48, 21).element, 0, "Shift+Right places before it moves, so the last cell stays empty");
  }
  note("Shift+arrow painted 44..47,21 and left 48,21 empty — placement happens before the move");

  // --- X: the flood fill respects the pocket's walls ----------------------
  {
    const before = await editorBoard();
    await gotoCell(page, 12, 10);
    await pressKey(page, "KeyX");
    record("key.editor.KeyX", "op.edit.fill");
    const after = await waitForSession(
      (board) => sessionTile(board, 10, 9).element !== 0 && sessionTile(board, 15, 11).element !== 0,
      "the sealed pocket to fill",
    );
    let changed = 0;
    for (let y = 1; y <= BOARD_ROWS; y += 1) {
      for (let x = 1; x <= BOARD_COLS; x += 1) {
        const a = sessionTile(before, x, y);
        const b = sessionTile(after, x, y);
        if (a.element !== b.element || a.color !== b.color) {
          changed += 1;
          assert.ok(x >= 10 && x <= 15 && y >= 9 && y <= 11,
            `the fill escaped its pocket at ${x},${y}`);
        }
      }
    }
    assert.equal(changed, 18, "the fill should have taken exactly the 18 tiles inside the walls");
    note("X filled the 18-tile pocket and stopped at its walls");
  }

  // --- F4: text entry, ordering under a fast burst ------------------------
  await gotoCell(page, 30, 23);
  await pressKey(page, "F4");
  record("key.editor.F4");
  await waitForGrid(page, (cells) => modeText(cells) === "Text entry ", "text-entry mode");
  // No per-key delay: this is the DoD's "rapid input is ordered" clause on the
  // editor's most order-sensitive command. Every character is one websocket
  // message and one board write, and they must land left to right.
  await page.keyboard.type("ORDERED!", { delay: 0 });
  record("key.text.printable", "op.edit.text");
  await waitForTile(37, 23, (tile) => tile.element !== 0, "the last typed character");
  {
    const board = await editorBoard();
    const got = [];
    for (let i = 0; i < 8; i += 1) got.push(String.fromCharCode(sessionTile(board, 30 + i, 23).color));
    assert.equal(got.join(""), "ORDERED!", `rapid text entry landed out of order: ${got.join("")}`);
  }
  await pressKey(page, "Backspace");
  record("key.text.Backspace");
  await waitForTile(37, 23, (tile) => tile.element === 0, "Backspace to erase the last character");
  await pressKey(page, "Delete");
  record("key.text.Delete");
  await waitForTile(36, 23, (tile) => tile.element === 0, "Delete to erase in text mode too");
  await pressKey(page, "Escape");
  record("key.text.Escape");
  await waitForGrid(page, (cells) => modeText(cells) === "Drawing off", "Escape to leave text entry");
  await pressKey(page, "F4");
  await waitForGrid(page, (cells) => modeText(cells) === "Text entry ", "text-entry mode again");
  await pressKey(page, "Enter");
  record("key.text.Enter");
  await waitForGrid(page, (cells) => modeText(cells) === "Drawing off", "Enter to leave text entry");
  note("text entry typed ORDERED! in order and both erase keys walked back over it");

  // --- the mouse ----------------------------------------------------------
  {
    const rect = await page.evaluate(() => {
      const canvas = document.querySelector("canvas[data-screen]");
      const r = canvas.getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height };
    });
    const at = (x, y) => ({
      x: rect.x + ((x - 0.5) / 80) * rect.width,
      y: rect.y + ((y - 0.5) / 25) * rect.height,
    });
    const start = at(50, 23);
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    record("pointer.place");
    await waitForTile(50, 23, (tile) => tile.element !== 0, "the clicked cell to be placed");
    for (let x = 51; x <= 53; x += 1) {
      const p = at(x, 23);
      await page.mouse.move(p.x, p.y);
    }
    await page.mouse.up();
    record("pointer.drag");
    await waitForTile(53, 23, (tile) => tile.element !== 0, "the dragged cells to be placed");
    const board = await editorBoard();
    for (let x = 50; x <= 53; x += 1) {
      assert.notEqual(sessionTile(board, x, 23).element, 0, `the drag should have painted ${x},23`);
    }
    note("a click placed 50,23 and a drag carried the brush to 53,23");
  }

  // =========================================================================
  // Act 1b — stats, parameters and ZZT-OOP
  // =========================================================================

  // --- the Lion's slider: digits, arrows and Enter ------------------------
  await gotoTile(page, 14, 5, "Lion");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => statPromptOpen(cells), "the Lion's stat prompt");
  {
    const cells = await readGrid(page);
    assert.ok(hasText(cells, "Lion"), "the stat prompt names the element");
    assert.ok(hasText(cells, "Intelligence"), "the Lion's parameter prompt");
  }
  await pressKey(page, "Digit7");
  record("key.stat.digit", "op.stat.p1");
  await waitForSession((board) => sessionStatAt(board, 14, 5)?.p1 === 6, "the Lion's P1 to become 6 (slider 7)");
  await pressKey(page, "ArrowLeft");
  record("key.stat.ArrowLeft");
  await waitForSession((board) => sessionStatAt(board, 14, 5)?.p1 === 5, "the Lion's P1 to step down");
  await pressKey(page, "Numpad6");
  record("key.stat.Numpad6");
  await waitForSession((board) => sessionStatAt(board, 14, 5)?.p1 === 6, "the Lion's P1 to step back up with the keypad");
  await pressKey(page, "ArrowRight");
  record("key.stat.ArrowRight");
  await waitForSession((board) => sessionStatAt(board, 14, 5)?.p1 === 7, "the Lion's P1 to step up");
  await pressKey(page, "Numpad4");
  record("key.stat.Numpad4");
  await waitForSession((board) => sessionStatAt(board, 14, 5)?.p1 === 6, "the Lion's P1 to step down with the keypad");
  await pressKey(page, "Enter");
  record("key.stat.Enter");
  await waitForGrid(page, (cells) => !statPromptOpen(cells), "the Lion's prompt to finish");
  note("the Lion's Intelligence took a digit, both arrows and both keypad keys, and committed on Enter");

  // --- the Spinning Gun: two sliders and the Bullets/Stars choice ----------
  await gotoTile(page, 18, 5, "Spinning gun");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => statPromptOpen(cells), "the gun's stat prompt");
  await pressKey(page, "Digit5");
  await pressKey(page, "Enter");
  await waitForSession((board) => sessionStatAt(board, 18, 5)?.p1 === 4, "the gun's P1");
  await pressKey(page, "Digit9");
  record("op.stat.p2");
  await waitForSession((board) => (sessionStatAt(board, 18, 5)?.p2 & 0x7f) === 8, "the gun's P2");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Firing type"), "the Bullets/Stars choice");
  await pressKey(page, "ArrowRight");
  record("op.stat.bulletType");
  await waitForSession((board) => (sessionStatAt(board, 18, 5)?.p2 & 0x80) !== 0, "the gun to fire stars");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => !statPromptOpen(cells), "the gun's prompt to finish");

  // --- the Duplicator's direction choice ----------------------------------
  await gotoTile(page, 26, 5, "Duplicator");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => statPromptOpen(cells), "the duplicator's stat prompt");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Source direction"), "the direction choice");
  // The fixture's duplicator has no step at all, and statDirection reads (0,0)
  // as east — the last of the four — so LEFT is the press that moves the choice.
  // Right would leave the selection where it was and send nothing, which is a
  // passing-looking way to test nothing.
  await pressKey(page, "ArrowLeft");
  record("op.stat.direction");
  await waitForSession((board) => {
    const stat = sessionStatAt(board, 26, 5);
    return stat && stat.stepX === -1 && stat.stepY === 0;
  }, "the duplicator to take the west source direction");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => !statPromptOpen(cells), "the duplicator's prompt to finish");

  // --- the Passage's board parameter --------------------------------------
  await gotoTile(page, 22, 5, "Passage");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Room thru passage"), "the passage's board picker");
  await pickFromList(page, "1: Edit Annex", "the passage board picker");
  record("op.stat.p3");
  await waitForSession((board) => sessionStatAt(board, 22, 5)?.p3 === 1, "the passage to point at the annex");
  await waitForGrid(page, (cells) => isEditorChrome(cells), "the board picker to close");

  // --- Escape abandons a stat prompt --------------------------------------
  await gotoTile(page, 14, 5, "Lion");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => statPromptOpen(cells), "a stat prompt to escape from");
  await pressKey(page, "Escape");
  record("key.stat.Escape");
  await waitForGrid(page, (cells) => isEditorChrome(cells), "Escape to abandon the stat prompt");

  // --- the Object: a character parameter, then its ZZT-OOP program --------
  await gotoTile(page, 10, 5, "Object");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => statPromptOpen(cells), "the object's stat prompt");
  {
    const cells = await readGrid(page);
    assert.ok(hasText(cells, "Character"), "the object's character parameter");
  }
  const objectP1 = (await editorBoard()).stats.find((stat) => stat.x === 10 && stat.y === 5).p1;
  await pressKey(page, "Tab");
  record("key.stat.Tab");
  await waitForSession((board) => sessionStatAt(board, 10, 5)?.p1 === (objectP1 + 9) % 256,
    "Tab to step the object's character by nine");
  await pressKey(page, "ArrowRight");
  await waitForSession((board) => sessionStatAt(board, 10, 5)?.p1 === (objectP1 + 10) % 256,
    "the character to step by one");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Edit Program"), "the ZZT-OOP program editor");
  record("op.program.read");
  {
    const cells = await readGrid(page);
    assert.ok(hasText(cells, "@greeter"), "the program editor shows the object's own program");
  }
  // Edit a line and save. The code editor is TextWindowEdit's vocabulary
  // (modal.ts programEditorKey): Down clamps at the last line, typing inserts at
  // the cursor, and Escape is what saves. The marker is typed rather than a line
  // appended, so the assertion does not depend on where the cursor came to rest
  // — and the rest of the program has to survive it.
  for (let i = 0; i < 8; i += 1) await pressKey(page, "ArrowDown");
  await page.keyboard.type("EDITED-", { delay: 8 });
  await pressKey(page, "Escape");
  record("op.program.save");
  const savedProgram = await waitForSession(
    (board) => (sessionStatAt(board, 10, 5)?.program || "").includes("EDITED-"),
    "the edited program to be saved back to the object",
  );
  {
    const program = sessionStatAt(savedProgram, 10, 5).program;
    assert.ok(program.includes("@greeter"), `the object's name line survived the edit: ${JSON.stringify(program)}`);
    assert.ok(program.includes(":touch"), `the object's label survived the edit: ${JSON.stringify(program)}`);
    note(`the object's program is now ${JSON.stringify(program)}`);
  }
  await waitForGrid(page, (cells) => isEditorChrome(cells), "the program editor to close");
  note("the object's program was read, extended and written back through the session");

  // =========================================================================
  // Act 1c — board and world dialogs
  // =========================================================================

  // --- I: Board Information, every row ------------------------------------
  async function boardInfo(page, label, describe) {
    await pressKey(page, "KeyI");
    await waitForGrid(page, (cells) => hasText(cells, "Board Information"), "Board Information");
    await pickFromList(page, label, describe);
  }

  await boardInfo(page, "Title: Edit Draft", "the board title");
  record("key.editor.KeyI");
  await waitForGrid(page, (cells) => hasText(cells, "New title for board"), "the board title prompt");
  await replaceEntry(page, "DRAFTED");
  record("op.property.boardTitle");
  await waitForSession((board) => board.properties.boardName === "DRAFTED", "the board to be renamed");

  await boardInfo(page, "Can fire: 255 shots.", "the shot limit");
  await waitForGrid(page, (cells) => hasText(cells, "Maximum shots"), "the shot prompt");
  await replaceEntry(page, "7");
  record("op.property.maxShots");
  await waitForSession((board) => board.properties.maxShots === 7, "the shot limit to become 7");

  // M16.13a (a): the session installs InitElementsEditor's element table
  // (editor.go:513), whose ForceDarknessOff keeps a dark board drawn cell for
  // cell — otherwise turning darkness on blacks out the 1500 cells the author
  // is trying to edit. TileToColorAndChar's darkness branch is exactly
  // {ch:0xB0, color:0x07}, and the lit board has none of those, so counting them
  // discriminates cleanly.
  const darkenedCells = (cells) => {
    let n = 0;
    for (let row = 0; row < BOARD_ROWS; row += 1) {
      for (let col = 0; col < BOARD_COLS; col += 1) {
        const cell = cellAt(cells, col, row);
        if (cell.ch === 0xb0 && cell.color === 0x07) n += 1;
      }
    }
    return n;
  };
  assert.equal(darkenedCells(await readGrid(page)), 0,
    "the lit draft board draws no darkness cells, which is what makes the count below a probe");
  await boardInfo(page, "Board is dark: No", "the darkness toggle");
  record("op.property.dark");
  await waitForSession((board) => board.properties.isDark === true, "the board to become dark");
  assert.equal(darkenedCells(await readGrid(page)), 0,
    "a dark board must still be EDITED LIT: InitElementsEditor's ForceDarknessOff keeps every cell " +
    "drawn, and without it the editor covers the board it is editing in darkness");
  note('turning "Board is dark" on left every cell drawn: a dark board is edited lit');

  await boardInfo(page, "Re-enter when zapped: No", "the re-enter toggle");
  record("op.property.reenter");
  await waitForSession((board) => board.properties.reenterWhenZapped === true, "re-enter when zapped");

  await boardInfo(page, "Time limit, 0=None: 0 sec.", "the time limit");
  await waitForGrid(page, (cells) => hasText(cells, "Time limit"), "the time-limit prompt");
  await replaceEntry(page, "42");
  record("op.property.timeLimit");
  await waitForSession((board) => board.properties.timeLimitSec === 42, "the time limit to become 42");

  // The four board-edge rows are labelled with the CP437 arrows main.ts writes
  // into them (openEditorBoardInfo), which is what the canvas decodes back to.
  for (const [exit, arrow] of [[0, "\u0018"], [1, "\u0019"], [2, "\u001b"], [3, "\u001a"]]) {
    await boardInfo(page, `Board ${arrow}: None`, `exit ${exit}`);
    await waitForGrid(page, (cells) => hasText(cells, "Select Board"), `the exit ${exit} picker`);
    await pickFromList(page, "1: Edit Annex", `exit ${exit} target`);
    await waitForSession((board) => board.properties.neighborBoards[exit] === 1, `exit ${exit} to point at the annex`);
  }
  record("op.property.exit");

  await boardInfo(page, "World name: EDIT", "the world name");
  await waitForGrid(page, (cells) => hasText(cells, "World name:"), "the world-name prompt");
  await replaceEntry(page, "DRAFTW");
  record("op.property.worldName");
  await waitForSession((board) => board.properties.worldName === "DRAFTW", "the world to be renamed");
  note("Board Information changed the title, shot limit, darkness, re-entry, time limit, all four exits and the world name");

  // --- B: switch boards, and add one --------------------------------------
  await pressKey(page, "KeyB");
  record("key.editor.KeyB");
  await waitForGrid(page, (cells) => hasText(cells, "Switch boards"), "the board switcher");
  await pickFromList(page, "1: Edit Annex", "the annex");
  record("op.board.switch");
  await waitForSession((board) => board.properties.boardName === "Edit Annex", "the switch to the annex");

  // --- Z: clear the board (on the annex, so nothing later reads it) -------
  await pressKey(page, "KeyZ");
  record("key.editor.KeyZ");
  await waitForGrid(page, (cells) => hasText(cells, "Clear board?"), "the clear-board prompt");
  await pressKey(page, "KeyY");
  record("op.board.clear");
  await waitForSession((board) => sessionTile(board, 28, 12).element === 0, "the annex to be cleared");
  {
    const board = await editorBoard();
    assert.equal(board.statCount, 0, "a cleared board keeps only the player stat");
    assert.equal(sessionTile(board, 30, 12).element, 4, "a cleared board puts the player back at its centre");
  }

  // --- B again: back to board 0, which is a board like any other ----------
  // "Switch boards" is EditorSelectBoard with titleScreenIsNone FALSE
  // (editor.go:668-669): the world's first board is listed under its own name
  // and selects like the rest. The browser used to call it "None" and then drop
  // it from the list, so an author who moved off it was stranded (M16.13a).
  await pressKey(page, "KeyB");
  await waitForGrid(page, (cells) => hasText(cells, "Switch boards"), "the board switcher");
  await pickFromList(page, "0: DRAFTED", "the title board");
  await waitForSession((board) => board.properties.boardName === "DRAFTED",
    "the switch back to the world's first board");
  note('"Switch boards" listed board 0 under its own name and switched back to it');

  // =========================================================================
  // Act 2 — a new world, authored from nothing
  // =========================================================================
  await pressKey(page, "KeyN");
  record("key.editor.KeyN");
  await waitForGrid(page, (cells) => hasText(cells, "Make new world?"), "the new-world prompt");
  await pressKey(page, "KeyY");
  record("op.board.new");
  await waitForSession((board) => board.boardCount === 0 && board.properties.worldName === "",
    "the session to reset to a new world");
  note("N reset the session to a one-board world; everything below is authored from nothing");

  const menus = await editorMenus();
  const byKey = Object.fromEntries(menus.map((menu) => [menu.key, menu]));

  // selectPattern arms the P brush on a known pattern by stamping a scratch cell
  // and cycling until the session says that cell holds the element wanted. The
  // brush is client-side state that Act 1 left somewhere in the middle of its
  // five-pattern cycle; counting presses from there would be a guess, and a wall
  // of the wrong tile would still look like a wall.
  const SCRATCH = { x: 58, y: 23 };
  async function selectPattern(page, element) {
    for (let i = 0; i < PATTERNS.length + 1; i += 1) {
      await gotoCell(page, SCRATCH.x, SCRATCH.y);
      await pressKey(page, "Space");
      await new Promise((resolve) => setTimeout(resolve, 60));
      if (sessionTile(await editorBoard(), SCRATCH.x, SCRATCH.y).element === element) {
        await pressKey(page, "Delete");
        await waitForTile(SCRATCH.x, SCRATCH.y, (tile) => tile.element === 0, "the scratch cell to be cleared");
        return;
      }
      await pressKey(page, "KeyP");
    }
    throw new Error(`the P brush never reached element ${element}`);
  }

  // --- the two walls that box the creature row in -------------------------
  await selectPattern(page, 21);
  // Held-key repeat: Playwright marks the second and later keydowns of a held
  // key as auto-repeat, which is exactly what the browser sends when a person
  // leans on Shift+Right. Nothing is awaited between them, so this is also the
  // DoD's "held input is ordered" clause: 57 places and 57 moves, interleaved,
  // must produce one unbroken run.
  for (const row of WALL_ROWS) {
    await gotoCell(page, 2, row);
    await page.keyboard.down("ShiftLeft");
    await page.keyboard.down("ArrowRight");
    for (let i = 0; i < 56; i += 1) await page.keyboard.down("ArrowRight");
    await page.keyboard.up("ArrowRight");
    await page.keyboard.up("ShiftLeft");
    await waitForTile(58, row, (tile) => tile.element === 21, `the wall on row ${row} to reach x=58`);
    const board = await editorBoard();
    for (let x = 2; x <= 58; x += 1) {
      assert.equal(sessionTile(board, x, row).element, 21,
        `the held Shift+Right run broke at ${x},${row}`);
    }
    assert.equal(sessionTile(board, 59, row).element, 0,
      `the run overshot: placement happens before the move, so ${59},${row} must stay empty`);
  }
  note("two 57-tile walls were drawn with held Shift+Right, with no gap and no double-place");

  // --- every placeable element, from the F1/F2/F3 pickers ------------------
  const placed = [];
  for (const [key, row] of [["f1", ITEM_ROW], ["f2", CREATURE_ROW], ["f3", TERRAIN_ROW]]) {
    const menu = byKey[key];
    assert.ok(menu && menu.items.length > 0, `the ${key} menu should have items`);
    const fkey = key.toUpperCase();

    await pressKey(page, fkey);
    record(`key.editor.${fkey}`);
    const cells = await waitForGrid(page, (c) => pickerOpen(c) && pickerRows(c).length >= menu.items.length,
      `the ${fkey} element picker`);
    const drawn = pickerRows(cells);
    assert.deepEqual(
      drawn.map((entry) => entry.shortcut),
      menu.items.map((item) => item.shortcut),
      `the ${fkey} picker draws the shortcuts ElementDefs defines, in order`,
    );
    for (let i = 0; i < menu.items.length; i += 1) {
      assert.equal(drawn[i].name, menu.items[i].name.slice(0, 13),
        `the ${fkey} picker names item ${i} as the engine does`);
    }
    // Escape closes the picker without placing anything.
    await pressKey(page, "Escape");
    record("key.category.Escape");
    await waitForGrid(page, (c) => !pickerOpen(c), `the ${fkey} picker to close on Escape`);

    for (let i = 0; i < menu.items.length; i += 1) {
      const item = menu.items[i];
      const x = SWEEP_X0 + i * SWEEP_DX;
      await gotoCell(page, x, row);
      await pressKey(page, fkey);
      await waitForGrid(page, (c) => pickerOpen(c) && pickerRows(c).length >= menu.items.length, `the ${fkey} picker`);
      await pressKey(page, shortcutCode(item.shortcut));
      await waitForTile(x, row, (tile) => tile.element === item.elementId,
        `${item.name} at ${x},${row}`);
      placed.push({ ...item, x, y: row });
      // Vanilla runs EditorEditStat straight after AddStat, so a stat-backed
      // element arrives with its first parameter dialog already open. Which
      // dialog that is comes from the element's own parameter names, which is
      // why the harness sends it along with the menu: waiting for the right one
      // and dismissing it is the difference between a test and a race.
      switch (item.firstPrompt) {
        case "sidebar":
          await waitForGrid(page, (c) => statPromptOpen(c), `${item.name}'s parameter prompt`);
          await pressKey(page, "Escape");
          await waitForGrid(page, (c) => isEditorChrome(c), `${item.name}'s prompt to close`);
          break;
        case "program":
          await waitForGrid(page, (c) => hasText(c, item.promptTitle), `${item.name}'s program editor`);
          await pressKey(page, "Escape");
          await waitForGrid(page, (c) => !hasText(c, item.promptTitle), `${item.name}'s program editor to close`);
          break;
        case "board":
          await waitForGrid(page, (c) => hasText(c, "Room thru passage"), `${item.name}'s board picker`);
          await pressKey(page, "Escape");
          await waitForGrid(page, (c) => !hasText(c, "Room thru passage"), `${item.name}'s board picker to close`);
          break;
        default:
          // No dialog to wait for — but a stat-backed element still runs the
          // lease round trip, and openEditorStatSettings clears the sidebar's
          // menus on its way to discovering there is nothing to edit. Pressing
          // the next F-key into that window opens a picker the late reply then
          // closes again, which is how this was found.
          if (item.statBacked) await page.waitForTimeout(200);
      }
    }
    record("key.category.shortcut", "op.edit.element");
    note(`${fkey} placed all ${menu.items.length} ${menu.title} elements on row ${row}`);
  }

  // A key that matches no element closes the picker and places nothing.
  {
    await gotoCell(page, 56, TERRAIN_ROW);
    await pressKey(page, "F3");
    await waitForGrid(page, (c) => pickerOpen(c), "the F3 picker");
    await pressKey(page, "Slash");
    record("key.category.nomatch");
    await waitForGrid(page, (c) => !pickerOpen(c), "the picker to close on a non-matching key");
    const board = await editorBoard();
    assert.equal(sessionTile(board, 56, TERRAIN_ROW).element, 0,
      "a non-matching key must place nothing");
  }

  // --- the placements are the engine's, parameters and all ----------------
  {
    const board = await editorBoard();
    for (const item of placed) {
      const tile = sessionTile(board, item.x, item.y);
      assert.equal(tile.element, item.elementId, `${item.name} should be at ${item.x},${item.y}`);
      const stat = sessionStatAt(board, item.x, item.y);
      if (stat) {
        assert.equal(stat.element, item.elementId, `${item.name}'s stat should sit on its own tile`);
      }
    }
    note(`${placed.length} elements placed and verified against the session`);
  }

  // --- some authored text and a pattern row -------------------------------
  await gotoCell(page, TEXT_X, TEXT_ROW);
  await pressKey(page, "F4");
  await waitForGrid(page, (cells) => modeText(cells) === "Text entry ", "text-entry mode");
  await page.keyboard.type(TYPED_TEXT, { delay: 0 });
  await waitForTile(TEXT_X + TYPED_TEXT.length - 1, TEXT_ROW, (tile) => tile.element !== 0,
    "the authored caption");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => modeText(cells) === "Drawing off", "text entry to close");

  // P before Space: the brush is still the last menu element, which is not one
  // of the five patterns, so plotting it first would place nothing at all. Which
  // pattern each press lands on depends on where the cycle already was, so the
  // claim is the cycle itself: five presses visit all five patterns.
  const stamped = [];
  for (let i = 0; i < PATTERNS.length; i += 1) {
    const x = PATTERN_X + i * 2;
    await gotoCell(page, x, PATTERN_ROW);
    await pressKey(page, "KeyP");
    await pressKey(page, "Space");
    await new Promise((resolve) => setTimeout(resolve, 80));
    stamped.push(sessionTile(await editorBoard(), x, PATTERN_ROW).element);
  }
  assert.deepEqual([...stamped].sort((a, b) => a - b), [...PATTERNS].sort((a, b) => a - b),
    `five presses of P should have stamped all five patterns, got ${stamped}`);

  {
    const rect = await page.evaluate(() => {
      const canvas = document.querySelector("canvas[data-screen]");
      const r = canvas.getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height };
    });
    const at = (x, y) => ({
      x: rect.x + ((x - 0.5) / 80) * rect.width,
      y: rect.y + ((y - 0.5) / 25) * rect.height,
    });
    const start = at(POINTER_X, POINTER_ROW);
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    for (let x = POINTER_X + 1; x <= POINTER_X + 3; x += 1) {
      const p = at(x, POINTER_ROW);
      await page.mouse.move(p.x, p.y);
    }
    await page.mouse.up();
    await waitForTile(POINTER_X + 3, POINTER_ROW, (tile) => tile.element !== 0, "the authored drag");
  }

  // --- name the board and the world ---------------------------------------
  // The row captions carry the CURRENT names, which for a brand-new world are
  // whatever WorldCreate left behind — read them rather than assume.
  const fresh = (await editorBoard()).properties;
  await pressKey(page, "KeyI");
  await waitForGrid(page, (cells) => hasText(cells, "Board Information"), "Board Information");
  await pickFromList(page, `Title: ${fresh.boardName || "Untitled"}`, "the new board's title");
  await waitForGrid(page, (cells) => hasText(cells, "New title for board"), "the title prompt");
  await replaceEntry(page, "M16.13 Editor Proof");
  await waitForSession((board) => board.properties.boardName === "M16.13 Editor Proof", "the board title");

  await pressKey(page, "KeyI");
  await waitForGrid(page, (cells) => hasText(cells, "Board Information"), "Board Information");
  await pickFromList(page, `World name: ${fresh.worldName || "Untitled"}`, "the new world's name");
  await waitForGrid(page, (cells) => hasText(cells, "World name:"), "the world-name prompt");
  await replaceEntry(page, "ORCLEDIT");
  await waitForSession((board) => board.properties.worldName === "ORCLEDIT", "the world name");

  // --- T: export the authored board as .BRD, then import it back ----------
  // Board 0 is where everything above was authored, and it is where the run
  // stays — this world has no other board yet.
  await pressKey(page, "KeyT");
  record("key.editor.KeyT");
  await waitForGrid(page, (cells) => hasText(cells, "Transfer board:"), "the transfer menu");
  // The sidebar action menu's own key vocabulary, before anything is picked.
  for (const [code, id] of [["ArrowDown", "key.menu.ArrowDown"], ["ArrowUp", "key.menu.ArrowUp"],
    ["Numpad2", "key.menu.Numpad2"], ["Numpad8", "key.menu.Numpad8"],
    ["End", "key.menu.End"], ["Home", "key.menu.Home"]]) {
    await pressKey(page, code);
    record(id);
  }
  await pressKey(page, "Escape");
  record("key.menu.Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Transfer board:"), "the transfer menu to close");

  // Enter picks the selected item. Export is the harmless one to pick twice —
  // it only serialises the board — and this download is discarded, because the
  // committed one comes from the shortcut path below.
  await pressKey(page, "KeyT");
  await waitForGrid(page, (cells) => hasText(cells, "Transfer board:"), "the transfer menu");
  await pressKey(page, "ArrowDown");
  const [enterDownload] = await Promise.all([
    page.waitForEvent("download", { timeout: 20000 }),
    (async () => {
      await pressKey(page, "Enter");
      record("key.menu.Enter");
    })(),
  ]);
  await enterDownload.saveAs(path.join(outDir, "enter-picked.BRD"));
  await waitForGrid(page, (cells) => !hasText(cells, "Transfer board:"), "the transfer menu to close after Enter");

  await pressKey(page, "KeyT");
  await waitForGrid(page, (cells) => hasText(cells, "Transfer board:"), "the transfer menu");
  const [brdDownload] = await Promise.all([
    page.waitForEvent("download", { timeout: 20000 }),
    (async () => {
      await pressKey(page, "KeyE"); // the Export shortcut
      record("key.menu.shortcut", "op.board.export");
    })(),
  ]);
  files.board = "exported.BRD";
  exportedBoard = 0;
  await brdDownload.saveAs(path.join(outDir, files.board));
  note(`exported board ${exportedBoard} as ${brdDownload.suggestedFilename()}`);

  // Import it back onto the same board: the round trip must leave every tile
  // where it was. (ImportBoard also clears the four edge exits, which is why
  // this board has none to lose — EDITOR.PAS:534 does the same, because a .BRD
  // names boards that need not exist in the receiving world.)
  {
    const before = await editorBoard();
    await pressKey(page, "KeyT");
    await waitForGrid(page, (cells) => hasText(cells, "Transfer board:"), "the transfer menu");
    await pressKey(page, "ArrowDown");
    await pressKey(page, "ArrowUp");
    const [chooser] = await Promise.all([
      page.waitForEvent("filechooser", { timeout: 20000 }),
      (async () => {
        await pressKey(page, "Space"); // pick "Import board" with Space
        record("key.menu.Space", "op.board.import");
      })(),
    ]);
    await chooser.setFiles(path.join(outDir, files.board));
    await waitForSession((board) => board.properties.boardName === "M16.13 Editor Proof",
      "the imported board");
    const after = await editorBoard();
    assert.equal(after.elements, before.elements, "importing a board's own .BRD must not change its tiles");
    assert.equal(after.colors, before.colors, "importing a board's own .BRD must not change its colours");
    note("the exported .BRD imported back onto its own board without changing a tile");
  }

  // =========================================================================
  // Act 3 — the files, the gate, and test play
  // =========================================================================

  // The session's own serialization, taken at the same moment as the download,
  // so engine/m16_13_test.go can require the browser's file to BE those bytes.
  const authorityBefore = await editorWorldBytes();
  files.authority = "authority.ZZT";
  fs.writeFileSync(path.join(outDir, files.authority), authorityBefore);

  await pressKey(page, "KeyS");
  record("key.editor.KeyS");
  await waitForGrid(page, (cells) => hasText(cells, "World:"), "the world menu");
  const [zztDownload] = await Promise.all([
    page.waitForEvent("download", { timeout: 20000 }),
    (async () => {
      await pickFromList(page, "Download .ZZT", "the world download");
      record("op.world.download");
    })(),
  ]);
  files.world = "downloaded.ZZT";
  await zztDownload.saveAs(path.join(outDir, files.world));
  note(`downloaded ${zztDownload.suggestedFilename()} (${fs.statSync(path.join(outDir, files.world)).size} bytes)`);

  // --- add a board, then upload the download over the top of it -----------
  // Appending a board is the last thing that changes this world, and it happens
  // AFTER the download on purpose: the upload that follows replaces the session
  // with the downloaded bytes, so the added board is both exercised and undone,
  // and the world the rest of the run carries is exactly the file on disk.
  await pressKey(page, "KeyB");
  await waitForGrid(page, (cells) => hasText(cells, "Switch boards"), "the board switcher");
  await pickFromList(page, "Add new board", "adding a board");
  await waitForGrid(page, (cells) => hasText(cells, "Room's Title"), "the new board's name prompt");
  await typeEntry(page, "Editor Annex");
  record("op.board.add");
  await waitForSession((board) => board.boardCount === 1 && board.properties.boardName === "Editor Annex",
    "the appended board");

  // --- upload the file we just downloaded, through the M7.5 gate ----------
  {
    await pressKey(page, "KeyS");
    await waitForGrid(page, (cells) => hasText(cells, "World:"), "the world menu");
    const [chooser] = await Promise.all([
      page.waitForEvent("filechooser", { timeout: 20000 }),
      (async () => {
        await pickFromList(page, "Upload .ZZT", "the world upload");
        record("op.world.upload");
      })(),
    ]);
    await chooser.setFiles(path.join(outDir, files.world));
    await waitForSession((board) => board.properties.worldName === "ORCLEDIT" && board.boardCount === 0
      && board.properties.boardName === "M16.13 Editor Proof",
      "the uploaded world to replace the session");
    const roundTrip = await editorWorldBytes();
    assert.ok(roundTrip.equals(authorityBefore),
      "uploading the downloaded world must reproduce it byte for byte");
    note("the downloaded .ZZT passed the upload gate and reloaded into a byte-identical session");
  }

  // --- invite: the dialog exists and its outcome is reported --------------
  {
    await pressKey(page, "KeyS");
    await waitForGrid(page, (cells) => hasText(cells, "World:"), "the world menu");
    await pickFromList(page, "Invite collaborator", "the invite dialog");
    await waitForGrid(page, (cells) => hasText(cells, "Account id:"), "the account-id prompt");
    await typeEntry(page, "someone@example.com");
    record("op.world.invite");
    await waitForGrid(page, (cells) => hasText(cells, "Saved") || hasText(cells, "Cannot save"),
      "the invite outcome");
    const cells = await readGrid(page);
    note(`invite reported: ${hasText(cells, "Cannot save") ? "refused" : "accepted"}`);
    await pressKey(page, "Escape");
    await waitForGrid(page, (c) => isEditorChrome(c) && !hasText(c, "Account id:"), "the invite window to close");
  }

  // --- test play: the editing world must come back untouched --------------
  const beforeTestPlay = await editorWorldBytes();
  files.beforeTestPlay = "before-test-play.ZZT";
  fs.writeFileSync(path.join(outDir, files.beforeTestPlay), beforeTestPlay);

  await pressKey(page, "KeyS");
  await waitForGrid(page, (cells) => hasText(cells, "World:"), "the world menu");
  await pickFromList(page, "Test play together", "test play");
  record("op.testPlay");
  await waitForGrid(page, (cells) => hasText(cells, "Health:"), "the test-play board");
  {
    const cells = await readGrid(page);
    assert.ok(hasText(cells, TYPED_TEXT), "test play shows the caption this browser typed into the editor");
    note("test play joined an isolated instance of the authored world");
  }
  // Let the copy run: creatures move, the duplicator duplicates, the board
  // changes. None of it may reach the world still open in the editor.
  await idle(12);
  await page.keyboard.press("ArrowLeft");
  await idle(6);
  await waitForQuiet(page, 2000);

  // Back to the editor the long way round — the client left the session when it
  // went to play, so this is the production path a returning author walks.
  await page.reload();
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);
  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Edna");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("EDIT");
  await waitForGrid(page, (cells) => hasText(cells, "EDIT"), "the picker to match EDIT");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "E  Board editor"), "the title screen");
  await pauseClock(page);
  await pressKey(page, "KeyE");
  await waitForGrid(page, (cells) => isEditorChrome(cells), "the editor sidebar again");
  await waitForSession((board) => board.members === 1, "the session to have this browser again");

  const afterTestPlay = await editorWorldBytes();
  files.afterTestPlay = "after-test-play.ZZT";
  fs.writeFileSync(path.join(outDir, files.afterTestPlay), afterTestPlay);
  assert.ok(afterTestPlay.equals(beforeTestPlay),
    "test play changed the editing world; it must run on a copy");
  {
    const cells = await readGrid(page);
    assert.ok(hasText(cells, TYPED_TEXT), "the editor came back to the world it was left on");
  }
  note("test play left the editing world byte-identical");

  // --- save and publish ---------------------------------------------------
  await pressKey(page, "KeyS");
  await waitForGrid(page, (cells) => hasText(cells, "World:"), "the world menu");
  await pickFromList(page, "Save and publish", "publishing");
  await waitForGrid(page, (cells) => hasText(cells, "Save world as:"), "the publish prompt");
  // Published under the world's own name: GameWorldSave writes the name it is
  // given into World.Info, and the downloaded file above is being compared with
  // this session field for field — a rename here would be a difference the file
  // could not have known about.
  await replaceEntry(page, "ORCLEDIT");
  record("op.world.save");
  await waitForGrid(page, (cells) => hasText(cells, "World published as"), "the publish confirmation");
  await pressKey(page, "Escape");
  await waitForGrid(page, (cells) => isEditorChrome(cells), "the confirmation to close");

  // --- re-arm the modified flag, without moving a byte --------------------
  // The publish above cleared it, and the exit below needs it set. Vanilla
  // raises wasModified on ANY accepted Board Information line (editor.go:242),
  // so a toggle and its undo both count — and they leave the world exactly where
  // it was, which matters because the checks in engine/m16_13_test.go compare
  // the downloaded file against the session as it stands at the END of this run.
  // The second pick is also the proof the first reply landed: its row label is
  // built from the client's own copy of the properties.
  await boardInfo(page, "Re-enter when zapped: No", "the re-entry toggle");
  await waitForSession((board) => board.properties.reenterWhenZapped === true, "re-entry to come on");
  await boardInfo(page, "Re-enter when zapped: Yes", "the re-entry toggle, back off again");
  await waitForSession((board) => board.properties.reenterWhenZapped === false, "re-entry to go off");

  // --- Q, and EditorAskSaveChanged's "Save first?" ------------------------
  // leaveEditor transcribes EditorAskSaveChanged (editor.go:155-165). Until
  // M16.13a nothing raised `editorModified`, so the prompt was unreachable and
  // an edited world was abandoned in one keystroke; now every accepted edit,
  // board-info change and stat change raises it, and Q asks. Answering yes runs
  // the save and lets it carry the exit through.
  await pressKey(page, "KeyQ");
  record("key.editor.KeyQ");
  await waitForGrid(page, (cells) => hasText(cells, "Save first?"),
    "EditorAskSaveChanged's prompt on the way out");
  await pressKey(page, "KeyY");
  await waitForGrid(page, (cells) => hasText(cells, "Save world as:"), "the save-on-exit prompt");
  await replaceEntry(page, "ORCLEDIT");
  await waitForGrid(page, (cells) => hasText(cells, "E  Board editor"),
    "the title screen after saving on the way out");
  note("Q offered to save the modified world, and answering yes saved it and left");

  // =========================================================================
  // The report
  // =========================================================================
  fs.writeFileSync(
    path.join(outDir, "report.json"),
    JSON.stringify({ exercised: [...exercised].sort(), files, notes, exportedBoard }, null, 2) + "\n",
  );

  assert.deepEqual(pageErrors, [], "the page must raise no uncaught errors");
  // Chromium logs a console error when a socket the page is closing is closed by
  // the server at the same moment, which is precisely what leaving the editor
  // and starting test play both do (closeEditor / applyEditorTestPlay send
  // editorExit and then close). It is the orderly shutdown, not a fault; every
  // other console error still fails the run.
  const unexpectedConsole = consoleErrors.filter(
    (text) => !/WebSocket connection to .* failed: Close received after close/.test(text),
  );
  assert.deepEqual(unexpectedConsole, [], "the page must log no console errors");
  console.log(`\neditor vocabulary: ${exercised.size} manifest ids exercised`);
} catch (err) {
  failed = true;
  try {
    saveText("m1613-failure-screen.txt", gridToArt(await readGrid(page)));
  } catch {
    // The page may be gone; the trace below is the fallback.
  }
  console.error(err);
} finally {
  await context.tracing.stop({ path: path.join("test-results", "m16-13-trace.zip") });
  await browser.close();
  if (failed) process.exit(1);
}
