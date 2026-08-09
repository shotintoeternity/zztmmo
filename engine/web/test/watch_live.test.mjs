import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/watch_live.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { WATCH_LIVE_CYCLE_MS, nextWatchLiveIndex, watchLiveEmbedMode, watchLiveEntryLabel, watchLiveEntryTarget } = await import(`data:text/javascript;base64,${source}`);

assert.equal(WATCH_LIVE_CYCLE_MS, 15000);

assert.equal(nextWatchLiveIndex(-1, 0), -1);
assert.equal(nextWatchLiveIndex(-1, 3), 0);
assert.equal(nextWatchLiveIndex(0, 3), 1);
assert.equal(nextWatchLiveIndex(2, 3), 0);

assert.equal(watchLiveEmbedMode("?embed=1"), true);
assert.equal(watchLiveEmbedMode("?embed=0"), false);
assert.equal(watchLiveEmbedMode(""), false);

assert.equal(
  watchLiveEntryLabel({ kind: "live", world: "TOWN", title: "TOWN" }, 0, 2),
  "TV 1/2 TOWN",
);
assert.equal(
  watchLiveEntryLabel({ kind: "replay", replayId: "TOWN-20260807-120000", world: "TOWN" }, 1, 2),
  "TV R 2/2 TOWN",
);
assert.ok(
  watchLiveEntryLabel({ kind: "live", world: "LONGNAME", title: "A Very Long World Title" }, 0, 1).length <= 15,
  "channel labels must fit the watcher sidebar",
);

assert.deepEqual(
  watchLiveEntryTarget({ kind: "live", world: "TOWN" }),
  { kind: "live", world: "TOWN" },
  "live entries must reuse the existing world watcher path",
);
assert.deepEqual(
  watchLiveEntryTarget({ kind: "replay", replayId: "TOWN-20260809", startTick: 18 }),
  { kind: "replay", replayId: "TOWN-20260809", startTick: 18 },
  "replay entries must reuse the existing replay watcher path with its bounded start",
);
assert.equal(watchLiveEntryTarget({ kind: "replay" }), null);
assert.equal(watchLiveEntryTarget({ kind: "live" }), null);

console.log("watch_live.test.mjs: live channel helpers passed");
