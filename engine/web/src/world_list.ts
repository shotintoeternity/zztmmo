// world_list.ts — the title screen's world list as the server sends it.

import type { WorldSearchEntry, WorldShelf } from "./modal";

export function normalizeWorldEntries(entries: (WorldSearchEntry | string)[]): WorldSearchEntry[] {
  return entries.map((entry) => {
    if (typeof entry !== "string") {
      return {
        world: entry.world,
        id: entry.id || entry.world,
        title: entry.title || entry.world,
        author: entry.author || "Unknown",
        created: entry.created || "",
        players: entry.players || 0,
        favorite: entry.favorite === true,
        playCount: entry.playCount || 0,
        thumbnail: entry.thumbnail,
        editors: entry.editors || 0,
        friendsHere: entry.friendsHere ?? [],
        source: "local",
        // M18.9: the server's grouping. Left undefined by an older server or
        // by the bare-string list below, and the picker treats "not local" as
        // showable, so an unknown kind fails open — a world is never hidden
        // because the server did not say what it was.
        kind: entry.kind,
      };
    }
    const world = entry.split(" (")[0];
    return {
      world,
      id: world,
      title: world,
      author: "Unknown",
      created: "",
      players: 0,
      editors: 0,
      source: "local",
    };
  });
}

export function normalizeWorldShelves(shelves: WorldShelf[]): WorldShelf[] {
  return shelves
    .map((shelf) => ({
      id: typeof shelf.id === "string" ? shelf.id : "",
      title: typeof shelf.title === "string" ? shelf.title : "",
      worlds: Array.isArray(shelf.worlds) ? shelf.worlds.filter((world): world is string => typeof world === "string") : [],
    }))
    .filter((shelf) => shelf.id && shelf.title && shelf.worlds.length > 0);
}

type WorldListFetch = (input: string) => Promise<{ ok: boolean; status: number; json(): Promise<unknown> }>;

// fetchWorldList reads /api/worlds. A reply that is not OK throws, like a
// network failure, so the caller says the server did not answer; reading its
// body as a list would show an empty picker ("There are no ZZT worlds.").
export async function fetchWorldList(fetchFn: WorldListFetch): Promise<{ entries: WorldSearchEntry[]; shelves: WorldShelf[] }> {
  const response = await fetchFn("/api/worlds");
  if (!response.ok) {
    throw new Error(`world list: ${response.status}`);
  }
  const data = (await response.json()) as { worlds?: (WorldSearchEntry | string)[]; shelves?: WorldShelf[] };
  return {
    entries: normalizeWorldEntries(data.worlds ?? []),
    shelves: normalizeWorldShelves(data.shelves ?? []),
  };
}
