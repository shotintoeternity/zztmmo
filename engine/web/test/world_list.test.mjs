import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({ entryPoints: ["src/world_list.ts"], bundle: true, format: "esm", platform: "node", write: false });
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { fetchWorldList } = await import(`data:text/javascript;base64,${source}`);

const reply = (body, { ok = true, status = 200 } = {}) => async () => ({ ok, status, json: async () => body });

// An OK reply is the list and the shelves, normalized.
{
  const got = await fetchWorldList(reply({ worlds: ["TOWN (3 players)", { world: "CAVES", title: "Caves" }], shelves: [{ id: "new", title: "New", worlds: ["CAVES"] }] }));
  assert.deepEqual(got.entries.map((e) => e.world), ["TOWN", "CAVES"]);
  assert.equal(got.entries[1].title, "Caves");
  assert.deepEqual(got.shelves, [{ id: "new", title: "New", worlds: ["CAVES"] }]);
}

// M40.6: a refusal with a JSON body is not an empty world list. Before, a 503
// carrying {"error": ...} became "There are no ZZT worlds."
await assert.rejects(fetchWorldList(reply({ error: "unavailable" }, { ok: false, status: 503 })), /503/);

// A network failure still rejects, as it always did.
await assert.rejects(fetchWorldList(async () => { throw new Error("offline"); }), /offline/);

console.log("world_list.test.mjs: ok");
