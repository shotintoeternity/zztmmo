package zztgo

import "testing"

// A hyperlink reply must be answered before the reader can walk into the object
// again.
//
// The owner's report was "scroll hyperlinks do not work", in every view and
// without a world in common. What the passing suites all had in common instead
// was a released arrow: M4.6 steps with PlayerInput{} after submitting its
// reply, and control_keys.test.mjs presses keys rather than holding them. A
// player holds the arrow that walked them into the vendor, and the browser's own
// key repeat puts it back on the wire the moment the window closes.
//
// Before the fix in GameStepWithInputs the drain only OopSend'd the label and
// left the object to run it on its own tick. With the player still pushing into
// the object, ElementObjectTouch sent TOUCH first and rewound DataPos: the
// scroll reopened, the label never ran, and no amount of waiting helped.
func TestScrollReplyRunsWhileTheReaderIsStillWalkingIn(t *testing.T) {
	for _, held := range []struct {
		name string
		mask uint16
	}{
		{"arrow released", 0},
		{"arrow still held", InputMaskRight},
	} {
		t.Run(held.name, func(t *testing.T) {
			world := loadTownWorldForM45(t)
			rm := NewRoomManager(world)
			playerID := rm.JoinPlayer(1, 0, 0)

			stageTownPlayer(t, rm, playerID, 2, 20, 9)
			// The vendor charges a gem. Without one, `:ba` jumps to `:ca` and the
			// test would pass or fail for a reason that is not the reply. Read
			// the state AFTER staging: staging onto another board hands the
			// player a new PlayerState, and a pointer taken before it is stale.
			state := mustTownPlayerState(t, rm, playerID)
			state.Gems = 5

			scroll := stepTownUntilEvent(t, rm, playerID, m46Input(playerID, 1, InputMaskRight, 0), 20, "scroll")
			if scroll.Title != "Vendor" {
				t.Fatalf("scroll title = %q, want Vendor", scroll.Title)
			}

			if !rm.SubmitScrollReply(playerID, scroll.StatID, "ba") {
				t.Fatal("SubmitScrollReply ba failed")
			}
			// TOWN's vendor is a cycle-3 object, so give the answer more steps
			// than one cycle before calling it lost.
			for i := 0; i < 20; i++ {
				rm.StepDiffs(map[PlayerID]PlayerInput{
					playerID: m46Input(playerID, uint64(2+i), held.mask, 0),
				})
				if state.Ammo == 3 {
					if state.Gems != 4 {
						t.Fatalf("gems = %d after buying ammo, want 4", state.Gems)
					}
					return
				}
			}
			t.Fatalf("the :ba label never ran: ammo=%d gems=%d after 20 steps", state.Ammo, state.Gems)
		})
	}
}
