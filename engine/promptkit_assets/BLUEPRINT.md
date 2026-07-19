# ZZT semantic board blueprint v1

You are choosing the board's design, not serializing a `.ZZT` file. The host
turns your JSON into exact 60x25 ZWD, assigns legend bytes, creates stat records,
compiles it, simulates it, analyzes routes and ZZT-OOP, and asks for a targeted
repair if an invariant fails. Never count grid columns and never emit ZWD.

## What a good ZZT board needs

- One readable idea and a deliberate silhouette. Use large quiet regions,
  framed rooms, contrasting floor/wall materials, and a few focal details.
- A traversable route from `start` to every required edge port, passage,
  progression item, and finale object. Walls are immutable; Forest, Breakable,
  Fake, items, doors, creatures, and touchable Objects are treated as possible
  route cells by the optimistic route check.
- Mechanics that match the world plan. Put promised keys before matching Doors,
  make paired Passages use the same color, and put `#endgame` in a reachable
  Object on the promised finale board.
- ZZT-scale restraint: 60x25 is small. Prefer 6-20 operations and fewer than 30
  actors. The hard limit is 150 non-player stats.

## JSON shape

Return one raw JSON object (a single `json` fence is also accepted):

```json
{
  "version": 1,
  "board": "Exact board name",
  "start": {"x": 30, "y": 22},
  "dark": false,
  "max_shots": 255,
  "reenter": false,
  "time_limit": 0,
  "message": "optional short entry message",
  "exits": {"north": "Exact target name", "south": "", "west": "", "east": ""},
  "ports": {"north": 30},
  "background": {"element": "Empty", "color": "0x00"},
  "floor": {"element": "Empty", "color": "0x00"},
  "operations": [],
  "actors": []
}
```

All coordinates are 1-based: x=1..60 and y=1..25. Unknown JSON fields are an
error. `background` fills the board before sequential `operations`; later
operations cover earlier ones. `floor` is a traversable tile used to carve the
two cells at every declared edge port. North/south ports are x coordinates;
west/east ports are y coordinates. Every non-empty exit requires its port.
Exact exit target names are supplied in the board request and are not creative
choices.

## Drawing operations

Every non-text operation has `tile: {"element":"...","color":"..."}`.

- `fill`: filled rectangle using `x,y,x2,y2`.
- `border`: one-cell rectangular outline using `x,y,x2,y2`.
- `line`: horizontal or vertical line using `x,y,x2,y2`; optional width 1..5.
- `path`: Manhattan path using `x,y,x2,y2`; optional width 1..5 and `bend` of
  `horizontal-first` or `vertical-first`.
- `tile`: one cell using `x,y`.
- `text`: one horizontal printable-ASCII string using `x,y,text,color`, where
  color is `Text-Blue`, `Text-Green`, `Text-Cyan`, `Text-Red`, `Text-Purple`,
  `Text-Yellow`, or `Text-White`. It has no `tile`.

Use drawing tiles only for terrain and non-stat items. Put Player nowhere: the
host places it at `start`. Put every stat-backed element in `actors`.

Useful terrain and item elements include `Empty`, `Water`, `Forest`, `Solid`,
`Normal`, `Breakable`, `Boulder`, `SliderNS`, `SliderEW`, `Fake`, `Invisible`,
`Line`, `Ricochet`, `Ammo`, `Torch`, `Gem`, `Key`, `Door`, and `Energizer`.
Colors are DOS names (`blue`, `bright-cyan`, etc.) or `0x00`..`0xFF`. For Keys,
use foreground colors such as blue=`0x09`, green=`0x0A`, cyan=`0x0B`, red=`0x0C`,
purple=`0x0D`, yellow=`0x0E`, white=`0x0F`. For Doors, use the matching plain
name (`blue`, `green`, `cyan`, `red`, `purple`, `yellow`, `white`) because the
host encodes the key color into the door background nibble.

## Actors and ZZT-OOP

An actor is:

```json
{
  "element": "Object",
  "x": 12,
  "y": 8,
  "color": "0x0E",
  "character": "?",
  "cycle": 3,
  "step": "idle",
  "p2": 0,
  "p3": 0,
  "under": {"element":"Empty","color":"0x00"},
  "target": "Exact destination board for Passage only",
  "oop": "@name\n:touch\nHello.\n#end"
}
```

Supported actors are `Object`, `Scroll`, `Passage`, `Transporter`, `Pusher`,
`Bomb`, `BlinkWall`, `Duplicator`, `Bear`, `Ruffian`, `SpinningGun`, `Lion`,
`Tiger`, `Slime`, `Shark`, `CentipedeHead`, `CentipedeSegment`, `Bullet`, and
`Star`. The host supplies normal element cycle defaults, so omit `cycle` unless
intentional. Use `character` instead of p1 for an Object glyph; do not provide
both. Passage requires `target`, and only Passage may use it. `under` defaults to
the painted tile being covered. Actor coordinates must be unique and cannot
equal `start`.

OOP is a printable-ASCII JSON string with newline escapes. It becomes data, never executable
host code. Write normal ZZT-OOP: optional `@name`, labels like `:touch`, display
text, commands such as `#end`, `#go`, `#walk`, `#send`, `#set`, `#clear`,
`#give`, `#take`, `#play`, `#shoot`, `#put`, `#change`, `#if`, `#lock`, and
`#unlock`, plus choices `!label;Caption`. Do not invent commands or send to
missing labels. Never include a bare line `end` (that word belongs to ZWD, not
OOP). Keep ordinary display lines at most 42 characters, centered `$` lines at
most 45, choice captions at most 38, and `@` titles at most 45.

## Title board

Board 0 is a decorative splash, not a combat room. Use a clean frame or motif,
one centered `text` operation spelling the exact world name, at most one short
subtitle beneath it, generous empty space, no creatures or collectibles, and an
unobtrusive start. The engine provides its own Play/Restore/Quit menu.

## Output contract

Return only the one JSON object for the requested board. Do not add commentary,
Markdown explanation, ZWD, grid rows, legend keys, or stat records.
