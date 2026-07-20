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
const {
  editorReplyMatchesCursor,
  sameEditorCursor,
  editorCursorShown,
  editorCursorOverlay,
  EDITOR_CURSOR_CHAR,
  EDITOR_CURSOR_COLOR,
} = await import(`data:text/javascript;base64,${source}`);

assert.ok(sameEditorCursor({ x: 12, y: 7 }, { x: 12, y: 7 }));
assert.ok(!sameEditorCursor({ x: 12, y: 7 }, { x: 11, y: 7 }));

const current = { x: 30, y: 12 };
const staleReply = { x: 29, y: 12 };
const currentReply = { x: 30, y: 12 };
assert.equal(editorReplyMatchesCursor(current, staleReply), false, "stale editor replies are ignored");
assert.equal(editorReplyMatchesCursor(current, currentReply), true, "current-position replies may update inspect UI");

// M5.11: the cursor blink follows EditorLoop's 3-phase cursorBlinker. Phase 0 is
// the "tile shown" phase; phases 1 and 2 are "cursor shown".
assert.equal(editorCursorShown(0), false, "phase 0 reveals the tile underneath");
assert.equal(editorCursorShown(1), true, "phase 1 shows the cursor");
assert.equal(editorCursorShown(2), true, "phase 2 shows the cursor");
assert.equal(editorCursorShown(3), false, "the cycle wraps back to tile-shown");
assert.equal(editorCursorShown(-3), false, "negative counters normalize to phase 0");

const cursor = { x: 30, y: 12 };
const boardCols = 60;
const rows = 25;

// Phase 0 emits no overlay, so the underlying board cell — including a stat-backed
// object sitting under the cursor — renders untouched (under-tile visibility).
const revealed = editorCursorOverlay({ blink: 0, cursor, presence: [], selfId: "me", boardCols, rows });
assert.deepEqual(revealed, [], "off phase draws nothing over the tile/object");

// Cursor-shown phase draws the cross cursor 0xC5 in 0x0F at the board cell,
// shifted to 0-based screen coordinates.
const shown = editorCursorOverlay({ blink: 1, cursor, presence: [], selfId: "me", boardCols, rows });
assert.equal(shown.length, 1, "only the local cursor when no collaborators");
assert.deepEqual(shown[0], {
  x: 29,
  y: 11,
  color: EDITOR_CURSOR_COLOR,
  text: String.fromCharCode(EDITOR_CURSOR_CHAR),
});
assert.equal(EDITOR_CURSOR_CHAR, 0xc5, "vanilla editor cursor glyph");
assert.equal(EDITOR_CURSOR_COLOR, 0x0f, "vanilla editor cursor color");

// Collaborator cursors blink on the same cadence: present on cursor-shown phases,
// gone on the tile-shown phase so they never permanently hide the object beneath.
const presence = [
  { id: "me", name: "Self", color: 0x1e, x: 5, y: 5 },
  { id: "peer", name: "Collaborator", color: 0x1a, x: 10, y: 8 },
  { id: "off", name: "OffBoard", color: 0x1b, x: 200, y: 8 },
];
// M17.9: a collaborator is the ordinary editor cursor in their own colour — one
// cell, the same 0xC5 cross the local cursor uses, and no name label beside it.
const withPeer = editorCursorOverlay({ blink: 2, cursor, presence, selfId: "me", boardCols, rows });
assert.equal(withPeer.length, 2, "local cursor + one cell per remote peer; self and off-board skipped");
assert.deepEqual(
  withPeer[1],
  { x: 9, y: 7, color: 0x1a, text: String.fromCharCode(EDITOR_CURSOR_CHAR) },
  "peer draws the standard editor cursor glyph in its own colour",
);
assert.ok(
  !withPeer.some((cell) => cell.text.length > 1),
  "no collaborator name text is painted onto the board",
);
// M17.12: a collaborator on another board is not drawn at all — no ghost cursors
// from boards the viewer cannot see.
const boarded = [
  { id: "me", name: "Self", color: 0x0e, boardId: 1, x: 5, y: 5 },
  { id: "same", name: "SameBoard", color: 0x0b, boardId: 1, x: 10, y: 8 },
  { id: "other", name: "OtherBoard", color: 0x0a, boardId: 2, x: 12, y: 9 },
];
const onBoard1 = editorCursorOverlay({ blink: 1, cursor, presence: boarded, selfId: "me", boardCols, rows, boardId: 1 });
assert.equal(onBoard1.length, 2, "local cursor + only the collaborator on the same board");
assert.deepEqual(
  onBoard1[1],
  { x: 9, y: 7, color: 0x0b, text: String.fromCharCode(EDITOR_CURSOR_CHAR) },
  "same-board collaborator drawn; other-board collaborator omitted",
);
const onBoard2 = editorCursorOverlay({ blink: 1, cursor, presence: boarded, selfId: "me", boardCols, rows, boardId: 2 });
assert.equal(onBoard2.length, 2, "switching boards reveals that board's collaborator instead");
assert.equal(onBoard2[1].color, 0x0a, "the board-2 collaborator is the one now shown");
const unfiltered = editorCursorOverlay({ blink: 1, cursor, presence: boarded, selfId: "me", boardCols, rows });
assert.equal(unfiltered.length, 3, "omitting boardId keeps pre-M17.12 behaviour (no filtering)");

const peerRevealed = editorCursorOverlay({ blink: 0, cursor, presence, selfId: "me", boardCols, rows });
assert.deepEqual(peerRevealed, [], "collaborator cursors blink off with the local one");

console.log("editor_cursor.test.mjs: all assertions passed");
