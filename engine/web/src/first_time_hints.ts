// first_time_hints.ts — the one-time nudges a player sees the first time they
// meet a piece of the interface, and the record of which ones they have seen.
//
// A signed-in player carries that record on their account so it follows them
// between browsers; a guest carries it in localStorage. Both go through the same
// functions here, which is what keeps a hint from reappearing for a guest who
// then signs in.

import { EMPTY_ACCOUNT_HINTS, type AccountHintKey, type AccountHintPreferences } from "./preferences";

export const FIRST_TIME_HINTS: Record<AccountHintKey, string> = {
  players: "That other face is a real person - C chats",
  death: "You respawn. Your things stay yours.",
  chat: "C opens chat. Someone just spoke.",
};

export type HintStorage = {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
};

const GUEST_HINTS_KEY = "zztmmo.firstTimeHints";

export function normalizeHints(hints: Partial<AccountHintPreferences> | null | undefined): AccountHintPreferences {
  return {
    ...EMPTY_ACCOUNT_HINTS,
    players: hints?.players === true,
    death: hints?.death === true,
    chat: hints?.chat === true,
  };
}

export function loadGuestHints(storage: HintStorage): AccountHintPreferences {
  try {
    return normalizeHints(JSON.parse(storage.getItem(GUEST_HINTS_KEY) || "{}"));
  } catch {
    return normalizeHints(null);
  }
}

export function saveGuestHint(storage: HintStorage, hint: AccountHintKey) {
  const hints = loadGuestHints(storage);
  hints[hint] = true;
  storage.setItem(GUEST_HINTS_KEY, JSON.stringify(hints));
}

export function hintAlreadySeen(hints: Partial<AccountHintPreferences> | null | undefined, hint: AccountHintKey): boolean {
  return normalizeHints(hints)[hint];
}
