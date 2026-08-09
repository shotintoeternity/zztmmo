export type WatchLiveLineupEntry = {
  kind: "live" | "replay";
  world?: string;
  replayId?: string;
  title?: string;
  path?: string;
  board?: number;
  players?: number;
  watchers?: number;
  startTick?: number;
  ticks?: number;
};

export type WatchLiveTarget =
  | { kind: "live"; world: string }
  | { kind: "replay"; replayId: string; startTick: number };

export const WATCH_LIVE_CYCLE_MS = 15000;

export function nextWatchLiveIndex(current: number, total: number): number {
  if (total <= 0) {
    return -1;
  }
  return (current + 1 + total) % total;
}

export function watchLiveEntryLabel(entry: WatchLiveLineupEntry, index: number, total: number): string {
  const ordinal = total > 1 ? `${index + 1}/${total} ` : "";
  const name = (entry.title || entry.world || entry.replayId || "ZZT TV").slice(0, 9);
  const prefix = entry.kind === "replay" ? "TV R " : "TV ";
  return `${prefix}${ordinal}${name}`.slice(0, 15);
}

export function watchLiveEmbedMode(search: string): boolean {
  return new URLSearchParams(search).get("embed") === "1";
}

export function watchLiveEntryTarget(entry: WatchLiveLineupEntry): WatchLiveTarget | null {
  if (entry.kind === "replay" && entry.replayId) {
    return { kind: "replay", replayId: entry.replayId, startTick: entry.startTick ?? 0 };
  }
  if (entry.kind === "live" && entry.world) {
    return { kind: "live", world: entry.world };
  }
  return null;
}
