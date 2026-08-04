// blocks.ts — who this player can stop hearing, and what the rows say (M21.1).
//
// Pure logic, free of the DOM and of module state (the preferences.ts /
// deep_link.ts shape): main.ts owns the window, the keys and the socket. The
// block itself is enforced on the SERVER, at the chat fan-out — nothing here
// filters anything, and nothing here could, which is the point.
//
// Two facts decide the shape of this list.
//
//  1. **A display name is not an address.** Names are neither unique nor claimed
//     (a guest gets "player" + a random number, and anyone may type any name), so
//     every row carries the `PlayerID` the server put on each chat line and each
//     roster entry, and shows it. Two people called "bob" are two rows.
//  2. **Global chat crosses boards and worlds; the roster does not.** The server
//     only sends the players on the board this client is looking at, so the
//     roster alone cannot offer the person who just said something from
//     somewhere else. The candidates are therefore the union of the roster and
//     the recent chat senders, with the roster's name winning where both know
//     one.

export type BlockCandidate = {
  id: number;
  name: string;
  /** On this board, so the roster knows them; otherwise chat is the only trace. */
  here: boolean;
  blocked: boolean;
};

const UNKNOWN_NAME = "player";

/**
 * blockCandidates lists everyone this player could block, roster first.
 *
 * `self` is excluded: nobody needs to block themselves, and the server refuses
 * it anyway. So is anything with no id — an old server's chat line carries no
 * `playerId`, and a row that cannot be addressed must not be offered rather than
 * silently blocking the wrong person.
 */
export function blockCandidates(input: {
  roster: { id: number; name?: string }[];
  chat: { from: string; playerId?: number }[];
  blocked: ReadonlySet<number>;
  self: number;
}): BlockCandidate[] {
  const byID = new Map<number, BlockCandidate>();

  // The roster arrives in whatever order the room walked it, so it is put into id
  // order here — which is arrival order, since ids are minted in sequence.
  const arrived = [...input.roster].sort((a, b) => a.id - b.id);
  for (const player of arrived) {
    if (!player.id || player.id === input.self) {
      continue;
    }
    byID.set(player.id, {
      id: player.id,
      name: (player.name || "").trim() || UNKNOWN_NAME,
      here: true,
      blocked: input.blocked.has(player.id),
    });
  }

  // Newest chat first, so a long backlog does not bury the person who just
  // spoke behind fifty of their own earlier lines.
  for (let i = input.chat.length - 1; i >= 0; i -= 1) {
    const line = input.chat[i];
    const id = line.playerId ?? 0;
    if (!id || id === input.self || byID.has(id)) {
      continue;
    }
    byID.set(id, {
      id,
      name: (line.from || "").trim() || UNKNOWN_NAME,
      here: false,
      blocked: input.blocked.has(id),
    });
  }

  const candidates = [...byID.values()];
  // Everyone in the room first, then everyone else, and NOTHING else: the sort is
  // stable, so each half keeps the order it was inserted in — the room in arrival
  // order, the rest most-recent-speaker first. Sorting the second half by id too
  // would undo the point of walking the backlog backwards.
  candidates.sort((a, b) => (a.here === b.here ? 0 : a.here ? -1 : 1));
  return candidates;
}

// The select list's inner span. A row must fit or it wraps into the border.
const ROW_WIDTH = 40;

/**
 * blockRowLabel is one row of the window, and doubles as the value the text
 * window's hyperlink form hands back — which is why the id is in it and not just
 * decoration: it is what makes two players called "bob" two distinguishable
 * rows, and what lets main.ts map a pick back to an address.
 */
export function blockRowLabel(candidate: BlockCandidate): string {
  let label = `${candidate.name} #${candidate.id}`;
  if (candidate.blocked) {
    label += " [blocked]";
  } else if (!candidate.here) {
    label += " (elsewhere)";
  }
  return label.slice(0, ROW_WIDTH);
}

/** The header the window opens with, so an empty list still explains itself. */
export function blockWindowHeader(candidates: BlockCandidate[]): string[] {
  if (candidates.length === 0) {
    return ["", "  Nobody else is here and nobody else has", "  said anything yet.", ""];
  }
  return ["", "  Choose someone to block or unblock.", "  Blocking only changes what YOU hear.", ""];
}
