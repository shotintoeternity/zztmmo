export type WorldSelectionTransition = {
  worldName: string;
  startPlay: boolean;
};

export const WELCOME_WORLD = "WELCOME";
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

export function shouldOpenWelcomeFirstVisit(input: { authenticated: boolean; hasSeenWelcome: boolean; welcomeHosted: boolean }): boolean {
  return !input.authenticated && !input.hasSeenWelcome && input.welcomeHosted;
}
