// M16.18 — the mobile and browser-platform contract, one profile per run.
//
// Driven by engine/m16_18_test.go (TestM1618PlatformMatrix), which runs this
// script once per profile declared in fixtures/parity/device-matrix.json and
// hands the profile in through PROFILE_JSON. Everything the script observes goes
// back out as test-results/platform-matrix-<id>.json, which the Go side compares
// against that same declaration — so a profile cannot quietly cover less than it
// claims, and a claim cannot quietly cover a profile that never ran.
//
// WHAT THIS PROVES THAT THE OTHER BROWSER SUITES DO NOT. M16.9/M16.10 drive one
// Chromium at one desktop size with no touch. This is the same built client on
// three engines and five screens, and the questions are the platform's rather
// than the game's:
//
//   * LAYOUT. The 80x25 screen is one canvas with a fixed 640x350 backing store
//     letterboxed by CSS (style.css). A phone screen, a rotation, and a
//     deviceScaleFactor of 3 must all leave the whole grid on screen, undistorted
//     and unscrolled — a clipped column 79 is a missing sidebar, and a stretched
//     aspect ratio is a lost DOS geometry.
//
//   * NATIVE KEYBOARD ACTIVATION. A phone raises its keyboard for a focused
//     native control and never for a canvas, so the client mounts a hidden
//     1px <input>/<textarea> (M15.1's MobileTextInputBridge) while an editable
//     modal is open and focuses it inside a touch gesture. Whether it mounts, and
//     whether the gesture focuses it, is a per-engine fact.
//
//   * COMPOSITION AND DELETION. An IME (and Android's ordinary keyboard) reports
//     keydown keyCode 229 and delivers the real text only as composition events.
//     mobile_text_input.test.mjs drives that adapter under Node against a fake
//     DOM; here the events are real ones on a real element in a real engine.
//
//   * ISOLATION. Every character a player types is also a play-mode binding
//     (S saves, T lights a torch, arrows walk). While a text surface is open,
//     none of it may reach the server — asserted on the wire, not on the screen.
//
// EVERY TEXT SURFACE. modalAcceptsTextInput (modal.ts) names six: popupEntry,
// worldSearch, multilineEntry, chat, entry, programEditor. This script reaches
// all six through the production launch flow, in the order the player meets
// them, and certifies each with the same battery.
//
// NAVIGATION IS KEYBOARD-DRIVEN EVEN ON A TOUCH PROFILE, deliberately. Touch
// *gameplay* controls are the open gap task M16.18a; what a touch profile
// certifies here is text entry and layout, so the script uses real key events to
// get from screen to screen and the touch path only where the touch path is the
// thing under test. The one exception is the on-screen control bar, which is
// measured (not driven) because it is what a phone player sees on top of the
// board.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import {
  baseURL,
  cellAt,
  COLS,
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
  saveText,
  serverState,
  textAt,
  waitForGrid,
  waitForQuiet,
} from "./lib/canvas.mjs";

const profile = JSON.parse(process.env.PROFILE_JSON || "{}");
assert.ok(profile.id, "PROFILE_JSON must carry a profile id (engine/m16_18_test.go)");

// The declared surface list for this profile. A surface absent from it is one
// the declaration explains away; the script never decides on its own to skip.
const wanted = new Set(profile.surfaces || []);
const wants = (id) => wanted.has(id);

// How this engine reveals that it is a touch device.
//
//   "maxTouchPoints" — navigator.maxTouchPoints is non-zero from the first
//     frame, which is what both createTouchControls and MobileTextInputBridge
//     gate on. This is Chromium under touch emulation, and real iOS Safari
//     (maxTouchPoints 5 since iOS 13).
//   "gesture" — the engine reports maxTouchPoints 0 but delivers touch events
//     anyway. Playwright's WebKit build is like this under hasTouch, and so are
//     the hybrid devices M15.1's `touchSeen` fallback was written for: the
//     on-screen control bar is never built (it is decided once, at load), and
//     the text bridge mounts on the first touch the canvas sees. Certifying
//     that path on the Safari engine is the point of the profile.
const detection = profile.touch ? profile.touchDetection || "maxTouchPoints" : "none";
const expectsTouchBar = detection === "maxTouchPoints";
// Set once the client has seen its first touch: from then on `touchSeen` makes
// a gesture-gated engine behave like a maxTouchPoints one.
let touchSeenByClient = false;

// The canvas backing store, from main.ts. A DPR change must not move these.
const BACKING_W = 640;
const BACKING_H = 350;
const ASPECT = BACKING_W / BACKING_H;

// The observation record this run contributes to the matrix.
const observed = {
  profile: profile.id,
  engine: profile.engine,
  viewport: profile.viewport,
  deviceScaleFactor: profile.deviceScaleFactor,
  touch: !!profile.touch,
  layouts: [],
  surfaces: [],
  screenshots: [],
  notes: [],
};

const note = (text) => {
  observed.notes.push(text);
  console.log(`  · ${text}`);
};

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

/**
 * Measure the screen as the platform actually laid it out: the canvas rect in
 * CSS pixels, its backing store, the page's scroll extent, and the on-screen
 * control bar's buttons (which are `position: fixed` and therefore able to sit
 * on top of the board).
 */
async function measureLayout(page) {
  return page.evaluate(() => {
    const canvas = document.querySelector("canvas[data-screen]");
    if (!canvas) throw new Error("no screen canvas");
    const r = canvas.getBoundingClientRect();
    const round = (n) => Math.round(n * 100) / 100;
    const bar = document.querySelector(".touch-controls");
    const buttons = bar ? Array.from(bar.querySelectorAll("button")) : [];
    const rects = buttons.map((b) => b.getBoundingClientRect());
    // Which of the 25 text rows a control button covers. The canvas is scaled,
    // so a row is (button top - canvas top) / (canvas height / 25).
    const rowH = r.height / 25;
    const covered = new Set();
    for (const b of rects) {
      const top = Math.max(b.top, r.top);
      const bottom = Math.min(b.bottom, r.bottom);
      const left = Math.max(b.left, r.left);
      const right = Math.min(b.right, r.right);
      if (bottom <= top || right <= left) continue;
      for (let row = Math.floor((top - r.top) / rowH); row <= Math.floor((bottom - r.top - 0.01) / rowH); row += 1) {
        if (row >= 0 && row < 25) covered.add(row);
      }
    }
    return {
      viewport: { width: window.innerWidth, height: window.innerHeight },
      devicePixelRatio: window.devicePixelRatio,
      maxTouchPoints: navigator.maxTouchPoints,
      canvas: {
        left: round(r.left),
        top: round(r.top),
        width: round(r.width),
        height: round(r.height),
        backingWidth: canvas.width,
        backingHeight: canvas.height,
      },
      scroll: {
        width: document.documentElement.scrollWidth,
        height: document.documentElement.scrollHeight,
      },
      touchBar: {
        present: !!bar,
        buttons: buttons.length,
        coveredRows: Array.from(covered).sort((a, b) => a - b),
        // Where the controls actually sit. A screenshot of a tall phone page is
        // stitched from slices and can draw a `position: fixed` bar once per
        // slice, so the picture alone cannot answer "is the bar only at the
        // bottom?" — these numbers can.
        top: rects.length ? round(Math.min(...rects.map((b) => b.top))) : null,
        bottom: rects.length ? round(Math.max(...rects.map((b) => b.bottom))) : null,
      },
    };
  });
}

/**
 * Assert the invariants that make a screen usable, and record the measurement.
 *
 * The touch bar's overlap is compared against the profile's DECLARED value
 * rather than required to be zero: on a landscape phone the letterboxed canvas
 * fills the viewport's height, so the fixed control bar genuinely sits on the
 * bottom rows. That is a defect with a filed gap task, and pinning the number
 * here is what makes the fix visible — when the bar stops covering the board the
 * declaration goes to zero and this assertion is what says so.
 */
async function checkLayout(page, label) {
  const layout = await measureLayout(page);
  const { canvas, viewport } = layout;
  const where = `${profile.id} @ ${label}`;

  assert.equal(canvas.backingWidth, BACKING_W, `${where}: the canvas backing store must stay ${BACKING_W} wide`);
  assert.equal(canvas.backingHeight, BACKING_H, `${where}: the canvas backing store must stay ${BACKING_H} tall`);

  const aspect = canvas.width / canvas.height;
  assert.ok(
    Math.abs(aspect - ASPECT) < 0.02,
    `${where}: the canvas is ${canvas.width}x${canvas.height} (aspect ${aspect.toFixed(3)}), want ${ASPECT.toFixed(3)} — CP437 geometry is stretched`,
  );

  // The whole grid must be on screen: a clipped right edge is a missing sidebar
  // column, and a clipped bottom is the message line.
  assert.ok(canvas.left >= -1, `${where}: the canvas starts at x=${canvas.left}, off the left edge`);
  assert.ok(canvas.top >= -1, `${where}: the canvas starts at y=${canvas.top}, off the top edge`);
  assert.ok(
    canvas.left + canvas.width <= viewport.width + 1,
    `${where}: the canvas ends at x=${canvas.left + canvas.width}, past the ${viewport.width}px viewport`,
  );
  assert.ok(
    canvas.top + canvas.height <= viewport.height + 1,
    `${where}: the canvas ends at y=${canvas.top + canvas.height}, past the ${viewport.height}px viewport`,
  );

  // Nothing may scroll: the page IS the screen (style.css `overflow: hidden`).
  assert.ok(
    layout.scroll.width <= viewport.width + 1 && layout.scroll.height <= viewport.height + 1,
    `${where}: the page scrolls (${layout.scroll.width}x${layout.scroll.height} in a ${viewport.width}x${viewport.height} viewport)`,
  );

  // Readability floor: below 4 CSS px per column an 8x14 glyph is under half its
  // native size and the sidebar's numbers stop being legible. Every declared
  // profile is above it; a new one that is not needs an owner decision, not a
  // quietly lowered bar.
  const pxPerCell = canvas.width / COLS;
  assert.ok(
    pxPerCell >= 4,
    `${where}: ${pxPerCell.toFixed(2)} CSS px per column — the 80-column screen is not legible at this size`,
  );

  assert.equal(
    layout.touchBar.present,
    expectsTouchBar,
    `${where}: the on-screen control bar is built exactly when navigator.maxTouchPoints is non-zero at load (this engine reports ${layout.maxTouchPoints}, detection ${detection})`,
  );
  if (layout.touchBar.present) {
    assert.ok(
      layout.touchBar.top >= viewport.height / 2,
      `${where}: an on-screen control starts at y=${layout.touchBar.top} in a ${viewport.height}px viewport — the bar belongs in the bottom half, within a thumb's reach`,
    );
    assert.ok(
      layout.touchBar.bottom <= viewport.height + 1,
      `${where}: an on-screen control ends at y=${layout.touchBar.bottom}, past the ${viewport.height}px viewport`,
    );
  }

  const expectedCover = profile.touchBarCoveredRows ?? [];
  assert.deepEqual(
    layout.touchBar.coveredRows,
    expectedCover,
    `${where}: the on-screen control bar covers text rows ${JSON.stringify(layout.touchBar.coveredRows)}, declared ${JSON.stringify(expectedCover)} (fixtures/parity/device-matrix.json)`,
  );

  observed.layouts.push({ label, pxPerCell: Math.round(pxPerCell * 100) / 100, ...layout });
  note(`layout @ ${label}: canvas ${canvas.width}x${canvas.height} at ${canvas.left},${canvas.top}, ${pxPerCell.toFixed(2)}px/col, DPR ${layout.devicePixelRatio}, bar covers ${JSON.stringify(layout.touchBar.coveredRows)}`);
  return layout;
}

async function screenshot(page, label) {
  const file = path.join(resultsDir, `platform-${profile.id}-${label}.png`);
  fs.mkdirSync(resultsDir, { recursive: true });
  await page.screenshot({ path: file });
  observed.screenshots.push(path.basename(file));
}

// ---------------------------------------------------------------------------
// The hidden native control (MobileTextInputBridge)
// ---------------------------------------------------------------------------

const overlay = (page) => page.locator('body > input[aria-hidden="true"], body > textarea[aria-hidden="true"]');

/** Everything about the mounted control a phone keyboard depends on. */
async function overlayState(page) {
  return page.evaluate(() => {
    const el = document.querySelector('body > input[aria-hidden="true"], body > textarea[aria-hidden="true"]');
    if (!el) return null;
    const style = getComputedStyle(el);
    return {
      tag: el.tagName.toLowerCase(),
      focused: document.activeElement === el,
      fontSize: style.fontSize,
      position: style.position,
      pointerEvents: style.pointerEvents,
      enterKeyHint: el.enterKeyHint || "",
      autocapitalize: el.getAttribute("autocapitalize") || "",
      spellcheck: el.getAttribute("spellcheck") || "",
    };
  });
}

/** The touch gesture that raises a phone keyboard: a tap on the board. */
async function tapCanvas(page) {
  await page.locator("canvas[data-screen]").dispatchEvent("touchstart");
}

/**
 * Type `text` the way this profile's player types it.
 *
 * On a touch profile that means a full composition — start, two updates, end,
 * and the ordinary `input` event a browser fires right behind compositionend in
 * the SAME task (the bridge's double-commit guard clears on a microtask, so
 * splitting them would test a sequence no browser produces). On a desktop
 * profile it means real key events, because there is no hidden control at all.
 */
async function typeText(page, text) {
  if (!profile.touch) {
    await page.keyboard.type(text, { delay: 5 });
    return "keyboard";
  }
  const head = text.slice(0, Math.max(1, text.length - 2));
  await overlay(page).evaluate((el, [whole, partial]) => {
    el.dispatchEvent(new CompositionEvent("compositionstart", { data: "" }));
    el.dispatchEvent(new CompositionEvent("compositionupdate", { data: partial }));
    el.dispatchEvent(new CompositionEvent("compositionupdate", { data: whole }));
    el.dispatchEvent(new CompositionEvent("compositionend", { data: whole }));
    el.value = whole;
    el.dispatchEvent(new InputEvent("input", { inputType: "insertText", data: whole }));
  }, [text, head]);
  return "composition";
}

/** Erase one character the way this profile's player erases it. */
async function deleteOne(page) {
  if (!profile.touch) {
    await page.keyboard.press("Backspace");
    return "keyboard";
  }
  await overlay(page).evaluate((el) => {
    el.dispatchEvent(new InputEvent("input", { inputType: "deleteContentBackward", data: null }));
  });
  return "deleteContentBackward";
}

/** Commit the surface the way this profile's player commits it. */
async function commit(page) {
  if (!profile.touch) {
    await page.keyboard.press("Enter");
    return "keyboard";
  }
  // The soft-keyboard Return: a single-line <input> rejects the newline, so it
  // arrives only as a keydown (M17.7) — which the bridge routes to the same
  // submit path desktop Enter uses.
  await overlay(page).press("Enter");
  return "soft-keyboard-return";
}

// ---------------------------------------------------------------------------
// The per-surface battery
// ---------------------------------------------------------------------------

/**
 * Certify one open text surface and record what was checked.
 *
 * `seed` is typed with one extra trailing character, which the deletion check
 * then removes — so composition and deletion are proven against the same buffer
 * and the surface is left holding exactly `seed`.
 */
async function certifySurface(page, { id, kind, expectTag, seed, shows, inRoom, isolationEcho = "stbq" }) {
  const checks = {};
  const record = (name, detail) => {
    checks[name] = detail;
  };

  // --- mount: is there a native control, and should there be? --------------
  const state = await overlayState(page);
  if (profile.touch) {
    // A gesture-gated engine reports no touch points, so nothing is mounted
    // until the client has seen a touch. After that first one `touchSeen` makes
    // it behave exactly like a maxTouchPoints engine, which is the fallback
    // M15.1 wrote for hybrid devices and this is where it gets certified.
    const gestureFirst = detection === "gesture" && !touchSeenByClient;
    if (gestureFirst) {
      assert.equal(
        state,
        null,
        `${id}: an engine reporting maxTouchPoints 0 must not mount the control before it has seen a touch`,
      );
    } else {
      assert.ok(state, `${id}: a touch device must mount the hidden native control for an editable modal`);
    }

    // --- activation: the tap that raises the keyboard ---------------------
    await tapCanvas(page);
    touchSeenByClient = true;
    const mounted = await overlayState(page);
    assert.ok(mounted, `${id}: the canvas touch must leave a native control mounted`);
    assert.equal(mounted.tag, expectTag, `${id}: the control must be a <${expectTag}>`);
    assert.equal(mounted.fontSize, "16px", `${id}: a sub-16px control makes iOS zoom the page on focus`);
    assert.equal(mounted.position, "fixed", `${id}: the control must not scroll the page into view`);
    assert.equal(mounted.pointerEvents, "none", `${id}: the control must never swallow a tap`);
    assert.equal(mounted.autocapitalize, "off", `${id}: autocapitalize must be off`);
    assert.ok(mounted.focused, `${id}: the canvas tap must focus the control — that gesture IS the keyboard`);
    record(
      "mount",
      `<${mounted.tag}> 16px, pointer-events:none, autocapitalize off` +
        (gestureFirst ? " (mounted by the first touch: this engine reports maxTouchPoints 0)" : " (mounted with the modal)"),
    );
    record("activation", `canvas touchstart focused the control (enterKeyHint ${mounted.enterKeyHint || "—"})`);
  } else {
    assert.equal(state, null, `${id}: a device with no touch points must not mount a native control`);
    record("mount", "no native control on a pointer-only device (correct: the canvas takes keys)");
    record("activation", "n/a — a desktop keyboard needs no activation gesture");
    await page.locator("canvas[data-screen]").focus();
  }

  // --- composition (or typing) ---------------------------------------------
  const method = await typeText(page, seed + "X");
  await waitForGrid(page, (cells) => hasText(cells, shows(seed + "X")), `${id}: ${JSON.stringify(seed + "X")} on screen`);
  const once = await readGrid(page);
  assert.ok(
    !hasText(once, shows(seed + "X" + seed)),
    `${id}: the committed text was delivered twice:\n${gridToArt(once)}`,
  );
  record("composition", `${method} committed ${JSON.stringify(seed + "X")} exactly once`);

  // --- deletion -------------------------------------------------------------
  const how = await deleteOne(page);
  await waitForGrid(
    page,
    (cells) => hasText(cells, shows(seed)) && !hasText(cells, shows(seed + "X")),
    `${id}: ${how} to erase one character`,
  );
  record("deletion", `${how} erased one character`);

  // --- isolation: none of this may reach the game ---------------------------
  // The control has the focus on a touch profile, so the game keys are aimed at
  // the canvas exactly as a tap on the board would aim them on a phone. That is
  // the hostile case: the modal, not the board, must swallow them.
  //
  // Every letter below is also a play-mode binding — s saves, t lights a torch,
  // b toggles sound, q quits — and the proof that the modal took them rather
  // than the game is twofold: no input frame reached the server (the wire), and
  // the four characters landed in the buffer (the screen). Up is the one arrow
  // every text surface leaves the buffer alone for — left is an erase key in
  // some of them, and down moves a line cursor in others.
  await page.locator("canvas[data-screen]").focus();
  const before = inRoom ? await roomPlayer() : null;
  // What those four letters look like once the surface has them: the save
  // prompt's `alphanum` charset upper-cases what it accepts (modal.ts).
  const typed = isolationEcho;
  for (const code of ["KeyS", "KeyT", "KeyB", "KeyQ", "ArrowUp"]) {
    await page.keyboard.press(code);
  }
  await page.waitForTimeout(60);
  const state2 = await serverState();
  assert.deepEqual(
    state2.pending,
    [],
    `${id}: play-mode keys typed into a text surface must send no input frame, saw ${JSON.stringify(state2.pending)}`,
  );
  if (inRoom) {
    await idle(2);
    const after = await roomPlayer();
    assert.deepEqual(
      { x: after.x, y: after.y },
      { x: before.x, y: before.y },
      `${id}: typing must not move the player`,
    );
  }
  await waitForGrid(
    page,
    (cells) => hasText(cells, shows(seed + typed)),
    `${id}: the play-mode letters to land in the buffer instead of the game`,
  );
  // Leave the surface holding exactly `seed`, so the caller can submit it.
  for (let i = 0; i < typed.length; i += 1) await deleteOne(page);
  await waitForGrid(
    page,
    (cells) => hasText(cells, shows(seed)) && !hasText(cells, shows(seed + typed)),
    `${id}: the buffer to return to ${JSON.stringify(seed)}`,
  );
  record(
    "isolation",
    inRoom
      ? `S/T/B/Q/Up sent no input frame, moved no player, and landed in the buffer as ${JSON.stringify(typed)}`
      : `S/T/B/Q/Up sent no input frame and landed in the buffer as ${JSON.stringify(typed)} (no room joined yet)`,
  );

  observed.surfaces.push({ id, kind, checks });
  console.log(`  ✓ ${id} (${kind})`);
}

/**
 * Is `needle` on the BOARD half of the screen (columns 0..59)?
 *
 * The sidebar's menu carries rows like "D  Dream a world", so a plain hasText
 * for a modal's title matches the menu that opens it and "the modal closed"
 * would never become true. Modals are drawn over the board, so restricting the
 * search to the board columns is what separates the two.
 */
function boardHasText(cells, needle) {
  for (let row = 0; row < 25; row += 1) {
    if (textAt(cells, 0, row, 60).includes(needle)) return true;
  }
  return false;
}

async function roomPlayer() {
  const state = await serverState();
  assert.equal(state.players.length, 1, `expected exactly one player, saw ${JSON.stringify(state.players)}`);
  return state.players[0];
}

// ---------------------------------------------------------------------------
// Editor helpers (the program editor is only reachable through the editor)
//
// The predicates are the ones engine/web/test/editor_solo.test.mjs certifies —
// repeated here rather than shared, because a matrix run must not depend on
// M16.13's script staying shaped the way it is today.
// ---------------------------------------------------------------------------

const isEditorChrome = (cells) => textAt(cells, 62, 1, 15) === "  ZZT Editor   ";

/**
 * The editor sidebar's cursor readout — the session's own cursor, not the one
 * the client last drew. It reads "Pos: x,y" over a plain tile and "x,y Stat n:
 * …" over a stat, so both spellings are accepted.
 */
function cursorPos(cells) {
  for (let row = 0; row < 25; row += 1) {
    const match = /^(?:Pos:\s*)?(\d+),(\d+)\b/.exec(textAt(cells, 60, row, COLS - 60).trim());
    if (match) return [Number(match[1]), Number(match[2])];
  }
  return null;
}

// A select list draws its entries as hyperlinks whose caption starts at column
// 14, with the selected line always on screen row 13 (editor_solo.test.mjs).
const windowCursorLine = (cells) => textAt(cells, 14, 13, 37).trimEnd();

/** Walk a select list to `label` and press Enter. */
async function pickFromList(page, label, describe) {
  let cells = await readGrid(page);
  for (let i = 0; i < 80; i += 1) {
    if (windowCursorLine(cells) === label) {
      await page.keyboard.press("Enter");
      return;
    }
    const was = windowCursorLine(cells);
    await page.keyboard.press("ArrowDown");
    cells = await waitForGrid(
      page,
      (c) => windowCursorLine(c) !== was,
      `${describe}: the list cursor to move off ${JSON.stringify(was)} towards ${JSON.stringify(label)}`,
    );
  }
  throw new Error(`${describe}: never reached ${label}`);
}

/** Walk the editor cursor to a cell, one arrow at a time. */
async function gotoCell(page, x, y) {
  // The readout is the session's own editorInspect reply, so it lands a round
  // trip after the editor chrome does.
  const at = cursorPos(await waitForGrid(page, (cells) => cursorPos(cells) !== null, "the editor cursor readout"));
  assert.ok(at, "the editor sidebar must show the cursor readout before a journey");
  let [cx, cy] = at;
  for (let guard = 0; (cx !== x || cy !== y) && guard < 200; guard += 1) {
    if (cx < x) { await page.keyboard.press("ArrowRight"); cx += 1; }
    else if (cx > x) { await page.keyboard.press("ArrowLeft"); cx -= 1; }
    else if (cy < y) { await page.keyboard.press("ArrowDown"); cy += 1; }
    else { await page.keyboard.press("ArrowUp"); cy -= 1; }
  }
  await waitForGrid(page, (cells) => {
    const at2 = cursorPos(cells);
    return !!at2 && at2[0] === x && at2[1] === y;
  }, `the editor cursor at ${x},${y}`);
}

// ---------------------------------------------------------------------------
// The run
// ---------------------------------------------------------------------------

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser({
  engine: profile.engine,
  hasTouch: !!profile.touch,
  viewport: profile.viewport,
  deviceScaleFactor: profile.deviceScaleFactor,
});
let failed = false;

try {
  await installImageProbe(page);
  // Log console errors where they happen: the end-of-run assertion says only
  // that one occurred, and on a matrix run "which engine, at which act" is the
  // whole diagnosis.
  page.on("console", (msg) => {
    if (msg.type() === "error") console.log(`  ! console error: ${msg.text()}`);
  });
  await context.tracing.start({ screenshots: true, snapshots: true });

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);

  // =========================================================================
  // 1. popupEntry — the launch name prompt, the first thing every player meets
  // =========================================================================
  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await checkLayout(page, "launch-prompt");
  await screenshot(page, "launch-prompt");

  if (wants("popupEntry")) {
    await certifySurface(page, {
      id: "popupEntry",
      kind: "Launch name prompt (PopupPromptString)",
      expectTag: "input",
      seed: "Matrix",
      shows: (text) => text,
      inRoom: false,
    });
  }
  // Whatever the profile did above, the name has to be committed to move on.
  if (!wants("popupEntry")) await typeText(page, "Matrix");
  await commit(page);

  // =========================================================================
  // 2. worldSearch — the world picker's query field
  // =========================================================================
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await checkLayout(page, "world-picker");
  await screenshot(page, "world-picker");

  if (wants("worldSearch")) {
    await certifySurface(page, {
      id: "worldSearch",
      kind: "World picker search field",
      expectTag: "input",
      seed: "CONTROL",
      shows: (text) => text,
      inRoom: false,
    });
  } else {
    await typeText(page, "CONTROL");
  }
  await waitForGrid(page, (cells) => boardHasText(cells, "CONTROL"), "the picker to match CONTROL");
  await commit(page);

  // =========================================================================
  // 3. The title screen — and the resize/rotation check
  // =========================================================================
  // The sidebar's "W  World:" row naming CONTROL is what says the picker really
  // selected it: a query that matched but never committed leaves the client on
  // its default world, and every later act would be certifying TOWN.
  await waitForGrid(
    page,
    (cells) => hasText(cells, "P  Play") && hasText(cells, "CONTROL"),
    "the title screen for CONTROL",
  );
  await pauseClock(page);
  await checkLayout(page, "title");
  await screenshot(page, "title");

  // Rotate (or resize) without reloading: the CSS is pure vw/vh, so the screen
  // must re-letterbox itself. This is the "resize/DPR" half of the contract —
  // a phone that keeps a landscape layout after a rotation is unplayable, and a
  // client that re-fits only on load would pass every other check here.
  {
    const rotated = { width: profile.viewport.height, height: profile.viewport.width };
    await page.setViewportSize(rotated);
    await page.waitForTimeout(120);
    // The bar's coverage is a function of the shape, so the rotated screen is
    // measured with its own declared expectation.
    const rotatedCover = profile.rotatedTouchBarCoveredRows ?? profile.touchBarCoveredRows ?? [];
    const saved = profile.touchBarCoveredRows;
    profile.touchBarCoveredRows = rotatedCover;
    await checkLayout(page, "rotated");
    await screenshot(page, "rotated");
    profile.touchBarCoveredRows = saved;
    await page.setViewportSize(profile.viewport);
    await page.waitForTimeout(120);
    await checkLayout(page, "restored");
    note(`rotation to ${rotated.width}x${rotated.height} and back re-letterboxed the screen`);
  }

  // =========================================================================
  // 4. multilineEntry — the Dream prompt (opened, never submitted)
  // =========================================================================
  if (wants("multilineEntry")) {
    await page.locator("canvas[data-screen]").focus();
    await page.keyboard.press("KeyD");
    await waitForGrid(page, (cells) => boardHasText(cells, "Dream a world"), "the dream prompt");
    await certifySurface(page, {
      id: "multilineEntry",
      kind: "Dream-a-world premise prompt",
      expectTag: "input",
      seed: "a quiet",
      shows: (text) => text,
      inRoom: false,
    });
    await screenshot(page, "dream-prompt");
    // Escape, not Enter: submitting would start a generation this harness has no
    // model for, and the surface under test is the text entry, not the job.
    await page.locator("canvas[data-screen]").focus();
    await page.keyboard.press("Escape");
    await waitForGrid(page, (cells) => !boardHasText(cells, "Dream a world"), "the dream prompt to close");
  }

  // =========================================================================
  // 5. programEditor — the only multiline surface, reached through the editor
  // =========================================================================
  if (wants("programEditor")) {
    await page.locator("canvas[data-screen]").focus();
    await page.keyboard.press("KeyE");
    await waitForGrid(page, (cells) => isEditorChrome(cells), "the editor sidebar");
    await checkLayout(page, "editor");
    await screenshot(page, "editor");

    // The editor opens on the world's current board, which for CONTROL is the
    // title board — and a title board has no objects. Switch to Control Field,
    // where fixtures/control.zwd's "target" object stands at 20,12.
    await page.keyboard.press("KeyB");
    await waitForGrid(page, (cells) => boardHasText(cells, "Switch boards"), "the board switcher");
    await pickFromList(page, "1: Control Field", "the control board");
    await waitForGrid(page, (cells) => isEditorChrome(cells), "the board switcher to close");

    // Enter opens the object's stat prompt (Character), Enter again its ZZT-OOP
    // program.
    await gotoCell(page, 20, 12);
    await page.keyboard.press("Enter");
    await waitForGrid(page, (cells) => hasText(cells, "Character"), "the object's stat prompt");
    await page.keyboard.press("Enter");
    await waitForGrid(page, (cells) => hasText(cells, "Edit Program"), "the ZZT-OOP program editor");

    await certifySurface(page, {
      id: "programEditor",
      kind: "ZZT-OOP program editor (multiline)",
      expectTag: "textarea",
      seed: "#zap",
      shows: (text) => text,
      inRoom: false,
    });
    await screenshot(page, "program-editor");

    // Escape saves and closes (EditorEditStatText has no cancel).
    await page.locator("canvas[data-screen]").focus();
    await page.keyboard.press("Escape");
    await waitForGrid(page, (cells) => isEditorChrome(cells), "the program editor to close");

    // Leaving asks whether to save the world. The answer is no: the matrix is
    // certifying text entry, and a run that rewrote the harness's .ZZT would be
    // leaving state behind for the next profile.
    await page.keyboard.press("Escape");
    await waitForGrid(page, (cells) => hasText(cells, "Save first?"), "the editor's save prompt");
    await page.keyboard.press("KeyN");
    await waitForGrid(page, (cells) => hasText(cells, "P  Play"), "the title screen after the editor");
  }

  // =========================================================================
  // 6. Playing: the board, the sidebar, and the two in-room text surfaces
  // =========================================================================
  await page.locator("canvas[data-screen]").focus();
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await idle(2);
  await waitForQuiet(page);
  await checkLayout(page, "playing");
  await screenshot(page, "playing");

  // The sidebar is the half of the screen a narrow viewport is most likely to
  // eat, and it is drawn into the same grid — so its presence is proof the whole
  // 80 columns survived the layout, not merely the 60-column board.
  {
    const cells = await readGrid(page);
    assert.ok(hasText(cells, "Health:"), "the sidebar's Health row must be on screen");
    assert.ok(hasText(cells, "Ammo:"), "the sidebar's Ammo row must be on screen");
    assert.notEqual(cellAt(cells, 79, 0).ch, undefined, "column 79 must decode — the screen is not clipped");
  }

  if (wants("chat")) {
    await page.keyboard.press("KeyC");
    await waitForGrid(page, (cells) => hasText(cells, "Global Chat"), "the chat window");
    await certifySurface(page, {
      id: "chat",
      kind: "Global chat composer",
      expectTag: "input",
      seed: "hello",
      shows: (text) => "> " + text,
      inRoom: true,
    });
    await screenshot(page, "chat");
    await page.locator("canvas[data-screen]").focus();
    await page.keyboard.press("Escape");
    await waitForGrid(page, (cells) => !hasText(cells, "Global Chat"), "the chat window to close");
  }

  if (wants("entry")) {
    // S is the save prompt: the server answers with SavePromptEvent and the
    // client opens the "Save game:" entry, charset alphanum.
    await page.locator("canvas[data-screen]").focus();
    // S travels as a command byte, so the tick that consumes it has to be the
    // one this script takes: an idle step would refuse while it is pending.
    await command(page, "KeyS", 0x53);
    await waitForGrid(page, (cells) => hasText(cells, "Save game:"), "the save-name entry");
    await certifySurface(page, {
      id: "entry",
      kind: "Save-game name entry (sidebar prompt)",
      expectTag: "input",
      // Three characters, so the composition's extra one and the four isolation
      // letters both fit the prompt's 8-wide field (main.ts openEntry).
      seed: "MTR",
      shows: (text) => text,
      inRoom: true,
      isolationEcho: "STBQ",
    });
    await screenshot(page, "save-entry");
    await page.locator("canvas[data-screen]").focus();
    await page.keyboard.press("Escape");
    await waitForGrid(page, (cells) => !hasText(cells, "Save game:"), "the save entry to close");
  }

  // A modal drawn over the board, at this screen size, with the sidebar intact:
  // the DoD's "modal layout remains usable" screenshot.
  await page.locator("canvas[data-screen]").focus();
  await page.keyboard.press("KeyH");
  await waitForGrid(page, (cells) => hasText(cells, "Help"), "the help window");
  await checkLayout(page, "modal-help");
  await screenshot(page, "modal-help");
  await page.keyboard.press("Escape");

  // The client is still playable when the matrix is done with it.
  await page.locator("canvas[data-screen]").focus();
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the board after the modal");

  const covered = observed.surfaces.map((s) => s.id);
  assert.deepEqual(
    covered.slice().sort(),
    Array.from(wanted).sort(),
    `the run covered ${JSON.stringify(covered)}, the declaration asks for ${JSON.stringify(Array.from(wanted))}`,
  );
  console.log(`platform_matrix.test.mjs: ${profile.id} covered ${covered.length} text surface(s)`);
} catch (error) {
  failed = true;
  observed.error = String(error && error.stack ? error.stack : error);
  saveText(`platform-${profile.id}-failure.txt`, observed.error);
  try {
    saveText(`platform-${profile.id}-screen.txt`, gridToArt(await readGrid(page)));
  } catch {
    // The page is already gone; the stack above is what matters.
  }
  throw error;
} finally {
  await context.tracing.stop({ path: `test-results/platform-${profile.id}-trace.zip` });
  await browser.close();
  // Leaving the editor for play closes the editor session's socket while the
  // server is closing its own end, and the side that loses that race logs
  // "Close received after close". It is the normal shape of a two-sided close —
  // the join that follows it succeeds, which every act after the editor depends
  // on — so it is recorded in the observation rather than either ignored or
  // treated as a client fault. Anything else on the console is a real error.
  const closeRace = /Close received after close/;
  for (const text of consoleErrors.filter((text) => closeRace.test(text))) {
    observed.notes.push(`benign console message: ${text}`);
  }
  fs.writeFileSync(
    path.join(resultsDir, `platform-matrix-${profile.id}.json`),
    JSON.stringify(observed, null, 2) + "\n",
  );
  if (!failed) {
    assert.deepEqual(pageErrors, [], "the page must raise no uncaught errors");
    assert.deepEqual(
      consoleErrors.filter((text) => !closeRace.test(text)),
      [],
      "the console must carry no errors",
    );
  }
}
