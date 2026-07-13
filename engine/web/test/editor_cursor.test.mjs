import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle editor_cursor.ts under Node so the held-arrow fix is covered as pure
// state logic: delayed inspect/diff replies for old cursor positions must not
// move the browser-owned editor cursor backward.
const output = await build({
  entryPoints: ["src/editor_cursor.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { editorReplyMatchesCursor, sameEditorCursor } = await import(`data:text/javascript;base64,${source}`);

assert.ok(sameEditorCursor({ x: 12, y: 7 }, { x: 12, y: 7 }));
assert.ok(!sameEditorCursor({ x: 12, y: 7 }, { x: 11, y: 7 }));

const current = { x: 30, y: 12 };
const staleReply = { x: 29, y: 12 };
const currentReply = { x: 30, y: 12 };
assert.equal(editorReplyMatchesCursor(current, staleReply), false, "stale editor replies are ignored");
assert.equal(editorReplyMatchesCursor(current, currentReply), true, "current-position replies may update inspect UI");

console.log("editor_cursor.test.mjs: all assertions passed");
