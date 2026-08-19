// museum.ts — the client half of Museum of ZZT search.
//
// The server proxies the Museum's API (it holds the network access and the
// rate limiting); this file turns what comes back into rows for the world
// picker and merges them with the worlds already hosted locally, so a title
// that exists in both appears once. The failure lines are here rather than
// inline because a search that finds nothing, a Museum that is down and a world
// that refuses to load are three different messages, and a player deserves to
// be told which one happened.

import type { WorldSearchEntry } from "./modal";

export type MuseumSearchResult = {
  id?: string;
  letter: string;
  filename: string;
  title: string;
  author?: string[];
  releaseDate?: string;
  genres?: string[];
  rating?: number | null;
  playableBoards?: number;
  totalBoards?: number;
  archiveName?: string;
};

export type MuseumPlayResponse = {
  world?: string;
  choices?: { name: string }[];
};

export function mergeWorldEntries(localEntries: WorldSearchEntry[], museumEntries: WorldSearchEntry[]): WorldSearchEntry[] {
  const museumByWorld = new Map(museumEntries.map((entry) => [entry.world.toUpperCase(), entry]));
  const localWorlds = new Set(localEntries.map((entry) => entry.world.toUpperCase()));
  return [
    ...localEntries.map((entry) => {
      const museum = museumByWorld.get(entry.world.toUpperCase());
      if (!museum) {
        return entry;
      }
      return {
        ...entry,
        id: museum.id || entry.id,
        title: museum.title || entry.title,
        author: museum.author || entry.author,
        created: museum.created || entry.created,
      };
    }),
    ...museumEntries.filter((entry) => !localWorlds.has(entry.world.toUpperCase())),
  ];
}

export function museumResultsToEntries(results: MuseumSearchResult[]): WorldSearchEntry[] {
  return results.map((result) => {
    const stem = result.filename.replace(/\.[^.]+$/, "").toUpperCase().slice(0, 8);
    return {
      world: stem,
      id: result.id || result.archiveName || result.filename.replace(/\.[^.]+$/, ""),
      title: result.title || result.filename,
      author: museumAuthorText(result.author ?? []),
      created: result.releaseDate || "",
      source: "museum",
      letter: result.letter,
      filename: result.filename,
    };
  });
}

function museumAuthorText(authors: string[]): string {
  const cleaned = authors.map((author) => author.trim()).filter(Boolean);
  const known = cleaned.filter((author) => author.toLowerCase() !== "unknown");
  return (known.length > 0 ? known : cleaned).join(", ") || "Unknown";
}

export function museumPlayFailureLines(reason: string): string[] {
  return ["", `  Not playable: ${reason}`, ""];
}

export function museumNetworkFailureLines(): string[] {
  return ["", "  The Museum did not answer.", ""];
}
