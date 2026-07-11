# World Plan: The Bakery Gate

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Title Screen | title art | no | — |
| 1 | plaza | Town Plaza | START. social hub, locked bakery gate | no | N→fountain, E→bench, W→noticeboard, passage↔bakery |
| 2 | fountain | Fountain Square | wade to retrieve bakery key | no | S→plaza |
| 3 | bench | Bench Corner | chatty NPCs, hint about fountain | no | W→plaza |
| 4 | noticeboard | Noticeboard Nook | lore signs, bakery closed notice | no | E→plaza |
| 5 | bakery | Inside the Bakery | locked; opens with key. #endgame | no | passage↔plaza |

## Progression spine

1. plaza → bench (free): NPCs mention key lost in fountain.
2. plaza → fountain (free): grab bakery key from water.
3. fountain → plaza (free): return with key.
4. plaza → bakery (locked passage): key opens gate. #endgame

## Generation order

title → plaza → fountain → bench → noticeboard → bakery