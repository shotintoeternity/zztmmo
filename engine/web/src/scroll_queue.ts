// scroll_queue.ts decides what an arriving scroll does: open now, wait its turn
// behind what is on screen, or not be shown at all (M36.25).
//
// Scrolls arrive as events and must never overwrite one another: a second
// scroll opening on top of the first is text the player never got to read. They
// queue instead, and the server freezes the reader until each one they touched
// is answered.

export type PendingScroll = {
  title: string;
  lines: string[];
  // The OBJECT that showed it; its reply goes back to this stat.
  statId: number;
  // A scroll the player touched (playerStatId >= 0). The server froze them for
  // it and waits for exactly one reply, so it is never dropped.
  owned: boolean;
};

export type ScrollAdmission = "show" | "queue" | "drop";

// MAX_QUEUED_BOARD_SCROLLS caps the board-wide scrolls waiting behind the one on
// screen. Nobody is frozen for those, so an object talking to the board every
// cycle would otherwise grow the queue for as long as the modal blocks the
// player from walking away. Matches nothing in vanilla, where a text window
// stopped the whole game and no object could speak while one was open.
export const MAX_QUEUED_BOARD_SCROLLS = 8;

function sameScroll(a: PendingScroll, b: PendingScroll): boolean {
  return (
    a.statId === b.statId &&
    a.title === b.title &&
    a.lines.length === b.lines.length &&
    a.lines.every((line, i) => line === b.lines[i])
  );
}

export class ScrollQueue {
  private queue: PendingScroll[] = [];
  private current: PendingScroll | null = null;

  // offer says what to do with an arriving scroll. "queue" has already queued
  // it. otherModalOpen is a modal on screen that is NOT a scroll (chat, a
  // prompt): a scroll waits behind it rather than replacing it and losing
  // whatever was typed there.
  offer(scroll: PendingScroll, otherModalOpen: boolean): ScrollAdmission {
    if (scroll.lines.length === 0) {
      return "drop";
    }
    if (!scroll.owned) {
      if (this.current && sameScroll(this.current, scroll)) {
        return "drop";
      }
      if (this.queue.some((queued) => sameScroll(queued, scroll))) {
        return "drop";
      }
    }
    if (!this.current && !otherModalOpen) {
      return "show";
    }
    if (!scroll.owned && this.queue.filter((queued) => !queued.owned).length >= MAX_QUEUED_BOARD_SCROLLS) {
      return "drop";
    }
    this.queue.push(scroll);
    return "queue";
  }

  // opened records the scroll now on screen.
  opened(scroll: PendingScroll) {
    this.current = scroll;
  }

  // closed ends the scroll on screen, returning it so its reply can be sent.
  closed(): PendingScroll | null {
    const was = this.current;
    this.current = null;
    return was;
  }

  get openStatId(): number {
    return this.current ? this.current.statId : -1;
  }

  next(): PendingScroll | undefined {
    return this.queue.shift();
  }

  get length(): number {
    return this.queue.length;
  }

  clear() {
    this.queue = [];
    this.current = null;
  }

  // boardChanged forgets every scroll from the board that was left. The server
  // drops them too (M36.16: a stat id names an object on the board it was read
  // on), so the open one is owed no reply, and a queued one shown on the new
  // board would answer an unrelated object with the same stat id.
  boardChanged() {
    this.clear();
  }
}
