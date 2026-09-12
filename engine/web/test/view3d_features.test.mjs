// view3d_features.test.mjs — M35.2: the 3D view's vocabulary, end to end.
//
// Driven by engine/m35_2_test.go, which hosts fixtures/view3d.zwd on the
// production server objects with the tick loop under this script's control.
//
// WHY THIS EXISTS, GIVEN M35.1. That suite proves the toggle: V clears the
// board columns to transparent, the three.js chunk arrives, and the page comes
// back pixel-identical. That is the smallest true thing and it is not the
// feature. A player who presses 3 meets a vocabulary -- which keys walk, which
// keys only move the camera, which keys must never reach the wire at all, what
// the sidebar promises while they are standing in the board, and what the view
// says that the text screen cannot say. None of that is covered by a toggle.
//
// Every assertion below is made on the decoded canvas, on a screenshot, or on
// the server's own view of the player. None is made on a module's return value:
// the client keeps the 3D view in module scope, and a test that could reach in
// and read `view3d.rig` would be testing the object rather than the client.
//
// THE CLOCK. M35.1 runs on the real clock because the view draws on
// requestAnimationFrame and a frozen clock never draws a first frame. This
// suite needs the tick lock instead -- `pending` is the only place a stray
// input frame can hide, and without a fake clock the 55ms sampler fills it with
// repeats. Playwright's clock fakes rAF along with everything else, so the
// frames are still there; they are taken by advancing the clock on purpose
// (`frames()` below) rather than by waiting.

import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import {
  baseURL,
  command,
  hasText,
  idle,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  markProfileWarm,
  pauseClock,
  pressExpectingNoInput,
  readGrid,
  runClock,
  serverState,
  step,
  tickUntilGrid,
  textAt,
  waitForGrid,
  walk,
} from "./lib/canvas.mjs";

assert.ok(baseURL, "BASE_URL must be set by the harness");

const OUT_DIR = process.env.M352_OUT || join("test-results", "view3d-features");
mkdirSync(OUT_DIR, { recursive: true });

const COLS = 80;
const ROWS = 25;
const BOARD_COLS = 60;

// The sidebar rows the 3D view writes (main.ts drawView3DRows). Read from the
// screen, at the columns the client writes them to, so a row that moved is a
// failure rather than a silently missed assertion.
const ROW_VIEW = 13;
const ROW_SAVE = 21;
const ROW_LOOK = 24;

// ZZT's save key, as the server receives it: a raw key byte, not a mask.
const KEY_S = "S".charCodeAt(0);

// What the server must see for each arrow while the body faces EAST. The
// client rotates the mask; these are the ordinary board directions that come
// out the other side, and they are what `walk` would have awaited for a
// different key entirely -- which is the whole point.
const EAST_FORWARD = { dx: 1, dy: 0, key: 0xcd }; // up    -> east
const EAST_BACK = { dx: -1, dy: 0, key: 0xcb }; // down  -> west
const EAST_STRAFE_LEFT = { dx: 0, dy: -1, key: 0xc8 }; // left  -> north
const EAST_STRAFE_RIGHT = { dx: 0, dy: 1, key: 0xd0 }; // right -> south

/**
 * Hold one arrow for exactly one tick and await the ROTATED frame.
 *
 * `walk` derives what to await from the key it pressed, which is precisely the
 * assumption this section is testing, so it cannot be used here: pressing up
 * while facing east must arrive as east, and a helper that waited for north
 * would hang whether the feature worked or not.
 */
async function walkFacing(page, code, expected) {
  await page.keyboard.down(code);
  await step({ await: expected, timeoutMs: 8000 });
  await page.keyboard.up(code);
  await step({ await: { dx: 0, dy: 0 } });
}

/** Advance the fake clock, which is what takes animation frames here. */
async function frames(page, ms = 250) {
  await runClock(page, ms);
}

/** The server's view of our player — position, not pixels. */
async function me() {
  const state = await serverState();
  assert.equal(state.players.length, 1, `expected exactly one player, saw ${JSON.stringify(state.players)}`);
  return state.players[0];
}

async function assertAt(x, y, what) {
  const player = await me();
  assert.deepEqual({ x: player.x, y: player.y }, { x, y }, `${what}: player should be at ${x},${y}`);
}

/** Alpha of one screen cell's centre, off the 2D canvas itself (M35.1's probe). */
async function cellAlpha(page, cx, cy) {
  return page.evaluate(
    ({ cx, cy, COLS, ROWS }) => {
      const canvas = document.querySelector("canvas[data-screen]");
      const ctx = canvas.getContext("2d");
      const cw = canvas.width / COLS;
      const ch = canvas.height / ROWS;
      return ctx.getImageData(Math.floor((cx + 0.5) * cw), Math.floor((cy + 0.5) * ch), 1, 1).data[3];
    },
    { cx, cy, COLS, ROWS },
  );
}

/**
 * A screenshot of the board region only — the window the world is drawn in.
 *
 * Clipped to the board columns rather than the page, because the sidebar is
 * drawn by drawScreen either way and a full-page shot would call a sidebar
 * repaint "the view changed". The clip is measured off the screen canvas, which
 * is letterboxed inside its wrap.
 */
async function boardShot(page, name) {
  const box = await page.locator("canvas[data-screen]").boundingBox();
  const buffer = await page.screenshot({
    clip: { x: box.x, y: box.y, width: (box.width * BOARD_COLS) / COLS, height: box.height },
  });
  if (name) writeFileSync(join(OUT_DIR, `${name}.png`), buffer);
  return buffer;
}

const sameShot = (a, b) => Buffer.compare(a, b) === 0;

/** The sidebar row text, from the sidebar columns only. */
const sidebarRow = (cells, row) => textAt(cells, 60, row, COLS - 60).trimEnd();

async function in3D(page) {
  return page.locator("canvas[data-view3d]").evaluate((el) => el.hidden === false);
}

async function waitFor3D(page, want, describe) {
  await page.waitForFunction(
    (w) => (document.querySelector("canvas[data-view3d]")?.hidden === false) === w,
    want,
    { timeout: 15000 },
  );
  await frames(page);
  assert.equal(await in3D(page), want, describe);
}

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
await markProfileWarm(context);
let failed = false;

try {
  await installImageProbe(page);

  const chunks = [];
  page.on("response", (response) => {
    const url = response.url();
    if (url.endsWith(".js")) chunks.push(url.split("/").pop());
    if (response.status() >= 400) consoleErrors.push(`HTTP ${response.status()} ${url}`);
  });

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Standing");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("VIEW3D");
  await waitForGrid(page, (cells) => hasText(cells, "VIEW3D"), "the picker to match VIEW3D");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "P  Play"), "the title screen for VIEW3D");
  await pauseClock(page);
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await assertAt(6, 12, "the fixture's start");

  // =========================================================================
  // 1. The client opens on the text screen, and says where 3 goes
  // =========================================================================
  //
  // ZZTMMO is a text game and the board it draws is the real one, so 3D is
  // somewhere a player chooses to go. The row is the whole discoverability of
  // the feature: this view shipped once without one and nobody could find it.
  assert.equal(await in3D(page), false, "the client must open on the text screen");
  assert.equal(await cellAlpha(page, 10, 12), 255, "the board is painted on the text screen");

  let cells = await readGrid(page);
  assert.match(sidebarRow(cells, ROW_VIEW), /3\s+3D view/, "row 13 must offer the 3D view");
  assert.match(sidebarRow(cells, ROW_SAVE), /S\s+Save game/, "row 21 must promise Save on the text screen");
  assert.equal(sidebarRow(cells, ROW_LOOK), "", "row 24 must be blank outside the world");
  const classicShot = await boardShot(page, "01-classic");

  // =========================================================================
  // 2. Both keys toggle, and the sidebar changes its promises with the view
  // =========================================================================
  //
  // 3 is the shortcut the sidebar advertises; V still works for the hands that
  // learned it first. A feature reachable by only one of the two keys the
  // client documents is half-shipped.
  const chunksBefore = chunks.length;
  await page.keyboard.press("Digit3");
  await waitFor3D(page, true, "3 must open the world");
  assert.ok(chunks.length > chunksBefore, `3 must fetch the 3D chunk; saw ${chunks.join(", ")}`);
  assert.equal(await cellAlpha(page, 10, 12), 0, "the board columns must be a hole in 3D");
  assert.equal(await cellAlpha(page, BOARD_COLS + 8, 12), 255, "the sidebar is not part of the world");

  cells = await readGrid(page);
  assert.match(sidebarRow(cells, ROW_VIEW), /3\s+Standard view/, "row 13 must offer the way back");
  assert.match(sidebarRow(cells, ROW_SAVE), /S\s+Save: press 3/, "row 21 must stop promising Save in the world");
  assert.match(sidebarRow(cells, ROW_LOOK), /WASD\s+Look/, "row 24 must name the camera keys in the world");
  const worldShot = await boardShot(page, "02-world");
  assert.ok(!sameShot(classicShot, worldShot), "the board region must actually look different in 3D");

  await page.keyboard.press("Digit3");
  await waitFor3D(page, false, "3 again must put the text screen back");
  await page.keyboard.press("KeyV");
  await waitFor3D(page, true, "V must open the world too");
  await assertAt(6, 12, "toggling the view");

  // =========================================================================
  // 3. The arrows walk, and facing north they walk the text screen's way
  // =========================================================================
  //
  // The arrows ALWAYS move you -- that is 1f91e4d's decision and it stands. The
  // frame they are read in is what follows the camera, and facing north is the
  // identity, so at eye level looking north the vocabulary is exactly the text
  // screen's. Section 3a turns and shows the frame move.
  await walk(page, "ArrowRight", 3);
  await assertAt(9, 12, "three arrow steps east in the 3D view");
  await walk(page, "ArrowDown", 1);
  await assertAt(9, 13, "an arrow step south in the 3D view");
  await walk(page, "ArrowUp", 1);
  await assertAt(9, 12, "an arrow step back north");

  // =========================================================================
  // 3a. Turn, and the arrows come with you
  // =========================================================================
  //
  // Standing in the board you are a body, not a map reader. An arrow that walked
  // you sideways across your own field of view is the one thing a first-person
  // camera cannot promise, so up walks the way you look, down walks backwards,
  // and left and right step sideways WITHOUT turning -- turning is D, which is
  // a camera key and puts nothing on the wire.
  //
  // The client resolves all four against your facing and sends an ordinary
  // board direction, because six bits is all the wire has. So the assertions
  // are made on the server's own view of where the player went.
  const turned = await pressExpectingNoInput(page, "KeyD");
  assert.deepEqual(turned.pending, [], "turning must put nothing on the wire");
  await frames(page, 400);

  // Facing EAST now. Each arrow, and where the body actually ended up.
  await walkFacing(page, "ArrowLeft", EAST_STRAFE_LEFT);
  await assertAt(9, 11, "facing east, left steps north");
  await walkFacing(page, "ArrowRight", EAST_STRAFE_RIGHT);
  await assertAt(9, 12, "facing east, right steps south");
  await walkFacing(page, "ArrowUp", EAST_FORWARD);
  await assertAt(10, 12, "facing east, up walks east");
  await walkFacing(page, "ArrowDown", EAST_BACK);
  await assertAt(9, 12, "facing east, down walks west without turning round");

  // Turn back north, where the frame is the identity again, so the rest of this
  // script reads in the vocabulary the text screen uses.
  await pressExpectingNoInput(page, "KeyA");
  await frames(page, 400);
  await walk(page, "ArrowRight", 1);
  await assertAt(10, 12, "facing north again, right is east again");
  await walk(page, "ArrowLeft", 1);
  await assertAt(9, 12, "back where section 3 left off");

  // =========================================================================
  // 4. WASD moves the camera and NEVER reaches the wire
  // =========================================================================
  //
  // The certified row input.play-wasd-removed (M16.10) wants W/A/D inert on the
  // text screen. In the world they are not inert -- they are the camera -- and
  // they must still put nothing on the wire. S joins them here, which is the
  // binding this view takes away: it is ZZT's save key everywhere else.
  const beforeLook = await boardShot(page);
  for (const code of ["KeyW", "KeyS", "KeyA", "KeyD"]) {
    const state = await pressExpectingNoInput(page, code);
    assert.deepEqual(state.pending, [], `${code} must send no input frame in 3D, saw ${JSON.stringify(state.pending)}`);
  }
  await idle(1);
  await assertAt(9, 12, "after WASD in the 3D view");
  // S must not open the save prompt here, and the camera must have moved: four
  // keys that send nothing AND do nothing would pass the assertion above.
  cells = await readGrid(page);
  assert.ok(!hasText(cells, "Save game:"), "S must look down in the world, not open the save prompt");
  await frames(page);
  assert.ok(!sameShot(beforeLook, await boardShot(page, "03-after-wasd")), "WASD must move the camera");

  // =========================================================================
  // 5. On the text screen, S is ZZT's save key again
  // =========================================================================
  //
  // The other half of the same decision, and the reason row 21 changes: a key
  // cannot be both, so S saves everywhere except in the world.
  await page.keyboard.press("KeyV");
  await waitFor3D(page, false, "back to the text screen");
  await command(page, "KeyS", KEY_S);
  await tickUntilGrid(page, (cells) => hasText(cells, "Save game:"), "S to open the save prompt on the text screen");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Save game:"), "the save prompt to close");
  await assertAt(9, 12, "after saving from the text screen");
  await page.keyboard.press("KeyV");
  await waitFor3D(page, true, "back into the world");

  // =========================================================================
  // 6. The sign reads itself out, at eye level, and only the one you are at
  // =========================================================================
  //
  // A sign is a row of letters lying flat on the floor, and from eye height a
  // row of letters is edge-on: in the world it is a coloured wall and nothing
  // more. So the one you are standing at is written along the bottom of the
  // board, in its own colours. The fixture's sign is at 10..14,11 and the path
  // is the row below it.
  const signRow = ROWS - 1;
  await walk(page, "ArrowLeft", 2); // 9,12 -> 7,12, then east along the sign
  await walk(page, "ArrowRight", 4);
  await assertAt(11, 12, "standing at the sign");
  await frames(page);
  cells = await readGrid(page);
  assert.ok(
    textAt(cells, 0, signRow, BOARD_COLS).includes("ZZT3D"),
    `the sign you are standing at must read itself out along the bottom; row ${signRow} is ` +
      JSON.stringify(textAt(cells, 0, signRow, BOARD_COLS)),
  );
  await boardShot(page, "06-sign-readout");

  // =========================================================================
  // 7. F steps back out and in, and sends nothing
  // =========================================================================
  const beforeF = await boardShot(page);
  let state = await pressExpectingNoInput(page, "KeyF");
  assert.deepEqual(state.pending, [], `F must send no input frame, saw ${JSON.stringify(state.pending)}`);
  await frames(page, 600); // the camera glides rather than cutting
  assert.ok(!sameShot(beforeF, await boardShot(page, "04-after-F")), "F must move the camera out of the body");
  await assertAt(11, 12, "after F");
  await page.keyboard.press("KeyF");
  await frames(page, 600);
  await boardShot(page, "05-back-at-eye-level");

  // =========================================================================
  // 8. Ghosting — G leaves your body, and nothing is sent while you are out
  // =========================================================================
  //
  // view3d/index.ts:97-107: "Your card stays on the board ... while the camera
  // drifts off through the walls. Nothing is sent while you are out there, so a
  // ghost is a way of looking and never a way of reaching."
  //
  // All three clauses are asserted here, and the third is what makes the first
  // two testable at all. While a ghost was still putting input on the wire, a
  // changed board region proved only that SOMETHING moved -- the body was
  // walking under the same arrow. With the wire silent, a view that changes
  // while the body does not is the camera and can be nothing else.
  await assertAt(11, 12, "standing at the sign before ghosting");
  const beforeG = await boardShot(page, "09-before-G");
  state = await pressExpectingNoInput(page, "KeyG");
  assert.deepEqual(state.pending, [], `G itself must send nothing, saw ${JSON.stringify(state.pending)}`);
  await frames(page, 600);
  const ghostShot = await boardShot(page, "10-ghosted");
  assert.ok(!sameShot(beforeG, ghostShot), "G must move the camera out of the body");
  await assertAt(11, 12, "the body must stay put when the camera leaves");

  // The eyes went with the camera, so the ghost still reads the sign it drifted
  // away from -- signAtEye reads from ghostAt while ghosted.
  cells = await readGrid(page);
  assert.ok(
    textAt(cells, 0, signRow, BOARD_COLS).includes("ZZT3D"),
    "a ghost reads the sign it is standing at, because reading is something eyes do",
  );

  // Nothing on the wire. The arrow is HELD and the frame is AWAITED, because
  // pressExpectingNoInput cannot see a movement key: the keyup's own zero frame
  // overwrites the keydown's in the server's one-entry input slot, so a press
  // and release reads as "nothing was sent" whether or not anything was. If the
  // promise holds, no frame ever arrives and this step times out.
  await page.keyboard.down("ArrowLeft");
  let arrowReachedTheWire = true;
  try {
    await step({ await: { dx: -1, dy: 0, key: 0xcb }, timeoutMs: 3000 });
    arrowReachedTheWire = true;
  } catch {
    arrowReachedTheWire = false;
  }
  assert.equal(arrowReachedTheWire, false, "an arrow held while ghosted must reach the server as nothing at all");

  // ... and while it was held, the camera flew. The body is the control: it has
  // not moved, so the board region can only have changed because the camera did.
  await frames(page, 900);
  const driftShot = await boardShot(page, "11-ghost-drifted");
  await page.keyboard.up("ArrowLeft");
  assert.ok(!sameShot(ghostShot, driftShot), "a held arrow must fly the ghost");
  await assertAt(11, 12, "and the body it left must not have moved a square");

  // G brings you home.
  await page.keyboard.press("KeyG");
  await frames(page, 900);
  await boardShot(page, "12-home");
  await assertAt(11, 12, "home from the ghost, still where the body was standing");

  // =========================================================================
  // 9. A fake wall is floor, and the wall it imitates is not
  // =========================================================================
  //
  // The one thing a glyph cannot say. ElementDefs gives the fake the normal
  // wall's own character on purpose, so 18,12 and 26,12 arrive at the client as
  // the same two bytes; only ScreenCell.element tells them apart. The player
  // walks through one and stops at the other, which is the behaviour the view
  // has to draw.
  await walk(page, "ArrowRight", 7);
  await assertAt(18, 12, "walking onto the fake wall at 18,12");
  await boardShot(page, "12-standing-on-the-fake");
  await walk(page, "ArrowRight", 7);
  await assertAt(25, 12, "walking up to the normal wall at 26,12");
  await walk(page, "ArrowRight", 2); // two more frames, into the wall
  await assertAt(25, 12, "the normal wall must stop the walk");
  await boardShot(page, "13-at-the-wall");

  // ... and the other half of "walk up to it and it appears": eleven columns
  // east of the sign, with a wall in between, it is gone. Every sign on the
  // board written out at once would be noise.
  await frames(page);
  cells = await readGrid(page);
  assert.ok(
    !textAt(cells, 0, ROWS - 1, BOARD_COLS).includes("ZZT3D"),
    `the sign must stop reading itself out once you have walked away; row ${ROWS - 1} is ` +
      JSON.stringify(textAt(cells, 0, ROWS - 1, BOARD_COLS)),
  );

  console.log(`view3d_features.test.mjs: the 3D vocabulary, shots in ${OUT_DIR}`);
} catch (error) {
  failed = true;
  console.error(error);
} finally {
  await browser.close();
}

if (pageErrors.length > 0 || consoleErrors.length > 0) {
  console.error("page errors:", pageErrors, consoleErrors);
  failed = true;
}
process.exit(failed ? 1 : 0);
