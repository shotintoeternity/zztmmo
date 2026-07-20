// M12.5 — browser-side orchestration for the plan-then-paint generation job.
// Keeping this independent of the canvas makes the flow driveable under Node:
// the UI supplies rendering and the final join; this module supplies protocol
// requests, polling, and the ZZT-style status copy.

import { TEXT_WINDOW_WIDTH } from "./textwindow";

// An ordinary text-window line is drawn at column X+4 inside an inner span of
// TEXT_WINDOW_WIDTH-5 cells, so TEXT_WINDOW_WIDTH-8 chars fit without bleeding
// past the right border into the sidebar. Progress lines are client-composed
// (unlike engine scroll lines, which arrive pre-wrapped), so we clamp them here.
const PROGRESS_LINE_WIDTH = TEXT_WINDOW_WIDTH - 8;

function clampProgressLine(line: string): string {
  if (line.length <= PROGRESS_LINE_WIDTH) {
    return line;
  }
  return line.slice(0, PROGRESS_LINE_WIDTH - 1) + "\x85"; // CP437 ellipsis
}

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
  retryable?: boolean;
  failedBoard?: string;
  progress?: GenerationProgress[];
};

/**
 * DreamFailure carries the failed job's identity so the UI can offer a
 * targeted retry (M12.22): when the server kept resumable state (retryable),
 * re-requesting the failed board continues the same job instead of paying for
 * a whole fresh generation.
 */
export class DreamFailure extends Error {
  constructor(
    message: string,
    readonly jobId: string,
    readonly retryable: boolean,
    readonly failedBoard?: string,
  ) {
    super(message);
    this.name = "DreamFailure";
  }
}

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

// The server can describe one logical step with two wire events: the world
// loop announces `painting` with index/total (generation.go:224) and
// paintBoard's own attempt announces `painting` again for the same board and
// attempt with only a detail (generation.go:374); `planning` is emitted twice
// the same way. Both twins render the identical line, so the scroll showed
// every step twice (M12.18). Collapse consecutive events that share an
// identity — stage+board+attempt, the fields that name a step — rather than
// comparing rendered strings, and keep whichever twin carries the "N of M"
// placement.
function dedupeProgress(progress: GenerationProgress[]): GenerationProgress[] {
  const deduped: GenerationProgress[] = [];
  for (const event of progress) {
    const prev = deduped[deduped.length - 1];
    if (
      prev &&
      prev.stage === event.stage &&
      (prev.board ?? "") === (event.board ?? "") &&
      (prev.attempt ?? 0) === (event.attempt ?? 0)
    ) {
      if (!prev.index && event.index) {
        deduped[deduped.length - 1] = { ...prev, index: event.index, total: event.total };
      }
      continue;
    }
    deduped.push(event);
  }
  return deduped;
}

/** generationLines translates wire progress into the browser's text window. */
export function generationLines(progress: GenerationProgress[]): string[] {
  if (progress.length === 0) {
    return ["", "$Imagining the world...", ""];
  }

  const locations = new Map<string, { index: number; total: number }>();
  const lines = dedupeProgress(progress).map((event) => {
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
    // M17.13: a board that would not form is stubbed rather than failing the
    // whole dream, so the player sees what was lost and still gets a world.
    if (event.stage === "salvaging") {
      return event.board ? `Lost board: ${event.board}` : "Some rooms would not form...";
    }
    if (event.stage === "planning") return "Imagining the world...";
    if (event.stage === "validating") return "Checking every board...";
    if (event.stage === "persisting") return "Saving the new world...";
    return event.detail || event.stage;
  });
  return lines.slice(-10).map(clampProgressLine);
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
  ground = false,
): Promise<string> {
  const response = await fetcher("/api/generate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ prompt, async: true, ground }),
  });
  if (!response.ok) throw new Error(await response.text());
  const { id } = (await response.json()) as { id: string };
  if (!id) throw new Error("generation server returned no job id");
  return pollDreamJob(id, fetcher, wait, onProgress);
}

/**
 * retryDreamBoard re-requests the failed board of a still-resumable job
 * (M12.22) and resumes polling the same job id. The server refuses jobs that
 * are not failed-with-resume-state, so double retries surface as errors here.
 */
export async function retryDreamBoard(
  jobId: string,
  fetcher: Fetcher,
  wait: () => Promise<void>,
  onProgress: (progress: GenerationProgress[]) => void,
): Promise<string> {
  const response = await fetcher("/api/generate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ retry: jobId, async: true }),
  });
  if (!response.ok) throw new Error(await response.text());
  return pollDreamJob(jobId, fetcher, wait, onProgress);
}

async function pollDreamJob(
  id: string,
  fetcher: Fetcher,
  wait: () => Promise<void>,
  onProgress: (progress: GenerationProgress[]) => void,
): Promise<string> {
  for (;;) {
    await wait();
    const statusResponse = await fetcher(`/api/generate?id=${encodeURIComponent(id)}`);
    if (!statusResponse.ok) throw new Error(await statusResponse.text());
    const job = (await statusResponse.json()) as GenerationJob;
    onProgress(job.progress ?? []);
    if (job.status === "complete" && job.world) return job.world;
    if (job.status === "failed") {
      throw new DreamFailure(job.error || "generation failed", id, !!job.retryable, job.failedBoard);
    }
  }
}
