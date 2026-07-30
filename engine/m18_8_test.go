package zztgo

// M18.8 — objects that run their program at board load instead of waiting for
// :touch.
//
// The engine side is vanilla and is not under test here: ElementObjectTick runs
// a program while DataPos >= 0 and #end parks it, which is why the leading #end
// above the first label is the whole mechanism. What is under test is the
// generation-side audit that catches a generated object missing it, and — just
// as important — that the audit stays quiet about the many objects which
// legitimately do work at load.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestM188OopPreludeStopsAtLabelOrEnd(t *testing.T) {
	cases := []struct {
		name string
		data string
		want []string
	}{
		{
			name: "parked before the first label, the correct idiom",
			data: "@npc\r#end\r:touch\rHello traveler.\r#end",
			want: nil,
		},
		{
			name: "missing #end, so the greeting runs at load",
			data: "@npc\rHello traveler.\r#end\r:touch\rAgain?\r#end",
			want: []string{"Hello traveler."},
		},
		{
			name: "no label at all is a one-shot and never a prelude",
			data: "@cutscene\rThe gate groans open.\r#end",
			want: nil,
		},
		{
			name: "setup before the label is still a prelude, judged later",
			data: "@patrol\r#cycle 2\r#walk n\r:touch\rHalt.\r#end",
			want: []string{"#cycle 2", "#walk n"},
		},
		{
			name: "no @name line",
			data: "#give ammo 5\r:touch\r#end",
			want: []string{"#give ammo 5"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := oopPrelude(tc.data)
			if len(got) != len(tc.want) {
				t.Fatalf("oopPrelude = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("oopPrelude = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

func TestM188OopPreludeOffenceIsNarrow(t *testing.T) {
	// The benign list is not taste: each of these appears in the prelude of
	// labeled objects in the community corpus under llmworld/examples, most of
	// them dozens of times. Flagging any of them would burn a repair attempt on
	// every future dream that painted an ordinary patroller or music object.
	benign := []string{
		"#cycle 3", "#char 2", "#color red", "#walk n", "#set flag", "#clear flag",
		"#bind other", "#zap touch", "#restart", "#if flag done", "#try n",
		"#put n solid", "#send other:label", "#shoot n", "#lock", "#unlock",
		"#play tcfa", "#become empty", "#change fake floor", "#go n", "#idle",
		"'a comment", "/n/n/e", "?n", "",
	}
	for _, line := range benign {
		if offence := oopPreludeOffence(line); offence != "" {
			t.Errorf("oopPreludeOffence(%q) = %q, want no offence", line, offence)
		}
	}

	offending := map[string]string{
		"You are late.":            "text",
		"$A CENTERED SHOUT":        "text",
		"!label;a menu item":       "text",
		"#give ammo 15":            "#give",
		"#take gems 1":             "#take",
		"#endgame":                 "#endgame",
		"#GIVE TORCHES 3":          "#give",
		"The rain falls sideways.": "text",
	}
	for line, want := range offending {
		offence := oopPreludeOffence(line)
		if offence == "" {
			t.Errorf("oopPreludeOffence(%q) = no offence, want %s", line, want)
			continue
		}
		if !strings.HasPrefix(offence, want) {
			t.Errorf("oopPreludeOffence(%q) = %q, want it to start with %q", line, offence, want)
		}
	}
}

// zwdWorldWithObjects builds a one-board world whose stats are the given OOP
// programs, so the audit can be exercised through the real compiler on the real
// compiled bytes rather than on a hand-built Engine.
func zwdWorldWithObjects(t *testing.T, programs ...string) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString("zwd 1\nworld \"M188\"\n\nboard \"Prelude Test\"\n")
	b.WriteString("  start player at 10,12\n  max-shots 0\n  dark false\n  reenter false\n  time-limit 0\n")
	b.WriteString("  exits north none south none west none east none\n\n  grid\n")
	for row := 0; row < 25; row++ {
		line := []byte(strings.Repeat(".", 60))
		if row == 11 { // 0-based grid row 11 is the 1-based y=12 the stats name
			line[9] = '@'
			for i := range programs {
				line[20+i*4] = 'o'
			}
		}
		b.WriteString(string(line) + "\n")
	}
	b.WriteString("  end\n\n  legend\n    . = Empty color 0x00\n    o = Object color 0x0B\n    @ = Player color 0x1F\n  end\n\n  stats\n")
	for i, program := range programs {
		fmt.Fprintf(&b, "    stat at %d,12 element Object cycle 3 p1 cp437:0x02 step idle\n", 21+i*4)
		b.WriteString("    oop\n")
		for _, line := range strings.Split(program, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("    end\n")
	}
	b.WriteString("  end\nend\n")

	data, err := CompileZWD(b.String())
	if err != nil {
		t.Fatalf("CompileZWD: %v\n%s", err, b.String())
	}
	return data
}

func TestM188AuditFlagsTheTalkerAndNotThePatroller(t *testing.T) {
	// Two objects, one board. The first is the reported bug: a labeled NPC whose
	// greeting sits above the label, so it fires the moment the board opens. The
	// second is the pattern a blunter rule would break — it legitimately walks
	// and sets its cycle at load, and is also touchable.
	data := zwdWorldWithObjects(t,
		"@talker\nYou are late. By nine nights.\n#give ammo 15\n#end\n:touch\nAgain?\n#end",
		"@patrol\n#cycle 2\n#walk n\n#end\n:touch\nHalt.\n#end",
	)

	failures, err := auditObjectPreludes(data)
	if err != nil {
		t.Fatalf("auditObjectPreludes: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(failures), failures)
	}
	got := failures[0].Err
	if !strings.Contains(got, "talker") {
		t.Errorf("finding does not name the offending object: %q", got)
	}
	if strings.Contains(got, "patrol") {
		t.Errorf("finding blames the legitimate patroller: %q", got)
	}
	if !strings.Contains(got, "#give") || !strings.Contains(got, "You are late") {
		t.Errorf("finding does not say what the object did: %q", got)
	}
	if !strings.Contains(got, "#end") {
		t.Errorf("finding does not tell the model how to fix it: %q", got)
	}
}

func TestM188AuditIsQuietOnACorrectWorld(t *testing.T) {
	data := zwdWorldWithObjects(t,
		"@npc\n#end\n:touch\nHello traveler.\n#give torches 3\n#end",
		"@sign\n#end\n:touch\n$THE WAY NORTH\n#end",
		"@cutscene\nThe gate groans open.\n#end",
	)
	failures, err := auditObjectPreludes(data)
	if err != nil {
		t.Fatalf("auditObjectPreludes: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("correct world produced findings: %v", failures)
	}
}

// TestM188AuditReadsPristinePrograms guards the ordering trap: #zap rewrites
// ":label" to "'label" inside stat.Data, so an audit run after the acceptance
// simulation had ticked the board would see an object with no labels left and
// wave its prelude through.
func TestM188AuditReadsPristinePrograms(t *testing.T) {
	zapped := "@zapper\rHello.\r#zap touch\r:touch\rAgain?\r#end"
	if got := oopPrelude(zapped); len(got) == 0 {
		t.Fatal("prelude of a pristine program should be visible")
	}
	// The same program after #zap ran: the label is now a comment, so there is
	// no label to be "before" and the object reads as a one-shot.
	afterZap := strings.Replace(zapped, "\r:touch", "\r'touch", 1)
	if got := oopPrelude(afterZap); got != nil {
		t.Fatalf("a zapped program should read as label-less, got %q", got)
	}
	// Which is exactly why auditObjectPreludes loads its own untouched world.
	data := zwdWorldWithObjects(t, "@zapper\nHello.\n#zap touch\n#end\n:touch\nAgain?\n#end")
	failures, err := auditObjectPreludes(data)
	if err != nil {
		t.Fatalf("auditObjectPreludes: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(failures), failures)
	}
}

// TestM188CommittedCorpusHasNoTalkativeObjects is the false-positive gate the
// task DoD asks for: every world already committed under llmworld/generated
// must pass the new audit untouched. A regression here means the next dream
// spends a repair attempt on a board that was fine.
func TestM188CommittedCorpusHasNoTalkativeObjects(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "llmworld", "generated", "*.zwd"))
	if len(paths) == 0 {
		t.Fatal("no generated worlds under llmworld/generated — these are committed and required")
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".zwd")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			data, err := CompileZWD(string(src))
			if err != nil {
				t.Fatalf("CompileZWD: %v", err)
			}
			failures, err := auditObjectPreludes(data)
			if err != nil {
				t.Fatalf("auditObjectPreludes: %v", err)
			}
			for _, failure := range failures {
				t.Errorf("committed world flagged: %s", failure)
			}
		})
	}
}

// talkativeGeneratedBoard is generatedBoard's fixture with the leading #end
// left out — the exact defect the owner reported: the object's greeting sits
// above :touch, so it fires the moment the board opens. #endgame stays under
// the label so the board still satisfies the plan's promised finale.
func talkativeGeneratedBoard(name string) string {
	rows := []string{"@o" + strings.Repeat(".", 58)}
	for len(rows) < 25 {
		rows = append(rows, strings.Repeat(".", 60))
	}
	return "```zwd\nboard \"" + name + "\"\n  start player at 1,1\n  dark false\n" +
		"  exits north none south none west none east none\n  grid\n" +
		strings.Join(rows, "\n") + "\n  end\n  legend\n" +
		"    @ = Player color 0x1F\n    . = Empty color 0x00\n    o = Object color 0x0F\n" +
		"  end\n  stats\n    stat at 2,1 element Object cycle 3\n    oop\n" +
		"    @greeter\n    You are late. By nine nights, roughly.\n    #end\n" +
		"    :touch\n    #endgame\n    #end\n    end\n  end\nend\n```"
}

// TestM188TalkativeBoardIsRepairedNotStubbed drives the real pipeline: the
// first paint of Start talks at load, and the audit must push that board — and
// only that board — back through the repair loop. It must not stub the board
// (M17.13's floor is for boards that crash, and deleting a room over a
// misplaced #end trades a small defect for a large one) and must not fail the
// world at the final validate gate.
func TestM188TalkativeBoardIsRepairedNotStubbed(t *testing.T) {
	plan := generationPlan("1. start: begin. #endgame")
	fake, claude := newFakeClaude(t,
		plan,
		talkativeGeneratedBoard("Start"),
		generatedBoard("Title", false),
		generatedBoard("Start", false),
	)
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 3)

	result, err := service.Generate(context.Background(), "test", "an object that will not wait", "TALKIES", nil, false)
	if err != nil {
		t.Fatalf("generation failed instead of repairing: %v", err)
	}
	if len(result.Stubbed) != 0 {
		t.Fatalf("a talkative board was stubbed rather than repaired: %v", result.Stubbed)
	}

	var repair string
	for _, req := range fake.requests {
		if strings.Contains(req.Messages[0].Content, "board load") {
			repair = req.Messages[0].Content
		}
	}
	if repair == "" {
		t.Fatalf("no repair request named the problem; %d requests made", len(fake.requests))
	}
	if !strings.Contains(repair, "greeter") {
		t.Errorf("repair request does not name the offending object:\n%s", repair)
	}
	if !strings.Contains(repair, "#end") {
		t.Errorf("repair request does not say how to fix it:\n%s", repair)
	}

	// The shipped world is clean: the repaint parked the object.
	data, err := CompileZWD(result.ZWD)
	if err != nil {
		t.Fatalf("shipped world does not compile: %v", err)
	}
	failures, err := auditObjectPreludes(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("shipped world still has talkative objects: %v", failures)
	}
}
