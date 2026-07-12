# World Plan: The Moonlit Observatory

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Starlit Title | title art with drawn sky | no | — |
| 1 | dometop | Dome Top | START. astronomer greets, asks for lens | no | S→stairhall, passage↔cellar |
| 2 | stairhall | Winding Stair | descent room, star sky above | no | N→dometop, S→landing |
| 3 | landing | Cellar Landing | door puzzle to reach cellar | no | N→stairhall, S→cellar |
| 4 | cellar | Dusty Cellar | dark maze holding the lens | yes | N→landing, passage↔dometop |

## Progression spine

1. dometop → stairhall (free; astronomer's request)
2. stairhall → landing (free descent)
3. landing → cellar (solve door puzzle)
4. cellar: find torch, retrieve lens, take passage↔dometop back
5. dometop: return lens to astronomer, view stars through telescope. #endgame

## Generation order

title → dometop → stairhall → landing → cellar