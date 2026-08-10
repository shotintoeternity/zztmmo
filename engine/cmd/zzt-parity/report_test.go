package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// synthetic manifest exercising every status the report distinguishes.
func sampleManifest() *manifest {
	return &manifest{
		SchemaVersion: 1,
		Rows: []manifestRow{
			{ID: "element.E_LION", Dimension: "element", Contract: "V", Status: "pass", Test: "TestLionParity"},
			{ID: "element.E_BEAR", Dimension: "element", Contract: "V", Status: "unverified", AssignedTask: "M16.5"},
			{ID: "protocol.Diff", Dimension: "protocol", Contract: "P", Status: "pass", Test: "TestDiffProjection", Fixture: "fixtures/town.replay.json"},
			{ID: "task.M1.1", Dimension: "task", Contract: "E", Status: "deviation"},
			{ID: "mode.mobile-touchplay", Dimension: "browser-mode", Contract: "E", Status: "gap", AssignedTask: "M16.18a"},
			{ID: "service.x", Dimension: "service", Contract: "out-of-scope", Status: "out-of-scope"},
		},
	}
}

// sampleDeviceMatrix is a minimal well-formed M16.18 matrix: one covered
// profile with evidence, one skipped profile that says why.
func sampleDeviceMatrix() *deviceMatrix {
	return &deviceMatrix{
		Note:     "sample",
		Surfaces: []string{"chat", "entry"},
		Checks:   []string{"layout", "composition"},
		Profiles: []deviceProfile{
			{
				ID: "chromium-desktop", Engine: "chromium", Status: "covered",
				Evidence: "TestM1618PlatformMatrix/chromium-desktop",
				Surfaces: []string{"chat", "entry"},
			},
			{
				ID: "firefox-touch-portrait", Engine: "firefox", Touch: true, Status: "skipped",
				Reason: "Playwright cannot emulate touch in Firefox",
			},
		},
	}
}

func passingGates() []gateResult {
	gates := plannedGates(true, true)
	for i := range gates {
		gates[i].Passed = true
	}
	return gates
}

func TestBuildReportTallies(t *testing.T) {
	r := buildReport(sampleManifest(), "fixtures/parity/manifest.json", passingGates(), sampleDeviceMatrix(), nil)

	if r.TotalRows != 6 {
		t.Fatalf("TotalRows = %d, want 6", r.TotalRows)
	}
	want := map[string]int{"pass": 2, "unverified": 1, "deviation": 1, "gap": 1, "out-of-scope": 1}
	for k, v := range want {
		if r.StatusTotals[k] != v {
			t.Errorf("StatusTotals[%q] = %d, want %d", k, r.StatusTotals[k], v)
		}
	}

	// VerifiedRows must be exactly the pass rows, sorted by id, each with its test.
	if len(r.VerifiedRows) != 2 {
		t.Fatalf("VerifiedRows = %d, want 2", len(r.VerifiedRows))
	}
	if r.VerifiedRows[0].ID != "element.E_LION" || r.VerifiedRows[1].ID != "protocol.Diff" {
		t.Errorf("VerifiedRows not sorted by id: %+v", r.VerifiedRows)
	}
	if r.VerifiedRows[0].Test != "TestLionParity" {
		t.Errorf("verified row test = %q, want TestLionParity", r.VerifiedRows[0].Test)
	}
}

func TestCertificationBlockedByOpenRows(t *testing.T) {
	r := buildReport(sampleManifest(), "m", passingGates(), sampleDeviceMatrix(), nil)
	if r.Certified {
		t.Fatal("manifest with unverified+gap rows must not be certified")
	}
	joined := strings.Join(r.Blockers, "\n")
	if !strings.Contains(joined, "unverified") || !strings.Contains(joined, "gap") {
		t.Errorf("blockers missing unverified/gap: %v", r.Blockers)
	}
}

func TestCertificationRequiresPassRowsToNameTest(t *testing.T) {
	m := &manifest{Rows: []manifestRow{
		{ID: "a", Dimension: "element", Status: "pass", Test: ""},
	}}
	r := buildReport(m, "m", passingGates(), sampleDeviceMatrix(), nil)
	if r.Certified {
		t.Fatal("a pass row with no covering test must block certification")
	}
	if len(r.Blockers) != 1 || !strings.Contains(r.Blockers[0], "name no covering test") {
		t.Errorf("expected a no-covering-test blocker, got %v", r.Blockers)
	}
}

func TestCertificationHappyPath(t *testing.T) {
	m := &manifest{Rows: []manifestRow{
		{ID: "a", Dimension: "element", Status: "pass", Test: "TestA"},
		{ID: "b", Dimension: "task", Status: "deviation"},
		{ID: "c", Dimension: "service", Status: "out-of-scope"},
	}}
	r := buildReport(m, "m", passingGates(), sampleDeviceMatrix(), nil)
	if !r.Certified {
		t.Fatalf("all-terminal manifest with passing gates must certify, blockers: %v", r.Blockers)
	}
}

func TestFailedGateBlocksCertification(t *testing.T) {
	m := &manifest{Rows: []manifestRow{{ID: "a", Dimension: "element", Status: "pass", Test: "TestA"}}}
	gates := passingGates()
	gates[goTestGateIndex(t, gates)].Passed = false
	r := buildReport(m, "m", gates, sampleDeviceMatrix(), nil)
	if r.Certified {
		t.Fatal("a failed clean gate must block certification")
	}
	if !strings.Contains(strings.Join(r.Blockers, "\n"), "go test") {
		t.Errorf("expected a go test gate blocker, got %v", r.Blockers)
	}
}

func TestSkippedGateBlocksCertification(t *testing.T) {
	m := &manifest{Rows: []manifestRow{{ID: "a", Dimension: "element", Status: "pass", Test: "TestA"}}}
	gates := passingGates()
	gates[0].Skipped = true
	r := buildReport(m, "m", gates, sampleDeviceMatrix(), nil)
	if r.Certified {
		t.Fatal("a skipped clean gate must block certification (it was not actually run)")
	}
}

// The report must be byte-for-byte reproducible: no timestamps, no map-order or
// slice-order leaks. Rendering the same inputs twice must match exactly.
func TestReportDeterminism(t *testing.T) {
	m := sampleManifest()
	var jsonA, jsonB, mdA, mdB bytes.Buffer

	rA := buildReport(m, "m", passingGates(), sampleDeviceMatrix(), nil)
	if err := writeJSON(&jsonA, rA); err != nil {
		t.Fatal(err)
	}
	if err := writeMarkdown(&mdA, rA); err != nil {
		t.Fatal(err)
	}

	// Rebuild from a fresh manifest value to catch any accidental input mutation.
	rB := buildReport(sampleManifest(), "m", passingGates(), sampleDeviceMatrix(), nil)
	if err := writeJSON(&jsonB, rB); err != nil {
		t.Fatal(err)
	}
	if err := writeMarkdown(&mdB, rB); err != nil {
		t.Fatal(err)
	}

	if jsonA.String() != jsonB.String() {
		t.Error("JSON report is not deterministic across runs")
	}
	if mdA.String() != mdB.String() {
		t.Error("Markdown report is not deterministic across runs")
	}
	// The Markdown must not advertise a coverage percentage as parity.
	if strings.Contains(mdA.String(), "% coverage") || strings.Contains(strings.ToLower(mdA.String()), "line coverage:") {
		t.Error("report must not present line coverage as a parity claim")
	}
}

// ---------------------------------------------------------------------------
// Device/browser matrix (task M16.18)
// ---------------------------------------------------------------------------

func certifiableManifest() *manifest {
	return &manifest{Rows: []manifestRow{
		{ID: "a", Dimension: "element", Status: "pass", Test: "TestA"},
	}}
}

// The DoD's central rule: a skip with no reason is a certification blocker, and
// the same matrix with a reason is not.
func TestUnexplainedDeviceSkipBlocksCertification(t *testing.T) {
	devices := sampleDeviceMatrix()
	devices.Profiles[1].Reason = ""
	r := buildReport(certifiableManifest(), "m", passingGates(), devices, nil)
	if r.Certified {
		t.Fatal("a device profile skipped with no reason must block certification")
	}
	if !strings.Contains(strings.Join(r.Blockers, "\n"), "skipped with no reason") {
		t.Errorf("expected an unexplained-skip blocker, got %v", r.Blockers)
	}

	if r2 := buildReport(certifiableManifest(), "m", passingGates(), sampleDeviceMatrix(), nil); !r2.Certified {
		t.Errorf("an explained skip must not block certification, blockers: %v", r2.Blockers)
	}
}

func TestCoveredDeviceProfileMustNameEvidence(t *testing.T) {
	devices := sampleDeviceMatrix()
	devices.Profiles[0].Evidence = ""
	r := buildReport(certifiableManifest(), "m", passingGates(), devices, nil)
	if r.Certified {
		t.Fatal("a covered device profile with no evidence must block certification")
	}
	if !strings.Contains(strings.Join(r.Blockers, "\n"), "names no evidence") {
		t.Errorf("expected a no-evidence blocker, got %v", r.Blockers)
	}
}

func TestMissingDeviceMatrixBlocksCertification(t *testing.T) {
	r := buildReport(certifiableManifest(), "m", passingGates(), nil, nil)
	if r.Certified {
		t.Fatal("a report with no device/browser matrix must not certify (task M16.18)")
	}
	if !strings.Contains(strings.Join(r.Blockers, "\n"), "no device/browser matrix") {
		t.Errorf("expected a missing-matrix blocker, got %v", r.Blockers)
	}
}

// The matrix has to be IN the report, not merely consulted by it: the DoD asks
// for a device/browser matrix a reader can see.
func TestMarkdownRendersTheDeviceMatrix(t *testing.T) {
	var md bytes.Buffer
	r := buildReport(sampleManifest(), "m", passingGates(), sampleDeviceMatrix(), nil)
	if err := writeMarkdown(&md, r); err != nil {
		t.Fatal(err)
	}
	out := md.String()
	for _, want := range []string{
		"## Device and browser matrix",
		"chromium-desktop",
		"firefox-touch-portrait",
		"Playwright cannot emulate touch in Firefox",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report's device matrix section is missing %q", want)
		}
	}
}

// The real committed matrix must satisfy the same rules the synthetic ones do —
// otherwise the gate only ever ran against test data.
func TestCommittedDeviceMatrixHasNoUnexplainedSkip(t *testing.T) {
	devices, err := loadDeviceMatrix(filepath.Join("..", "..", "..", "fixtures", "parity", "device-matrix.json"))
	if err != nil {
		t.Fatalf("the committed device/browser matrix must load: %v", err)
	}
	if blockers := deviceMatrixBlockers(devices); len(blockers) > 0 {
		t.Errorf("the committed device/browser matrix is not certifiable: %v", blockers)
	}
}

func TestSplitCommand(t *testing.T) {
	got := splitCommand("go test -count=1 ./...")
	want := []string{"go", "test", "-count=1", "./..."}
	if len(got) != len(want) {
		t.Fatalf("splitCommand len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("splitCommand[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// goTestGateIndex finds the `go test` gate rather than assuming its position:
// M16.20 reordered the gate list so the browser track installs first, and a
// test that hard-codes an index silently starts asserting about another gate.
func goTestGateIndex(t *testing.T, gates []gateResult) int {
	t.Helper()
	for i, g := range gates {
		if g.Name == "go test" {
			return i
		}
	}
	t.Fatal("no `go test` gate in the planned list")
	return 0
}

// ---------------------------------------------------------------------------
// M16.20 — silent skips
// ---------------------------------------------------------------------------

// TestUndeclaredSkipBlocksCertification is the mechanised half of M16.20's "no
// silent skips": a real-browser suite that skipped itself because the harness
// was absent must fail the certification, not ride along inside an "ok".
func TestUndeclaredSkipBlocksCertification(t *testing.T) {
	skips := []skipRecord{{
		Package: "github.com/shotintoeternity/zztmmo/engine",
		Test:    "TestM1614CollaborativeEditorInBrowsers",
		Reason:  "browser harness unavailable: run `npm ci` in engine/web",
	}}
	r := buildReport(certifiableManifest(), "m", passingGates(), sampleDeviceMatrix(), skips)
	if r.Certified {
		t.Fatal("a test that skipped itself must block certification")
	}
	if !strings.Contains(strings.Join(r.Blockers, "\n"), "TestM1614CollaborativeEditorInBrowsers") {
		t.Errorf("blockers do not name the skipped test: %v", r.Blockers)
	}
}

// A skip that declares itself is evidence, not a hole: M16.18's Firefox touch
// profile skips with the reason the device matrix carries, and the report says
// so rather than pretending the run was complete.
func TestDeclaredSkipDoesNotBlockButIsReported(t *testing.T) {
	skips := []skipRecord{{
		Package: "github.com/shotintoeternity/zztmmo/engine",
		Test:    "TestM1618PlatformMatrix/firefox-touch-portrait",
		Reason:  "declared skip: Playwright cannot emulate touch in Firefox",
	}}
	r := buildReport(certifiableManifest(), "m", passingGates(), sampleDeviceMatrix(), skips)
	if !r.Certified {
		t.Fatalf("a declared skip must not block certification, blockers: %v", r.Blockers)
	}
	if len(r.Skips) != 1 {
		t.Fatalf("the report must still carry the skip: %+v", r.Skips)
	}
	var buf bytes.Buffer
	if err := writeMarkdown(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "firefox-touch-portrait") {
		t.Error("the rendered report does not name the skipped test")
	}
}

// Skips are sorted, so two runs of the same tree render the same report.
func TestSkipsAreSortedForDeterminism(t *testing.T) {
	skips := []skipRecord{
		{Package: "b", Test: "TestZ", Reason: "declared skip: z"},
		{Package: "a", Test: "TestB", Reason: "declared skip: b"},
		{Package: "a", Test: "TestA", Reason: "declared skip: a"},
	}
	r := buildReport(certifiableManifest(), "m", passingGates(), sampleDeviceMatrix(), skips)
	got := []string{r.Skips[0].Test, r.Skips[1].Test, r.Skips[2].Test}
	want := []string{"TestA", "TestB", "TestZ"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("skips = %v, want %v", got, want)
		}
	}
}

// The browser track must be in the gate list and must run BEFORE the go gates:
// the real-browser suites live inside `go test ./...` and skip themselves when
// the harness is absent, so installing it afterwards certifies nothing (M16.20).
func TestBrowserTrackRunsBeforeTheGoGates(t *testing.T) {
	gates := plannedGates(true, true)
	pos := map[string]int{}
	for i, g := range gates {
		pos[g.Name] = i
	}
	for _, name := range []string{"npm ci", "playwright install", "npm run build", "go test", "go test -race"} {
		if _, ok := pos[name]; !ok {
			t.Fatalf("gate %q is missing from the certification list: %+v", name, gates)
		}
	}
	if pos["playwright install"] > pos["go test"] || pos["npm run build"] > pos["go test"] {
		t.Errorf("the browser harness is installed after the suites that need it: %v", pos)
	}
	if pos["npm ci"] > pos["playwright install"] {
		t.Errorf("playwright is installed before its node_modules: %v", pos)
	}
}

// lastMeaningfulLine strips the file:line prefix `t.Skip` prints, so the reason
// in the report reads as the reason and the declared-skip marker is found.
func TestSkipReasonIsExtractedFromTestOutput(t *testing.T) {
	got := lastMeaningfulLine([]string{
		"=== RUN   TestM1618PlatformMatrix/firefox-touch-portrait",
		"    m16_18_test.go:308: declared skip: Playwright cannot emulate touch in Firefox",
		"",
	})
	want := "declared skip: Playwright cannot emulate touch in Firefox"
	if got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
	if !(skipRecord{Reason: got}).declared() {
		t.Error("a declared skip's reason must be recognised as declared")
	}
}

// The go gates are driven under `go test -json`, and `-json` is a flag of the
// `test` subcommand: `go -json test …` is not a command, which is how the first
// M16.20 certification run failed (NOTES.md 2026-08-01). Assert the assembled
// argument list rather than trusting the splice.
func TestGoTestJSONArgsPutTheFlagAfterTheSubcommand(t *testing.T) {
	for _, g := range plannedGates(true, true) {
		if !g.goTest {
			continue
		}
		args := splitCommand(g.Command)
		got := goTestJSONArgs(args)
		if len(got) < 3 || got[0] != "go" || got[1] != "test" || got[2] != "-json" {
			t.Fatalf("gate %q assembled %v, want `go test -json …`", g.Name, got)
		}
		if strings.Join(got[3:], " ") != strings.Join(args[2:], " ") {
			t.Errorf("gate %q lost or reordered its own flags: %v", g.Name, got)
		}
	}
}

// Every go gate carries an explicit -timeout, and it beats `go test`'s default
// ten minutes by a margin (task M33.3). The default is the whole bug: the
// engine package runs the real-browser family inside the `go test` gate, which
// M33.1 measured at 592s of that 600s budget, so the certification run was one
// added suite away from reporting a wall clock as a gate failure.
func TestGoGatesCarryATimeoutBeyondTheDefault(t *testing.T) {
	const goDefaultTimeout = 10 * time.Minute
	seen := 0
	for _, g := range plannedGates(true, true) {
		if !g.goTest {
			continue
		}
		seen++
		args := splitCommand(g.Command)
		idx := -1
		for i, a := range args {
			if a == "-timeout" {
				idx = i
			}
		}
		if idx < 0 || idx+1 >= len(args) {
			t.Fatalf("gate %q has no -timeout: %q", g.Name, g.Command)
		}
		d, err := time.ParseDuration(args[idx+1])
		if err != nil {
			t.Fatalf("gate %q has an unparseable -timeout %q: %v", g.Name, args[idx+1], err)
		}
		if d <= goDefaultTimeout {
			t.Errorf("gate %q asks for %s, which is no better than go test's default %s", g.Name, d, goDefaultTimeout)
		}
	}
	if seen != 2 {
		t.Fatalf("expected both go gates to be checked, saw %d", seen)
	}
}

// The panic `go test` prints when a package outlives its -timeout is recognised
// as a timeout, and ordinary output — including a test named "timed out" and a
// panic of any other kind — is not (task M33.3).
func TestTimeoutPanicIsRecognised(t *testing.T) {
	yes := []string{
		"panic: test timed out after 10m0s\n",
		"panic: test timed out after 30m0s\n",
		"\tpanic: test timed out after 1h0m0s\n",
	}
	for _, line := range yes {
		if !isTimeoutPanic(line) {
			t.Errorf("isTimeoutPanic(%q) = false, want true", line)
		}
	}
	no := []string{
		"panic: runtime error: index out of range [3]\n",
		"    m33_3_test.go:12: timed out waiting for the block confirmation\n",
		"--- FAIL: TestSomethingTimedOut (0.03s)\n",
		"",
	}
	for _, line := range no {
		if isTimeoutPanic(line) {
			t.Errorf("isTimeoutPanic(%q) = true, want false", line)
		}
	}
}

// A gate that ran out of wall clock blocks certification, but says so as a
// clock rather than as a verdict on the tree: the report must not read as a red
// suite when nothing was proved either way (task M33.3).
func TestTimedOutGateIsDistinguishableFromAFailure(t *testing.T) {
	timedOut := passingGates()
	failed := passingGates()
	var idx int
	for i, g := range timedOut {
		if g.Name == "go test" {
			idx = i
		}
	}
	timedOut[idx].Passed, timedOut[idx].TimedOut = false, true
	failed[idx].Passed = false

	timeoutRep := buildReport(sampleManifest(), "m.json", timedOut, sampleDeviceMatrix(), nil)
	failRep := buildReport(sampleManifest(), "m.json", failed, sampleDeviceMatrix(), nil)

	joined := strings.Join(timeoutRep.Blockers, "\n")
	if !strings.Contains(joined, "ran out of wall clock") || !strings.Contains(joined, "no verdict") {
		t.Fatalf("a timed-out gate must be blocked as a clock, got: %v", timeoutRep.Blockers)
	}
	if strings.Contains(joined, `clean gate "go test" failed`) {
		t.Errorf("a timed-out gate must not also be reported as a failure: %v", timeoutRep.Blockers)
	}
	if failJoined := strings.Join(failRep.Blockers, "\n"); !strings.Contains(failJoined, `clean gate "go test" failed`) || strings.Contains(failJoined, "wall clock") {
		t.Errorf("a genuinely failed gate must still read as a failure: %v", failRep.Blockers)
	}

	var md bytes.Buffer
	if err := writeMarkdown(&md, timeoutRep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "**TIMEOUT**") {
		t.Error("the markdown gate table must mark the timeout as such")
	}
}

// The timeout is recorded with the run the way the tool versions are: a reader
// comparing two runs' gate timings needs the clock they were measured against
// (task M33.3).
func TestRunRecordCarriesTheGoTestTimeout(t *testing.T) {
	dir := t.TempDir()
	rep := buildReport(sampleManifest(), "m.json", passingGates(), sampleDeviceMatrix(), nil)
	if err := writeRunRecord(dir, dir, rep, []gateTiming{{Name: "go test", Seconds: 1, Passed: true}}, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec runRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.GoTestTimeout != goTestTimeout {
		t.Fatalf("run record says goTestTimeout=%q, gates were run with %q", rec.GoTestTimeout, goTestTimeout)
	}
}
