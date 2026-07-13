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
  const localWorlds = new Set(localEntries.map((entry) => entry.world.toUpperCase()));
  return [
    ...localEntries,
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
      author: (result.author ?? []).join(", ") || "Unknown",
      created: result.releaseDate || "",
      source: "museum",
      letter: result.letter,
      filename: result.filename,
    };
  });
}

export function museumPlayFailureLines(reason: string): string[] {
  return ["", `  Not playable: ${reason}`, ""];
}

export function museumNetworkFailureLines(): string[] {
  return ["", "  The Museum did not answer.", ""];
}
