package zztgo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidatePlanLASTLITE is the exemplar: the reference world plan must parse
// and pass every mechanical check.
func TestValidatePlanLASTLITE(t *testing.T) {
	src := readLASTLITE(t)
	plan, err := ParsePlan(src)
	if err != nil {
		t.Fatalf("ParsePlan(LASTLITE): %v", err)
	}
	if len(plan.Boards) != 12 {
		t.Fatalf("want 12 boards, got %d", len(plan.Boards))
	}
	if len(plan.Spine) != 6 {
		t.Fatalf("want 6 spine steps, got %d", len(plan.Spine))
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("LASTLITE should be a valid plan, got:\n%v", err)
	}
}

// TestValidatePlanParsesLinks checks the link tokenizer against LASTLITE's
// mixed edge/passage, comma/space, and annotated cells.
func TestValidatePlanParsesLinks(t *testing.T) {
	src := readLASTLITE(t)
	plan, err := ParsePlan(src)
	if err != nil {
		t.Fatal(err)
	}
	get := func(id string) PlanBoard {
		for _, b := range plan.Boards {
			if b.ID == id {
				return b
			}
		}
		t.Fatalf("board %q not found", id)
		return PlanBoard{}
	}

	// cliffstair: "W→moor N→lighthouse, passage↔cottage" — two edges + one bidir
	// passage, separated by both spaces and a comma.
	cs := get("cliffstair")
	wantCS := []PlanLink{
		{Kind: "edge", Dir: "W", Target: "moor"},
		{Kind: "edge", Dir: "N", Target: "lighthouse"},
		{Kind: "passage", Bidir: true, Target: "cottage"},
	}
	if !linksEqual(cs.Links, wantCS) {
		t.Fatalf("cliffstair links = %+v, want %+v", cs.Links, wantCS)
	}

	// lighthouse: "S→cliffstair, passage→lamproom" — one-way passage.
	lh := get("lighthouse")
	wantLH := []PlanLink{
		{Kind: "edge", Dir: "S", Target: "cliffstair"},
		{Kind: "passage", Bidir: false, Target: "lamproom"},
	}
	if !linksEqual(lh.Links, wantLH) {
		t.Fatalf("lighthouse links = %+v, want %+v", lh.Links, wantLH)
	}

	// title: "—" — no links.
	if tl := get("title"); len(tl.Links) != 0 {
		t.Fatalf("title should have no links, got %+v", tl.Links)
	}
}

// TestValidatePlanOrphanBoard: a board no other board links to must be rejected.
func TestValidatePlanOrphanBoard(t *testing.T) {
	// Two boards: start links nowhere; the second is unreachable.
	src := `# World Plan: ORPHAN

## Board graph

| # | id    | name  | concept | dark | exits/links |
|---|-------|-------|---------|------|-------------|
| 0 | title | Title | title   | no   | —           |
| 1 | start | Start | START.  | no   | —           |
| 2 | attic | Attic | lonely  | no   | —           |

## Progression spine

1. start: nothing. #endgame
`
	err := ValidatePlan(src)
	if err == nil {
		t.Fatal("expected an error for an unreachable board")
	}
	if !strings.Contains(err.Error(), `board "attic" is not reachable`) {
		t.Fatalf("wrong message: %v", err)
	}
}

// TestValidatePlanKeyBehindOwnDoor: the door precedes the key that opens it.
func TestValidatePlanKeyBehindOwnDoor(t *testing.T) {
	src := `# World Plan: DEADLOCK

## Board graph

| # | id    | name  | concept | dark | exits/links |
|---|-------|-------|---------|------|-------------|
| 0 | title | Title | title   | no   | —           |
| 1 | start | Start | START.  | no   | E→vault     |
| 2 | vault | Vault | the key | no   | W→start     |

## Progression spine

1. start → vault through the **BLUE DOOR**.
2. vault: grab the **BLUE KEY**. #endgame
`
	err := ValidatePlan(src)
	if err == nil {
		t.Fatal("expected an error for a key locked behind its own door")
	}
	if !strings.Contains(err.Error(), "a key behind its own door") {
		t.Fatalf("wrong message: %v", err)
	}
}

// TestValidatePlanMissingPassageTarget: a passage points at a board that does
// not exist.
func TestValidatePlanMissingPassageTarget(t *testing.T) {
	src := `# World Plan: NOWHERE

## Board graph

| # | id    | name  | concept | dark | exits/links      |
|---|-------|-------|---------|------|------------------|
| 0 | title | Title | title   | no   | —                |
| 1 | start | Start | START.  | no   | passage→ghosttown |

## Progression spine

1. start: step into the void. #endgame
`
	err := ValidatePlan(src)
	if err == nil {
		t.Fatal("expected an error for a missing passage target")
	}
	if !strings.Contains(err.Error(), `passage target "ghosttown" is not a board`) {
		t.Fatalf("wrong message: %v", err)
	}
}

// TestValidatePlanReciprocity: a one-way east exit with no way back is rejected.
func TestValidatePlanReciprocity(t *testing.T) {
	src := `# World Plan: ONEWAY

## Board graph

| # | id    | name  | concept | dark | exits/links |
|---|-------|-------|---------|------|-------------|
| 0 | title | Title | title   | no   | —           |
| 1 | start | Start | START.  | no   | E→cliff     |
| 2 | cliff | Cliff | trap    | no   | —           |

## Progression spine

1. start → cliff. #endgame
`
	err := ValidatePlan(src)
	if err == nil {
		t.Fatal("expected a reciprocity error")
	}
	if !strings.Contains(err.Error(), "no return exit") {
		t.Fatalf("wrong message: %v", err)
	}
}

func TestParsePlanNormalizesPlannerStyleLinks(t *testing.T) {
	src := `# World Plan: SPACED

## Board graph

| # | id | name | concept | dark | exits/links |
|---|----|------|---------|------|-------------|
| 0 | title | Title Screen | title | no | — |
| 1 | shopfront | Shop Front | START. opening | no | E → Kitchen Floor |
| 2 | kitchen | Kitchen Floor | ovens | no | W → Shop Front |

## Progression spine

1. shopfront → kitchen. #endgame
`
	plan, err := ParsePlan(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("planner-style links should normalize, got %v", err)
	}
	if got := plan.Boards[1].Links[0].Target; got != "kitchen" {
		t.Fatalf("east target = %q, want kitchen", got)
	}
}

func readLASTLITE(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "llmworld", "plans", "LASTLITE.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func linksEqual(a, b []PlanLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
