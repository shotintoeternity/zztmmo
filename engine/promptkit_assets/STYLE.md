# ZZT board style analysis — corpus study

Distilled from `llmworld/examples/`: 200 ZWD boards decompiled from ~100
Museum of ZZT games (1997+, high-rated, ZZT-format only) plus the vanilla
worlds shipped with the engine. Two boards were selected per game by a score
favoring object/stat density, authored text, and color variety
(`engine/gen_llmworld_test.go`). This document is the raw material for the
M12.3 generation system prompt: what good ZZT boards actually do, with named
corpus files as evidence.

## Corpus at a glance

- 200 boards; 72 carry ZZT-OOP objects, 79 use text elements for lettering,
  51 use fake walls as floors, 5 are dark boards.
- Stat counts range from a handful to the 150-stat ceiling
  (`SMLLSPCS_board82.zwd` hits it exactly).
- Two games failed decompilation with corrupt boards (STREK1, WEIRD01) and
  are excluded.

## 1. Boards are composed scenes, not tile soup

Every strong board reads as one deliberate picture with a purpose:

- **A border frame first.** Almost every board outlines its playfield in
  Solid/Normal/Breakable walls or scenery (water, moonscape). The interior
  is then subdivided into rooms, corridors, or one large set-piece.
- **One idea per board.** `CUTLASS_board27.zwd` is a transporter-lattice
  gem-hunt arena; `ONAMOON_board19.zwd` is a moonbase interior surrounded by
  cratered terrain; `SEWERS_board17.zwd` is a color showcase. The board name
  states the idea ("$SCRAPPED - Netting", "Moonbase SE (Training Facility)").
- **Landmarks anchor navigation**: a door with a distinct color, a passage
  in a framed alcove, a building with a doorway gap. Exits (board edges)
  line up with paths, not random wall gaps.

## 2. Shading and texture

DOS color attributes (high nibble = background, low = foreground) are the
whole visual vocabulary, and good boards exploit them:

- **Terrain mixing**: `ONAMOON_board19.zwd` builds a moonscape from
  interleaved Solid 0x07, Breakable 0x78, Normal 0x78, and Water 0x78/0x08 —
  gray-on-gray noise that reads as cratered rock. The same trick appears in
  ATOMIC and DARKCIT2 with different palettes.
- **Fake walls are floor carpet** (51/200 boards): Fake 0x70 (gray floor),
  0x07 (dim texture), 0xFF (bright panel) give interiors a filled, furnished
  feel and cost nothing at runtime. Player start squares frequently sit on
  fake-wall floors — spawn logic must tolerate that (M7.1).
- **Background bits as paint**: colors like 0x4E, 0x78, 0x8F use the
  background nibble to fill the cell even when the glyph is sparse.
  Blinking-bit colors (0x80+) appear as deliberate effects on STK-style
  boards (`SEWERS_board17.zwd` objects at 0x89–0x8F).
- **Water as both hazard and shading**: Water 0x1F ringing an arena
  (`CUTLASS_board27.zwd`), Water 0x78 as gray marsh (`ONAMOON_board19.zwd`).

## 3. Text and lettering

79/200 boards draw words directly on the board with text elements:

- **Titles and signage**: a header row of Text-Cyan cells spelling the board
  title (`CUTLASS_board27.zwd` row 3), a giant red "NEW STK--Objects,
  Blinking" banner (`SEWERS_board17.zwd`), a "LUNAR COMBAT PREPAREDNESS
  CARD" poster (`ONAMOON_board19.zwd`).
- The text element's *color byte is the character*: each letter is one cell,
  color = the ASCII code (0x57 = 'W'), element = Text-<color> chooses the
  background hue. In ZWD this shows up as many single-letter legend entries.
- Text is used sparingly and monumentally — a title, a warning, a scoreboard
  label — never paragraphs (paragraphs belong in scrolls/objects).
- **Monospace layout alignment**: When drawing large block letters, structures, or borders, remember that the ASCII art is tiled monospace. Every character cell is a fixed grid cell. Alignment, letter width, character spacing, and vertical proportions must be mathematically/manually aligned. If you draw block letters, ensure they are spelling the correct word and centered perfectly using precise padding, without letter skewing or layout overlap.


## 4. Population: creatures and objects

The boards chosen for density show consistent placement logic:

- **Creatures are themed and zoned**, not sprinkled: `CUTLASS_board27.zwd`
  seeds each maze cell with one of Lion/Tiger/Ruffian/Centipede so every
  pocket has a different threat; heads get their segment chains laid out
  adjacently.
- **Objects are machinery and cast**: 89 legend entries across the corpus
  are Objects — vendors, doors with dialogue, buttons, decorative animated
  props. Objects carry the story; items (gems/ammo/torches) pace the
  economy along the path the player must walk.
- **Centipede sentinels**: real worlds carry off-board (0,0) stats as dead
  centipede-chain placeholders; the decompiler drops them (harmless for
  layout study, boards still load and run).

## 5. ZZT-OOP idioms

The scripts in the corpus are short, ritualized, and voice-heavy:

- **The pickup ritual** (`CUTLASS_board12.zwd`):
  `@Name` / `:touch` / `#play <notes>` / one line of flavor text /
  `#give score|health|gems N` / `#die`. Named objects ("Money Bag",
  "Heart", "Save Gem") make the scroll title read as the item.
- **The gate ritual** (`CUTLASS_board27.zwd`): `:touch` → `#lock` →
  `#if <flag/counter> no` → success sound + `/e/s` movement to open, with a
  `:no` branch playing a failure jingle and one taunting line
  ("You want the treasure? Collect the gems!") → `#unlock`.
- **Progressive dialogue via `#zap`** (`OBELISK_board59.zwd`): repeated
  `:touch` labels, each `#zap touch` burning the previous line, so a door
  says "Still can't get in without the key!" then "(No, there's STILL no
  key!!)" — terse, playful, escalating.
- **Sound is punctuation**: nearly every interaction opens with `#play`;
  pickups get a rising jingle, refusals a falling one.
- The writing voice is second-person, wry, and short — one or two lines per
  beat, exclamation-heavy, parentheticals for asides. Vendor-style menus use
  `!label;text` choice lines (see TASKS.md M3.10's vendor fixture).

## 6. Color conventions worth copying

- Foreground-on-black for actors: 0x0F white, 0x0C red, 0x0A green, 0x0B
  cyan, 0x0D purple, 0x0E yellow — creatures keep their vanilla colors.
- 0x07/0x78/0x08 gray family for stone, machinery, and moonscape.
- Doors/keys use the seven key colors; a door's background nibble names its
  key (Door 0x4D = purple-on-red).
- Blink bit (0x80+) reserved for special effects, not general scenery.

## 7. Structural limits observed in real boards

These match the M12.0 limits table and are routinely approached, not
exceeded: ≤150 stats (hit exactly by Small Spaces), 60×25 grid, one player
per board, exits by name to adjacent boards, OOP blocks a few dozen lines at
most. Dense boards spend their stat budget on transporters/creatures OR on
scripted objects — rarely both at maximum.
