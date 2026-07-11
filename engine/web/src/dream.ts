// M12.5 — browser-side orchestration for the plan-then-paint generation job.
// Keeping this independent of the canvas makes the flow driveable under Node:
// the UI supplies rendering and the final join; this module supplies protocol
// requests, polling, and the ZZT-style status copy.

export type GenerationProgress = {
  stage: string;
  board?: string;
  index?: number;
  total?: number;
  attempt?: number;
  maxAttempts?: number;
  detail?: string;
};

export type GenerationJob = {
  status: "running" | "complete" | "failed";
  world?: string;
  error?: string;
  progress?: GenerationProgress[];
};

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

/** generationLines translates wire progress into the browser's text window. */
export function generationLines(progress: GenerationProgress[]): string[] {
  if (progress.length === 0) {
    return ["", "$Imagining the world...", ""];
  }

  const locations = new Map<string, { index: number; total: number }>();
  const lines = progress.map((event) => {
    if (event.stage === "painting" && event.board && event.index && event.total) {
      locations.set(event.board, { index: event.index, total: event.total });
    }
    if (event.stage === "painting") {
      const place = event.board ? locations.get(event.board) : undefined;
      const name = event.board ?? "board";
      const prefix = place ? `Painting board ${place.index} of ${place.total}: ${name}` : `Painting board: ${name}`;
      return event.attempt && event.attempt > 1
        ? `${prefix} (attempt ${event.attempt} of ${event.maxAttempts ?? 3})`
        : prefix;
    }
    if (event.stage === "repairing") {
      return `Repairing ${event.board ?? "board"}: attempt ${event.attempt ?? 1} of ${event.maxAttempts ?? 3}`;
    }
    if (event.stage === "repairing-plan") {
      return `Repairing the plan: attempt ${event.attempt ?? 1} of ${event.maxAttempts ?? 3}`;
    }
    if (event.stage === "planning") return "Imagining the world...";
    if (event.stage === "validating") return "Checking every board...";
    if (event.stage === "persisting") return "Saving the new world...";
    return event.detail || event.stage;
  });
  return lines.slice(-10);
}

/**
 * runDreamGeneration starts an async generation job and resolves to the hosted
 * world name once it is safe to take the ordinary join path. It deliberately
 * treats the server as authoritative: no model text reaches the client here.
 */
export async function runDreamGeneration(
  prompt: string,
  fetcher: Fetcher,
  wait: () => Promise<void>,
  onProgress: (progress: GenerationProgress[]) => void,
): Promise<string> {
  const response = await fetcher("/api/generate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ prompt, async: true }),
  });
  if (!response.ok) throw new Error(await response.text());
  const { id } = (await response.json()) as { id: string };
  if (!id) throw new Error("generation server returned no job id");

  for (;;) {
    await wait();
    const statusResponse = await fetcher(`/api/generate?id=${encodeURIComponent(id)}`);
    if (!statusResponse.ok) throw new Error(await statusResponse.text());
    const job = (await statusResponse.json()) as GenerationJob;
    onProgress(job.progress ?? []);
    if (job.status === "complete" && job.world) return job.world;
    if (job.status === "failed") throw new Error(job.error || "generation failed");
  }
}
