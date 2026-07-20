# World Plan: Bakery Gate Plaza

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Warm Bread Plaza | title art | no | — |
| 1 | plaza | Social Plaza | START. locked bakery gate, fountain landmark | no | N→gate, E→fountain, W→benches |
| 2 | fountain | Fountain Court | wade fountain, retrieve bakery key | no | W→plaza |
| 3 | benches | Bench Corner | idle patrons, hint dialogue | no | E→plaza |
| 4 | gate | Bakery Gate | locked gate needs key | no | S→plaza, N→bakery |
| 5 | bakery | Bakery Interior | reward room, #endgame | no | S→gate |

## Progression spine

1. plaza → benches (free): patron hints key is in fountain.
2. plaza → fountain (free): retrieve bakery key.
3. plaza → gate (free): key opens locked gate.
4. gate → bakery (gated on key): reach reward. #endgame

## Generation order

title → plaza → fountain → benches → gate → bakery