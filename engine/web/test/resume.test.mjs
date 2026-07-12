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
  reconnectDelay,
  buildJoinMessage,
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
}

console.log("resume.test.mjs: all assertions passed");
