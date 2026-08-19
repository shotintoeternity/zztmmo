package zztgo

// M16.18 — the mobile and browser-platform contract.
//
// WHAT THIS TASK CERTIFIES. Every other browser sweep drives one Chromium at one
// desktop size with no touch. This one drives the same built client across the
// declared device/browser matrix — three engines, portrait and landscape phone
// screens, deviceScaleFactor 3 — and asks the platform's questions rather than
// the game's: does the 80x25 screen still fit and stay undistorted, does the
// phone keyboard come up, does a composed character arrive exactly once, does a
// deletion delete one, and does none of it leak into gameplay. Every text
// surface modalAcceptsTextInput names (modal.ts) is exercised on every profile
// that declares it.
//
// THE MATRIX IS A COMMITTED DECLARATION, NOT A RUN LOG. fixtures/parity/
// device-matrix.json names each profile, its screen, the surfaces it covers, and
// — for anything it does not cover — the reason. The parity report renders that
// file (cmd/zzt-parity/report.go) and refuses to certify while a skip carries no
// reason, which is the DoD's "no unexplained skip". This test is the other half
// of the pincer: it runs what the file declares and fails if the run covered
// less (or more) than the claim. Neither half can drift without the other going
// red.
//
// THE M15 SCOPE DECISION (M16.0) WAS RESOLVED BY BUILDING THE CONTROLS, NOT BY
// THIS TASK. The owner chose on 2026-07-15 to build touch gameplay controls
// rather than narrow the claim (gap task M16.18a), and on 2026-07-30 to defer
// that work past the beta with the product copy narrowed to desktop browsers.
// M16.18 therefore certified mobile TEXT ENTRY (manifest row
// mode.mobile-textentry) and the layout it happens in, and left
// mode.mobile-touchplay at `gap`.
//
// M16.18a landed on 2026-08-01 and this file grew the other half. A profile that
// declares `touchplay` now plays the game with no keyboard at all, and
// m1618CheckObservation holds that run to the same claim-versus-evidence rule
// the text surfaces get. TestM1618ProductCopyMakesNoTouchGameplayClaim needs no
// change to follow: it required the desktop-only disclaimer only while the row
// was `gap`, so the requirement lifted itself when the row did — which is the
// property it was written for.
//
// M21.5 (2026-08-04) added the acts a MULTIPLAYER phone needs: the same profiles
// now open and close the Players window and answer a yes/no prompt with taps
// alone. They are declared here rather than in the surface list because neither
// is a text surface — the point is which controls exist, not what can be typed
// into them.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const m1618MatrixPath = "../fixtures/parity/device-matrix.json"

// ---------------------------------------------------------------------------
// The declaration
// ---------------------------------------------------------------------------

type m1618Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type m1618Profile struct {
	ID                string        `json:"id"`
	Label             string        `json:"label"`
	Engine            string        `json:"engine"`
	Viewport          m1618Viewport `json:"viewport"`
	DeviceScaleFactor float64       `json:"deviceScaleFactor"`
	Touch             bool          `json:"touch"`
	Orientation       string        `json:"orientation"`

	// TouchDetection is how the engine reveals itself as a touch device:
	// "maxTouchPoints" (the client's own gate, and real iOS Safari) or "gesture"
	// (an engine that reports zero touch points but delivers touch events, which
	// is Playwright's WebKit and every hybrid device M15.1's `touchSeen`
	// fallback exists for). Empty means "maxTouchPoints".
	TouchDetection string `json:"touchDetection,omitempty"`

	// Status is "covered" (the suite runs here) or "skipped" (it does not, and
	// Reason says why). A skipped profile with no reason is exactly what the
	// report's blocker rule exists to catch.
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Evidence string `json:"evidence,omitempty"`

	// Surfaces this profile exercises, and — for any text surface it leaves out
	// — why. Together they must account for every surface in the inventory.
	Surfaces        []string          `json:"surfaces,omitempty"`
	SurfacesOmitted map[string]string `json:"surfacesOmitted,omitempty"`

	// TouchPlay declares that this profile certifies touch GAMEPLAY (M16.18a) —
	// move, shoot, torch, pause through the on-screen bar with no keyboard —
	// and not only text entry. It can only be true where a bar is built at all,
	// which is the maxTouchPoints gate; the browser script runs
	// certifyTouchGameplay exactly when it is set, and records what it did.
	TouchPlay bool `json:"touchplay,omitempty"`

	// The text rows the on-screen control bar covers, at this profile's shape and
	// rotated 90 degrees. Declared rather than required-to-be-zero because on a
	// landscape phone it genuinely covers the bottom of the board: gap task
	// M16.18b. When that is fixed these go to zero and the browser script's
	// assertion is what notices.
	TouchBarCoveredRows        []int `json:"touchBarCoveredRows"`
	RotatedTouchBarCoveredRows []int `json:"rotatedTouchBarCoveredRows"`

	// Notes carries what a reader of the matrix needs and the fields cannot say:
	// why a profile exists, and what its measurements mean.
	Notes string `json:"notes,omitempty"`
}

type m1618Matrix struct {
	Note     string         `json:"note"`
	Surfaces []string       `json:"surfaces"`
	Checks   []string       `json:"checks"`
	Profiles []m1618Profile `json:"profiles"`
}

func m1618LoadMatrix(t *testing.T) m1618Matrix {
	t.Helper()
	data, err := os.ReadFile(m1618MatrixPath)
	if err != nil {
		t.Fatalf("read the device/browser matrix: %v", err)
	}
	var matrix m1618Matrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("parse %s: %v", m1618MatrixPath, err)
	}
	return matrix
}

// TestM1618DeviceMatrixIsWellFormed is the declaration's own gate: it holds
// whether or not a browser is installed, so a checkout with no Playwright still
// proves the matrix explains itself. The rules are the ones the parity report
// refuses to certify without (report.go deviceMatrixBlockers), asserted here
// too so a bad edit is named by the test that owns the file.
func TestM1618DeviceMatrixIsWellFormed(t *testing.T) {
	matrix := m1618LoadMatrix(t)

	if len(matrix.Profiles) == 0 {
		t.Fatal("the device/browser matrix declares no profiles")
	}
	if len(matrix.Surfaces) == 0 {
		t.Fatal("the device/browser matrix declares no text-surface inventory")
	}

	// The inventory must be the client's own list of editable modals. A surface
	// added to modalAcceptsTextInput and not to the matrix is a surface no
	// device was ever tested against.
	wantSurfaces := m1618ClientTextSurfaces(t)
	got := append([]string(nil), matrix.Surfaces...)
	sort.Strings(got)
	sort.Strings(wantSurfaces)
	if strings.Join(got, ",") != strings.Join(wantSurfaces, ",") {
		t.Errorf("the matrix inventories %v; modal.ts modalAcceptsTextInput names %v", got, wantSurfaces)
	}

	seen := map[string]bool{}
	engines := map[string]bool{}
	touchPlayProfiles := 0
	for _, p := range matrix.Profiles {
		where := "profile " + p.ID
		if p.ID == "" {
			t.Fatalf("a profile has no id")
		}
		if seen[p.ID] {
			t.Errorf("duplicate profile id %q", p.ID)
		}
		seen[p.ID] = true
		engines[p.Engine] = true

		switch p.Engine {
		case "chromium", "firefox", "webkit":
		default:
			t.Errorf("%s: unknown browser engine %q", where, p.Engine)
		}
		if p.Viewport.Width <= 0 || p.Viewport.Height <= 0 {
			t.Errorf("%s: no viewport", where)
		}
		if p.DeviceScaleFactor <= 0 {
			t.Errorf("%s: no deviceScaleFactor", where)
		}

		switch p.Status {
		case "skipped":
			if strings.TrimSpace(p.Reason) == "" {
				t.Errorf("%s: a skipped profile must say why (the report treats an unexplained skip as a blocker)", where)
			}
		case "covered":
			if p.Evidence == "" {
				t.Errorf("%s: a covered profile must name the test that covers it", where)
			}
			if len(p.Surfaces) == 0 {
				t.Errorf("%s: a covered profile must exercise at least one text surface", where)
			}
			// Every inventoried surface is either exercised or explained.
			for _, surface := range matrix.Surfaces {
				if m1618Contains(p.Surfaces, surface) {
					continue
				}
				if strings.TrimSpace(p.SurfacesOmitted[surface]) == "" {
					t.Errorf("%s: text surface %q is neither exercised nor explained", where, surface)
				}
			}
			for _, surface := range p.Surfaces {
				if !m1618Contains(matrix.Surfaces, surface) {
					t.Errorf("%s: exercises %q, which is not in the surface inventory", where, surface)
				}
			}
			for surface := range p.SurfacesOmitted {
				if m1618Contains(p.Surfaces, surface) {
					t.Errorf("%s: %q is both exercised and explained away", where, surface)
				}
			}
		default:
			t.Errorf("%s: status %q is neither \"covered\" nor \"skipped\"", where, p.Status)
		}

		if !p.Touch && (len(p.TouchBarCoveredRows) > 0 || len(p.RotatedTouchBarCoveredRows) > 0) {
			t.Errorf("%s: a pointer-only profile has no on-screen control bar to cover rows with", where)
		}

		// Touch gameplay can only be claimed where the controls exist. The bar
		// is decided once, from navigator.maxTouchPoints; an engine that reports
		// zero touch points and merely delivers touch events (Playwright's
		// WebKit, and the hybrid devices M15.1's `touchSeen` fallback exists
		// for) gets no bar, so it has nothing to play with.
		if p.TouchPlay {
			if !p.Touch {
				t.Errorf("%s: claims touch gameplay on a pointer-only profile", where)
			}
			if p.TouchDetection != "" && p.TouchDetection != "maxTouchPoints" {
				t.Errorf("%s: claims touch gameplay with detection %q, which builds no control bar", where, p.TouchDetection)
			}
			if p.Status != "covered" {
				t.Errorf("%s: claims touch gameplay but is %q, so nothing ran to prove it", where, p.Status)
			}
			touchPlayProfiles++
		}
	}

	// M16.18a's row (mode.mobile-touchplay) is `pass` only because some profile
	// here plays the game with no keyboard. A matrix that stopped declaring one
	// would leave that claim with nothing behind it, so the absence is named
	// here rather than discovered at M16.20.
	if touchPlayProfiles == 0 {
		t.Error("no profile declares `touchplay`: the mode.mobile-touchplay claim would have no covering run (task M16.18a)")
	}

	// The contract says "supported desktop engines", plural: a matrix that only
	// ever ran Chromium would be a matrix in name only.
	for _, engine := range []string{"chromium", "firefox", "webkit"} {
		if !engines[engine] {
			t.Errorf("the matrix declares no %s profile at all", engine)
		}
	}
}

// m1618ClientTextSurfaces reads the client's own definition of "a modal that
// takes text" so the inventory cannot fall behind it.
func m1618ClientTextSurfaces(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("web", "src", "modal.ts"))
	if err != nil {
		t.Fatalf("read web/src/modal.ts: %v", err)
	}
	const marker = "export function modalAcceptsTextInput"
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatal("web/src/modal.ts no longer defines modalAcceptsTextInput")
	}
	body := string(src)[i:]
	if end := strings.Index(body, "\n}"); end > 0 {
		body = body[:end]
	}
	var kinds []string
	for _, part := range strings.Split(body, `m.kind === "`)[1:] {
		if q := strings.Index(part, `"`); q > 0 {
			kinds = append(kinds, part[:q])
		}
	}
	if len(kinds) == 0 {
		t.Fatal("could not read the editable-modal kinds out of modal.ts")
	}
	return kinds
}

func m1618Contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The matrix run
// ---------------------------------------------------------------------------

// TestM1618PlatformMatrix runs the platform suite once per declared profile.
// Each subtest is a real browser of that engine at that screen, driving the
// production server objects through the same tick-locked harness M16.9 built.
func TestM1618PlatformMatrix(t *testing.T) {
	matrix := m1618LoadMatrix(t)
	for _, profile := range matrix.Profiles {
		profile := profile
		if profile.Status != "covered" {
			t.Run(profile.ID, func(t *testing.T) {
				t.Skipf("declared skip: %s", profile.Reason)
			})
			continue
		}
		t.Run(profile.ID, func(t *testing.T) {
			m1618RequireEngine(t, profile.Engine)
			h := m1610NewHarness(t)
			spec, err := json.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			out := h.runBrowserScript("platform_matrix.test.mjs", "PROFILE_JSON="+string(spec))
			t.Logf("%s:\n%s", profile.Label, out)
			m1618CheckObservation(t, matrix, profile)
		})
	}
}

// m1618CheckObservation reads what the browser script recorded and holds it
// against the declaration. The script asserts its own invariants as it goes;
// this is the claim-versus-evidence check — a profile that ran but covered a
// different set of surfaces, or recorded a check with no finding, fails here.
func m1618CheckObservation(t *testing.T, matrix m1618Matrix, profile m1618Profile) {
	t.Helper()
	reportPath := filepath.Join("web", "test-results", "platform-matrix-"+profile.ID+".json")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("the browser script wrote no observation at %s: %v", reportPath, err)
	}
	var observed struct {
		Profile string `json:"profile"`
		Engine  string `json:"engine"`
		Layouts []struct {
			Label     string  `json:"label"`
			PxPerCell float64 `json:"pxPerCell"`
			Canvas    struct {
				Width         float64 `json:"width"`
				Height        float64 `json:"height"`
				BackingWidth  int     `json:"backingWidth"`
				BackingHeight int     `json:"backingHeight"`
			} `json:"canvas"`
			DevicePixelRatio float64 `json:"devicePixelRatio"`
		} `json:"layouts"`
		Surfaces []struct {
			ID     string            `json:"id"`
			Kind   string            `json:"kind"`
			Checks map[string]string `json:"checks"`
		} `json:"surfaces"`
		TouchPlay   map[string]string `json:"touchplay"`
		Screenshots []string          `json:"screenshots"`
	}
	if err := json.Unmarshal(data, &observed); err != nil {
		t.Fatalf("parse %s: %v", reportPath, err)
	}

	if observed.Profile != profile.ID || observed.Engine != profile.Engine {
		t.Fatalf("the observation is for %s/%s, this profile is %s/%s",
			observed.Profile, observed.Engine, profile.ID, profile.Engine)
	}

	var covered []string
	for _, s := range observed.Surfaces {
		covered = append(covered, s.ID)
		for _, check := range matrix.Checks {
			if check == "layout" || check == "resize-dpr" {
				continue // whole-screen checks, recorded in Layouts
			}
			if strings.TrimSpace(s.Checks[check]) == "" {
				t.Errorf("%s/%s: check %q recorded nothing", profile.ID, s.ID, check)
			}
		}
	}
	sort.Strings(covered)
	want := append([]string(nil), profile.Surfaces...)
	sort.Strings(want)
	if strings.Join(covered, ",") != strings.Join(want, ",") {
		t.Errorf("%s covered %v, declared %v", profile.ID, covered, want)
	}

	// The layout half: the declared screens were measured, the backing store
	// never moved, and the rotation actually happened.
	labels := map[string]bool{}
	for _, l := range observed.Layouts {
		labels[l.Label] = true
		if l.Canvas.BackingWidth != 640 || l.Canvas.BackingHeight != 350 {
			t.Errorf("%s @ %s: backing store is %dx%d, want 640x350",
				profile.ID, l.Label, l.Canvas.BackingWidth, l.Canvas.BackingHeight)
		}
		if l.PxPerCell < 4 {
			t.Errorf("%s @ %s: %.2f CSS px per column", profile.ID, l.Label, l.PxPerCell)
		}
	}
	for _, required := range []string{"launch-prompt", "title", "rotated", "restored", "playing", "modal-help"} {
		if !labels[required] {
			t.Errorf("%s: no layout was measured at %q", profile.ID, required)
		}
	}
	if len(observed.Screenshots) == 0 {
		t.Errorf("%s: the run produced no screenshots — the DoD asks for the layout to be shown, not only asserted", profile.ID)
	}

	// Touch gameplay (M16.18a): every act the DoD names must have happened, and
	// a profile that does not claim it must not have quietly done it either —
	// the matrix is a declaration, and that rule runs in both directions here as
	// it does for the text surfaces above.
	if profile.TouchPlay {
		// windows and prompt are M21.5's: the Players window opened and closed,
		// and a yes/no prompt answered, entirely by tap.
		for _, act := range []string{"move", "shoot", "torch", "pause", "isolation", "windows", "prompt"} {
			if strings.TrimSpace(observed.TouchPlay[act]) == "" {
				t.Errorf("%s: touch gameplay act %q recorded nothing", profile.ID, act)
			}
		}
	} else if len(observed.TouchPlay) > 0 {
		t.Errorf("%s: recorded touch gameplay it does not declare: %v", profile.ID, observed.TouchPlay)
	}
}

// m1618RequireEngine skips (loudly, and only locally) when a browser engine is
// not installed. CI installs all three (.github/workflows/ci.yml), so a skip
// there would be a red flag rather than an environment fact.
func m1618RequireEngine(t *testing.T, engine string) {
	t.Helper()
	m169RequireBrowserHarness(t)
	m169RequireClientBuild(t)
	cmd := exec.Command("node", "-e",
		fmt.Sprintf("const p=require('playwright').%s.executablePath();process.stdout.write(p)", engine))
	cmd.Dir = "web"
	out, err := cmd.Output()
	if err != nil {
		m169BrowserAbsent(t, fmt.Sprintf("playwright cannot resolve %s: %v", engine, err))
	}
	if _, err := os.Stat(strings.TrimSpace(string(out))); err != nil {
		m169BrowserAbsent(t, fmt.Sprintf("%s is not installed: run `npx playwright install %s` in engine/web", engine, engine))
	}
}

// ---------------------------------------------------------------------------
// The claim
// ---------------------------------------------------------------------------

// TestM1618ProductCopyMakesNoTouchGameplayClaim is the DoD's "touch gameplay is
// neither implied nor marked `pass` without a tested input path", enforced
// against the two places a claim can live: the manifest row and the copy a
// player reads.
//
// While mode.mobile-touchplay is `gap`, the README must say the product is for
// desktop browsers and must not offer phones as supported. When M16.18a lands
// and the row stops being `gap`, this test stops requiring the disclaimer — so
// it pins the claim to the evidence in both directions rather than freezing one
// sentence forever.
func TestM1618ProductCopyMakesNoTouchGameplayClaim(t *testing.T) {
	manifest := loadParityManifest(t)
	var touchplay, textentry parityRow
	for _, row := range manifest.Rows {
		switch row.ID {
		case "mode.mobile-touchplay":
			touchplay = row
		case "mode.mobile-textentry":
			textentry = row
		}
	}
	if touchplay.ID == "" || textentry.ID == "" {
		t.Fatal("the manifest has lost its mobile rows")
	}

	if touchplay.Status == "pass" && touchplay.Test == "" {
		t.Error("mode.mobile-touchplay is `pass` with no test naming the input path that proves it")
	}
	if textentry.Status == "pass" && textentry.Test == "" {
		t.Error("mode.mobile-textentry is `pass` with no covering test")
	}

	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	copyText := string(readme)

	if touchplay.Status == "gap" {
		if !strings.Contains(copyText, "Desktop browsers with a keyboard") {
			t.Error("README.md must scope the product to desktop browsers while touch gameplay is a gap")
		}
		if !strings.Contains(copyText, "Phones and tablets are not supported yet") {
			t.Error("README.md must say phones and tablets are not supported yet while touch gameplay is a gap")
		}
		// The inverse claim, in any of the forms someone might reach for.
		for _, claim := range []string{
			"playable on phones",
			"play on your phone",
			"mobile-friendly",
			"works on mobile",
			"touch controls",
		} {
			if strings.Contains(strings.ToLower(copyText), claim) {
				t.Errorf("README.md claims %q while touch gameplay is an open gap (M16.18a)", claim)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// M16.18c — the harness's own page error
// ---------------------------------------------------------------------------

// TestM1618cPauseClockAccountsForItsOwnPageError covers the browser harness
// rather than the product, the way M16.14d's test does — and closes the hole
// M16.14d left.
//
// M16.14d made web/test/lib/canvas.mjs's pauseClock retry the "Cannot
// fast-forward to the past" that a slow round trip provokes, and the retry
// cannot lose. What it did not do is stop the FAILED first attempt reaching
// page.on("pageerror"), the channel every browser suite ends by asserting is
// empty — so a loaded machine still reddened runs that had recovered (twice
// during M16.18a, on firefox-desktop, at 132s and 187s against a stable 60s).
//
// The mechanism is Firefox-specific and now named. Playwright evaluates the
// pause inside the page; Chromium and WebKit await the returned promise through
// the protocol, which attaches a handler to it, while Firefox's juggler watches
// it from outside through the Debugger API (Runtime.js, _awaitPromise /
// onPromiseSettled). Nothing in the page ever handles the rejection, so
// SpiderMonkey reports it to the console service as an unhandled rejection and
// juggler forwards that as Page.uncaughtError (PageAgent.js, _onRuntimeError).
// Every rejected evaluate is therefore reported twice on Firefox: once to the
// caller and once to the page.
//
// The script forces the losing attempt instead of waiting for load to supply
// one, and holds the fix to all four halves of the claim: the run is green, the
// error was actually provoked (a green that provoked nothing would prove
// nothing — M16.18a found exactly that kind of measurement), an unaccounted-for
// rewind still lands, and a real page fault raised while the accounting is
// outstanding still lands.
//
// It needs Firefox and Chromium but no server and no client build.
func TestM1618cPauseClockAccountsForItsOwnPageError(t *testing.T) {
	m169RequireBrowserHarness(t)

	cmd := exec.Command("node", filepath.Join("test", "pause_clock_errors.test.mjs"))
	cmd.Dir = "web"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pause_clock_errors.test.mjs failed: %v\n--- script output ---\n%s", err, out)
	}
	t.Logf("pauseClock page errors:\n%s", out)
}
