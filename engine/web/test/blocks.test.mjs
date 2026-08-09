import assert from "node:assert/strict";
import { build } from "esbuild";

// M21.1 — who the block window offers, and what its rows say, under Node (the
// preferences.ts / deep_link.ts pattern). The block itself is enforced on the
// SERVER, at the chat fan-out, and engine/m21_1_test.go is what proves that; what
// is proved here is the list a player chooses from.
//
// The two rules these tests exist to pin:
//   1. Every row is addressable. A display name is not — names are neither
//      unique nor claimed — so a candidate with no PlayerID is not offered at
//      all, and two people with one name are two rows.
//   2. The roster alone is not enough. Global chat crosses boards and worlds
//      while the roster is per-board, so the person who just said something from
//      elsewhere has to be offerable too.

const output = await build({
  entryPoints: ["src/blocks.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { blockCandidates, blockRowLabel, blockWindowHeader, mergeServerBlocks } = await import(
  `data:text/javascript;base64,${source}`
);

const none = new Set();

// --- who is offered -------------------------------------------------------

{
  // The roster's people come first, in arrival (id) order, and never yourself.
  const candidates = blockCandidates({
    roster: [
      { id: 3, name: "Cy" },
      { id: 1, name: "Me" },
      { id: 2, name: "Bob" },
    ],
    chat: [],
    blocked: none,
    self: 1,
  });
  assert.deepEqual(
    candidates.map((c) => c.id),
    [2, 3],
    "the roster is offered in id order, minus yourself",
  );
  assert.ok(candidates.every((c) => c.here), "roster entries are here");
}

{
  // A chat sender who is NOT on this board is still offerable — the whole reason
  // the roster alone will not do.
  const candidates = blockCandidates({
    roster: [{ id: 1, name: "Me" }],
    chat: [
      { from: "Far", playerId: 8, text: "hi" },
      { from: "Me", playerId: 1, text: "hi back" },
    ],
    blocked: none,
    self: 1,
  });
  assert.deepEqual(
    candidates.map((c) => [c.id, c.here]),
    [[8, false]],
    "a chat sender from another board is offered, and marked as elsewhere",
  );
}

{
  // The roster's name wins where both know one, and there is one row per person.
  const candidates = blockCandidates({
    roster: [{ id: 4, name: "Roster Name" }],
    chat: [
      { from: "Chat Name", playerId: 4, text: "a" },
      { from: "Chat Name", playerId: 4, text: "b" },
    ],
    blocked: none,
    self: 1,
  });
  assert.equal(candidates.length, 1, "one row per player, not one per line");
  assert.equal(candidates[0].name, "Roster Name");
  assert.equal(candidates[0].here, true);
}

{
  // Newest first among chat-only senders: a long backlog must not bury the
  // person who just spoke.
  const chat = [
    { from: "First", playerId: 10, text: "old" },
    { from: "Second", playerId: 11, text: "newer" },
    { from: "Third", playerId: 12, text: "newest" },
  ];
  const candidates = blockCandidates({ roster: [], chat, blocked: none, self: 1 });
  assert.deepEqual(candidates.map((c) => c.id), [12, 11, 10]);
}

{
  // An unaddressable line — an older server sends no playerId — is not offered.
  // Blocking it would have to guess, and the guess is somebody real.
  const candidates = blockCandidates({
    roster: [],
    chat: [{ from: "Ghost", text: "who am I" }, { from: "Zero", playerId: 0, text: "nor I" }],
    blocked: none,
    self: 1,
  });
  assert.deepEqual(candidates, [], "a line with no id offers no row");
}

{
  // Two players may share a display name. They are two rows, and the label is
  // what keeps them apart.
  const candidates = blockCandidates({
    roster: [{ id: 5, name: "bob" }, { id: 6, name: "bob" }],
    chat: [],
    blocked: none,
    self: 1,
  });
  const labels = candidates.map(blockRowLabel);
  assert.equal(new Set(labels).size, 2, `two players named bob need two labels: ${JSON.stringify(labels)}`);
}

// --- what the rows say ----------------------------------------------------

{
  const [bob] = blockCandidates({
    roster: [{ id: 2, name: "Bob" }],
    chat: [],
    blocked: new Set([2]),
    self: 1,
  });
  assert.equal(bob.blocked, true);
  assert.ok(blockRowLabel(bob).includes("[blocked]"), "an already-blocked row must say so");
  assert.ok(blockRowLabel(bob).includes("#2"), "the row carries the address, not just the name");
}

{
  const [ada] = blockCandidates({
    roster: [{ id: 2, name: "Ada Lovelace", handle: "ada", hasProfile: true }],
    chat: [],
    blocked: none,
    self: 1,
  });
  assert.equal(blockRowLabel(ada), "@ada #2 [profile]");
}

{
  const [far] = blockCandidates({
    roster: [],
    chat: [{ from: "Far", playerId: 9, text: "hi" }],
    blocked: none,
    self: 1,
  });
  assert.ok(blockRowLabel(far).includes("(elsewhere)"), "a candidate who is not here says so");
}

{
  // A nameless player still gets a row: the id is the address, and the name was
  // never the thing that mattered.
  const [anon] = blockCandidates({ roster: [{ id: 7 }], chat: [], blocked: none, self: 1 });
  assert.equal(anon.name, "player");
  assert.ok(blockRowLabel(anon).includes("#7"));
}

{
  // Every row fits the window, whatever a player types as a name.
  const [long] = blockCandidates({
    roster: [{ id: 12345, name: "N".repeat(200) }],
    chat: [],
    blocked: new Set([12345]),
    self: 1,
  });
  assert.ok(blockRowLabel(long).length <= 40, `row too wide: ${blockRowLabel(long).length}`);
}

// --- the header -----------------------------------------------------------

{
  const empty = blockWindowHeader([]);
  assert.ok(empty.join(" ").includes("Nobody else"), "an empty list must still explain itself");
  const some = blockWindowHeader([{ id: 2, name: "Bob", here: true, blocked: false }]);
  assert.ok(
    some.join(" ").toUpperCase().includes("YOU"),
    "the header must say a block only changes what YOU hear",
  );
  for (const line of [...empty, ...some]) {
    assert.ok(line.length <= 42, `header line too wide: ${JSON.stringify(line)}`);
  }
}

// --- what the join snapshot tells us (M21.4) ------------------------------
//
// The mirror used to start empty on every connection, so a durable block made
// last week showed as unmarked. The server now names the blocked people among
// the roster it is already sending; what is pinned here is that the client folds
// that in WITHOUT ever letting it take a mark away.

{
  // The headline: a returning player's row reads "[blocked]" on the first frame,
  // having done nothing at all this session.
  const blocked = mergeServerBlocks(new Set(), [2]);
  const [bob] = blockCandidates({
    roster: [{ id: 2, name: "Bob" }],
    chat: [],
    blocked,
    self: 1,
  });
  assert.equal(bob.blocked, true, "a block the server reported must mark its row");
  assert.ok(blockRowLabel(bob).includes("[blocked]"));
}

{
  // Additive, never authoritative. The server's list covers only the players in
  // that snapshot, so an id it omits means "not in your roster" — reading it as
  // "not blocked" would unmark somebody who had walked off the board.
  const merged = mergeServerBlocks(new Set([7]), [2]);
  assert.deepEqual([...merged].sort((a, b) => a - b), [2, 7]);
}

{
  // An absent list changes nothing: a board-change snapshot carries no answer,
  // and neither does an older server.
  const merged = mergeServerBlocks(new Set([7]), undefined);
  assert.deepEqual([...merged], [7], "a snapshot with no list must not clear the mirror");
  assert.deepEqual([...mergeServerBlocks(new Set([7]), [])], [7]);
}

{
  // Id 0 addresses nobody. Admitting it would mark every row the window builds
  // from a chat line whose id the server cleared.
  assert.deepEqual([...mergeServerBlocks(new Set(), [0, 3])], [3]);
}

{
  // The input set is not mutated: main.ts reassigns, and a function that also
  // wrote through would make the two disagree about which one is the mirror.
  const before = new Set([1]);
  mergeServerBlocks(before, [9]);
  assert.deepEqual([...before], [1], "mergeServerBlocks must not write through its argument");
}

console.log("blocks.test.mjs: candidates, addressability, row labels, header and server merge passed");
