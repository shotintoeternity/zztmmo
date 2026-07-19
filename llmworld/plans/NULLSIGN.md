# World Plan: NULL SIGNAL

## Premise

Rain has fallen upward for nine nights. A relay station at the edge of the
world is broadcasting a voice from nowhere, but its signal path is broken.
Cross the neon orchard, wake the ossuary relay, climb the dark antenna, and
answer whatever is waiting inside the carrier wave.

## Theme and palette

Black space, cold cyan masonry, violet rain, and gray fake-wall floors. Yellow
and bright white are reserved for pickups and the broadcast core. Each board
is built around one large silhouette: the rain tower, orchard trellises, relay
ribs, antenna braces, then concentric rings.

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Title Screen | clean wordmark and dead antenna | no | - |
| 1 | platform | Storm Platform | START. supplies and first combat | no | E->orchard |
| 2 | orchard | Neon Orchard | water bridge and BLUE KEY | no | W->platform E->ossuary |
| 3 | ossuary | Relay Ossuary | BLUE DOOR, wake relay, CYAN KEY | no | W->orchard E->spire |
| 4 | spire | Signal Spire | DARK. CYAN DOOR and paired passage | yes | W->ossuary passage<->core |
| 5 | core | Broadcast Core | answer the awakened signal. #endgame | no | passage<->spire |

## Progression spine

1. platform -> orchard; collect supplies from the operator.
2. orchard: cross the water bridge and acquire the **BLUE KEY**.
3. ossuary: open the **BLUE DOOR** and touch the relay, setting **RELAYAWAKE**.
4. ossuary: take the **CYAN KEY** beyond the relay.
5. spire: open the **CYAN DOOR** and enter the passage.
6. core: the carrier checks `#if RELAYAWAKE`, answers, and runs #endgame.

## Generation order

platform -> orchard -> ossuary -> spire -> core -> title
