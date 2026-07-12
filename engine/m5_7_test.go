package zztgo

import (
	"strings"
	"testing"
)

// M5.7 — ZZT-OOP authoring aids (OopAnalyze).

// analyzeProgram builds a one-object board carrying program and returns the
// authoring analysis for that object.
func analyzeProgram(t *testing.T, program string) ([]OopLabelInfo, []OopWarning) {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.WorldCreate()
	e.World.Info.CurrentBoard = 1
	e.World.BoardCount = 1
	e.BoardCreate()
	e.AddStat(10, 10, E_OBJECT, 0x0F, 1, StatTemplateDefault)
	stat := &e.Board.Stats[e.Board.StatCount]
	stat.Data = program
	stat.DataLen = int16(len(program))
	return e.OopAnalyze(e.Board.StatCount)
}

func labelNames(labels []OopLabelInfo) []string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return names
}

func warningText(warnings []OopWarning) string {
	parts := make([]string, len(warnings))
	for i, w := range warnings {
		parts[i] = w.Message
	}
	return strings.Join(parts, " | ")
}

// The TOWN vendor script from the M5.7 spec: three menu hyperlinks pointing at
// three labels the object defines. Every send resolves, so it is warning-free,
// and its :labels are listed.
func TestOopAnalyzeVendorScript(t *testing.T) {
	program := strings.Join([]string{
		"@Vendor",
		"#end",
		":touch",
		`"Hello, you must be new to town! ..."`,
		"!ba;Ammunition, 3 shots.........1 gem",
		"!bt;Torch.......................1 gem",
		"!bx;Advice......................Free",
		":ba",
		"#take gems 1",
		"#give ammo 3",
		"#end",
		":bt",
		"#take gems 1",
		"#give torches 1",
		"#end",
		":bx",
		"Free advice: leave while you can.",
		"#end",
		"",
	}, "\r")

	labels, warnings := analyzeProgram(t, program)

	got := labelNames(labels)
	want := []string{"TOUCH", "BA", "BT", "BX"}
	if len(got) != len(want) {
		t.Fatalf("labels=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("label %d=%q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
	if len(warnings) != 0 {
		t.Fatalf("vendor script warned unexpectedly: %s", warningText(warnings))
	}
}

// A #send to a label the object does not define warns; a #send to one it does
// define does not.
func TestOopAnalyzeMissingSendWarns(t *testing.T) {
	program := strings.Join([]string{
		"@obj",
		":touch",
		"#send here",   // resolves
		"#send nowhere", // does not
		":here",
		"#end",
		"",
	}, "\r")

	labels, warnings := analyzeProgram(t, program)
	if names := labelNames(labels); len(names) != 2 || names[0] != "TOUCH" || names[1] != "HERE" {
		t.Fatalf("labels=%v, want [TOUCH HERE]", names)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings=%q, want exactly one (the #send nowhere)", warningText(warnings))
	}
	if !strings.Contains(warnings[0].Message, "NOWHERE") {
		t.Fatalf("warning=%q, want it to name NOWHERE", warnings[0].Message)
	}
	if warnings[0].Line != 3 {
		t.Errorf("warning line=%d, want 3 (0-based)", warnings[0].Line)
	}
}

// #zap and #restore validate their labels like #send; a hyperlink to a missing
// label warns; a real command does not.
func TestOopAnalyzeZapRestoreAndHyperlink(t *testing.T) {
	program := strings.Join([]string{
		"@obj",
		":shot",
		"#zap shot",     // resolves (self)
		"#restore gone", // missing
		"#become object", // known command, no warning
		"!buy;Buy this",  // hyperlink to missing :buy
		"#end",
		"",
	}, "\r")

	_, warnings := analyzeProgram(t, program)
	text := warningText(warnings)
	if len(warnings) != 2 {
		t.Fatalf("warnings=%q, want two (#restore gone and !buy)", text)
	}
	if !strings.Contains(text, "GONE") || !strings.Contains(text, "BUY") {
		t.Fatalf("warnings=%q, want ones naming GONE and BUY", text)
	}
}

// An unknown #word that is neither a command nor a local label warns as an
// unknown command / missing label.
func TestOopAnalyzeUnknownCommandWarns(t *testing.T) {
	program := strings.Join([]string{
		"@obj",
		":touch",
		"#frobnicate",
		"#end",
		"",
	}, "\r")

	_, warnings := analyzeProgram(t, program)
	if len(warnings) != 1 {
		t.Fatalf("warnings=%q, want one (#frobnicate)", warningText(warnings))
	}
	if !strings.Contains(strings.ToLower(warnings[0].Message), "frobnicate") {
		t.Fatalf("warning=%q, want it to name 'frobnicate'", warnings[0].Message)
	}
}

// A #word that is not a known command but IS a local label is a valid implicit
// self-send: no warning.
func TestOopAnalyzeImplicitSelfSendIsValid(t *testing.T) {
	program := strings.Join([]string{
		"@obj",
		":touch",
		"#shootagain",
		":shootagain",
		"#shoot n",
		"#end",
		"",
	}, "\r")

	_, warnings := analyzeProgram(t, program)
	if len(warnings) != 0 {
		t.Fatalf("implicit self-send warned: %s", warningText(warnings))
	}
}

// Known commands with arguments, comments, movement, and object-name lines never
// produce spurious warnings.
func TestOopAnalyzeKnownCommandsAreQuiet(t *testing.T) {
	program := strings.Join([]string{
		"@guard",
		"'a comment line",
		"#walk n",
		"/n",
		"?s",
		"#give health 10",
		"#if alligned #shoot seek",
		"#char 2",
		"#cycle 3",
		"#play cdefg",
		"#end",
		"",
	}, "\r")

	_, warnings := analyzeProgram(t, program)
	if len(warnings) != 0 {
		t.Fatalf("known-command script warned: %s", warningText(warnings))
	}
}

// The analysis reaches the browser through ProgramText, and a program saved with
// a bad send still saves — the aid is advisory, never a gate.
func TestEditorSessionProgramAnalysisAndSaveSucceeds(t *testing.T) {
	session := NewEditorSession("TEST", testEmptyWorld(t))
	member := &webSocketClient{}
	if err := session.Enter(member); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	defer session.Exit(member)

	var objectID int16
	if err := session.Apply(member, func(e *Engine) {
		e.AddStat(10, 10, E_OBJECT, 0x0F, 3, StatTemplateDefault)
		objectID = e.Board.StatCount
		program := "@obj\r:touch\r#end\r"
		e.Board.Stats[objectID].Data = program
		e.Board.Stats[objectID].DataLen = int16(len(program))
		e.BoardClose()
		e.DrainScreenDirty()
	}); err != nil {
		t.Fatal(err)
	}

	msg, err := session.ProgramText(member, objectID)
	if err != nil {
		t.Fatalf("ProgramText: %v", err)
	}
	if len(msg.Labels) != 1 || msg.Labels[0].Name != "TOUCH" {
		t.Fatalf("ProgramText labels=%v, want [TOUCH]", labelNames(msg.Labels))
	}
	if len(msg.Warnings) != 0 {
		t.Fatalf("clean program warned: %s", warningText(msg.Warnings))
	}

	// Save an "invalid" program: a send to a label that does not exist.
	saveReply, err := session.SaveProgram(member, objectID, []string{"@obj", ":touch", "#send ghost", "#end"})
	if err != nil {
		t.Fatalf("SaveProgram: %v", err)
	}
	if saveReply.Type != MessageTypeEditorStatSettings {
		t.Fatalf("save reply=%+v, want a stat-settings reply (save must succeed)", saveReply)
	}

	// Reopening now surfaces the warning, proving the aid is advisory and the
	// invalid script was nonetheless stored.
	reopened, err := session.ProgramText(member, objectID)
	if err != nil {
		t.Fatalf("ProgramText after save: %v", err)
	}
	if len(reopened.Warnings) != 1 || !strings.Contains(reopened.Warnings[0].Message, "GHOST") {
		t.Fatalf("post-save warnings=%q, want one naming GHOST", warningText(reopened.Warnings))
	}
}
