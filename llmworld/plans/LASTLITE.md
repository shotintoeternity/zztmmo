# World Plan: THE LAST LIGHTHOUSE

The Phase-1 artifact of plan-then-paint generation: everything the per-board
generator needs to paint boards that cohere, and everything the validator
needs to check solvability before a single board is generated. This file
doubles as the reference format for the M12.4 planner.

## Premise

You are the lighthouse keeper's kid. Three nights ago the lamp went out, and
the tide that came in with the dark was not the usual tide. The village is
half-drowned, something walks the moor, and the keeper is missing. Climb to
the lamp room and relight the light.

## Theme and palette

Coastal-gothic. Grays and blues dominate: stone in Solid 0x07/0x08 with
Breakable 0x78 rubble, sea in Water 0x1F and 0x9F, drowned ground as Fake
0x70/0x07 carpet. Forest 0x20 for marsh scrub. Warm colors are *rationed*:
yellow/red appear only in lamplight, torches, and the finale — so the relit
lamp reads as the emotional payoff. Text lettering in Text-White for signs,
Text-Blue for the sea-things' graffiti.

## Board graph

| # | id        | name                    | concept                                     | dark | exits/links                       |
|---|-----------|-------------------------|---------------------------------------------|------|-----------------------------------|
| 0 | title     | Title screen            | lighthouse silhouette, dead lamp            | no   | —                                 |
| 1 | saltroad  | The Salt Road           | START. coastal approach, tutorial creatures | no   | E→village                         |
| 2 | village   | The Drowned Village     | HUB. half-flooded square, signposts         | no   | W→saltroad N→moor E→pier S→cellar (passage) |
| 3 | fishrow   | Fisher's Row            | ruined cottages, BLUE KEY in a locked house | no   | E→moor (via fence gap)            |
| 4 | cellar    | The Tide Cellar         | DARK. flooded undercroft, BLUE DOOR, lever  | yes  | passage↔village                   |
| 5 | moor      | The Moor of Eyes        | creature gauntlet, RED KEY at the cairn     | no   | S→village W→fishrow E→cliffstair  |
| 6 | pier      | The Shattered Pier      | vendor object sells torches for gems        | no   | W→village                         |
| 7 | cottage   | Keeper's Cottage        | story beat: keeper's journal (scrolls)      | no   | passage↔cliffstair                |
| 8 | cliffstair| The Cliff Stair         | vertical switchback climb, wind pushers     | no   | W→moor N→lighthouse, passage↔cottage |
| 9 | lighthouse| Lighthouse Ground Floor | RED DOOR, spiral base, drowned graffiti     | no   | S→cliffstair, passage→lamproom    |
| 10| lamproom  | The Lamp Room           | FINALE. relight the lamp                    | no   | passage↔lighthouse                |
| 11| undertow  | The Undertow            | DARK. optional gem hoard, worth the torches | yes  | passage↔cellar                    |

## Progression spine (solvability walk)

1. saltroad → village (free).
2. village → fishrow via moor edge OR directly W path: **BLUE KEY** sits in
   Fisher's Row behind a breakable-wall house guarded by ruffians.
3. village passage → cellar (dark; torches from starting supply or vendor).
   **BLUE DOOR** in cellar → lever object sets flag **TIDEGATE**.
4. TIDEGATE drains the moor's south channel: the moor's gauntlet is
   passable (its gate object checks `#if TIDEGATE`). **RED KEY** at the cairn.
5. moor → cliffstair → lighthouse ground floor: **RED DOOR** → passage to
   lamproom.
6. lamproom: the dead lamp object takes 5 gems ("lenses") to relight →
   victory text + #endgame. Gems are scattered ≥8 across boards so the
   count always closes.

Economy: torches gated by the vendor (gems), gems free on boards. Ammo only
on saltroad and moor. Health via hearts in cottage and undertow.

## Board I want to build first: The Lamp Room

The finale, because it cashes the palette rule. Circular lamp chamber at the
top of the tower: the outer ring is Normal 0x08 masonry with glass windows
(Breakable 0x19, blue-on-cyan) looking onto night sky (Empty 0x01 field with
Text-Blue star glyphs). Center dais in Fake 0x70 steps up to THE LAMP — an
Object (char 0x0F ☼, color 0x0E) that is *dark* until fed 5 gems, then:
`#play` a rising fanfare, repaint itself 0x8E (blinking yellow), `#change`
the window ring's night cells to yellow lamplight sweeping outward, victory
scroll, `#endgame`. One companion object: the keeper's parrot(?) — no,
keep it lonely; the keeper's hat on the floor and one scroll. The only
warm colors in the whole world appear on this board, in the moment of
winning. 150-stat budget spent almost entirely on the window/light cells
that flip color.

## Generation order

hub-first: village → saltroad, moor, pier, cellar → fishrow, cliffstair,
cottage → lighthouse, undertow → lamproom → title (title last, it's a
portrait of the finished lighthouse).
