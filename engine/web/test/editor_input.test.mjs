import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle editor_input.ts under Node so the F4 text-entry latency fix is covered
// without a browser: printable keys paint locally immediately, while the server
// remains authoritative through the normal editorDiff reply.
const output = await build({
  entryPoints: ["src/editor_input.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const {
  editorTextRenderColor,
  optimisticEditorTextCell,
  optimisticEditorEraseCell,
} = await import(`data:text/javascript;base64,${source}`);

assert.equal(editorTextRenderColor(0x09), 0x1f, "blue text renders as bright blue on white");
assert.equal(editorTextRenderColor(0x0e), 0x6f, "yellow text renders as yellow on white");
assert.equal(editorTextRenderColor(0x0f), 0x0f, "white text keeps vanilla white-on-black");
assert.equal(editorTextRenderColor(0x02), 0x1f, "invalid low fg clamps to blue");

assert.deepEqual(
  optimisticEditorTextCell({ x: 8, y: 6 }, "A".charCodeAt(0), 0x0e),
  { x: 7, y: 5, ch: "A".charCodeAt(0), color: 0x6f },
  "printable text paints the current cursor cell immediately",
);
assert.equal(optimisticEditorTextCell({ x: 8, y: 6 }, 0x03, 0x0e), null, "control bytes are not text");

assert.deepEqual(
  optimisticEditorEraseCell({ x: 8, y: 6 }),
  { x: 6, y: 5, ch: 0x20, color: 0x0f },
  "backspace locally clears the cell to the left",
);
assert.equal(optimisticEditorEraseCell({ x: 1, y: 6 }), null, "left edge has no cell to clear");

console.log("editor_input.test.mjs: all assertions passed");
