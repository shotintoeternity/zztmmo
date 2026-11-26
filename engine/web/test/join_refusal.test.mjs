import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/join_refusal.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { joinRefusalFrom } = await import(`data:text/javascript;base64,${source}`);

// The server's refusal, as websocket_server.go joinRefusedMessage sends it.
const crowded = {
  type: "event",
  event: {
    type: "scroll",
    statId: -1,
    playerStatId: -1,
    title: "Start crowded",
    lines: ["", "  Players are standing on and", "  around this board's start.", "  Try again in a moment."],
  },
};

// 1. Before the first snapshot, an ownerless scroll is the refusal.
assert.deepEqual(joinRefusalFrom(crowded, false), { title: "Start crowded", lines: crowded.event.lines });

// 2. After it, the same shape is an object speaking to the board, not a refusal.
assert.equal(joinRefusalFrom(crowded, true), null);

// 3. A scroll somebody owns, or one an object opened, is never a refusal.
assert.equal(joinRefusalFrom({ ...crowded, event: { ...crowded.event, playerStatId: 0 } }, false), null);
assert.equal(joinRefusalFrom({ ...crowded, event: { ...crowded.event, statId: 4 } }, false), null);

// 4. Anything that is not a scroll event is not one either.
assert.equal(joinRefusalFrom({ type: "snapshot" }, false), null);
assert.equal(joinRefusalFrom({ type: "event", event: { type: "transfer" } }, false), null);

console.log("join_refusal: ok");
