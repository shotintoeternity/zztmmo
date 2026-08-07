import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/first_time_hints.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const {
  FIRST_TIME_HINTS,
  hintAlreadySeen,
  loadGuestHints,
  normalizeHints,
  saveGuestHint,
} = await import(`data:text/javascript;base64,${source}`);

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => data.set(key, value),
  };
}

assert.equal(FIRST_TIME_HINTS.players, "That other face is a real person - C chats");
assert.equal(FIRST_TIME_HINTS.death, "You respawn. Your things stay yours.");
assert.equal(FIRST_TIME_HINTS.chat, "C opens chat. Someone just spoke.");

assert.deepEqual(normalizeHints(null), { players: false, death: false, chat: false });
assert.deepEqual(normalizeHints({ players: true, death: "true", chat: true }), {
  players: true,
  death: false,
  chat: true,
});
assert.equal(hintAlreadySeen({ chat: true }, "chat"), true);
assert.equal(hintAlreadySeen({ chat: true }, "death"), false);

{
  const storage = memoryStorage();
  assert.deepEqual(loadGuestHints(storage), { players: false, death: false, chat: false });
  saveGuestHint(storage, "players");
  assert.equal(loadGuestHints(storage).players, true);
  saveGuestHint(storage, "players");
  assert.deepEqual(loadGuestHints(storage), { players: true, death: false, chat: false });
}

{
  const storage = memoryStorage({ "zztmmo.firstTimeHints": "not json" });
  assert.deepEqual(loadGuestHints(storage), { players: false, death: false, chat: false });
}

console.log("first_time_hints.test.mjs: ok");
