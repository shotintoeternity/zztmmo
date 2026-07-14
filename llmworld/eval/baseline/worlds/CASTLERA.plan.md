# World Plan: Castle Ravenmoor

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Castle Ravenmoor | title art, credits | no | — |
| 1 | gate | Broken Gate | START. storm-lashed entry courtyard | no | N→hall |
| 2 | hall | Great Hall | central hub, portrait gallery, locked doors | no | S→gate, E→library, W→chapel, N→stairs |
| 3 | library | Dusty Library | puzzle room, find brass key clue | no | W→hall, N→armory |
| 4 | armory | Armory | combat room, blue key guarded | no | S→library |
| 5 | chapel | Ruined Chapel | dark, holy water pickup, riddle | yes | E→hall, passage↔crypt |
| 6 | crypt | Sunken Crypt | dark dungeon, monster gauntlet, red key | yes | passage↔chapel, N→dungeon |
| 7 | dungeon | Deep Dungeon | dark maze, keeper of the stake | yes | S→crypt, passage↔throne |
| 8 | stairs | Grand Staircase | locked stair, needs three keys | no | S→hall, N→throne |
| 9 | throne | Vampire's Throne | boss arena, vampire lord | no | S→stairs, passage↔dungeon, N→endgame |
| 10 | endgame | Dawn Breaks | #endgame victory board | no | S→throne |

## Progression spine

1. gate → hall (free).
2. hall → library → armory: collect blue key (combat).
3. hall → chapel → crypt (passage): collect red key, grab holy water.
4. crypt → dungeon: defeat keeper, collect brass key.
5. With three keys, hall → stairs unlocks (locked stair).
6. stairs → throne: fight vampire lord (holy water required).
7. throne → endgame. #endgame

## Generation order

title → gate → hall → library → armory → chapel → crypt → dungeon → stairs → throne → endgame