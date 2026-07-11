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
- **Immersive Writing Style**: Text, dialogue, and scrolls should be interesting, evocative, and deeply immerse the player in the world. Avoid generic, dry, or purely functional placeholders (like "You touched the sign. The exit is north."). Write with personality, flavor, and narrative texture that matches the board's theme (e.g., eerie whispers in a haunted tower, mechanical chatter in a engine room, poetic remnants of historical events). A line or two of highly descriptive flavor text can completely transform the atmosphere of a room.
- **Immediate Touch Feedback**: Every interactive object that can be touched (defines a `:touch` label) **must** provide immediate textual feedback. When touched, they should say something so that the player knows what is happening. This should be conveyed either via direct dialogue (e.g., the character speaking: `Step away from the control panel!`) or via third-person/narrator description (e.g., `The ancient console hums, but the security lockout prevents input.`). Never have a touch trigger a silent gameplay action without accompanying text.
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

## 8. What makes a great ZZT game

A great ZZT game treats the board as both a **place** and a **composition**.
Each screen should read immediately: a room, cave, temple, machine, town
square, riverbank, dungeon, shrine, portrait, artifact, or strange little
stage. The player should understand where they are, what matters, and what
might be dangerous.

ZZT art is often symbolic, but it is not purely abstract. Many strong ZZT
games use selective realism when representing important real-world people,
objects, buildings, machines, monsters, or artifacts on a single board. This
is not always necessary, but when the subject needs to be recognizable,
impressive, or emotionally important, realistic board art can make the scene
land harder.

The visual craft comes from using ZZT's limited tiles like an ASCII drawing
system. Solids, fake walls, breakable walls, water, forests, stars, line
walls, objects, and other elements are not important here because of what
they "are" in the game world. They matter because of how they look on the
board. These elements become marks, tones, densities, and textures.

Solids can create bold filled areas, strong outlines, and hard silhouettes.
Fake walls, breakables, water, and other visually textured elements can be
used for shading, gradients, stippling, crosshatching, highlights, shadows,
and visual noise. With the full STK palette, these tiles can be combined
almost like different brush strokes, letting the author create depth, contour,
texture, atmosphere, and recognizable forms from abstract symbols.

Good ZZT art often comes from blending these elements carefully. A portrait
might use different tile densities to shape a face through contrast and shadow.
A machine might use solids, line walls, stars, and colored objects to suggest
panels, lights, vents, circuitry, and depth. A landscape might use color and
texture to imply distance, water, stone, sky, or foliage without needing
literal representation. The point is not that each tile must "mean" one thing.
The point is that each tile contributes to the image.

Good board style also means avoiding obvious beginner habits. Do not use
bright yellow borders around boards unless there is a very specific reason.
They usually look harsh, artificial, and disconnected from the scene. Borders
should feel like part of the board's architecture, atmosphere, or composition,
not like a default picture frame slapped around the edge.

Avoid rainbow colors, checkerboard spam, noisy textures with no shape, giant
empty rooms, perfectly rectangular spaces with nothing breaking them up, random
STK tiles scattered everywhere, and overusing bright colors just because they
are available. Avoid making every wall the same color and thickness. Avoid
boards where every area is outlined instead of composed. Avoid using
decorations that do not support the scene, route, mood, or gameplay.

Board structure matters as much as art. A strong world has hubs, loops, gates,
shortcuts, optional rooms, locked areas, secrets, and escalating regions. Each
board should have a purpose: exploration, puzzle, combat, story, transition,
reveal, rest, spectacle, or visual centerpiece. Avoid repetitive mazes, dead
rooms, and boards that exist only to pad the game.

The visual design must serve, not hinder, navigation. Use beautiful boards
with lots of art whenever possible to represent the board itself (such as
landscapes, machines, or detailed backgrounds), but make sure the playfield —
the actual path the player walks along and the areas they must visit to progress —
is clean, open, and free of physical obstacles (like solids, normal walls, or
breakable walls). Art belongs in the scenery and borders, not blocking the
player's path.

Gameplay should be simple but expressive: conserve ammo, spend keys carefully,
solve readable puzzles, talk to strange objects, trigger machines, dodge
enemies, uncover secrets, and learn the local rules of the world. Avoid using
generic prefab enemies (such as Tigers, Lions, Centipedes, Ruffians, or Bears) —
they feel generic and noisy. It is much better to build custom enemies, hazards,
and blockers using interactive Objects (`E_OBJECT`) running custom ZZT-OOP
scripts. These custom objects can speak, flash, drop custom loot, make distinct
sound effects, and act like tiny actors with personality: guards, terminals,
priests, vending machines, doors, elevators, bosses, ghosts, or weird machines.
Make sure these custom objects are placed prominently where the player can
easily reach, touch, and interact with them.

The best ZZT games feel handmade, readable, dangerous, funny, and a little
haunted. They do not try to escape ZZT's limits. They turn those limits into
style.

### ZZT-OOP objects: when to use #lock / #unlock

`#lock` sets the object's lock flag (P2=1), preventing it from receiving
messages from other objects or a second player touch while it runs. `#unlock`
clears it. Use them **only** when one of these is true:

- The object runs a **multi-label state machine** and must not be interrupted
  mid-sequence by another actor's message.
- The object has **multiple labels triggered by other objects** (e.g. a gate
  that receives `open`/`close` messages from a switch) and must protect a
  critical block between them.

**Do not use `#lock`/`#unlock` for simple read-only signs and NPCs.** A
single-touch object that shows a scroll and hits `#end` completes in one
OopExecute call — the lock is 0 duration and adds nothing. The correct idiom
for a plain NPC or sign is:

```
@npcname
#end
:touch
#play tcfa
Hello traveler. The dungeon is to the north.
#end
```

Using `#lock`/`#unlock` on every object is cargo-cult style inherited from
workarounds that do not apply to stateless read-only objects. Leave it out.


## 9. Title Screen Layout & Typography Rules

Title screens are the player's first impression. A scrambled or misspelled title banner ruins immersion. When drawing large text titles:

1. **Spell the Planned Game Name**: You must draw the actual name of the game itself (specified in your plan's `WorldName`) as the main title banner on the title screen. Do not use generic placeholder words. Double-check the spelling letter-by-letter, mapping each letter key to its monospaced 3x5 font template from `ZWD.md` (e.g., if the planned name is "DYING STAR", lay out the templates for 'D', 'Y', 'I', 'N', 'G', 'S', 'T', 'A', 'R' exactly).
2. **Monospace Centering Math**:
   * Each 3x5 block letter is 3 columns wide, with 1 column of padding between letters.
   * A word of $N$ letters is exactly $W = 4N - 1$ columns wide.
   * To center the word on the 60-column ZZT screen, your starting column index must be exactly $(60 - W) / 2$.
   * *Example*: `DYING` (5 letters) is $4(5) - 1 = 19$ columns wide. Center it by starting at column $(60 - 19) / 2 = 20$.
3. **Legend Key Drawing Technique**: Use a single letter (like `y` for yellow or `c` for cyan) in the grid to represent the filled pixels of all block letters, and map it to `Text-<Color>` in the legend.
   * *Concrete Example*: To spell out the letters **"A B C"** centered in yellow on a board, use `y` for the letter pixels and `.` for empty space.
     * $W = 4(3) - 1 = 11$ columns wide. Centered starting index in 58 columns of playfield is $(58 - 11) / 2 = 23.5 \rightarrow 23$ padding columns.
     * The grid rows and legend must look exactly like this:
     ```
     Grid:
     w........................y..yy..yyy........................w
     w.......................y.y.y.y.y..........................w
     w.......................yyy.yy..y..........................w
     w.......................y.y.y.y.y..........................w
     w.......................y.y.yy..yyy........................w

     Legend:
     y = Text-Yellow color 0x20
     ```
4. **Atmospheric Contrast**: Frame your title banner with themed ascii drawings (e.g. stars, machinery, mountains) but keep the title text clear and easy to read using `Text-<Color>` elements.
5. **Title Screen Object & Scripting Rules**:
   * **Player is Immobile**: On the title screen (board 0), the player is not playable and cannot move. Therefore, **objects on the title screen can never be touched**.
   * **No `:touch` Labels**: Because collision is impossible, title screen objects must **never** start with `#end` followed by a `:touch` label (or contain `:touch` blocks).
   * **Narrate Immediately**: Instead, use objects that trigger automatically at startup to explain the plot, animate scenery, or set the atmosphere.
   * **Atmospheric/Plot Scrolling**: Use either scrolling text (single-line messages interspersed with wait commands like `/i` for pacing in between lines, e.g. `The cosmos is cold... \n /i \n /i \n A star dies...`) or open a long passage scroll explaining the plot of the game immediately at startup (by placing the text block at the very top of the script so it displays upon board load, rather than behind a touch label).




