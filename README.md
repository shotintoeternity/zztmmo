# ZZTMMO

[![CI](https://github.com/shotintoeternity/zztmmo/actions/workflows/ci.yml/badge.svg)](https://github.com/shotintoeternity/zztmmo/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Welcome back to the Town of ZZT. This time, bring friends.**

ZZTMMO turns Tim Sweeney's 1991 shareware classic **ZZT** into a shared browser world: same blue text windows, the same char 2 smiley in white on blue (pick your own color if you like), and the same ruffians making awful decisions. But now multiple people can explore the boards together.

Explore classic `.ZZT` worlds in synchronized rooms, chat while you play, read scrolls, buy supplies, get hurt, respawn, cross passages, save shared snapshots, and watch someone else discover that yes, that fake wall was fake the whole time.

## Play Together Today

*   **Explore classic ZZT worlds together:** Load local `.ZZT` files or search Museum of ZZT titles from the world picker, then join each world as its own live instance.
*   **Share rooms without sharing a keyboard:** Multiple players can stand on the same board, move independently, trigger scrolls, buy and use items, take damage, die, and respawn.
*   **Keep the world moving:** Board transfers, passages, active-room ticks, frozen empty rooms, dark rooms, torches, high scores, help screens, pause, quit, and title-screen flows are all handled server-side.
*   **Chat like it's 199x with better sockets:** Browser clients get global chat, with optional JSONL persistence when saves are enabled.
*   **Save the shared mess:** Room snapshots can be saved to disk and restored later, so a party can preserve puzzle progress instead of starting from a pristine world every session.
*   **Play co-op, not PvP:** Player bullets do not hurt other players. Friendly fire is a deployment setting (`-friendly-fire`), never something a world file can turn on, so a downloaded world cannot decide to make your party hostile. No first-party PvP world is currently hosted.
*   **Race the daily challenge:** `/challenge` opens a timed first-party course inside the same client. Every run is a recorded, isolated session, so a time is measured in simulation ticks, checkable by replaying the recording it cites, and raceable as a ghost that is drawn on your own screen and nowhere else. Signing in posts a time; a guest can still run it and see their own.

## The ZZT Feel

*   **Canvas CP437 renderer:** The browser draws the original 60x25 board plus 20x25 sidebar in DOS colors using pixel-perfect PNG font sheets from Adrian Siekierka's [Zeta](https://github.com/asiekierka/zeta).
*   **ZZT-style windows:** Scrolls, files, prompts, help, saves, generated-world progress, world search, and failure messages render as text-mode modal windows.
*   **Server-authoritative simulation:** Clients send keyboard state and command bytes; the Go server owns gameplay and streams snapshots, dirty-cell diffs, HUD updates, sounds, modal events, and board changes.
*   **Faithful where it matters:** Classic behavior and bugs are treated as the spec unless multiplayer needs an explicit deviation.

## Beta Notes

ZZTMMO is in a small public beta. It is a real server running real ZZT worlds, and it is also a work in progress — the point of the beta is to find where it bends.

**Scope.** Desktop browsers with a keyboard are what the beta is aimed at and where everything above is certified. A touchscreen can now play rather than only type: phones and tablets get an on-screen direction pad plus Fire, Torch and Pause, each driving the same keys a keyboard sends. The controls take their own strip of the screen in either orientation, so the board and the sidebar stay whole — a landscape phone shows the screen smaller rather than partly under the buttons.

**Known rough edges.**

*   Worlds run live on the server, so a restart returns everyone to the title screen. Save with **S** and come back with **R**. A planned restart warns connected players a minute ahead; an unplanned one does not.
*   **D**, "Dream a world", asks a language model for a brand-new world. It is rate limited per player, and the server has a daily ceiling — if it refuses, that is the limit talking, not a crash. Generation takes a couple of minutes and can fail on a board or two; the failed rooms come back as empty stubs rather than sinking the world.
*   Multiplayer changes some of ZZT's assumptions. Boards tick for everyone standing on them, so another player can spring a trap, take the item you were walking toward, or wander off the edge mid-scroll.
*   A crowded board can place a late arrival somewhere the world never meant them to stand, because spawn placement looks for the nearest free square rather than the nearest square you could have walked to.
*   Museum of ZZT worlds are community-authored and were written for one player. Some are wonderful; some will do something strange with several.

**Reporting problems.** Press **F** on the title screen for the in-game pointer, or go straight to [GitHub issues](https://github.com/shotintoeternity/zztmmo/issues). The useful ones say which world you were in, what you did, and what happened — a screenshot of the board helps a lot.

## Moonshot Roadmap

ZZTMMO is already playable as a shared ZZT server. What is still ahead:

*   **Dream worlds:** Generate compact, playable `.ZZT` worlds from prompts, validate them, and host them as multiplayer instances.
*   **Museum search-and-play:** Keep turning the Museum of ZZT archive into a walkable universe where old community worlds are a few keystrokes away.
*   **Player identity:** Account-backed names, persistent player state, invites, parties, and cleaner ownership for worlds and saves.
*   **Party instances:** Make it easy for a group to spin up a private run, continue later, and invite more players into the same adventure.
*   **Seasons:** A calendar of hand-authored challenge courses rather than one committed course.
*   **Ghost racing:** Race a specific friend's run, and take ghosts outside the challenge layer.
*   **Live-DM tools:** Possession, moderation, and "dungeon master" style control for running events inside classic ZZT worlds.
*   **Community publishing:** Grow the browser editor into a collaborative way to build, test, publish, and share worlds without leaving the page.

## Running it Yourself

See **[CONTRIBUTING.md](CONTRIBUTING.md)** for prerequisites, a local quick start, the server flags, and the repository layout. World generation is documented separately in **[docs/generation.md](docs/generation.md)**.

## Credits & Special Appreciation

This project would not exist without the dedication of the ZZT preservation community and the creators who came before us:

*   **Tim Sweeney** (Creator of ZZT / Epic Games): His game and the vibrant community surrounding it have changed my life.
*   **Adrian Siekierka** ([@asiekierka](https://github.com/asiekierka)): Immense thanks for reconstructing the original Turbo Pascal code of ZZT in [reconstruction-of-zzt](https://github.com/asiekierka/reconstruction-of-zzt), and for [Zeta](https://github.com/asiekierka/zeta), whose pixel-perfect font sheets power ZZTMMO's canvas renderer.
*   **Ben Hoyt** ([@benhoyt](https://github.com/benhoyt)): The author of [zztgo](https://github.com/benhoyt/zztgo), which made it possible to build ZZTMMO on top of a modern Go codebase.

This project's own source is licensed under the **MIT License** (see [LICENSE](LICENSE)). It also redistributes original Epic MegaGames content — the ZZT help files and the shareware world "Town of ZZT" — which the MIT license does not cover. [NOTICE.md](NOTICE.md) records the provenance of every third-party piece, including the reconstruction the engine descends from and the Zeta font sheets the renderer draws with.

## Greetz

atom, blazer, bluemagus, bongo, capnkev, chronos, cly5m, crankgod, darkmage, dex, dive, dr. dos, drac0, dragonlord, evilmario, fishfood, flatcoat_lab, flicker, funk, hercules, hm, hydra, jujubee, kkairos, knightt, lemmer, lord_igsel, madtom, masamune, mono, mooseka, myth, nadir, roastbeef, smiley, tseng, tucan, viovis, wil, xabbott, xf, yenrab, zamros, zed
