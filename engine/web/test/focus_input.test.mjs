// M16.10 — the input surfaces that are not keys on the board.
//
// Driven by engine/m16_10_test.go (TestM1610BrowserFocusAndProtocol).
//
// Three claims, all of which need a browser to make:
//
//   * FOCUS NEVER LEAKS TEXT INTO MOVEMENT. A player typing in chat is pressing
//     the same physical keys that walk, shoot, save, and quit. The DoD asks for
//     this to be checked, and the only place it can honestly be checked is the
//     wire: not "the player did not appear to move" but "no input frame was
//     sent at all".
//
//   * IME COMMIT AND DELETE. mobile_text_input.test.mjs drives the bridge's
//     pure adapter under Node. The bridge's actual job is to own a hidden
//     native control and translate ITS events, and composition is the case
//     where a keydown reports keyCode 229 and is useless — so the events have
//     to be real ones on a real element, in a context that reports touch.
//
//   * A DROPPED CONNECTION RESUMES, AND STILL TAKES INPUT. M16.11 proved the
//     resume puts the player back in place. What it did not press afterwards is
//     a key: a client whose listeners did not survive the rejoin would look
//     perfectly correct and be unplayable.

import assert from "node:assert/strict";

import {
  baseURL,
  gridToArt,
  hasText,
  idle,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  launchOpensPicker,
  pauseClock,
  pressExpectingNoInput,
  readGrid,
  saveText,
  serverState,
  tickUntilGrid,
  waitForGrid,
  waitForQuiet,
  walk,
} from "./lib/canvas.mjs";

async function me() {
  const state = await serverState();
  assert.equal(state.players.length, 1, `expected exactly one player, saw ${JSON.stringify(state.players)}`);
  return state.players[0];
}

/** Join CONTROL through the production launch flow and start playing. */
async function joinAndPlay(page, name) {
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);
  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type(name);
  await page.keyboard.press("Enter");
  // M20.1: the picker only opens when the URL does not already name a world —
  // after the reload below the address bar says /play/CONTROL, and this load
  // lands on CONTROL's title screen directly.
  if (launchOpensPicker(page)) {
    await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
    await page.keyboard.type("CONTROL");
    await waitForGrid(page, (cells) => hasText(cells, "CONTROL"), "the picker to match CONTROL");
    await page.keyboard.press("Enter");
  }
  await waitForGrid(
    page,
    (cells) => hasText(cells, "P  Play") && !hasText(cells, "Type your name") && !hasText(cells, "Choose a World"),
    "the title screen for CONTROL",
  );
  await pauseClock(page);
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await idle(2);
  await waitForQuiet(page);
}

// hasTouch is what makes MobileTextInputBridge willing to mount its hidden
// control (shouldUseOverlay reads navigator.maxTouchPoints), which is the whole
// composition path.
const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser({ hasTouch: true });
let failed = false;

try {
  await installImageProbe(page);
  await context.tracing.start({ screenshots: true, snapshots: true });

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served");
  await joinAndPlay(page, "Focus");

  // Move off the spawn square, so a later resume "in place" is distinguishable
  // from a fresh spawn.
  await walk(page, "ArrowRight", 2);
  const settled = await me();
  assert.deepEqual({ x: settled.x, y: settled.y }, { x: 8, y: 12 }, "two steps east of the CONTROL spawn");

  // =========================================================================
  // 1. Chat capture isolation
  // =========================================================================
  await page.keyboard.press("KeyC");
  await waitForGrid(page, (cells) => hasText(cells, "Global Chat"), "the chat window");

  // Every character here is also a play-mode binding: s saves, q quits, t
  // lights a torch, b toggles sound, p pauses, h opens help. None of them may
  // reach the server, and none of them may open a modal over the chat.
  for (const code of ["KeyS", "KeyQ", "KeyT", "KeyB", "KeyP", "KeyH", "ArrowUp", "ArrowRight"]) {
    const state = await pressExpectingNoInput(page, code);
    assert.deepEqual(
      state.pending,
      [],
      `${code} typed into chat must send no input frame, saw ${JSON.stringify(state.pending)}`,
    );
  }
  await idle(2);
  const duringChat = await me();
  assert.deepEqual(
    { x: duringChat.x, y: duringChat.y },
    { x: 8, y: 12 },
    "typing in chat must not move the player",
  );

  const chatScreen = await readGrid(page);
  assert.ok(hasText(chatScreen, "Global Chat"), "the chat window must still be the modal on screen");
  // The letters went into the chat buffer rather than nowhere: the modal draws
  // what has been typed, so the buffer is visible on the canvas. ArrowLeft is
  // chat's erase key and ArrowUp/Right are ignored, so what should be showing
  // is exactly the six command letters.
  assert.ok(
    hasText(chatScreen, "sqtbph"),
    `the typed letters must have landed in the chat buffer:\n${gridToArt(chatScreen)}`,
  );

  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Global Chat"), "the chat window to close");

  // Closing hands the keyboard back: the same keys move again.
  await walk(page, "ArrowRight", 1);
  const afterChat = await me();
  assert.deepEqual(
    { x: afterChat.x, y: afterChat.y },
    { x: 9, y: 12 },
    "the player must be movable again once chat closes",
  );

  // =========================================================================
  // 2. IME composition: commit and delete
  // =========================================================================
  await page.keyboard.press("KeyC");
  await waitForGrid(page, (cells) => hasText(cells, "Global Chat"), "the chat window, again");

  // The bridge mounts its control on the first touch gesture (noteTouchStart),
  // because that is the only moment iOS will raise a keyboard. Tap the canvas.
  await page.locator("canvas[data-screen]").dispatchEvent("touchstart");
  const field = page.locator('body > input[aria-hidden="true"]');
  await field.waitFor({ state: "attached", timeout: 5000 });

  // A composing IME reports keydown keyCode 229 and mutates the field, so the
  // committed text arrives ONLY as composition events. Fire the real ones — and
  // fire the `input` event the browser emits right behind compositionend in the
  // SAME task, because that is when it happens and because the guard that
  // swallows it (skipCommittedText) clears itself on a microtask. Splitting
  // these across two evaluate() calls would be testing a sequence no browser
  // produces, and would report a double-commit that cannot occur.
  await field.evaluate((input) => {
    input.dispatchEvent(new CompositionEvent("compositionstart", { data: "" }));
    input.dispatchEvent(new CompositionEvent("compositionupdate", { data: "he" }));
    input.dispatchEvent(new CompositionEvent("compositionupdate", { data: "hey" }));
    input.dispatchEvent(new CompositionEvent("compositionend", { data: "hey" }));
    input.value = "hey";
    input.dispatchEvent(new InputEvent("input", { inputType: "insertText", data: "hey" }));
  });
  await waitForGrid(page, (cells) => hasText(cells, "hey"), "the composed text to reach the chat buffer");

  await idle(1);
  const afterCommit = await readGrid(page);
  assert.ok(
    hasText(afterCommit, "> hey") && !hasText(afterCommit, "heyhey"),
    `a committed composition must be delivered exactly once:\n${gridToArt(afterCommit)}`,
  );

  // Delete, the other half of the bridge's vocabulary.
  await field.evaluate((input) => {
    input.dispatchEvent(new InputEvent("input", { inputType: "deleteContentBackward", data: null }));
  });
  await waitForGrid(
    page,
    (cells) => hasText(cells, "> he") && !hasText(cells, "> hey"),
    "deleteContentBackward to erase one character",
  );

  // Nothing about any of this reached the game.
  const duringComposition = await serverState();
  assert.deepEqual(duringComposition.pending, [], "composition must not produce input frames");
  const stillPut = await me();
  assert.deepEqual({ x: stillPut.x, y: stillPut.y }, { x: 9, y: 12 }, "composition must not move the player");

  // The hidden control has the focus now — that is its whole point — so the
  // keyboard shortcut has to be aimed back at the canvas, exactly as a tap on
  // the board would do on a phone.
  await page.locator("canvas[data-screen]").focus();
  await page.keyboard.press("Escape");
  await waitForGrid(page, (cells) => !hasText(cells, "Global Chat"), "the chat window to close again");

  // =========================================================================
  // 3. A dropped connection resumes — and the resumed client still takes input
  // =========================================================================
  const beforeDrop = await me();
  await page.reload(); // a hard disconnect; the resume token lives in sessionStorage
  // The reload lands on the launch sequence again — the client always asks for
  // a name and a world (promptNicknameOnLaunch), and it is the JOIN that then
  // carries the stored token. So this is the production path a returning player
  // actually walks, not a back door.
  await joinAndPlay(page, "Focus");

  const resumed = await me();
  assert.deepEqual(
    { x: resumed.x, y: resumed.y },
    { x: beforeDrop.x, y: beforeDrop.y },
    `resume must reclaim the run in place, not spawn a fresh player at the start square`,
  );

  // The part M16.11 did not check: the rejoined page still has working input.
  await walk(page, "ArrowDown", 1);
  const afterResume = await me();
  assert.deepEqual(
    { x: afterResume.x, y: afterResume.y },
    { x: resumed.x, y: resumed.y + 1 },
    "a resumed client must still deliver keystrokes to the game",
  );
  await tickUntilGrid(page, (cells) => hasText(cells, "Health:"), "the sidebar after the resumed move");

  console.log("  ✓ chat capture isolation, IME commit/delete, and a resumed client that still takes input");
} catch (error) {
  failed = true;
  saveText("focus-input-failure.txt", String(error && error.stack ? error.stack : error));
  try {
    saveText("focus-input-screen.txt", gridToArt(await readGrid(page)));
    saveText("focus-input-server.json", JSON.stringify(await serverState(), null, 2));
  } catch {
    // The page is already gone; the stack above is what matters.
  }
  throw error;
} finally {
  await context.tracing.stop({ path: "test-results/focus-input-trace.zip" });
  await browser.close();
  if (!failed) {
    assert.deepEqual(pageErrors, [], "the page must raise no uncaught errors");
    assert.deepEqual(consoleErrors, [], "the console must carry no errors");
  }
}
