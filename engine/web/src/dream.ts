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

// Three ASCII periods, not a single ellipsis glyph: CP437 has no horizontal
// ellipsis. This line used to append "\x85" commented as one, which the font
// sheet draws as 'a' with a grave accent — every clamped progress line ended in
// a stray accented letter (M18.7). Three periods also match the copy above
// ("Imagining the world...").
const PROGRESS_ELLIPSIS = "...";

function clampProgressLine(line: string): string {
  if (line.length <= PROGRESS_LINE_WIDTH) {
    return line;
  }
  return line.slice(0, PROGRESS_LINE_WIDTH - PROGRESS_ELLIPSIS.length) + PROGRESS_ELLIPSIS;
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
  // M17.13: the boards that would not paint and were salvaged into stub rooms.
  // Set on a *complete* job — the world is playable but incomplete.
  stubbedBoards?: string[];
  progress?: GenerationProgress[];
};

/**
 * DreamResult is what a finished job hands back. A salvaged dream (M17.13) is
 * complete AND retryable: it hosts a world whose lost rooms are stubs, and it
 * keeps the resume state that repaints them. So the world name arrives together
 * with the salvage state rather than instead of it — the caller enters the world
 * and offers the repaint, which is the whole of M16.17c.
 */
export type DreamResult = {
  world: string;
  jobId: string;
  retryable: boolean;
  failedBoard?: string;
  stubbedBoards: string[];
};

/** salvagedBoards names the rooms a completed dream lost, or "" if none. */
export function salvagedBoards(dream: DreamResult): string {
  if (!dream.retryable || dream.stubbedBoards.length === 0) return "";
  return dream.failedBoard || dream.stubbedBoards.join(", ");
}

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
    // M16.17d: the plan's own name was a canonical world or another player's,
    // so the world was minted one instead. The player chose neither name, so
    // the line says only the thing they need — which world is theirs.
    if (event.stage === "naming") {
      return event.detail ? `Your world is called ${event.detail}` : "Naming the world...";
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
): Promise<DreamResult> {
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
): Promise<DreamResult> {
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
): Promise<DreamResult> {
  for (;;) {
    await wait();
    const statusResponse = await fetcher(`/api/generate?id=${encodeURIComponent(id)}`);
    if (!statusResponse.ok) throw new Error(await statusResponse.text());
    const job = (await statusResponse.json()) as GenerationJob;
    onProgress(job.progress ?? []);
    if (job.status === "complete" && job.world) {
      // M16.17c: a complete job may still be retryable. Carry that out rather
      // than dropping it on the floor — the server kept the resume state for
      // exactly this job id, and it is the only way back to the lost rooms.
      return {
        world: job.world,
        jobId: id,
        retryable: !!job.retryable,
        failedBoard: job.failedBoard,
        stubbedBoards: job.stubbedBoards ?? [],
      };
    }
    if (job.status === "failed") {
      throw new DreamFailure(job.error || "generation failed", id, !!job.retryable, job.failedBoard);
    }
  }
}
