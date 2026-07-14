# World Plan: Apollo 11 — Sea of Tranquility

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Apollo Eleven | title art, starfield lettering | no | — |
| 1 | padramp | Launch Pad 39A | START. board Saturn V, checklist gate | no | N→command |
| 2 | command | Command Module | switches align for liftoff | no | S→padramp, N→ascent |
| 3 | ascent | Powered Ascent | stage separation, dodge debris | yes | S→command, N→orbit |
| 4 | orbit | Earth Orbit | timing burn to translunar injection | no | S→ascent, N→transit |
| 5 | transit | Translunar Coast | cislunar drift, docking puzzle | yes | S→orbit, E→lunarorbit |
| 6 | lunarorbit | Lunar Orbit | separate LM Eagle from Columbia | no | W→transit, passage↔descent |
| 7 | descent | Powered Descent | dodge boulders, land Eagle | yes | passage↔lunarorbit, N→surface |
| 8 | surface | Tranquility Base | one small step, plant flag, gather samples | no | S→descent, N→liftoff |
| 9 | liftoff | Lunar Liftoff | ascent stage rendezvous with Columbia | yes | S→surface, N→return |
| 10 | return | Transearth Coast | midcourse correction, jettison service module | yes | S→liftoff, E→reentry |
| 11 | reentry | Reentry Corridor | angle the capsule through the plasma | yes | W→return, N→splashdown |
| 12 | splashdown | Pacific Splashdown | recovery raft. #endgame | no | S→reentry |

## Progression spine

1. padramp → command (free)
2. command → ascent (align liftoff switches)
3. ascent → orbit (survive stage separation)
4. orbit → transit (timed TLI burn)
5. transit → lunarorbit (dock CSM to LM)
6. lunarorbit ↔ descent (undock Eagle)
7. descent → surface (land under fuel limit)
8. surface → liftoff (collect samples, plant flag)
9. liftoff → return (rendezvous with Columbia)
10. return → reentry (correction burn, jettison SM)
11. reentry → splashdown (hold reentry angle). #endgame

## Generation order

title → padramp → command → ascent → orbit → transit → lunarorbit → descent → surface → liftoff → return → reentry → splashdown