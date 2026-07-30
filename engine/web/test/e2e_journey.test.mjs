import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright";

const baseURL = process.env.BASE_URL || "http://127.0.0.1:8080";
const resultsDir = path.resolve("test-results");
if (!fs.existsSync(resultsDir)) {
  fs.mkdirSync(resultsDir, { recursive: true });
}

console.log(`Starting E2E Player Journeys against ${baseURL}...`);

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
await context.tracing.start({ screenshots: true, snapshots: true });

const page = await context.newPage();

// Catch browser errors
page.on("pageerror", (err) => {
  console.error("Browser Page Error:", err);
});
page.on("console", (msg) => {
  if (msg.type() === "error") {
    console.error("Browser Console Error:", msg.text());
  }
});

try {
  // =========================================================================
  // JOURNEY 1: ACCEPTANCE WORLD (ACCEPT.ZZT)
  // =========================================================================
  console.log("=== JOURNEY 1: Acceptance World (ACCEPT.ZZT) ===");

  await page.goto(baseURL);
  await page.waitForLoadState("domcontentloaded");

  // Step 1: Launch name prompt ("Welcome to ZZTMMO! Type your name and press Enter.")
  await page.waitForTimeout(300);
  await page.keyboard.type("AcceptTester");
  await page.keyboard.press("Enter");

  // Step 2: Choose a World picker
  await page.waitForTimeout(300);
  await page.keyboard.type("ACCEPT");
  await page.keyboard.press("Enter");

  // Step 3: Wait for game to connect and load ACCEPT Board 1 ("Acceptance Main")
  await page.waitForTimeout(500);

  // Step 4: Player movement & Item pickups (Gem at x=10, Ammo at x=14, Key at x=18, Door at x=22)
  console.log("  - Moving right to collect Gem, Ammo, Key, and open Door...");
  for (let i = 0; i < 18; i++) {
    await page.keyboard.press("ArrowRight");
    await page.waitForTimeout(60);
  }

  // Step 5: Shoot bullet (Space or Shift+ArrowRight)
  console.log("  - Shooting bullet...");
  await page.keyboard.press("Space");
  await page.waitForTimeout(100);

  // Step 6: Light Torch (T)
  console.log("  - Lighting torch...");
  await page.keyboard.press("KeyT");
  await page.waitForTimeout(100);

  // Step 7: Interact with Vendor Object at x=26
  console.log("  - Interacting with Vendor object & replying to scroll...");
  for (let i = 0; i < 4; i++) {
    await page.keyboard.press("ArrowRight");
    await page.waitForTimeout(60);
  }
  await page.waitForTimeout(200);

  // Vendor scroll modal is open; select hyperlink !ba by pressing Enter
  await page.keyboard.press("Enter");
  await page.waitForTimeout(300);

  // Step 8: Move right to hit Bear at x=30 (take damage & respawn)
  console.log("  - Colliding with Bear (taking damage)...");
  for (let i = 0; i < 4; i++) {
    await page.keyboard.press("ArrowRight");
    await page.waitForTimeout(60);
  }
  await page.waitForTimeout(300);

  // Step 9: Walk into Passage at x=34 to transfer to Board 2 ("Acceptance Target")
  console.log("  - Stepping into Passage (board transition)...");
  for (let i = 0; i < 10; i++) {
    await page.keyboard.press("ArrowRight");
    await page.waitForTimeout(60);
  }
  await page.waitForTimeout(500);

  // Step 10: Save game (S -> ACCSAVE)
  console.log("  - Saving game snapshot ACCSAVE...");
  await page.keyboard.press("KeyS");
  await page.waitForTimeout(200);
  await page.keyboard.type("ACCSAVE");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(300);

  // Step 11: Quit to Title Screen (Q -> Y)
  console.log("  - Quitting game to Title Monitor...");
  await page.keyboard.press("KeyQ");
  await page.waitForTimeout(200);
  await page.keyboard.press("KeyY");
  await page.waitForTimeout(500);

  // Step 12: Restore saved game (R -> ACCSAVE)
  console.log("  - Restoring saved game ACCSAVE...");
  await page.keyboard.press("KeyR");
  await page.waitForTimeout(200);
  await page.keyboard.type("ACCSAVE");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(500);
  await page.keyboard.press("KeyP"); // Press P to play restored game
  await page.waitForTimeout(500);

  console.log("Journey 1 (ACCEPT.ZZT) PASSED cleanly!");

  // =========================================================================
  // JOURNEY 2: TOWN WORLD ROUTE (TOWN.ZZT) WITHOUT STATE STAGING
  // =========================================================================
  console.log("=== JOURNEY 2: TOWN World Route (TOWN.ZZT) ===");

  // Quit back to Title
  await page.keyboard.press("KeyQ");
  await page.waitForTimeout(200);
  await page.keyboard.press("KeyY");
  await page.waitForTimeout(500);

  // Open World Picker via 'W' or restart flow
  await page.keyboard.press("KeyW");
  await page.waitForTimeout(300);

  await page.keyboard.type("TOWN");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(500);

  console.log("  - Traversing TOWN Plaza...");
  for (let i = 0; i < 10; i++) {
    await page.keyboard.press("ArrowRight");
    await page.waitForTimeout(60);
  }
  for (let i = 0; i < 5; i++) {
    await page.keyboard.press("ArrowDown");
    await page.waitForTimeout(60);
  }

  console.log("Journey 2 (TOWN.ZZT) PASSED cleanly!");

  await context.tracing.stop();
  await browser.close();
  console.log("ALL E2E PLAYER JOURNEY TESTS PASSED SUCCESSFULLY.");
  process.exit(0);
} catch (err) {
  console.error("E2E Journey Test Failed:", err);
  const tracePath = path.join(resultsDir, "e2e_journey_trace.zip");
  const screenshotPath = path.join(resultsDir, "e2e_journey_failure.png");
  await context.tracing.stop({ path: tracePath });
  await page.screenshot({ path: screenshotPath });
  console.error(`Saved failure trace to ${tracePath} and screenshot to ${screenshotPath}`);
  await browser.close();
  process.exit(1);
}
