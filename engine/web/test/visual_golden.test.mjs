// M16.9 — the browser visual-parity golden suite.
//
// Driven by engine/m16_9_test.go (TestM169BrowserCanvasGoldens), which hosts the
// GOLDEN world on the production server objects with the tick loop under this
// script's control. Everything below is captured from the real client canvas
// through the production title screen and world picker — no test hooks in the
// client, no state staging, no direct calls into the client's modules.
//
// The goldens live in fixtures/browser-goldens/*.json. Each carries the cell
// truth (character + DOS attribute per cell, in hex) and an ASCII-art rendering
// for the reviewer. Record with GOLDEN_UPDATE=1 — and then READ the art before
// committing it; a recorded golden is a claim about what the player sees.
//
// Alongside the goldens are two kinds of assertion the goldens cannot make:
//   * semantic checks (the CP437 sweep really is 0..255 in order, the colour
//     sweep really is every fg/bg pair) — these would still hold if someone
//     re-recorded a wrong golden;
//   * animation invariants (transition, pause blink, energizer) — checked as
//     properties across frames rather than as whole-frame goldens, because the
//     transition's cell order is a local Math.random shuffle and pinning a
//     mid-fade frame would be pinning noise.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import {
  COLS,
  baseURL,
  cellAt,
  checkGolden,
  command,
  gridToArt,
  hasText,
  idle,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  pauseClock,
  readGrid,
  resultsDir,
  runClock,
  saveText,
  serverState,
  step,
  textAt,
  updateGoldens,
  waitForGrid,
  tickUntilGrid,
  waitForQuiet,
  walk,
} from "./lib/canvas.mjs";

// Where the harness paints its sweeps (engine/m16_9_test.go m169GoldenWorld).
// Tile (x,y) is screen cell (x-1, y-1).
const SWEEP_X = 2 - 1;
const SWEEP_W = 56;
const GLYPH_SWEEP_Y = 2 - 1;
const COLOR_SWEEP_Y = 8 - 1;
const TEXT_FAMILY_Y = 14 - 1;

const KEY_T = "T".charCodeAt(0);
const KEY_P = "P".charCodeAt(0);
const KEY_Q = "Q".charCodeAt(0);
const KEY_S = "S".charCodeAt(0);

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
  // Title screen, through the production launch flow
  // =========================================================================
  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served, not the build-me 404 page");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Golden");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("GOLDEN");
  await waitForGrid(page, (cells) => hasText(cells, "GOLDEN"), "the picker to match GOLDEN");
  await page.keyboard.press("Enter");
  await waitForGrid(
    page,
    (cells) => hasText(cells, "P  Play") && hasText(cells, "GOLDEN"),
    "the title screen for GOLDEN",
  );

  // From here on the page's clock is frozen: no blink, no poll, no animation
  // moves unless this script advances it.
  await pauseClock(page);
  const title = await checkGolden(
    page,
    "title",
    "Title screen: board 0 of GOLDEN behind the monitor sidebar, reached through the real picker.",
  );
  assert.ok(hasText(title, "GOLDEN"), "the title sidebar names the selected world");
  assert.ok(hasText(title, "R  Restore game"), "the title menu is the production one");

  // =========================================================================
  // Playing: board, authentic sidebar, CP437 sweep, DOS colours, text tiles
  // =========================================================================
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await idle(2);
  await waitForQuiet(page);

  const board = await checkGolden(
    page,
    "playing-board",
    "Playing mode: the GOLDEN showcase board (CP437 0x00-0xFF as text tiles, every DOS attribute " +
      "on the Normal-wall glyph, one row per text-colour family) beside the authentic sidebar.",
  );

  // --- semantic checks the golden cannot make for itself --------------------
  // A re-recorded-but-wrong golden would still match itself; these would not.
  // CP437 0x00..0xFF, in order, as Text-White tiles. Four codes render as a
  // single flat colour — 0x00/0x20/0xFF are blank and 0xDB is the full block —
  // so the canvas cannot say which glyph painted them, and the decoder reports
  // a uniform cell rather than inventing one. That set is itself an assertion:
  // it is a property of the font the client ships.
  const uniformGlyphCodes = [];
  for (let i = 0; i < 256; i += 1) {
    const cell = cellAt(board, SWEEP_X + (i % SWEEP_W), GLYPH_SWEEP_Y + Math.floor(i / SWEEP_W));
    if (cell.uniform) {
      uniformGlyphCodes.push(i);
      continue;
    }
    // `alt` is the other reading of a glyph that is some other glyph's exact
    // inverse; both describe the same pixels, so either satisfies the sweep.
    const readings = [cell, cell.alt].filter(Boolean);
    assert.ok(
      readings.some((r) => r.ch === i && r.color === 0x0f),
      `CP437 sweep cell ${i} drew ${JSON.stringify(readings)}, want char 0x${i.toString(16)} colour 0x0F`,
    );
  }
  assert.deepEqual(
    uniformGlyphCodes,
    [0x00, 0x20, 0xdb, 0xff],
    "only the blank codes and the full block may be indistinguishable from a flat cell",
  );

  let uniformColourCells = 0;
  for (let i = 0; i < 256; i += 1) {
    const cell = cellAt(board, SWEEP_X + (i % SWEEP_W), COLOR_SWEEP_Y + Math.floor(i / SWEEP_W));
    const fg = i & 0x0f;
    const bg = (i >> 4) & 0x0f;
    if (fg === bg) {
      // Foreground on identical background: the glyph is invisible, and the
      // decoder is honest about not knowing which one it was.
      assert.equal(cell.color & 0x0f, fg, `colour sweep 0x${i.toString(16)} lost its colour`);
      uniformColourCells += 1;
      continue;
    }
    const readings = [cell, cell.alt].filter(Boolean);
    assert.ok(
      readings.some((r) => r.ch === 0xb2 && r.color === i),
      `DOS colour sweep 0x${i.toString(16)} drew ${JSON.stringify(readings)}, want the Normal wall 0xB2 on attribute 0x${i.toString(16)}`,
    );
  }
  assert.equal(uniformColourCells, 16, "exactly the 16 fg==bg attributes are indistinguishable");

  // Text tiles: seven families, each showing 'A' in its own fixed colour
  // (TileToColorAndChar: text colour is (element - E_TEXT_MIN + 1) * 16 + 0x0F).
  const textFamilies = [0x1f, 0x2f, 0x3f, 0x4f, 0x5f, 0x6f, 0x0f];
  textFamilies.forEach((colour, familyIndex) => {
    for (let i = 0; i < 4; i += 1) {
      const cell = cellAt(board, 1 + familyIndex * 5 + i, TEXT_FAMILY_Y);
      assert.equal(cell.ch, 0x41, `text family ${familyIndex} cell ${i} is not 'A'`);
      assert.equal(
        cell.color,
        colour,
        `text family ${familyIndex} drew colour 0x${cell.color.toString(16)}, want 0x${colour.toString(16)}`,
      );
    }
  });

  // The sidebar is the client's own transcription of GameDrawSidebar; the
  // golden pins its pixels, this pins that it is showing THIS player's state.
  assert.ok(hasText(board, "Health:100"), "the sidebar shows health");
  assert.ok(hasText(board, "Torches:0"), "the sidebar shows torches");
  assert.ok(hasText(board, "T  Torch"), "the sidebar shows the play-mode key legend");

  // =========================================================================
  // Player identity overlay: two browsers, one board
  // =========================================================================
  // The board cells are identical for both players — what makes a canvas
  // "yours" is the overlay the client draws at YOUR player's square (the pause
  // blink, GAME.PAS:1518-1533) and the HUD it draws from YOUR stats. Two pages
  // on the same board must therefore differ exactly there and nowhere else.
  const second = await context.newPage();
  await installImageProbe(second);
  await second.goto(baseURL);
  await second.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(second);
  await waitForGrid(second, (cells) => hasText(cells, "Type your name"), "the second player's name prompt");
  await second.keyboard.type("Second");
  await second.keyboard.press("Enter");
  await waitForGrid(second, (cells) => hasText(cells, "Choose a World"), "the second player's picker");
  await second.keyboard.type("GOLDEN");
  await waitForGrid(second, (cells) => hasText(cells, "GOLDEN"), "the second player's match");
  await second.keyboard.press("Enter");
  await waitForGrid(second, (cells) => hasText(cells, "P  Play"), "the second player's title");
  await pauseClock(second);
  await second.keyboard.press("KeyP");
  await waitForGrid(second, (cells) => hasText(cells, "Health:100"), "the second player's board");
  await idle(2);
  await waitForQuiet(page);
  await waitForQuiet(second);

  const state2 = await serverState();
  assert.equal(state2.players.length, 2, "both browsers must be in the room");
  const [p1, p2] = state2.players;
  assert.notDeepEqual({ x: p1.x, y: p1.y }, { x: p2.x, y: p2.y }, "the arrivals must not share a square (M4.3b push-out)");

  // Pause each player in turn: the "Pausing..." label and the blinking player
  // glyph are drawn locally, at the local player's square only.
  await page.keyboard.press("KeyP");
  await step({ await: { key: KEY_P } });
  await waitForGrid(page, (cells) => hasText(cells, "Pausing..."), "player one's pause overlay");
  await waitForQuiet(page);
  const paused1 = await checkGolden(
    page,
    "identity-paused-player-one",
    "Player one's canvas with two players on the board: the pause overlay marks THIS player's square.",
  );
  const other = await readGrid(second);
  assert.ok(!hasText(other, "Pausing..."), "the other player's canvas must not show our pause overlay");
  assert.equal(
    cellAt(paused1, p1.x - 1, p1.y - 1).ch,
    0x02,
    "the pause overlay draws the player glyph at our own square",
  );

  // The blink is client-side and clock-driven (250ms, GAME.PAS:1520's
  // SoundHasTimeElapsed(TickTimeCounter, 25)). Advance the fake clock rather
  // than sleeping, and assert the property — the glyph alternates with a blank
  // while "Pausing..." stays put — instead of pinning a frame.
  const blinkGlyphs = new Set();
  for (let i = 0; i < 4; i += 1) {
    await runClock(page, 250);
    const frame = await readGrid(page);
    assert.ok(hasText(frame, "Pausing..."), "the Pausing... label never blinks; only the glyph does");
    blinkGlyphs.add(cellAt(frame, p1.x - 1, p1.y - 1).ch);
  }
  assert.deepEqual(
    [...blinkGlyphs].sort((a, b) => a - b),
    [0x02, 0x20],
    `the paused player's square must alternate glyph and blank, saw ${[...blinkGlyphs]}`,
  );

  // Vanilla unpauses on a MOVE, not on a second P (GAME.PAS's paused branch,
  // ported at game.go:1871-1900) — so this both lifts the pause and starts the
  // walk east.
  await walk(page, "ArrowRight", 1);
  await waitForGrid(page, (cells) => !hasText(cells, "Pausing..."), "the pause to lift on a move");
  await second.close();
  await idle(1);
  await waitForQuiet(page);

  // =========================================================================
  // Windows: scroll, help, debug, save, quit, high score
  // =========================================================================
  // Walk to the vendor object at tile x=18. The player started at x=6 and the
  // unpausing move above took them to x=7; the torch is at 9, the gem at 12 and
  // the energizer at 15.
  await walk(page, "ArrowRight", 2);
  await waitForGrid(page, (cells) => hasText(cells, "Torches:1"), "the torch to be collected");
  await walk(page, "ArrowRight", 3);
  await waitForGrid(page, (cells) => hasText(cells, "Gems:1"), "the gem to be collected");

  // --- energizer: the blink is the SERVER's (ElementPlayerTick alternates the
  // player glyph and cycles the tile colour every tick while energised) -------
  await walk(page, "ArrowRight", 3);
  await waitForQuiet(page);
  const beforeEnergiser = await serverState();
  const me = beforeEnergiser.players[0];
  const energised = [];
  for (let i = 0; i < 2; i += 1) {
    await idle(1);
    await waitForQuiet(page);
    const frame = await readGrid(page);
    const here = await serverState();
    energised.push(cellAt(frame, here.players[0].x - 1, here.players[0].y - 1));
    await checkGolden(
      page,
      `energizer-tick-${i}`,
      `Energised player, consecutive ticks (${i}): ElementPlayerTick alternates the player glyph ` +
        "0x02/0x01 and cycles the tile colour every tick.",
    );
  }
  assert.notEqual(
    `${energised[0].ch}/${energised[0].color}`,
    `${energised[1].ch}/${energised[1].color}`,
    "an energised player must look different on consecutive ticks",
  );
  assert.ok(
    energised.every((cell) => cell.ch === 0x01 || cell.ch === 0x02),
    `the energised glyph must stay the player's own 0x01/0x02, saw ${energised.map((c) => c.ch)}`,
  );

  // --- the vendor's scroll --------------------------------------------------
  await walk(page, "ArrowRight", 3);
  // The vendor is an Object on cycle 3: the :touch it was just sent runs on its
  // next scheduled tick, so take ticks until the scroll actually opens.
  await tickUntilGrid(page, (cells) => hasText(cells, "Golden Vendor"), "the vendor's scroll window");
  await waitForQuiet(page);
  await checkGolden(
    page,
    "window-scroll",
    "The CP437 scroll window (TXTWIND.PAS TextWindowDraw) over the board, opened by touching the vendor.",
  );
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Golden Vendor"), "the scroll to close");
  // Closing sends a scrollReply, which un-freezes the reader in the room. Wait
  // for the server to have applied it rather than guessing.
  for (let i = 0; i < 50; i += 1) {
    const now = await serverState();
    if (!now.players[0].scrollOpen) break;
    await page.waitForTimeout(60);
  }
  await idle(1);
  await waitForQuiet(page);

  // Every window below is opened by the SERVER (ElementPlayerTick's command
  // switch emits HelpEvent / DebugPromptEvent / SavePromptEvent / the quit
  // prompt), so each command key needs the tick that runs it — pressing and
  // then merely waiting would wait forever in a harness where nothing ticks.
  // The predicates name text only the window itself has: "H  Help" and
  // "S  Save game" are already on the sidebar, and matching those would have
  // photographed the bare board and called it a window.

  // --- help ----------------------------------------------------------------
  await command(page, "KeyH", "H".charCodeAt(0));
  await tickUntilGrid(page, (cells) => hasText(cells, "Playing ZZT"), "the help window (GAME.HLP)");
  await checkGolden(page, "window-help", "The .HLP help window (GAME.HLP through /api/help), opened with H.");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Playing ZZT"), "the help window to close");

  // --- debug prompt ---------------------------------------------------------
  // GameDebugPrompt is a bare 11-wide field with no label, so it is identified
  // by the sidebar line it takes over rather than by any text of its own.
  const sidebarBlock = (cells) => [3, 4, 5, 6, 7].map((row) => textAt(cells, 60, row, 20)).join("|");
  const sidebarBefore = sidebarBlock(await waitForQuiet(page));
  await command(page, "Shift+Slash", "?".charCodeAt(0));
  await tickUntilGrid(
    page,
    (cells) => sidebarBlock(cells) !== sidebarBefore,
    "the debug prompt to take over its sidebar lines",
  );
  await checkGolden(
    page,
    "window-debug",
    "GameDebugPrompt's sidebar entry (PromptString at 63,5, width 11), opened with '?'.",
  );
  await page.keyboard.press("Escape");
  await waitForQuiet(page);

  // --- save prompt ----------------------------------------------------------
  await command(page, "KeyS", KEY_S);
  await tickUntilGrid(page, (cells) => hasText(cells, "Save game:"), "the save prompt");
  await checkGolden(page, "window-save", "SidebarPromptString(\"Save game:\", \".SAV\"), opened with S.");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Save game:"), "the save prompt to close");

  // --- quit prompt ----------------------------------------------------------
  await command(page, "KeyQ", KEY_Q);
  await tickUntilGrid(page, (cells) => hasText(cells, "End this game?"), "the quit prompt");
  await checkGolden(page, "window-quit", "GamePromptEndPlay's \"End this game?\" confirmation, opened with Q.");
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "End this game?"), "the quit prompt to close");
  await waitForQuiet(page);

  // =========================================================================
  // Board transition, then the dark board and its torch
  // =========================================================================
  // The passage is at tile (24,20); the player is west of the vendor at (17,20),
  // and the vendor blocks row 20, so the route is up, east, and back down onto
  // the passage — that last step is the transfer.
  await walk(page, "ArrowUp", 1);
  await walk(page, "ArrowRight", 7);

  const boardBefore = (await serverState()).players[0].boardId;
  await walk(page, "ArrowDown", 1);
  const afterPassage = await serverState();
  assert.notEqual(afterPassage.players[0].boardId, boardBefore, "the passage must move the player to a new board");

  // The client fades the old board into the new one over TRANSITION_DURATION_MS
  // of *animation clock*. The cell order is a local Math.random shuffle, so what
  // is asserted mid-fade is the invariant — the purple fill is present but has
  // not taken the whole board — rather than a golden of shuffled noise.
  // The fill is a full block (0xDB) in purple on black — a flat purple cell,
  // which is exactly what the decoder reports it as: uniform, foreground 5.
  const isFill = (cell) => cell.uniform && (cell.color & 0x0f) === 0x05;
  await runClock(page, 40);
  const midway = await readGrid(page);
  const fillCells = midway.filter(isFill).length;
  assert.ok(fillCells > 0, "the fade must be showing its purple fill part-way through");
  assert.ok(fillCells < 60 * 25, "the fade must be part-way through, not a fully purple screen");
  await runClock(page, 2000);
  const settled = await waitForGrid(page, (cells) => cells.filter(isFill).length === 0, "the fade to finish");
  assert.ok(
    settled.filter((cell) => cell.ch === 0xb0).length > 100,
    "the dark board is mostly the unlit hatch character 0xB0",
  );

  await idle(1);
  await waitForQuiet(page);
  await checkGolden(
    page,
    "transition-end-dark-board",
    "The board-transition end state: the dark \"Golden Dark\" board, drawn as TileToColorAndChar's " +
      "unlit 0xB0 on 0x07 everywhere the player cannot see.",
  );

  // Torch: pick it up, light it, and the lit radius appears (TORCH_DIST_SQR).
  await walk(page, "ArrowRight", 3);
  await waitForGrid(page, (cells) => hasText(cells, "Torches:1"), "the dark board's torch");
  const unlit = await readGrid(page);
  await command(page, "KeyT", KEY_T);
  await tickUntilGrid(page, (cells) => hasText(cells, "Torches:0"), "the torch to be spent");
  const lit = await checkGolden(
    page,
    "playing-torch-lit",
    "The same dark board with a lit torch: the visible radius replaces the 0xB0 hatch.",
  );
  const hatchBefore = unlit.filter((cell) => cell.ch === 0xb0).length;
  const hatchAfter = lit.filter((cell) => cell.ch === 0xb0).length;
  assert.ok(hatchAfter < hatchBefore, `lighting a torch must reveal tiles: ${hatchBefore} -> ${hatchAfter} hatched cells`);

  // =========================================================================
  // Quit, through the high-score placement, entry, and table
  // =========================================================================
  await command(page, "KeyQ", KEY_Q);
  await tickUntilGrid(page, (cells) => hasText(cells, "End this game?"), "the quit prompt");
  // Answering the prompt sends a quitReply message, which the room drains at
  // the top of a tick — so what follows needs ticks, not patience.
  await page.keyboard.press("KeyY");
  await tickUntilGrid(page, (cells) => hasText(cells, "New high score"), "the high-score placement window");
  const placement = await checkGolden(
    page,
    "window-highscore-placement",
    "The \"New high score for GOLDEN\" list shown when a quitting score places, with vanilla's " +
      "\"-- You! --\" marker on the earned slot, carrying the score the player just earned — " +
      "HighScoresAdd shifts the list down and writes the score before it draws (M16.9a).",
  );
  assert.ok(hasText(placement, "-- You! --"), "the placement window marks the earned slot");
  // M16.9a: the marked row must show this run's score, not the slot's old one
  // (-1 for the empty list this world starts with). The sidebar above says 10.
  assert.ok(hasText(placement, "Score:10"), "this run's score is 10");
  assert.ok(
    hasText(placement, "   10  -- You! --"),
    "the marked row carries the earned score, not the empty slot's -1",
  );

  // Dismissing the placement list is what opens the name prompt (main.ts
  // closeModal -> pendingHighScore -> openHighScoreName).
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => hasText(cells, "Congratulations"), "the high-score name entry");
  await checkGolden(
    page,
    "window-highscore-entry",
    "PopupPromptString(\"Congratulations!  Enter your name:\"), the prompt that follows the placement list.",
  );
  await page.keyboard.type("GLD");
  await page.keyboard.press("Enter");
  const scores = await tickUntilGrid(page, (cells) => hasText(cells, "GLD"), "the recorded high-score table");
  await checkGolden(
    page,
    "window-highscore-table",
    "The high-score table window that follows the entry, with this run's name recorded.",
  );
  assert.ok(hasText(scores, "GLD"), "the name just typed is in the table");

  // =========================================================================
  // The harness itself: a one-cell regression must produce a useful artifact
  // =========================================================================
  // DoD: "a one-cell glyph/color regression produces a useful diff artifact".
  // Proven against the harness rather than by breaking the client: take the
  // real screen, corrupt exactly one cell of the expectation, and require the
  // comparison to name that cell and write actual/expected/diff images.
  {
    const { diffGrids, formatDiff, writeArtifacts } = await import("./lib/canvas.mjs");
    const actual = await readGrid(page);
    const tampered = actual.map((cell) => ({ ...cell }));
    const victim = 7 * COLS + 11;
    tampered[victim] = { ch: (actual[victim].ch + 1) & 0xff, color: actual[victim].color ^ 0x10 };
    const mismatches = diffGrids(tampered, actual);
    assert.equal(mismatches.length, 1, "a one-cell change must report exactly one cell");
    assert.deepEqual({ x: mismatches[0].x, y: mismatches[0].y }, { x: 11, y: 7 }, "the diff names the cell");
    const message = formatDiff("self-check", mismatches);
    assert.match(message, /\(col 11, row 7\): expected .* got /, `the diff text must be readable, got: ${message}`);
    const files = await writeArtifacts(page, "self-check", tampered, actual, mismatches);
    for (const file of files) {
      assert.ok(fs.existsSync(file) && fs.statSync(file).size > 0, `${file} must be written and non-empty`);
    }
    assert.equal(files.filter((f) => f.endsWith(".png")).length, 3, "actual, expected and diff images");
    console.log(`  ✓ one-cell regression self-check: ${message.split("\n")[1].trim()}`);
  }

  assert.deepEqual(pageErrors, [], "the client must not raise page errors");
  assert.deepEqual(consoleErrors, [], "the client must not log console errors or fetch failures");

  console.log(
    updateGoldens
      ? "GOLDENS RECORDED — review fixtures/browser-goldens/*.json before committing"
      : "ALL GOLDENS PASSED",
  );
} catch (err) {
  failed = true;
  console.error("visual golden suite FAILED:", err);
  try {
    const cells = await readGrid(page);
    saveText("visual-golden-failure.txt", gridToArt(cells));
    console.error("screen at failure:\n" + gridToArt(cells));
  } catch (readErr) {
    console.error("could not read the canvas at failure:", readErr);
  }
  console.error("pageErrors:", pageErrors);
  console.error("consoleErrors:", consoleErrors);
  await page.screenshot({ path: path.join(resultsDir, "visual-golden-failure.png") }).catch(() => {});
} finally {
  await context.tracing.stop({ path: path.join(resultsDir, "visual-golden-trace.zip") }).catch(() => {});
  await browser.close();
  process.exit(failed ? 1 : 0);
}
