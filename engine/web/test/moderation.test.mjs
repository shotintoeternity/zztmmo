import assert from "node:assert/strict";
import { build } from "esbuild";

// M21.2 — what the Players window offers an operator, under Node (the blocks.ts
// pattern). Every one of these actions is performed and authorized on the
// SERVER; engine/m21_2_test.go is what proves that. What is proved here is the
// menu, and the two things about it that are easy to get quietly wrong:
//
//   1. A non-operator's window must be unchanged. M21.1 shipped a straight
//      pick-a-player-then-yes/no flow, and an extra level for everybody would be
//      charging every player a keystroke for a power almost none of them have.
//   2. Refuse must not promise more than it delivers. The client cannot tell a
//      signed-in player from a guest, and a refusal binds only to an account, so
//      the row and the header have to say so rather than let the operator find
//      out when the guest walks back in.

const output = await build({
  entryPoints: ["src/moderation.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { moderationChoices, moderationHeader } = await import(`data:text/javascript;base64,${source}`);

const bob = { name: "Bob", id: 7, blocked: false };

// --- a non-operator's window is untouched ---------------------------------

{
  assert.deepEqual(moderationChoices(bob, false), []);
  assert.deepEqual(moderationChoices({ ...bob, blocked: true }, false), []);
}

// --- an operator gets every action, mildest first --------------------------

{
  const choices = moderationChoices(bob, true);
  assert.deepEqual(
    choices.map((choice) => choice.action),
    ["block", "mute", "unmute", "kick", "refuse"],
  );
  // Mute and unmute are both offered rather than toggled: an operator who has
  // just arrived knows nothing about who is muted, and a toggle computed from
  // that nothing would invert the wrong way (M21.1 made the same call for
  // block, and the server takes the action it is told either way).
  assert.ok(choices.some((choice) => choice.action === "mute"));
  assert.ok(choices.some((choice) => choice.action === "unmute"));
}

// The one row that follows what this client already knows: you cannot block
// somebody twice, so an already-blocked player is offered the lift.
{
  const choices = moderationChoices({ ...bob, blocked: true }, true);
  assert.equal(choices[0].action, "unblock");
  assert.match(choices[0].confirm, /Unblock Bob/);
}

// --- every row fits, and every row says what it does ----------------------

{
  const long = { name: "A player with a very long display name indeed", id: 12345, blocked: false };
  for (const choice of moderationChoices(long, true)) {
    assert.ok(choice.label.length <= 40, `row "${choice.label}" wraps into the window border`);
    assert.ok(choice.confirm.length > 0);
  }
}

// The honest limit, in the place an operator reads before choosing.
{
  const choices = moderationChoices(bob, true);
  const refuse = choices.find((choice) => choice.action === "refuse");
  assert.match(refuse.label, /guest/i, "the refuse row must say what a refusal does not reach");
  const kick = choices.find((choice) => choice.action === "kick");
  assert.match(kick.label, /return/i, "the kick row must say the kicked player may come back");

  const header = moderationHeader(bob);
  assert.ok(header.some((line) => /signed-in/i.test(line)), "the header must state what refuse binds to");
  assert.ok(header.some((line) => line.includes("#7")), "the header must name the id, since names are not addresses");
  for (const line of header) {
    assert.ok(line.length <= 40, `header line "${line}" wraps into the window border`);
  }
}

// A nameless candidate (an older server's chat line, a guest who typed nothing)
// still produces a readable question rather than "Mute ? ".
{
  for (const choice of moderationChoices({ name: "", id: 3, blocked: false }, true)) {
    assert.ok(!/\s\?/.test(choice.confirm), `"${choice.confirm}" reads as a question about nobody`);
  }
}

console.log("moderation.test.mjs: ok");
