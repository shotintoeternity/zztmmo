# ZWD: ZZT World Description

ZWD is a text source format for describing a ZZT world before compiling it to a
vanilla `.ZZT` file. It is designed for humans and LLMs to write, and for the
compiler to reject precisely when the result cannot become a faithful ZZT world.

ZWD never embeds binary board data, never executes generated text, and never
writes `World.Info.Flags`. Flags are shared runtime puzzle state, not authoring
input.

## Grounding

ZWD maps directly to the engine structs and serializer:

| ZWD concept | Engine field | Binary format |
| --- | --- | --- |
| World name | `TWorld.Info.Name` | world header `WorldName` |
| Boards | `TWorld.BoardData`, `TWorld.BoardLen` | boards after the 512-byte world header |
| Board name | `TBoard.Name` | board header `BoardName` |
| Board grid | `TBoard.Tiles[1..60][1..25]` | RLE tile stream |
| Board properties | `TBoard.Info` | board property block |
| Element name | `ElementDefs[*].Name` | tile/stat element id |
| Stat | `TBoard.Stats[0..StatCount]` | status element record |
| OOP text | `TStat.Data`, `DataLen`, `DataPos` | optional code block after a stat |

The binary file stores 60 by 25 board tiles as 3-byte runs of `{count, element,
color}`. It stores each status element as a 33-byte record followed by code only
when `DataLen > 0`. The first status element, stat `0`, is always the player.

## File Shape

A ZWD file is UTF-8 text, but compiled ZZT strings are byte-counted after CP437
encoding. Non-printable CP437 bytes are written as `cp437:0xNN`.

Comments start with `#` when `#` is the first non-space character on a line,
except inside fenced OOP blocks.

```zwd
zwd 1
world "SHORTNAME"

board "Title screen"
  start player at 30,12
  max-shots 255
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
  ... exactly 25 rows, each exactly 60 legend characters ...
  end

  legend
    . = Empty color 0x0F
  end

  stats
  end
end
```

The `zwd 1` line is mandatory. Unknown top-level sections are errors.

## World Header

```zwd
world "TOWNLIKE"
```

| Field | Rule |
| --- | --- |
| Name | 1 to 20 bytes after CP437 encoding. Use the intended `.ZZT` stem. |
| Board count | Implied by the number of `board` sections. Board `0` is the title screen. |
| Current board | Compilers should emit board `0` for fresh worlds unless a future ZWD version adds a start-board field. |
| Player inventory | Defaults to vanilla new-world values: health 100; ammo, gems, torches, score, torch ticks, energizer ticks, and board time all zero; all keys false. |
| Flags | Not allowed in ZWD. Runtime flags are global shared state and start empty. |
| Save/locked byte | Must compile as a normal editable world, not a save. |

The world may contain at most 101 boards: board `0` plus boards `1..100`.

## Board Sections

```zwd
board "Armory"
  start player at 30,23
  max-shots 4
  dark false
  reenter true
  time-limit 0
  exits north "Town" south none west none east "Market"
  message ""
  ...
end
```

| Field | Engine mapping | Rule |
| --- | --- | --- |
| Board name | `TBoard.Name` | 0 to 50 bytes. Names do not have to be unique, but references are clearer when they are. |
| `start player at X,Y` | `TBoard.Info.StartPlayerX/Y`, stat `0` | Exactly one player start per board. Coordinates are 1-based, `1..60` and `1..25`. |
| `max-shots N` | `TBoard.Info.MaxShots` | `0..255`. `255` is the default from `BoardCreate`. |
| `dark BOOL` | `TBoard.Info.IsDark` | `true` or `false`. |
| `reenter BOOL` | `TBoard.Info.ReenterWhenZapped` | `true` or `false`. |
| `time-limit N` | `TBoard.Info.TimeLimitSec` | `0..32767` seconds. `0` disables the timer. |
| `exits` | `NeighborBoards[0..3]` | `north`, `south`, `west`, `east`, each `none` or a board name. Compilers resolve names to board ids; `none` compiles as `0`. |
| `message` | `TBoard.Info.Message` | Optional, 0 to 58 bytes. Normally empty. |

The compiler should reject an exit pointing at board `0`, because ZZT uses zero
as "no exit" for board edges.

## Grid

Each board has one `grid` block. It is exactly 25 rows, and each row is exactly
60 legend characters after parsing escapes.

Every single character cell in the grid (including literal spaces `" "` if they are present) **must** have an explicit matching entry in the `legend` section. Unmapped characters will fail compilation. To avoid easy-to-miss alignment errors, it is highly recommended to use `.` for Empty tiles and **never use literal spaces `" "` in grid rows** unless they are explicitly mapped in the legend (e.g. `  = Empty color 0x00`).

```zwd
grid
############################################################
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................@.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
############################################################
end
```

The player start coordinate must correspond to exactly one grid cell whose
legend entry is `Player`. The compiler writes stat `0` there and uses the
legend entry's `under` tile if supplied; otherwise the player's under tile is
`Empty color 0x00`.

## Legend

Legend entries map a single grid character to a tile. The left-hand side is one
visible character or `cp437:0xNN` when the legend key itself is not printable.

```zwd
legend
  # = Solid color 0x0E
  . = Empty color 0x0F
  @ = Player color 0x1F under Empty color 0x00
  g = Gem color yellow
  k = Key color blue
  + = Door color blue
  p = Passage color green to "Armory"
  o = Object color 0x0F
end
```

Element names are matched against `ElementDefs[*].Name` after removing spaces,
hyphens, underscores, and case. Numeric ids are allowed as `element 21`, but
names are preferred because they follow the engine's `ElementDefs`.

Colors may be hexadecimal DOS attributes (`0x1F`) or color names:

| Name | Value |
| --- | --- |
| black | `0` |
| blue | `1` |
| green | `2` |
| cyan | `3` |
| red | `4` |
| purple | `5` |
| brown | `6` |
| white | `7` |
| gray | `8` |
| bright-blue | `9` |
| bright-green | `10` |
| bright-cyan | `11` |
| bright-red | `12` |
| bright-purple | `13` |
| yellow | `14` |
| bright-white | `15` |

For stored tile colors, `color 0xFG` means DOS foreground `G` on background `F`,
matching ZZT's color byte. For elements whose editor color is a choice sentinel
such as `COLOR_CHOICE_ON_BLACK`, `COLOR_WHITE_ON_CHOICE`, or
`COLOR_CHOICE_ON_CHOICE`, the compiler stores the final board color byte, not
the sentinel.

Text tiles use the `Text` element family (`E_TEXT_BLUE` through
`E_TEXT_WHITE`). A text grid cell stores the displayed byte in the tile color
field, so a future shorthand like `Text "Hi" color yellow` must expand to one
text tile per grid cell rather than storing a string in one cell.

## Stats

Every active element beyond the player is described in the `stats` block. A
stat's board tile must use the same element at the same coordinate.

```zwd
stats
  stat at 24,12 element Lion cycle 2 p1 6 under Empty color 0x00
  stat at 31,12 element Passage cycle 0 p3 board "Armory" under Empty color 0x00

  stat at 34,10 element Object cycle 3 p1 cp437:0x02 step idle under Empty color 0x00
  oop
  @guide
  "Welcome to a tiny ZWD world!"
  #end
  end
end
```

| Field | Engine mapping | Rule |
| --- | --- | --- |
| `at X,Y` | `TStat.X/Y` | Required. 1-based board coordinate. |
| `element NAME` | tile element and stat element | Required. Must be an element with a tick proc, touch proc, OOP, or text behavior. |
| `cycle N` | `TStat.Cycle` | Required unless the element has a non-negative `ElementDefs` default. Valid `0..32767`. |
| `p1`, `p2`, `p3` | `TStat.P1/P2/P3` | Optional bytes, defaulting to `InitEditorStatSettings`: p1=4, p2=4, p3=0, except object p1=1 and bear p1=8. |
| `p3 board "NAME"` | `TStat.P3` | Board-name shorthand for elements whose `ParamBoardName` is set, such as passages. |
| `step DIR` | `TStat.StepX/Y` | `idle`, `north`, `south`, `west`, `east`, or explicit `dx,dy` int16 values. Use names when `ParamDirName` is set. |
| `under ELEMENT color C` | `TStat.Under` | Optional. Defaults to `Empty color 0x00`. Required when a stat starts over non-empty terrain. |
| `follower`, `leader` | `TStat.Follower/Leader` | Optional. Defaults to `-1`. Use only for centipedes or imported worlds. |
| `data-pos N` | `TStat.DataPos` | Optional. Defaults to `0`. `-1` means pre-ended OOP. |
| `oop ... end` | `TStat.Data`, `DataLen` | Optional fenced block. Stored as bytes exactly after line-ending normalization. |
| `bind INDEX` | negative `DataLen` | Optional advanced feature. Binds this stat's code to a previous stat index. Not allowed with `oop`. |

The compiler assigns stat ids in source order after stat `0` for the player.
This matters for centipede `leader`/`follower` and for advanced `bind` use.

Parameter aliases should follow the names on `ElementDefs`: `Param1Name`,
`Param2Name`, `ParamBulletTypeName`, `ParamBoardName`, `ParamDirName`, and
`ParamTextName`. If an element has no such parameter, the compiler still accepts
raw `p1`, `p2`, and `p3` bytes, but it should warn when a named alias is used on
the wrong element.

## Passage vs. Object: Critical Distinction

**Passages** and **Objects** look visually similar (both can display any CP437 glyph) but are fundamentally different and must NEVER be confused:

| Feature | `Passage` | `Object` |
|---|---|---|
| Purpose | Teleports player to another board | Scripted interactive element |
| Required ZWD field | `p3 board "NAME"` | `oop ... end` block |
| Behavior when touched | Player is instantly teleported | Object's `:touch` handler fires |
| Has ZZT-OOP code? | **Never** | **Always** (at minimum `@name\r#end\r:touch`) |
| Legend element | `Passage color 0xNN to "BOARD"` | `Object color 0xNN` |

**Rules:**
1. Use `element Passage` **only** when the stat teleports the player to a named board. It **must** have `p3 board "BOARD NAME"`, and **must not** have an `oop` block.
2. Use `element Object` for everything else: signs, NPCs, interactive props, doors controlled by flags, vendors, etc. It **must** have an `oop` block with a name and at least a `:touch` label.
3. **Do not use Object with a passage glyph (`cp437:0xF0`)** to simulate a passage — this creates a dead, unresponsive tile since the engine's passage teleport logic only fires for the `Passage` element, not `Object`.
4. Each board exit to a neighboring board **must** use `exits north/south/east/west "BOARD NAME"` in the board header, **not** a passage element on the board edge.


## Limits

| Limit | Value | Reason |
| --- | --- | --- |
| Boards | `0..100` non-title boards, 101 stored boards total | `MAX_BOARD = 100`; board `0` is title screen. |
| Board grid | `60x25` | `BOARD_WIDTH = 60`, `BOARD_HEIGHT = 25`. |
| Board name | `<=50` bytes | `TString50`, stored as length plus 50 bytes. |
| World name | `<=20` bytes | World header stores length plus 20 bytes. |
| Flags | exactly 0 authored flags | ZWD cannot initialize shared runtime flags. |
| Stats per board | `<=150` non-player stats plus stat `0` player | `MAX_STAT = 150`; binary `StatElementCount` excludes player. |
| Stat record | 33 bytes plus optional code | `SizeOfStat = 33`. |
| Board data | `<=20000` bytes after compression and stat code | Engine scratch buffer `TIoTmpBuf [20000]byte`; board RLE plus info plus stats plus OOP must fit. |
| OOP block | `<=32767` bytes per stored code block | `DataLen` is signed int16; negative values mean `#bind`. |
| Bound OOP | bind target must be a previous stat `1..StatCount` | Negative `DataLen` stores the target stat index; stat `0` cannot be a bind target. |
| Color nibbles | `0..15` foreground and background | DOS color attributes are 4-bit foreground plus 4-bit background. |
| Player starts | exactly 1 per board | Stat `0` is always the player-controlled element. |
| Coordinates | `1..60`, `1..25` | File coordinates are 1-based inside the board. |
| String fields | byte-counted, not rune-counted | ZZT stores Pascal strings. |

The compiler should estimate compressed board length by running the same RLE as
`BoardClose`, not by counting grid characters. The worst-case tile stream alone
is `1500 * 3 = 4500` bytes, but OOP blocks can push a board over the 20000-byte
scratch limit.

## Example: One-Room Greeting

```zwd
zwd 1
world "HELLO"

board "Title screen"
  start player at 30,12
  max-shots 255
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
  ############################################################
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #............................o.............................#
  #............................@.............................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  ############################################################
  end

  legend
    # = Solid color 0x0E
    . = Empty color 0x0F
    @ = Player color 0x1F under Empty color 0x00
    o = Object color 0x0F
  end

  stats
    stat at 30,11 element Object cycle 3 p1 cp437:0x02 under Empty color 0x00
    oop
    @hello
    "This world was written as text."
    "The compiler turns it into real ZZT."
    #end
    end
  end
end
```

## Example: Two Boards With a Passage

```zwd
zwd 1
world "TWOROOMS"

board "Title screen"
  start player at 30,19
  max-shots 4
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
  ############################################################
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #............................p.............................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #............................@.............................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  ############################################################
  end

  legend
    # = Solid color 0x0E
    . = Empty color 0x0F
    @ = Player color 0x1F under Empty color 0x00
    p = Passage color 0x2F to "Vault"
  end

  stats
    stat at 30,12 element Passage cycle 0 p3 board "Vault" under Empty color 0x00
  end
end

board "Vault"
  start player at 30,19
  max-shots 4
  dark true
  reenter true
  time-limit 0
  exits north none south none west none east none

  grid
  ############################################################
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #.........................g.g.g............................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #............................!.............................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #............................@.............................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  #..........................................................#
  ############################################################
  end

  legend
    # = Solid color 0x0E
    . = Empty color 0x0F
    @ = Player color 0x1F under Empty color 0x00
    g = Gem color yellow
    ! = Scroll color 0x0F
  end

  stats
    stat at 30,12 element Scroll cycle 1 under Empty color 0x00
    oop
    You found the vault.
    Bring a torch next time.
    #end
    end
  end
end
```

## ZZT-OOP Language Syntax Guidelines

When writing code inside `oop ... end` blocks for ZZT objects or scrolls, you must follow the strict rules of ZZT-OOP:

1. **No Quotes for Dialogue/Text**: Dialogue or flavor text lines must **NEVER** be wrapped in double quotes. Write them as plain raw text. Wrapping them in quotes causes the literal quote characters to be rendered on screen.
   * *Incorrect*: `"Welcome to my shop!"`
   * *Correct*: `Welcome to my shop!`
2. **Left-Align All Code (No Indentation)**: Inside the ZWD `oop ... end` block, all ZZT-OOP lines (commands, text, labels, etc.) must start at column 1 (left-aligned with no leading spaces). Any leading space on a line causes the engine to treat the entire line as literal text rather than a command or label.
   * *Incorrect*:
     ```
         oop
         @baker
         #end
         :touch
         "Hello"
         end
     ```
   * *Correct*:
     ```
         oop
     @baker
     #end
     :touch
     Hello
     end
     ```
3. **Labels**: Define a message handler label using a colon prefix (e.g. `:touch`, `:bench`). Label names are case-insensitive.
4. **Commands**: Prefix commands with `#` (e.g. `#play`, `#give`, `#take`, `#end`, `#endgame`, `#lock`, `#unlock`, `#zap`).
5. **Initial Halt**: If an object defines a `:touch` label or other label, place `#end` on the line immediately after the name to prevent the object from executing the label's code automatically when the board loads.
6. **Movement**: Use `/` (force move) or `?` (try move) followed by direction (`n`, `s`, `w`, `e`).
7. **Local Board Scope**: Objects can only send direct messages (e.g. `#send target:label`) to other objects *in the same room (board)*. Objects in other rooms are frozen and cannot receive messages. To trigger events across different boards, you must use global flags (`#set flagname` on one board, and `#if flagname` on the other). The global flag limit is exactly 10 flags.

## Element Reachability and Accessibility

When placing elements on a board's grid, you must ensure that all interactive objects and items are physically accessible to the player:

1. **Adjacent Accessibility**: Any tile that the player needs to touch, talk to, or collect (such as an `Object`, `Scroll`, `Passage`, or items like a `Key`, `Gem`, or `Ammo`) must be placed adjacent to a walkable tile (e.g., `Empty`, `Fake` wall, or `Forest`).
2. **Impassable Barriers**: You must **never** place interactive objects or items inside solid structures (like `Solid` walls or `Normal` walls) or completely surround them with impassable terrain (like `Water`, unless a `Transporter` or bridge is provided). The player cannot walk on or touch these tiles, making the game unplayable.
3. **Stat-to-Tile Mapping**: Every `Object`, `Scroll`, and `Passage` tile placed in the grid **must** have a corresponding entry in the board's `stats` block at the exact same coordinate. Placing stat-backed tiles in the grid without stats causes the game engine to crash.
4. **Board Edge Exits vs. Grid Layout**: If a board has an exit in the header (e.g. `exits north "NextBoard"`), you must ensure the corresponding edge of the grid (e.g. the top row) has walkable openings (Empty or Fake tiles). If the edge is entirely blocked by Solid/Normal walls, the player cannot step off the board to transition.
5. **Passage Color Matching**: When linking boards via passages, always color-code them. If Board A has a passage to Board B, then Board B **must** have a passage pointing back to Board A of the **exact same color**. ZZT matches passage teleports strictly by color; mismatched colors will drop the player at the board's default start coordinates instead of the passage.
6. **Key and Door Pairing**: ZZT only supports 7 key/door colors: `blue`, `green`, `cyan`, `red`, `purple`, `yellow`, `white`. Always place the Key color in a reachable area *before* the Door color that requires it. A player can only carry one key of each color at a time.


## Monospace 3x5 Block Letter Reference Font

If you are drawing large title banners or text signage on a board, you must lay them out mathematically in the grid using the following 3x5 font templates. 
* Every letter is 3 characters wide and 5 lines tall. 
* Separate adjacent block letters with exactly one empty tile column (`.`).
* All keys representing these letters in the grid **must** map to `Text-<Color>` elements in the legend (e.g. `Text-Yellow`), **never** to solid/normal walls or objects, to avoid legend mapping collisions.

```
A:     B:     C:     D:     E:     F:     G:     H:     I:     J:
AAA    BB.    CCC    BB.    EEE    EEE    CCC    H.H    III    .JJ
A.A    B.B    C..    B.B    E..    E..    C..    H.H    .I.    ..J
AAA    BB.    C..    B.B    EEE    EEE    C.G    HHH    .I.    ..J
A.A    B.B    C..    B.B    E..    E..    C.C    H.H    .I.    J.J
A.A    BB.    CCC    BB.    EEE    E..    CCC    H.H    III    .JJ

K:     L:     M:     N:     O:     P:     Q:     R:     S:     T:
K.K    L..    M.M    N.N    OOO    PP.    QQQ    RR.    SSS    TTT
K.K    L..    MMM    NNN    O.O    P.P    Q.Q    R.R    S..    .T.
KK.    L..    M.M    N.N    O.O    PP.    Q.Q    RR.    SSS    .T.
K.K    L..    M.M    N.N    O.O    P..    Q.Q    R.R    ..S    .T.
K.K    LLL    M.M    N.N    OOO    P..    QQQ.   R.R    SSS    .T.

U:     V:     W:     X:     Y:     Z:     ':
U.U    V.V    W.W    X.X    Y.Y    ZZZ    ...
U.U    V.V    W.W    .X.    Y.Y    ..Z    .T.
U.U    V.V    W.W    .X.    YYY    .Z.    ...
U.U    V.V    W.W    .X.    ..Y    Z..    ...
UUU    .V.    .W.    X.X    ..Y    ZZZ    ...
```

## Compiler Expectations

The compiler must:

1. Parse with line and column positions and return errors that name the board,
   row, and field where possible.
2. Resolve all board-name references after parsing the complete file.
3. Initialize `ElementDefs` before validating element names and default cycles.
4. Build `TBoard` values, run the same serialization path used by `BoardClose`,
   and reject any board whose serialized length exceeds the engine scratch
   buffer.
5. Load the compiled bytes back through `worldReadFrom` and step the headless
   validation gate before accepting the world.
6. Preserve OOP text byte-for-byte except for normalizing line endings to `\n`.
7. Reject unknown elements, malformed colors, duplicate legend keys, missing
   grid symbols, missing player starts, impossible stat coordinates, and any
   field that would be silently truncated by ZZT's binary format.

## References

- `engine/gamevars.go`: `TWorldInfo`, `TBoardInfo`, `TBoard`, `TStat`, element
  constants, and hard limits.
- `engine/serialize.go`: Pascal strings, world info, board info, stat records,
  and RLE tile records.
- `engine/game.go`: `BoardClose`, `BoardOpen`, `WorldCreate`, `worldReadFrom`,
  and `worldWriteTo`.
- Wiki of ZZT, "ZZT file format":
  `https://wiki.zzt.org/wiki/ZZT_file_format`.

