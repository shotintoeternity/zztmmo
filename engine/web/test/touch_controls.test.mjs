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
const { createTouchControls, TOUCH_BUTTONS, TOUCH_MENU_ROWS } = await import(`data:text/javascript;base64,${source}`);

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
  removeChild(child) { this.children = this.children.filter((c) => c !== child); return child; }
  get lastChild() { return this.children[this.children.length - 1] ?? null; }
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
const commandCalls = [];
let kbToggles = 0;
const controls = createTouchControls(host, {
  key: (down, code, key) => keyCalls.push({ down, code, key }),
  toggleKeyboard: () => { kbToggles += 1; },
  command: (action) => commandCalls.push(action),
}, 1);
assert.notEqual(controls, null);

const bar = controls.element;
// M39.1: the bar and the ☰ menu's panel are two siblings. The panel must not be
// inside the bar: the bar's measured height is the board's reservation, and an
// open menu would otherwise push the board up.
assert.equal(body.children.length, 2, "the bar and the menu are appended once each");
assert.equal(body.children[0], bar);
assert.equal(body.children[1], controls.menu);
assert.ok(!bar.children.includes(controls.menu), "the menu is not inside the bar");
const buttons = bar.children.flatMap((group) => group.children);
const byLabel = (label) => {
  const found = buttons.find((b) => b.textContent === label);
  assert.ok(found, `no touch control is labelled ${JSON.stringify(label)}`);
  return found;
};
assert.equal(buttons.length, TOUCH_BUTTONS.length);
// The fixed rows, then (M39.2) the container the open F1-F3 picker fills.
assert.equal(controls.menu.children.length, TOUCH_MENU_ROWS.length + 1);
const menuRows = controls.menu.children.slice(0, TOUCH_MENU_ROWS.length);
const pickerList = controls.menu.children[TOUCH_MENU_ROWS.length];
assert.equal(pickerList.className, "touch-menu-picker");
assert.equal(pickerList.hidden, true, "no picker is open at load");
// A control must never take focus from the hidden text input: that is what keeps
// the soft keyboard up when a control is tapped (M15.1's seam).
for (const button of [...buttons, ...menuRows]) {
  assert.equal(button.tabIndex, -1, `${button.textContent} is focusable`);
}

// ---------------------------------------------------------------------------
// The mapping — every control is a key the keyboard already sends
// ---------------------------------------------------------------------------

// Each control carries a stable data-touch id: the browser matrix names its
// buttons by that rather than by a glyph, and the ids must therefore be unique
// and complete.
const ids = TOUCH_BUTTONS.map((spec) => spec.id);
const rowIds = TOUCH_MENU_ROWS.map((spec) => spec.id);
assert.equal(new Set([...ids, ...rowIds]).size, ids.length + rowIds.length, "a menu row reuses a bar control's data-touch id");
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
  "KeyE",                                            // title.ts ' E ' Board editor (M39.3)
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
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Color", "World", "Play", "Editor"], "the bar starts in title mode");

controls.setMode("playing");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Chat", "Players", "Pause", "Torch", "Fire", "☰"]);

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

// The editor has its own key vocabulary; the pad and Enter navigate it, the
// play/title commands mean nothing there, and (M39.2) ☰ holds the rest.
controls.setMode("editor");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "☰"]);

// M22.1: a watcher's bar. It is the one mode with no direction pad and no
// Enter: the client's input sampler is off and the server drops anything a
// watcher sends, so every one of those would be a button that does nothing.
// What is left is the way out, which a room a phone can enter and not leave
// would otherwise lack.
controls.setMode("watching");
assert.deepEqual(visible(), ["Leave"]);

controls.setMode("title");
assert.deepEqual(visible(), [...DPAD, "⌨", "⏎", "Color", "World", "Play", "Editor"], "modes are reversible");

// The direction pad is laid out as a cross by :nth-child (style.css), so it must
// be present in every mode that HAS one — hiding a single arrow would silently
// re-letter the others. "watching" is deliberately absent from this list: it
// hides all four together, which is a bar without a pad rather than a broken
// one.
for (const mode of ["title", "playing", "editor", "modal", "prompt"]) {
  controls.setMode(mode);
  for (const label of DPAD) {
    assert.equal(byLabel(label).hidden, false, `the ${label} pad key must exist in ${mode} mode`);
  }
}

// ---------------------------------------------------------------------------
// M39.1 — the ☰ menu
// ---------------------------------------------------------------------------

const more = byLabel("☰");
const menu = controls.menu;
const row = (id) => {
  const found = menuRows.find((b) => b.attributes["data-touch"] === id);
  assert.ok(found, `no menu row ${id}`);
  return found;
};
const visibleRows = () => menuRows.filter((b) => !b.hidden).map((b) => b.textContent);
const openMenu = () => {
  if (menu.hidden) more.dispatch("pointerdown");
  assert.equal(menu.hidden, false, "☰ must open the menu");
};

controls.setMode("playing");
assert.equal(menu.hidden, true, "the menu starts closed");

// ☰ sends no key: it only opens and closes the panel.
keyCalls.length = 0;
assert.equal(more.dispatch("pointerdown").defaultPrevented, true);
assert.equal(menu.hidden, false);
assert.equal(more.attributes["data-open"], "true");
more.dispatch("pointerdown");
assert.equal(menu.hidden, true, "a second ☰ closes it");
assert.equal(more.attributes["data-open"], "false");
assert.deepEqual(keyCalls, [], "☰ must send no key");

// On the text screen there is no 3D view to stand up in or ghost out of, so
// those rows are not offered; the toggle is, and says where it goes.
controls.setMode("playing", { view3d: false, firstPerson: false });
assert.deepEqual(visibleRows(), ["Save", "Help", "Quit", "3D view"]);
controls.setMode("playing", { view3d: true, firstPerson: false });
assert.deepEqual(visibleRows(), ["Save", "Help", "Quit", "Text view", "Eye level", "Ghost"]);
controls.setMode("playing", { view3d: true, firstPerson: true });
assert.deepEqual(visibleRows(), ["Save", "Help", "Quit", "Text view", "Step back", "Ghost"]);

// Save, Help and Quit are COMMANDS, never letters: in the 3D view the letter S
// walks the player backwards (eye level) or tilts the camera (orbit), so a
// Save that pressed KeyS would move instead of saving.
for (const [id, action] of [["save", "save"], ["help", "help"], ["quit", "quit"]]) {
  openMenu();
  keyCalls.length = 0;
  commandCalls.length = 0;
  assert.equal(row(id).dispatch("pointerdown").defaultPrevented, true);
  assert.deepEqual(commandCalls, [action], `${id} must send the ${action} command`);
  assert.deepEqual(keyCalls, [], `${id} must not press a letter`);
  assert.equal(menu.hidden, true, `${id} must close the menu`);
}

// The 3D rows are the keys main.ts already handles before any command lookup.
for (const [id, code, key] of [["view3d", "Digit3", "3"], ["standup", "KeyF", "f"], ["ghost", "KeyG", "g"]]) {
  openMenu();
  keyCalls.length = 0;
  commandCalls.length = 0;
  row(id).dispatch("pointerdown");
  assert.deepEqual(keyCalls, [{ down: true, code, key }, { down: false, code, key }], `${id} must tap ${code}`);
  assert.deepEqual(commandCalls, []);
  assert.equal(menu.hidden, true, `${id} must close the menu`);
}

// Any other control closes it: the player has moved on.
openMenu();
byLabel("Torch").dispatch("pointerdown");
assert.equal(menu.hidden, true, "tapping a bar control closes the menu");
openMenu();
byLabel("▲").dispatch("pointerdown");
byLabel("▲").dispatch("pointerup");
assert.equal(menu.hidden, true, "the pad closes the menu");

// A mode change closes it, so a menu opened in play cannot float over the
// window Save or Quit opens — and nothing in it is offered outside play.
openMenu();
controls.setMode("prompt");
assert.equal(menu.hidden, true, "a mode change closes the menu");
assert.equal(more.hidden, true, "☰ is play mode's and the editor's, not a prompt's");
for (const m of ["title", "modal", "prompt", "watching"]) {
  controls.setMode(m, { view3d: true, firstPerson: true });
  assert.deepEqual(visibleRows(), [], `no menu row is offered in ${m} mode`);
}

// Re-asserting the same mode (drawScreen does it every frame) must NOT close it.
controls.setMode("playing");
openMenu();
controls.setMode("playing");
assert.equal(menu.hidden, false, "a redraw in the same mode leaves the menu open");

// ---------------------------------------------------------------------------
// M39.2 — the editor's ☰ menu
// ---------------------------------------------------------------------------

const editorView = (editor) => ({ view3d: false, firstPerson: false, editor: { drawing: false, picker: null, busy: false, ...editor } });
const EDITOR_ROWS = [
  "Plot", "Draw on", "Pattern", "Color", "Fill", "Item", "Creature", "Terrain",
  "Switch boards", "Board info", "World", "Clear board", "New world", "Help", "Quit",
];

controls.setMode("title");
controls.setMode("editor", editorView({}));
assert.equal(more.hidden, false, "☰ is on the editor's bar");
assert.equal(menu.hidden, true, "a mode change into the editor leaves the menu closed");
assert.equal(menu.className, "touch-menu touch-menu-wide", "the editor's rows lay out in two columns");
assert.deepEqual(visibleRows(), EDITOR_ROWS, "the editor's commands, none of play mode's");
// Draw mode says what a tap does now, like the 3D toggle.
controls.setMode("editor", editorView({ drawing: true }));
assert.deepEqual(visibleRows(), EDITOR_ROWS.map((label) => (label === "Draw on" ? "Draw off" : label)));

// The editor runs on the client (handleEditorKey switches on event.code), so
// every row taps the same key a keyboard would. None is a command byte.
for (const [id, code, key] of [
  ["edit-plot", "Space", " "], ["edit-draw", "Tab", "Tab"], ["edit-pattern", "KeyP", "p"],
  ["edit-color", "KeyC", "c"], ["edit-fill", "KeyX", "x"], ["edit-item", "F1", "F1"],
  ["edit-creature", "F2", "F2"], ["edit-terrain", "F3", "F3"], ["edit-boards", "KeyB", "b"],
  ["edit-info", "KeyI", "i"], ["edit-world", "KeyS", "s"], ["edit-clear", "KeyZ", "z"],
  ["edit-new", "KeyN", "n"], ["edit-help", "KeyH", "h"], ["edit-quit", "KeyQ", "q"],
]) {
  controls.setMode("editor", editorView({}));
  openMenu();
  keyCalls.length = 0;
  commandCalls.length = 0;
  assert.equal(row(id).dispatch("pointerdown").defaultPrevented, true);
  assert.deepEqual(keyCalls, [{ down: true, code, key }, { down: false, code, key }], `${id} must tap ${code}`);
  assert.deepEqual(commandCalls, [], `${id} must not send a command byte`);
  assert.equal(menu.hidden, true, `${id} must close the menu`);
}

// F4 text entry and L have no row: a phone cannot type outside a window, and
// the browser folded Load into the World menu. Enter is on the bar already.
for (const code of ["F4", "KeyL", "Enter"]) {
  assert.ok(!TOUCH_MENU_ROWS.some((spec) => spec.kind === "key" && spec.modes.includes("editor") && spec.code === code), `${code} must not be an editor row`);
}

// A sidebar picker other than F1-F3, the stat prompt, or text entry reads
// letters as its own: a Pattern tap would jump a board list to a row starting
// with P. So only Back is offered, and it is Escape.
controls.setMode("editor", editorView({ busy: true }));
assert.deepEqual(visibleRows(), ["Back"]);
openMenu();
keyCalls.length = 0;
row("edit-back").dispatch("pointerdown");
assert.deepEqual(keyCalls, [{ down: true, code: "Escape", key: "Escape" }, { down: false, code: "Escape", key: "Escape" }]);
controls.setMode("editor", editorView({}));
assert.equal(visibleRows().includes("Back"), false, "Back is only offered while something is up");

// The F1-F3 picker answers to a letter, which a phone has no way to type. While
// it is open the menu opens by itself and lists its elements by name.
const picker = [{ shortcut: "Z", name: "Player" }, { shortcut: "A", name: "Ammo" }];
controls.setMode("editor", editorView({}));
assert.equal(menu.hidden, true);
controls.setMode("editor", editorView({ picker }));
assert.equal(menu.hidden, false, "the picker opens the menu");
assert.equal(pickerList.hidden, false);
assert.deepEqual(pickerList.children.map((b) => b.textContent), ["Player", "Ammo"]);
assert.deepEqual(visibleRows(), ["Back"], "the editor's commands step aside for the picker");
for (const b of pickerList.children) {
  assert.equal(b.tabIndex, -1, `${b.textContent} is focusable`);
}
// A redraw with the same picker rebuilds nothing.
const first = pickerList.children[0];
controls.setMode("editor", editorView({ picker }));
assert.equal(pickerList.children[0], first, "the same picker keeps its rows");
// A row taps the element's shortcut, the key handleEditorCategoryKey reads.
keyCalls.length = 0;
assert.equal(pickerList.children[1].attributes["data-touch"], "pick-a");
pickerList.children[1].dispatch("pointerdown");
assert.deepEqual(keyCalls, [{ down: true, code: "KeyA", key: "a" }, { down: false, code: "KeyA", key: "a" }]);
assert.equal(menu.hidden, true, "a pick closes the menu");
// main.ts then clears the picker, which empties the list.
controls.setMode("editor", editorView({}));
assert.equal(pickerList.hidden, true);
assert.deepEqual(pickerList.children, []);
assert.deepEqual(visibleRows(), EDITOR_ROWS);
// However the picker went (Escape from a keyboard, say), the menu it opened goes with it.
controls.setMode("editor", editorView({ picker }));
assert.equal(menu.hidden, false);
controls.setMode("editor", editorView({}));
assert.equal(menu.hidden, true, "the menu the picker opened closes with it");
// And the picker never shows outside the editor.
controls.setMode("editor", editorView({ picker }));
controls.setMode("playing", editorView({ picker }));
assert.equal(pickerList.hidden, true);
assert.equal(menu.hidden, true);
assert.equal(menu.className, "touch-menu");
assert.deepEqual(visibleRows(), ["Save", "Help", "Quit", "3D view"], "play mode's rows come back");

// M39.3: the title bar's Editor taps E, the title menu's Board editor key, and
// is on no other screen's bar.
controls.setMode("title");
keyCalls.length = 0;
byLabel("Editor").dispatch("pointerdown");
assert.deepEqual(keyCalls, [{ down: true, code: "KeyE", key: "e" }, { down: false, code: "KeyE", key: "e" }], "Editor must tap KeyE");
for (const m of ["playing", "editor", "modal", "prompt", "watching"]) {
  controls.setMode(m);
  assert.equal(byLabel("Editor").hidden, true, `Editor must not be on the ${m} bar`);
}

console.log("touch_controls.test.mjs: all assertions passed");
