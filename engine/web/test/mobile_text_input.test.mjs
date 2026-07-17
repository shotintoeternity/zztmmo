import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/mobile_text_input.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { MobileTextInputBridge, handleModalTextInput, modalAcceptsTextInput } = await import(`data:text/javascript;base64,${source}`);

class FakeElement {
  constructor(tag, host) {
    this.tag = tag;
    this.host = host;
    this.body = host.body;
    this.style = {};
    this.value = "";
    this.listeners = new Map();
    this.focused = 0;
    this.blurred = 0;
  }

  setAttribute() {}
  addEventListener(type, listener) { this.listeners.set(type, listener); }
  dispatch(type, detail = {}) {
    if (typeof detail.preventDefault !== "function") {
      detail.preventDefault = () => { detail.defaultPrevented = true; };
    }
    this.listeners.get(type)?.(detail);
    return detail;
  }
  // Track document.activeElement so the bridge's isFocused()/keep-keyboard path is
  // exercised: focusing an element makes it active; blurring it clears active.
  focus() { this.focused += 1; this.host.activeElement = this; }
  blur() { this.blurred += 1; if (this.host.activeElement === this) this.host.activeElement = null; }
  remove() {
    const index = this.body.children.indexOf(this);
    if (index >= 0) this.body.children.splice(index, 1);
  }
}

const body = {
  children: [],
  appendChild(element) { this.children.push(element); },
};
const host = {
  body,
  activeElement: null,
  createElement(tag) { return new FakeElement(tag, host); },
};

const bridge = new MobileTextInputBridge(host, 1);
const entry = {
  kind: "entry",
  label: "Name:",
  suffix: "",
  width: 20,
  buffer: "",
  charset: "any",
  onSubmit() {},
};
const mirror = (modal) => (input) => handleModalTextInput(modal, input);

// The overlay is native and focusable, but its text is mirrored into the modal
// buffer; it never becomes a rendered source of truth itself. A cold open does
// NOT focus — iOS raises the keyboard only inside a user gesture, and focusing an
// already-active field is what makes the keyboard flicker. The gesture (canvas
// touchstart → noteTouchStart) is the sole place we focus and raise it.
bridge.sync(entry, mirror(entry));
const input = body.children[0];
assert.equal(input.tag, "input");
assert.equal(input.focused, 0);
assert.equal(bridge.isActive(), true);
bridge.noteTouchStart();
assert.equal(input.focused, 1);
input.value = "Ada";
input.dispatch("input", { inputType: "insertText", data: "Ada" });
assert.equal(entry.buffer, "Ada");
input.dispatch("input", { inputType: "deleteContentBackward", data: null });
assert.equal(entry.buffer, "Ad");

// Composition commits once even when a browser subsequently emits its ordinary
// insertText event. This is the Android/IME seam that canvas keydown misses.
input.dispatch("compositionstart");
input.dispatch("compositionupdate", { data: "Z" });
input.dispatch("input", { inputType: "insertCompositionText", data: "Z" });
assert.equal(entry.buffer, "Ad");
input.dispatch("compositionend", { data: "Z" });
input.dispatch("input", { inputType: "insertText", data: "Z" });
assert.equal(entry.buffer, "AdZ");

const multi = {
  kind: "multilineEntry",
  title: "Dream a world",
  buffer: "",
  submitted: null,
  onSubmit(text) { this.submitted = text; },
};
bridge.sync(multi, mirror(multi));
const dreamInput = body.children[0];
assert.equal(dreamInput.tag, "input");
dreamInput.dispatch("input", { inputType: "insertText", data: "moon sea" });
dreamInput.dispatch("input", { inputType: "insertLineBreak", data: "\n" });
assert.equal(multi.buffer, "moon sea");
assert.equal(multi.submitted, "moon sea");

const chat = {
  kind: "chat",
  title: "Global Chat",
  messages: [],
  buffer: "",
  submitted: null,
  onSubmit(text) { this.submitted = text; },
};
bridge.sync(chat, mirror(chat));
const chatInput = body.children[0];
assert.equal(chatInput.tag, "input");
chatInput.dispatch("input", { inputType: "insertText", data: "hello from touch" });
chatInput.dispatch("input", { inputType: "insertLineBreak", data: "\n" });
assert.equal(chat.submitted, "hello from touch");

// M17.6: the iOS soft-keyboard Return does not mutate a single-line <input>, so
// no `input` event fires — only `beforeinput` reports the line break. The bridge
// must route it to the same submit path desktop Enter uses, or the name popup /
// editor prompts can never be submitted from the on-screen keyboard.
const enterEntry = {
  kind: "entry",
  label: "Name:",
  suffix: "",
  width: 20,
  buffer: "Zoe",
  charset: "any",
  submitted: null,
  onSubmit(text) { this.submitted = text; },
};
bridge.sync(enterEntry, mirror(enterEntry));
const enterInput = body.children[0];
assert.equal(enterInput.enterKeyHint, "go");
const enterEvent = enterInput.dispatch("beforeinput", { inputType: "insertLineBreak", data: null });
assert.equal(enterEntry.submitted, "Zoe");
assert.equal(enterEvent.defaultPrevented, true);

// M17.7: other soft keyboards report Return only as a `keydown` (key "Enter").
// That must submit too, and its preventDefault cancels the `beforeinput` a browser
// would otherwise also fire, so Enter never double-submits.
const keyEntry = {
  kind: "entry",
  label: "Name:",
  suffix: "",
  width: 20,
  buffer: "Rex",
  charset: "any",
  submitted: null,
  onSubmit(text) { this.submitted = text; },
};
bridge.sync(keyEntry, mirror(keyEntry));
const keyInput = body.children[0];
const keyEvent = keyInput.dispatch("keydown", { key: "Enter" });
assert.equal(keyEntry.submitted, "Rex");
assert.equal(keyEvent.defaultPrevented, true);

// Chat submits on the paragraph-break variant (some keyboards report it) and
// labels its Return key "send".
const enterChat = {
  kind: "chat",
  title: "Global Chat",
  messages: [],
  buffer: "gg",
  submitted: null,
  onSubmit(text) { this.submitted = text; },
};
bridge.sync(enterChat, mirror(enterChat));
const enterChatInput = body.children[0];
assert.equal(enterChatInput.enterKeyHint, "send");
enterChatInput.dispatch("beforeinput", { inputType: "insertParagraph", data: null });
assert.equal(enterChat.submitted, "gg");

// The source-code editor keeps a native <textarea> where Return is real newline
// content, so it must NOT get the beforeinput submit shortcut — its newline
// still flows through the `input` path as document text.
const editor = {
  kind: "programEditor",
  title: "Object",
  lines: [""],
  cursorRow: 0,
  cursorCol: 0,
  onSubmit() {},
};
bridge.sync(editor, mirror(editor));
const editorEl = body.children[0];
assert.equal(editorEl.tag, "textarea");
assert.equal(editorEl.listeners.has("beforeinput"), false);
assert.equal(editorEl.listeners.has("keydown"), false);

assert.equal(modalAcceptsTextInput({ kind: "programEditor" }), true);
assert.equal(modalAcceptsTextInput({ kind: "worldSearch" }), true);
assert.equal(modalAcceptsTextInput({ kind: "yesno" }), false);
bridge.close();
assert.equal(body.children.length, 0);
assert.equal(dreamInput.blurred, 1);
assert.equal(chatInput.blurred, 1);

console.log("mobile_text_input.test.mjs: all assertions passed");
