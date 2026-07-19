# Corpus-derived ZZT style, expressed semantically

This guidance comes from decompiled community ZZT boards. It describes design
choices only; the host renderer owns grid bytes, legends, stats, and ZWD syntax.

## Composed scenes, not tile soup

A strong board reads at a glance. It usually has one dominant silhouette: a
framed arena, streets around buildings, rooms along a corridor, an island in
water, a machine around a central chamber, or a monumental title. Establish
large regions first, then add a few details that reinforce the idea. Scattered
one-cell decoration rarely looks intentional.

Use hierarchy:

- one dominant shape or route;
- one or two secondary structures;
- a limited accent material or color for objectives and danger;
- empty space around text, characters, and important items.

Frames are common because the board itself is a screen-sized stage. An outer
frame, inset room, or strong horizon makes even a simple encounter feel placed.
Break symmetry only where it directs the player or tells a story.

## Palette and material

Classic ZZT boards get richness from restrained DOS colors rather than from
many unrelated hues. Gray, white, blue, cyan, brown, and black make dependable
structural families; bright colors work best as small accents. Let one material
own a surface: Normal or Solid for architecture, Fake for walkable patterned
floors, Water for a bold boundary, Forest for permeable texture, Breakable for
fragile barriers, and Line for machinery or fine framing.

Shading is architectural. A darker outline beside a lighter wall can imply
depth; an alternating floor can suggest stone, carpet, circuitry, or water
glare. Keep repeated texture rhythmic and subordinate to navigation. Avoid
rainbow noise unless the premise genuinely calls for spectacle.

Color is also mechanics. Keys and Doors must share a color and should be easy
to distinguish from scenery. Passage pairs should share a color. Reserve a
consistent accent for interactive Objects, objectives, or hazards so the player
learns the board's visual language.

## Routes and play

The player's route should be legible before it is difficult. Connect the start,
edge ports, passages, promised items, and finale with deliberate corridors or
open rooms. Gates should communicate their solution: a visible colored Door,
an earlier matching Key, a suspicious Fake wall, a breakable seam, or an Object
whose dialogue gives a clue. Do not hide required progress behind arbitrary
immutable scenery.

Good boards combine a small number of systems rather than using every element:

- one terrain constraint plus one enemy family;
- a key-and-door branch around a central route;
- an Object conversation that changes a flag and opens a later response;
- a dark board whose torches and sight lines are intentionally placed;
- a passage network expressed as a physical machine, transit stop, or portal.

Place creatures in formations, patrol lanes, nests, or guarded pockets. A lone
enemy can be comic punctuation; many random enemies feel like debris. Give
combat space to breathe and do not make the only route depend on an implausible
fight. Items should reward exploration or prepare the player for a clear need.

## Text and title boards

On-board text is signage and graphic design, not a substitute for dialogue.
Use short labels, one-line location names, or a clean title. Interactive prose
belongs in Object or Scroll OOP, where it can pace itself in a ZZT text window.

Title board 0 is a splash screen. Spell the exact world name once in a centered
horizontal text band, with at most one short subtitle beneath it. Use a single
coherent text color or a deliberate small palette, a thin frame or one compact
motif, and generous negative space. It should contain no combat, creatures,
collectibles, or hand-drawn menu instructions. The player start is present only
because the file format requires it and should be visually unobtrusive.

## Objects, writing, and OOP

Objects are both cast and machinery. Their displayed character and color should
fit their role: a face, terminal, sign, lever, statue, vehicle, or abstract
symbol. Place them where a player naturally approaches them. A Scroll is best
for a document or one-use explanatory beat; an Object is better for a recurring
character, stateful interaction, trigger, or finale.

Memorable ZZT writing is short, specific, and aware of the physical board. A
voice may be dry, absurd, officious, melancholy, or warmly observant, but it
should not sound like generic quest text. Prefer two sharp lines over a wall of
exposition. Let descriptions notice the cheapness of a prop, the anxiety of a
machine, or the bureaucratic reason a monster is blocking a corridor.

Use labels as explicit interaction states. `:touch` handles contact. Named
labels support choices and messages. Flags should describe durable world facts,
be set before they are checked, and stay within ZZT's small global flag budget.
Use `#send` only to real labels. End scripts deliberately with `#end`; put
`#endgame` only in the reachable finale promised by the plan. Use `#play` as
punctuation rather than constant noise.

Keep text-window lines compact and wrap at natural phrase boundaries. Choices
should name the action or attitude, not say merely “yes” and “no.” If an Object
changes state, let its later dialogue acknowledge that change. Mechanical OOP
and prose should reinforce the same board idea.

## Cohesion across boards

Repeat a few motifs across the world—a material, transit color, border rhythm,
kind of signage, or recurring voice—while giving each board its own silhouette.
Directional neighbors should meet at compatible port coordinates. Passage
destinations should look and read like paired endpoints. The progression spine
is authoritative: every promised key, door, flag, passage, and finale must be
visible in the board designs that own it.
