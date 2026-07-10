#!/usr/bin/env python3
# Builds llmworld/generated/ARCHIVE.zwd — a flooded-library interior board,
# authored (as the LLM) against the M12.3 prompt kit: framed playfield,
# gray-family stone + fake-wall marble floor, monumental lettering used once,
# water shading, key/door gating, and both OOP rituals (pickup + gate/#zap).
W, H = 60, 25

# Start every interior cell as marble floor; frame in stone.
g = [['f'] * W for _ in range(H)]
for x in range(W):
    g[0][x] = '#'
    g[H - 1][x] = '#'
for y in range(H):
    g[y][0] = '#'
    g[y][W - 1] = '#'

def stamp(x, y, s):          # 1-based coords, write string s along row y from col x
    for i, ch in enumerate(s):
        g[y - 1][x - 1 + i] = ch

def rect(x1, y1, x2, y2, ch): # inclusive 1-based border rectangle
    for x in range(x1, x2 + 1):
        g[y1 - 1][x - 1] = ch
        g[y2 - 1][x - 1] = ch
    for y in range(y1, y2 + 1):
        g[y - 1][x1 - 1] = ch
        g[y - 1][x2 - 1] = ch

def fill(x1, y1, x2, y2, ch):
    for y in range(y1, y2 + 1):
        for x in range(x1, x2 + 1):
            g[y - 1][x - 1] = ch

# --- Title lettering: A R C H I V E across row 3, cols 27..33 ---
stamp(27, 3, '1234567')

# --- Bookshelf blocks in the main hall (Breakable, gray) ---
for bx in (4, 13, 22):
    rect(bx, 6, bx + 6, 9, 'B'); fill(bx + 1, 7, bx + 5, 8, 'f')
for bx in (4, 13, 22):
    rect(bx, 12, bx + 6, 15, 'B'); fill(bx + 1, 13, bx + 5, 14, 'f')

# --- Right wing: flooded reading room ---
fill(40, 6, 58, 22, '~')
# a dry stone landing inside the flood, reachable from the hall
fill(40, 12, 45, 14, 'f')

# --- Top-right key chamber, walled in stone, lion guarding a purple key ---
rect(49, 4, 58, 10, '#')
fill(50, 5, 57, 9, 'f')
g[5 - 1][53 - 1] = 'L'   # lion at 53,5
g[7 - 1][55 - 1] = 'k'   # purple key at 55,7
g[8 - 1][50 - 1] = '<'   # doorway gap into the chamber (from the flood landing)
g[8 - 1][49 - 1] = 'f'

# --- Bottom-right alcove behind a purple door: the Codex idol ---
rect(50, 18, 58, 23, '#')
fill(51, 19, 57, 22, 'f')
g[20 - 1][50 - 1] = '+'   # purple door on the alcove wall
g[20 - 1][49 - 1] = 'f'
g[21 - 1][54 - 1] = 'I'   # Codex idol at 54,21

# --- Population: gems as scattered pages, ammo, the archivist ---
for (x, y) in [(9, 18), (17, 20), (25, 18), (33, 21), (11, 10), (20, 16)]:
    g[y - 1][x - 1] = 'g'
g[18 - 1][6 - 1] = 'A'     # ammo at 6,18
g[20 - 1][9 - 1] = 'O'     # Archivist object at 9,20 (open lower hall)
g[20 - 1][5 - 1] = '@'     # player start at 5,20

# --- validate width/height ---
rows = [''.join(r) for r in g]
for i, r in enumerate(rows, 1):
    assert len(r) == W, f"row {i} is {len(r)} wide: {r!r}"
assert len(rows) == H

grid = '\n'.join(rows)

zwd = f'''zwd 1
world "ARCHIVE"

board "The Drowned Archive"
  start player at 5,20
  max-shots 5
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
{grid}
  end

  legend
    . = Empty color 0x07
    f = Fake color 0x70
    # = Solid color 0x07
    B = Breakable color 0x78
    ~ = Water color 0x1F
    < = Empty color 0x07
    + = Door color 0x5F
    k = Key color 0x0D
    L = Lion color 0x0C
    A = Ammo color 0x03
    g = Gem color 0x0B
    O = Object color 0x0F
    I = Object color 0x0E
    @ = Player color 0x1F under Empty color 0x07
    1 = Text-Yellow color 0x41
    2 = Text-Yellow color 0x52
    3 = Text-Yellow color 0x43
    4 = Text-Yellow color 0x48
    5 = Text-Yellow color 0x49
    6 = Text-Yellow color 0x56
    7 = Text-Yellow color 0x45
  end

  stats
    stat at 53,5 element Lion p1 6 p2 0 step idle under Fake color 0x70
    stat at 9,20 element Object cycle 3 p1 cp437:0x02 under Fake color 0x70
    oop
@Archivist
#end
:touch
#play tcfc
Shh. This is a library. Was.
The tide took the east wing, and the
Codex with it -- locked behind the
violet door. The key? A lion has it.
I did say "was" a library.
#zap touch
#end
:touch
(Still shushing. Still no key.
The lion is not much of a reader.)
#end

    end
    stat at 54,21 element Object cycle 1 p1 cp437:0x0F under Fake color 0x70
    oop
@Codex
#end
:touch
#lock
#play +c-e+g+c-e+g#
You lift the Sunken Codex.
Its pages are dry. Impossible.
#give score 500
#play s.-c-a-g
#die

    end
  end
end
'''

import os
out = os.path.join(os.path.dirname(__file__), 'ARCHIVE.zwd')
with open(out, 'w') as f:
    f.write(zwd)
print("wrote", out, len(zwd), "bytes")
print(grid)
