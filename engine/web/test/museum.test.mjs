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

const local = [
  { world: "TEEN", id: "TEEN", title: "Teen Priest", author: "Local", created: "", source: "local" },
  { world: "YAPOK", id: "YAPOK", title: "YAPOK", author: "Unknown", created: "", source: "local" },
];
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
  {
    id: "zzt_yapok",
    letter: "y",
    filename: "yapok.zip",
    title: "Yapok Sundria",
    author: ["Yapok Jr."],
    releaseDate: "1995-10-22",
    archiveName: "zzt_yapok",
  },
  {
    id: "zzt_yapokstk",
    letter: "y",
    filename: "yapokstk.zip",
    title: "Yapok Sundria (Unofficial STK Edition)",
    author: ["Unknown", "Yapok Jr."],
    releaseDate: "",
  },
]);

assert.equal(museum[0].world, "TEEN");
assert.equal(museum[0].source, "museum");
assert.equal(museum[0].author, "Draco");
assert.equal(museum[1].world, "ZIGZAG-A");
assert.equal(museum[1].filename, "zigzag-and-crystal-maze.zip");
assert.equal(museum[3].author, "Yapok Jr.", "Unknown co-author labels are dropped when a known author exists");
const merged = mergeWorldEntries(local, museum);
assert.deepEqual(merged.map((entry) => entry.world), ["TEEN", "YAPOK", "ZIGZAG-A", "YAPOKSTK"]);
assert.deepEqual(
  merged.find((entry) => entry.world === "YAPOK"),
  { world: "YAPOK", id: "zzt_yapok", title: "Yapok Sundria", author: "Yapok Jr.", created: "1995-10-22", source: "local" },
  "local files are enriched from Museum metadata but stay local/selectable",
);

assert.deepEqual(museumPlayFailureLines("world failed validation"), ["", "  Not playable: world failed validation", ""]);
assert.deepEqual(museumNetworkFailureLines(), ["", "  The Museum did not answer.", ""]);

console.log("museum.test.mjs: mapping and failure windows passed");
