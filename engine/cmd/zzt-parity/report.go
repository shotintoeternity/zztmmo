package main

// The parity report model (task M16.1).
//
// This file holds the *pure* half of the parity gate command: it reads the M16
// traceability manifest (fixtures/parity/manifest.json, defined by PARITY.md and
// built by M16.0) plus the results of the clean gates, and renders a
// deterministic JSON + Markdown report keyed by that manifest. No timestamps, no
// durations, no wall-clock or map-iteration ordering leaks into the output, so a
// clean clone produces byte-identical reports run to run and the artifact is
// diffable. main.go supplies the gate results by actually running the gates; the
// report logic here is exercised directly by report_test.go without them.
//
// Certification (PARITY.md §2, §3): the product is certified only when every
// manifest row is `pass` or an owner-approved `deviation` (or a non-claim
// `out-of-scope`) — no `unverified`, `gap`, or `unknown` — AND every clean gate
// passes. A `pass` row must name the exact test that certifies it; a `pass` with
// an empty `test` is itself a blocker. Aggregate line coverage is never consulted:
// parity is proven row by row, not by a coverage percentage.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Manifest schema (mirror of the fields the report consumes; PARITY.md §3)
// ---------------------------------------------------------------------------

type manifestRow struct {
	ID           string `json:"id"`
	Dimension    string `json:"dimension"`
	Subject      string `json:"subject"`
	Contract     string `json:"contract"`
	Test         string `json:"test,omitempty"`
	Fixture      string `json:"fixture,omitempty"`
	Status       string `json:"status"`
	AssignedTask string `json:"assignedTask,omitempty"`
}

type manifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	Rows          []manifestRow `json:"rows"`
}

// ---------------------------------------------------------------------------
// Device/browser matrix (task M16.18)
// ---------------------------------------------------------------------------

// The mirror of fixtures/parity/device-matrix.json the report consumes. The
// file is authored by hand and enforced against real runs by
// TestM1618PlatformMatrix; here it is rendered, and one rule is applied — a
// profile the matrix does not cover must say why. An unexplained skip is a
// certification blocker, which is what makes "the matrix contains no unexplained
// skip" a property of the artifact rather than a promise about it.
type deviceProfile struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Engine      string `json:"engine"`
	Orientation string `json:"orientation"`
	Touch       bool   `json:"touch"`
	Viewport    struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"viewport"`
	DeviceScaleFactor float64 `json:"deviceScaleFactor"`
	// TouchPlay: this profile plays the game through the on-screen controls with
	// no keyboard (task M16.18a). It is the evidence behind the manifest row
	// mode.mobile-touchplay, so the table names it rather than leaving a reader
	// to infer it from "touch: yes", which only means the screen has a finger.
	TouchPlay bool     `json:"touchplay,omitempty"`
	Status    string   `json:"status"`
	Reason    string   `json:"reason,omitempty"`
	Evidence  string   `json:"evidence,omitempty"`
	Surfaces  []string `json:"surfaces,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}

type deviceMatrix struct {
	Note     string          `json:"note"`
	Surfaces []string        `json:"surfaces"`
	Checks   []string        `json:"checks"`
	Profiles []deviceProfile `json:"profiles"`
}

// skipRecord is one test the certification run skipped (task M16.20). A skip
// is invisible in `go test`'s default output — a suite that skips itself
// because a browser is not installed reports the same "ok" as one that ran —
// which is exactly the "silent skip" M16.20's DoD forbids. The go test gates
// therefore run under `-json`, every skip is recorded here by name, and a skip
// that does not declare itself blocks certification.
//
// A declared skip says so in its own message: it begins with `declared skip:`
// followed by the reason (M16.18's firefox-touch-portrait profile, whose
// reason the device matrix already carries). Anything else is undeclared.
type skipRecord struct {
	Package string `json:"package"`
	Test    string `json:"test"`
	Reason  string `json:"reason"`
}

const declaredSkipMarker = "declared skip:"

func (s skipRecord) declared() bool {
	return strings.Contains(s.Reason, declaredSkipMarker)
}

// gateResult is one clean gate (go build/vet/test/-race, npm ci/test/build).
// Output is deliberately excluded from the report model: it is nondeterministic.
// main.go streams gate output to the console; only the pass/fail verdict — which
// is a deterministic function of the tree — is recorded here.
type gateResult struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Dir     string `json:"dir"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`

	// goTest marks a gate the runner drives under `-json` so it can record the
	// tests that skipped (task M16.20). Not serialized: it is how the gate is
	// run, not a fact about the tree, and the report must stay a pure function
	// of the tree.
	goTest bool `json:"-"`
}

// ---------------------------------------------------------------------------
// Report model
// ---------------------------------------------------------------------------

type dimensionSummary struct {
	Dimension  string `json:"dimension"`
	Total      int    `json:"total"`
	Pass       int    `json:"pass"`
	Unverified int    `json:"unverified"`
	Deviation  int    `json:"deviation"`
	Gap        int    `json:"gap"`
	OutOfScope int    `json:"outOfScope"`
}

// verifiedRow is a `pass` row and the exact test certifying it — the DoD's
// "reports the exact test covering each already-verified row".
type verifiedRow struct {
	ID        string `json:"id"`
	Dimension string `json:"dimension"`
	Contract  string `json:"contract"`
	Test      string `json:"test"`
	Fixture   string `json:"fixture,omitempty"`
}

type report struct {
	SchemaVersion int    `json:"schemaVersion"`
	ManifestPath  string `json:"manifestPath"`
	TotalRows     int    `json:"totalRows"`

	// StatusTotals is the whole-manifest tally, keyed by status. Go marshals
	// map keys in sorted order, so this stays deterministic.
	StatusTotals map[string]int     `json:"statusTotals"`
	ByDimension  []dimensionSummary `json:"byDimension"`

	// VerifiedRows lists every `pass` row with its covering test, sorted by id.
	VerifiedRows []verifiedRow `json:"verifiedRows"`

	Gates []gateResult `json:"gates"`

	// DeviceMatrix is M16.18's device/browser matrix, rendered as part of the
	// report so the platforms a claim covers travel with the claim.
	DeviceMatrix *deviceMatrix `json:"deviceMatrix,omitempty"`

	// Skips is every test the go test gates skipped, sorted (task M16.20).
	// Deterministic: the same tree skips the same tests, and an undeclared skip
	// is a blocker rather than a line nobody reads.
	Skips []skipRecord `json:"skips"`

	Certified bool     `json:"certified"`
	Blockers  []string `json:"blockers"`
}

// ---------------------------------------------------------------------------
// Building the report
// ---------------------------------------------------------------------------

const reportSchemaVersion = 1

// buildReport composes the deterministic report from a manifest and the gate
// results. It never mutates its inputs and never reads wall-clock or the
// filesystem, so it is a pure function of (m, gates).
func buildReport(m *manifest, manifestPath string, gates []gateResult, devices *deviceMatrix, skips []skipRecord) report {
	sorted := append([]skipRecord(nil), skips...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Package != sorted[j].Package {
			return sorted[i].Package < sorted[j].Package
		}
		return sorted[i].Test < sorted[j].Test
	})
	r := report{
		SchemaVersion: reportSchemaVersion,
		ManifestPath:  manifestPath,
		TotalRows:     len(m.Rows),
		StatusTotals:  map[string]int{},
		Gates:         gates,
		DeviceMatrix:  devices,
		Skips:         sorted,
	}

	dims := map[string]*dimensionSummary{}
	for _, row := range m.Rows {
		r.StatusTotals[row.Status]++

		d := dims[row.Dimension]
		if d == nil {
			d = &dimensionSummary{Dimension: row.Dimension}
			dims[row.Dimension] = d
		}
		d.Total++
		switch row.Status {
		case "pass":
			d.Pass++
			r.VerifiedRows = append(r.VerifiedRows, verifiedRow{
				ID:        row.ID,
				Dimension: row.Dimension,
				Contract:  row.Contract,
				Test:      row.Test,
				Fixture:   row.Fixture,
			})
		case "unverified":
			d.Unverified++
		case "deviation":
			d.Deviation++
		case "gap":
			d.Gap++
		case "out-of-scope":
			d.OutOfScope++
		}
	}

	for _, d := range dims {
		r.ByDimension = append(r.ByDimension, *d)
	}
	sort.Slice(r.ByDimension, func(i, j int) bool {
		return r.ByDimension[i].Dimension < r.ByDimension[j].Dimension
	})
	sort.Slice(r.VerifiedRows, func(i, j int) bool {
		return r.VerifiedRows[i].ID < r.VerifiedRows[j].ID
	})

	r.Blockers = certificationBlockers(m, gates, devices, sorted)
	r.Certified = len(r.Blockers) == 0
	return r
}

// certificationBlockers lists every reason the product is not yet certified,
// sorted for determinism. An empty slice means certified. The rules are
// PARITY.md §2/§3: no non-terminal row status, every `pass` names a real test,
// and every clean gate passes.
func certificationBlockers(m *manifest, gates []gateResult, devices *deviceMatrix, skips []skipRecord) []string {
	var blockers []string
	blockers = append(blockers, deviceMatrixBlockers(devices)...)
	for _, s := range skips {
		if s.declared() {
			continue
		}
		reason := strings.TrimSpace(s.Reason)
		if reason == "" {
			reason = "no reason given"
		}
		blockers = append(blockers, fmt.Sprintf("test %s (%s) skipped without declaring itself: %s", s.Test, s.Package, reason))
	}

	var unverified, gap, unknown, passNoTest int
	for _, row := range m.Rows {
		switch row.Status {
		case "pass":
			if strings.TrimSpace(row.Test) == "" {
				passNoTest++
			}
		case "deviation", "out-of-scope":
			// terminal, non-blocking
		case "unverified":
			unverified++
		case "gap":
			gap++
		default:
			unknown++
		}
	}
	if unverified > 0 {
		blockers = append(blockers, fmt.Sprintf("%d row(s) still `unverified` (must be pass/deviation before certification)", unverified))
	}
	if gap > 0 {
		blockers = append(blockers, fmt.Sprintf("%d row(s) are `gap` (a filed gap task must land first)", gap))
	}
	if unknown > 0 {
		blockers = append(blockers, fmt.Sprintf("%d row(s) carry an unknown/unrecognized status", unknown))
	}
	if passNoTest > 0 {
		blockers = append(blockers, fmt.Sprintf("%d `pass` row(s) name no covering test", passNoTest))
	}
	for _, g := range gates {
		if g.Skipped {
			blockers = append(blockers, fmt.Sprintf("clean gate %q was skipped, not run", g.Name))
			continue
		}
		if !g.Passed {
			blockers = append(blockers, fmt.Sprintf("clean gate %q failed", g.Name))
		}
	}

	sort.Strings(blockers)
	return blockers
}

// deviceMatrixBlockers is M16.18's DoD, mechanised: the report must carry a
// device/browser matrix, and that matrix must contain no unexplained skip. A
// covered profile that names no evidence is the same failure wearing the other
// hat — a claim with nothing behind it.
func deviceMatrixBlockers(devices *deviceMatrix) []string {
	if devices == nil {
		return []string{"no device/browser matrix was recorded (fixtures/parity/device-matrix.json, task M16.18)"}
	}
	var blockers []string
	if len(devices.Profiles) == 0 {
		return []string{"the device/browser matrix declares no profiles (task M16.18)"}
	}
	for _, p := range devices.Profiles {
		switch p.Status {
		case "covered":
			if strings.TrimSpace(p.Evidence) == "" {
				blockers = append(blockers, fmt.Sprintf("device profile %q is covered but names no evidence", p.ID))
			}
			if len(p.Surfaces) == 0 {
				blockers = append(blockers, fmt.Sprintf("device profile %q is covered but exercises no text surface", p.ID))
			}
		case "skipped":
			if strings.TrimSpace(p.Reason) == "" {
				blockers = append(blockers, fmt.Sprintf("device profile %q is skipped with no reason", p.ID))
			}
		default:
			blockers = append(blockers, fmt.Sprintf("device profile %q carries an unknown status %q", p.ID, p.Status))
		}
	}
	return blockers
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// writeJSON emits the report as indented JSON with a trailing newline.
func writeJSON(w io.Writer, r report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// writeMarkdown renders the human-facing report. It is deterministic: every
// list is pre-sorted by buildReport and no timing or environment data appears.
func writeMarkdown(w io.Writer, r report) error {
	p := func(format string, args ...interface{}) {
		fmt.Fprintf(w, format, args...)
	}

	p("# Parity certification report (M16.1)\n\n")
	p("Keyed by `%s`. Parity is proven row by row against the M16 manifest; ", r.ManifestPath)
	p("aggregate line coverage is deliberately not reported and is not a parity claim.\n\n")

	verdict := "**NOT CERTIFIED**"
	if r.Certified {
		verdict = "**CERTIFIED**"
	}
	p("Status: %s (%d manifest rows)\n\n", verdict, r.TotalRows)

	// Clean gates.
	p("## Clean gates\n\n")
	p("| gate | command | dir | result |\n|---|---|---|---|\n")
	for _, g := range r.Gates {
		result := "PASS"
		switch {
		case g.Skipped:
			result = "**SKIPPED**"
		case !g.Passed:
			result = "**FAIL**"
		}
		p("| %s | `%s` | `%s` | %s |\n", g.Name, g.Command, g.Dir, result)
	}
	p("\n")

	// Manifest status by dimension.
	p("## Manifest status by dimension\n\n")
	p("| dimension | total | pass | unverified | deviation | gap | out-of-scope |\n")
	p("|---|---|---|---|---|---|---|\n")
	for _, d := range r.ByDimension {
		p("| %s | %d | %d | %d | %d | %d | %d |\n",
			d.Dimension, d.Total, d.Pass, d.Unverified, d.Deviation, d.Gap, d.OutOfScope)
	}
	// Totals row.
	statuses := []string{"pass", "unverified", "deviation", "gap", "out-of-scope"}
	p("| **all** | **%d** | **%d** | **%d** | **%d** | **%d** | **%d** |\n",
		r.TotalRows, r.StatusTotals["pass"], r.StatusTotals["unverified"],
		r.StatusTotals["deviation"], r.StatusTotals["gap"], r.StatusTotals["out-of-scope"])
	_ = statuses
	p("\n")

	// Device/browser matrix (M16.18).
	p("## Device and browser matrix\n\n")
	if r.DeviceMatrix == nil {
		p("None recorded — `fixtures/parity/device-matrix.json` is missing (task M16.18).\n\n")
	} else {
		p("%s\n\n", r.DeviceMatrix.Note)
		p("Text surfaces: %s. Checks: %s.\n\n",
			strings.Join(r.DeviceMatrix.Surfaces, ", "), strings.Join(r.DeviceMatrix.Checks, ", "))
		p("| profile | engine | screen | touch | status | surfaces | evidence / reason |\n")
		p("|---|---|---|---|---|---|---|\n")
		for _, d := range r.DeviceMatrix.Profiles {
			touch := "—"
			if d.Touch {
				touch = "yes"
			}
			if d.TouchPlay {
				touch = "yes, gameplay"
			}
			status := d.Status
			detail := d.Evidence
			if d.Status == "skipped" {
				status = "**skipped**"
				detail = d.Reason
			}
			surfaces := "—"
			if len(d.Surfaces) > 0 {
				surfaces = fmt.Sprintf("%d/%d", len(d.Surfaces), len(r.DeviceMatrix.Surfaces))
			}
			p("| `%s` | %s | %dx%d @%gx | %s | %s | %s | %s |\n",
				d.ID, d.Engine, d.Viewport.Width, d.Viewport.Height, d.DeviceScaleFactor,
				touch, status, surfaces, detail)
		}
		p("\n")
		for _, d := range r.DeviceMatrix.Profiles {
			if d.Notes != "" {
				p("- `%s`: %s\n", d.ID, d.Notes)
			}
		}
		p("\n")
	}

	// Skipped tests (M16.20). A run with nothing here ran everything it built.
	p("## Skipped tests\n\n")
	if len(r.Skips) == 0 {
		p("None — no test in the go gates skipped itself.\n\n")
	} else {
		p("| test | package | declared | reason |\n|---|---|---|---|\n")
		for _, s := range r.Skips {
			declared := "**no**"
			if s.declared() {
				declared = "yes"
			}
			p("| `%s` | `%s` | %s | %s |\n", s.Test, s.Package, declared, s.Reason)
		}
		p("\n")
	}

	// Verified rows and their covering tests.
	p("## Verified rows and covering tests\n\n")
	if len(r.VerifiedRows) == 0 {
		p("No rows are `pass` yet — every behavioral row is `unverified` and assigned to a later M16 sweep (M16.2+).\n\n")
	} else {
		p("| row | dimension | contract | covering test | fixture |\n|---|---|---|---|---|\n")
		for _, v := range r.VerifiedRows {
			fixture := v.Fixture
			if fixture == "" {
				fixture = "—"
			}
			p("| `%s` | %s | %s | `%s` | %s |\n", v.ID, v.Dimension, v.Contract, v.Test, fixture)
		}
		p("\n")
	}

	// Blockers.
	p("## Certification blockers\n\n")
	if len(r.Blockers) == 0 {
		p("None — every row is `pass`/`deviation`/`out-of-scope` and every clean gate passed.\n")
	} else {
		for _, b := range r.Blockers {
			p("- %s\n", b)
		}
	}
	p("\n")

	return nil
}
