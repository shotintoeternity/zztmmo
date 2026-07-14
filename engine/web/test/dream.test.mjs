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

// Fix #A: client-composed progress lines must be clamped to the text window's
// inner width (TEXT_WINDOW_WIDTH-8 = 42) so a long board name plus the attempt
// suffix cannot bleed past the window border into the sidebar.
const longName = generationLines([
  { stage: "painting", board: "The Everlong Saga of the ZZTers", index: 7, total: 12, attempt: 2, maxAttempts: 3 },
]);
assert.equal(longName.length, 1);
assert.ok(longName[0].length <= 42, `progress line too wide: ${longName[0].length}`);
assert.ok(longName[0].endsWith("\x85"), "over-width line should be truncated with a CP437 ellipsis");

// M12.18: the server emits two wire events for one logical step (the world
// loop's "painting" with index/total, then paintBoard's "painting" with only a
// detail — generation.go:224/374; "planning" twins the same way at 198/342).
// Each poll returns the full cumulative array, so replay one as it grows and
// assert every progress line renders exactly once per poll.
const polledSequence = [
  { stage: "planning", attempt: 1, maxAttempts: 3, detail: "imagining the world plan" },
  { stage: "planning", attempt: 1, maxAttempts: 3, detail: "asking Claude for a world plan" },
  { stage: "painting", board: "Morning Light", index: 1, total: 2, attempt: 1, maxAttempts: 3 },
  { stage: "painting", board: "Morning Light", attempt: 1, maxAttempts: 3, detail: "asking Claude for board ZWD" },
  { stage: "repairing", board: "Morning Light", attempt: 2, maxAttempts: 3, detail: "orphan stat" },
  { stage: "painting", board: "Morning Light", attempt: 2, maxAttempts: 3, detail: "asking Claude for board ZWD" },
  { stage: "painting", board: "Lunar Liftoff", index: 2, total: 2, attempt: 1, maxAttempts: 3 },
  { stage: "painting", board: "Lunar Liftoff", attempt: 1, maxAttempts: 3, detail: "asking Claude for board ZWD" },
];
for (let polled = 1; polled <= polledSequence.length; polled++) {
  // Poll the same prefix twice — a repeated poll of an unchanged job must not
  // duplicate lines either.
  for (let repeat = 0; repeat < 2; repeat++) {
    const lines = generationLines(polledSequence.slice(0, polled));
    const counts = new Map();
    for (const line of lines) counts.set(line, (counts.get(line) ?? 0) + 1);
    for (const [line, count] of counts) {
      assert.equal(count, 1, `progress line rendered ${count} times after ${polled} events: ${line}`);
    }
  }
}
const fullLines = generationLines(polledSequence);
assert.deepEqual(fullLines, [
  "Imagining the world...",
  "Painting board 1 of 2: Morning Light",
  "Repairing Morning Light: attempt 2 of 3",
  "Painting board 1 of 2: Morning Light (att\x85", // clamped to the window width

  "Painting board 2 of 2: Lunar Liftoff",
]);
// The dedupe keys on event identity (stage+board+attempt), not rendered text:
// a real second attempt renders its own line even though the board repeats,
// and the index/total from whichever twin carried them survive the collapse.
const twinOrderSwapped = generationLines([
  { stage: "painting", board: "Hub", attempt: 1, maxAttempts: 3, detail: "asking Claude for board ZWD" },
  { stage: "painting", board: "Hub", index: 3, total: 9, attempt: 1, maxAttempts: 3 },
]);
assert.deepEqual(twinOrderSwapped, ["Painting board 3 of 9: Hub"]);

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
assert.deepEqual(JSON.parse(successCalls[0].init.body), { prompt: "an underwater clockwork city", async: true, ground: false });
assert.equal(successCalls[2].url, "/api/generate?id=gen-1");
assert.equal(generationLines(progress.at(-1))[0], "Painting board 7 of 12: The Tide Cellar");

// Opt-in grounding flag propagates into the /api/generate request body.
const groundCalls = [];
const groundReplies = [
  { id: "gen-3" },
  { status: "complete", world: "GROUNDED", progress: [] },
];
const groundFetcher = async (url, init) => {
  groundCalls.push({ url, init });
  return new Response(JSON.stringify(groundReplies.shift()), { status: 200 });
};
await runDreamGeneration("the saga of the mzxers", groundFetcher, async () => {}, () => {}, true);
assert.deepEqual(JSON.parse(groundCalls[0].init.body), { prompt: "the saga of the mzxers", async: true, ground: true });

const failureReplies = [
  { id: "gen-2" },
  { status: "failed", error: "board Start exhausted 3 generation attempts" },
];
await assert.rejects(
  runDreamGeneration("broken dream", async () => new Response(JSON.stringify(failureReplies.shift())), async () => {}, () => {}),
  /board Start exhausted 3 generation attempts/,
);

console.log("M12.5 dream flow: success and failure paths passed");
