// M16.14d — pauseClock must not lose the race it used to lose.
//
// The golden library freezes the page clock by reading it and then pausing it
// one millisecond later (lib/canvas.mjs, pauseClock). Those are two round trips,
// and Playwright's fake clock keeps advancing in between — it re-syncs itself to
// real time on a timer of at most 100ms — so a slow round trip carries the clock
// past the requested instant and pauseAt throws "Cannot fast-forward to the
// past". That is not hypothetical: it took down whichever browser suite was
// unlucky whenever the machine was loaded (NOTES.md 2026-07-31).
//
// A suite that happens to pass proves nothing about a race, so this test FORCES
// the losing case: page.evaluate is wrapped so every read is followed by a real
// delay long enough for the clock's re-sync to fire. Under that harness the old
// one-shot implementation is required to fail — otherwise the test would be
// proving nothing — and the real pauseClock is required to survive.

import assert from "node:assert/strict";
import { chromium } from "playwright";
import { pauseClock } from "./lib/canvas.mjs";

const CLOCK_TIME = "2026-01-01T00:00:00.000Z";
// Comfortably longer than the clock's own 100ms re-sync timer, so the clock has
// certainly moved past `now + 1` by the time the pause lands.
const STALL_MS = 300;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browser = await chromium.launch({ headless: true });

/**
 * A page whose clock is installed and ticking, with a timer of its own running
 * so it looks like the client rather than an idle document. Each case gets its
 * own context: a failed pauseAt leaves the clock stopped, which would hand the
 * next case a race that no longer exists.
 */
async function newTickingPage() {
  const context = await browser.newContext();
  await context.clock.install({ time: CLOCK_TIME });
  const page = await context.newPage();
  await page.goto("about:blank");
  await page.evaluate(() => {
    window.__ticks = 0;
    setInterval(() => { window.__ticks++; }, 10);
  });
  return { context, page };
}

/** The same page, with every clock read followed by a real-time stall. */
function withSlowRoundTrip(page) {
  return {
    clock: page.clock,
    async evaluate(...args) {
      const value = await page.evaluate(...args);
      await sleep(STALL_MS);
      return value;
    },
  };
}

// ---------------------------------------------------------------------------
// 1. The harness really does force the failure
// ---------------------------------------------------------------------------

{
  const { context, page } = await newTickingPage();
  const slow = withSlowRoundTrip(page);

  // pauseClock as it was written before M16.14d: read once, pause once.
  const naive = async (p) => {
    const now = await p.evaluate(() => Date.now());
    await p.clock.pauseAt(now + 1);
  };

  let failure = null;
  try {
    await naive(slow);
  } catch (e) {
    failure = e;
  }
  assert.ok(
    failure,
    "the slow-round-trip harness did not make the one-shot pause fail, so it " +
      "cannot prove anything about the retry either",
  );
  assert.match(String(failure), /fast-forward to the past/i);
  await context.close();
  console.log("· the forced slow round trip does make a one-shot pause throw");
}

// ---------------------------------------------------------------------------
// 2. pauseClock survives it, and the clock really is stopped afterwards
// ---------------------------------------------------------------------------

{
  const { context, page } = await newTickingPage();

  await pauseClock(withSlowRoundTrip(page));
  console.log("· pauseClock survived the same forced slow round trip");

  // Paused means paused: real time passes, the page's own interval is due many
  // times over, and neither the clock nor the timer may move.
  const clockBefore = await page.evaluate(() => Date.now());
  const ticksBefore = await page.evaluate(() => window.__ticks);
  await sleep(STALL_MS);
  const clockAfter = await page.evaluate(() => Date.now());
  const ticksAfter = await page.evaluate(() => window.__ticks);

  assert.equal(clockAfter, clockBefore, "the paused clock moved");
  assert.equal(ticksAfter, ticksBefore, "a timer fired under the paused clock");
  console.log("· the clock is genuinely frozen: no drift and no timer fired");

  // And the frozen clock is still usable the way the suites use it.
  await page.clock.runFor(100);
  const clockRun = await page.evaluate(() => Date.now());
  assert.equal(clockRun - clockAfter, 100, "runFor did not advance by exactly 100ms");
  assert.ok((await page.evaluate(() => window.__ticks)) > ticksAfter, "runFor fired no timer");
  console.log("· runFor still advances the paused clock by exactly what it is asked for");

  await context.close();
}

// ---------------------------------------------------------------------------
// 3. The ordinary path is untouched
// ---------------------------------------------------------------------------

{
  const { context, page } = await newTickingPage();
  await pauseClock(page);
  const a = await page.evaluate(() => Date.now());
  await sleep(50);
  assert.equal(await page.evaluate(() => Date.now()), a, "the paused clock moved");
  await context.close();
  console.log("· an ordinary (fast) pause still pauses");
}

await browser.close();
console.log("pause_clock: OK");
