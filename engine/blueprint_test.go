package zztgo

import (
	"strings"
	"testing"
)

func TestBoardBlueprintRendersCompilableReachableZWD(t *testing.T) {
	bp := BoardBlueprint{
		Version: 1,
		Board:   "Relay",
		Start:   BlueprintPoint{X: 3, Y: 3},
		Exits:   BlueprintExits{},
		Background: BlueprintTile{
			Element: "Solid", Color: "0x07",
		},
		Floor: BlueprintTile{Element: "Empty", Color: "0x00"},
		Operations: []BlueprintOperation{
			{Kind: "fill", X: 2, Y: 2, X2: 59, Y2: 24, Tile: &BlueprintTile{Element: "Empty", Color: "0x00"}},
			{Kind: "border", X: 8, Y: 5, X2: 20, Y2: 10, Tile: &BlueprintTile{Element: "Normal", Color: "0x08"}},
			{Kind: "path", X: 3, Y: 3, X2: 30, Y2: 12, Width: 2, Bend: "horizontal-first", Tile: &BlueprintTile{Element: "Fake", Color: "0x17"}},
			{Kind: "text", X: 24, Y: 2, Text: "RELAY", Color: "Text-Cyan"},
		},
		Actors: []BlueprintActor{{
			Element: "Object", X: 30, Y: 12, Color: "0x0E", Character: "?",
			OOP: "@finale\n:touch\nThe relay clears its throat.\n#endgame\n#end",
		}},
	}
	section, err := RenderBoardBlueprint(bp, "Relay")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(section, "\n") < BOARD_HEIGHT || !strings.Contains(section, "stat at 30,12 element Object") {
		t.Fatalf("lowered section is incomplete:\n%s", section)
	}
	src := "zwd 1\nworld \"RELAY\"\n" + section
	if _, err := CompileZWD(src); err != nil {
		t.Fatalf("compiled blueprint: %v\n%s", err, src)
	}
	if routes := EvalZWDLayoutRoutes(src); !routes.Passed {
		t.Fatalf("route failures: %s", routes.Detail)
	}
	if oop := EvalZWDOOP(src); !oop.Passed {
		t.Fatalf("OOP failures: %s", oop.Detail)
	}
}

func TestBoardBlueprintCarvesDeclaredPort(t *testing.T) {
	north := 17
	bp := BoardBlueprint{
		Version: 1, Board: "Gate", Start: BlueprintPoint{X: 17, Y: 4},
		Exits: BlueprintExits{North: "Next"}, Ports: BlueprintPorts{North: &north},
		Background: BlueprintTile{Element: "Solid", Color: "0x07"},
		Floor:      BlueprintTile{Element: "Empty", Color: "0x00"},
		Operations: []BlueprintOperation{{Kind: "path", X: 17, Y: 4, X2: 17, Y2: 2, Tile: &BlueprintTile{Element: "Empty", Color: "0x00"}}},
	}
	section, err := RenderBoardBlueprint(bp, "Gate")
	if err != nil {
		t.Fatal(err)
	}
	_, parsed, err := extractGeneratedBoard("```zwd\n"+section+"```", "Gate")
	if err != nil {
		t.Fatal(err)
	}
	for _, y := range []int{1, 2} {
		key := parsed.grid[y-1].text[north-1]
		if parsed.legend[key].element != E_EMPTY {
			t.Fatalf("port cell (%d,%d) was not carved", north, y)
		}
	}
}

func TestBoardBlueprintRejectsSchemaAndSemanticErrors(t *testing.T) {
	if _, err := ParseBoardBlueprint(`{"version":1,"mystery":true}`); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	base := BoardBlueprint{
		Version: 1, Board: "Bad", Start: BlueprintPoint{X: 1, Y: 1},
		Exits:      BlueprintExits{East: "Elsewhere"},
		Background: BlueprintTile{Element: "Empty", Color: "0x00"},
		Floor:      BlueprintTile{Element: "Empty", Color: "0x00"},
	}
	if _, err := RenderBoardBlueprint(base, "Bad"); err == nil || !strings.Contains(err.Error(), "ports.east is omitted") {
		t.Fatalf("missing port error = %v", err)
	}
	base.Exits = BlueprintExits{}
	base.Actors = []BlueprintActor{{Element: "Object", X: 1, Y: 1, Color: "white"}}
	if _, err := RenderBoardBlueprint(base, "Bad"); err == nil || !strings.Contains(err.Error(), "already occupied") {
		t.Fatalf("actor collision error = %v", err)
	}
}
