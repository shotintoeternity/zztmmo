// font.ts — Zeta's 8x14 EGA font sheet (pc_ega.png): 256 glyphs as 32 columns
// by 8 rows in CP437 order, so glyph N sits at (N % 32, N / 32). It is loaded
// once into a white-on-transparent canvas, which both the 2D overlay (tinted
// per EGA color) and the 3D atlas texture draw from.

import pcEgaUrl from "./pc_ega.png";
import { paletteColor } from "./palette";

export const GLYPH_COLS = 32;
export const GLYPH_ROWS = 8;
export const CELL_W = 8;
export const CELL_H = 14;

export type Font = {
  /** White glyphs on transparent: the atlas. */
  sheet: HTMLCanvasElement;
  /** One pre-tinted sheet per EGA foreground color. */
  tinted: HTMLCanvasElement[];
};

export function loadFont(): Promise<Font> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const sheet = document.createElement("canvas");
      sheet.width = img.width;
      sheet.height = img.height;
      const ctx = sheet.getContext("2d");
      if (!ctx) {
        reject(new Error("canvas 2d context unavailable"));
        return;
      }
      ctx.drawImage(img, 0, 0);
      const imgData = ctx.getImageData(0, 0, sheet.width, sheet.height);
      const data = imgData.data;
      // The sheet is black-on-white 1-bit. Punch the background out and force
      // the ink to pure white so a tint is the palette color undarkened.
      for (let i = 0; i < data.length; i += 4) {
        if (data[i] + data[i + 1] + data[i + 2] < 50) {
          data[i + 3] = 0;
        } else {
          data[i] = 255;
          data[i + 1] = 255;
          data[i + 2] = 255;
          data[i + 3] = 255;
        }
      }
      ctx.putImageData(imgData, 0, 0);

      const tinted: HTMLCanvasElement[] = [];
      for (let i = 0; i < 16; i += 1) {
        const canvas = document.createElement("canvas");
        canvas.width = sheet.width;
        canvas.height = sheet.height;
        const tctx = canvas.getContext("2d");
        if (tctx) {
          tctx.imageSmoothingEnabled = false;
          tctx.drawImage(sheet, 0, 0);
          tctx.globalCompositeOperation = "source-in";
          tctx.fillStyle = paletteColor(i);
          tctx.fillRect(0, 0, canvas.width, canvas.height);
        }
        tinted.push(canvas);
      }
      resolve({ sheet, tinted });
    };
    img.onerror = () => reject(new Error("font sheet failed to load"));
    img.src = pcEgaUrl;
  });
}
