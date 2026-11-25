// M37.8 — the launch name box offers Google sign-in, quietly, and only there.
//
// Driven by engine/m37_8_browser_test.go, which hosts COLLAB on M16.14's
// harness: the production server objects with sign-in on and a hermetic fake
// Google on the control listener, so the whole redirect chain is real.
//
// WHAT THIS PROVES THAT modal.test.mjs CANNOT. The unit test owns the box: the
// hint row, Tab, and G staying a letter. Only a browser shows that main.ts
// learns sign-in exists from /api/auth/me and offers it in the open box, that
// Tab (and, on a phone, the bar's Google button) really leaves for Google and
// comes back signed in, and that a guest who just types a name is untouched.

import assert from "node:assert/strict";
import {
  baseURL,
  controlURL,
  hasText,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  markProfileWarm,
  readGrid,
  textAt,
  waitForGrid,
} from "./lib/canvas.mjs";

const WORLD = "COLLAB";
const KEY_HINT = "Optional: press Tab to sign in with Google.";
const TAP_HINT = "Optional: tap Google below to sign in.";

async function armAccount(account) {
  const response = await fetch(`${controlURL}/control/auth/next?account=${encodeURIComponent(account)}`, { signal: AbortSignal.timeout(20000) });
  if (!response.ok) throw new Error(`arming ${account} failed (${response.status})`);
}

async function open({ hasTouch = false } = {}) {
  const viewport = hasTouch ? { width: 844, height: 390 } : undefined;
  const b = await launchGoldenBrowser(hasTouch ? { hasTouch, viewport } : {});
  await markProfileWarm(b.context);
  await installImageProbe(b.page);
  await load(b.page);
  return b;
}

async function load(page) {
  // /play/COLLAB names the world, so the launch flow asks only for a name, and
  // the sign-in round trip returns to this same address.
  const response = await page.goto(`${baseURL}/play/${WORLD}`);
  assert.equal(response?.status(), 200, "the client index must be served");
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
}

/** Tab or a tap leaves for Google; the marker cannot survive the navigation. */
async function signInFromTheBox(page, act) {
  await page.evaluate(() => { window.__m378BeforeLogin = true; });
  await act();
  await page.waitForFunction(() => !window.__m378BeforeLogin, null, { timeout: 30000 });
  await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
  await installDecoder(page);
}

const failures = [];
const browsers = [];
try {
  // A keyboard visitor signs in from the box.
  {
    const ada = await open();
    browsers.push(ada);
    const { page } = ada;
    await waitForGrid(page, (c) => hasText(c, "Type your name") && hasText(c, KEY_HINT), "the name box with the sign-in hint");
    // G is a letter here: a name may start with it, and nothing navigates.
    await page.keyboard.type("G");
    await new Promise((resolve) => setTimeout(resolve, 300));
    assert.ok(page.url().endsWith(`/play/${WORLD}`), "G does not leave for Google");
    await page.keyboard.press("Backspace");

    await armAccount("ada");
    await signInFromTheBox(page, () => page.keyboard.press("Tab"));
    // Back from Google: the same box, the same question, and no offer to sign in again.
    let cells = await waitForGrid(page, (c) => hasText(c, "Type your name"), "the name box after signing in");
    await new Promise((resolve) => setTimeout(resolve, 500));
    cells = await readGrid(page);
    assert.ok(!hasText(cells, KEY_HINT), "a signed-in visitor is not offered sign-in");
    await page.keyboard.type("Ada");
    await page.keyboard.press("Enter");
    cells = await waitForGrid(page, (c) => hasText(c, "E  Board editor") && textAt(c, 65, 24, 15).trim() === "Ada Lovelace",
      "the title sidebar to name the account");
    assert.equal(textAt(cells, 62, 24, 3), " G ", "the title keeps its sign-in row");
    console.log("  · Tab in the name box signed in through the identity provider");
  }

  // A guest just types a name: the offer changes nothing about that path.
  {
    const guest = await open();
    browsers.push(guest);
    const { page } = guest;
    await waitForGrid(page, (c) => hasText(c, KEY_HINT), "the guest's name box with the hint");
    await page.keyboard.type("Gus");
    await page.keyboard.press("Enter");
    const cells = await waitForGrid(page, (c) => hasText(c, "E  Board editor"), "the guest's title screen");
    assert.equal(textAt(cells, 65, 24, 15).trim(), "Google sign-in", "a guest stays a guest");
    console.log("  · a guest typed a name and played on");
  }

  // A phone has no Tab: the bar's Google button takes the same offer.
  {
    const phone = await open({ hasTouch: true });
    browsers.push(phone);
    const { page } = phone;
    const google = page.locator('.touch-controls button[data-touch="google"]');
    await waitForGrid(page, (c) => hasText(c, TAP_HINT), "the phone's name box with the tap hint");
    assert.equal(await google.isVisible(), true, "the bar offers Google while the box does");
    await armAccount("bob");
    await signInFromTheBox(page, () => google.tap());
    await waitForGrid(page, (c) => hasText(c, "Type your name"), "the phone's name box after signing in");
    await new Promise((resolve) => setTimeout(resolve, 500));
    assert.ok(!hasText(await readGrid(page), TAP_HINT), "a signed-in phone is not offered sign-in");
    assert.equal(await google.isVisible(), false, "the Google button goes with the offer");
    await page.keyboard.type("Bob");
    await page.keyboard.press("Enter");
    await waitForGrid(page, (c) => hasText(c, "E  Board editor") && textAt(c, 65, 24, 15).trim() === "Bob Bones",
      "the phone's title sidebar to name the account");
    assert.equal(await google.isVisible(), false, "no Google button on the title bar");
    console.log("  · the phone bar's Google button signed in");
  }

  for (const b of browsers) {
    if (b.pageErrors.length) failures.push(`page errors: ${b.pageErrors.join("; ")}`);
  }
} finally {
  for (const b of browsers) await b.browser.close();
}
if (failures.length) {
  throw new Error(failures.join("\n"));
}
console.log("launch_sign_in.test.mjs: M37.8 passed");
