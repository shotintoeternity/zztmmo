// palette.ts — the sixteen EGA colors, in DOS attribute order.

export const EGA: readonly string[] = [
  "#000000", "#0000aa", "#00aa00", "#00aaaa", "#aa0000", "#aa00aa", "#aa5500", "#aaaaaa",
  "#555555", "#5555ff", "#55ff55", "#55ffff", "#ff5555", "#ff55ff", "#ffff55", "#ffffff",
];

export function paletteColor(index: number): string {
  return EGA[index & 0x0f];
}

/** paletteRGB is the same color as three floats in 0..1, for a vertex attribute. */
export function paletteRGB(index: number): [number, number, number] {
  const hex = EGA[index & 0x0f];
  return [parseInt(hex.slice(1, 3), 16) / 255, parseInt(hex.slice(3, 5), 16) / 255, parseInt(hex.slice(5, 7), 16) / 255];
}

/** hexRGB parses "#RRGGBB" into three floats, or returns null for anything else. */
export function hexRGB(hex: string | undefined): [number, number, number] | null {
  if (!hex || !/^#[0-9a-fA-F]{6}$/.test(hex)) {
    return null;
  }
  return [parseInt(hex.slice(1, 3), 16) / 255, parseInt(hex.slice(3, 5), 16) / 255, parseInt(hex.slice(5, 7), 16) / 255];
}
