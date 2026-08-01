// M16.18c — pauseClock's recovered attempt must not redden the run it recovered.
//
// M16.14d made pauseClock retry the "Cannot fast-forward to the past" it loses
// under load, on the reasoning that the retry cannot lose. It cannot — but on
// Firefox the failed FIRST attempt also reaches `page.on("pageerror")`, the
// channel every browser suite ends by asserting is empty, so a loaded machine
// still turned a passing run red (twice during M16.18a, on firefox-desktop).
//
// Why Firefox and not the others: Playwright evaluates the pause inside the
// page and takes the result back over the wire. Chromium and WebKit await the
// returned promise through the protocol, which attaches a handler to it.
// Firefox's juggler watches it from outside instead, through the Debugger API,
// so nothing in the page ever handles the rejection; SpiderMonkey reports the
// unhandled rejection to the console service and juggler forwards that as
// Page.uncaughtError. Every rejected evaluate is therefore reported twice.
//
// This script forces the losing attempt rather than waiting for a loaded
// machine to supply one — Date.now is shadowed in the page to answer once from
// the past, which is exactly what a slow round trip does — and then checks all
// four halves of the claim: the run is green, the error was accounted for
// rather than never provoked, an unprovoked one still lands, and so does a
// second one.

import assert from "node:assert/strict";
import { launchGoldenBrowser, pauseClock } from "./lib/canvas.mjs";

const REWIND = "Error: Cannot fast-forward to the past";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** A loaded document with a timer of its own, so the clock has work to run. */
async function ready(page) {
  await page.goto("about:blank");
  await page.evaluate(() => {
    window.__ticks = 0;
    setInterval(() => { window.__ticks++; }, 10);
  });
}

/**
 * Make the page's next clock read answer from the past, and only the next one.
 * pauseClock reads Date.now() and pauses at now+1, so a single stale answer
 * makes attempt 0 fail and leaves attempt 1 — the one that must recover — with
 * an honest clock. Shadowing the read is the same thing a slow round trip does
 * and far more repeatable than arranging real load.
 */
async function poisonNextClockRead(page, backwardsMs) {
  await page.evaluate((back) => {
    const real = Date.now;
    let first = true;
    Date.now = () => {
      const t = real.call(Date);
      if (!first) return t;
      first = false;
      return t - back;
    };
  }, backwardsMs);
}

/** Page errors settle after the API rejection, so give them room to arrive. */
async function settle() {
  await sleep(750);
}

// ---------------------------------------------------------------------------
// 1. Firefox: the failed attempt is provoked, accounted for, and invisible
// ---------------------------------------------------------------------------

{
  const { browser, page, pageErrors, suppressed } = await launchGoldenBrowser({
    engine: "firefox",
  });
  await ready(page);
  await poisonNextClockRead(page, 5000);

  await pauseClock(page);
  await settle();

  // Both halves matter. Green alone would also be what a test that never
  // provoked the failure reports — M16.18a found one of those.
  assert.deepEqual(pageErrors, [], "the recovered attempt still reddened the run");
  assert.deepEqual(
    suppressed,
    [REWIND],
    "the failing attempt was not provoked at all, so the green above proves nothing",
  );
  console.log("· firefox: the failed attempt was provoked, accounted for, and left the run green");

  // Accounted for is not the same as ignored: an unprovoked one still lands.
  // The clock is paused now, so any target behind it fails the same way.
  const now = await page.evaluate(() => Date.now());
  await assert.rejects(() => page.clock.pauseAt(now - 5000));
  await settle();
  assert.deepEqual(
    pageErrors,
    [REWIND],
    "a clock rewind nobody accounted for was swallowed",
  );
  console.log("· firefox: an unaccounted-for rewind still reaches the page-error channel");

  // And the credit was one-shot, not a standing exemption.
  await assert.rejects(() => page.clock.pauseAt(now - 5000));
  await settle();
  assert.deepEqual(pageErrors, [REWIND, REWIND], "the accounting was not one-shot");
  assert.deepEqual(suppressed, [REWIND], "a second error was moved aside on one credit");
  console.log("· firefox: the credit is spent once, not standing");

  await browser.close();
}

// ---------------------------------------------------------------------------
// 2. A genuine page fault is untouched
// ---------------------------------------------------------------------------

{
  const { browser, page, pageErrors, suppressed } = await launchGoldenBrowser({
    engine: "firefox",
  });
  await ready(page);
  await poisonNextClockRead(page, 5000);
  await pauseClock(page);

  // A page fault of the shape a suite exists to catch, raised while the credit
  // from the pause above is still outstanding. The fake clock re-raises a
  // timer's exception to whoever advanced it, so runFor rejects — and that
  // rejection is what carries the fault into the page-error channel, by the
  // very Firefox path the accounting above sits on. If the accounting were a
  // string match with no credit behind it, this is the report it would eat.
  await page.evaluate(() => {
    setTimeout(() => { throw new Error("client blew up"); }, 0);
  });
  await assert.rejects(() => page.clock.runFor(10), /client blew up/);
  await settle();

  assert.equal(pageErrors.length, 1, `expected exactly the client fault, got ${JSON.stringify(pageErrors)}`);
  assert.match(pageErrors[0], /client blew up/);
  assert.deepEqual(suppressed, [REWIND]);
  console.log("· firefox: a real page fault is reported while the accounting is outstanding");

  await browser.close();
}

// ---------------------------------------------------------------------------
// 3. Chromium never had the symptom, and does not gain one
// ---------------------------------------------------------------------------

{
  const { browser, page, pageErrors, suppressed } = await launchGoldenBrowser();
  await ready(page);
  await poisonNextClockRead(page, 5000);

  await pauseClock(page);
  await settle();

  assert.deepEqual(pageErrors, []);
  // Chromium reports the rejection only to the caller, so the credit is owed
  // and never spent — which is the point: nothing is moved aside speculatively.
  assert.deepEqual(suppressed, [], "chromium moved an error aside it never reported");
  console.log("· chromium: unchanged — no page error to account for, and none invented");

  // The clock is still genuinely frozen after the failed first attempt.
  const a = await page.evaluate(() => Date.now());
  await sleep(200);
  assert.equal(await page.evaluate(() => Date.now()), a, "the paused clock moved");

  await browser.close();
}

console.log("pause_clock_errors: OK");
