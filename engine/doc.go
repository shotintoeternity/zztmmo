// Package zztgo is the ZZTMMO simulation engine: a headless, deterministic,
// server-authoritative build of Tim Sweeney's 1991 ZZT that many players can
// stand inside at once.
//
// # Where the code came from, and why it looks like this
//
// About a quarter of this package — twelve files, some 9,000 of its 41,000
// non-test lines — was not written by hand. It arrived by machine conversion:
// Adrian Siekierka reconstructed ZZT's original Turbo Pascal source from the
// 1991 binary, and Ben Hoyt ran that through his pas2go converter to produce
// zztgo. ZZTMMO is a fork of zztgo. The converted files still carry
// their Pascal unit name in the package clause (`package zztgo // unit: Game`),
// still use the original identifiers down to the capitalization, and still
// carry Ben's TODOs. Each one opens with a header saying where it came from.
//
// That shape is deliberate and it is load-bearing. The contract of this project
// is that a 1991 world behaves the way it behaved in 1991, so the original's
// behavior — including its bugs — is the specification. A quirk gets ported and
// marked `// ZZT-QUIRK:` rather than fixed, and code that reads awkwardly gets
// left awkward, because the reference implementation it must agree with is a
// DOS executable and the diff against the Pascal is the only proof available.
// Tidying converted code is how you silently break a twenty-year-old world.
// ../NOTICE.md has the full provenance chain and the licensing.
//
// Code written for ZZTMMO — the server, the protocol, rooms, persistence, world
// generation, the editor session layer — is ordinary Go and is held to ordinary
// Go standards.
//
// # Determinism
//
// The simulation must produce identical state from identical input, because
// that is what makes replays verifiable, saves portable and multiplayer
// reconcilable. Inside simulation code that means: no wall clock, no sleeping,
// no goroutine scheduling that gameplay can observe, no ranging over a map
// where the order reaches game state, and no randomness except through the
// engine's own seeded generator. Replay fixtures under ../fixtures pin this
// down by hashing engine state after scripted input; a fixture hash changing is
// a behavior change, and is treated as one.
//
// # References in comments to documents that are not here
//
// Comments throughout this package cite NOTES.md, ANALYSIS.md, CLAUDE.md and
// similar files. Those are the project's internal working documents — a
// decision log, a line-by-line analysis of the converted code, and the working
// agreement the engine is built under — and they are kept out of the
// distribution rather than shipped. A citation is there so a maintainer can
// find the reasoning behind an unobvious decision; nothing in the build, the
// tests or the running server depends on those files, with one deliberate
// exception, TASKS.md, which ships because the certification tests parse it.
// The claim a comment makes should stand on its own, and if one does not, that
// is a defect in the comment.
//
// # Orientation
//
// The converted ZZT core is game.go (the loop, world load/save, board
// transitions), elements.go (per-element tick and touch behavior), oop.go (the
// #-command object language), gamevars.go (world and board state),
// editor.go, txtwind.go (text windows), input.go, sounds.go and serialize.go.
// video.go is the exception: it was rewritten to render into a buffer instead
// of a terminal, which is the seam that made a headless server possible.
//
// The multiplayer layer above it is room_manager.go, which owns one world
// instance and advances it a tick at a time for every player standing in it;
// websocket_server.go, which owns connections, sessions and the lock that
// serializes access to each instance; protocol.go, the wire format the browser
// client speaks; snapshot.go for saving and restoring a shared world; and
// web_api.go for the HTTP surface beside the socket.
//
// Commands live under cmd/: zzt-server is the real server, zztgo the original
// single-player terminal game, and the rest are maintainer tools — zzt-parity
// runs the certification gates, zzt-replay and zzt-shot reproduce and picture a
// recorded session, zzt-build compiles a world from ZWD text.
package zztgo
