// M16.10 — sound, in a real browser, through the real client.
//
// Driven by engine/m16_10_test.go (TestM1610BrowserAudioParity).
//
// engine/web/test/sound.test.mjs already exercises ZztSound against a mock
// graph by importing it under Node. What it cannot show is that the synth is
// WIRED: that a server SoundEvent reaches queue() with the priority the engine
// gave it, that a gesture unlocked the context before the first note (M17.3's
// bug), or that the sidebar's 'B' toggle actually silences it. Here the world
// makes the sounds, the built client receives them over its own WebSocket, and
// the only thing replaced is the AudioContext constructor — so every assertion
// below is about automation the REAL ZztSound scheduled.
//
// Note bytes and their priorities, for the record:
//   #play cdefg        oop.go:749       priority -1   (the appending kind)
//   gem pickup         elements.go:1048 priority  2   first note C-4 = 512 Hz
//   energizer          elements.go:676  priority  9   56 notes, none above ~342 Hz

import assert from "node:assert/strict";

import {
  WALK_CLICK_HZ,
  audioLog,
  installAudioMock,
  melodyAdmissions,
  melodyTones,
  tones,
} from "./lib/audio.mjs";
import {
  baseURL,
  command,
  gridToArt,
  hasText,
  idle,
  installDecoder,
  installImageProbe,
  launchGoldenBrowser,
  pauseClock,
  readGrid,
  runClock,
  saveText,
  serverState,
  tickUntilGrid,
  waitForGrid,
  waitForQuiet,
  walk,
} from "./lib/canvas.mjs";

const KEY_B = "B".charCodeAt(0);

// The gem's melody opens on C-4. Nothing else this route plays reaches it, so
// its presence or absence is how a dropped melody is told from a played one.
const GEM_FIRST_HZ = 512;

// C major, one octave: c d e f g. The interval pattern is the assertion —
// it holds whatever the base octave is, and it is what a mis-parse breaks.
const SEMITONES_BETWEEN_CDEFG = [2, 2, 1, 2];
const SEMITONE = Math.pow(2, 1 / 12);

const { browser, context, page, pageErrors, consoleErrors } = await launchGoldenBrowser();
let failed = false;

try {
  await installAudioMock(page);
  await installImageProbe(page);
  await context.tracing.start({ screenshots: true, snapshots: true });

  const response = await page.goto(baseURL);
  assert.equal(response?.status(), 200, "the client index must be served");
  await page.waitForSelector("canvas[data-screen]", { timeout: 15000 });
  await installDecoder(page);

  await waitForGrid(page, (cells) => hasText(cells, "Type your name"), "the launch name prompt");
  await page.keyboard.type("Band");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "Choose a World"), "the world picker");
  await page.keyboard.type("CONTROL");
  await waitForGrid(page, (cells) => hasText(cells, "CONTROL"), "the picker to match CONTROL");
  await page.keyboard.press("Enter");
  await waitForGrid(page, (cells) => hasText(cells, "P  Play"), "the title screen for CONTROL");

  // =========================================================================
  // 1. The context is created and unlocked by a real gesture
  // =========================================================================
  // M17.3 shipped an AudioContext that never left "suspended" because nothing
  // resumed it, and M17.7 recorded that it was "not yet audibly confirmed in a
  // real browser". This is that confirmation: typing a name is a gesture, and
  // main.ts's capture-phase unlock listener must have turned it into a resume.
  const opening = await audioLog(page);
  assert.equal(opening.contexts, 1, "the client must build exactly one AudioContext");
  assert.ok(opening.resumes > 0, "a user gesture must have resumed the AudioContext before play starts");

  await pauseClock(page);
  await page.keyboard.press("KeyP");
  await waitForGrid(page, (cells) => hasText(cells, "Health:100"), "the joined board");
  await idle(2);
  await waitForQuiet(page);

  // =========================================================================
  // 2. #play parses into notes — the whole way from OOP to the oscillator
  // =========================================================================
  // Approach the band from the south along row 13, so nothing else on row 12 is
  // touched on the way: this test wants exactly one melody in flight.
  await walk(page, "ArrowDown", 1);
  await walk(page, "ArrowRight", 24);
  const before = (await audioLog(page)).events.length;
  await walk(page, "ArrowUp", 1); // touches the band; the player does not move
  await idle(2);

  // The scheduler is a setInterval on a frozen clock and only looks 50ms ahead,
  // so the melody arrives in instalments. Advancing a full second drains it.
  await runClock(page, 1200);
  const played = melodyTones(await audioLog(page), before);
  assert.equal(
    played.length,
    5,
    `#play cdefg must schedule five tones, saw ${JSON.stringify(played)}`,
  );
  for (let i = 0; i < SEMITONES_BETWEEN_CDEFG.length; i += 1) {
    const ratio = played[i + 1] / played[i];
    const want = Math.pow(SEMITONE, SEMITONES_BETWEEN_CDEFG[i]);
    assert.ok(
      Math.abs(ratio - want) / want < 0.01,
      `cdefg step ${i}: ${played[i]}Hz -> ${played[i + 1]}Hz is ${ratio.toFixed(4)}, want ` +
        `${want.toFixed(4)} (${SEMITONES_BETWEEN_CDEFG[i]} semitones)`,
    );
  }

  // The footstep poke rides the same oscillator and is the other half of what
  // a player hears while walking (M16.6b fixed it never being heard at all), so
  // the melody assertions filter it out and this one requires it.
  assert.ok(
    tones(await audioLog(page)).includes(WALK_CLICK_HZ),
    `walking must produce vanilla's ${WALK_CLICK_HZ}Hz footstep click`,
  );

  // =========================================================================
  // 3. Priority -1 APPENDS to a melody already sounding
  // =========================================================================
  // Touch the band twice without letting the first melody drain. queue()'s
  // priority < 0 branch splices the new notes onto the unplayed tail instead of
  // replacing the buffer, so the run that follows is ten tones, not five.
  const beforeAppend = (await audioLog(page)).events.length;
  await walk(page, "ArrowUp", 1);
  await idle(2);
  await runClock(page, 100); // enough to start it, not enough to finish it
  await walk(page, "ArrowUp", 1);
  await idle(2);
  await runClock(page, 2000);
  const appended = melodyTones(await audioLog(page), beforeAppend);
  assert.equal(
    appended.length,
    10,
    `a second #play at priority -1 must append rather than replace, saw ${JSON.stringify(appended)}`,
  );

  // =========================================================================
  // 4. A lower priority cannot interrupt a melody that is sounding
  // =========================================================================
  // Approach from the east so the energizer (priority 9) is touched BEFORE the
  // gem (priority 2). queue() then rejects the gem outright: no buffer swap, no
  // reset, no note. Both halves are asserted — the admission count does not
  // move, and the gem's opening C-4 never reaches the oscillator.
  await walk(page, "ArrowRight", 7);
  await walk(page, "ArrowUp", 1);
  const beforeEnergizer = (await audioLog(page)).events.length;
  await walk(page, "ArrowLeft", 1); // the energizer at 36,12
  await idle(2);
  await runClock(page, 100);
  const admittedEnergizer = melodyAdmissions(await audioLog(page), beforeEnergizer);
  assert.ok(admittedEnergizer >= 1, "the energizer melody must be admitted");

  const beforeGem = (await audioLog(page)).events.length;
  await walk(page, "ArrowLeft", 1);
  await walk(page, "ArrowLeft", 1); // the gem at 34,12
  await tickUntilGrid(page, (cells) => hasText(cells, "Gems:1"), "the gem to be collected");
  await runClock(page, 300);
  const sinceGem = await audioLog(page);
  assert.equal(
    melodyAdmissions(sinceGem, beforeGem),
    0,
    "a priority-2 melody must not displace a priority-9 one that is still sounding",
  );
  assert.ok(
    !melodyTones(sinceGem).includes(GEM_FIRST_HZ),
    `the gem's own melody must never have been voiced; ${GEM_FIRST_HZ}Hz appeared in ` +
      JSON.stringify(melodyTones(sinceGem)),
  );

  // And the energizer melody is genuinely still going — otherwise the check
  // above would pass for the boring reason that nothing was playing at all.
  assert.ok(
    melodyTones(sinceGem, beforeGem).length > 0,
    "the priority-9 melody must still be scheduling notes while the gem is refused",
  );

  // =========================================================================
  // 5. The 'B' toggle silences the synth
  // =========================================================================
  await command(page, "KeyB", KEY_B);
  await tickUntilGrid(page, (cells) => hasText(cells, "Be noisy"), "B to mute");
  await runClock(page, 3000); // let whatever was sounding run out under the mute
  const beforeMuted = (await audioLog(page)).events.length;
  // Walk back into the band. The server still sends the sound event; the client
  // must drop it on the floor.
  await walk(page, "ArrowDown", 1);
  await walk(page, "ArrowLeft", 4);
  await walk(page, "ArrowUp", 1);
  await idle(2);
  await runClock(page, 1500);
  assert.deepEqual(
    tones(await audioLog(page), beforeMuted),
    [],
    "no note may be voiced while the player has asked to be quiet",
  );

  // Unmuting restores it, so the toggle is a toggle and not a one-way door.
  await command(page, "KeyB", KEY_B);
  await tickUntilGrid(page, (cells) => hasText(cells, "Be quiet"), "B again to unmute");
  const beforeUnmuted = (await audioLog(page)).events.length;
  await walk(page, "ArrowUp", 1);
  await idle(2);
  await runClock(page, 1500);
  assert.equal(
    melodyTones(await audioLog(page), beforeUnmuted).length,
    5,
    "unmuting must let the next #play through in full",
  );

  console.log("  ✓ AudioContext unlock, #play parsing, -1 append, priority arbitration, and the B toggle");
} catch (error) {
  failed = true;
  saveText("audio-parity-failure.txt", String(error && error.stack ? error.stack : error));
  try {
    saveText("audio-parity-screen.txt", gridToArt(await readGrid(page)));
    saveText("audio-parity-log.json", JSON.stringify(await audioLog(page), null, 2));
    saveText("audio-parity-server.json", JSON.stringify(await serverState(), null, 2));
  } catch {
    // The page is already gone; the stack above is what matters.
  }
  throw error;
} finally {
  await context.tracing.stop({ path: "test-results/audio-parity-trace.zip" });
  await browser.close();
  if (!failed) {
    assert.deepEqual(pageErrors, [], "the page must raise no uncaught errors");
    assert.deepEqual(consoleErrors, [], "the console must carry no errors");
  }
}
