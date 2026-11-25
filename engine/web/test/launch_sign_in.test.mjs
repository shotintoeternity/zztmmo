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
import fs from "node:fs";
import {
  baseURL,
  controlURL,
  hasText,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  markProfileWarm,
  pickListRow,
  readGrid,
  textAt,
  waitForGrid,
} from "./lib/canvas.mjs";

const WORLD = "COLLAB";
const KEY_HINT = "Optional: press Tab to sign in with Google.";
const TAP_HINT = "Optional: tap Google below to sign in.";
// M37.10: the server here requires a username, as zzt-server does with sign-in.
const USERNAME_BOX = "Choose a username";
const USERNAME_RULE = "3 to 16 letters, digits or _. Everyone sees it.";
const sidebarName = (c) => textAt(c, 65, 24, 15).trim();

async function clearField(page, typed) {
  for (let i = 0; i < typed.length; i += 1) await page.keyboard.press("Backspace");
}

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
    // M37.10: back from Google, the same box asks for a username, and no
    // longer offers sign-in.
    let cells = await waitForGrid(page, (c) => hasText(c, USERNAME_BOX) && hasText(c, USERNAME_RULE), "the username box after signing in");
    assert.ok(!hasText(cells, KEY_HINT), "a signed-in visitor is not offered sign-in");
    // The name on the Google account is not a username: a space is refused.
    await page.keyboard.type("Ada Lovelace");
    await page.keyboard.press("Enter");
    await waitForGrid(page, (c) => hasText(c, USERNAME_BOX) && hasText(c, "Use 3 to 16 letters, digits or _."), "the rule after a malformed username");
    await clearField(page, "Ada Lovelace");
    await page.keyboard.type("ada");
    await page.keyboard.press("Enter");
    cells = await waitForGrid(page, (c) => hasText(c, "E  Board editor") && sidebarName(c) === "ada",
      "the title sidebar to name the username");
    assert.equal(textAt(cells, 62, 24, 3), " G ", "the title keeps its sign-in row");
    assert.ok(!hasText(cells, "Lovelace"), "the Google name is not on the title screen");
    console.log("  · Tab in the name box signed in through the identity provider and claimed a username");

    // A later visit already has a username: the box asks nothing.
    await load(page);
    cells = await waitForGrid(page, (c) => hasText(c, "E  Board editor") && sidebarName(c) === "ada" && !hasText(c, "Type your name"),
      "the title screen on a later visit, with no box");
    console.log("  · a later visit went straight to the title screen as ada");

    // M37.3: Account, then Export my data, downloads what the server keeps.
    await page.keyboard.press("KeyG");
    await waitForGrid(page, (c) => hasText(c, "Export my data"), "the signed-in Account menu");
    const download = page.waitForEvent("download", { timeout: 20000 });
    await pickListRow(page, (line) => line.includes("Export my data"), "the account menu");
    const file = await download;
    assert.equal(file.suggestedFilename(), "zztmmo-ada-data.json", "the download is named for the username");
    const exported = JSON.parse(fs.readFileSync(await file.path(), "utf8"));
    assert.equal(exported.username, "ada");
    assert.equal(exported.email, "ada@example.test");
    assert.equal(exported.googleName, "Ada Lovelace");
    assert.ok(page.url().endsWith(`/play/${WORLD}`), "the page stays where it was");
    console.log("  · Export my data downloaded the account's data");
  }

  // A guest just types a name: the offer changes nothing about that path.
  {
    const guest = await open();
    browsers.push(guest);
    const { page } = guest;
    await waitForGrid(page, (c) => hasText(c, KEY_HINT), "the guest's name box with the hint");
    // M37.11: Ada's username is hers, in any capitalisation.
    await page.keyboard.type("Ada");
    await page.keyboard.press("Enter");
    await waitForGrid(page, (c) => hasText(c, "Type your name") && hasText(c, "That name belongs to a signed-in player. Try another."),
      "the guest told ada is taken");
    await clearField(page, "Ada");
    await page.keyboard.type("Gus");
    await page.keyboard.press("Enter");
    const cells = await waitForGrid(page, (c) => hasText(c, "E  Board editor"), "the guest's title screen");
    assert.equal(textAt(cells, 65, 24, 15).trim(), "Google sign-in", "a guest stays a guest");
    console.log("  · a guest was refused Ada's username, then typed a name and played on");
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
    await waitForGrid(page, (c) => hasText(c, USERNAME_BOX), "the phone's username box after signing in");
    assert.ok(!hasText(await readGrid(page), TAP_HINT), "a signed-in phone is not offered sign-in");
    assert.equal(await google.isVisible(), false, "the Google button goes with the offer");
    // Ada has "ada", in any capitalisation.
    await page.keyboard.type("ADA");
    await page.keyboard.press("Enter");
    await waitForGrid(page, (c) => hasText(c, USERNAME_BOX) && hasText(c, "That username is taken. Try another."), "the phone told ada is taken");
    await clearField(page, "ADA");
    await page.keyboard.type("bob");
    await page.keyboard.press("Enter");
    await waitForGrid(page, (c) => hasText(c, "E  Board editor") && sidebarName(c) === "bob",
      "the phone's title sidebar to name the username");
    assert.equal(await google.isVisible(), false, "no Google button on the title bar");
    console.log("  · the phone bar's Google button signed in, and a taken username was refused");
  }

  // M37.3a: Ada deletes her account. A wrong username deletes nothing; hers
  // puts the page back at a guest's first screen, and "ada" is free again.
  {
    const { page } = browsers[0];
    await load(page);
    await waitForGrid(page, (c) => hasText(c, "E  Board editor") && sidebarName(c) === "ada", "Ada's title screen before deleting");
    const deleteFromMenu = async (typed) => {
      await page.keyboard.press("KeyG");
      await waitForGrid(page, (c) => hasText(c, "Delete my account"), "the Account menu");
      await pickListRow(page, (line) => line.includes("Delete my account"), "the account menu");
      await waitForGrid(page, (c) => hasText(c, "Type your username to delete your account:"), "the delete confirmation");
      await page.keyboard.type(typed);
      await page.keyboard.press("Enter");
    };
    await deleteFromMenu("bob");
    await waitForGrid(page, (c) => hasText(c, "That is not your username.") && hasText(c, "Nothing was deleted."), "the refused confirmation");
    await page.keyboard.press("Escape");
    await page.evaluate(() => { window.__m373aBeforeDelete = true; });
    await deleteFromMenu("ada");
    await page.waitForFunction(() => !window.__m373aBeforeDelete, null, { timeout: 20000 });
    await page.waitForSelector("canvas[data-screen]", { timeout: 20000 });
    await installDecoder(page);
    await waitForGrid(page, (c) => hasText(c, "Type your name") && hasText(c, KEY_HINT), "a guest's name box after deleting");
    await page.keyboard.type("ada");
    await page.keyboard.press("Enter");
    const cells = await waitForGrid(page, (c) => hasText(c, "E  Board editor") && !hasText(c, "Type your name"), "a guest named ada on the title screen");
    assert.equal(sidebarName(cells), "Google sign-in", "the page is a guest's again");
    console.log("  · Ada deleted her account from the Account menu, and her username was free again");
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
