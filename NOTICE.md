# Provenance and third-party content

## The engine

`engine/` is a fork of [benhoyt/zztgo](https://github.com/benhoyt/zztgo) by Ben
Hoyt, MIT licensed. Ben's original license text is preserved verbatim at
`engine/LICENSE.txt`.

zztgo is itself a machine-assisted conversion of
[asiekierka/reconstruction-of-zzt](https://github.com/asiekierka/reconstruction-of-zzt),
Adrian Siekierka's reconstruction of the original ZZT Pascal source. This fork
keeps the converted code deliberately Pascal-shaped: quirks and bugs of the 1991
original are ported faithfully and marked `// ZZT-QUIRK:` rather than fixed.

## The game content

ZZT was created by **Tim Sweeney** and published by **Epic MegaGames** in 1991.
Epic released the ZZT source code in 2020.

Two categories of original Epic content are redistributed in this repository,
for testing and rendering-fidelity work:

| Files | What they are |
|---|---|
| `engine/*.HLP` (11 files) | The in-game help text shipped with ZZT |
| `fixtures/TOWN.ZZT` | "Town of ZZT", the freely-distributable shareware world |

These are not covered by this repository's MIT license. They are included on the
same basis as the upstream zztgo repository, which ships the same files. If you
own these works and would prefer they not be redistributed here, open an issue
and they will be removed.

`fixtures/TOWN.ZZT` is load-bearing for the test suite: the deterministic replay
harness runs it under scripted input and asserts identical state hashes across
runs. Removing it would require regenerating the replay fixtures against a
different world.
