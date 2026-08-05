// M16.18a — the on-screen touch control surface, under Node against a fake DOM.
//
// This is the pure half of the proof. It exercises the mapping and the mode
// gating without a browser, so a mistyped code string (`"NumPad8"` typechecks
// perfectly and simply never moves the player) is named here, at the line that
// declares it, instead of surfacing as a mysterious timeout in the real-browser
// matrix. The other half — that these buttons actually move, shoot, light a
// torch and pause a real server through a real touchscreen — is
// engine/web/test/platform_matrix.test.mjs, driven by TestM1618PlatformMatrix.
//
// Two properties matter most and are asserted from both directions:
//
//   * EVERY CONTROL IS A KEY THE KEYBOARD ALREADY HAS. Fire is the space bar,
//     Torch is T, Pause is P. Nothing here invents an input the server would
//     have to learn (keys.ts owns the vocabulary; the server decodes the same
//     keymask either way), so touch cannot drift from the keyboard path.
//   * A CONTROL IS ONLY ON SCREEN WHERE IT MEANS SOMETHING. Fire is a space,
//     and behind an open text surface a space belongs in the buffer — so the
//     gameplay controls must be absent in `modal` mode, and the title menu's
//     World/Play must be absent while a room is being played.

import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/touch_controls.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { createTouchControls, TOUCH_BUTTONS } = await import(`data:text/javascript;base64,${source}`);

// ---------------------------------------------------------------------------
// A DOM small enough to read
// ---------------------------------------------------------------------------

class FakeElement {
  constructor(tag) {
    this.tag = tag;
    this.className = "";
    this.textContent = "";
    this.hidden = false;
    this.tabIndex = 0;
    this.children = [];
    this.attributes = {};
    this.listeners = new Map();
  }

  setAttribute(name, value) { this.attributes[name] = value; }
  appendChild(child) { this.children.push(child); return child; }
  addEventListener(type, listener) { this.listeners.set(type, listener); }
  dispatch(type, detail = {}) {
    if (typeof detail.preventDefault !== "function") {
      detail.preventDefault = () => { detail.defaultPrevented = true; };
    }
    this.listeners.get(type)?.(detail);
    return detail;
  }
}

const body = { children: [], appendChild(element) { this.children.push(element); } };
const host = { body, createElement: (tag) => new FakeElement(tag) };

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// Nothing is built on a device with no touch points: desktop is untouched.
assert.equal(createTouchControls(host, { key() {}, toggleKeyboard() {} }, 0), null);
assert.deepEqual(body.children, [], "a skipped bar must not be appended to the page");

const keyCalls = [];
let kbToggles = 0;
const controls = createTouchControls(host, {
  key: (down, code, key) => keyCalls.push({ down, code, key }),
  toggleKeyboard: () => { kbToggles += 1; },
}, 1);
assert.notEqual(controls, null);

const bar = controls.element;
assert.equal(body.children.length, 1, "the bar is appended once");
const buttons = bar.children.flatMap((group) => group.children);
const byLabel = (label) => {
  const found = buttons.find((b) => b.textContent === label);
  assert.ok(found, `no touch control is labelled ${JSON.stringify(label)}`);
  return found;
};
assert.equal(buttons.length, TOUCH_BUTTONS.length);
// A control must never take focus from the hidden text input: that is what keeps
// the soft keyboard up when a control is tapped (M15.1's seam).
for (const button of buttons) {
  assert.equal(button.tabIndex, -1, `${button.textContent} is focusable`);
}

// ---------------------------------------------------------------------------
// The mapping — every control is a key the keyboard already sends
// ---------------------------------------------------------------------------

// Each control carries a stable data-touch id: the browser matrix names its
// buttons by that rather than by a glyph, and the ids must therefore be unique
// and complete.
const ids = TOUCH_BUTTONS.map((spec) => spec.id);
assert.equal(new Set(ids).size, ids.length, `duplicate data-touch id in ${JSON.stringify(ids)}`);
for (const button of buttons) {
  assert.ok(button.attributes["data-touch"], `${button.textContent} carries no data-touch id`);
}
assert.deepEqual(
  buttons.map((b) => b.attributes["data-touch"]),
  ids,
  "the DOM order must be the declared order (style.css lays the pad out by :nth-child)",
);

const pressed = (label) => {
  keyCalls.length = 0;
  byLabel(label).dispatch("pointerdown");
  return keyCalls.slice();
};

// A tapped menu key sends down then up in one press: one discrete keystroke.
assert.deepEqual(pressed("World"), [
  { down: true, code: "KeyW", key: "w" },
  { down: false, code: "KeyW", key: "w" },
]);
assert.deepEqual(pressed("Play"), [
  { down: true, code: "KeyP", key: "p" },
  { down: false, code: "KeyP", key: "p" },
]);
// M19.2: the color picker's 'C', which is a title-menu key exactly like World.
assert.deepEqual(pressed("Color"), [
  { down: true, code: "KeyC", key: "c" },
  { down: false, code: "KeyC", key: "c" },
]);
assert.deepEqual(pressed("⏎"), [
  { down: true, code: "Enter", key: "Enter" },
  { down: false, code: "Enter", key: "Enter" },
]);

// The three M16.18a gameplay controls. Torch and Pause are command bytes — one
// tap, one byte, exactly as ElementPlayerTick's `switch UpCase(InputKeyPressed)`
// expects (elements.go:1498-1530).
assert.deepEqual(pressed("Torch"), [
  { down: true, code: "KeyT", key: "t" },
  { down: false, code: "KeyT", key: "t" },
]);
// Pause is the SAME key byte as Play; the mode gate below is what makes them
// two controls instead of one confusing one.
assert.deepEqual(pressed("Pause"), [
  { down: true, code: "KeyP", key: "p" },
  { down: false, code: "KeyP", key: "p" },
]);

// M21.5's two windows. Both are letters main.ts handles in its play-mode branch
// before the key reaches the engine, so they are taps like Torch rather than
// anything new on the wire.
assert.deepEqual(pressed("Chat"), [
  { down: true, code: "KeyC", key: "c" },
  { down: false, code: "KeyC", key: "c" },
]);
assert.deepEqual(pressed("Players"), [
  { down: true, code: "KeyL", key: "l" },
  { down: false, code: "KeyL", key: "l" },
]);

// The way out of a window, and the two answers a prompt takes.
assert.deepEqual(pressed("Esc"), [
  { down: true, code: "Escape", key: "Escape" },
  { down: false, code: "Escape", key: "Escape" },
]);
assert.deepEqual(pressed("Yes"), [
  { down: true, code: "KeyY", key: "y" },
  { down: false, code: "KeyY", key: "y" },
]);
assert.deepEqual(pressed("No"), [
  { down: true, code: "KeyN", key: "n" },
  { down: false, code: "KeyN", key: "n" },
]);

// Fire is the space bar and it is HELD: the shoot bit has to still be set when
// the tick that consumes it runs, and holding is also how Space repeats.
assert.deepEqual(pressed("Fire"), [{ down: true, code: "Space", key: " " }]);
byLabel("Fire").dispatch("pointerup");
assert.deepEqual(keyCalls.at(-1), { down: false, code: "Space", key: " " });

// A held direction presses on pointerdown and releases on pointerup, so it keeps
// moving while held in gameplay (main.ts's 55ms sampler re-sends the mask).
for (const [label, code] of [["▲", "ArrowUp"], ["◄", "ArrowLeft"], ["►", "ArrowRight"], ["▼", "ArrowDown"]]) {
  assert.deepEqual(pressed(label), [{ down: true, code, key: code }], `${label} must press ${code}`);
  byLabel(label).dispatch("pointerup");
  assert.deepEqual(keyCalls.at(-1), { down: false, code, key: code }, `${label} must release ${code}`);
}

// A finger that slides off a held control releases it rather than leaving the
// player walking into a wall forever.
for (const type of ["pointercancel", "pointerleave"]) {
  keyCalls.length = 0;
  byLabel("►").dispatch("pointerdown");
  byLabel("►").dispatch(type);
  assert.deepEqual(keyCalls.at(-1), { down: false, code: "ArrowRight", key: "ArrowRight" }, `${type} must release`);
}

// The ⌨ button toggles the soft keyboard instead of sending a key.
keyCalls.length = 0;
byLabel("⌨").dispatch("pointerdown");
assert.equal(kbToggles, 1);
assert.deepEqual(keyCalls, [], "the keyboard toggle must send no key");

// pointerdown preventDefaults so a control never steals focus from the input.
assert.equal(byLabel("World").dispatch("pointerdown").defaultPrevented, true);
assert.equal(byLabel("Fire").dispatch("pointerdown").defaultPrevented, true);
byLabel("Fire").dispatch("pointerup");

// No control may reach the simulation with a vocabulary the keyboard cannot
// produce: every code here is one keys.ts already handles or maps to a command.
const KEYBOARD_CODES = new Set([
  "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", // keys.ts MOVEMENT_CODES
  "Space", "Enter",                                  // keys.ts isHandledKey
  "KeyT", "KeyP", "KeyW", "KeyC",                    // keys.ts COMMAND_CODES / title menu
  "KeyL",                                            // main.ts play-mode branch (M21.1)
  "Escape", "KeyY", "KeyN",                          // modal.ts textWindowKey / yesNoKey
]);
for (const spec of TOUCH_BUTTONS) {
  if (spec.kind !== "key") continue;
  assert.ok(KEYBOARD_CODES.has(spec.code), `${spec.label} sends ${spec.code}, which no keyboard path produces`);
}

// ---------------------------------------------------------------------------
// The mode gate
// ---------------------------------------------------------------------------

const visible = () => buttons.filter((b) => !b.hidden).map((b) => b.textContent);
const DPAD = ["▲", "◄", "►", "▼"];

// The client opens on the title screen, so the bar starts there rather than
// showing every control for one frame.
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Color", "World", "Play"], "the bar starts in title mode");

controls.setMode("playing");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Chat", "Players", "Pause", "Torch", "Fire"]);

// The whole point of the gate: behind an open window there is no Fire to tap,
// so a space cannot land in the game instead of the text buffer — and no Torch
// or Pause either, which would be a command byte sent from behind a modal. Chat
// and Players are gated for the same reason from M21.5 on: their letters would
// land in the buffer of the very window they opened.
controls.setMode("modal");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Esc"]);

// A yes/no prompt answers to Y, N and Escape only (yesNoKey), so it is the one
// window that offers its answers — and it must not offer them anywhere else,
// where 'y' and 'n' are two more letters that would land in a text buffer.
controls.setMode("prompt");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Esc", "Yes", "No"]);

// The editor has its own key vocabulary; the pad and Enter navigate it, and the
// play/title commands mean nothing there.
controls.setMode("editor");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎"]);

controls.setMode("title");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Color", "World", "Play"], "modes are reversible");

// The direction pad is laid out as a cross by :nth-child (style.css), so it must
// be present in EVERY mode — hiding one would silently re-letter the others.
for (const mode of ["title", "playing", "editor", "modal", "prompt"]) {
  controls.setMode(mode);
  for (const label of DPAD) {
    assert.equal(byLabel(label).hidden, false, `the ${label} pad key must exist in ${mode} mode`);
  }
}

console.log("touch_controls.test.mjs: all assertions passed");
