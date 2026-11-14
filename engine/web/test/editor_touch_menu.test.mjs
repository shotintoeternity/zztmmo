// M39.2 — the editor's ☰ menu, in a real browser on a touch profile.
//
// Driven by engine/m39_2_browser_test.go, which hosts EDIT (fixtures/editor.zwd)
// on the PRODUCTION zzt-server binary and runs this script against it.
//
// WHAT THIS PROVES THAT touch_controls.test.mjs CANNOT. The unit test owns the
// rows and their gating against a fake DOM. Only a browser shows that a tapped
// row reaches main.ts's editor handler, that the server places the tile, and
// that the F1 picker (a sidebar picker answered by a letter a phone cannot
// type) is answered by its element's name in the menu.
//
// The editor is opened by tapping the title bar's Editor (M39.3), so after the
// launch name everything is touch.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";
import { cellAt, gridToArt, hasText, installDecoder, installImageProbe, markProfileWarm, readGrid, textAt, waitForGrid } from "./lib/canvas.mjs";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const resultsDir = path.resolve(process.env.M392_OUT || "test-results/editor-touch-menu");
fs.mkdirSync(resultsDir, { recursive: true });

const WORLD = "EDIT";
// A fresh editor's brush is a yellow Solid. A full block has no glyph edge to
// read, so the decoder reports it as a uniform cell, yellow on yellow.
const isYellowSolid = (cell) => cell.uniform && cell.color === 0xee;
const AMMO = 0x84; // ElementDefs[E_AMMO].Character

const isEditorChrome = (cells) => textAt(cells, 62, 1, 15) === "  ZZT Editor   ";
const boardCell = (cells, x, y) => cellAt(cells, x - 1, y - 1);
const readoutRow = (cells) => textAt(cells, 61, 20, 19).trim();
function cursorPos(cells) {
  const m = /^(?:Pos: )?(\d+),(\d+)/.exec(readoutRow(cells));
  return m ? { x: Number(m[1]), y: Number(m[2]) } : null;
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 844, height: 390 }, hasTouch: true, isMobile: true });
await markProfileWarm(context);
const page = await context.newPage();
const pageErrors = [];
page.on("pageerror", (err) => pageErrors.push(String(err)));

const bar = (id) => page.locator(`.touch-controls button[data-touch="${id}"]`);
const row = (id) => page.locator(`.touch-menu button[data-touch="${id}"]`);
const menuOpen = () => page.locator(".touch-menu").isVisible();
const visibleRows = () =>
  page.evaluate(() =>
    Array.from(document.querySelectorAll(".touch-menu button"))
      .filter((b) => b.offsetParent !== null)
      .map((b) => b.textContent),
  );

/** ☰, then a row: the two taps a phone makes. */
async function viaMenu(id) {
  await bar("more").tap();
  await row(id).waitFor({ state: "visible", timeout: 5000 });
  await row(id).tap();
}

try {
  await installImageProbe(page);
  // /play/EDIT names the world, so the launch flow asks only for a name.
  const response = await page.goto(`${baseURL}/play/${WORLD}`);
  assert.equal(response?.status(), 200, "the client index must be served");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Tess");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "E  Board editor"), "the title screen for EDIT");

  // M39.3: the title bar's Editor, so from the name onward this is all touch.
  assert.equal(await bar("editor").isVisible(), true, "the title bar offers Editor");
  await bar("editor").tap();
  let cells = await waitForGrid(page, (c) => isEditorChrome(c) && cursorPos(c) !== null, "the editor and its cursor readout");
  const start = cursorPos(cells);
  assert.deepEqual(start, { x: 30, y: 12 }, "the editor cursor starts at 30,12 on EDIT's draft board");
  // The cursor is drawn over the tile under it, so a tile is only read once the
  // cursor has moved off it.
  assert.ok(!isYellowSolid(boardCell(cells, 31, 12)), "31,12 starts empty");
  assert.notEqual(boardCell(cells, 32, 12).ch, AMMO, "32,12 starts empty");

  // The editor's bar is the pad, ⌨, Enter and ☰.
  assert.equal(await bar("more").isVisible(), true, "☰ is on the editor's bar");
  assert.equal(await menuOpen(), false, "the menu starts closed");
  await bar("more").tap();
  assert.equal(await menuOpen(), true, "☰ opens the menu");
  const rows = await visibleRows();
  for (const label of ["Plot", "Draw on", "Pattern", "Color", "Fill", "Item", "Creature", "Terrain", "Switch boards", "Board info", "World", "Clear board", "New world", "Help", "Quit"]) {
    assert.ok(rows.includes(label), `the editor menu offers ${label}; it offered ${JSON.stringify(rows)}`);
  }
  for (const label of ["Save", "3D view", "Back"]) {
    assert.ok(!rows.includes(label), `the editor menu must not offer ${label}`);
  }
  // The panel must fit above the bar: none of it may be off the top of the screen.
  const box = await page.locator(".touch-menu").boundingBox();
  assert.ok(box && box.y >= 0, `the menu runs off the top of a landscape phone: ${JSON.stringify(box)}`);
  await bar("more").tap();
  assert.equal(await menuOpen(), false, "a second ☰ closes it");

  const stepRight = async (x) => {
    await bar("right").tap();
    await waitForGrid(page, (c) => {
      const at = cursorPos(c);
      return !!at && at.x === x && at.y === 12;
    }, `the cursor at ${x},12, by the pad`);
  };

  // --- Plot: the brush goes down at the cursor, by the server's hand ---------
  await stepRight(31);
  await viaMenu("edit-plot");
  assert.equal(await menuOpen(), false, "a row tap closes the menu");
  await stepRight(32);
  cells = await waitForGrid(page, (c) => isYellowSolid(boardCell(c, 31, 12)), "the plotted Solid at 31,12");

  // --- F1, answered by name ---------------------------------------------------
  await viaMenu("edit-item");
  // The picker opens the menu by itself and lists its elements.
  await page.locator(".touch-menu-picker button", { hasText: "Ammo" }).waitFor({ state: "visible", timeout: 5000 });
  const pickerRows = await visibleRows();
  assert.ok(pickerRows.includes("Back"), "Back is offered while the picker is up");
  assert.ok(!pickerRows.includes("Plot"), "the editor's commands step aside for the picker");
  await page.locator(".touch-menu-picker button", { hasText: "Ammo" }).tap();
  assert.equal(await menuOpen(), false, "picking closes the menu");
  await stepRight(33);
  cells = await waitForGrid(page, (c) => boardCell(c, 32, 12).ch === AMMO, "Ammo placed at 32,12 through the picker");
  assert.ok(isYellowSolid(boardCell(cells, 31, 12)), "the plotted tile is still there");

  // --- Back closes a picker without choosing ---------------------------------
  await viaMenu("edit-creature");
  await row("edit-back").waitFor({ state: "visible", timeout: 5000 });
  await row("edit-back").tap();
  await waitForGrid(page, (c) => hasText(c, "Switch boards"), "the command block back after Back");
  assert.equal(await menuOpen(), false, "the menu goes with the picker");

  // --- Draw mode says what a tap does now -------------------------------------
  await viaMenu("edit-draw");
  await waitForGrid(page, (c) => textAt(c, 68, 24, 11) === "Drawing on ", "draw mode on");
  await bar("more").tap();
  assert.ok((await visibleRows()).includes("Draw off"), "the draw row now offers to switch it off");
  await row("edit-draw").tap();
  await waitForGrid(page, (c) => textAt(c, 68, 24, 11) === "Drawing off", "draw mode off");

  // --- Quit, and decline to save, all by touch --------------------------------
  await viaMenu("edit-quit");
  await waitForGrid(page, (c) => hasText(c, "Save first?"), "the editor's save prompt");
  await bar("no").tap();
  await waitForGrid(page, (c) => hasText(c, "E  Board editor"), "the title screen after the editor");
  assert.equal(await bar("more").isVisible(), false, "☰ is not on the title bar");

  assert.deepEqual(pageErrors, [], "no page errors");
  console.log("=== M39.2 editor touch menu: PASS ===");
} catch (err) {
  console.error(err);
  try {
    await page.screenshot({ path: path.join(resultsDir, "failure.png") });
    fs.writeFileSync(path.join(resultsDir, "failure.txt"), gridToArt(await readGrid(page)));
  } catch {
    // a dead page has nothing more to say
  }
  process.exitCode = 1;
} finally {
  await browser.close();
}
