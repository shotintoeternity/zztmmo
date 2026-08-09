// preferences.ts — the account-wide preferences a signed-in player carries from
// browser to browser (M19.3), kept free of the DOM and of module state so every
// rule below is unit-testable under Node (the resume.ts / player_tint.ts shape).
// main.ts owns when these are fetched and what is redrawn afterwards.

import { isPlayerColor } from "./player_tint";

// What /api/preferences answers. `stored` is separate from `color` on purpose:
// an account WITH a document whose color is empty has chosen the vanilla
// white-on-blue player, and an account with no document has never chosen
// anything. Collapsing the two would make "No color" un-choosable, which is the
// M19.2 lesson one layer up.
export interface AccountPreferences {
  authenticated: boolean;
  stored: boolean;
  color: string;
  hints: AccountHintPreferences;
  profile: AccountProfilePreferences;
  shareLocationWithFollowers: boolean;
}

export type AccountHintKey = "players" | "death" | "chat";
export type AccountHintPreferences = Record<AccountHintKey, boolean>;
export const EMPTY_ACCOUNT_HINTS: AccountHintPreferences = { players: false, death: false, chat: false };
export type AccountProfilePreferences = {
  handle: string;
  displayName: string;
  about: string[];
};
export const EMPTY_ACCOUNT_PROFILE: AccountProfilePreferences = { handle: "", displayName: "", about: [] };

const PREFERENCES_URL = "/api/preferences";

// A fetch-shaped function, so tests can hand in a stub instead of a network.
export type FetchLike = (input: string, init?: { method?: string; headers?: Record<string, string>; body?: string }) => Promise<{
  ok: boolean;
  status: number;
  json(): Promise<unknown>;
}>;

function readPreferences(value: unknown): AccountPreferences {
  const doc = (value ?? {}) as Partial<AccountPreferences>;
  const color = isPlayerColor(doc.color) ? doc.color : "";
  const hints = (doc.hints ?? {}) as Partial<AccountHintPreferences>;
  const profileDoc = (doc.profile ?? {}) as Partial<AccountProfilePreferences>;
  const about = Array.isArray(profileDoc.about)
    ? profileDoc.about.filter((line): line is string => typeof line === "string")
    : [];
  return {
    authenticated: doc.authenticated === true,
    stored: doc.stored === true,
    color,
    hints: {
      players: hints.players === true,
      death: hints.death === true,
      chat: hints.chat === true,
    },
    profile: {
      handle: typeof profileDoc.handle === "string" ? profileDoc.handle : "",
      displayName: typeof profileDoc.displayName === "string" ? profileDoc.displayName : "",
      about,
    },
    shareLocationWithFollowers: doc.shareLocationWithFollowers === true,
  };
}

// A failed or unauthenticated read returns null rather than a blank document:
// null means "we do not know what this account wants", and the caller falls
// back to localStorage. A blank document would mean "this account wants no
// color", which is a claim a failed request has no business making.
export async function fetchAccountPreferences(fetchFn: FetchLike): Promise<AccountPreferences | null> {
  try {
    const response = await fetchFn(PREFERENCES_URL);
    if (!response.ok) {
      return null;
    }
    const prefs = readPreferences(await response.json());
    return prefs.authenticated ? prefs : null;
  } catch {
    return null;
  }
}

// The picker's write path for a signed-in player. It sends the color even when
// it is empty — that is how "No color" is said out loud — and returns the
// document the server stored, so the caller holds what the server holds rather
// than what it hoped for.
export async function saveAccountColor(fetchFn: FetchLike, color: string): Promise<AccountPreferences | null> {
  const wanted = isPlayerColor(color) ? color : "";
  try {
    const response = await fetchFn(PREFERENCES_URL, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ color: wanted }),
    });
    if (!response.ok) {
      return null;
    }
    return readPreferences(await response.json());
  } catch {
    return null;
  }
}

export async function saveAccountHint(fetchFn: FetchLike, hint: AccountHintKey): Promise<AccountPreferences | null> {
  try {
    const response = await fetchFn(PREFERENCES_URL, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ hints: { [hint]: true } }),
    });
    if (!response.ok) {
      return null;
    }
    return readPreferences(await response.json());
  } catch {
    return null;
  }
}

export async function saveAccountProfile(fetchFn: FetchLike, profile: AccountProfilePreferences): Promise<AccountPreferences | null> {
  try {
    const response = await fetchFn(PREFERENCES_URL, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ profile }),
    });
    if (!response.ok) {
      return null;
    }
    return readPreferences(await response.json());
  } catch {
    return null;
  }
}

export async function saveShareLocationWithFollowers(fetchFn: FetchLike, shareLocationWithFollowers: boolean): Promise<AccountPreferences | null> {
  try {
    const response = await fetchFn(PREFERENCES_URL, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ shareLocationWithFollowers }),
    });
    if (!response.ok) {
      return null;
    }
    return readPreferences(await response.json());
  } catch {
    return null;
  }
}

// effectivePlayerColor is the whole of "a signed-in player's stored color wins
// over localStorage; a guest keeps localStorage only" (M19.3), in one place so
// the join, the title swatch and the picker's own starting value cannot drift
// apart.
//
// A stored document wins even when its color is empty: that is the deliberate
// vanilla player, and a pick left behind in this browser must not resurrect the
// color the player just took off. An account with no document falls back to
// this browser's pick, which is what makes the first signed-in join adopt it
// (the server does the adopting — see resolveAccountPlayerColor).
export function effectivePlayerColor(sources: { account: AccountPreferences | null; local: string }): string {
  const { account, local } = sources;
  if (account && account.authenticated && account.stored) {
    return isPlayerColor(account.color) ? account.color : "";
  }
  return isPlayerColor(local) ? local : "";
}
