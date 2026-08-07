import assert from "node:assert/strict";
import { build } from "esbuild";

// M22.1 — the read-only client's pure half: the join a watcher sends, and the
// sidebar a watcher gets instead of a player's.
//
// Both are DOM-free by construction (sidebar.ts writes through an injected
// WriteText, exactly as the engine's VideoWriteText is written through), so the
// claims below are checked here under Node rather than in a browser.

const bundle = async (entry) => {
  const output = await build({
    entryPoints: [entry],
    bundle: true,
    format: "esm",
    platform: "node",
    write: false,
  });
  return import(`data:text/javascript;base64,${Buffer.from(output.outputFiles[0].contents).toString("base64")}`);
};

const { buildWatchMessage, buildJoinMessage } = await bundle("src/resume.ts");
const { drawWatchSidebar, watchersLine, drawSidebar } = await bundle("src/sidebar.ts");

// ---------------------------------------------------------------------------
// The join
// ---------------------------------------------------------------------------

// A watch join says what it is and claims nothing else. The absences are the
// assertions: a name and a color describe a smiley this connection will not
// have, and a resume token names a run it was never given.
const watch = buildWatchMessage("join");
assert.deepEqual(watch, { type: "join", spectate: true });
assert.equal("name" in watch, false, "a watcher named itself");
assert.equal("color" in watch, false, "a watcher picked a color for a smiley it has not got");
assert.equal("resumeToken" in watch, false, "a watcher offered a token for a run it never had");

// And a player's join is untouched by it — the two are separate messages, so a
// future field on one cannot leak into the other.
assert.equal("spectate" in buildJoinMessage("join", "browser", "tok", "#a1b2c3"), false);

// ---------------------------------------------------------------------------
// The count
// ---------------------------------------------------------------------------

assert.equal(watchersLine(0), "0 watching");
assert.equal(watchersLine(1), "1 watching");
assert.equal(watchersLine(12), "12 watching");
// A negative count is a malformed frame, not a negative number of people.
assert.equal(watchersLine(-3), "0 watching");

// ---------------------------------------------------------------------------
// The sidebar
// ---------------------------------------------------------------------------

// A recording WriteText, so what the sidebar draws can be read back as text.
function recorder() {
  const rows = Array.from({ length: 25 }, () => Array.from({ length: 80 }, () => " "));
  const write = (x, y, color, text) => {
    for (let i = 0; i < text.length; i += 1) {
      if (x + i >= 0 && x + i < 80 && y >= 0 && y < 25) {
        rows[y][x + i] = text[i];
      }
    }
  };
  write.row = (y) => rows[y].join("");
  write.all = () => rows.map((r) => r.join("")).join("\n");
  return write;
}

const watcher = recorder();
drawWatchSidebar(watcher, 3);
const watcherText = watcher.all();

// Row 7 is the health row. A watcher has no health, and the row says what is
// true instead of standing empty — an empty row reads as a player with nothing
// left, which is the opposite of what is happening.
assert.ok(watcher.row(7).includes("Watching"), `row 7 is ${JSON.stringify(watcher.row(7))}`);
assert.ok(watcherText.includes("3 watching"), "the count is not on the sidebar");
assert.ok(watcher.row(23).includes("Leave"), "a watcher has no way out");

// The affordances a watcher must NOT be offered. Each of these is a control
// whose key the server would drop or that names state a watcher has not got, so
// showing it would be a button that lies.
const player = recorder();
drawSidebar(player);
const playerText = player.all();
for (const gone of ["Health:", "Ammo:", "Torches:", "Gems:", "Score:", "Keys:", "Shoot", "Move", "Save game", "Chat", "Players", "Torch", "Pause"]) {
  assert.ok(playerText.includes(gone), `the player's sidebar should have ${gone} — the fixture is wrong`);
  assert.ok(!watcherText.includes(gone), `a watcher was offered ${gone}`);
}

// The banner is kept: a watcher is still looking at this game.
assert.ok(watcherText.includes("ZZTMMO"));

// The sidebar redraws from the count alone, so a watcher arriving or leaving
// changes the line and nothing else.
const one = recorder();
drawWatchSidebar(one, 1);
assert.ok(one.all().includes("1 watching"));
assert.ok(!one.all().includes("3 watching"));

// A live watcher does not get Share: there is no recording id to render. A
// replay watcher does, and it is the one extra action M22.4 adds to the
// read-only sidebar.
assert.ok(!watcherText.includes("Share"), "a live watcher was offered replay sharing");
const replay = recorder();
drawWatchSidebar(replay, 2, true);
assert.ok(replay.all().includes("Share"), "a replay watcher was not offered Share");
assert.ok(replay.row(21).includes(" S "), "the Share action must name its key");

console.log("watch.test.mjs: all assertions passed");
