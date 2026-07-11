# World Plan: The Clockwork Tomb

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Title Screen | brass gear title art | no | — |
| 1 | descent | The Brass Descent | START. entry stair into tomb | no | S→antechamber |
| 2 | antechamber | Gear Antechamber | intro gear puzzle, brass key #1 | no | N→descent, E→pendulumhall, S→ringgallery |
| 3 | pendulumhall | Hall of Pendulums | timed swings, copper key #1 | no | W→antechamber, E→steamworks |
| 4 | steamworks | Steam Vent Works | steam trap bypass, brass key #2 | no | W→pendulumhall, S→gearvault |
| 5 | gearvault | Gear Vault | moving-gear maze, copper key #2 | yes | N→steamworks, W→ringgallery |
| 6 | ringgallery | Concentric Ring Gallery | align rings with 4 keys | no | N→antechamber, E→gearvault, S→guardianward |
| 7 | guardianward | Guardian Ward | automaton guardians block door | no | N→ringgallery, passage↔corevault |
| 8 | corevault | Core of the Tomb | clockwork heart relic. #endgame | no | passage↔guardianward |

## Progression spine

1. descent → antechamber (free); collect brass key #1.
2. antechamber → pendulumhall → steamworks; collect copper key #1, brass key #2.
3. steamworks → gearvault; solve gear maze, collect copper key #2.
4. gearvault → ringgallery; align concentric rings with 2 brass + 2 copper keys.
5. ringgallery → guardianward; defeat automaton guardians.
6. guardianward → corevault; retrieve clockwork heart. #endgame

## Generation order

title → descent → antechamber → pendulumhall → steamworks → gearvault → ringgallery → guardianward → corevault