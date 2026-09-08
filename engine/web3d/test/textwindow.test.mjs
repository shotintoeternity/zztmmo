import assert from "node:assert/strict";
import { build } from "esbuild";

const out = await build({ entryPoints: ["src/modals.ts"], bundle: true, format: "esm", platform: "node", write: false });
const { hyperlinkOf, newTextModal, modalKey, renderModal } = await import(
  `data:text/javascript;base64,${Buffer.from(out.outputFiles[0].contents).toString("base64")}`
);

assert.equal(hyperlinkOf("!buy;Buy a torch"), "buy");
assert.equal(hyperlinkOf("!-HELP;Read the help"), "", "a file link is not a label");
assert.equal(hyperlinkOf("plain text"), "");

// Enter on a hyperlink picks it; Escape closes without one.
{
  let picked = "";
  let closed = 0;
  const modal = newTextModal("Shop", ["Welcome", "!buy;Buy", "!leave;Leave"], false, (label) => { picked = label; }, () => { closed += 1; });
  assert.equal(modalKey(modal, { code: "ArrowDown", key: "ArrowDown" }), "redraw");
  assert.equal(modal.state.linePos, 2);
  assert.equal(modalKey(modal, { code: "Enter", key: "Enter" }), "close");
  assert.equal(picked, "buy");
  assert.equal(closed, 0);
  const again = newTextModal("Shop", ["Welcome"], false, () => {}, () => { closed += 1; });
  assert.equal(modalKey(again, { code: "Escape", key: "Escape" }), "close");
  assert.equal(closed, 1);
}

// The window paints inside the board columns and centers its title.
{
  const writes = [];
  renderModal((x, y, color, text) => writes.push({ x, y, color, text }), newTextModal("Scroll", ["Hello"], false));
  assert.ok(writes.every((w) => w.x + w.text.length <= 60), "a text window never touches the sidebar");
  assert.ok(writes.some((w) => w.text === "Scroll" && w.color === 0x1e));
}

// The sidebar prompts.
{
  let answer = null;
  const yesno = { kind: "yesno", question: "End this game? ", onAnswer: (yes) => { answer = yes; } };
  assert.equal(modalKey(yesno, { code: "KeyX", key: "x" }), "ignore");
  assert.equal(modalKey(yesno, { code: "KeyY", key: "y" }), "close");
  assert.equal(answer, true);
  let submitted;
  const entry = { kind: "entry", prompt: "Save game:", buffer: "", width: 8, onSubmit: (t) => { submitted = t; } };
  for (const ch of "TOWNSAVE!") {
    modalKey(entry, { code: "Key" + ch, key: ch });
  }
  assert.equal(entry.buffer, "TOWNSAVE", "the field is eight wide");
  assert.equal(modalKey(entry, { code: "Backspace", key: "Backspace" }), "redraw");
  assert.equal(modalKey(entry, { code: "Enter", key: "Enter" }), "close");
  assert.equal(submitted, "TOWNSAV");
  const cancelled = { kind: "entry", prompt: "", buffer: "x", width: 11, onSubmit: (t) => { submitted = t; } };
  modalKey(cancelled, { code: "Escape", key: "Escape" });
  assert.equal(submitted, null);
}

console.log("textwindow ok");
