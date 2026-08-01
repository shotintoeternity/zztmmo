package zztgo

// Task M16.20a — PARITY_SCAFFOLD=1 regeneration must be non-destructive.
//
// The scaffold used to rebuild fixtures/parity/manifest.json from the deriver
// and keep only a subset of the manifest's own content: rows it could not
// re-derive were dropped (M16.7a's three service.prompt-* rows vanished on every
// run), and derived values overwrote landed edits on the rows that survived
// (M16.6b's elem.player note and proto.event.walkClick authority). M16.20
// reconciles against this file, so the documented workflow must not be able to
// corrupt it.
//
// These tests pin the merge contract itself; TestParityManifestIsCanonical
// (parity_manifest_test.go) pins the result for the real manifest.

import (
	"encoding/json"
	"strings"
	"testing"
)

// m1620aDerived is a miniature derived inventory: one row of each shape the
// merge treats differently, with the deriver's defaults on every field.
func m1620aDerived() []parityRow {
	return []parityRow{
		{
			ID: "elem.demo", Dimension: "element", Subject: "E_DEMO (demo): custom tick",
			Contract: "V", Authority: "derived authority", Parity: "exact",
			Status: "unverified", AssignedTask: "M16.3", Notes: "derived note",
		},
		{
			ID: "route.demo", Dimension: "route", Subject: "HTTP/WS route /demo",
			Contract: "E", Authority: "web_api.go / websocket_server.go", Parity: "exact",
			Status: "unverified", AssignedTask: "M16.16",
		},
	}
}

// m1620aHandEdited is that inventory as a later sweep would leave it on disk:
// the element row verified and re-annotated by hand, plus a curated row the
// deriver knows nothing about.
func m1620aHandEdited(t *testing.T) []byte {
	t.Helper()
	rows := []parityRow{
		{
			ID: "elem.demo", Dimension: "element", Subject: "E_DEMO (demo): custom tick",
			Contract: "E", Authority: "hand-edited authority; ELEMENTS.PAS:1393-1402",
			Parity: "deviation", Deviation: "mp-respawn",
			Test: "TestDemo", Fixture: "fixtures/demo.zwd", Status: "pass",
			Notes: "hand-edited note",
		},
		{
			ID: "route.demo", Dimension: "route", Subject: "HTTP/WS route /demo",
			Contract: "E", Authority: "web_api.go / websocket_server.go", Parity: "exact",
			Status: "unverified", AssignedTask: "M16.16",
		},
		{
			ID: "service.hand-added", Dimension: "service", Subject: "a row no deriver produces",
			Contract: "E", Authority: "completed task contracts (TASKS.md); NOTES.md",
			Parity: "exact", Test: "TestDemo", Status: "pass",
			Notes: "added by a later task, exactly as M16.7a added service.prompt-*",
		},
	}
	out, err := json.Marshal(parityManifest{SchemaVersion: 1, Rows: rows, Deviations: seededDeviations()})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func m1620aRowsByID(t *testing.T, m parityManifest) map[string]parityRow {
	t.Helper()
	byID := map[string]parityRow{}
	for _, r := range m.Rows {
		byID[r.ID] = r
	}
	return byID
}

// A regeneration keeps every hand-edited field and every hand-added row, and
// takes only the deriver-owned subject.
func TestM1620aScaffoldPreservesHandEdits(t *testing.T) {
	merged, report, err := mergeParityManifest(m1620aHandEdited(t), m1620aDerived(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := m1620aRowsByID(t, merged)

	// The row the deriver cannot produce survives — the M16.7a failure.
	hand, ok := byID["service.hand-added"]
	if !ok {
		t.Fatal("hand-added row was dropped by regeneration")
	}
	if hand.Notes == "" || hand.Test != "TestDemo" || hand.Status != "pass" {
		t.Errorf("hand-added row was not carried forward verbatim: %+v", hand)
	}
	if len(report.preserved) != 1 || report.preserved[0] != "service.hand-added" {
		t.Errorf("preserved rows misreported: %v", report.preserved)
	}

	// Every curator-owned field on a derived row keeps its on-disk value — the
	// M16.6b failure (notes and authority) plus the fields around them.
	got := byID["elem.demo"]
	for _, c := range []struct{ field, want, have string }{
		{"notes", "hand-edited note", got.Notes},
		{"authority", "hand-edited authority; ELEMENTS.PAS:1393-1402", got.Authority},
		{"contract", "E", got.Contract},
		{"parity", "deviation", got.Parity},
		{"deviation", "mp-respawn", got.Deviation},
		{"test", "TestDemo", got.Test},
		{"fixture", "fixtures/demo.zwd", got.Fixture},
		{"status", "pass", got.Status},
	} {
		if c.have != c.want {
			t.Errorf("elem.demo.%s: regeneration wrote %q, want the on-disk %q", c.field, c.have, c.want)
		}
	}
	// An emptied field is a decision too: a sweep that cleared assignedTask when
	// the row passed must not have the deriver's default put back.
	if got.AssignedTask != "" {
		t.Errorf("elem.demo.assignedTask: regeneration restored %q over a deliberately cleared field", got.AssignedTask)
	}
	// The deriver still owns the mechanical description.
	if got.Subject != "E_DEMO (demo): custom tick" {
		t.Errorf("elem.demo.subject: %q, want the derived subject", got.Subject)
	}
	// ...and every override it lost is reported rather than silent.
	if len(report.overrode) == 0 {
		t.Error("merge report lists no overridden fields; the disk-wins decisions were silent")
	}
}

// A regeneration with nothing changed rewrites the same bytes.
func TestM1620aScaffoldIsIdempotent(t *testing.T) {
	first, _, err := mergeParityManifest(m1620aHandEdited(t), m1620aDerived(), nil)
	if err != nil {
		t.Fatal(err)
	}
	firstOut, err := marshalParityManifest(first)
	if err != nil {
		t.Fatal(err)
	}
	second, report, err := mergeParityManifest(firstOut, m1620aDerived(), nil)
	if err != nil {
		t.Fatal(err)
	}
	secondOut, err := marshalParityManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstOut) != string(secondOut) {
		t.Errorf("second regeneration differs from the first:\n%s\n---\n%s", firstOut, secondOut)
	}
	if len(report.added) != 0 || len(report.dropped) != 0 {
		t.Errorf("idempotent run reported churn: added %v, dropped %v", report.added, report.dropped)
	}
}

// Deleting a row takes the explicit opt-in, and the opt-in refuses the two ways
// of misusing it.
func TestM1620aScaffoldDropRequiresOptIn(t *testing.T) {
	prev := m1620aHandEdited(t)

	merged, report, err := mergeParityManifest(prev, m1620aDerived(), []string{"service.hand-added"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m1620aRowsByID(t, merged)["service.hand-added"]; ok {
		t.Error("row named in the drop list survived")
	}
	if len(report.dropped) != 1 || report.dropped[0] != "service.hand-added" {
		t.Errorf("dropped rows misreported: %v", report.dropped)
	}

	// A row whose code surface really is gone (an element deleted, a task box
	// unticked) is still kept — TestParityManifest then rejects it as stale,
	// which is the loud version of the failure this task fixes. The scaffold
	// says so rather than leaving the red gate to explain itself.
	_, report, err = mergeParityManifest(prev, m1620aDerived()[:1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.stale) != 1 || report.stale[0] != "route.demo" {
		t.Errorf("a preserved row in a mechanical dimension was not flagged stale: %v", report.stale)
	}

	// Dropping a row the code still derives would be undone on the next run.
	if _, _, err := mergeParityManifest(prev, m1620aDerived(), []string{"elem.demo"}); err == nil {
		t.Error("dropping a still-derived row was accepted")
	} else if !strings.Contains(err.Error(), "elem.demo") {
		t.Errorf("unhelpful error for a still-derived drop: %v", err)
	}
	// A typo must not silently delete nothing.
	if _, _, err := mergeParityManifest(prev, m1620aDerived(), []string{"service.hand-addded"}); err == nil {
		t.Error("dropping an id that is not in the manifest was accepted")
	}
}

// The specific rows and edits M18.1 found destroyed (NOTES.md 2026-07-30) come
// through a regeneration of the real manifest intact.
func TestM1620aScaffoldKeepsTheRowsItDestroyed(t *testing.T) {
	onDisk := []byte(mustRead(t, parityManifestPath))
	merged, _, err := mergeParityManifest(onDisk, buildParityRows(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := m1620aRowsByID(t, merged)

	// M16.7a's oracle-verified quit/debug/help prompt rows.
	for _, id := range []string{"service.prompt-debug", "service.prompt-help", "service.prompt-quit"} {
		r, ok := byID[id]
		if !ok {
			t.Errorf("regeneration dropped %q (M16.7a)", id)
			continue
		}
		if r.Status != "pass" || r.Test == "" {
			t.Errorf("%s came back unverified: status=%q test=%q", id, r.Status, r.Test)
		}
	}
	// M16.6b's two edits on rows the deriver does re-derive.
	if n := byID["elem.player"].Notes; !strings.Contains(n, "walk click") {
		t.Errorf("elem.player lost M16.6b's note: %q", n)
	}
	if a := byID["proto.event.walkClick"].Authority; !strings.Contains(a, "ELEMENTS.PAS:1393-1402") {
		t.Errorf("proto.event.walkClick lost M16.6b's authority: %q", a)
	}
}

// The fixture stores ZZT source quotations verbatim. encoding/json escapes `<`,
// `>` and `&` by default, so a note quoting `P1 < Random(10)` came back as a
// backslash-u escape and turned every regeneration into a diff of its own.
func TestM1620aScaffoldDoesNotEscapeHTML(t *testing.T) {
	out, err := marshalParityManifest(parityManifest{
		SchemaVersion: 1,
		Rows: []parityRow{{
			ID: "elem.demo", Dimension: "element", Subject: "E_DEMO",
			Contract: "V", Authority: "a", Parity: "exact", Status: "unverified",
			AssignedTask: "M16.3", Notes: "`P1 < Random(10)` & <WORLD>.HI",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `\u`) {
		t.Errorf("manifest bytes are HTML-escaped:\n%s", out)
	}
	if !strings.Contains(string(out), "`P1 < Random(10)` & <WORLD>.HI") {
		t.Errorf("note was not written verbatim:\n%s", out)
	}
}
