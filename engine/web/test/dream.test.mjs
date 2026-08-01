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
const { DreamFailure, generationLines, retryDreamBoard, runDreamGeneration, salvagedBoards } = await import(
  `data:text/javascript;base64,${source}`
);

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
// M18.7: the cut is marked with three ASCII periods. CP437 has no horizontal
// ellipsis glyph, and the single "\x85" this used to append draws as 'a' with a
// grave accent — a stray letter on the screen testers watch while a world
// generates. Assert the exact marker, and that no CP437-unmappable character
// reached the line.
assert.ok(longName[0].endsWith("..."), `over-width line should end in "...": ${longName[0]}`);
assert.ok(!/\x85/.test(longName[0]), "the a-grave truncation marker is gone");

// M18.7: the exact line from the owner's screenshot, which rendered as
// "Painting board 6 of 8: Coat Check (attempà". Same events, same clamp.
const reported = generationLines([
  { stage: "painting", board: "Coat Check", index: 6, total: 8, attempt: 2, maxAttempts: 3 },
]);
assert.deepEqual(reported, ["Painting board 6 of 8: Coat Check (atte..."]);
assert.equal(reported[0].length, 42);

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
  "Painting board 1 of 2: Morning Light (a...", // clamped to the window width

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
const dream = await runDreamGeneration("an underwater clockwork city", successFetcher, async () => {}, (next) => progress.push(next));
assert.equal(dream.world, "TIDECELLAR");
// A dream that lost nothing carries no offer for the UI to make.
assert.equal(dream.jobId, "gen-1");
assert.equal(dream.retryable, false);
assert.deepEqual(dream.stubbedBoards, []);
assert.equal(salvagedBoards(dream), "");
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

// M12.22: a retryable failure rejects with DreamFailure carrying the job id
// and failed board, and retryDreamBoard re-requests that board and resumes
// polling the same job id to completion.
const retryCalls = [];
const retryReplies = [
  { id: "gen-4" },
  { status: "failed", error: 'board "Lunar Liftoff" exhausted 3 generation attempts', retryable: true, failedBoard: "Lunar Liftoff", progress: [] },
  { id: "gen-4" },
  { status: "running", progress: [{ stage: "painting", board: "Lunar Liftoff", attempt: 1, maxAttempts: 3 }] },
  { status: "complete", world: "MOONWORLD", progress: [] },
];
const retryFetcher = async (url, init) => {
  retryCalls.push({ url, init });
  return new Response(JSON.stringify(retryReplies.shift()), { status: 200 });
};
let failure;
try {
  await runDreamGeneration("to the moon", retryFetcher, async () => {}, () => {});
} catch (error) {
  failure = error;
}
assert.ok(failure instanceof DreamFailure, `expected DreamFailure, got ${failure}`);
assert.equal(failure.retryable, true);
assert.equal(failure.jobId, "gen-4");
assert.equal(failure.failedBoard, "Lunar Liftoff");
const retried = await retryDreamBoard(failure.jobId, retryFetcher, async () => {}, () => {});
assert.equal(retried.world, "MOONWORLD");
assert.equal(salvagedBoards(retried), "");
assert.deepEqual(JSON.parse(retryCalls[2].init.body), { retry: "gen-4", async: true });
assert.equal(retryCalls[3].url, "/api/generate?id=gen-4");
// A non-retryable failure surfaces retryable=false so the UI skips the offer.
const planFailReplies = [
  { id: "gen-5" },
  { status: "failed", error: "plan generation exhausted repairs" },
];
let planFailure;
try {
  await runDreamGeneration("bad plan", async () => new Response(JSON.stringify(planFailReplies.shift())), async () => {}, () => {});
} catch (error) {
  planFailure = error;
}
assert.ok(planFailure instanceof DreamFailure);
assert.equal(planFailure.retryable, false);

console.log("M12.5 dream flow: success, failure, and M12.22 retry paths passed");

// M17.13: a salvaged board renders as a named loss, not as a raw stage token.
assert.deepEqual(
  generationLines([{ stage: "salvaging", board: "The Tide Cellar", detail: "exhausted 3 attempts" }]),
  ["Lost board: The Tide Cellar"],
);
assert.deepEqual(
  generationLines([{ stage: "salvaging", detail: "2 of 9 boards failed" }]),
  ["Some rooms would not form..."],
);

// M16.17c: a salvaged dream is complete AND retryable. The world name must
// arrive together with that state — the client enters the world and offers the
// repaint, so dropping either field here is the whole defect.
const salvageCalls = [];
const salvageReplies = [
  { id: "gen-6" },
  { status: "running", progress: [{ stage: "salvaging", board: "Start", detail: "exhausted 1 attempt" }] },
  {
    status: "complete",
    world: "DREAM",
    retryable: true,
    failedBoard: "Start",
    stubbedBoards: ["Start"],
    progress: [],
  },
];
const salvageFetcher = async (url, init) => {
  salvageCalls.push({ url, init });
  return new Response(JSON.stringify(salvageReplies.shift()), { status: 200 });
};
const salvaged = await runDreamGeneration("a lighthouse", salvageFetcher, async () => {}, () => {});
assert.equal(salvaged.world, "DREAM", "a salvaged dream still hosts its world");
assert.equal(salvaged.jobId, "gen-6", "the offer resumes this job, not a fresh generation");
assert.equal(salvaged.retryable, true);
assert.deepEqual(salvaged.stubbedBoards, ["Start"]);
assert.equal(salvagedBoards(salvaged), "Start");

// Two lost rooms with no failedBoard name: the offer still says which rooms.
assert.equal(
  salvagedBoards({ world: "W", jobId: "gen-7", retryable: true, stubbedBoards: ["Attic", "Cellar"] }),
  "Attic, Cellar",
);
// Retryable with nothing stubbed, or stubs on a job the server will not resume:
// neither is an offer the client can make good on.
assert.equal(salvagedBoards({ world: "W", jobId: "gen-8", retryable: true, stubbedBoards: [] }), "");
assert.equal(salvagedBoards({ world: "W", jobId: "gen-9", retryable: false, stubbedBoards: ["Attic"] }), "");

// The repaint resumes the SAME job id, and its answer is a plain complete job:
// no offer left to make.
const repaintReplies = [{ id: "gen-6" }, { status: "complete", world: "DREAM", progress: [] }];
const repaintFetcher = async (url, init) => {
  salvageCalls.push({ url, init });
  return new Response(JSON.stringify(repaintReplies.shift()), { status: 200 });
};
const repainted = await retryDreamBoard(salvaged.jobId, repaintFetcher, async () => {}, () => {});
assert.equal(repainted.world, "DREAM");
assert.equal(salvagedBoards(repainted), "", "the repainted world has no rooms left to offer");
assert.deepEqual(JSON.parse(salvageCalls.at(-2).init.body), { retry: "gen-6", async: true });

console.log("M16.17c salvage offer: a complete-and-retryable dream carries its world and its lost rooms");

// M16.17d: the plan's name was taken (a classic, or another account's), so the
// world was minted one instead. The player chose neither name, so the line says
// only the thing they need — which world is theirs.
assert.deepEqual(
  generationLines([{ stage: "naming", detail: "GEN0A3F" }]),
  ["Your world is called GEN0A3F"],
);
assert.deepEqual(generationLines([{ stage: "naming" }]), ["Naming the world..."]);
console.log("M16.17d naming line: the minted world name reaches the progress window");
