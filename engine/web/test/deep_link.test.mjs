import assert from "node:assert/strict";
import { build } from "esbuild";

// M20.1 — the /play/<world> deep link's pure rules, under Node (the
// title_flow.ts / preferences.ts pattern). What the browser does with these
// answers — the title-screen pause, the refusal window, the address bar — is
// proved against a real Chromium in web/test/deep_link_journey.test.mjs.
//
// The rule these tests exist to pin: a link names a world the way the JOIN path
// names it. /api/worlds is the authority (M18.13), so resolution is a lookup in
// that list and never a string handed to the server as typed.

const output = await build({
  entryPoints: ["src/deep_link.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { deepLinkWorldName, deepLinkPath, watchLinkWorldName, watchLinkPath, replayLinkID, replayLinkPath, resolveDeepLinkWorld, deepLinkRefusalLines } = await import(
  `data:text/javascript;base64,${source}`
);

// --- which paths are deep links at all -----------------------------------

{
  assert.equal(deepLinkWorldName("/play/ACCEPT"), "ACCEPT");
  assert.equal(deepLinkWorldName("/play/town"), "town");
  // A tidied trailing slash is the same link.
  assert.equal(deepLinkWorldName("/play/TOWN/"), "TOWN");
  // Percent-escapes round-trip, so a name with a space survives a copy-paste.
  assert.equal(deepLinkWorldName("/play/MY%20WORLD"), "MY WORLD");
  assert.equal(watchLinkWorldName("/watch/ACCEPT"), "ACCEPT");
  assert.equal(watchLinkWorldName("/watch/town"), "town");
  assert.equal(watchLinkWorldName("/watch/TOWN/"), "TOWN");
  assert.equal(watchLinkWorldName("/watch/MY%20WORLD"), "MY WORLD");
  assert.equal(replayLinkID("/replay/TOWN-20260807-120000"), "TOWN-20260807-120000");
  assert.equal(replayLinkID("/replay/session_one/"), "session_one");
}

{
  // Not deep links: the app root, the bare prefix, and anything else the SPA
  // fallback serves. Each opens the picker, which is the pre-M20.1 behaviour.
  for (const path of ["/", "", "/play", "/play/", "/play///", "/watch/TOWN", "/playground/TOWN"]) {
    assert.equal(deepLinkWorldName(path), "", `${JSON.stringify(path)} must not be a deep link`);
  }
  for (const path of ["/", "", "/watch", "/watch/", "/watch///", "/play/TOWN", "/watchtower/TOWN"]) {
    assert.equal(watchLinkWorldName(path), "", `${JSON.stringify(path)} must not be a watch link`);
  }
  for (const path of ["/", "", "/replay", "/replay/", "/replay///", "/watch/TOWN", "/replayer/TOWN"]) {
    assert.equal(replayLinkID(path), "", `${JSON.stringify(path)} must not be a replay link`);
  }
}

{
  // A malformed escape is still a link the player typed: it comes back as text
  // so they can be told, rather than vanishing into the picker.
  assert.equal(deepLinkWorldName("/play/%E0%A4%A"), "%E0%A4%A");
  assert.equal(watchLinkWorldName("/watch/%E0%A4%A"), "%E0%A4%A");
  assert.equal(replayLinkID("/replay/%E0%A4%A"), "%E0%A4%A");
}

// --- resolution: the join path's identity, not the URL's spelling ---------

const listing = [{ world: "ACCEPT" }, { world: "TOWN" }, { world: "CAVES" }];

{
  // The headline claim: one world, whichever case the link was written in, and
  // the value returned is always the LISTED name.
  assert.equal(resolveDeepLinkWorld("TOWN", listing), "TOWN");
  assert.equal(resolveDeepLinkWorld("town", listing), "TOWN");
  assert.equal(resolveDeepLinkWorld("ToWn", listing), "TOWN");
  assert.equal(resolveDeepLinkWorld("  town  ", listing), "TOWN");
}

{
  // A name nothing answers to resolves to nothing — never to the first entry,
  // never to a default. main.ts refuses on "", so this is what stops a dead
  // link falling through to Untitled.
  assert.equal(resolveDeepLinkWorld("NOSUCH", listing), "");
  assert.equal(resolveDeepLinkWorld("", listing), "");
  assert.equal(resolveDeepLinkWorld("TOWN", []), "");
  // A deeper path is not a world name, and is refused the same way.
  assert.equal(resolveDeepLinkWorld("TOWN/1", listing), "");
  // Nor is a filename: the picker's identity is the bare name (M18.13).
  assert.equal(resolveDeepLinkWorld("TOWN.ZZT", listing), "");
}

// --- the shareable address -----------------------------------------------

{
  assert.equal(deepLinkPath("TOWN"), "/play/TOWN");
  assert.equal(deepLinkPath("MY WORLD"), "/play/MY%20WORLD");
  assert.equal(watchLinkPath("TOWN"), "/watch/TOWN");
  assert.equal(watchLinkPath("MY WORLD"), "/watch/MY%20WORLD");
  assert.equal(replayLinkPath("TOWN-20260807-120000"), "/replay/TOWN-20260807-120000");
  assert.equal(replayLinkPath("session one"), "/replay/session%20one");
  // Round trip: what the address bar shows resolves back to the same world.
  for (const name of ["TOWN", "MY WORLD", "A+B"]) {
    assert.equal(deepLinkWorldName(deepLinkPath(name)), name);
    assert.equal(watchLinkWorldName(watchLinkPath(name)), name);
    assert.equal(replayLinkID(replayLinkPath(name)), name);
  }
}

// --- the refusal window ---------------------------------------------------

{
  const lines = deepLinkRefusalLines("NOSUCH");
  assert.ok(
    lines.some((l) => l.includes("NOSUCH")),
    `the refusal must name the world that failed: ${JSON.stringify(lines)}`,
  );
  assert.ok(
    lines.some((l) => l.toLowerCase().includes("choose a world")),
    `the refusal must point at the picker: ${JSON.stringify(lines)}`,
  );
  // Every line has to fit the CP437 window's inner span, or it bleeds into the
  // sidebar — including one built from a name a link can make arbitrarily long.
  for (const line of deepLinkRefusalLines("X".repeat(300), "the server did not answer")) {
    assert.ok(line.length <= 42, `refusal line too wide: ${JSON.stringify(line)}`);
  }
}

{
  // The reason is optional and only shown when there is one to show.
  assert.equal(deepLinkRefusalLines("NOSUCH").some((l) => l.includes("(")), false);
  assert.ok(deepLinkRefusalLines("NOSUCH", "the server did not answer").some((l) => l.includes("(the server")));
}

console.log("deep_link.test.mjs: /play/<world> parsing, join-path resolution, address and refusal passed");
