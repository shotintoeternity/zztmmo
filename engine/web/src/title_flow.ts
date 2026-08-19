// title_flow.ts — the small decisions the title screen makes: which world a
// selection opens, and whether this browser has been here before.
//
// Kept out of title.ts and main.ts because these are exactly the branches that
// used to get walked by accident. A first visit and a return visit see
// different screens, so a browser test that does not say which one it is
// silently tests whichever the default happens to be that month — the failure
// mode M33.2 exists to prevent. Small, pure and separately tested for that
// reason.

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
