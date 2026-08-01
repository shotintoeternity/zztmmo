// Command zzt-parity is the single repo command for M16 parity certification.
//
// It runs the clean gates the project relies on, in the order PARITY.md §8
// fixes — npm ci, the pinned Playwright engines, npm run build, npm test, then
// go build, go vet, go test and go test -race — and emits a deterministic JSON
// + Markdown report keyed by the M16 traceability manifest
// (fixtures/parity/manifest.json), plus a run record (run.json) carrying the
// tool versions, commit and per-gate wall clock the report deliberately
// excludes. The report names the exact test certifying each verified row, lists
// every test the run skipped, and never treats aggregate line coverage as a
// parity claim (PARITY.md §2). See report.go for the report model.
//
// Usage (canonical):
//
//	cd engine && go run ./cmd/zzt-parity            # run gates + write report
//	make parity                                     # same, from the repo root
//	make certify                                    # the same run, gated (M16.20)
//
// Flags:
//
//	-out DIR            directory for the report/run artifacts (default: <root>/fixtures/parity)
//	-run-gates          run the clean gates (default true); -run-gates=false only
//	                    re-renders the report from the current manifest
//	-race               include the `go test -race` gate (default true)
//	-browser            install the pinned Playwright engines and require the
//	                    real-browser suites to run rather than skip (default true)
//	-require-certified  exit non-zero unless the manifest certifies (the M16.20 gate)
//
// The command exits non-zero if any gate fails, and under -require-certified if
// the manifest is not certified, so CI can gate on it while still uploading the
// artifacts. It writes only into -out (which is gitignored), so a clean run
// leaves `git status --short` empty.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	outDir := flag.String("out", "", "directory for report.json/report.md (default: <repo>/fixtures/parity)")
	runGates := flag.Bool("run-gates", true, "run the clean gates before writing the report")
	withRace := flag.Bool("race", true, "include the `go test -race` gate")
	requireCertified := flag.Bool("require-certified", false, "exit non-zero unless every manifest row is certified (the M16.20 gate)")
	withBrowser := flag.Bool("browser", true, "install the pinned Playwright engines and require the real-browser suites to run rather than skip (task M16.20)")
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
	var skips []skipRecord
	var timings []gateTiming
	var loadMetrics string
	if *runGates {
		gates, skips, timings, loadMetrics = runCleanGates(root, *withRace, *withBrowser)
	} else {
		gates = plannedGates(*withRace, *withBrowser)
		for i := range gates {
			gates[i].Skipped = true
		}
	}

	// M16.18's device/browser matrix travels with the report. A missing file is
	// not fatal — the report says so and refuses to certify (deviceMatrixBlockers)
	// rather than failing the command that was asked to write it.
	devices, err := loadDeviceMatrix(filepath.Join(root, "fixtures", "parity", "device-matrix.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "zzt-parity: %v\n", err)
	}

	rep := buildReport(m, manifestRel, gates, devices, skips)

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
	// The run record is everything about THIS run that the report deliberately
	// excludes because it is not a function of the tree: tool versions, the
	// commit, per-gate wall clock, and the load run's measured numbers (task
	// M16.20). Keeping it in its own file is what lets two certification runs
	// produce byte-identical reports and still publish their timings.
	if *runGates {
		if err := writeRunRecord(dir, root, rep, timings, loadMetrics); err != nil {
			fmt.Fprintf(os.Stderr, "zzt-parity: %v\n", err)
			os.Exit(2)
		}
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

// loadDeviceMatrix reads M16.18's device/browser matrix. A matrix that is not
// there returns (nil, err) so the caller can report both: the report renders
// "none recorded" and lists a blocker, which is stricter than a silent absence.
func loadDeviceMatrix(path string) (*deviceMatrix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the device/browser matrix: %w", err)
	}
	var matrix deviceMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &matrix, nil
}

// plannedGates is the fixed, ordered list of clean gates. `go test` runs with
// -count=1 because the parity manifest validator reads files Go's test cache
// does not track (NOTES.md 2026-07-15), so a cached pass could otherwise mask a
// manifest edit.
//
// ORDER MATTERS (task M16.20). The browser track — `npm ci`, the pinned
// Playwright engines and the built client — comes FIRST, because the real-
// browser suites live inside `go test ./...` and skip themselves when the
// harness is absent. Running the Go gates first, as this list used to, meant a
// clean clone certified itself with every browser suite silently skipped: the
// exact hole M16.20's DoD names. The go gates then run under `-json` so those
// skips are recorded by name rather than hidden inside an "ok".
func plannedGates(withRace, withBrowser bool) []gateResult {
	engine := "engine"
	web := "engine/web"
	var gates []gateResult
	gates = append(gates, gateResult{Name: "npm ci", Command: "npm ci", Dir: web})
	if withBrowser {
		gates = append(gates, gateResult{
			Name:    "playwright install",
			Command: "npx playwright install chromium firefox webkit",
			Dir:     web,
		})
	}
	gates = append(gates,
		gateResult{Name: "npm run build", Command: "npm run build", Dir: web},
		gateResult{Name: "npm test", Command: "npm test", Dir: web},
		gateResult{Name: "go build", Command: "go build ./...", Dir: engine},
		gateResult{Name: "go vet", Command: "go vet ./...", Dir: engine},
		// The real-browser suites are mandatory here and nowhere else: this is
		// the gate whose result the manifest's browser rows rest on.
		gateResult{Name: "go test", Command: "go test -count=1 ./...", Dir: engine, goTest: true, requireBrowser: withBrowser},
	)
	if withRace {
		// No requireBrowser: the race gate would otherwise re-run eleven
		// Playwright suites, doubling the certification run for a class of
		// finding the wire-level concurrency tests already cover. They
		// declare-skip here and the report says so, gate by gate.
		gates = append(gates, gateResult{
			Name: "go test -race", Command: "go test -race -count=1 ./...", Dir: engine, goTest: true,
		})
	}
	return gates
}

// requireBrowserEnv makes the real-browser harness fail rather than skip
// (m169RequireBrowserHarness, m1618RequireEngine). The certification run has
// just installed the engines; a suite that skips itself here is a hole in the
// claim, not a courtesy to a checkout that never asked for a browser.
const requireBrowserEnv = "ZZT_PARITY_REQUIRE_BROWSER=1"

// runCleanGates executes each gate in order, streaming its output to the
// console, and records the pass/fail verdict. A failed gate does not stop the
// run: all gates execute so the report reflects the full picture.
func runCleanGates(root string, withRace, withBrowser bool) ([]gateResult, []skipRecord, []gateTiming, string) {
	gates := plannedGates(withRace, withBrowser)
	var skips []skipRecord
	var timings []gateTiming
	var loadMetrics string
	for i, g := range gates {
		fmt.Printf("\n=== gate: %s (%s in %s) ===\n", g.Name, g.Command, g.Dir)
		args := splitCommand(g.Command)
		dir := filepath.Join(root, g.Dir)
		started := time.Now()
		var err error
		if g.goTest {
			var gateSkips []skipRecord
			var metrics string
			gateSkips, metrics, err = runGoTestJSON(g.Name, dir, args, g.requireBrowser)
			skips = append(skips, gateSkips...)
			if metrics != "" && loadMetrics == "" {
				loadMetrics = metrics
			}
		} else {
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Dir = dir
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = gateEnv(false)
			err = cmd.Run()
		}
		gates[i].Passed = err == nil
		timings = append(timings, gateTiming{Name: g.Name, Seconds: time.Since(started).Seconds(), Passed: err == nil})
		if err != nil {
			fmt.Printf("=== gate %s FAILED: %v ===\n", g.Name, err)
		} else {
			fmt.Printf("=== gate %s passed ===\n", g.Name)
		}
	}
	return gates, skips, timings, loadMetrics
}

// gateEnv builds a gate's environment. requireBrowser makes the real-browser
// suites mandatory for that gate; without it they are opt-in and declare-skip
// (m169RequireBrowserHarness).
func gateEnv(requireBrowser bool) []string {
	env := os.Environ()
	if requireBrowser {
		env = append(env, requireBrowserEnv)
	}
	return env
}

// runGoTestJSON runs one `go test` gate under -json, streaming a condensed
// progress line per package and collecting (a) every skipped test with the
// reason it printed and (b) the load run's measured metrics, which M16.19 emits
// as test log lines and M16.20 publishes as an artifact.
func runGoTestJSON(gate, dir string, args []string, requireBrowser bool) ([]skipRecord, string, error) {
	jsonArgs := goTestJSONArgs(args)
	cmd := exec.Command(jsonArgs[0], jsonArgs[1:]...)
	cmd.Dir = dir
	cmd.Env = gateEnv(requireBrowser)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}

	type event struct {
		Action  string  `json:"Action"`
		Package string  `json:"Package"`
		Test    string  `json:"Test"`
		Output  string  `json:"Output"`
		Elapsed float64 `json:"Elapsed"`
	}
	var skips []skipRecord
	var loadMetrics strings.Builder
	output := map[string][]string{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		var ev event
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		key := ev.Package + "\x00" + ev.Test
		switch ev.Action {
		case "output":
			if ev.Test != "" {
				output[key] = append(output[key], strings.TrimRight(ev.Output, "\n"))
			}
			if ev.Test == loadMetricsTest {
				loadMetrics.WriteString(ev.Output)
			}
		case "skip":
			if ev.Test == "" {
				continue // a package with no test files
			}
			skips = append(skips, skipRecord{Gate: gate, Package: ev.Package, Test: ev.Test, Reason: lastMeaningfulLine(output[key])})
			delete(output, key)
		case "pass", "fail":
			// A failing test's whole captured output is echoed: a certification
			// run whose failures cannot be read is not evidence of anything, and
			// -json otherwise swallows them.
			if ev.Action == "fail" && ev.Test != "" {
				fmt.Printf("--- FAIL: %s (%s)\n", ev.Test, ev.Package)
				for _, line := range output[key] {
					fmt.Println(line)
				}
			}
			if ev.Test == "" {
				fmt.Printf("%-8s %-50s %.2fs\n", ev.Action, ev.Package, ev.Elapsed)
			}
			delete(output, key)
		}
	}
	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && waitErr == nil {
		waitErr = scanErr
	}
	return skips, loadMetrics.String(), waitErr
}

// goTestJSONArgs splices `-json` in as a flag of the `test` subcommand, which
// is where it belongs: `go -json test ./...` is not a command, and the first
// M16.20 certification run failed with `go help` output because of it.
func goTestJSONArgs(args []string) []string {
	if len(args) < 2 {
		return args
	}
	out := append([]string{args[0], args[1], "-json"}, args[2:]...)
	return out
}

// loadMetricsTest is M16.19's bounded-load run; its measured numbers are log
// lines, and M16.20 publishes them beside the report.
const loadMetricsTest = "TestM1619ThirtyNetworkClientLoadAndMetrics"

// lastMeaningfulLine returns the reason a test printed as it skipped: the last
// non-empty output line, minus the file:line prefix `t.Skip` puts on it.
func lastMeaningfulLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "=== ") || strings.HasPrefix(line, "--- ") {
			continue
		}
		if idx := strings.Index(line, ".go:"); idx >= 0 {
			if colon := strings.Index(line[idx+4:], ": "); colon >= 0 {
				line = strings.TrimSpace(line[idx+4+colon+2:])
			}
		}
		return line
	}
	return ""
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

// gateTiming is one gate's wall clock. Deliberately outside the report: a
// duration is a fact about the machine, not about the tree.
type gateTiming struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Passed  bool    `json:"passed"`
}

// runRecord is the environment half of the certification evidence (M16.20):
// what ran it, on what, from which commit, and how long each gate took.
type runRecord struct {
	SchemaVersion int               `json:"schemaVersion"`
	Certified     bool              `json:"certified"`
	Commit        string            `json:"commit"`
	TreeDirty     bool              `json:"treeDirty"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	Tools         map[string]string `json:"tools"`
	Gates         []gateTiming      `json:"gates"`
	TotalSeconds  float64           `json:"totalSeconds"`
	Skips         []skipRecord      `json:"skips"`
	LoadMetrics   string            `json:"loadMetricsFile,omitempty"`
}

func writeRunRecord(dir, root string, rep report, timings []gateTiming, loadMetrics string) error {
	rec := runRecord{
		SchemaVersion: 1,
		Certified:     rep.Certified,
		Commit:        gitOutput(root, "rev-parse", "HEAD"),
		TreeDirty:     gitOutput(root, "status", "--short") != "",
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Tools: map[string]string{
			"go":         runtime.Version(),
			"node":       toolVersion(root, "node", "--version"),
			"npm":        toolVersion(root, "npm", "--version"),
			"playwright": toolVersion(filepath.Join(root, "engine", "web"), "npx", "playwright", "--version"),
		},
		Gates: timings,
		Skips: rep.Skips,
	}
	for _, t := range timings {
		rec.TotalSeconds += t.Seconds
	}
	if strings.TrimSpace(loadMetrics) != "" {
		name := "load-metrics.txt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(loadMetrics), 0o644); err != nil {
			return err
		}
		rec.LoadMetrics = name
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "run.json"), append(data, '\n'), 0o644)
}

func gitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func toolVersion(dir, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(out))
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
