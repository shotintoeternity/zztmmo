# CUTLINE — the co-op product cutline

ZZTMMO's product claim is one sentence: **a small group of people can play a ZZT
world together, and the server decides what happened.** Everything else on the
roadmap — Dream, the Museum, accounts, collaborative editing, ghosts, a live DM —
is a thing you can add to a game that already does that.

This file is the cutline: one acceptance journey that says the claim is still
true, in the form a machine can run and a person can repeat.

## The policy

**Before promoting another roadmap system, run this journey and keep it green.**

An attractive feature that leaves the journey red has not advanced the core
shared-ZZT experience; it has traded it for something else. That is a decision
the owner may still take — but it should be taken deliberately, with the red run
in front of them, rather than discovered later by a group of testers.

The journey is not a substitute for the M16 certification suite (see PARITY.md).
Certification asks whether one player's experience matches vanilla ZZT; the
cutline asks whether a group has an experience at all.

## The four claims

1. **One world, not three copies.** A pickup one player takes is gone for the
   others; a door one player unlocks stays open for the group.
2. **One authoritative result.** Players in the same room are told the same
   server `StateHash` on the same tick.
3. **The group survives a reconnect.** A dropped player resumes in place, as the
   same player, and leaves no ghost in anybody else's roster.
4. **The group survives save/restore.** A restore is refused while the world is
   occupied, and once taken it rolls the shared world back for everyone.

Plus one more, which is why the journey does not stop at the acceptance fixture:
the same group can do all of that in a **shipped classic** (TOWN), including
watching one of their number leave through a board edge.

## Running it automatically

```sh
cd engine
ZZT_BROWSER=1 go test -count=1 -run TestCoopCutlineThreePlayerAcceptanceJourney -timeout 20m .
```

Three real Chromium browsers, the production `zzt-server` binary, the Vite-built
client, and no staged state: every player starts at the title screen and types
their own name. It takes roughly 65 seconds.

- `-count=1` matters. `go test` caches the Go driver, and the `.mjs` script is
  not a tracked dependency, so a cached pass can outlive the code it passed on.
- Like the other real-browser suites the journey is **opt-in** (CLAUDE.md rule 3):
  without `ZZT_BROWSER=1` it declares a skip, and `make certify` requires it.
- On failure each player's trace, screenshot and protocol transcript land in
  `engine/web/test-results/coop/`.

The script is `engine/web/test/coop_journey.test.mjs`; its driver is
`engine/coop_cutline_test.go`. Both carry the reasoning behind the assertions.
The three players are walked by `engine/web/test/lib/walk.mjs`, shared with the
M16.11 journeys (M16.11e); its header is where the input/tick mechanism that
makes a step land is written down.

## Running it by hand

Same journey, three browser windows — **separate windows, not three tabs**: tabs
in the background have their timers throttled and the client samples held keys
every 55ms, so a backgrounded player simply stops walking. Sign-in is not
required; the launch prompt's name is enough.

Start a server with both worlds available:

```sh
cd engine
go run ./cmd/zzt-server -worlds <dir with ACCEPT.ZZT and TOWN.ZZT> -saves ./saves
```

Then, with three windows open on it — call the players Ada, Bo and Cy:

1. **Gather.** In each window type a name, pick `ACCEPT`, and press <kbd>P</kbd>
   at its title screen. *Selecting a world must stop at its title screen and join
   nothing; pressing P is what joins.* All three should now be standing on
   Acceptance Main, and each sidebar should show the same world.
2. **Save the pristine world.** As Ada press <kbd>S</kbd> and save as `COOPSAVE`.
   *Claim 4 needs something to roll back to.*
3. **One world.** Walk Ada east along row 12: torch, gem, ammo, key, then the
   cyan door, which her key opens. Now walk Bo east along the same row. *He must
   collect nothing — Ada took it — and he must pass the door without ever holding
   a key.* This is claim 1, and it is the whole product in one row of tiles.
4. **Together.** Walk all three around the vendor and east onto the passage at
   x=34. *All three should arrive on Acceptance Target and see each other there.*
5. **Reconnect.** Reload Cy's window, rejoin ACCEPT, and press P. *Cy must come
   back where he left, and Ada and Bo must still see exactly three players — not
   four.* This is claim 3; a fourth figure on screen is the failure.
6. **Restore.** With everyone still playing, a restore must be refused — the
   client only offers <kbd>R</kbd> from the title screen, so this is the one step
   easier to check with `curl`:
   ```sh
   curl -si -X POST localhost:8080/api/restore \
     -H 'Content-Type: application/json' -d '{"world":"ACCEPT","name":"COOPSAVE"}' | head -1
   ```
   *409 while anyone is in the world.* Then quit all three to the title
   (<kbd>Q</kbd>, <kbd>Y</kbd>, <kbd>Esc</kbd> through the score windows), press
   <kbd>R</kbd> in Ada's window and take `COOPSAVE`, press <kbd>P</kbd>, and walk
   east again. *The torch and the gem are back on their squares.* Bring Bo and Cy
   back in; all three are in the restored world.
7. **A real classic.** Quit, pick `TOWN`, and press <kbd>P</kbd> in all three.
   *All three meet in the same room, and when one of them walks off the edge of
   the board the other two watch her go and are left with two players.*

If a step disagrees with what is written here, the cutline is red. Record what
happened in NOTES.md before changing anything — the journey is evidence, and a
step quietly relaxed to keep it green is worth less than a red run.
