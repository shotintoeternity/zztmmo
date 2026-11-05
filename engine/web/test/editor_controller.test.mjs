import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/editor_controller.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { editorKeyMutates } = await import(`data:text/javascript;base64,${source}`);

// Place, erase and fill are what a read-only member is refused at the key.
for (const code of ["Space", "Delete", "Backspace", "KeyX"]) {
  assert.equal(editorKeyMutates(code), true, `${code} must be refused to a read-only member`);
}
// Moving, inspecting, menus, help, and the lease-gated commands are not.
for (const code of ["ArrowUp", "Numpad8", "KeyH", "KeyB", "KeyI", "KeyS", "KeyT", "KeyW", "KeyZ", "KeyN", "Enter", "F1", "F4", "Tab", "KeyP", "KeyC", "Escape", "KeyQ"]) {
  assert.equal(editorKeyMutates(code), false, `${code} is not a direct board edit`);
}
console.log("editor_controller ok");
