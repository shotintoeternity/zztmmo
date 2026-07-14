import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle help.ts + modal.ts together so the M5.12 help-navigation flow can be
// driven end to end without a DOM: openHelp builds a text modal, and
// handleModalKey routes Enter/Escape through it exactly as the browser does.
const output = await build({
  entryPoints: ["src/help.ts", "src/modal.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
  outdir: "out",
});
async function load(name) {
  const file = output.outputFiles.find((f) => f.path.endsWith(name));
  const source = Buffer.from(file.contents).toString("base64");
  return import(`data:text/javascript;base64,${source}`);
}
const { openHelp, helpFileFor } = await load("help.js");
const { handleModalKey } = await load("modal.js");

function key(code, k = "", opts = {}) {
  return { code, key: k, ctrlKey: false, metaKey: false, altKey: false, ...opts };
}

// A tiny stand-in for the real help graph. EDITOR.HLP cross-links to CREATURE.HLP
// via "!-creature" and jumps within itself via "!cmds" → ":cmds".
const FILES = {
  "EDITOR.HLP": [
    "$The ZZT Editor",
    "",
    "!cmds;Editor Commands",
    "!-creature;Creatures",
    "",
    ":cmds;Editing commands:",
    "[L] Load a world.",
  ],
  "CREATURE.HLP": ["$Creatures", "", "A lion chases you."],
};

function makeDeps() {
  const opened = [];
  const requested = [];
  const deps = {
    fetchLines: async (file) => {
      requested.push(file);
      const lines = FILES[file];
      if (!lines) {
        throw new Error("404");
      }
      return lines;
    },
    openModal: (modal) => {
      opened.push(modal);
    },
  };
  return { deps, opened, requested };
}

// Wait for the openHelp fetch chain (a couple of microtasks) to settle.
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

// helpFileFor uppercases and appends .HLP so a case-sensitive server resolves it.
assert.equal(helpFileFor("creature"), "CREATURE.HLP");
assert.equal(helpFileFor("langtut"), "LANGTUT.HLP");

// Full flow: open EDITOR.HLP, follow "!-creature" into CREATURE.HLP, and back.
{
  const { deps, opened, requested } = makeDeps();
  openHelp("EDITOR.HLP", "World editor help", deps);
  await tick();
  assert.deepEqual(requested, ["EDITOR.HLP"]);
  let m = opened.at(-1);
  assert.equal(m.kind, "text");
  assert.equal(m.selectable, false, "help windows never send scroll replies");
  assert.equal(m.state.viewingFile, true);
  assert.equal(m.onBack, undefined, "the root has no back path");

  // In-file jump: move onto "!cmds" and press Enter → scroll to the ":cmds" line.
  m.state.linePos = 3; // "!cmds;Editor Commands"
  assert.ok(m.state.lines[m.state.linePos - 1].startsWith("!cmds"));
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "redraw");
  assert.ok(
    m.state.lines[m.state.linePos - 1].startsWith(":cmds"),
    "a bare !label jumps to its :label line without closing",
  );

  // Cross-file: move onto "!-creature" and press Enter → open CREATURE.HLP.
  m.state.linePos = 4; // "!-creature;Creatures"
  assert.ok(m.state.lines[m.state.linePos - 1].startsWith("!-creature"));
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "redraw");
  await tick();
  assert.deepEqual(requested, ["EDITOR.HLP", "CREATURE.HLP"]);
  m = opened.at(-1);
  assert.match(m.state.lines.join(" "), /A lion chases you/);
  assert.equal(m.state.title, "World editor help", "title stays constant, as vanilla never resets it");
  assert.equal(typeof m.onBack, "function", "a sub-file has a back path");

  // Escape from the sub-file returns to EDITOR.HLP rather than closing.
  assert.equal(handleModalKey(m, key("Escape", "Escape")), "redraw");
  m = opened.at(-1);
  assert.match(m.state.lines.join(" "), /The ZZT Editor/);
  assert.equal(m.onBack, undefined, "back at the root: Escape now closes");

  // Escape at the root closes the window.
  assert.equal(handleModalKey(m, key("Escape", "Escape")), "close");
}

// A missing help target surfaces a window, not a dead link.
{
  const { deps, opened } = makeDeps();
  openHelp("EDITOR.HLP", "World editor help", deps);
  await tick();
  let m = opened.at(-1);
  m.state.linePos = 4; // "!-creature", but we break the graph below
  deps.fetchLines = async () => {
    throw new Error("404");
  };
  m.onOpenFile("nope"); // NOPE.HLP does not exist
  await tick();
  m = opened.at(-1);
  assert.match(m.state.lines.join(" "), /Not available/);
}

// Enter on ordinary text (no link) closes the help window, as vanilla breaks on
// ENTER for a non-hyperlink line.
{
  const { deps, opened } = makeDeps();
  openHelp("EDITOR.HLP", "World editor help", deps);
  await tick();
  const m = opened.at(-1);
  m.state.linePos = 7; // "[L] Load a world."
  assert.equal(handleModalKey(m, key("Enter", "Enter")), "close");
}

console.log("help.test.mjs: all assertions passed");
