package zztgo

// Throwaway M16.3 convergence aid — NOT COMMITTED. Dumps engine vs oracle
// rows at a named checkpoint of a scenario.

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestOracleDebugDump(t *testing.T) {
	scn := os.Getenv("ORACLE_DEBUG_SCN")
	if scn == "" {
		t.Skip("set ORACLE_DEBUG_SCN=name (and ORACLE_DEBUG_LABEL, ORACLE_DEBUG_ROWS=a-b)")
	}
	label := os.Getenv("ORACLE_DEBUG_LABEL")
	rows := os.Getenv("ORACLE_DEBUG_ROWS")
	var r0, r1 int
	fmt.Sscanf(rows, "%d-%d", &r0, &r1)

	checkpoints := parseOracleCapture(t, "../fixtures/oracle/"+scn+".capture.txt")
	err := oracleAdapterRun(t, scn+".scn", scn+".capture.txt", func(l string) {
		if l != label {
			return
		}
		var cp *oracleCheckpoint
		for i := range checkpoints {
			if checkpoints[i].Label == l {
				cp = &checkpoints[i]
			}
		}
		for y := r0; y <= r1 && y < 25; y++ {
			var eng, orc strings.Builder
			var engA, orcA strings.Builder
			for x := 0; x < 60; x++ {
				ec, oc := E.Screen[x][y], cp.Cells[y][x]
				eng.WriteByte(printable(ec.Ch))
				orc.WriteByte(printable(oc.Ch))
				engA.WriteString(fmt.Sprintf("%02x", ec.Color))
				orcA.WriteString(fmt.Sprintf("%02x", oc.Attr))
			}
			t.Logf("row %d\n  eng %q\n  orc %q\n  engA %s\n  orcA %s", y, eng.String(), orc.String(), engA.String(), orcA.String())
		}
		t.Logf("engine: paused=%v tick=%d timer=%d sec=%d hsec=%d health=%d ammo=%d",
			E.PlayerFor(0).Paused, E.CurrentTick, E.TimerTicks,
			E.PlayerFor(0).BoardTimeSec, E.PlayerFor(0).BoardTimeHsec, E.PlayerFor(0).Health, E.PlayerFor(0).Ammo)
		for i := int16(0); i <= E.Board.StatCount; i++ {
			st := E.Board.Stats[i]
			if st.X == 0 && st.Y == 0 || E.Board.Tiles[st.X][st.Y].Element == E_MESSAGE_TIMER {
				t.Logf("  stat %d at (%d,%d) cycle=%d p2=%d (message timer?)", i, st.X, st.Y, st.Cycle, st.P2)
			}
		}
	})
	t.Logf("run result: %v", err)
}

func printable(b byte) byte {
	if b >= 32 && b < 127 {
		return b
	}
	return '?'
}
