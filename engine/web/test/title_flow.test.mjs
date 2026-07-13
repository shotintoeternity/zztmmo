import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/title_flow.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const { selectWorldForTitle } = await import(`data:text/javascript;base64,${source}`);

{
  const selection = selectWorldForTitle("CAVES");
  assert.deepEqual(selection, { worldName: "CAVES", startPlay: false });
}

console.log("title_flow.test.mjs: world selection stays on title passed");
