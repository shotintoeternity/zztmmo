export type WorldSelectionTransition = {
  worldName: string;
  startPlay: boolean;
};

// The first-visit key outlives the WELCOME world it was built for (owner
// 2026-08-11): M33.2's browser discipline requires every script to declare
// which visitor it is, and markProfileWarm writes this key to do it. Keeping
// it means the seven suites that call markProfileWarm keep saying something
// true, and a future first-visit flow has its storage already specified.
export const FIRST_VISIT_WELCOME_KEY = "zzt-first-visit-welcome";

export function selectWorldForTitle(worldName: string): WorldSelectionTransition {
  return { worldName, startPlay: false };
}

export function hasSeenWelcome(storage: Storage): boolean {
  return storage.getItem(FIRST_VISIT_WELCOME_KEY) === "1";
}

export function markWelcomeSeen(storage: Storage) {
  storage.setItem(FIRST_VISIT_WELCOME_KEY, "1");
}
