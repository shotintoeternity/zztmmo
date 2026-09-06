# ZZTMMO 3D

A 3D client for [ZZTMMO](https://github.com/shotintoeternity/zztmmo).

The ZZTMMO server is authoritative and the browser client is a dumb terminal:
it sends keymasks and command bytes, and draws the 60x25 board of CP437 cells
the server streams back. This project is a second dumb terminal for the same
server. It speaks the same WebSocket protocol, receives the same cells, and
draws them as a 3D world instead of a text screen.

Nothing in the main repository changes. Run the existing `zzt-server`, then
open this client against it.

## Status

Just started. See the git log.
