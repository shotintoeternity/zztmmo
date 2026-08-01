package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
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
	gates := plannedGates(true)
	for i := range gates {
		gates[i].Passed = true
	}
	return gates
}

func TestBuildReportTallies(t *testing.T) {
	r := buildReport(sampleManifest(), "fixtures/parity/manifest.json", passingGates(), sampleDeviceMatrix())

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
	r := buildReport(sampleManifest(), "m", passingGates(), sampleDeviceMatrix())
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
	r := buildReport(m, "m", passingGates(), sampleDeviceMatrix())
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
	r := buildReport(m, "m", passingGates(), sampleDeviceMatrix())
	if !r.Certified {
		t.Fatalf("all-terminal manifest with passing gates must certify, blockers: %v", r.Blockers)
	}
}

func TestFailedGateBlocksCertification(t *testing.T) {
	m := &manifest{Rows: []manifestRow{{ID: "a", Dimension: "element", Status: "pass", Test: "TestA"}}}
	gates := passingGates()
	gates[2].Passed = false // go test
	r := buildReport(m, "m", gates, sampleDeviceMatrix())
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
	r := buildReport(m, "m", gates, sampleDeviceMatrix())
	if r.Certified {
		t.Fatal("a skipped clean gate must block certification (it was not actually run)")
	}
}

// The report must be byte-for-byte reproducible: no timestamps, no map-order or
// slice-order leaks. Rendering the same inputs twice must match exactly.
func TestReportDeterminism(t *testing.T) {
	m := sampleManifest()
	var jsonA, jsonB, mdA, mdB bytes.Buffer

	rA := buildReport(m, "m", passingGates(), sampleDeviceMatrix())
	if err := writeJSON(&jsonA, rA); err != nil {
		t.Fatal(err)
	}
	if err := writeMarkdown(&mdA, rA); err != nil {
		t.Fatal(err)
	}

	// Rebuild from a fresh manifest value to catch any accidental input mutation.
	rB := buildReport(sampleManifest(), "m", passingGates(), sampleDeviceMatrix())
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
	r := buildReport(certifiableManifest(), "m", passingGates(), devices)
	if r.Certified {
		t.Fatal("a device profile skipped with no reason must block certification")
	}
	if !strings.Contains(strings.Join(r.Blockers, "\n"), "skipped with no reason") {
		t.Errorf("expected an unexplained-skip blocker, got %v", r.Blockers)
	}

	if r2 := buildReport(certifiableManifest(), "m", passingGates(), sampleDeviceMatrix()); !r2.Certified {
		t.Errorf("an explained skip must not block certification, blockers: %v", r2.Blockers)
	}
}

func TestCoveredDeviceProfileMustNameEvidence(t *testing.T) {
	devices := sampleDeviceMatrix()
	devices.Profiles[0].Evidence = ""
	r := buildReport(certifiableManifest(), "m", passingGates(), devices)
	if r.Certified {
		t.Fatal("a covered device profile with no evidence must block certification")
	}
	if !strings.Contains(strings.Join(r.Blockers, "\n"), "names no evidence") {
		t.Errorf("expected a no-evidence blocker, got %v", r.Blockers)
	}
}

func TestMissingDeviceMatrixBlocksCertification(t *testing.T) {
	r := buildReport(certifiableManifest(), "m", passingGates(), nil)
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
	r := buildReport(sampleManifest(), "m", passingGates(), sampleDeviceMatrix())
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
