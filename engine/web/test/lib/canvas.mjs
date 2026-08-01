// M16.9 — the browser-canvas golden library.
//
// Everything here exists to turn "what the player sees" into something a test
// can assert on without guessing. The client renders one <canvas> and exposes no
// DOM, so the goldens are read back out of the canvas backing store (640x350,
// one 8x14 EGA cell per character, blitted 1:1 — see main.ts CELL_W/CELL_H) and
// decoded into CP437 cells by matching each cell against the client's OWN font
// atlas, fetched from the same /assets URL the page loaded.
//
// Why not Go's PNG renderer (engine/render_png.go)? It shares this repo's font
// and palette tables, so it would happily agree with a client bug. The DoD says
// the goldens come from the browser, and they do: page pixels in, cells out.
//
// DECODING, AND WHY IT IS SOUND. A cell holds at most two colours: the glyph ink
// (foreground) and the fill (background). Decode is:
//   1. map every pixel to an EGA palette index (exact match; an unknown colour
//      is an error, not a guess);
//   2. one colour present  -> a uniform cell: character 0x20, fg = bg = colour.
//      Which glyph painted it is genuinely unknowable from pixels, and a golden
//      must not pretend otherwise;
//   3. two colours present -> try both ink assignments, keep the one whose
//      1-bit mask is a real glyph in the atlas (preferring the sparser ink, then
//      the lower palette index, so the choice is deterministic);
//   4. verify: re-derive the mask from the decoded (char, fg, bg) and require it
//      to reproduce the cell exactly.
// Step 4 is what makes cell equality equivalent to pixel equality: a decode that
// could not have produced these pixels is rejected rather than recorded. A
// character whose bitmap is identical to another's (0x00/0x20/0xFF are all
// blank) canonicalises to one representative — those cells are the same picture,
// which is the only thing a *visual* golden is entitled to claim.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium, firefox, webkit } from "playwright";

// The three engines a desktop browser can be (M16.18). Chromium is the only one
// the golden suites need — a golden is of the client, not of the engine — so the
// other two are imported but launched only by the platform matrix.
export const ENGINES = { chromium, firefox, webkit };

export const COLS = 80;
export const ROWS = 25;
export const CELL_W = 8;
export const CELL_H = 14;
export const WIDTH = COLS * CELL_W;
export const HEIGHT = ROWS * CELL_H;

// main.ts's `ega`, duplicated on purpose: the harness must not import the thing
// it is checking. A palette drift shows up as an undecodable colour, loudly.
export const EGA = [
  "#000000", "#0000aa", "#00aa00", "#00aaaa",
  "#aa0000", "#aa00aa", "#aa5500", "#aaaaaa",
  "#555555", "#5555ff", "#55ff55", "#55ffff",
  "#ff5555", "#ff55ff", "#ffff55", "#ffffff",
];

// A fixed instant for the fake clock. Any constant does; committing one keeps
// "the goldens were captured at this moment" out of the list of things that can
// drift.
const CLOCK_TIME = "2026-01-01T00:00:00.000Z";

/**
 * Where `launchGoldenBrowser` parks a page's recorded error channel, so
 * `pauseClock` can account for the one page error the harness produces itself
 * (M16.18c — the reasoning is at `pauseClock`). A property rather than a
 * WeakMap: the suites wrap `page` to force timings, and a wrapper forwards a
 * property where it cannot forward an identity.
 */
export const ERROR_CHANNEL = "__zztErrorChannel";

// The exact text of that error. It has exactly one producer in this repo:
// Playwright's injected clock, from `_innerFastForwardTo`, reached only by
// `clock.pauseAt` and `clock.fastForward` — and the harness never calls
// `fastForward`. So no client fault can wear this string.
const CLOCK_REWIND_ERROR = "Error: Cannot fast-forward to the past";

export const goldenDir = process.env.GOLDEN_DIR
  ? path.resolve(process.env.GOLDEN_DIR)
  : path.resolve("../../fixtures/browser-goldens");
export const resultsDir = path.resolve("test-results");
export const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
export const controlURL = process.env.CONTROL_URL || "";
export const updateGoldens = process.env.GOLDEN_UPDATE === "1";

fs.mkdirSync(resultsDir, { recursive: true });

/**
 * Launch Chromium with everything that could move a pixel nailed down: a fixed
 * viewport, deviceScaleFactor 1 (the canvas backing store is 640x350 whatever
 * CSS does, but a DPR change would still alter what a screenshot captures),
 * animations under a fake clock, and a device-independent colour treatment.
 *
 * M16.18 widened the signature so the platform matrix can ask for another engine
 * or another screen without a second launcher: every default below is the
 * golden suites' existing one, so their calls are unchanged.
 */
export async function launchGoldenBrowser({
  hasTouch = false,
  engine = "chromium",
  viewport = { width: 1280, height: 720 },
  deviceScaleFactor = 1,
} = {}) {
  const launcher = ENGINES[engine];
  if (!launcher) throw new Error(`unknown browser engine ${engine}`);
  const browser = await launcher.launch({ headless: true });
  const context = await browser.newContext({
    viewport,
    deviceScaleFactor,
    colorScheme: "dark",
    reducedMotion: "no-preference",
    // hasTouch raises navigator.maxTouchPoints, which is the ONLY thing that
    // lets MobileTextInputBridge mount its hidden native control — and with it
    // the composition/IME path (M16.10). Off by default: the goldens are of a
    // desktop client, and a touch context would also mount the on-screen bar.
    hasTouch,
  });
  await context.clock.install({ time: CLOCK_TIME });
  const page = await context.newPage();
  const pageErrors = [];
  const consoleErrors = [];
  // Errors the harness provoked itself, moved aside rather than dropped, so a
  // run can still be asked what it swallowed. Only `pauseClock` fills this —
  // see the accounting below it for why one entry, and only one, may move.
  const suppressed = [];
  const channel = { pageErrors, consoleErrors, suppressed, owedClockRewinds: 0 };
  page[ERROR_CHANNEL] = channel;
  page.on("pageerror", (err) => {
    const text = String(err);
    if (channel.owedClockRewinds > 0 && text === CLOCK_REWIND_ERROR) {
      channel.owedClockRewinds--;
      suppressed.push(text);
      return;
    }
    pageErrors.push(text);
  });
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  return { browser, context, page, pageErrors, consoleErrors, suppressed };
}

/**
 * Freeze the page clock. Called after the page has loaded, per Playwright's own
 * guidance: pausing during load can wedge a page waiting on a timer.
 */
export async function pauseClock(page) {
  // pauseAt refuses to travel backwards, and the page's timers have been
  // running since install() — so pause at wherever the fake clock has reached.
  //
  // Reading the clock and pausing it are two round trips, and the clock keeps
  // moving in between: Playwright re-syncs it to real time on a timer of at
  // most 100ms, so a slow round trip can carry it past `now + 1` and pauseAt
  // then throws "Cannot fast-forward to the past" (M16.14d — it used to take
  // down whichever browser suite was unlucky under load).
  //
  // The retry cannot lose. pauseAt stops the clock BEFORE it checks the target
  // (`_innerPause()` clears the real-time sync, and only then does it compare),
  // so by the time the first attempt has thrown, the clock is already frozen:
  // the second read is of a clock nothing can advance, and `now + 1` is
  // necessarily in its future. One retry is enough, and a second failure means
  // this reasoning has stopped being true — so it is raised, not swallowed.
  //
  // M16.18c: the retry recovers the script, but on Firefox the failed attempt
  // ALSO lands in the page-error channel every browser suite asserts is empty,
  // which is why a loaded machine could still redden a run that passed. The
  // mechanism, confirmed against both sides:
  //
  //   Playwright runs `__pwClock.controller.pauseAt(t)` in the page and takes
  //   its result back over the wire. Chromium and WebKit await the returned
  //   promise through the protocol, which attaches a real handler to it.
  //   Firefox's juggler instead watches it from outside, through the Debugger
  //   API (`Runtime.js`, `_awaitPromise` / `onPromiseSettled`), so nothing in
  //   the page ever handles the rejection. SpiderMonkey reports the unhandled
  //   rejection to the console service as a script error carrying an exception,
  //   and juggler turns exactly that into `Page.uncaughtError`
  //   (`PageAgent.js`, `_onRuntimeError`) — Playwright's `pageerror` event.
  //
  // So on Firefox every rejected evaluate is reported twice: once to the caller
  // and once to the page. The pause cannot be made race-free from here (the
  // target instant has to be computed in Node, and widening the margin would
  // fast-forward the fake clock by a load-dependent amount and move the
  // goldens), so the failure is accounted for instead of avoided.
  //
  // The accounting is deliberately the narrowest thing that works: one credit
  // per attempt we actually saw fail, spent only on a page error whose text is
  // exactly CLOCK_REWIND_ERROR, and the spent error is kept in `suppressed`
  // rather than dropped. A real fault cannot hide behind it — that string has
  // no other producer (see above), an unprovoked one still lands, and a second
  // one lands too.
  for (let attempt = 0; ; attempt++) {
    const now = await page.evaluate(() => Date.now());
    try {
      await page.clock.pauseAt(now + 1);
      return;
    } catch (e) {
      if (attempt > 0 || !/fast-forward to the past/i.test(String(e))) throw e;
      expectClockRewindError(page);
    }
  }
}

/**
 * Account for the page error the attempt that just failed is about to deposit.
 * It arrives after the API rejection, not before — measured, five times out of
 * five — so the ordinary case is to owe one and let the recorder spend it; the
 * already-arrived case is handled too rather than assumed away.
 *
 * A page the caller built itself has no channel to reconcile, and asserts
 * nothing about page errors either, so there is nothing to do for it.
 */
function expectClockRewindError(page) {
  const channel = page[ERROR_CHANNEL];
  if (!channel) return;
  const arrived = channel.pageErrors.indexOf(CLOCK_REWIND_ERROR);
  if (arrived !== -1) {
    channel.suppressed.push(...channel.pageErrors.splice(arrived, 1));
    return;
  }
  channel.owedClockRewinds++;
}

/** Advance the page's fake clock, firing every timer that comes due. */
export async function runClock(page, ms) {
  await page.clock.runFor(ms);
}

// ---------------------------------------------------------------------------
// In-page decoder
// ---------------------------------------------------------------------------

/**
 * Record every Image the page constructs, so the decoder can use the client's
 * OWN font atlas rather than a copy. Must run before navigation. Vite inlines
 * pc_ega.png as a data: URL (it is under the 4KB limit), so there is no network
 * resource to look for — the client's `new Image()` is the only handle on it.
 */
export async function installImageProbe(page) {
  await page.addInitScript(() => {
    window.__m169Images = [];
    const OriginalImage = window.Image;
    const Wrapped = function (...args) {
      const img = new OriginalImage(...args);
      window.__m169Images.push(img);
      return img;
    };
    Wrapped.prototype = OriginalImage.prototype;
    window.Image = Wrapped;
  });
}

/**
 * Install the decoder in the page. It reads the font atlas the client itself
 * loaded, builds a glyph-mask table, and exposes window.__m169 for
 * readGrid/expectedPNG/diffPNG.
 */
export async function installDecoder(page) {
  await page.waitForFunction(
    () =>
      (window.__m169Images || []).some((img) => img.src && img.complete) ||
      performance.getEntriesByType("resource").some((r) => /\.png(\?|$)/.test(r.name)),
    null,
    { timeout: 15000 },
  );
  await page.evaluate(async (ega) => {
    const CELL_W = 8;
    const CELL_H = 14;
    const COLS = 80;
    const ROWS = 25;
    const GLYPH_COLS = 32;

    const atlasURL =
      (window.__m169Images || []).map((img) => img.src).find((src) => src) ||
      performance
        .getEntriesByType("resource")
        .map((r) => r.name)
        .find((name) => /\.png(\?|$)/.test(name));
    if (!atlasURL) throw new Error("the client loaded no font atlas");

    const bitmap = await createImageBitmap(await (await fetch(atlasURL)).blob());
    const atlasCanvas = document.createElement("canvas");
    atlasCanvas.width = bitmap.width;
    atlasCanvas.height = bitmap.height;
    const atlasCtx = atlasCanvas.getContext("2d");
    atlasCtx.drawImage(bitmap, 0, 0);
    const atlas = atlasCtx.getImageData(0, 0, bitmap.width, bitmap.height).data;

    // main.ts treats a pixel as ink when r+g+b >= 50 (it punches everything
    // darker out to transparent). Same rule here, so the masks line up.
    const maskByChar = [];
    const inkCount = [];
    for (let ch = 0; ch < 256; ch += 1) {
      const gx = (ch % GLYPH_COLS) * CELL_W;
      const gy = Math.floor(ch / GLYPH_COLS) * CELL_H;
      const rows = [];
      let ink = 0;
      for (let y = 0; y < CELL_H; y += 1) {
        let bits = 0;
        for (let x = 0; x < CELL_W; x += 1) {
          const i = ((gy + y) * bitmap.width + gx + x) * 4;
          if (atlas[i] + atlas[i + 1] + atlas[i + 2] >= 50) {
            bits |= 1 << (7 - x);
            ink += 1;
          }
        }
        rows.push(bits.toString(16).padStart(2, "0"));
      }
      maskByChar.push(rows.join(""));
      inkCount.push(ink);
    }

    // Several CP437 codes share a bitmap (0x00/0x20/0xFF are all blank). A
    // visual golden can only name the picture, so each mask gets one
    // representative: space and the full block where they apply, else the
    // lowest code.
    const charByMask = new Map();
    for (let ch = 255; ch >= 0; ch -= 1) charByMask.set(maskByChar[ch], ch);
    charByMask.set(maskByChar[0x20], 0x20);
    charByMask.set(maskByChar[0xdb], 0xdb);

    const rgbToIndex = new Map();
    for (let i = 0; i < ega.length; i += 1) {
      const hex = ega[i];
      const key = `${parseInt(hex.slice(1, 3), 16)},${parseInt(hex.slice(3, 5), 16)},${parseInt(hex.slice(5, 7), 16)}`;
      rgbToIndex.set(key, i);
    }

    // One pre-tinted atlas per foreground colour, exactly as main.ts builds it,
    // so the harness can re-render a golden for the expected/diff artifacts.
    const punched = document.createElement("canvas");
    punched.width = bitmap.width;
    punched.height = bitmap.height;
    const punchedCtx = punched.getContext("2d");
    punchedCtx.drawImage(bitmap, 0, 0);
    const img = punchedCtx.getImageData(0, 0, bitmap.width, bitmap.height);
    for (let i = 0; i < img.data.length; i += 4) {
      if (img.data[i] + img.data[i + 1] + img.data[i + 2] < 50) {
        img.data[i + 3] = 0;
      } else {
        img.data[i] = 255;
        img.data[i + 1] = 255;
        img.data[i + 2] = 255;
        img.data[i + 3] = 255;
      }
    }
    punchedCtx.putImageData(img, 0, 0);
    const tinted = [];
    for (let i = 0; i < 16; i += 1) {
      const c = document.createElement("canvas");
      c.width = bitmap.width;
      c.height = bitmap.height;
      const cx = c.getContext("2d");
      cx.imageSmoothingEnabled = false;
      cx.drawImage(punched, 0, 0);
      cx.globalCompositeOperation = "source-in";
      cx.fillStyle = ega[i];
      cx.fillRect(0, 0, c.width, c.height);
      tinted.push(c);
    }

    function screenPixels() {
      const canvas = document.querySelector("canvas[data-screen]");
      if (!canvas) throw new Error("no screen canvas");
      const ctx = canvas.getContext("2d");
      return { data: ctx.getImageData(0, 0, canvas.width, canvas.height).data, width: canvas.width };
    }

    function decodeCell(data, width, col, row) {
      const x0 = col * CELL_W;
      const y0 = row * CELL_H;
      const indices = new Int8Array(CELL_W * CELL_H);
      const seen = [];
      for (let y = 0; y < CELL_H; y += 1) {
        for (let x = 0; x < CELL_W; x += 1) {
          const i = ((y0 + y) * width + x0 + x) * 4;
          const key = `${data[i]},${data[i + 1]},${data[i + 2]}`;
          const idx = rgbToIndex.get(key);
          if (idx === undefined) {
            return { error: `cell (${col},${row}) pixel (${x},${y}) is rgb(${key}), not an EGA colour` };
          }
          indices[y * CELL_W + x] = idx;
          if (seen.indexOf(idx) < 0) seen.push(idx);
        }
      }
      if (seen.length === 1) {
        return { ch: 0x20, fg: seen[0], bg: seen[0], uniform: true };
      }
      if (seen.length > 2) {
        return { error: `cell (${col},${row}) has ${seen.length} colours; a text cell has at most two` };
      }
      const candidates = [];
      for (const ink of seen) {
        const rows = [];
        let count = 0;
        for (let y = 0; y < CELL_H; y += 1) {
          let bits = 0;
          for (let x = 0; x < CELL_W; x += 1) {
            if (indices[y * CELL_W + x] === ink) {
              bits |= 1 << (7 - x);
              count += 1;
            }
          }
          rows.push(bits.toString(16).padStart(2, "0"));
        }
        const mask = rows.join("");
        const ch = charByMask.get(mask);
        if (ch !== undefined) {
          candidates.push({ ch, fg: ink, bg: seen.find((c) => c !== ink), ink: count });
        }
      }
      if (candidates.length === 0) {
        return { error: `cell (${col},${row}) matches no glyph in the atlas` };
      }
      // Some CP437 glyphs are each other's exact inverse (0x07 the bullet and
      // 0x08 the inverse bullet, say), so "white 0x08 on black" and "black 0x07
      // on white" are the SAME pixels. Pixels cannot break that tie and neither
      // will this decoder: it reports the lower character code and hands the
      // other interpretation back as `alt`, which the comparison accepts too.
      candidates.sort((a, b) => a.ch - b.ch);
      const best = candidates[0];
      const alt = candidates[1] || null;
      // Soundness: what we decoded must reproduce what we read.
      const mask = maskByChar[best.ch];
      for (let y = 0; y < CELL_H; y += 1) {
        const bits = parseInt(mask.slice(y * 2, y * 2 + 2), 16);
        for (let x = 0; x < CELL_W; x += 1) {
          const want = bits & (1 << (7 - x)) ? best.fg : best.bg;
          if (indices[y * CELL_W + x] !== want) {
            return { error: `cell (${col},${row}) decoded to char 0x${best.ch.toString(16)} but does not render back to itself` };
          }
        }
      }
      return { ch: best.ch, fg: best.fg, bg: best.bg, alt };
    }

    function drawGrid(ctx, cells) {
      for (let row = 0; row < ROWS; row += 1) {
        for (let col = 0; col < COLS; col += 1) {
          const cell = cells[row * COLS + col];
          const fg = cell.color & 0x0f;
          const bg = (cell.color >> 4) & 0x0f;
          ctx.fillStyle = ega[bg];
          ctx.fillRect(col * CELL_W, row * CELL_H, CELL_W, CELL_H);
          ctx.drawImage(
            tinted[fg],
            (cell.ch % GLYPH_COLS) * CELL_W,
            Math.floor(cell.ch / GLYPH_COLS) * CELL_H,
            CELL_W,
            CELL_H,
            col * CELL_W,
            row * CELL_H,
            CELL_W,
            CELL_H,
          );
        }
      }
    }

    window.__m169 = {
      readGrid() {
        const { data, width } = screenPixels();
        const cells = [];
        const errors = [];
        for (let row = 0; row < ROWS; row += 1) {
          for (let col = 0; col < COLS; col += 1) {
            const cell = decodeCell(data, width, col, row);
            if (cell.error) {
              errors.push(cell.error);
              cells.push({ ch: 0x3f, color: 0x0f, undecoded: true });
            } else {
              cells.push({
                ch: cell.ch,
                color: (cell.bg << 4) | cell.fg,
                uniform: !!cell.uniform,
                alt: cell.alt ? { ch: cell.alt.ch, color: (cell.alt.bg << 4) | cell.alt.fg } : null,
              });
            }
          }
        }
        return { cells, errors };
      },
      actualPNG() {
        return document.querySelector("canvas[data-screen]").toDataURL("image/png");
      },
      expectedPNG(cells) {
        const c = document.createElement("canvas");
        c.width = COLS * CELL_W;
        c.height = ROWS * CELL_H;
        const ctx = c.getContext("2d");
        ctx.imageSmoothingEnabled = false;
        drawGrid(ctx, cells);
        return c.toDataURL("image/png");
      },
      diffPNG(mismatches) {
        const screen = document.querySelector("canvas[data-screen]");
        const c = document.createElement("canvas");
        c.width = screen.width;
        c.height = screen.height;
        const ctx = c.getContext("2d");
        ctx.drawImage(screen, 0, 0);
        ctx.globalAlpha = 0.45;
        ctx.fillStyle = "#000000";
        ctx.fillRect(0, 0, c.width, c.height);
        ctx.globalAlpha = 1;
        ctx.strokeStyle = "#ff00ff";
        ctx.lineWidth = 1;
        for (const m of mismatches) {
          ctx.strokeRect(m.x * CELL_W + 0.5, m.y * CELL_H + 0.5, CELL_W - 1, CELL_H - 1);
        }
        return c.toDataURL("image/png");
      },
    };
  }, EGA);
}

/** Read the canvas as an 80x25 grid of {ch, color} cells. */
export async function readGrid(page) {
  const grid = await page.evaluate(() => window.__m169.readGrid());
  if (grid.errors.length > 0) {
    throw new Error(`canvas did not decode:\n  ${grid.errors.slice(0, 10).join("\n  ")}`);
  }
  return grid.cells;
}

// ---------------------------------------------------------------------------
// Reading the grid
// ---------------------------------------------------------------------------

export function cellAt(cells, col, row) {
  return cells[row * COLS + col];
}

/** The characters of one row (or a run of it) as a JS string. */
export function textAt(cells, col, row, len = COLS - col) {
  let out = "";
  for (let i = 0; i < len; i += 1) out += String.fromCharCode(cellAt(cells, col + i, row).ch);
  return out;
}

/** Where `needle` appears on screen, or null. Rows are searched top to bottom. */
export function findText(cells, needle) {
  for (let row = 0; row < ROWS; row += 1) {
    const col = textAt(cells, 0, row).indexOf(needle);
    if (col >= 0) return { x: col, y: row };
  }
  return null;
}

export function hasText(cells, needle) {
  return findText(cells, needle) !== null;
}

/** Poll the canvas until `pred(cells)` holds; throws with the screen on timeout. */
export async function waitForGrid(page, pred, describe, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  for (;;) {
    last = await readGrid(page);
    if (pred(last)) return last;
    if (Date.now() > deadline) {
      saveText(`timeout-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(last));
      throw new Error(`timed out waiting for ${describe}; the screen was:\n${gridToArt(last)}`);
    }
    await page.waitForTimeout(80);
  }
}

/**
 * Wait for something the server can only produce by ticking — an object running
 * its :touch program, a message expiring — by taking ticks until it appears.
 * Plain waitForGrid would sit there forever: in this harness nothing advances
 * unless this script asks it to.
 */
export async function tickUntilGrid(page, pred, describe, maxTicks = 24) {
  for (let i = 0; i <= maxTicks; i += 1) {
    const cells = await waitForQuiet(page, 1500);
    if (pred(cells)) return cells;
    if (i === maxTicks) {
      saveText(`timeout-${describe.replace(/\W+/g, "-")}.txt`, gridToArt(cells));
      throw new Error(`${describe} did not appear within ${maxTicks} ticks; the screen was:\n${gridToArt(cells)}`);
    }
    await idle(1);
  }
  throw new Error("unreachable");
}

/**
 * Wait until the canvas stops changing. Server messages are not timer-driven,
 * so a tick's diff arrives whenever the socket delivers it; two identical reads
 * in a row is the cheapest honest "the frame has landed".
 */
export async function waitForQuiet(page, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs;
  let previous = JSON.stringify(await readGrid(page));
  for (;;) {
    await page.waitForTimeout(120);
    const current = await readGrid(page);
    const serialised = JSON.stringify(current);
    if (serialised === previous) return current;
    previous = serialised;
    if (Date.now() > deadline) return current;
  }
}

/** The screen as reviewable ASCII art (non-printable CP437 becomes '·'). */
export function gridToArt(cells) {
  const lines = [];
  for (let row = 0; row < ROWS; row += 1) lines.push(art(cells, row));
  return lines.join("\n");
}

// ---------------------------------------------------------------------------
// Goldens
// ---------------------------------------------------------------------------

const PRINTABLE = /[ -~]/;

function art(cells, row) {
  let out = "";
  for (let col = 0; col < COLS; col += 1) {
    const ch = String.fromCharCode(cells[row * COLS + col].ch);
    out += PRINTABLE.test(ch) ? ch : "·";
  }
  return out;
}

function hex(cells, row, key) {
  let out = "";
  for (let col = 0; col < COLS; col += 1) {
    out += cells[row * COLS + col][key].toString(16).padStart(2, "0");
  }
  return out;
}

/** Serialise a grid: hex truth for the comparison, ASCII art for the reviewer. */
export function gridToGolden(name, note, cells) {
  const rows = [];
  for (let row = 0; row < ROWS; row += 1) {
    rows.push({ art: art(cells, row), ch: hex(cells, row, "ch"), color: hex(cells, row, "color") });
  }
  return { name, note, rows };
}

export function goldenToGrid(golden) {
  const cells = [];
  for (let row = 0; row < ROWS; row += 1) {
    const { ch, color } = golden.rows[row];
    for (let col = 0; col < COLS; col += 1) {
      cells.push({
        ch: parseInt(ch.slice(col * 2, col * 2 + 2), 16),
        color: parseInt(color.slice(col * 2, col * 2 + 2), 16),
      });
    }
  }
  return cells;
}

function describe(cell) {
  const ch = String.fromCharCode(cell.ch);
  const glyph = PRINTABLE.test(ch) ? `'${ch}'` : " ";
  return `0x${cell.ch.toString(16).padStart(2, "0")}${glyph} colour 0x${cell.color.toString(16).padStart(2, "0")}`;
}

/** Cell-level differences between two grids, expected first. */
export function diffGrids(expected, actual) {
  const out = [];
  for (let row = 0; row < ROWS; row += 1) {
    for (let col = 0; col < COLS; col += 1) {
      const i = row * COLS + col;
      const readings = [actual[i], actual[i].alt].filter(Boolean);
      const same = readings.some((r) => r.ch === expected[i].ch && r.color === expected[i].color);
      if (!same) {
        out.push({ x: col, y: row, expected: expected[i], actual: actual[i] });
      }
    }
  }
  return out;
}

export function formatDiff(name, mismatches) {
  const lines = [`${name}: ${mismatches.length} cell(s) differ`];
  for (const m of mismatches.slice(0, 40)) {
    lines.push(`  (col ${m.x}, row ${m.y}): expected ${describe(m.expected)}, got ${describe(m.actual)}`);
  }
  if (mismatches.length > 40) lines.push(`  … and ${mismatches.length - 40} more`);
  return lines.join("\n");
}

function writeDataURL(file, dataURL) {
  fs.writeFileSync(file, Buffer.from(dataURL.split(",")[1], "base64"));
}

/**
 * Write the three images a reviewer needs — what the browser drew, what the
 * golden says, and where they differ — plus the cell-level text diff. Returns
 * the paths, which the caller puts in the failure message.
 */
export async function writeArtifacts(page, name, expectedCells, actualCells, mismatches) {
  fs.mkdirSync(resultsDir, { recursive: true });
  const stem = path.join(resultsDir, `golden-${name}`);
  writeDataURL(`${stem}.actual.png`, await page.evaluate(() => window.__m169.actualPNG()));
  writeDataURL(
    `${stem}.expected.png`,
    await page.evaluate((cells) => window.__m169.expectedPNG(cells), expectedCells),
  );
  writeDataURL(
    `${stem}.diff.png`,
    await page.evaluate((ms) => window.__m169.diffPNG(ms), mismatches.map((m) => ({ x: m.x, y: m.y }))),
  );
  fs.writeFileSync(`${stem}.diff.txt`, formatDiff(name, mismatches) + "\n");
  fs.writeFileSync(
    `${stem}.actual.json`,
    JSON.stringify(gridToGolden(name, "captured on failure", actualCells), null, 2) + "\n",
  );
  return [`${stem}.actual.png`, `${stem}.expected.png`, `${stem}.diff.png`, `${stem}.diff.txt`];
}

export function goldenPath(name) {
  return path.join(goldenDir, `${name}.json`);
}

export function loadGolden(name) {
  const file = goldenPath(name);
  if (!fs.existsSync(file)) return null;
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

export function saveGolden(name, note, cells) {
  fs.mkdirSync(goldenDir, { recursive: true });
  fs.writeFileSync(goldenPath(name), JSON.stringify(gridToGolden(name, note, cells), null, 2) + "\n");
}

/**
 * Capture the canvas and check it against the reviewed golden. With
 * GOLDEN_UPDATE=1 it records instead — which is a reviewing operation, not a
 * passing one: the recorded art must be read before it is committed.
 */
export async function checkGolden(page, name, note) {
  const actual = await readGrid(page);
  if (updateGoldens) {
    saveGolden(name, note, actual);
    console.log(`  ~ recorded golden ${name}`);
    return actual;
  }
  const golden = loadGolden(name);
  if (!golden) {
    throw new Error(`no golden for ${name}; record it with GOLDEN_UPDATE=1 and review the art before committing`);
  }
  const expected = goldenToGrid(golden);
  const mismatches = diffGrids(expected, actual);
  if (mismatches.length > 0) {
    const files = await writeArtifacts(page, name, expected, actual, mismatches);
    throw new Error(`${formatDiff(name, mismatches)}\n  artifacts: ${files.join(", ")}`);
  }
  console.log(`  ✓ golden ${name}`);
  return actual;
}

// ---------------------------------------------------------------------------
// The control listener (engine/m16_9_test.go)
// ---------------------------------------------------------------------------

/**
 * Take `n` server ticks. With `await`, each tick waits for the browser's own
 * input frame to arrive first — that is the tick lock: the input is still a real
 * keystroke, but the tick that consumes it is taken only once it has landed.
 * Without it, the server refuses to step while an input is pending, so a lost
 * frame is reported where it happened instead of as a hash mismatch later.
 */
export async function step(options = {}) {
  const response = await fetch(`${controlURL}/control/step`, {
    method: "POST",
    body: JSON.stringify({ n: options.n || 1, await: options.await, timeoutMs: options.timeoutMs || 10000 }),
    signal: AbortSignal.timeout((options.timeoutMs || 10000) + 5000),
  });
  const text = await response.text();
  if (!response.ok) throw new Error(`control /step failed (${response.status}): ${text.trim()}`);
  return JSON.parse(text);
}

export async function serverState() {
  const response = await fetch(`${controlURL}/control/state`, { signal: AbortSignal.timeout(15000) });
  if (!response.ok) throw new Error(`control /state failed (${response.status})`);
  const state = await response.json();
  // Go marshals an empty slice as null; "no input is pending" is the assertion
  // several M16.10 checks are built on, so normalize it to an array here rather
  // than making every caller spell the difference.
  state.pending = state.pending ?? [];
  state.players = state.players ?? [];
  return state;
}

// The server turns a movement keymask into a delta AND the scancode
// ElementPlayerTick switches on (inputMessageToPlayerInput; input.go:17-21), so
// the awaited input carries both.
// The numeric keypad's 8/4/6/2 fold into the SAME mask bits as the arrows
// (keys.ts movementMask), which is vanilla's own vocabulary — so they produce
// byte-identical input frames and `walk` takes either name.
const DIRECTIONS = {
  ArrowUp: { dx: 0, dy: -1, key: 0xc8 },
  ArrowDown: { dx: 0, dy: 1, key: 0xd0 },
  ArrowLeft: { dx: -1, dy: 0, key: 0xcb },
  ArrowRight: { dx: 1, dy: 0, key: 0xcd },
  Numpad8: { dx: 0, dy: -1, key: 0xc8 },
  Numpad2: { dx: 0, dy: 1, key: 0xd0 },
  Numpad4: { dx: -1, dy: 0, key: 0xcb },
  Numpad6: { dx: 1, dy: 0, key: 0xcd },
};

/**
 * Walk `n` tiles, one tile per tick. The browser sends a frame on keydown and
 * then one per 55ms sampler firing; the fake clock means the sampler fires only
 * when we advance it, so this loop produces exactly one frame per tick.
 */
export async function walk(page, code, n) {
  const dir = DIRECTIONS[code];
  assert.ok(dir, `walk() needs an arrow key, got ${code}`);
  await page.keyboard.down(code);
  for (let i = 0; i < n; i += 1) {
    await step({ await: { dx: dir.dx, dy: dir.dy, key: dir.key } });
    if (i < n - 1) await runClock(page, 60);
  }
  await page.keyboard.up(code);
  // The keyup's zero frame is consumed by its own tick, so no straggler is left
  // to land inside a later one.
  await step({ await: { dx: 0, dy: 0 } });
}

/** Press a play-mode command key (T, P, S, Q, ?) and take the tick that runs it. */
export async function command(page, code, keyByte) {
  await page.keyboard.press(code);
  await step({ await: { key: keyByte } });
}

/**
 * Shift+direction: BoardShoot along that direction, which also becomes the
 * player's facing (elements.go:1422-1425). Shift is released LAST so the final
 * frame this leaves pending is the all-zero one; the shift-only frame in between
 * would shoot again if a tick ever landed on it, and awaiting zero is what makes
 * sure none does.
 */
export async function shootShift(page, code) {
  const dir = DIRECTIONS[code];
  assert.ok(dir, `shootShift() needs an arrow key, got ${code}`);
  await page.keyboard.down("ShiftLeft");
  await page.keyboard.down(code);
  await step({ await: { dx: dir.dx, dy: dir.dy, key: dir.key, shift: true } });
  await page.keyboard.up(code);
  await page.keyboard.up("ShiftLeft");
  await step({ await: { dx: 0, dy: 0 } });
}

/**
 * Space: shoot along the last direction walked. The keymask's shoot bit reaches
 * the engine as InputKeyPressed == ' ' with no delta (inputMessageToPlayerInput),
 * which is the `pState.DirX/DirY` branch of ElementPlayerTick.
 */
export async function shootSpace(page) {
  await page.keyboard.down("Space");
  await step({ await: { key: 0x20, shift: true } });
  await page.keyboard.up("Space");
  await step({ await: { dx: 0, dy: 0 } });
}

/**
 * Press a key that must reach the server as NOTHING — a removed binding, or a
 * key the open modal owns. Returns the control listener's view so the caller can
 * assert on `pending`, which is the only place a stray input frame could hide.
 */
export async function pressExpectingNoInput(page, code) {
  await page.keyboard.press(code);
  // The client sends on keydown, so a frame it decided to send is already in
  // flight; give it a real chance to arrive before declaring the absence.
  await page.waitForTimeout(50);
  return serverState();
}

/** Take `n` ticks with no input at all. */
export async function idle(n = 1) {
  return step({ n });
}

export function saveText(name, text) {
  fs.mkdirSync(resultsDir, { recursive: true });
  fs.writeFileSync(path.join(resultsDir, name), text);
}
