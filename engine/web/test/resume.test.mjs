import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle resume.ts under Node so M13.2's reconnect + resume-token state machine
// can be exercised as pure logic, the same way modal.test.mjs covers modal key
// routing. resume.ts is deliberately DOM/WebSocket free for exactly this.
const output = await build({
  entryPoints: ["src/resume.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const {
  tokenKey,
  loadResumeToken,
  saveResumeToken,
  clearResumeToken,
  editorTokenKey,
  loadEditorToken,
  saveEditorToken,
  clearEditorToken,
  reconnectDelay,
  buildJoinMessage,
  buildEditorEnterMessage,
  loadPlayerColor,
  savePlayerColor,
  clearPlayerColor,
} = await import(`data:text/javascript;base64,${source}`);

// A plain in-memory stand-in for sessionStorage.
function memStore() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => void map.set(k, v),
    removeItem: (k) => void map.delete(k),
    size: () => map.size,
  };
}

// Tokens are keyed by world so two worlds never collide.
{
  const store = memStore();
  saveResumeToken(store, "TOWN", "abc");
  saveResumeToken(store, "CAVES", "xyz");
  assert.equal(loadResumeToken(store, "TOWN"), "abc");
  assert.equal(loadResumeToken(store, "CAVES"), "xyz");
  assert.notEqual(tokenKey("TOWN"), tokenKey("CAVES"));
}

// An absent token reads as empty, not null/undefined, so it omits cleanly.
{
  const store = memStore();
  assert.equal(loadResumeToken(store, "TOWN"), "");
}

// Clearing a token (explicit quit) forgets exactly that world's run.
{
  const store = memStore();
  saveResumeToken(store, "TOWN", "abc");
  clearResumeToken(store, "TOWN");
  assert.equal(loadResumeToken(store, "TOWN"), "");
}

// An empty token is never written: a fresh join must not persist a blank key.
{
  const store = memStore();
  saveResumeToken(store, "TOWN", "");
  assert.equal(store.size(), 0);
}

// A hostile store (sandboxed/quota-full sessionStorage) must never throw: the
// client falls back to fresh joins, it does not crash.
{
  const hostile = {
    getItem() {
      throw new Error("blocked");
    },
    setItem() {
      throw new Error("blocked");
    },
    removeItem() {
      throw new Error("blocked");
    },
  };
  assert.equal(loadResumeToken(hostile, "TOWN"), "");
  assert.doesNotThrow(() => saveResumeToken(hostile, "TOWN", "abc"));
  assert.doesNotThrow(() => clearResumeToken(hostile, "TOWN"));
}

// Backoff is capped exponential and clamps at the cap.
{
  assert.equal(reconnectDelay(0, 500, 8000), 500);
  assert.equal(reconnectDelay(1, 500, 8000), 1000);
  assert.equal(reconnectDelay(2, 500, 8000), 2000);
  assert.equal(reconnectDelay(4, 500, 8000), 8000); // 500*16=8000
  assert.equal(reconnectDelay(10, 500, 8000), 8000); // clamped
  assert.equal(reconnectDelay(-1, 500, 8000), 500); // guarded
}

// The join message carries a token only when one is present.
{
  const fresh = buildJoinMessage("join", "browser", "");
  assert.deepEqual(fresh, { type: "join", name: "browser" });
  assert.ok(!("resumeToken" in fresh));

  const resume = buildJoinMessage("join", "browser", "abc");
  assert.deepEqual(resume, { type: "join", name: "browser", resumeToken: "abc" });

  // M19.1 — the picked colour rides the join, and an unset one is OMITTED
  // rather than sent as "": JoinMessage.Color is `omitempty`, and an absent
  // colour is what the server reads as the vanilla white-on-blue player.
  const uncolored = buildJoinMessage("join", "browser", "abc", "");
  assert.ok(!("color" in uncolored), "an unset colour must not ride the join");
  const colored = buildJoinMessage("join", "browser", "abc", "#a1b2c3");
  assert.deepEqual(colored, { type: "join", name: "browser", resumeToken: "abc", color: "#a1b2c3" });
}

// The colour is stored under its own key, in localStorage rather than the
// sessionStorage the tokens use: it is a property of the player, not of a run,
// and it should survive closing the tab (M19.1; M19.2 writes it, M19.3 makes a
// signed-in player's account copy win over it).
{
  const store = memStore();
  assert.equal(loadPlayerColor(store), "", "no pick yet reads as no colour");
  savePlayerColor(store, "#00c0ff");
  assert.equal(loadPlayerColor(store), "#00c0ff");
  savePlayerColor(store, "");
  assert.equal(loadPlayerColor(store), "#00c0ff", "an empty write is ignored, as it is for a token");
  assert.notEqual(tokenKey("TOWN"), "zzt-color");

  // M19.2 — the picker's "No colour" row. It has to REMOVE the key: the empty
  // write above is ignored by design, so a player choosing vanilla again would
  // otherwise keep wearing the colour they just took off.
  clearPlayerColor(store);
  assert.equal(loadPlayerColor(store), "", "clearing takes the player back to vanilla");
  clearPlayerColor(store);
  assert.equal(loadPlayerColor(store), "", "and clearing an unpicked colour is not an error");
}

// M16.14f — the editor's membership token is a SEPARATE key. A browser can be
// holding a dropped run and a dropped editing session for one world at the same
// time, and they name different things on the server; one prefix for both would
// have the editor re-entering with a player's token and the reverse.
{
  const store = memStore();
  saveResumeToken(store, "TOWN", "player");
  saveEditorToken(store, "TOWN", "member");
  assert.notEqual(tokenKey("TOWN"), editorTokenKey("TOWN"));
  assert.equal(loadResumeToken(store, "TOWN"), "player");
  assert.equal(loadEditorToken(store, "TOWN"), "member");

  // Leaving the editor on purpose forgets the membership, and only that.
  clearEditorToken(store, "TOWN");
  assert.equal(loadEditorToken(store, "TOWN"), "");
  assert.equal(loadResumeToken(store, "TOWN"), "player");
}

// The editor token is keyed by world, absent reads as empty, an empty one is
// never written, and a hostile store cannot throw — the resume token's rules,
// because it is the same storage on the same page.
{
  const store = memStore();
  saveEditorToken(store, "EDIT", "one");
  saveEditorToken(store, "OTHER", "two");
  assert.equal(loadEditorToken(store, "EDIT"), "one");
  assert.equal(loadEditorToken(store, "OTHER"), "two");
  assert.equal(loadEditorToken(store, "NEVER"), "");
  saveEditorToken(store, "BLANK", "");
  assert.equal(store.size(), 2);

  const hostile = {
    getItem() {
      throw new Error("blocked");
    },
    setItem() {
      throw new Error("blocked");
    },
    removeItem() {
      throw new Error("blocked");
    },
  };
  assert.equal(loadEditorToken(hostile, "EDIT"), "");
  assert.doesNotThrow(() => saveEditorToken(hostile, "EDIT", "one"));
  assert.doesNotThrow(() => clearEditorToken(hostile, "EDIT"));
}

// editorEnter carries the membership token only when one is stored: an empty
// one must be omitted, or the server reads a first entry as a lookup.
{
  const fresh = buildEditorEnterMessage("editorEnter", "EDIT", "");
  assert.deepEqual(fresh, { type: "editorEnter", world: "EDIT" });
  assert.ok(!("resumeToken" in fresh));

  const resumed = buildEditorEnterMessage("editorEnter", "EDIT", "member");
  assert.deepEqual(resumed, { type: "editorEnter", world: "EDIT", resumeToken: "member" });
}

console.log("resume.test.mjs: all assertions passed");
