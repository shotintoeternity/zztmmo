// A join the server refuses (a full board, or a crowded start: M42.4) is
// answered with one ownerless scroll, before any snapshot, and then the socket
// is closed. Read as an ordinary close, that looked like a dropped connection:
// the scroll never showed, the sidebar said "Connection error", and the
// reconnect asked again about twice a second. joinRefusalFrom recognises the
// refusal so the client can show it and stop.

export type JoinRefusal = { title: string; lines: string[] };

type RefusalCandidate = {
  type?: string;
  event?: { type?: string; statId?: number; playerStatId?: number; title?: string; lines?: string[] };
};

// joinRefusalFrom returns the refusal a message carries, or null. Only a
// message that arrives before this socket's first snapshot can be one: after
// it, an ownerless scroll is an object talking to the whole board.
export function joinRefusalFrom(message: RefusalCandidate, joined: boolean): JoinRefusal | null {
  if (joined || message.type !== "event" || !message.event) {
    return null;
  }
  const event = message.event;
  if (event.type !== "scroll" || (event.statId ?? -1) !== -1 || (event.playerStatId ?? -1) !== -1) {
    return null;
  }
  return { title: event.title ?? "", lines: event.lines ?? [] };
}
