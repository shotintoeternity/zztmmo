// M16.14f — the editor socket reconnects, in a real browser.
//
// Driven by engine/m16_14f_test.go (TestM1614fBrowserEditorReconnectsAfterIts
// SocketIsClosed), which hosts the EDIT world (fixtures/editor.zwd) on the
// production server objects.
//
// The drop is REAL: /control/editor/drop closes the editor socket from the
// server with no close handshake and no editorExit, which is what a blip, a
// server restart or a closed lid look like to the page. Before M16.14f that
// landed the author on the title screen of the world they were editing.
//
// The recovery is asserted in three independent ways, because a page that
// simply froze would satisfy any one of them alone:
//
//   * the canvas is the EDITOR, on the board the author was on, with the cursor
//     where they left it — and not the title screen;
//   * a tile painted into the session WHILE THE BROWSER WAS AWAY is on that
//     canvas, which only a fresh snapshot can put there;
//   * the socket is live afterwards: a keystroke reaches the session.
//
// And no key is pressed between the drop and the repaint. The script counts
// them and the Go side requires the count to be zero.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import {
  baseURL,
  cellAt,
  controlURL,
  gridToArt,
  hasText,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  markProfileWarm,
  readGrid,
  saveText,
  textAt,
  waitForGrid,
} from "./lib/canvas.mjs";

const outDir = process.env.M1614F_OUT || path.resolve("test-results/m16-14f");
fs.mkdirSync(outDir, { recursive: true });

// Where the paint lands: an empty cell of Edit Annex, far from the cursor so
// the editor's own blink cannot be mistaken for it.
const PAINT_X = 5;
const PAINT_Y = 5;
// A Normal wall, not a Solid: a solid block fills its whole cell with one
// colour, and a uniform cell decodes as a blank rather than as a glyph.
const E_NORMAL = 22;
const PAINT_COLOR = 0x0c;
const PAINT_CHAR = 0xb2;

// Where the author's cursor is when the socket dies.
const CURSOR_X = 33;
const CURSOR_Y = 14;

const notes = [];
function note(text) {
  notes.push(text);
  console.log(`  · ${text}`);
}

// keysPressedDuringRecovery is the DoD's "without the player touching
// anything", counted rather than asserted by inspection: every keystroke goes
// through pressKey, and the window between the drop and the repaint is the one
// place where the count must not move.
let recovering = false;
let keysPressedDuringRecovery = 0;

async function pressKey(page, code) {
  if (recovering) keysPressedDuringRecovery += 1;
  await page.keyboard.press(code);
}

// ---------------------------------------------------------------------------
// The session, read from the server rather than from the screen under test
// ---------------------------------------------------------------------------

async function control(pathname, init) {
  const response = await fetch(`${controlURL}${pathname}`, { signal: AbortSignal.timeout(15000), ...init });
  if (!response.ok) throw new Error(`${pathname} failed (${response.status}): ${await response.text()}`);
  return response.json();
}

const editorBoard = () => control("/control/editor/board");
const editorMembers = () => control("/control/editor/members");

/** Poll the session until it holds `want` members. */
async function waitForMembers(want, describe, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const seen = await editorMembers();
    if (seen.members.length === want) return seen;
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${describe}: the session holds ${seen.members.length} member(s), want ${want}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
}

/** Poll the session until `pred(board)` holds. */
async function waitForSession(pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  for (;;) {
    last = await editorBoard();
    if (pred(last)) return last;
    if (Date.now() > deadline) {
      saveText(`m1614f-timeout-${describe.replace(/\W+/g, "-")}.json`, JSON.stringify(last, null, 2));
      throw new Error(`timed out waiting for ${describe} in the editor session`);
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
}

function sessionTile(board, x, y) {
  const i = (y - 1) * 60 + (x - 1);
  return {
    element: parseInt(board.elements.slice(i * 2, i * 2 + 2), 16),
    color: parseInt(board.colors.slice(i * 2, i * 2 + 2), 16),
  };
}

// ---------------------------------------------------------------------------
// The editor sidebar, read from the canvas (the same probes M16.13 uses)
// ---------------------------------------------------------------------------

const isEditorChrome = (cells) => textAt(cells, 62, 1, 15) === "  ZZT Editor   ";
const readoutRow = (cells) => textAt(cells, 61, 20, 19).trim();
const boardCell = (cells, x, y) => cellAt(cells, x - 1, y - 1);

function cursorPos(cells) {
  const m = /^(?:Pos: )?(\d+),(\d+)/.exec(readoutRow(cells));
  return m ? [Number(m[1]), Number(m[2])] : null;
}

function cursorAt(cells, x, y) {
  const at = cursorPos(cells);
  return !!at && at[0] === x && at[1] === y;
}

const WINDOW_CURSOR_ROW = 13;
const windowCursorLine = (cells) => textAt(cells, 14, WINDOW_CURSOR_ROW, 37).trimEnd();

/** Walk a select list to `label` and press Enter (M16.13's helper). */
async function pickFromList(page, label, describe) {
  let cells = await readGrid(page);
  for (let i = 0; i < 40; i += 1) {
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
  saveText("m1614f-list.txt", gridToArt(cells));
  throw new Error(`${describe}: never reached ${label}`);
}

async function moveCursor(page, code, x, y) {
  await pressKey(page, code);
  await waitForGrid(page, (cells) => cursorAt(cells, x, y), `the cursor at ${x},${y} after ${code}`);
}

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
await markProfileWarm(context);
let failed = false;

try {
  await installImageProbe(page);
  await context.tracing.start({ screenshots: true, snapshots: true });
  page.on("response", async (response) => {
    if (response.status() >= 400) consoleErrors.push(`HTTP ${response.status()} ${response.url()}`);
  });

  // =========================================================================
  // Act 1 — an author, editing
  // =========================================================================
  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served, not the build-me 404 page");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Rae");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("EDIT");
  await waitForGrid(page, (cells) => hasText(cells, "EDIT"), "the picker to match EDIT");
  await pressKey(page, "Enter");
  await waitForGrid(page, (cells) => hasText(cells, "E  Board editor"), "the title screen for EDIT");

  // The clock is deliberately NOT paused: this suite is about timers. The
  // reconnect backoff is a window.setTimeout, and a frozen clock would never
  // fire it — the recovery has to happen on the client's own schedule.
  await pressKey(page, "KeyE");
  await waitForGrid(page, (cells) => isEditorChrome(cells), "the editor sidebar");
  await waitForGrid(page, (cells) => cursorAt(cells, 30, 12), "the editor cursor readout");

  // Move onto the second board, so "it came back where the author was" is a
  // claim about a board they chose rather than the one the world opens on.
  await pressKey(page, "KeyB");
  await waitForGrid(page, (cells) => hasText(cells, "Switch boards"), "the board list");
  await pickFromList(page, "1: Edit Annex", "the board list");
  await waitForSession((board) => board.properties.boardName === "Edit Annex", "the switch to Edit Annex");
  note("the author is editing board 1, Edit Annex");

  // ...and put the cursor somewhere they would notice losing.
  await moveCursor(page, "ArrowRight", 31, 12);
  await moveCursor(page, "ArrowRight", 32, 12);
  await moveCursor(page, "ArrowRight", 33, 12);
  await moveCursor(page, "ArrowDown", 33, 13);
  await moveCursor(page, "ArrowDown", 33, CURSOR_Y);
  assert.equal(CURSOR_X, 33, "the cursor constants and the walk above must agree");

  const before = await editorMembers();
  assert.equal(before.members.length, 1, "exactly this browser is in the session");
  const memberBefore = before.members[0].id;

  {
    const cells = await readGrid(page);
    assert.notEqual(
      boardCell(cells, PAINT_X, PAINT_Y).ch,
      PAINT_CHAR,
      `the paint target ${PAINT_X},${PAINT_Y} must start empty, or its arrival would prove nothing`,
    );
  }

  // =========================================================================
  // Act 2 — the socket dies underneath the page
  // =========================================================================
  recovering = true;
  const dropped = await control("/control/editor/drop", { method: "POST" });
  assert.equal(dropped.closed, 1, "the server must have closed exactly one editor socket");
  note("the server closed the browser's editor socket with no handshake");

  // The world moves on while the browser is away. With no member left there is
  // nobody to broadcast a diff to, so this tile can only reach the canvas in a
  // snapshot the reconnect fetches.
  const painted = await control(
    `/control/editor/paint?x=${PAINT_X}&y=${PAINT_Y}&element=${E_NORMAL}&color=${PAINT_COLOR}`,
    { method: "POST" },
  );
  assert.equal(painted.nowElement, E_NORMAL, "the paint did not take in the session");
  note(`a wall was painted at ${PAINT_X},${PAINT_Y} of board ${painted.boardId} while the browser was disconnected`);

  // =========================================================================
  // Act 3 — it comes back, untouched
  // =========================================================================
  // The session sees the reconnect before the canvas can: waiting here first
  // means a canvas timeout below is a repaint failure rather than an ambiguity
  // about whether the browser ever came back.
  const returned = await waitForMembers(1, "the browser to re-enter the session");
  note(`the session has ${returned.members.length} member again (${returned.members[0].id})`);

  const back = await waitForGrid(
    page,
    (cells) => isEditorChrome(cells) && boardCell(cells, PAINT_X, PAINT_Y).ch === PAINT_CHAR,
    "the editor to reconnect and repaint from a fresh snapshot",
    30000,
  );
  assert.equal(
    boardCell(back, PAINT_X, PAINT_Y).color,
    PAINT_COLOR,
    "the repainted tile must carry the colour the session gave it",
  );
  assert.ok(
    !hasText(back, "E  Board editor"),
    "the browser must not have fallen back to the title screen of the world it was editing",
  );

  const restored = await waitForGrid(
    page,
    (cells) => cursorAt(cells, CURSOR_X, CURSOR_Y),
    `the cursor back at ${CURSOR_X},${CURSOR_Y}`,
  );
  assert.ok(isEditorChrome(restored), "the editor sidebar must still be the screen");
  recovering = false;
  note(`the editor repainted itself and put the cursor back at ${CURSOR_X},${CURSOR_Y} with no keystroke`);

  const after = await editorMembers();
  assert.equal(after.members.length, 1, "one person who reconnected is one member, not two");
  const memberAfter = after.members[0].id;
  assert.equal(
    after.members[0].boardId,
    painted.boardId,
    "the session must have the reconnected member on the board they were editing",
  );
  note(`the session holds one member (${memberBefore} before the drop, ${memberAfter} after)`);

  // The socket is genuinely live again, not a frozen picture of one: a
  // keystroke has to reach the session and change the world.
  await pressKey(page, "Space");
  const edited = await waitForSession(
    (board) => sessionTile(board, CURSOR_X, CURSOR_Y).element !== 0,
    "the reconnected browser's edit to reach the session",
  );
  assert.equal(
    edited.properties.boardName,
    "Edit Annex",
    "the reconnected browser must still be editing the board it was on",
  );
  note("an edit made after the reconnect reached the session, so the new socket is live");

  fs.writeFileSync(
    path.join(outDir, "report.json"),
    JSON.stringify(
      {
        memberBefore,
        memberAfter,
        closed: dropped.closed,
        membersAfter: after.members.length,
        keysPressedDuringRecovery,
        notes,
      },
      null,
      2,
    ),
  );

  assert.deepEqual(pageErrors, [], "the page must raise no uncaught errors");
  assert.deepEqual(consoleErrors, [], "the page must log no console errors");
  console.log("editor_reconnect.test.mjs: all assertions passed");
} catch (error) {
  failed = true;
  try {
    saveText("m1614f-failure.txt", gridToArt(await readGrid(page)));
  } catch {
    // The page may be unreadable; the trace below is the fallback.
  }
  console.error(error);
} finally {
  await context.tracing.stop({ path: path.join(outDir, "trace.zip") });
  await browser.close();
}

process.exit(failed ? 1 : 0);
