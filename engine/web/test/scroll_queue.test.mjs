import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/scroll_queue.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { ScrollQueue, MAX_QUEUED_BOARD_SCROLLS } = await import(`data:text/javascript;base64,${source}`);

const boardScroll = (text, statId = 3) => ({ title: "Talker", lines: [text], statId, owned: false });
const ownedScroll = (text, statId = 5) => ({ title: "Vendor", lines: [text], statId, owned: true });

// 1. A looping object cannot pile scrolls up behind the one being read.
{
  const q = new ScrollQueue();
  const first = boardScroll("Hello");
  assert.equal(q.offer(first, false), "show");
  q.opened(first);
  for (let i = 0; i < 100; i += 1) {
    q.offer(boardScroll("Hello"), true);
  }
  assert.equal(q.length, 0, "a repeat of the scroll on screen is not queued again");

  for (let i = 0; i < 100; i += 1) {
    q.offer(boardScroll(`Line ${i}`), true);
  }
  assert.equal(typeof MAX_QUEUED_BOARD_SCROLLS, "number");
  assert.equal(q.length, MAX_QUEUED_BOARD_SCROLLS, "board-wide scrolls are capped");
  assert.equal(q.offer(boardScroll("Line 0"), true), "drop", "a repeat of a queued scroll is dropped");

  // The scroll the player touched is still owed a reply: the cap never drops it.
  assert.equal(q.offer(ownedScroll("Buy ammo?"), true), "queue");
  assert.equal(q.length, MAX_QUEUED_BOARD_SCROLLS + 1);
  assert.equal(q.offer(ownedScroll("Buy ammo?"), true), "queue", "an owned scroll is never de-duplicated away");
}

// 2. A scroll never displaces a modal that is not a scroll (chat, with its draft).
{
  const q = new ScrollQueue();
  const scroll = boardScroll("Hello");
  assert.equal(q.offer(scroll, true), "queue", "with chat open, a scroll waits");
  assert.equal(q.openStatId, -1);
  assert.deepEqual(q.next(), scroll, "and is shown once the chat closes");
  assert.equal(q.offer(boardScroll("Again"), false), "show", "with nothing open, a scroll opens at once");
}

// 3. A board change forgets every scroll from the board that was left: the open
//    one is owed no reply there, and nothing queued is shown on the new board.
{
  const q = new ScrollQueue();
  const open = ownedScroll("Buy ammo?");
  q.opened(open);
  q.offer(boardScroll("Queued"), true);
  q.boardChanged();
  assert.equal(q.closed(), null, "the scroll from the old board gets no reply");
  assert.equal(q.next(), undefined, "nothing from the old board is shown on the new one");
  assert.equal(q.openStatId, -1);
}

console.log("scroll_queue.test.mjs: cap, de-duplication, no displacement and board change passed");
