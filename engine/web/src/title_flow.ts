export type WorldSelectionTransition = {
  worldName: string;
  startPlay: boolean;
};

export function selectWorldForTitle(worldName: string): WorldSelectionTransition {
  return { worldName, startPlay: false };
}
