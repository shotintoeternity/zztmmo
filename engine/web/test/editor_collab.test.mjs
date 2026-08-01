// M16.14 — the collaborative editor, in three real browsers.
//
// Driven by engine/m16_14_test.go (TestM1614CollaborativeEditorInBrowsers),
// which hosts COLLAB (fixtures/editor.zwd) on the production server objects with
// an AuthService wired to a hermetic identity provider on the control listener.
//
// THE CAST. Ada and Bob sign in for real — the browser presses G on the title
// screen and rides the whole OAuth redirect through the fake Google, which
// checks its own PKCE challenge before it issues a code. The guest never signs
// in. All three open the SAME editor session on the same world.
//
// THREE AUTHORITIES, and every claim names which one it rests on:
//   * each browser's decoded canvas  — screens, dialogs, collaborator cursors;
//   * /control/editor/session        — members, read-only flags, held leases;
//   * /control/editor/board + world  — the tiles and the serialized .ZZT, which
//     engine/m16_14_test.go re-reads with an independent vanilla-format parser.
//
// WHAT IT FOUND, AND WHAT M16.14a DID ABOUT IT. Three divergences, recorded
// rather than worked around, and filed as a gap task that has since closed them.
// The acts that pinned each one in its broken form now require the fix:
//   (a) board- and world-scoped changes reach every member they concern — the
//       frame to those watching that board, the switcher's board list to all;
//   (b) an invited collaborator edits without re-entering the editor;
//   (c) a stat lease is given back however the shared engine has moved.
// A run that records a finding is a run that found something NEW.

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
  runClock,
  saveText,
  textAt,
  waitForGrid,
  waitForQuiet,
} from "./lib/canvas.mjs";

const outDir = process.env.M1614_OUT || path.resolve("test-results/m16-14");
fs.mkdirSync(outDir, { recursive: true });

const WORLD = "COLLAB";
const BOARD_COLS = 60;
const BOARD_ROWS = 25;
const DRAFT_BOARD = 0;
const ANNEX_BOARD = 1;

// The cells this run writes. They are clear of everything fixtures/editor.zwd
// authored (its furniture is on rows 5 and 15), so a landmark is only ever what
// this run put there.
const ADA_CELL = { x: 45, y: 3 };
const BOB_CELL = { x: 47, y: 3 };
const RACE_CELL = { x: 50, y: 10 };
const ANNEX_CELL = { x: 20, y: 20 };
const PUBLISH_CELL = { x: 43, y: 3 };
// The cell the freshly-invited collaborator draws on, before anybody has
// changed their brush: it is checked in the session, not on a canvas, because
// the default Solid brush decodes ambiguously (see E_NORMAL below).
const INVITE_CELL = { x: 49, y: 3 };
const ECHO_CELL = { x: 45, y: 12 };
// Row 5 of the draft board: object, lion, spinning gun, passage, duplicator.
const OBJECT_CELL = { x: 10, y: 5 };
const LION_CELL = { x: 14, y: 5 };
// Somewhere with nothing on it, for parking a cursor where the blink probe is
// unambiguous — one square per browser, so two cursors never share a cell and
// no browser's own white cross can hide a tile another one is being asked about.
const PARKS = { Ada: { x: 51, y: 22 }, Bob: { x: 54, y: 22 }, Guest: { x: 57, y: 22 } };

const EDITOR_CURSOR_CHAR = 0xc5;
const EDITOR_CURSOR_COLOR = 0x0f;
const E_EMPTY = 0;
// The brush every author in this run draws with. The default pattern is Solid,
// a full block: on a flat background that decodes as a uniform cell, which the
// canvas decoder cannot tell from empty floor (NOTES.md M16.9) — so a wall drawn
// with it could only ever be checked in the session, never on a collaborator's
// screen. One press of P moves the brush to the Normal wall, whose 0xB2 glyph is
// textured and therefore decodes as itself.
const E_NORMAL = 22;
const NORMAL_CHAR = 0xb2;

const checkpoints = [];
const findings = [];
const notes = [];
const files = {};
const landmarks = new Map();

function note(text) {
  notes.push(text);
  console.log(`  · ${text}`);
  fs.appendFileSync(path.join(outDir, "progress.log"), `${text}\n`);
}

function finding(text) {
  findings.push(text);
  note(`FINDING (M16.14a): ${text}`);
}

// ---------------------------------------------------------------------------
// The server, read from outside the browsers
// ---------------------------------------------------------------------------

async function control(route) {
  const response = await fetch(`${controlURL}${route}`, { signal: AbortSignal.timeout(20000) });
  if (!response.ok) throw new Error(`${route} failed (${response.status}): ${(await response.text()).trim()}`);
  return response.json();
}

const editorBoard = () => control("/control/editor/board");
const sessionState = () => control("/control/editor/session");
const worldsOnDisk = () => control("/control/worlds/files");
const instances = () => control("/control/instances");
const holdSession = (ms) => control(`/control/editor/hold?ms=${ms}`);
const armAccount = (account) => control(`/control/auth/next?account=${encodeURIComponent(account)}`);

async function editorWorldBytes() {
  const { data } = await control("/control/editor/world");
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

async function waitFor(read, pred, describe, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  for (;;) {
    last = await read();
    if (pred(last)) return last;
    if (Date.now() > deadline) {
      saveText(`m1614-timeout-${describe.replace(/\W+/g, "-")}.json`, JSON.stringify(last, null, 2));
      throw new Error(`timed out waiting for ${describe}; the server said ${JSON.stringify(last).slice(0, 900)}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
}

const waitForSession = (pred, describe, timeoutMs) => waitFor(editorBoard, pred, describe, timeoutMs);
const waitForMembers = (pred, describe, timeoutMs) => waitFor(sessionState, pred, describe, timeoutMs);

function memberFor(state, { accountId, name }) {
  return state.members.find((m) => (accountId ? m.accountId === accountId : m.name === name)) || null;
}

// ---------------------------------------------------------------------------
// The editor sidebar, read from a canvas
//
// These probes are the ones engine/web/test/editor_solo.test.mjs established for
// M16.13; they are repeated rather than shared so that a sidebar move reddens
// each sweep in its own words rather than through a common helper nobody owns.
// ---------------------------------------------------------------------------

const isEditorChrome = (cells) => textAt(cells, 62, 1, 15) === "  ZZT Editor   ";
const presencePanelOpen = (cells) => textAt(cells, 62, 3, 15) === "  Who's here   ";
const readoutRow = (cells) => textAt(cells, 61, 20, 19).trim();
// The mode line, EDITOR.PAS:174-181 as editor.ts draws it: "Text entry ",
// "Drawing on " or "Drawing off" at (68,24).
const modeText = (cells) => textAt(cells, 68, 24, 11);
const elementText = (cells) => textAt(cells, 62, 23, 17).trim();
const boardCell = (cells, x, y) => cellAt(cells, x - 1, y - 1);

function cursorPos(cells) {
  const m = /^(?:Pos: )?(\d+),(\d+)/.exec(readoutRow(cells));
  return m ? [Number(m[1]), Number(m[2])] : null;
}

const cursorAt = (cells, x, y) => {
  const at = cursorPos(cells);
  return !!at && at[0] === x && at[1] === y;
};

// A select list draws its entries as hyperlinks whose caption starts at column
// 14, and the selected line is always screen row 13 (M16.10/M16.13's probe).
const WINDOW_CURSOR_ROW = 13;
const windowCursorLine = (cells) => textAt(cells, 14, WINDOW_CURSOR_ROW, 37).trimEnd();
// The board switcher cannot be detected by its title: the editor's command block
// permanently carries a "B   Switch boards" row, so hasText would always say yes.
// Its entries are "<id>: <name>", and the selected one is always on row 13.
const boardListOpen = (cells) => /^\d+: /.test(windowCursorLine(cells));

// ---------------------------------------------------------------------------
// One browser
// ---------------------------------------------------------------------------

async function openBrowser(label) {
  const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
  const ed = { label, browser, context, page, pageErrors, consoleErrors, closed: false, park: PARKS[label] };
  await installImageProbe(page);
  page.on("response", async (response) => {
    if (response.status() >= 400) consoleErrors.push(`HTTP ${response.status()} ${response.url()}`);
  });
  await context.tracing.start({ screenshots: true, snapshots: true });
  await load(ed);
  return ed;
}

async function load(ed) {
  const response = await ed.page.goto(baseURL);
  assert.equal(response?.status(), 200, `${ed.label}: the client index must be served`);
  await ed.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(ed.page);
}

const press = (ed, code) => ed.page.keyboard.press(code);

async function screen(ed, pred, describe, timeoutMs) {
  return waitForGrid(ed.page, pred, `${ed.label}: ${describe}`, timeoutMs);
}

/**
 * Freeze the page clock, retrying the race inside pauseClock: it reads Date.now()
 * out of the page and then pauses one millisecond later, and a fake clock that is
 * still ticking with real time can have passed that instant by the time the pause
 * arrives. One browser pausing once rarely notices; this run pauses six times.
 */
async function freezeClock(ed) {
  for (let attempt = 0; ; attempt += 1) {
    try {
      await pauseClock(ed.page);
      return;
    } catch (err) {
      if (attempt >= 5) throw err;
    }
  }
}

/** Walk the production launch flow: name, world picker, title screen. */
async function reachTitle(ed, playerName) {
  await screen(ed, (c) => hasText(c, "Type your name"), "the launch name prompt");
  await ed.page.keyboard.type(playerName);
  await press(ed, "Enter");
  await screen(ed, (c) => hasText(c, "Choose a World"), "the world picker");
  await ed.page.keyboard.type(WORLD);
  await screen(ed, (c) => hasText(c, WORLD), `the picker to match ${WORLD}`);
  await press(ed, "Enter");
  await screen(ed, (c) => hasText(c, "E  Board editor"), "the title screen");
  // From here the page clock is frozen. The editor's cursor blink is a 3-phase
  // 150ms interval, so a running clock would make every canvas read a coin toss;
  // the run advances it deliberately when it wants a particular phase.
  await freezeClock(ed);
}

/**
 * Sign in the way the product does: G on the title screen navigates to
 * /api/auth/google/start, the hermetic IdP redirects back through the real
 * callback, and the client reloads with the session cookie HandleCallback set.
 * Nothing is injected into the browser.
 */
async function signIn(ed, account, displayName) {
  await armAccount(account);
  await ed.page.evaluate(() => {
    window.__m1614BeforeLogin = true;
  });
  await press(ed, "KeyG");
  // A fresh window means the whole redirect chain completed and the client
  // reloaded; the marker cannot survive a navigation.
  await ed.page.waitForFunction(() => !window.__m1614BeforeLogin, null, { timeout: 30000 });
  await ed.page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(ed.page);
  await reachTitle(ed, ed.label);
  const cells = await screen(ed, (c) => textAt(c, 65, 23, 15).trim() === displayName,
    `the title sidebar to name ${displayName}`);
  assert.equal(textAt(cells, 62, 23, 3), " G ", `${ed.label}: the sign-in row keeps its badge`);
  note(`${ed.label} signed in through the identity provider as ${displayName}`);
}

/** E on the title screen, into the shared editor session. */
async function enterEditor(ed) {
  await press(ed, "KeyE");
  await screen(ed, (c) => isEditorChrome(c), "the editor sidebar");
  await screen(ed, (c) => cursorPos(c) !== null, "the editor cursor readout");
}

/** Leave the editor the ordinary way and come back to the title screen. */
async function leaveEditor(ed) {
  await press(ed, "Escape");
  await screen(ed, (c) => hasText(c, "E  Board editor"), "the title screen after leaving the editor");
}

/** Reload and walk all the way back into the editor — the returning-author path. */
async function returnToEditor(ed) {
  await load(ed);
  await reachTitle(ed, ed.label);
  await enterEditor(ed);
}

// ---------------------------------------------------------------------------
// Cursor movement and the blink phase
// ---------------------------------------------------------------------------

async function moveTo(ed, x, y, { rapid = false } = {}) {
  const cells = await readGrid(ed.page);
  const at = cursorPos(cells);
  if (!at) throw new Error(`${ed.label}: the editor has no cursor readout`);
  let [cx, cy] = at;
  const steps = [];
  for (; cx > x; cx -= 1) steps.push("ArrowLeft");
  for (; cx < x; cx += 1) steps.push("ArrowRight");
  for (; cy > y; cy -= 1) steps.push("ArrowUp");
  for (; cy < y; cy += 1) steps.push("ArrowDown");
  for (const code of steps) {
    await ed.page.keyboard.press(code, rapid ? { delay: 0 } : undefined);
  }
  return screen(ed, (c) => cursorAt(c, x, y), `the cursor at ${x},${y}`);
}

/**
 * Put the editor blink on the phase that shows cursors, or the one that shows
 * the tile underneath. setEditorBlinking starts on a cursor-shown phase and the
 * clock is frozen, so this is how the run chooses: a screen comparison wants the
 * cursors gone, a cursor assertion wants them drawn.
 *
 * The probe is the cell under this browser's OWN cursor, which every caller
 * parks on empty floor first — a tile that genuinely drew a white 0xC5 would
 * read as "shown" whatever the phase.
 */
async function setCursorPhase(ed, shown) {
  for (let i = 0; i <= 3; i += 1) {
    const cells = await readGrid(ed.page);
    const at = cursorPos(cells);
    if (!at) throw new Error(`${ed.label}: the editor has no cursor readout`);
    const cell = boardCell(cells, at[0], at[1]);
    const isShown = cell.ch === EDITOR_CURSOR_CHAR && cell.color === EDITOR_CURSOR_COLOR;
    if (isShown === shown) return cells;
    await runClock(ed.page, 150);
  }
  saveText(`m1614-blink-${ed.label}.txt`, gridToArt(await readGrid(ed.page)));
  throw new Error(`${ed.label}: the blink never reached the ${shown ? "cursor" : "tile"} phase`);
}

/**
 * Wait until a browser DRAWS a board cell the way `pred` wants it. The browser's
 * own cursor is parked out of the way and the blink is put on its tile phase
 * first: a cursor sitting on the cell would answer for it, and the local cursor
 * is white whatever is underneath.
 */
async function waitForTileOnScreen(ed, x, y, pred, describe, timeoutMs = 20000) {
  await moveTo(ed, ed.park.x, ed.park.y);
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const cells = await setCursorPhase(ed, false);
    const cell = boardCell(cells, x, y);
    if (pred(cell)) return cells;
    if (Date.now() > deadline) {
      saveText(`m1614-tile-${ed.label}.txt`, gridToArt(cells));
      throw new Error(`${ed.label}: timed out waiting for ${describe} at (${x},${y}); ` +
        `the cell drew ch ${cell.ch} colour ${cell.color}`);
    }
    await ed.page.waitForTimeout(100);
  }
}

// ---------------------------------------------------------------------------
// Modal helpers
// ---------------------------------------------------------------------------

/**
 * Walk a select list to the first entry `want` accepts and press Enter. The list
 * is not searched from a screenshot first: a text window shows only the lines
 * around its cursor, so an entry further down is genuinely not on screen yet.
 * `want` may be the exact label or a predicate, which is how this run picks a
 * board whose name two browsers disagree about (M16.14a (a)).
 */
async function pickFromList(ed, want, describe) {
  const matches = typeof want === "function" ? want : (line) => line === want;
  let cells = await readGrid(ed.page);
  for (let i = 0; i < 40; i += 1) {
    if (matches(windowCursorLine(cells))) {
      await press(ed, "Enter");
      return windowCursorLine(cells);
    }
    const was = windowCursorLine(cells);
    await press(ed, "ArrowDown");
    cells = await screen(ed, (c) => windowCursorLine(c) !== was,
      `${describe}: the list cursor to move off ${JSON.stringify(was)}`);
  }
  saveText(`m1614-list-${ed.label}.txt`, gridToArt(cells));
  throw new Error(`${ed.label}: ${describe} never found its entry`);
}

/** Every entry of an open select list, walked from the top and stopping at the clamp. */
async function listEntries(ed) {
  const entries = [];
  let cells = await readGrid(ed.page);
  for (let i = 0; i < 40; i += 1) {
    const line = windowCursorLine(cells);
    if (entries.length > 0 && line === entries[entries.length - 1]) break;
    entries.push(line);
    await press(ed, "ArrowDown");
    await ed.page.waitForTimeout(40);
    cells = await readGrid(ed.page);
  }
  return entries;
}

async function typeEntry(ed, text) {
  if (text.length > 0) await ed.page.keyboard.type(text, { delay: 8 });
  await press(ed, "Enter");
}

async function replaceEntry(ed, text) {
  for (let i = 0; i < 24; i += 1) await press(ed, "Backspace");
  await typeEntry(ed, text);
}

/**
 * Escape out of an open window, and wait until the text that identified it is
 * gone. The editor's own Escape LEAVES the editor, so every caller has to be
 * sure a window really is open first — which is what `gone` is checked against.
 */
async function closeWindow(ed, open, describe) {
  const isOpen = typeof open === "function" ? open : (cells) => hasText(cells, open);
  await press(ed, "Escape");
  await screen(ed, (c) => isEditorChrome(c) && !isOpen(c), `${describe} to close`);
}

// ---------------------------------------------------------------------------
// Landmarks and convergence checkpoints
// ---------------------------------------------------------------------------

/**
 * Record what the session holds at a cell, so engine/m16_14_test.go can hold the
 * SERIALIZED world to the same claim through its own vanilla-format reader.
 * Reads the tiles from the server, never from a canvas: a browser that drew a
 * convincing tile it never sent must not be able to write the landmark that
 * proves it.
 */
async function setLandmark(board, { x, y }, what, expect) {
  const state = await waitForSession(
    (b) => b.properties.boardId === board && (!expect || expect(sessionTile(b, x, y))),
    `${what} at board ${board} (${x},${y})`);
  const tile = sessionTile(state, x, y);
  landmarks.set(`${board}:${x}:${y}`, { board, x, y, element: tile.element, color: tile.color, what });
  return tile;
}

function boardRegion(cells) {
  const out = [];
  for (let y = 0; y < BOARD_ROWS; y += 1) {
    for (let x = 0; x < BOARD_COLS; x += 1) {
      const cell = cellAt(cells, x, y);
      out.push(cell.ch, cell.color);
    }
  }
  return out;
}

/**
 * One schedule's convergence proof: every browser's board region is required to
 * be identical, cell for cell, and the session's serialized world is written out
 * beside the landmarks everyone agreed on.
 *
 * Cursors are taken off the screen first. The local cursor is white and a
 * collaborator's is their own colour, so two browsers CANNOT agree on a cell a
 * cursor is sitting on — that difference is the overlay working, not the world
 * diverging.
 */
async function checkpoint(label, editors) {
  const grids = [];
  for (const ed of editors) {
    await moveTo(ed, ed.park.x, ed.park.y);
  }
  for (const ed of editors) {
    grids.push({ ed, cells: await setCursorPhase(ed, false) });
  }
  const reference = grids[0];
  const referenceRegion = boardRegion(reference.cells);
  for (const other of grids.slice(1)) {
    const region = boardRegion(other.cells);
    if (JSON.stringify(region) !== JSON.stringify(referenceRegion)) {
      const diffs = [];
      for (let i = 0; i < referenceRegion.length; i += 2) {
        if (referenceRegion[i] !== region[i] || referenceRegion[i + 1] !== region[i + 1]) {
          const cell = i / 2;
          diffs.push(`(${(cell % BOARD_COLS) + 1},${Math.floor(cell / BOARD_COLS) + 1}): ` +
            `${reference.ed.label} ${referenceRegion[i]}/${referenceRegion[i + 1]} vs ` +
            `${other.ed.label} ${region[i]}/${region[i + 1]}`);
        }
      }
      saveText(`m1614-divergence-${label.replace(/\W+/g, "-")}.txt`,
        `${reference.ed.label}\n${gridToArt(reference.cells)}\n\n${other.ed.label}\n${gridToArt(other.cells)}`);
      throw new Error(`checkpoint ${label}: ${other.ed.label} and ${reference.ed.label} disagree on ` +
        `${diffs.length} board cell(s): ${diffs.slice(0, 8).join("; ")}`);
    }
  }

  const screensFile = `screens-${label.replace(/\W+/g, "-")}.txt`;
  fs.writeFileSync(path.join(outDir, screensFile),
    `${label}\nagreed by: ${editors.map((e) => e.label).join(", ")}\n\n${gridToArt(reference.cells)}\n`);
  const worldFile = `world-${label.replace(/\W+/g, "-")}.ZZT`;
  fs.writeFileSync(path.join(outDir, worldFile), await editorWorldBytes());

  checkpoints.push({
    label,
    world: worldFile,
    screens: screensFile,
    browsers: editors.map((e) => e.label),
    landmarks: [...landmarks.values()],
  });
  note(`checkpoint "${label}": ${editors.map((e) => e.label).join(" and ")} agree on every board cell, ` +
    `over ${landmarks.size} landmark(s)`);
}

// ---------------------------------------------------------------------------
// The run
// ---------------------------------------------------------------------------

const editors = [];
let failed = false;

try {
  // =========================================================================
  // Act 0 — three browsers, two real sign-ins
  // =========================================================================
  const ada = await openBrowser("Ada");
  const bob = await openBrowser("Bob");
  const guest = await openBrowser("Guest");
  editors.push(ada, bob, guest);

  for (const ed of editors) await reachTitle(ed, ed.label);
  {
    const cells = await readGrid(guest.page);
    assert.equal(textAt(cells, 65, 23, 15).trim(), "Google sign-in",
      "a signed-out browser is offered the sign-in row");
  }
  await signIn(ada, "ada", "Ada Lovelace");
  await signIn(bob, "bob", "Bob Bones");

  // =========================================================================
  // Act 1 — the owner publishes, and ownership lands on disk
  // =========================================================================
  await enterEditor(ada);
  {
    const state = await waitForMembers((s) => s.members.length === 1, "Ada alone in the session");
    assert.equal(state.members[0].accountId, "google:ada", "the session knows which account is editing");
    assert.equal(state.members[0].readOnly, false, "an unowned world is editable by whoever opens it");
  }

  // One P: off the Solid pattern and onto the Normal wall (see NORMAL_CHAR).
  await press(ada, "KeyP");
  await moveTo(ada, PUBLISH_CELL.x, PUBLISH_CELL.y);
  await press(ada, "Space");
  await setLandmark(DRAFT_BOARD, PUBLISH_CELL, "the wall Ada drew before publishing",
    (tile) => tile.element === E_NORMAL);

  await press(ada, "KeyS");
  await screen(ada, (c) => hasText(c, "World:"), "the world menu");
  await pickFromList(ada, "Save and publish", "publishing");
  await screen(ada, (c) => hasText(c, "Save world as:"), "the publish prompt");
  await replaceEntry(ada, WORLD);
  await screen(ada, (c) => hasText(c, "World published as"), "the publish confirmation");
  await closeWindow(ada, "World published as", "the publish confirmation");

  files.published = "published.ZZT";
  fs.writeFileSync(path.join(outDir, files.published), await editorWorldBytes());
  {
    const disk = await waitFor(worldsOnDisk, (w) => !!w.access[WORLD], `${WORLD}'s access sidecar`);
    assert.equal(disk.access[WORLD].ownerAccountId, "google:ada",
      "publishing an unowned world makes the publisher its owner");
    assert.ok(disk.files.some((f) => f.name === `${WORLD}.ZZT`), `${WORLD}.ZZT is on disk`);
    note(`Ada published ${WORLD} and became its owner`);
  }

  // =========================================================================
  // Act 2 — everyone else arrives read-only, and stays that way
  // =========================================================================
  await enterEditor(bob);
  await enterEditor(guest);
  const roster = await waitForMembers((s) => s.members.length === 3, "all three browsers in the session");
  assert.ok(memberFor(roster, { accountId: "google:ada" }), "the session lists Ada");
  assert.equal(memberFor(roster, { accountId: "google:bob" }).readOnly, true,
    "an uninvited account opens an owned world read-only");
  assert.equal(roster.members.find((m) => m.accountId === "").readOnly, true,
    "a signed-out guest opens an owned world read-only");

  // An unauthorized edit, from the keyboard, on the world's own bytes.
  const beforeIntrusion = await editorWorldBytes();
  for (const ed of [bob, guest]) {
    await moveTo(ed, ADA_CELL.x, ADA_CELL.y);
    await press(ed, "Space");
    await screen(ed, (c) => hasText(c, "read-only for this account"), "the read-only refusal");
    await closeWindow(ed, "read-only for this account", "the read-only refusal");
    await press(ed, "KeyI");
    await screen(ed, (c) => hasText(c, "read-only for this account"),
      "the read-only refusal for Board Information");
    await closeWindow(ed, "read-only for this account", "the read-only refusal");
  }
  assert.ok((await editorWorldBytes()).equals(beforeIntrusion),
    "a read-only member's refused edits must not move a byte of the world");
  note("both read-only members were refused, and the world is byte-identical");

  // The owner-only action is not even offered.
  await press(guest, "KeyS");
  await screen(guest, (c) => hasText(c, "World:"), "the world menu");
  {
    const entries = await listEntries(guest);
    assert.ok(!entries.includes("Invite collaborator"),
      `a read-only member is offered ${JSON.stringify(entries)}, which must not include the invite`);
    assert.ok(entries.includes("Download .ZZT"), "a read-only member may still take a copy away");
    note(`the guest's world menu offers ${entries.length} entries and no invite`);
  }
  await closeWindow(guest, "Download .ZZT", "the guest's world menu");

  // =========================================================================
  // Act 3 — the invite, and the divergence it leaves behind
  // =========================================================================
  await press(ada, "KeyS");
  await screen(ada, (c) => hasText(c, "World:"), "the world menu");
  await pickFromList(ada, "Invite collaborator", "the invite dialog");
  await screen(ada, (c) => hasText(c, "Account id:"), "the account-id prompt");
  await typeEntry(ada, "google:bob");
  await screen(ada, (c) => hasText(c, "Saved") || hasText(c, "Cannot save"), "the invite outcome");
  {
    const cells = await readGrid(ada.page);
    assert.ok(!hasText(cells, "Cannot save"), "the owner's invite must be accepted");
  }
  await closeWindow(ada, "Saved", "the invite confirmation");
  {
    const disk = await waitFor(worldsOnDisk,
      (w) => (w.access[WORLD]?.collaboratorAccountIds || []).includes("google:bob"),
      "the invited collaborator on disk");
    assert.deepEqual(disk.access[WORLD].collaboratorAccountIds, ["google:bob"],
      "the invite is recorded in the world's access sidecar");
  }
  await waitForMembers((s) => memberFor(s, { accountId: "google:bob" })?.readOnly === false,
    "the server to clear Bob's read-only flag");

  // M16.14a (b), closed: the invite sends the invitee a snapshot addressed to
  // them, which is the only thing their client's editorReadOnly is ever set
  // from. So Bob draws NOW — no re-entry, no "Read-only" window — and the tile
  // is read back out of the session rather than off his own canvas.
  await moveTo(bob, INVITE_CELL.x, INVITE_CELL.y);
  await press(bob, "Space");
  await setLandmark(DRAFT_BOARD, INVITE_CELL, "the tile the invitee drew without re-entering",
    (t) => t.element !== E_EMPTY);
  {
    const cells = await readGrid(bob.page);
    assert.ok(!hasText(cells, "read-only for this account"),
      "an invited collaborator's own browser must stop refusing them");
  }
  // And being told did not move him: the snapshot carries the cursor he last
  // reported, not the middle of the board.
  assert.ok(cursorAt(await readGrid(bob.page), INVITE_CELL.x, INVITE_CELL.y),
    "the invite must not drag the invitee's cursor");
  note("the invited collaborator edited straight away, without leaving the editor and coming back");

  // =========================================================================
  // Act 4 — live diffs and live cursors
  // =========================================================================
  // Bob draws with the same brush in his own colour, so which of two writers won
  // a cell is a fact about the tile rather than a guess.
  await press(bob, "KeyP");
  await press(bob, "KeyC");
  await press(bob, "KeyC");

  await moveTo(ada, ADA_CELL.x, ADA_CELL.y);
  await press(ada, "Space");
  const adaTile = await setLandmark(DRAFT_BOARD, ADA_CELL, "the wall Ada drew", (t) => t.element === E_NORMAL);
  for (const ed of [bob, guest]) {
    await waitForTileOnScreen(ed, ADA_CELL.x, ADA_CELL.y,
      (cell) => cell.ch === NORMAL_CHAR && cell.color === adaTile.color, "Ada's wall");
  }
  note(`Ada's edit reached both other browsers (colour ${adaTile.color.toString(16)})`);

  await moveTo(bob, BOB_CELL.x, BOB_CELL.y);
  await press(bob, "Space");
  const bobTile = await setLandmark(DRAFT_BOARD, BOB_CELL, "the wall Bob drew", (t) => t.element === E_NORMAL);
  assert.notEqual(bobTile.color, adaTile.color, "the two authors are drawing in different colours");
  for (const ed of [ada, guest]) {
    await waitForTileOnScreen(ed, BOB_CELL.x, BOB_CELL.y,
      (cell) => cell.ch === NORMAL_CHAR && cell.color === bobTile.color, "Bob's wall");
  }
  note("a read-only member still sees every collaborator's edit as it happens");

  // Cursors: Ada parks hers where nothing else is, and the others must draw it
  // in her own presence colour, on the phase that shows cursors.
  await moveTo(ada, 52, 6);
  const adaColor = (await waitForMembers((s) => memberFor(s, { accountId: "google:ada" })?.x === 52,
    "the session to carry Ada's cursor")).members.find((m) => m.accountId === "google:ada").color;
  for (const ed of [bob, guest]) {
    await moveTo(ed, ed.park.x, ed.park.y);
    await setCursorPhase(ed, true);
    const cells = await screen(ed, (c) => {
      const cell = boardCell(c, 52, 6);
      return cell.ch === EDITOR_CURSOR_CHAR && cell.color === adaColor;
    }, "Ada's cursor in her own colour");
    const own = boardCell(cells, ed.park.x, ed.park.y);
    assert.equal(own.color, EDITOR_CURSOR_COLOR, `${ed.label}: their own cursor stays white`);
    assert.equal(own.ch, EDITOR_CURSOR_CHAR, `${ed.label}: their own cursor is the same cross glyph`);
  }
  note(`Ada's cursor is drawn on both other screens in colour ${adaColor.toString(16)}, ` +
    "and each browser's own stays white");

  // The legend names the colours.
  await press(bob, "KeyW");
  await screen(bob, (c) => presencePanelOpen(c), "the collaborator legend");
  {
    const cells = await readGrid(bob.page);
    assert.ok(hasText(cells, "Bob Bones (you)"), "the legend marks the viewer's own entry");
    assert.ok(hasText(cells, "Ada Lovelace"), "the legend names the other signed-in collaborator");
    assert.ok(hasText(cells, "On this board:"), "the legend groups members by board");
  }
  await press(bob, "Escape");
  await screen(bob, (c) => !presencePanelOpen(c), "the legend to close");

  await checkpoint("live edits", [ada, bob, guest]);

  // =========================================================================
  // Act 4b — the local echo, caught in the act
  // =========================================================================
  // Text entry paints its own cell before the server has seen the keystroke
  // (editor_input.ts optimisticEditorTextCell, applied in handleEditorTextKey).
  // Convergence alone cannot tell that apart from a fast round trip, so the
  // session's own lock is held for a second and a half first: inside that window
  // the server has provably not answered, and the difference between the author's
  // screen and a collaborator's IS the echo.
  //
  // Both cursors are already parked and both blinks are on the tile phase from
  // the checkpoint above, so neither screen is being read through a cursor.
  await press(bob, "F4");
  await screen(bob, (c) => modeText(c) === "Text entry ", "Bob's text-entry mode");
  await moveTo(bob, ECHO_CELL.x, ECHO_CELL.y);
  const guestBeforeEcho = boardCell(await setCursorPhase(guest, false), ECHO_CELL.x, ECHO_CELL.y);

  await holdSession(1500);
  await press(bob, "KeyZ");
  {
    const echoed = boardCell(await readGrid(bob.page), ECHO_CELL.x, ECHO_CELL.y);
    assert.equal(echoed.ch, "z".charCodeAt(0),
      "the author's own screen must carry the character before the server has answered");
    const collaborator = boardCell(await readGrid(guest.page), ECHO_CELL.x, ECHO_CELL.y);
    assert.deepEqual(collaborator, guestBeforeEcho,
      "a collaborator must not see a character the server has not accepted yet");
    note(`the typed cell is on the author's screen (ch ${echoed.ch}) and on nobody else's ` +
      "while the session is still locked");
  }

  // And when the server does answer, the echo has to have been right: the
  // authoritative diff repaints that cell on every screen, and they must agree.
  const echoTile = await setLandmark(DRAFT_BOARD, ECHO_CELL, "the character Bob typed",
    (t) => t.color === "z".charCodeAt(0));
  await waitForTileOnScreen(guest, ECHO_CELL.x, ECHO_CELL.y,
    (cell) => cell.ch === "z".charCodeAt(0), "the typed character, once the session accepted it");
  {
    const authored = boardCell(await setCursorPhase(bob, false), ECHO_CELL.x, ECHO_CELL.y);
    const seen = boardCell(await setCursorPhase(guest, false), ECHO_CELL.x, ECHO_CELL.y);
    assert.deepEqual(authored, seen,
      "the optimistic cell and the authoritative one must be the same cell, or the author's screen " +
      "would keep a colour nobody else has");
    note(`the local echo predicted the session's own tile exactly (element ${echoTile.element}, ` +
      `drawn ${authored.ch}/${authored.color})`);
  }
  await press(bob, "Enter");
  await screen(bob, (c) => modeText(c) !== "Text entry ", "Bob leaving text-entry mode");

  // =========================================================================
  // Act 5 — a reply that lands after the cursor moved on
  // =========================================================================
  // Every cursor step sends an editorInspect and every reply carries the tile it
  // was asked about. Crossing the fixture's furniture at full speed puts several
  // of those replies in flight behind the cursor; applyEditorInspect drops the
  // ones that no longer match it (editor_cursor.ts editorReplyMatchesCursor), so
  // the rapid walk must end where the slow walk ends, showing the same tile.
  await moveTo(guest, 27, 5);
  await moveTo(guest, 9, 5, { rapid: true });
  await waitForQuiet(guest.page, 3000);
  const rapidEnd = await readGrid(guest.page);
  assert.ok(cursorAt(rapidEnd, 9, 5), "the rapid walk ends where it was aimed");
  const rapidElement = elementText(rapidEnd);
  await moveTo(guest, 30, 12);
  await moveTo(guest, 9, 5);
  await waitForQuiet(guest.page, 3000);
  assert.equal(elementText(await readGrid(guest.page)), rapidElement,
    "a rapid walk and a slow walk must leave the same tile under the cursor readout — " +
    "a stale inspect reply that was applied would leave the furniture it crossed");
  note(`eighteen cursor steps with no waiting between them still ended on ${JSON.stringify(rapidElement)}`);

  // =========================================================================
  // Act 6 — per-board and per-stat leases
  // =========================================================================
  await press(ada, "KeyI");
  await screen(ada, (c) => hasText(c, "Board Information"), "Ada's Board Information");
  await waitForMembers((s) => s.leases.some((l) => l.kind === "board" && l.boardId === DRAFT_BOARD &&
    l.holderName === "Ada Lovelace"), "Ada's board lease");

  await press(bob, "KeyI");
  await screen(bob, (c) => hasText(c, "Ada Lovelace is editing this board."),
    "the board lease refusal, naming its holder");
  await closeWindow(bob, "is editing this board", "the lease refusal");
  {
    const state = await sessionState();
    assert.equal(state.leases.filter((l) => l.kind === "board").length, 1,
      "a refused board lease must not become a second one");
  }

  await closeWindow(ada, "Board Information", "Ada's Board Information");
  await waitForMembers((s) => s.leases.length === 0, "the board lease to be released on close");

  await press(bob, "KeyI");
  await screen(bob, (c) => hasText(c, "Board Information"), "Bob's Board Information, once Ada let go");
  await waitForMembers((s) => s.leases.some((l) => l.kind === "board" && l.holderName === "Bob Bones"),
    "Bob's board lease");
  await closeWindow(bob, "Board Information", "Bob's Board Information");
  await waitForMembers((s) => s.leases.length === 0, "Bob's board lease to be released");
  note("the board lease is exclusive, names its holder when it refuses, and is released on close");

  // Per-stat: two different stats on one board, held at the same time.
  await moveTo(ada, OBJECT_CELL.x, OBJECT_CELL.y);
  await press(ada, "Enter");
  await screen(ada, (c) => !isEditorChrome(c), "Ada's stat dialog on the object");
  const objectStat = (await waitForMembers(
    (s) => s.leases.some((l) => l.kind === "stat" && l.holderName === "Ada Lovelace"),
    "Ada's stat lease on the object")).leases.find((l) => l.kind === "stat").statId;

  await moveTo(bob, OBJECT_CELL.x, OBJECT_CELL.y);
  await press(bob, "Enter");
  await screen(bob, (c) => hasText(c, "Ada Lovelace is editing this stat."),
    "the stat lease refusal, naming its holder");
  await closeWindow(bob, "is editing this stat", "the stat lease refusal");

  await moveTo(bob, LION_CELL.x, LION_CELL.y);
  await press(bob, "Enter");
  const twoLeases = await waitForMembers((s) => s.leases.filter((l) => l.kind === "stat").length === 2,
    "a second stat lease on the same board");
  assert.notEqual(twoLeases.leases[0].statId, twoLeases.leases[1].statId,
    "two stat leases on one board must be for two different stats");
  note(`leases are per stat: Ada holds stat ${objectStat}, Bob holds the lion's, on the same board`);
  await press(bob, "Escape");
  await screen(bob, (c) => isEditorChrome(c), "Bob's stat dialog to close");
  await waitForMembers((s) => s.leases.filter((l) => l.kind === "stat").length === 1,
    "Bob's stat lease to be released");

  // =========================================================================
  // Act 7 — a stat lease given back after a collaborator moved the engine
  //         (M16.14a (c), closed)
  // =========================================================================
  // Ada is still holding the object's stat lease. A lease key used to be
  // resolved against the SHARED ENGINE's current board, which follows whichever
  // member acted last (Apply -> focusMemberBoardLocked, M17.12), so a
  // collaborator switching boards moved the key out from under a lease that was
  // already held and the release that followed resolved to nothing. The key is
  // now the board that was ASKED FOR, exactly as the board lease's always was.
  await press(bob, "KeyB");
  await screen(bob, (c) => boardListOpen(c), "Bob's board switcher");
  await pickFromList(bob, (line) => line.startsWith(`${ANNEX_BOARD}:`), "Bob switching to the annex");
  await screen(bob, (c) => isEditorChrome(c), "Bob's editor on the annex");
  await waitForMembers((s) => memberFor(s, { accountId: "google:bob" })?.boardId === ANNEX_BOARD,
    "Bob on the annex, with the shared engine following him");

  await press(ada, "Escape");
  await screen(ada, (c) => isEditorChrome(c), "Ada's stat dialog to close");
  await waitForMembers((s) => s.leases.length === 0,
    "the stat lease, given back although another member had moved the engine");
  note("closing a stat dialog gives the lease back wherever the shared engine happens to be");

  // And it really is free: Bob comes back to the draft board and takes the stat
  // whose owner closed her dialog and walked away.
  await press(bob, "KeyB");
  await screen(bob, (c) => boardListOpen(c), "Bob's board switcher");
  await pickFromList(bob, (line) => line.startsWith(`${DRAFT_BOARD}:`), "Bob switching back");
  await screen(bob, (c) => isEditorChrome(c), "Bob's editor on the draft board");
  await moveTo(bob, OBJECT_CELL.x, OBJECT_CELL.y);
  await press(bob, "Enter");
  await screen(bob, (c) => !isEditorChrome(c), "Bob's stat dialog on the freed stat");
  {
    const cells = await readGrid(bob.page);
    assert.ok(!hasText(cells, "is editing this stat"),
      `the freed stat was refused to the next taker; the screen read:\n${gridToArt(cells)}`);
  }
  await waitForMembers((s) => s.leases.some((l) => l.kind === "stat" && l.holderName === "Bob Bones"),
    "the freed stat lease, taken by Bob");
  await press(bob, "Escape");
  await screen(bob, (c) => isEditorChrome(c), "Bob's stat dialog to close");
  await waitForMembers((s) => s.leases.length === 0, "Bob's stat lease to be released in turn");
  note("the released stat lease was taken by the next member to ask for it");

  // =========================================================================
  // Act 7b — two collaborators on two boards
  // =========================================================================
  for (const ed of [guest, bob]) {
    await press(ed, "KeyB");
    await screen(ed, (c) => boardListOpen(c), `${ed.label}'s board switcher`);
    await pickFromList(ed, (line) => line.startsWith(`${ANNEX_BOARD}:`), "switching to the annex");
    await screen(ed, (c) => isEditorChrome(c), "the editor after the switch");
  }
  await waitForMembers((s) => s.members.filter((m) => m.boardId === ANNEX_BOARD).length === 2,
    "Bob and the guest on the annex");

  // Ada's cursor is on the draft board, so it must not be drawn on theirs, and
  // the legend has to say where she is rather than pretend she is not there.
  await moveTo(bob, bob.park.x, bob.park.y);
  await setCursorPhase(bob, true);
  {
    const cells = await readGrid(bob.page);
    const adaSquare = boardCell(cells, 52, 6);
    assert.ok(!(adaSquare.ch === EDITOR_CURSOR_CHAR && adaSquare.color === adaColor),
      "a collaborator on another board must not leave a ghost cursor here");
  }
  await press(bob, "KeyW");
  await screen(bob, (c) => presencePanelOpen(c), "the legend on the annex");
  {
    const cells = await readGrid(bob.page);
    assert.ok(hasText(cells, "On other boards:"), "the legend separates members who are elsewhere");
    assert.ok(hasText(cells, "Ada Lovelace"), "and still names them");
  }
  await press(bob, "Escape");
  await screen(bob, (c) => !presencePanelOpen(c), "the legend to close");

  // Board leases are per board: Bob takes the annex's while Ada takes the
  // draft's, and both are granted.
  await press(bob, "KeyI");
  await screen(bob, (c) => hasText(c, "Board Information"), "Bob's Board Information on the annex");
  await press(ada, "KeyI");
  await screen(ada, (c) => hasText(c, "Board Information"), "Ada's Board Information on the draft board");
  {
    const state = await waitForMembers((s) => s.leases.filter((l) => l.kind === "board").length === 2,
      "one board lease per board");
    const boards = state.leases.filter((l) => l.kind === "board").map((l) => l.boardId).sort();
    assert.deepEqual(boards, [DRAFT_BOARD, ANNEX_BOARD],
      "the two board leases are for the two different boards");
    note("two collaborators hold two board leases at once, one per board");
  }
  await closeWindow(ada, "Board Information", "Ada's Board Information");
  await closeWindow(bob, "Board Information", "Bob's Board Information");
  await waitForMembers((s) => s.leases.length === 0, "both board leases to be released");

  // An edit on the annex, seen by the other member of the annex and by nobody
  // else's screen (M17.12: a diff is addressed to its own board).
  await moveTo(ada, ada.park.x, ada.park.y);
  const draftBefore = boardRegion(await setCursorPhase(ada, false));
  await moveTo(bob, ANNEX_CELL.x, ANNEX_CELL.y);
  await press(bob, "Space");
  const annexTile = await setLandmark(ANNEX_BOARD, ANNEX_CELL, "the wall Bob drew on the annex",
    (t) => t.element === E_NORMAL);
  await waitForTileOnScreen(guest, ANNEX_CELL.x, ANNEX_CELL.y,
    (cell) => cell.ch === NORMAL_CHAR && cell.color === annexTile.color, "Bob's annex wall");
  assert.deepEqual(boardRegion(await setCursorPhase(ada, false)), draftBefore,
    "an edit on the annex must not paint a cell on the draft board a collaborator is watching");
  note("an edit on one board reaches the members viewing that board and no one else");

  await checkpoint("two boards", [bob, guest]);

  // Bob and the guest come back to the draft board.
  for (const ed of [bob, guest]) {
    await press(ed, "KeyB");
    await screen(ed, (c) => boardListOpen(c), "the board switcher");
    await pickFromList(ed, (line) => line.startsWith(`${DRAFT_BOARD}:`), "switching back to the draft board");
    await screen(ed, (c) => isEditorChrome(c), "the editor after switching back");
  }
  await waitForMembers((s) => s.members.every((m) => m.boardId === DRAFT_BOARD),
    "everyone back on the draft board");

  // =========================================================================
  // Act 8 — two writers, one cell
  // =========================================================================
  // Ordered first, so "last write wins" is a fact and not a race: Ada's edit is
  // waited for, then Bob's lands on top of it.
  await moveTo(ada, RACE_CELL.x, RACE_CELL.y);
  await moveTo(bob, RACE_CELL.x, RACE_CELL.y);
  await press(ada, "Space");
  await setLandmark(DRAFT_BOARD, RACE_CELL, "the contested cell", (t) => t.color === adaTile.color);
  await press(bob, "Space");
  await setLandmark(DRAFT_BOARD, RACE_CELL, "the contested cell", (t) => t.color === bobTile.color);
  note("an ordered pair of edits on one cell leaves the later writer's tile");

  // Then genuinely simultaneous: both keystrokes go out with nothing awaited
  // between them. Which one wins is the server's business — that both browsers
  // and the session end up on the same answer is the invariant.
  //
  // This used to be racy under load (M16.14b): the session serialized the two
  // edits under its own lock, but each connection's goroutine broadcast its diff
  // AFTER releasing it, so the two diffs could reach a third browser in the
  // opposite order and leave it permanently showing the loser's tile. The
  // fan-out now goes out through the session's ordering gate, in the order the
  // session applied the edits, so the invariant below is strict.
  await Promise.all([press(ada, "Space"), press(bob, "Space")]);
  await waitForQuiet(ada.page, 3000);
  await waitForQuiet(bob.page, 3000);
  const contested = await editorBoard();
  const winner = sessionTile(contested, RACE_CELL.x, RACE_CELL.y);
  assert.ok(winner.color === adaTile.color || winner.color === bobTile.color,
    `the contested cell holds colour ${winner.color}, which neither author was drawing`);
  landmarks.set(`${DRAFT_BOARD}:${RACE_CELL.x}:${RACE_CELL.y}`, {
    board: DRAFT_BOARD, x: RACE_CELL.x, y: RACE_CELL.y, element: winner.element, color: winner.color,
    what: "the contested cell, after both authors wrote it at once",
  });
  for (const ed of [ada, bob, guest]) {
    await waitForTileOnScreen(ed, RACE_CELL.x, RACE_CELL.y, (cell) => cell.color === winner.color,
      "the contested cell settling on the session's answer");
  }
  note(`two simultaneous writes to one cell serialized to colour ${winner.color.toString(16)}, ` +
    "and all three screens followed it");

  await checkpoint("after the race", [ada, bob, guest]);

  // =========================================================================
  // Act 9 — an abrupt disconnect
  // =========================================================================
  // Ada takes a stat lease and opens the stat dialog, then her browser dies
  // without ever sending editorExit — the crash, the closed laptop, the lost
  // network. The session has to notice.
  await moveTo(ada, OBJECT_CELL.x, OBJECT_CELL.y);
  await press(ada, "Enter");
  await screen(ada, (c) => !isEditorChrome(c), "Ada's stat dialog");
  await waitForMembers((s) => s.leases.some((l) => l.kind === "stat" && l.holderName === "Ada Lovelace"),
    "Ada's stat lease before she disappears");

  assert.deepEqual(ada.pageErrors, [], "Ada's page must raise no uncaught errors");
  await ada.context.tracing.stop({ path: path.join("test-results", "m16-14-trace-Ada.zip") });
  await ada.browser.close();
  ada.closed = true;

  const survivors = await waitForMembers((s) => s.members.length === 2,
    "the session to drop the disconnected member", 30000);
  assert.ok(!memberFor(survivors, { accountId: "google:ada" }), "Ada's presence is gone");
  assert.equal(survivors.leases.length, 0, "an abrupt disconnect releases every lease its member held");

  // And the lease really is free: Bob takes the stat Ada was holding.
  await moveTo(bob, OBJECT_CELL.x, OBJECT_CELL.y);
  await press(bob, "Enter");
  await waitForMembers((s) => s.leases.some((l) => l.kind === "stat" && l.holderName === "Bob Bones"),
    "the freed stat lease, taken by Bob");
  await press(bob, "Escape");
  await screen(bob, (c) => isEditorChrome(c), "Bob's stat dialog to close");

  await press(guest, "KeyW");
  await screen(guest, (c) => presencePanelOpen(c), "the legend after the disconnect");
  {
    const cells = await readGrid(guest.page);
    assert.ok(!hasText(cells, "Ada Lovelace"), "the legend drops a member who disconnected");
  }
  await press(guest, "Escape");
  await screen(guest, (c) => !presencePanelOpen(c), "the legend to close");
  note("an abrupt disconnect released the lease and the presence entry, with no editorExit sent");

  // =========================================================================
  // Act 10 — board- and world-scoped changes, on every screen (M16.14a (a))
  // =========================================================================
  // Every per-cell edit above reached the other screens; nothing board-shaped
  // used to, because serveEditorBoard and the editorProperty case replied to the
  // acting client alone. They now fan out: the frame to the members watching
  // that board, and the world-scoped half — the switcher's board list — to
  // everybody.
  await press(bob, "KeyI");
  await screen(bob, (c) => hasText(c, "Board Information"), "Bob's Board Information");
  await pickFromList(bob, (line) => line.startsWith("Title: "), "the board title");
  await screen(bob, (c) => hasText(c, "New title for board"), "the board title prompt");
  await replaceEntry(bob, "Ada And Bob");
  await waitForSession((b) => b.properties.boardName === "Ada And Bob", "the renamed board");

  await press(guest, "KeyB");
  await screen(guest, (c) => boardListOpen(c), "the guest's board switcher");
  {
    const entries = await listEntries(guest);
    assert.ok(entries.includes("0: Ada And Bob"),
      `the guest's switcher shows ${JSON.stringify(entries)}; a rename another member made must reach it`);
    assert.ok(!entries.includes("0: Edit Draft"), "and must not leave the old name beside the new one");
    note("a board rename reached the collaborator's board list without them asking for anything");
  }
  await closeWindow(guest, boardListOpen, "the guest's board switcher");

  // The same routing, with a whole board at stake. The annex is cleared rather
  // than the draft board, so the run's landmarks survive it.
  for (const ed of [bob, guest]) {
    await press(ed, "KeyB");
    await screen(ed, (c) => boardListOpen(c), "the board switcher");
    await pickFromList(ed, (line) => line.startsWith(`${ANNEX_BOARD}:`), "switching to the annex");
    await screen(ed, (c) => isEditorChrome(c), "the editor on the annex");
  }
  await waitForMembers((s) => s.members.every((m) => m.boardId === ANNEX_BOARD),
    "both members on the annex");
  await moveTo(guest, guest.park.x, guest.park.y);
  const annexBefore = boardRegion(await setCursorPhase(guest, false));

  await press(bob, "KeyZ");
  await screen(bob, (c) => hasText(c, "Clear board?"), "the clear-board prompt");
  await press(bob, "KeyY");
  await waitForSession((b) => b.properties.boardId === ANNEX_BOARD &&
    sessionTile(b, ANNEX_CELL.x, ANNEX_CELL.y).element === E_EMPTY, "the cleared annex");
  landmarks.set(`${ANNEX_BOARD}:${ANNEX_CELL.x}:${ANNEX_CELL.y}`, {
    board: ANNEX_BOARD, x: ANNEX_CELL.x, y: ANNEX_CELL.y, element: E_EMPTY, color: 0,
    what: "the annex cell, after the board was cleared",
  });

  // The collaborator who was only watching has the cleared board on their screen,
  // without touching anything: the wall Bob drew on the annex in Act 7b is gone
  // from it, and their cursor stayed where they parked it.
  await moveTo(guest, guest.park.x, guest.park.y);
  const annexAfter = boardRegion(await setCursorPhase(guest, false));
  assert.notDeepEqual(annexAfter, annexBefore,
    "a board somebody else cleared must not leave the collaborator watching it with the old tiles");
  {
    const cells = await setCursorPhase(guest, false);
    const cleared = boardCell(cells, ANNEX_CELL.x, ANNEX_CELL.y);
    assert.notEqual(cleared.ch, NORMAL_CHAR,
      `the guest's screen still draws the wall the clear removed:\n${gridToArt(cells)}`);
    assert.ok(cursorAt(cells, guest.park.x, guest.park.y),
      "a broadcast repaint must not drag a collaborator's cursor to the acting member's");
  }
  note("Clear board repainted the collaborator watching the same board, and left their cursor alone");
  await checkpoint("after the clear", [bob, guest]);

  for (const ed of [bob, guest]) {
    await press(ed, "KeyB");
    await screen(ed, (c) => boardListOpen(c), "the board switcher");
    await pickFromList(ed, (line) => line.startsWith(`${DRAFT_BOARD}:`), "switching back to the draft board");
    await screen(ed, (c) => isEditorChrome(c), "the editor on the draft board");
  }

  // =========================================================================
  // Act 11 — test play, together, on a copy
  // =========================================================================
  const beforeTestPlay = await editorWorldBytes();
  files.beforeTestPlay = "before-test-play.ZZT";
  fs.writeFileSync(path.join(outDir, files.beforeTestPlay), beforeTestPlay);
  const instancesBefore = (await instances()).map((i) => i.name);

  await press(bob, "KeyS");
  await screen(bob, (c) => hasText(c, "World:"), "the world menu");
  await pickFromList(bob, "Test play together", "test play");
  // The reply is broadcast to the whole session, so the guest is taken along:
  // that is what "together" means.
  for (const ed of [bob, guest]) {
    await screen(ed, (c) => hasText(c, "Health:"), "the test-play board", 30000);
  }
  {
    const hosted = await waitFor(instances,
      (list) => list.some((i) => !instancesBefore.includes(i.name) && i.clients === 2),
      "one test-play instance holding both browsers");
    const testWorld = hosted.find((i) => !instancesBefore.includes(i.name) && i.clients === 2);
    assert.ok(/^TP[0-9A-F]{6}$/.test(testWorld.name),
      `the test-play world is named ${testWorld.name}, not a random TP world`);
    assert.equal(hosted.find((i) => i.name === WORLD)?.clients ?? 0, 0,
      "test play must not put anybody into the published world");
    note(`both browsers joined one test-play instance (${testWorld.name}); the editing world has no players`);
  }

  // Let the copy run. The fixture is full of creatures, so its board moves —
  // and none of it may reach the world still open in the editor.
  await idle(10);
  await waitForQuiet(bob.page, 3000);
  assert.ok((await editorWorldBytes()).equals(beforeTestPlay),
    "ten ticks of test play changed the editing world; it must run on a copy");
  note("ten ticks of co-op test play left the editing world byte-identical");

  for (const ed of [bob, guest]) await returnToEditor(ed);
  await waitForMembers((s) => s.members.length === 2, "both browsers back in the editor session");
  await checkpoint("back from test play", [bob, guest]);

  // =========================================================================
  // Act 12 — publishing over a world somebody is playing
  // =========================================================================
  await leaveEditor(guest);
  await press(guest, "KeyP");
  await screen(guest, (c) => hasText(c, "Health:"), "the guest playing the published world", 30000);
  await waitFor(instances, (list) => list.some((i) => i.name === WORLD && i.clients === 1),
    `a player inside ${WORLD}`);

  const diskBefore = await worldsOnDisk();
  const publishedBefore = diskBefore.files.find((f) => f.name === `${WORLD}.ZZT`);
  await press(bob, "KeyS");
  await screen(bob, (c) => hasText(c, "World:"), "the world menu");
  await pickFromList(bob, "Save and publish", "the refused publish");
  await screen(bob, (c) => hasText(c, "Save world as:"), "the publish prompt");
  await replaceEntry(bob, WORLD);
  await screen(bob, (c) => hasText(c, "Cannot save"), "the occupancy refusal");
  {
    const cells = await readGrid(bob.page);
    assert.ok(hasText(cells, "being played"),
      `the refusal must say why; the window read:\n${gridToArt(cells)}`);
  }
  await closeWindow(bob, "Cannot save", "the occupancy refusal");
  {
    const diskAfter = await worldsOnDisk();
    const publishedAfter = diskAfter.files.find((f) => f.name === `${WORLD}.ZZT`);
    assert.deepEqual(publishedAfter, publishedBefore,
      "a refused publish must leave the world on disk byte for byte where it was");
    assert.deepEqual(diskAfter.access[WORLD], diskBefore.access[WORLD],
      "a refused publish must not rewrite the access sidecar either");
    note(`publishing over ${WORLD} was refused while it was being played, and its bytes did not move`);
  }

  // =========================================================================
  // The report
  // =========================================================================
  fs.writeFileSync(
    path.join(outDir, "report.json"),
    JSON.stringify({ checkpoints, findings, notes, files }, null, 2) + "\n",
  );

  for (const ed of editors) {
    if (ed.closed) continue;
    assert.deepEqual(ed.pageErrors, [], `${ed.label}: the page must raise no uncaught errors`);
    // Chromium logs a console error when a socket the page is closing is closed
    // by the server at the same moment — which is exactly what leaving the
    // editor and starting test play both do. It is the orderly shutdown.
    const unexpected = ed.consoleErrors.filter(
      (text) => !/WebSocket connection to .* failed: Close received after close/.test(text),
    );
    assert.deepEqual(unexpected, [], `${ed.label}: the page must log no console errors`);
  }
  assert.deepEqual(findings, [],
    "M16.14a closed every divergence this sweep filed; a new one is a new gap task, not a passing run");
  console.log(`\ncollaborative editor: ${checkpoints.length} convergence checkpoints, no divergence`);
} catch (err) {
  failed = true;
  for (const ed of editors) {
    if (ed.closed) continue;
    try {
      saveText(`m1614-failure-${ed.label}.txt`, gridToArt(await readGrid(ed.page)));
    } catch {
      // The page may be gone; the traces below are the fallback.
    }
  }
  try {
    fs.writeFileSync(path.join(outDir, "report.json"),
      JSON.stringify({ checkpoints, findings, notes, files }, null, 2) + "\n");
  } catch {
    // Nothing more to salvage.
  }
  console.error(err);
} finally {
  for (const ed of editors) {
    if (ed.closed) continue;
    try {
      await ed.context.tracing.stop({ path: path.join("test-results", `m16-14-trace-${ed.label}.zip`) });
    } catch {
      // A browser that already died has no trace to write.
    }
    await ed.browser.close();
  }
  if (failed) process.exit(1);
}
