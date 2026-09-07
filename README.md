# ZZTMMO 3D

A 3D client for [ZZTMMO](https://github.com/shotintoeternity/zztmmo).

![The Town of ZZT, as a diorama](docs/diorama.png)

The ZZTMMO server is authoritative and its browser client is a dumb terminal:
it sends keymasks and command bytes, and draws the 60x25 board of CP437 cells
the server streams back. This is a second dumb terminal for the same server.
It speaks the same WebSocket protocol, receives the same cells, and draws them
as a world instead of a text screen. Nothing in the main repository changes,
and the server never learns which client you are using.

Every square becomes what its glyph says it is, at the glyph's true
proportions: a text cell is 8 pixels wide and 14 tall, so a tile is 1 wide and
1.75 deep, a wall is 1.75 tall, and a card is 1 by 1.75, and no glyph is ever
stretched. A `▓` wall is a block with yellow `▓` on every face. A `☻` is a white smiley on a blue card that always
faces you. A fake wall looks exactly like a wall, because it is drawn with the
wall's glyph, which is the joke working. Other players stand on the board as
their own cards, in the color they picked.

![The overhead view: north up, a couple of dozen columns around you](docs/overhead.png)

## Running it

You need a ZZTMMO server. From a checkout of that repository:

```sh
cd engine
cp ../fixtures/TOWN.ZZT .
go build -o zzt-server ./cmd/zzt-server
./zzt-server -addr 127.0.0.1:8080 -world TOWN -help . -web /path/to/zztmmo-3d/dist
```

Then in this repository:

```sh
npm install
npm run build
open http://127.0.0.1:8080/?world=TOWN&name=You
```

For development, `npm run dev` serves the client on 5173 and proxies `/ws` and
`/api` to a server on 8080, so you can edit and reload against a live game.

Query parameters:

| Parameter | Meaning |
|---|---|
| `world` | The world to join. Default `TOWN`. |
| `name` | Your name on the board. |
| `color` | Your card's background, as `%23RRGGBB`. |
| `board` | The board to start on; the server's default otherwise. |
| `view` | `overhead`, `chase`, `first`, `diorama` or `classic`. |

## Playing

The keys are ZZT's. Arrows move, Shift+arrow shoots, and T, P, B, S, Q, H and
`?` do what the sidebar says. Scrolls, help, prompts and the pause label draw as
ZZT's own text windows over the 3D view.

**A** and **D** strafe, in the first-person view only, where they are the one
thing the arrows cannot say: a step sideways without turning. Everywhere else
the arrows are already absolute board directions, so a strafe would mean
nothing and the keys do nothing. They never reach the server as themselves --
the client resolves them against your facing and sends an ordinary direction,
which is all ZZT's six-bit keymask can carry. (**A** sits next to **S**, which
is Save; a WASD reflex opens the save prompt, and Escape closes it.)

**V** cycles the view, which is the one key this client keeps for itself:

- **overhead** (the default) looks down at your smiley from high up, north
  up, with a couple of dozen columns and most of the rows around you in
  sight. Drag to orbit, wheel to zoom.
- **chase** is the same idea from closer behind, so the walls have faces.
- **first** stands inside your square at eye height, facing the way you last
  pushed. Left and right turn; up walks the way you face, down walks backwards,
  A and D step sideways without turning, and Space+up shoots straight ahead.
- **diorama** shows the whole board from the south, the way the text screen
  does, with depth.
- **classic** is the regular ZZTMMO screen: the board drawn flat as text.

### Reading, in a world you are standing in

Two kinds of words live on a ZZT board, and in three dimensions they need
reading two different ways.

The **message line** -- what the game writes over the bottom row when you touch
something -- is not part of the board, so it leaves the scene and is written on
the bottom row of the screen, where ZZT puts it, rather than standing up as a
row of cards.

**Signs** are part of the board. They are text elements: walls you can read, and
a board with its signs taken out is not the board, so they stay in the world.
But a sign is a row of letters lying flat across the floor, and from inside the
world, at eye height, a row of letters is edge-on and unreadable -- in first
person a sign is a colored wall and nothing more. So the sign you are standing
at reads itself out just above the message line, in its own colors, and only
that one: walk up to it and it appears, walk away and it is gone. Every sign on
the board written out at once would be noise; one sign that arrives as you
reach it is a sign you read.

![Standing at the Town of ZZT sign, which reads itself out along the bottom](docs/sign.png)

![First person, meeting a lion](docs/first.png)

![A scroll over the board](docs/scroll.png)

## How it works

- `src/net.ts` joins, sends input on the key edges and re-sends the held
  keymask every 55ms, exactly as the 2D client does, because the server
  consumes each frame on one 110ms tick.
- `src/classify.ts` turns a glyph and a DOS attribute into a shape: wall,
  short block, forest, water, gate, fog, floor or sprite. It is pure and
  tested.
- `src/scene.ts` rebuilds the board geometry when cells change and draws it
  through one pair of shaders that read the CP437 font atlas: a texel is
  foreground where the glyph has ink and background elsewhere.
- `src/text_runs.ts` finds the board message and the signs in the cells,
  groups a sign's runs into its lines, and answers which sign you are close
  enough to read. Pure and tested.
- `src/input.ts` is the key vocabulary. The strafes are two pseudo-bits above
  the server's six, resolved against your facing or dropped, so they can never
  be sent. Pure and tested.
- `src/camera.ts` is the five views. `src/overlay.ts`, `src/sidebar.ts`,
  `src/textwindow.ts` and `src/modals.ts` are the 80x25 text layer on top.

`npm test` runs the node suites for the classifier, the key vocabulary, the
text windows and the board text. `npm run build` type-checks and bundles.

## Not here yet

Sound, chat, the world picker, the editor, watching and replays, and the
challenge course. A reload joins as a new player: resume tokens are not kept. All of those exist in the 2D client, and this one connects
to the same server, so nothing stops them being added.

## Credits

ZZT is Tim Sweeney's. The server and the text-window and sidebar code are
from ZZTMMO; the font sheet is from Adrian Siekierka's Zeta. See NOTICE.md.
