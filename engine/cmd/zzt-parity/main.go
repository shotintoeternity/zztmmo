// Command zzt-parity is the single repo command for M16 parity certification.
//
// It runs the clean gates that the project already relies on — go build, go vet,
// go test, go test -race, npm ci, npm test, npm run build — and emits a
// deterministic JSON + Markdown report keyed by the M16 traceability manifest
// (fixtures/parity/manifest.json). The report names the exact test certifying
// each already-verified row and never treats aggregate line coverage as a parity
// claim (PARITY.md §2). See report.go for the report model.
//
// Usage (canonical):
//
//	cd engine && go run ./cmd/zzt-parity            # run gates + write report
//	make parity                                     # same, from the repo root
//
// Flags:
//
//	-out DIR        directory for report.json / report.md (default: <root>/fixtures/parity)
//	-run-gates      run the clean gates (default true); -run-gates=false only
//	                re-renders the report from the current manifest
//	-race           include the `go test -race` gate (default true)
//
// The command exits non-zero if any gate fails or the manifest is not yet
// certified, so CI can gate on it while still uploading the report artifact.
// It writes only into -out (which is gitignored), so a clean run leaves
// `git status --short` empty.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	outDir := flag.String("out", "", "directory for report.json/report.md (default: <repo>/fixtures/parity)")
	runGates := flag.Bool("run-gates", true, "run the clean gates before writing the report")
	withRace := flag.Bool("race", true, "include the `go test -race` gate")
	requireCertified := flag.Bool("require-certified", false, "exit non-zero unless every manifest row is certified (the M16.20 gate)")
	flag.Parse()

	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zzt-parity: %v\n", err)
		os.Exit(2)
	}
	manifestPath := filepath.Join(root, "fixtures", "parity", "manifest.json")
	manifestRel := "fixtures/parity/manifest.json"

	m, err := loadManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zzt-parity: %v\n", err)
		os.Exit(2)
	}

	var gates []gateResult
	if *runGates {
		gates = runCleanGates(root, *withRace)
	} else {
		gates = plannedGates(*withRace)
		for i := range gates {
			gates[i].Skipped = true
		}
	}

	rep := buildReport(m, manifestRel, gates)

	dir := *outDir
	if dir == "" {
		dir = filepath.Join(root, "fixtures", "parity")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "zzt-parity: %v\n", err)
		os.Exit(2)
	}
	if err := writeReportFiles(dir, rep); err != nil {
		fmt.Fprintf(os.Stderr, "zzt-parity: %v\n", err)
		os.Exit(2)
	}

	printSummary(rep, dir)

	// Exit policy (after the report artifact is written above, so CI can always
	// upload it):
	//   - a failed/skipped clean gate is always a hard failure;
	//   - not-yet-certified is expected before M16.20 and is a failure only
	//     under -require-certified (the final certification gate).
	if *runGates {
		for _, g := range gates {
			if g.Skipped || !g.Passed {
				os.Exit(1)
			}
		}
	}
	if *requireCertified && !rep.Certified {
		os.Exit(1)
	}
}

// findRepoRoot walks up from the working directory until it finds the parity
// manifest, so the command works regardless of the directory it is invoked from.
func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "fixtures", "parity", "manifest.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate fixtures/parity/manifest.json from %s", cwd)
		}
		dir = parent
	}
}

func loadManifest(path string) (*manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decoding manifest %s: %w", path, err)
	}
	return &m, nil
}

// plannedGates is the fixed, ordered list of clean gates. `go test` runs with
// -count=1 because the parity manifest validator reads files Go's test cache
// does not track (NOTES.md 2026-07-15), so a cached pass could otherwise mask a
// manifest edit.
func plannedGates(withRace bool) []gateResult {
	engine := "engine"
	web := "engine/web"
	gates := []gateResult{
		{Name: "go build", Command: "go build ./...", Dir: engine},
		{Name: "go vet", Command: "go vet ./...", Dir: engine},
		{Name: "go test", Command: "go test -count=1 ./...", Dir: engine},
	}
	if withRace {
		gates = append(gates, gateResult{Name: "go test -race", Command: "go test -race -count=1 ./...", Dir: engine})
	}
	gates = append(gates,
		gateResult{Name: "npm ci", Command: "npm ci", Dir: web},
		gateResult{Name: "npm test", Command: "npm test", Dir: web},
		gateResult{Name: "npm run build", Command: "npm run build", Dir: web},
	)
	return gates
}

// runCleanGates executes each gate in order, streaming its output to the
// console, and records the pass/fail verdict. A failed gate does not stop the
// run: all gates execute so the report reflects the full picture.
func runCleanGates(root string, withRace bool) []gateResult {
	gates := plannedGates(withRace)
	for i, g := range gates {
		fmt.Printf("\n=== gate: %s (%s in %s) ===\n", g.Name, g.Command, g.Dir)
		args := splitCommand(g.Command)
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = filepath.Join(root, g.Dir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		gates[i].Passed = err == nil
		if err != nil {
			fmt.Printf("=== gate %s FAILED: %v ===\n", g.Name, err)
		} else {
			fmt.Printf("=== gate %s passed ===\n", g.Name)
		}
	}
	return gates
}

// splitCommand does a trivial whitespace split; every gate command above is a
// fixed literal with no quoting or shell metacharacters.
func splitCommand(s string) []string {
	var out []string
	field := ""
	for _, r := range s {
		if r == ' ' {
			if field != "" {
				out = append(out, field)
				field = ""
			}
			continue
		}
		field += string(r)
	}
	if field != "" {
		out = append(out, field)
	}
	return out
}

func writeReportFiles(dir string, rep report) error {
	jsonPath := filepath.Join(dir, "report.json")
	jf, err := os.Create(jsonPath)
	if err != nil {
		return err
	}
	if err := writeJSON(jf, rep); err != nil {
		jf.Close()
		return err
	}
	if err := jf.Close(); err != nil {
		return err
	}

	mdPath := filepath.Join(dir, "report.md")
	mf, err := os.Create(mdPath)
	if err != nil {
		return err
	}
	if err := writeMarkdown(mf, rep); err != nil {
		mf.Close()
		return err
	}
	return mf.Close()
}

func printSummary(rep report, dir string) {
	fmt.Printf("\n=== parity report written to %s/report.{json,md} ===\n", dir)
	verdict := "NOT CERTIFIED"
	if rep.Certified {
		verdict = "CERTIFIED"
	}
	fmt.Printf("manifest: %d rows | verdict: %s\n", rep.TotalRows, verdict)
	if len(rep.Blockers) > 0 {
		fmt.Printf("blockers (%d):\n", len(rep.Blockers))
		for _, b := range rep.Blockers {
			fmt.Printf("  - %s\n", b)
		}
	}
}
