import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/title_flow.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { FIRST_VISIT_WELCOME_KEY, hasSeenWelcome, markWelcomeSeen, selectWorldForTitle } = await import(`data:text/javascript;base64,${source}`);

{
  const selection = selectWorldForTitle("CAVES");
  assert.deepEqual(selection, { worldName: "CAVES", startPlay: false });
}

// The WELCOME world is gone (owner 2026-08-11) but its storage key is not: the
// browser suites' markProfileWarm writes it to declare which visitor a script
// is (M33.2), so the key and its two accessors are still load-bearing.
{
  assert.equal(FIRST_VISIT_WELCOME_KEY, "zzt-first-visit-welcome");
  const storage = new Map();
  const shim = {
    getItem(key) {
      return storage.has(key) ? storage.get(key) : null;
    },
    setItem(key, value) {
      storage.set(key, String(value));
    },
  };
  assert.equal(hasSeenWelcome(shim), false);
  markWelcomeSeen(shim);
  assert.equal(hasSeenWelcome(shim), true);
}

console.log("title_flow.test.mjs: world selection and the first-visit key passed");
