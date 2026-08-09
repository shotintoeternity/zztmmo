import assert from "node:assert/strict";
import { build } from "esbuild";

// M19.3 — "a signed-in player's stored color wins over localStorage; a guest
// keeps localStorage only", as pure logic under Node (the resume.ts pattern).
// The server half is proved in engine/m19_3_test.go; what is proved here is the
// decision this browser makes about which of two answers to believe, and the
// two requests it makes to get one of them.

const output = await build({
  entryPoints: ["src/preferences.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { effectivePlayerColor, effectiveComfort, fetchAccountPreferences, saveAccountColor, saveAccountHint, saveAccountProfile, saveShareLocationWithFollowers, saveFavoriteWorld, saveAccountComfort } = await import(
  `data:text/javascript;base64,${source}`
);

const emptyProfile = { handle: "", displayName: "", about: [] };
const defaultComfort = { keyPreset: "vanilla", keyBindings: {}, reduceFlashing: false, palette: "vanilla" };
const defaultPrefs = (overrides) => ({
  authenticated: true,
  stored: true,
  color: "",
  hints: { players: false, death: false, chat: false },
  profile: emptyProfile,
  shareLocationWithFollowers: false,
  favoriteWorlds: [],
  comfort: defaultComfort,
  ...overrides,
});

// --- which color this browser wears --------------------------------------

{
  // The guest case: no account, so the browser's own pick is all there is.
  assert.equal(effectivePlayerColor({ account: null, local: "#ff0000" }), "#ff0000");
  assert.equal(effectivePlayerColor({ account: null, local: "" }), "");
  // Junk in localStorage is not a color; it must not reach a fillStyle.
  assert.equal(effectivePlayerColor({ account: null, local: "red;background:url(x)" }), "");
}

{
  // The headline rule: a stored account color beats whatever this browser has,
  // which is what makes it the same player in the next browser.
  const account = { authenticated: true, stored: true, color: "#00ff00" };
  assert.equal(effectivePlayerColor({ account, local: "#ff0000" }), "#00ff00");
  assert.equal(effectivePlayerColor({ account, local: "" }), "#00ff00");
}

{
  // The case that makes "No color" mean something: a document that EXISTS with
  // an empty color is the deliberate vanilla player, so a pick this browser
  // still holds must not put the color back on.
  const account = { authenticated: true, stored: true, color: "" };
  assert.equal(effectivePlayerColor({ account, local: "#ff0000" }), "");
}

{
  // A signed-in player who has never chosen anything has no document, so their
  // browser's pick still applies — and the server adopts it at the next join.
  const account = { authenticated: true, stored: false, color: "" };
  assert.equal(effectivePlayerColor({ account, local: "#ff0000" }), "#ff0000");
  // An unauthenticated document is not a document at all.
  assert.equal(
    effectivePlayerColor({ account: { authenticated: false, stored: true, color: "#00ff00" }, local: "#ff0000" }),
    "#ff0000",
  );
}

// --- reading the account --------------------------------------------------

function stubFetch(handler) {
  const calls = [];
  const fetchFn = async (url, init) => {
    calls.push({ url, init });
    return handler(url, init);
  };
  return { fetchFn, calls };
}

function jsonResponse(body, { ok = true, status = 200 } = {}) {
  return { ok, status, json: async () => body };
}

{
  const { fetchFn, calls } = stubFetch(() => jsonResponse({ authenticated: true, stored: true, color: "#a1b2c3" }));
  const prefs = await fetchAccountPreferences(fetchFn);
  assert.deepEqual(prefs, defaultPrefs({ color: "#a1b2c3" }));
  assert.equal(calls[0].url, "/api/preferences");
  assert.equal(calls[0].init, undefined, "the read is a plain GET");
}

{
  // A guest, a failed request and a server error all answer null rather than a
  // blank document: null means "we do not know", and only a document may claim
  // this account wants no color.
  const guest = await fetchAccountPreferences(stubFetch(() => jsonResponse({ authenticated: false })).fetchFn);
  assert.equal(guest, null);
  const failed = await fetchAccountPreferences(stubFetch(() => jsonResponse({}, { ok: false, status: 500 })).fetchFn);
  assert.equal(failed, null);
  const thrown = await fetchAccountPreferences(
    stubFetch(() => {
      throw new Error("offline");
    }).fetchFn,
  );
  assert.equal(thrown, null);
}

{
  // A color the server would never have sent is not trusted just because it
  // arrived over the account's own endpoint.
  const prefs = await fetchAccountPreferences(
    stubFetch(() => jsonResponse({ authenticated: true, stored: true, color: "not-a-color" })).fetchFn,
  );
  assert.deepEqual(prefs, defaultPrefs({ color: "" }));
}

{
  // Only known boolean hint fields are trusted; garbage loads as "not seen".
  const prefs = await fetchAccountPreferences(
    stubFetch(() => jsonResponse({ authenticated: true, stored: true, hints: { players: true, death: "yes", chat: true } })).fetchFn,
  );
  assert.deepEqual(prefs.hints, { players: true, death: false, chat: true });
}

{
  const prefs = await fetchAccountPreferences(
    stubFetch(() => jsonResponse({
      authenticated: true,
      stored: true,
      profile: { handle: "ada", displayName: "Ada", about: ["First line", 7, "Second line"] },
      shareLocationWithFollowers: true,
      favoriteWorlds: ["ALPHA", 7, "BETA"],
      comfort: { keyPreset: "custom", keyBindings: { up: ["KeyI"], torch: ["KeyO"] }, reduceFlashing: true, palette: "colorblind-assist" },
    })).fetchFn,
  );
  assert.deepEqual(prefs.profile, { handle: "ada", displayName: "Ada", about: ["First line", "Second line"] });
  assert.equal(prefs.shareLocationWithFollowers, true);
  assert.deepEqual(prefs.favoriteWorlds, ["ALPHA", "BETA"]);
  assert.deepEqual(prefs.comfort, {
    keyPreset: "custom",
    keyBindings: { up: ["KeyI"], torch: ["KeyO"] },
    reduceFlashing: true,
    palette: "colorblind-assist",
  });
}

// --- writing the account --------------------------------------------------

{
  const { fetchFn, calls } = stubFetch((_url, init) => jsonResponse({
    authenticated: true,
    stored: true,
    color: JSON.parse(init.body).color,
  }));
  const saved = await saveAccountColor(fetchFn, "#0088ff");
  assert.deepEqual(saved, defaultPrefs({ color: "#0088ff" }));
  assert.equal(calls[0].init.method, "PUT");
  assert.deepEqual(JSON.parse(calls[0].init.body), { color: "#0088ff" });
}

{
  // "No color" is said out loud — an empty color is sent, not omitted — because
  // a request that leaves the field out is indistinguishable from a browser
  // that never had one, and the account would keep the old color forever.
  const { fetchFn, calls } = stubFetch(() => jsonResponse({ authenticated: true, stored: true }));
  const saved = await saveAccountColor(fetchFn, "");
  assert.deepEqual(JSON.parse(calls[0].init.body), { color: "" });
  assert.deepEqual(saved, defaultPrefs({ color: "" }));
}

{
  // A refused or failed write answers null so the caller keeps holding what it
  // has rather than adopting a document the server never wrote.
  const refused = await saveAccountColor(stubFetch(() => jsonResponse({}, { ok: false, status: 401 })).fetchFn, "#ff0000");
  assert.equal(refused, null);
  const thrown = await saveAccountColor(
    stubFetch(() => {
      throw new Error("offline");
    }).fetchFn,
    "#ff0000",
  );
  assert.equal(thrown, null);
}

{
  const { fetchFn, calls } = stubFetch((_url, init) => jsonResponse({
    authenticated: true,
    stored: true,
    color: "#0088ff",
    hints: JSON.parse(init.body).hints,
  }));
  const saved = await saveAccountHint(fetchFn, "chat");
  assert.deepEqual(JSON.parse(calls[0].init.body), { hints: { chat: true } });
  assert.deepEqual(saved.hints, { players: false, death: false, chat: true });
}

{
  const profile = { handle: "ada", displayName: "Ada", about: ["Hi"] };
  const { fetchFn, calls } = stubFetch((_url, init) => jsonResponse({
    authenticated: true,
    stored: true,
    profile: JSON.parse(init.body).profile,
  }));
  const saved = await saveAccountProfile(fetchFn, profile);
  assert.deepEqual(JSON.parse(calls[0].init.body), { profile });
  assert.deepEqual(saved.profile, profile);
}

{
  const { fetchFn, calls } = stubFetch((_url, init) => jsonResponse({
    authenticated: true,
    stored: true,
    shareLocationWithFollowers: JSON.parse(init.body).shareLocationWithFollowers,
  }));
  const saved = await saveShareLocationWithFollowers(fetchFn, true);
  assert.deepEqual(JSON.parse(calls[0].init.body), { shareLocationWithFollowers: true });
  assert.equal(saved.shareLocationWithFollowers, true);
}

{
  const { fetchFn, calls } = stubFetch((_url, init) => jsonResponse({
    authenticated: true,
    stored: true,
    favoriteWorlds: [JSON.parse(init.body).favoriteWorld],
  }));
  const saved = await saveFavoriteWorld(fetchFn, "ALPHA", true);
  assert.deepEqual(JSON.parse(calls[0].init.body), { favoriteWorld: "ALPHA", favorite: true });
  assert.deepEqual(saved.favoriteWorlds, ["ALPHA"]);
}

{
  const comfort = { keyPreset: "custom", keyBindings: { up: ["KeyI"], torch: ["KeyO"] }, reduceFlashing: true, palette: "high-contrast" };
  const { fetchFn, calls } = stubFetch((_url, init) => jsonResponse({
    authenticated: true,
    stored: true,
    color: "#112233",
    comfort: JSON.parse(init.body).comfort,
  }));
  const saved = await saveAccountComfort(fetchFn, comfort);
  assert.deepEqual(JSON.parse(calls[0].init.body), { comfort });
  assert.deepEqual(saved.comfort, comfort);
  assert.equal(saved.color, "#112233");
}

{
  const local = { keyPreset: "one-handed", keyBindings: {}, reduceFlashing: true, palette: "colorblind-assist" };
  assert.deepEqual(effectiveComfort({ account: null, local }), local);
  assert.deepEqual(effectiveComfort({ account: { authenticated: true, stored: true, comfort: defaultComfort }, local }), defaultComfort);
  assert.deepEqual(effectiveComfort({ account: { authenticated: true, stored: false, comfort: defaultComfort }, local }), local);
}

console.log("preferences.test.mjs: ok");
