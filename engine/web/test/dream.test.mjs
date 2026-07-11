import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle the same browser source under Node. This gives M12.5 a small,
// dependency-free scripted-client test instead of leaving the async UI flow as
// a manual browser check.
const output = await build({
  entryPoints: ["src/dream.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { generationLines, runDreamGeneration } = await import(`data:text/javascript;base64,${source}`);

assert.deepEqual(generationLines([]), ["", "$Imagining the world...", ""]);
assert.deepEqual(
  generationLines([
    { stage: "painting", board: "The Tide Cellar", index: 7, total: 12, attempt: 1, maxAttempts: 3 },
    { stage: "repairing", board: "The Tide Cellar", attempt: 2, maxAttempts: 3 },
  ]),
  ["Painting board 7 of 12: The Tide Cellar", "Repairing The Tide Cellar: attempt 2 of 3"],
);

const successCalls = [];
const progress = [];
const successReplies = [
  { id: "gen-1" },
  { status: "running", progress: [{ stage: "planning", attempt: 1, maxAttempts: 3 }] },
  {
    status: "complete",
    world: "TIDECELLAR",
    progress: [{ stage: "painting", board: "The Tide Cellar", index: 7, total: 12, attempt: 1, maxAttempts: 3 }],
  },
];
const successFetcher = async (url, init) => {
  successCalls.push({ url, init });
  return new Response(JSON.stringify(successReplies.shift()), { status: 200 });
};
const world = await runDreamGeneration("an underwater clockwork city", successFetcher, async () => {}, (next) => progress.push(next));
assert.equal(world, "TIDECELLAR");
assert.equal(successCalls[0].url, "/api/generate");
assert.equal(successCalls[0].init.method, "POST");
assert.deepEqual(JSON.parse(successCalls[0].init.body), { prompt: "an underwater clockwork city", async: true });
assert.equal(successCalls[2].url, "/api/generate?id=gen-1");
assert.equal(generationLines(progress.at(-1))[0], "Painting board 7 of 12: The Tide Cellar");

const failureReplies = [
  { id: "gen-2" },
  { status: "failed", error: "board Start exhausted 3 generation attempts" },
];
await assert.rejects(
  runDreamGeneration("broken dream", async () => new Response(JSON.stringify(failureReplies.shift())), async () => {}, () => {}),
  /board Start exhausted 3 generation attempts/,
);

console.log("M12.5 dream flow: success and failure paths passed");
