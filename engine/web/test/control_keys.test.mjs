// M16.10 — the browser's play-mode control and modal vocabulary, end to end.
//
// Driven by engine/m16_10_test.go (TestM1610BrowserControlVocabulary), which
// hosts the CONTROL world (fixtures/control.zwd) on the production server
// objects with the tick loop under this script's control.
//
// WHY THIS EXISTS. engine/web/test/{modal,title,keys}.test.mjs bundle a single
// client module under Node and call its exported functions. That is useful, and
// it is not evidence that a KEY WORKS: it cannot catch a handler that never
// registers, a modal that swallows the key before the router sees it, an
// event.preventDefault that never fires, or a keymask the server decodes
// differently from the client that built it. Everything below is a real
// KeyboardEvent delivered to the real canvas of the built application, and every
// assertion is made on either the decoded canvas or the server's own view of the
// player — never on a module's return value.
//
// Each numbered section names the parity-manifest row it is the evidence for.

import assert from "node:assert/strict";

import {
  baseURL,
  cellAt,
  command,
  gridToArt,
  hasText,
  idle,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  pauseClock,
  pressExpectingNoInput,
  readGrid,
  runClock,
  saveText,
  serverState,
  shootShift,
  shootSpace,
  textAt,
  tickUntilGrid,
  waitForGrid,
  waitForQuiet,
  walk,
} from "./lib/canvas.mjs";

const KEY_T = "T".charCodeAt(0);
const KEY_P = "P".charCodeAt(0);
const KEY_B = "B".charCodeAt(0);
const KEY_S = "S".charCodeAt(0);
const KEY_Q = "Q".charCodeAt(0);
const KEY_H = "H".charCodeAt(0);

// The text window's current line is always drawn on the same screen row:
// drawLine puts lpos === linePos at TEXT_WINDOW_Y + HEIGHT/2 + 1 = 13
// (web/src/textwindow.ts). That row is this script's linePos probe.
const WINDOW_CURSOR_ROW = 13;

const BOARD_COLS = 60;

/** The server's view of our player — position and inventory, not pixels. */
async function me() {
  const state = await serverState();
  assert.equal(state.players.length, 1, `expected exactly one player, saw ${JSON.stringify(state.players)}`);
  return state.players[0];
}

async function assertAt(x, y, what) {
  const player = await me();
  assert.deepEqual({ x: player.x, y: player.y }, { x, y }, `${what}: player should be at ${x},${y}`);
  return player;
}

/** The text of the line the text window currently has selected. */
async function windowCursorLine(page) {
  return textAt(await readGrid(page), 0, WINDOW_CURSOR_ROW).trim();
}

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
let failed = false;

try {
  await installImageProbe(page);
  await context.tracing.start({ screenshots: true, snapshots: true });
  page.on("response", async (response) => {
    if (response.status() >= 400) {
      consoleErrors.push(`HTTP ${response.status()} ${response.url()}`);
    }
  });

  // =========================================================================
  // Join, through the production launch flow
  // =========================================================================
  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served, not the build-me 404 page");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Ctrl");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("CONTROL");
  await waitForGrid(page, (cells) => hasText(cells, "CONTROL"), "the picker to match CONTROL");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "P  Play"), "the title screen for CONTROL");

  // From here the page clock is frozen: nothing animates or polls unless this
  // script advances it, and the 55ms input sampler fires only when we say so.
  await pauseClock(page);
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await idle(2);
  await waitForQuiet(page);
  await assertAt(6, 12, "the CONTROL start position");

  // =========================================================================
  // 1. input.play-move — arrows AND the numeric keypad
  // =========================================================================
  // Walking east onto the ammo proves the arrow path all the way through:
  // keydown -> keymask -> inputMessageToPlayerInput -> ElementPlayerTick ->
  // ElementAmmoTouch -> HUD diff -> sidebar on the canvas.
  await walk(page, "ArrowRight", 3);
  await assertAt(9, 12, "three ArrowRight steps");
  await tickUntilGrid(page, (cells) => hasText(cells, "Ammo:5"), "the ammo pickup to reach the sidebar");

  // The keypad is not a second binding to test for completeness — it is the
  // ORIGINAL vocabulary (INPUT.PAS:217-234). Each of 8/4/6/2 must fold into the
  // same mask bit as its arrow, so `walk` awaits a byte-identical input frame.
  await walk(page, "Numpad2", 1);
  await assertAt(9, 13, "Numpad2 (down)");
  await walk(page, "Numpad8", 1);
  await assertAt(9, 12, "Numpad8 (up)");
  await walk(page, "Numpad4", 1);
  await assertAt(8, 12, "Numpad4 (left)");
  await walk(page, "Numpad6", 1);
  await assertAt(9, 12, "Numpad6 (right)");

  // =========================================================================
  // 2. input.play-wasd-removed — W/A/D must not reach the server at all
  // =========================================================================
  // M3.5 invented WASD and M4.2 removed it, because 'S' meant both "move down"
  // and ZZT's save key while ElementPlayerTick reads both out of one byte. The
  // row is only satisfied if the keys are inert on the WIRE: a client that
  // silently sent a movement frame and a server that happened to ignore it
  // would still look right on screen.
  for (const code of ["KeyW", "KeyA", "KeyD"]) {
    const state = await pressExpectingNoInput(page, code);
    assert.deepEqual(state.pending, [], `${code} must send no input frame, saw ${JSON.stringify(state.pending)}`);
  }
  await idle(1);
  await assertAt(9, 12, "W/A/D pressed");
  // And the other half of the same decision: 'S' is the save key, not "down".
  await command(page, "KeyS", KEY_S);
  await tickUntilGrid(page, (cells) => hasText(cells, "Save game:"), "S to open the save prompt, not walk south");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Save game:"), "the save prompt to close");
  await assertAt(9, 12, "S pressed");

  // =========================================================================
  // 3. input.play-shoot-shift — Shift+direction fires along that direction
  // =========================================================================
  const beforeShot = await readGrid(page);
  assert.equal(
    cellAt(beforeShot, 20 - 1, 12 - 1).ch,
    0x0f,
    "the target object should be standing at tile 20,12 before we shoot it",
  );
  await shootShift(page, "ArrowRight");
  await tickUntilGrid(page, (cells) => hasText(cells, "Ammo:4"), "the shot to be deducted from ammo");
  await assertAt(9, 12, "Shift+ArrowRight");

  // A bullet that is merely fired proves less than a bullet that arrives: the
  // target's `:shot` label runs #die, so its glyph leaving the board is the
  // shot having travelled eleven tiles and hit.
  await tickUntilGrid(
    page,
    (cells) => cellAt(cells, 20 - 1, 12 - 1).ch !== 0x0f,
    "the bullet to reach the target and #die it",
    30,
  );

  // =========================================================================
  // 4. input.play-shoot-space — Space fires along the LAST direction walked
  // =========================================================================
  // Shot south rather than east, so that "last direction" is what is actually
  // being asserted: a Space that reused a hard-coded east would put the bullet
  // in the wrong place.
  await walk(page, "ArrowDown", 1);
  await assertAt(9, 13, "one step south before the Space shot");
  await shootSpace(page);
  // The bullet is already in flight by the time the grid can be read, so what is
  // asserted is the COLUMN it is flying down, not a particular tile: south of
  // the player, in the player's own column. A Space that reused the previous
  // east-facing shot would put it on row 12 instead, and a Space that fired
  // nothing would put it nowhere.
  const afterSpace = await readGrid(page);
  const bulletRows = [];
  for (let row = 0; row < 25; row += 1) {
    if (cellAt(afterSpace, 9 - 1, row).ch === 0xf8) bulletRows.push(row + 1);
  }
  assert.ok(
    bulletRows.length === 1 && bulletRows[0] > 13,
    `Space must shoot SOUTH down column 9, the last direction walked; bullets found on rows ` +
      `${JSON.stringify(bulletRows)}\n${gridToArt(afterSpace)}`,
  );
  await tickUntilGrid(page, (cells) => hasText(cells, "Ammo:3"), "the Space shot to be deducted from ammo");
  await walk(page, "ArrowUp", 1);
  await assertAt(9, 12, "back north after the Space shot");

  // =========================================================================
  // 5. input.textwin-nav + scroll link reply + modal freeze/routing
  // =========================================================================
  // Walk east into the lecture object. The seventeenth step is the touch: the
  // player does not move, the object sends its scroll.
  await walk(page, "ArrowRight", 17);
  await tickUntilGrid(page, (cells) => hasText(cells, "CTRL-01"), "the lecture scroll to open");
  await assertAt(25, 12, "the touch step does not move the player");

  assert.match(await windowCursorLine(page), /CTRL-01 the first lecture line/,
    "a scroll opens on line 1");

  // While the window is open the modal owns the keyboard. This is the clause
  // "focus never leaks text into movement", checked at the wire: an arrow key
  // must produce no input frame at all, not merely no visible movement.
  const frozen = await pressExpectingNoInput(page, "ArrowRight");
  assert.deepEqual(frozen.pending, [], `an open modal must swallow ArrowRight, saw ${JSON.stringify(frozen.pending)}`);
  await idle(1);
  await assertAt(25, 12, "ArrowRight while the scroll is open");

  // Navigation. Each key is asserted by WHICH line it brought under the cursor,
  // so an off-by-one page size or an unclamped bound is a failure rather than a
  // window that merely moved.
  const nav = async (key, expect, why) => {
    await page.keyboard.press(key);
    const line = await windowCursorLine(page);
    assert.match(line, expect, `${key}: ${why} (cursor line was ${JSON.stringify(line)})`);
  };
  await nav("ArrowDown", /CTRL-02/, "one line down");
  await nav("PageDown", /CTRL-16/, "fourteen lines down (TEXT_WINDOW_HEIGHT - 4)");
  await nav("PageUp", /CTRL-02/, "fourteen lines back up");
  await nav("ArrowUp", /CTRL-01/, "one line up");
  await nav("ArrowUp", /CTRL-01/, "clamped at the first line");
  await nav("PageDown", /CTRL-15/, "fourteen down from line 1");
  await nav("PageDown", /Hand me a torch/, "clamped onto the last line, the hyperlink");

  // A hyperlink line re-titles the window, exactly as TextWindowSelect does.
  const onLink = await readGrid(page);
  assert.ok(
    hasText(onLink, "Press ENTER to select this"),
    `resting on a "!label;" line must offer the select prompt:\n${gridToArt(onLink)}`,
  );

  // Enter sends the scroll reply; the object's :encore label hands over a torch.
  await page.keyboard.press("Enter");
  await tickUntilGrid(page, (cells) => hasText(cells, "Torches:1"), "the :encore reply to give a torch");
  await waitForGrid(page, (cells) => !hasText(cells, "CTRL-01"), "the scroll to close on Enter");

  // Escape is the other exit. Re-open and dismiss it that way.
  await walk(page, "ArrowRight", 1);
  await tickUntilGrid(page, (cells) => hasText(cells, "CTRL-01"), "the lecture scroll to re-open");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "CTRL-01"), "Escape to close the scroll");

  // input.play-torch is NOT here: a torch only lights on a dark board
  // (elements.go:1499), so pressing T on Control Field would prove only that the
  // key routed. It is spent on Control Dark, in section 13.

  // =========================================================================
  // 7. input.play-pause
  // =========================================================================
  await command(page, "KeyP", KEY_P);
  await tickUntilGrid(page, (cells) => hasText(cells, "Pausing..."), "P to pause");
  // Vanilla lifts a pause on a MOVE, not on a second P (game.go:1871-1900).
  await walk(page, "ArrowLeft", 1);
  await waitForGrid(page, (cells) => !hasText(cells, "Pausing..."), "a move to lift the pause");

  // =========================================================================
  // 8. input.play-sound-toggle
  // =========================================================================
  // The sidebar's own label is the assertion: 'B' flips between offering to be
  // quiet and offering to be noisy, which is hud.soundEnabled round-tripping
  // through the server and back.
  await waitForGrid(page, (cells) => hasText(cells, "Be quiet"), "sound on at the start");
  await command(page, "KeyB", KEY_B);
  await tickUntilGrid(page, (cells) => hasText(cells, "Be noisy"), "B to mute");
  await command(page, "KeyB", KEY_B);
  await tickUntilGrid(page, (cells) => hasText(cells, "Be quiet"), "B again to unmute");

  // =========================================================================
  // 9. input.play-help
  // =========================================================================
  await command(page, "KeyH", KEY_H);
  await tickUntilGrid(page, (cells) => hasText(cells, "Playing ZZT"), "H to open GAME.HLP");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Playing ZZT"), "Escape to close the help window");

  // =========================================================================
  // 10. input.play-debug
  // =========================================================================
  // GameDebugPrompt is a bare 11-wide field with no label of its own, so it is
  // identified by the sidebar rows it takes over.
  const sidebarBlock = (cells) => [3, 4, 5, 6, 7].map((row) => textAt(cells, 60, row, 20)).join("|");
  const sidebarBefore = sidebarBlock(await waitForQuiet(page));
  await command(page, "Shift+Slash", "?".charCodeAt(0));
  await tickUntilGrid(page, (cells) => sidebarBlock(cells) !== sidebarBefore, "? to open the debug prompt");
  await page.keyboard.press("Escape");
  await waitForQuiet(page);

  // =========================================================================
  // 11. input.play-save + mode.modal-save — the filename entry, submitted
  // =========================================================================
  // The M16.9 golden photographed this prompt; what it did not do is TYPE into
  // it. A name that round-trips to "Saved as CTRLSAVE.SAV" is the modal's
  // charset filter, its buffer, its Enter, and the server's save path.
  await command(page, "KeyS", KEY_S);
  await tickUntilGrid(page, (cells) => hasText(cells, "Save game:"), "S to open the save prompt");
  await page.keyboard.type("CTRLSAVE");
  await page.keyboard.press("Enter");
  await tickUntilGrid(page, (cells) => hasText(cells, "Saved as CTRLSAVE.SAV"), "the save to be confirmed");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Saved as"), "the save confirmation to close");

  // =========================================================================
  // 12. input.play-quit — the prompt, declined
  // =========================================================================
  // Declining rather than confirming: M16.9's high-score goldens already own the
  // confirmed path, and this session has work left to do.
  await command(page, "KeyQ", KEY_Q);
  await tickUntilGrid(page, (cells) => hasText(cells, "End this game?"), "Q to open the quit prompt");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "End this game?"), "Escape to decline the quit");
  const stillHere = await me();
  assert.ok(stillHere.health > 0, "declining the quit prompt must leave the player in the room");

  // =========================================================================
  // 13. The same screen, from accumulated diffs and from a full snapshot
  // =========================================================================
  // DoD: "the same script succeeds against both full snapshots and subsequent
  // diffs". Every cell above arrived as a diff. Crossing a passage makes the
  // client throw that board away and rebuild it from a full SnapshotMessage, so
  // walking back to the same tile must reproduce the same board — cell for
  // cell. Only the 60-column board region is compared: the sidebar is painted
  // from the HUD rather than from the cell stream, and a DisplayMessage on one
  // side of the trip would be noise rather than evidence.
  // The route runs along row 13, one south of everything collectable: the two
  // captures are only comparable if the board is in the same state for both, so
  // nothing on this leg may be picked up, shot, or talked to.
  await walk(page, "ArrowDown", 1);
  await walk(page, "ArrowRight", 16);
  await assertAt(40, 13, "the fixed comparison tile, one south of the passage");
  // Long enough for every DisplayMessage this session raised (200 ticks each,
  // ElementAmmoTouch's among them) to have expired off the board. Otherwise a
  // message still showing for one capture and gone for the other would read as
  // a snapshot/diff divergence.
  await idle(260);
  await waitForQuiet(page);
  const fromDiffs = await readGrid(page);

  const tickUntilBoard = async (boardId, why, maxTicks = 40) => {
    for (let i = 0; i < maxTicks; i += 1) {
      const player = await me();
      if (player.boardId === boardId) return player;
      await idle(1);
    }
    throw new Error(`${why}: never reached board ${boardId} within ${maxTicks} ticks`);
  };

  await walk(page, "ArrowUp", 1);
  const onDark = await tickUntilBoard(2, "the passage to Control Dark");
  assert.deepEqual({ x: onDark.x, y: onDark.y }, { x: 20, y: 12 }, "the passage lands on its counterpart");
  // The board-change fade is client-side and clock-driven; finish it rather
  // than photographing a purple screen.
  await runClock(page, 3000);
  await waitForQuiet(page);

  // ---- input.play-torch, where a torch actually does something -------------
  const unlit = await readGrid(page);
  const hatched = (cells) => cells.filter((cell) => cell.ch === 0xb0).length;
  assert.ok(hatched(unlit) > 100, "an unlit dark board is mostly the 0xB0 hatch");
  await command(page, "KeyT", KEY_T);
  await tickUntilGrid(page, (cells) => hasText(cells, "Torches:0"), "T to spend the torch on a dark board");
  const lit = await readGrid(page);
  assert.ok(
    hatched(lit) < hatched(unlit),
    `lighting a torch must reveal tiles: ${hatched(unlit)} -> ${hatched(lit)} hatched cells`,
  );

  // Back through the passage: this rebuild of Control Field is a full snapshot.
  await walk(page, "ArrowDown", 1);
  await walk(page, "ArrowUp", 1);
  const back = await tickUntilBoard(1, "the return passage");
  assert.deepEqual({ x: back.x, y: back.y }, { x: 40, y: 12 }, "the return passage lands on its counterpart");
  await runClock(page, 3000);
  await waitForQuiet(page);

  await walk(page, "ArrowDown", 1);
  await assertAt(40, 13, "the same comparison tile, reached again");
  await idle(2);
  await waitForQuiet(page);
  const fromSnapshot = await readGrid(page);

  const divergent = [];
  for (let row = 0; row < 25; row += 1) {
    for (let col = 0; col < BOARD_COLS; col += 1) {
      const a = cellAt(fromDiffs, col, row);
      const b = cellAt(fromSnapshot, col, row);
      if (a.ch !== b.ch || a.color !== b.color) {
        divergent.push(`(col ${col}, row ${row}): diffs ${a.ch.toString(16)}/${a.color.toString(16)} vs ` +
          `snapshot ${b.ch.toString(16)}/${b.color.toString(16)}`);
      }
    }
  }
  if (divergent.length > 0) {
    saveText("snapshot-vs-diff-diffs.txt", gridToArt(fromDiffs));
    saveText("snapshot-vs-diff-snapshot.txt", gridToArt(fromSnapshot));
  }
  assert.deepEqual(divergent, [], "the board must render identically from diffs and from a full snapshot");

  console.log("  ✓ play vocabulary, text-window navigation, modal routing, and snapshot/diff equivalence");
} catch (error) {
  failed = true;
  saveText("control-keys-failure.txt", String(error && error.stack ? error.stack : error));
  try {
    saveText("control-keys-screen.txt", gridToArt(await readGrid(page)));
    saveText("control-keys-server.json", JSON.stringify(await serverState(), null, 2));
  } catch {
    // The page or the harness is already gone; the stack above is what matters.
  }
  throw error;
} finally {
  await context.tracing.stop({ path: "test-results/control-keys-trace.zip" });
  await browser.close();
  if (!failed) {
    assert.deepEqual(pageErrors, [], "the page must raise no uncaught errors");
    assert.deepEqual(consoleErrors, [], "the console must carry no errors");
  }
}
