package zztgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// M12.23 — generated-world acceptance and targeted repair hardening.
//
// A live BAKERY generation reached a browser with a board that panicked when a
// player walked into it, a title the authoring gate would have rejected, and a
// picker that hid the file entirely. The five tests here are the five ways that
// must not happen again: a malformed blueprint is repaired board-only, a board
// that cannot survive play is caught and named before persistence, the
// self-erasing OOP form is rejected statically even where no simulation can
// reach it, bad title art repaints board 0 instead of shipping, and a generated
// world is listed and selectable.

// rawSection strips the ```zwd fence the fake-model board helpers add, giving a
// section that can be dropped straight into assembleGeneratedZWD.
func rawSection(section string) string {
	return strings.TrimSuffix(strings.TrimPrefix(section, "```zwd\n"), "\n```")
}

// TestM1223MalformedOperationKindRepairsOnlyThatBoard covers item 1's acceptance
// consequence: the blueprint decoder runs with DisallowUnknownFields, so a model
// that writes `type` where the contract says `kind` is rejected — and that
// rejection must cost one board's attempt, not the world. The deployed build at
// 6e7dc60 failed every generation this way.
func TestM1223MalformedOperationKindRepairsOnlyThatBoard(t *testing.T) {
	plan := generationPlan("1. start: begin. #endgame")
	malformed := `{"version":1,"board":"Start","start":{"x":1,"y":1},` +
		`"background":{"element":"Empty","color":"0x00"},` +
		`"floor":{"element":"Empty","color":"0x00"},` +
		`"operations":[{"type":"rect","x":2,"y":2,"w":3,"h":3,"element":"Solid","color":"0x07"}]}`
	fake, claude := newFakeClaude(t, plan, malformed,
		generatedBlueprint(t, "Start", false), generatedBlueprint(t, "Title", true))
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 3)
	if _, err := service.Generate(context.Background(), "test", "a malformed blueprint", "KINDOK", nil, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 4 {
		t.Fatalf("API calls = %d, want planner, bad Start, repaired Start, Title", len(fake.requests))
	}
	repair := fake.requests[2].Messages[0].Content
	if !strings.Contains(repair, `unknown field "type"`) {
		t.Fatalf("repair prompt did not name the malformed encoding:\n%s", repair)
	}
	if !strings.Contains(repair, `Repair only board "Start"`) {
		t.Fatalf("repair prompt was not scoped to the failing board:\n%s", repair)
	}
}

// TestM1223CrashingBoardIsNamedAndRepaired is item 2: a board that panics under
// play is caught before persistence, attributed to the board that produced it,
// and repaired — rather than failing the whole world with an unattributed
// "headless validation panicked".
func TestM1223CrashingBoardIsNamedAndRepaired(t *testing.T) {
	plan := generationPlan("1. start: begin. #endgame")
	// #change Object Empty rewrites the running object's own tile and leaves its
	// stat pointing at nothing; the next tick reads a stat with no element.
	crashing := strings.Replace(generatedBoard("Start", false),
		"    @finale\n    #end\n", "    @finale\n    #change Object Empty\n    #end\n", 1)
	if crashing == generatedBoard("Start", false) {
		t.Fatal("test fixture did not inject the self-erasing #change")
	}
	fake, claude := newFakeClaude(t, plan, crashing,
		generatedBoard("Start", false), generatedBoard("Title", false))
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 3)
	result, err := service.Generate(context.Background(), "test", "a crashing room", "CRASHOK", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) < 3 {
		t.Fatalf("API calls = %d, want the crashing board to have been repaired", len(fake.requests))
	}
	repair := fake.requests[2].Messages[0].Content
	if !strings.Contains(repair, `board 1 "Start"`) || !strings.Contains(repair, "simulation panicked") {
		t.Fatalf("repair prompt did not name the board that crashed:\n%s", repair)
	}
	if strings.Contains(result.ZWD, "#change Object Empty") {
		t.Fatal("the crashing board was persisted")
	}
	// Whatever else acceptance salvages, the shipped world must survive play.
	data, err := CompileZWD(result.ZWD)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGeneratedZWD(data); err != nil {
		t.Fatalf("accepted world does not survive play: %v", err)
	}
}

// TestM1223SelfErasingChangeRejectedBehindTouchLabel is item 3. Simulation alone
// cannot catch this: nothing touches the object, so the label never runs and the
// world ticks happily for its 200 acceptance steps. Only static analysis of the
// whole program reaches it — which is why the check lives in OopAnalyze rather
// than in a prompt instruction.
func TestM1223SelfErasingChangeRejectedBehindTouchLabel(t *testing.T) {
	plan := generationPlan("1. start: begin. #endgame")
	hidden := strings.Replace(generatedBoard("Start", false),
		"    :touch\n    #endgame\n", "    :touch\n    #change Object Empty\n    #endgame\n", 1)
	if hidden == generatedBoard("Start", false) {
		t.Fatal("test fixture did not inject the self-erasing #change under :touch")
	}

	// It is genuinely unreachable by simulation...
	parsed, err := ParsePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	data, err := CompileZWD(assembleGeneratedZWD("HIDDEN", parsed, map[string]string{
		"Title": rawSection(generatedBoard("Title", false)),
		"Start": rawSection(hidden),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGeneratedZWD(data); err != nil {
		t.Fatalf("simulation unexpectedly reached the touch label: %v", err)
	}

	// ...and it is still rejected, with the repair the prompt should apply.
	// Boards are painted start-then-title, so the acceptance repair is the fourth
	// call: planner, Start, Title, then Start again.
	fake, claude := newFakeClaude(t, plan, hidden,
		generatedBoard("Title", false), generatedBoard("Start", false))
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 3)
	result, err := service.Generate(context.Background(), "test", "a hidden self-erasure", "TOUCHOK", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 4 {
		t.Fatalf("API calls = %d, want planner, Start, Title, repaired Start", len(fake.requests))
	}
	repair := fake.requests[3].Messages[0].Content
	if !strings.Contains(repair, `board name="Start"`) {
		t.Fatalf("the acceptance repair went to the wrong board:\n%s", repair)
	}
	if !strings.Contains(repair, "#change Object Empty") || !strings.Contains(repair, "#die") {
		t.Fatalf("repair prompt did not reject the self-erasing #change or name #die:\n%s", repair)
	}
	if strings.Contains(result.ZWD, "#change Object Empty") {
		t.Fatal("the self-erasing board was persisted")
	}
}

// TestM1223BadTitleArtRepairsBoardZero is item 4: the title-screen checks
// zzt-build has always run now run before persistence too, and a failure is
// charged to board 0 rather than sinking the world or shipping past the gate.
func TestM1223BadTitleArtRepairsBoardZero(t *testing.T) {
	plan := generationPlan("1. start: begin. #endgame")
	// A Torch is on the title brief's banned list — the exact element the live
	// BAKERY title carried when the authoring build rejected it.
	badTitle := strings.Replace(generatedBoard("Title", false),
		"@o"+strings.Repeat(".", 58), "@ot"+strings.Repeat(".", 57), 1)
	badTitle = strings.Replace(badTitle,
		"    o = Object color 0x0F", "    o = Object color 0x0F\n    t = Torch color 0x0E", 1)
	if !strings.Contains(badTitle, "Torch") {
		t.Fatal("test fixture did not place a Torch on the title board")
	}
	fake, claude := newFakeClaude(t, plan, generatedBoard("Start", false),
		badTitle, generatedBoard("Title", false))
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 3)
	result, err := service.Generate(context.Background(), "test", "a title with a torch", "TITLEOK", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 4 {
		t.Fatalf("API calls = %d, want planner, Start, bad Title, repaired Title", len(fake.requests))
	}
	repair := fake.requests[3].Messages[0].Content
	if !strings.Contains(repair, `board name="Title"`) || !strings.Contains(repair, "title-no-creatures-or-items") {
		t.Fatalf("title failure did not produce a board-0 repair:\n%s", repair)
	}
	if !strings.Contains(repair, "Torch at (3,1)") {
		t.Fatalf("title repair did not name the offending element:\n%s", repair)
	}
	if strings.Contains(result.ZWD, "Torch") {
		t.Fatal("the rejected title art was persisted")
	}
	// The shipped world passes the authoring gate's title checks.
	if report := EvalGeneratedZWD(result.ZWD, "Dream"); !report.Passed() {
		t.Fatalf("accepted world fails the authoring quality gate: %v", report.Failures())
	}
}

// TestM1223GeneratedWorldIsListedAndSelectable is item 5, end to end: a world
// the generator just wrote into the server's worlds directory is offered by
// /api/worlds and can be selected, with a safe local metadata fallback rather
// than being hidden for want of a Museum catalog row.
func TestM1223GeneratedWorldIsListedAndSelectable(t *testing.T) {
	dir := t.TempDir()
	plan := generationPlan("1. start: begin. #endgame")
	fake, claude := newFakeClaude(t, plan, generatedBoard("Start", false), generatedBoard("Title", false))
	defer claude.Close()
	service := newGenerationTestService(t, claude.URL, 3)
	service.outputDir = dir

	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.WorldsDir = dir
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Generator: service}
	handler := api.Handler()

	if _, err := service.Generate(context.Background(), "test", "a listed world", "DREAMED", server, false); err != nil {
		t.Fatal(err)
	}
	_ = fake

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/worlds", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/api/worlds status=%d, want 200", recorder.Code)
	}
	var body struct {
		Worlds []WorldListEntry `json:"worlds"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /api/worlds: %v (%s)", err, recorder.Body.String())
	}
	var listed *WorldListEntry
	for i := range body.Worlds {
		if body.Worlds[i].World == "DREAMED" {
			listed = &body.Worlds[i]
		}
	}
	if listed == nil {
		t.Fatalf("generated world is missing from the picker: %+v", body.Worlds)
	}
	// M18.9 changed the fallback: a world this server dreamed is credited as
	// such and grouped apart from uncatalogued community files, which keep the
	// "Local" label. The point of this assertion is unchanged — a generated
	// world reaches the picker with a title and a sensible author.
	if listed.Title == "" || listed.Author != "Dreamed here" {
		t.Fatalf("generated world listed as %+v, want a title and the dreamed author fallback", *listed)
	}
	if listed.Kind != WorldKindDreamed {
		t.Fatalf("generated world kind = %q, want %q — the .zwd beside it is what says so", listed.Kind, WorldKindDreamed)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/loadworld", strings.NewReader(`{"name":"DREAMED"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("selecting the generated world = %d (%s), want 200", recorder.Code, recorder.Body.String())
	}
}
