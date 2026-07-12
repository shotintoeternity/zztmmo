// resume.ts — the browser's reconnect + resume-token state machine (M13.2),
// deliberately free of the DOM and WebSocket so it can be unit-tested under Node
// (the M4.1 test pattern). main.ts owns the socket and wires these helpers in.

// A minimal Storage face so tests can pass a plain object instead of the real
// sessionStorage.
export interface TokenStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const TOKEN_PREFIX = "zzt-resume:";

export function tokenKey(world: string): string {
  return TOKEN_PREFIX + world;
}

export function loadResumeToken(store: TokenStore, world: string): string {
  try {
    return store.getItem(tokenKey(world)) ?? "";
  } catch {
    return "";
  }
}

export function saveResumeToken(store: TokenStore, world: string, token: string): void {
  if (!token) {
    return;
  }
  try {
    store.setItem(tokenKey(world), token);
  } catch {
    // A sandboxed or full sessionStorage just means resume is unavailable; the
    // client still plays, it merely rejoins fresh after a drop.
  }
}

export function clearResumeToken(store: TokenStore, world: string): void {
  try {
    store.removeItem(tokenKey(world));
  } catch {
    // ignore — see saveResumeToken.
  }
}

// reconnectDelay is a capped exponential backoff: base, 2*base, 4*base, ...
// clamped at cap. attempt is zero-based (the first retry is attempt 0).
export function reconnectDelay(attempt: number, base = 500, cap = 8000): number {
  const safeAttempt = attempt < 0 ? 0 : attempt;
  const delay = base * 2 ** safeAttempt;
  return delay > cap ? cap : delay;
}

// buildJoinMessage assembles the join payload, attaching a resume token only
// when one is stored. An empty token must be omitted so the server treats it as
// a fresh join rather than an unknown-token lookup.
export function buildJoinMessage(type: string, name: string, token: string): Record<string, unknown> {
  const message: Record<string, unknown> = { type, name };
  if (token) {
    message.resumeToken = token;
  }
  return message;
}
