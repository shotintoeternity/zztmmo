// deep_link.ts — the /play/<world>, /watch/<world> and /replay/<id> deep links.
//
// A URL a player can send someone else, which lands them on that world's title
// screen. Everything here is pure — a function of a path string and the world
// list — and free of the DOM, in the resume.ts / preferences.ts shape: main.ts
// owns when the location is read, when history is rewritten, and what is drawn.
//
// The server needs no route: spaFileServer (cmd/zzt-server/main.go) already
// serves the client for any path that is not a file, so /play/TOWN and
// /watch/TOWN and /replay/TOWN-20260807-120000 are the app.
//
// The resolution rule is the load-bearing part. A deep link must name a world
// the way the JOIN path names it, not by string: M18.13 made /api/worlds emit
// SanitizeSaveName(base) — the identity LoadPristineWorld will open — one entry
// per world. So a link is resolved against that list rather than handed to the
// server as typed, which is what makes /play/town and /play/TOWN one world and
// what refuses an unjoinable name BEFORE a socket rather than after.

/** The path prefix that names a world to play. */
export const DEEP_LINK_PREFIX = "/play/";
/** The path prefix that names a world to watch read-only. */
export const WATCH_LINK_PREFIX = "/watch/";
/** The path prefix that names a recording to watch read-only (M22.3). */
export const REPLAY_LINK_PREFIX = "/replay/";
/** The reserved ambient channel route (M28.1). */
export const WATCH_LIVE_PATH = "/watch/live";

/** The subset of an /api/worlds entry this module reads. */
export type DeepLinkCandidate = { world: string };

/**
 * deepLinkWorldName returns the world name a path asks for, or "" when the path
 * is not a deep link at all.
 *
 * A trailing slash is tolerated (/play/TOWN/ is the same link a browser or a
 * chat client may have tidied), and percent-escapes are decoded so a link
 * pasted out of the address bar round-trips. Anything left over — a deeper path,
 * a name no world answers to — is returned as-is and refused by resolution: a
 * name we cannot resolve must reach the player as a refusal, never as a silent
 * fall-through to the default world.
 */
export function deepLinkWorldName(pathname: string): string {
  return worldNameFromPrefixedPath(pathname, DEEP_LINK_PREFIX);
}

/** watchLinkWorldName is deepLinkWorldName's read-only twin (M22.2). */
export function watchLinkWorldName(pathname: string): string {
  if (isWatchLivePath(pathname)) {
    return "";
  }
  return worldNameFromPrefixedPath(pathname, WATCH_LINK_PREFIX);
}

export function isWatchLivePath(pathname: string): boolean {
  return pathname === WATCH_LIVE_PATH || pathname === WATCH_LIVE_PATH + "/";
}

/** replayLinkID returns the replay id a path asks for, or "" outside /replay. */
export function replayLinkID(pathname: string): string {
  return worldNameFromPrefixedPath(pathname, REPLAY_LINK_PREFIX);
}

function worldNameFromPrefixedPath(pathname: string, prefix: string): string {
  if (!pathname.startsWith(prefix)) {
    return "";
  }
  let rest = pathname.slice(prefix.length);
  while (rest.endsWith("/")) {
    rest = rest.slice(0, -1);
  }
  if (rest === "") {
    return "";
  }
  try {
    return decodeURIComponent(rest).trim();
  } catch {
    // A malformed escape is not a world name, but it IS a deep link the player
    // typed: hand back the raw text so they are told, rather than dropped into
    // the picker as though they had never asked for anything.
    return rest.trim();
  }
}

/** deepLinkPath is the shareable address for a world the client is showing. */
export function deepLinkPath(worldName: string): string {
  return DEEP_LINK_PREFIX + encodeURIComponent(worldName);
}

/** watchLinkPath is the shareable read-only address for a world. */
export function watchLinkPath(worldName: string): string {
  return WATCH_LINK_PREFIX + encodeURIComponent(worldName);
}

/** replayLinkPath is the shareable read-only address for a recording. */
export function replayLinkPath(id: string): string {
  return REPLAY_LINK_PREFIX + encodeURIComponent(id);
}

/**
 * resolveDeepLinkWorld maps a requested name onto the identity /api/worlds
 * lists, or "" when nothing there answers to it.
 *
 * The match is case-insensitive because the list is already canonical: M18.13's
 * entries are SanitizeSaveName output, so `town` and `TOWN` are the same single
 * entry and the caller must not have to know which case the link was written
 * in. The value returned is always the LISTED name, never the requested one —
 * enterWorld then selects exactly what the picker would have selected.
 */
export function resolveDeepLinkWorld(requested: string, entries: DeepLinkCandidate[]): string {
  const wanted = requested.trim().toUpperCase();
  if (wanted === "") {
    return "";
  }
  for (const entry of entries) {
    if (entry.world.toUpperCase() === wanted) {
      return entry.world;
    }
  }
  return "";
}

// The refusal window's inner span, matching the dream offer's clamp: a longer
// line bleeds through the border into the sidebar.
const REFUSAL_WIDTH = 42;
// A link can name anything at all, so the echoed name is clamped before it is
// framed by quotes and prose that must still fit.
const REFUSAL_NAME_WIDTH = 20;

/**
 * deepLinkRefusalLines is what a visitor sees when their link names no world we
 * host. It says which name failed — a bare "not found" leaves them unable to
 * tell a typo from a world that has gone — and it is followed by the picker, so
 * the outcome of a dead link is a usable client rather than a blank screen.
 */
export function deepLinkRefusalLines(requested: string, reason = ""): string[] {
  const shown = requested.slice(0, REFUSAL_NAME_WIDTH) + (requested.length > REFUSAL_NAME_WIDTH ? "..." : "");
  const lines = [
    "",
    `  No world named "${shown}" is here.`.slice(0, REFUSAL_WIDTH),
  ];
  if (reason) {
    lines.push(`  (${reason})`.slice(0, REFUSAL_WIDTH));
  }
  lines.push("", "  Choose a world from the list instead.", "");
  return lines;
}
