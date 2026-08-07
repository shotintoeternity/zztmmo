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
