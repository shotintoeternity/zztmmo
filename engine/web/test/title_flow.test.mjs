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
const { FIRST_VISIT_WELCOME_KEY, WELCOME_WORLD, hasSeenWelcome, markWelcomeSeen, selectWorldForTitle, shouldOpenWelcomeFirstVisit } = await import(`data:text/javascript;base64,${source}`);

{
  const selection = selectWorldForTitle("CAVES");
  assert.deepEqual(selection, { worldName: "CAVES", startPlay: false });
}

{
  assert.equal(WELCOME_WORLD, "WELCOME");
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

{
  assert.equal(shouldOpenWelcomeFirstVisit({ authenticated: false, hasSeenWelcome: false, welcomeHosted: true }), true);
  assert.equal(shouldOpenWelcomeFirstVisit({ authenticated: true, hasSeenWelcome: false, welcomeHosted: true }), false);
  assert.equal(shouldOpenWelcomeFirstVisit({ authenticated: false, hasSeenWelcome: true, welcomeHosted: true }), false);
  assert.equal(shouldOpenWelcomeFirstVisit({ authenticated: false, hasSeenWelcome: false, welcomeHosted: false }), false);
}

console.log("title_flow.test.mjs: world selection and first-visit welcome passed");
