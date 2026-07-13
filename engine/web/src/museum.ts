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
