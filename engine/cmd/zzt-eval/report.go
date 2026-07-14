package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/benhoyt/zztgo"
)

type shotRef struct {
	Label string
	Path  string // report-relative PNG path; "" when Err is set
	Err   string
}

type runResult struct {
	Premise     string
	Grounded    bool
	Label       string
	WorldName   string
	DisplayName string
	GenError    string
	Gate        zztgo.EvalReport
	Shots       []shotRef
	Verdict     *zztgo.EvalVerdict
	JudgeError  string
}

// writeReport renders the scored Markdown report. Screenshot paths are
// relative to the report file so the document is self-contained in -out.
func writeReport(w io.Writer, results []runResult) error {
	fmt.Fprintf(w, "# ZZT generation quality report (M12.17)\n\n")
	fmt.Fprintf(w, "Rubric and premise set: `llmworld/EVAL.md`. Scores are 0-5; `n/a` = ungrounded run.\n\n")

	fmt.Fprintf(w, "## Summary\n\n")
	fmt.Fprintf(w, "| run | world | tier-1 gate | %s |\n", strings.Join(zztgo.EvalDimensions, " | "))
	fmt.Fprintf(w, "|---|---|---|%s\n", strings.Repeat("---|", len(zztgo.EvalDimensions)))
	for _, r := range results {
		fmt.Fprintf(w, "| %s | %s | %s |%s\n", r.Label, r.WorldName, gateSummary(r), scoreCells(r))
	}
	fmt.Fprintf(w, "\n")

	for _, r := range results {
		fmt.Fprintf(w, "## %s\n\n", r.Label)
		fmt.Fprintf(w, "Premise: %s\n\nGrounded: %t\n\n", r.Premise, r.Grounded)
		if r.GenError != "" {
			fmt.Fprintf(w, "**Generation failed:** %s\n\n", r.GenError)
			continue
		}
		fmt.Fprintf(w, "World: `%s` (display name %q)\n\n", r.WorldName, r.DisplayName)

		fmt.Fprintf(w, "### Tier-1 structural gate\n\n")
		fmt.Fprintf(w, "| check | result | detail |\n|---|---|---|\n")
		for _, c := range r.Gate.Checks {
			mark := "PASS"
			if !c.Passed {
				mark = "**FAIL**"
			}
			fmt.Fprintf(w, "| %s | %s | %s |\n", c.Name, mark, markdownCell(c.Detail))
		}
		fmt.Fprintf(w, "\n")

		if r.Verdict != nil {
			fmt.Fprintf(w, "### Judge scores\n\n")
			fmt.Fprintf(w, "| dimension | score | justification |\n|---|---|---|\n")
			for _, s := range r.Verdict.Scores {
				fmt.Fprintf(w, "| %s | %s | %s |\n", s.Dimension, scoreCell(s.Score), markdownCell(s.Justification))
			}
			fmt.Fprintf(w, "\n%s\n\n", r.Verdict.Overall)
		} else if r.JudgeError != "" {
			fmt.Fprintf(w, "### Judge scores\n\n**Judge failed:** %s\n\n", r.JudgeError)
		}

		for _, shot := range r.Shots {
			if shot.Err != "" {
				fmt.Fprintf(w, "Screenshot %s failed: %s\n\n", shot.Label, shot.Err)
				continue
			}
			fmt.Fprintf(w, "![%s](%s)\n\n", shot.Label, shot.Path)
		}
	}
	return nil
}

func gateSummary(r runResult) string {
	if r.GenError != "" {
		return "generation failed"
	}
	failures := r.Gate.Failures()
	if len(failures) == 0 {
		return "PASS"
	}
	var names []string
	for _, f := range failures {
		names = append(names, f.Name)
	}
	return "**FAIL** (" + strings.Join(names, ", ") + ")"
}

func scoreCells(r runResult) string {
	if r.Verdict == nil {
		return strings.Repeat(" — |", len(zztgo.EvalDimensions))
	}
	byDim := map[string]int{}
	for _, s := range r.Verdict.Scores {
		byDim[s.Dimension] = s.Score
	}
	var b strings.Builder
	for _, dim := range zztgo.EvalDimensions {
		score, ok := byDim[dim]
		if !ok {
			b.WriteString(" — |")
			continue
		}
		fmt.Fprintf(&b, " %s |", scoreCell(score))
	}
	return b.String()
}

func scoreCell(score int) string {
	if score < 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d", score)
}

func markdownCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
