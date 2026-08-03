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
// The editor's membership token (M16.14f) is stored under its own prefix: a
// browser can hold a dropped run and a dropped editing session for the same
// world at once, and they name different things on the server — one a player in
// a room, the other a member of an EditorSession.
const EDITOR_TOKEN_PREFIX = "zzt-editor:";

export function tokenKey(world: string): string {
  return TOKEN_PREFIX + world;
}

export function editorTokenKey(world: string): string {
  return EDITOR_TOKEN_PREFIX + world;
}

function readToken(store: TokenStore, key: string): string {
  try {
    return store.getItem(key) ?? "";
  } catch {
    return "";
  }
}

function writeToken(store: TokenStore, key: string, token: string): void {
  if (!token) {
    return;
  }
  try {
    store.setItem(key, token);
  } catch {
    // A sandboxed or full sessionStorage just means resume is unavailable; the
    // client still plays, it merely rejoins fresh after a drop.
  }
}

function dropToken(store: TokenStore, key: string): void {
  try {
    store.removeItem(key);
  } catch {
    // ignore — see writeToken.
  }
}

export function loadResumeToken(store: TokenStore, world: string): string {
  return readToken(store, tokenKey(world));
}

export function saveResumeToken(store: TokenStore, world: string, token: string): void {
  writeToken(store, tokenKey(world), token);
}

export function clearResumeToken(store: TokenStore, world: string): void {
  dropToken(store, tokenKey(world));
}

export function loadEditorToken(store: TokenStore, world: string): string {
  return readToken(store, editorTokenKey(world));
}

export function saveEditorToken(store: TokenStore, world: string, token: string): void {
  writeToken(store, editorTokenKey(world), token);
}

export function clearEditorToken(store: TokenStore, world: string): void {
  dropToken(store, editorTokenKey(world));
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
export function buildJoinMessage(
  type: string,
  name: string,
  token: string,
  color = "",
): Record<string, unknown> {
  const message: Record<string, unknown> = { type, name };
  if (token) {
    message.resumeToken = token;
  }
  // M19.1: omitted when unset, matching JoinMessage.Color's `omitempty` — an
  // absent colour is the vanilla white-on-blue player, not a colour of "".
  if (color) {
    message.color = color;
  }
  return message;
}

// The picked player colour (M19.1). Unlike the resume tokens above this is
// localStorage rather than sessionStorage and is not keyed by world: it is a
// property of the player, not of a run, and it should survive closing the tab.
// M19.2 adds the picker that writes it; M19.3 moves a signed-in player's copy
// to their account, where localStorage becomes the guest-only fallback.
const COLOR_KEY = "zzt-color";

export function loadPlayerColor(store: TokenStore): string {
  return readToken(store, COLOR_KEY);
}

export function savePlayerColor(store: TokenStore, color: string): void {
  writeToken(store, COLOR_KEY, color);
}

// clearPlayerColor is the picker's "No colour" row (M19.2). It has to remove the
// key rather than write "": writeToken deliberately ignores an empty value (an
// empty resume token is not a token), and an absent key is what every join path
// already reads as the vanilla white-on-blue player.
export function clearPlayerColor(store: TokenStore): void {
  dropToken(store, COLOR_KEY);
}

// buildEditorEnterMessage is buildJoinMessage's editor counterpart (M16.14f):
// the world instead of a nickname, and the membership token only when one is
// stored, so a first entry is not read as a lookup of the empty token.
export function buildEditorEnterMessage(type: string, world: string, token: string): Record<string, unknown> {
  const message: Record<string, unknown> = { type, world };
  if (token) {
    message.resumeToken = token;
  }
  return message;
}
