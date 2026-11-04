import assert from "node:assert/strict";
import { build } from "esbuild";

// M36.27: reaching window.localStorage / window.sessionStorage can itself
// throw (a sandboxed frame, storage blocked by the browser). The helpers in
// resume.ts and preferences.ts guard their own calls, but the getter ran at
// the call site, outside any guard. safeStorage is that guard.
const output = await build({
  entryPoints: ["src/safe_storage.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { safeStorage } = await import(`data:text/javascript;base64,${source}`);

const blocked = {
  get localStorage() {
    throw new DOMException("The operation is insecure.", "SecurityError");
  },
  get sessionStorage() {
    throw new DOMException("The operation is insecure.", "SecurityError");
  },
};

// A throwing getter yields a working in-memory store, not an exception.
const local = safeStorage("localStorage", blocked);
local.setItem("k", "v");
assert.equal(local.getItem("k"), "v");
assert.equal(local.length, 1);
assert.equal(local.key(0), "k");
local.removeItem("k");
assert.equal(local.getItem("k"), null);

// The fallback is kept for the page's life, so what was stored is still there
// on the next call; local and session stay separate.
safeStorage("localStorage", blocked).setItem("kept", "1");
assert.equal(safeStorage("localStorage", blocked).getItem("kept"), "1");
assert.equal(safeStorage("sessionStorage", blocked).getItem("kept"), null);

// A working getter is passed straight through.
const real = { marker: true };
assert.equal(safeStorage("localStorage", { localStorage: real }), real);

console.log("safe_storage.test.mjs: all assertions passed");
