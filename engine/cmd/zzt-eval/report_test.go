package main

import (
	"strings"
	"testing"

	"github.com/benhoyt/zztgo"
)

func TestWriteReport(t *testing.T) {
	results := []runResult{
		{
			Premise: "a test premise", Grounded: true, Label: "test-premise-grounded",
			WorldName: "GEMCAVE", DisplayName: "Gem Cave",
			Gate: zztgo.EvalReport{WorldName: "GEMCAVE", Checks: []zztgo.EvalCheck{
				{Name: "compiles", Passed: true},
				{Name: "title-wordmark", Passed: false, Detail: "no text row spells \"Gem Cave\" | pipe"},
			}},
			Shots: []shotRef{
				{Label: "title (board 0)", Path: "shots/test-board0.png"},
				{Label: "board 2", Err: "render exploded"},
			},
			Verdict: &zztgo.EvalVerdict{
				Scores: []zztgo.EvalScore{
					{Dimension: "title-legibility", Score: 4, Justification: "clean"},
					{Dimension: "visual-composition", Score: 3, Justification: "fine"},
					{Dimension: "oop-voice", Score: 2, Justification: "flat"},
					{Dimension: "grounding-accuracy", Score: -1, Justification: "n/a"},
				},
				Overall: "workable",
			},
		},
		{
			Premise: "another premise", Grounded: false, Label: "another-premise-plain",
			GenError: "plan did not converge",
		},
	}

	var b strings.Builder
	if err := writeReport(&b, results); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"# ZZT generation quality report",
		"| test-premise-grounded | GEMCAVE | **FAIL** (title-wordmark) | 4 | 3 | 2 | n/a |",
		"| title-wordmark | **FAIL** | no text row spells \"Gem Cave\" \\| pipe |",
		"![title (board 0)](shots/test-board0.png)",
		"Screenshot board 2 failed: render exploded",
		"**Generation failed:** plan did not converge",
		"workable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n---\n%s", want, out)
		}
	}
}
