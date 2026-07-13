import assert from "node:assert/strict";
import { build } from "esbuild";

const output = await build({
  entryPoints: ["src/museum.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
});
const source = Buffer.from(output.outputFiles[0].contents).toString("base64");
const {
  mergeWorldEntries,
  museumNetworkFailureLines,
  museumPlayFailureLines,
  museumResultsToEntries,
} = await import(`data:text/javascript;base64,${source}`);

const local = [{ world: "TEEN", id: "TEEN", title: "Teen Priest", author: "Local", created: "", source: "local" }];
const museum = museumResultsToEntries([
  {
    id: "zzt_teen",
    letter: "t",
    filename: "teen.zip",
    title: "Teen Priest",
    author: ["Draco"],
    releaseDate: "1998-08-31",
    archiveName: "zzt_teen",
  },
  {
    letter: "z",
    filename: "zigzag-and-crystal-maze.zip",
    title: "Zigzag and the Crystal Maze",
    author: ["Benco"],
    releaseDate: "1997-04-01",
  },
]);

assert.equal(museum[0].world, "TEEN");
assert.equal(museum[0].source, "museum");
assert.equal(museum[0].author, "Draco");
assert.equal(museum[1].world, "ZIGZAG-A");
assert.equal(museum[1].filename, "zigzag-and-crystal-maze.zip");
assert.deepEqual(mergeWorldEntries(local, museum).map((entry) => entry.world), ["TEEN", "ZIGZAG-A"]);

assert.deepEqual(museumPlayFailureLines("world failed validation"), ["", "  Not playable: world failed validation", ""]);
assert.deepEqual(museumNetworkFailureLines(), ["", "  The Museum did not answer.", ""]);

console.log("museum.test.mjs: mapping and failure windows passed");
