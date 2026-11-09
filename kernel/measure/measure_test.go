package measure

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// reportPath returns where the JSON goes: $CICADA_ENGINE_MEASUREMENTS, or
// build/engine-measurements.json at the repository root.
func reportPath(t *testing.T) string {
	if p := os.Getenv("CICADA_ENGINE_MEASUREMENTS"); p != "" {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "build", "engine-measurements.json")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func buildReport(t *testing.T) *report {
	t.Helper()
	start := time.Now()
	r := newReport()
	for _, c := range voiceCases() {
		e, err := measureVoice(c)
		if err != nil {
			t.Fatalf("voice %s: %v", c.Name, err)
		}
		r.Entries = append(r.Entries, e)
	}
	for _, c := range fxCases() {
		e, err := measureFX(c)
		if err != nil {
			t.Fatalf("effect %s: %v", c.Name, err)
		}
		r.Entries = append(r.Entries, e)
	}
	sort.Slice(r.Entries, func(i, j int) bool { return r.Entries[i].Name < r.Entries[j].Name })
	r.ElapsedSec = round(time.Since(start).Seconds())
	return r
}

// TestEngineMeasurements renders every voice family and effect, writes the
// JSON report, and fails when a gated metric is worse than its limit.
func TestEngineMeasurements(t *testing.T) {
	r := buildReport(t)

	if os.Getenv("CICADA_MEASURE_UPDATE") == "1" {
		if err := writeJSON(tolerancePath(), buildTolerances(r)); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s from the current measurements; review the diff before committing", tolerancePath())
	}
	tol, err := loadTolerances()
	if err != nil {
		t.Fatal(err)
	}
	failures := gate(r, tol)

	path := reportPath(t)
	if err := writeJSON(path, r); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d entries, %.1f s)", path, len(r.Entries), r.ElapsedSec)
	for _, e := range r.Entries {
		var parts []string
		for _, name := range metricOrder {
			m := e.Metrics[name]
			if m.Status == statusMeasured {
				parts = append(parts, name+"="+formatValue(*m.Value))
			}
		}
		t.Logf("%-28s %s", e.Name, strings.Join(parts, " "))
	}
	for _, f := range failures {
		t.Error(f)
	}
}

func formatValue(v float64) string {
	return strings.TrimRight(strings.TrimRight(strings.Replace(strings.TrimSpace(fmt.Sprintf("%.2f", v)), "+", "", 1), "0"), ".")
}

// TestEveryFamilyHasAnEntry fails when a voice package or effect has no
// measurement entry, so a new family cannot slip past the suite.
func TestEveryFamilyHasAnEntry(t *testing.T) {
	have := map[string]bool{}
	for _, c := range voiceCases() {
		have["voice/"+c.Name] = true
	}
	for _, c := range fxCases() {
		have["fx/"+c.Name] = true
	}
	// Voice packages that render audio. keyboard holds shared constants only.
	skipped := map[string]string{"keyboard": "shared parameter tables and helpers; it renders nothing"}
	entries, err := os.ReadDir("../voice")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range entries {
		if !d.IsDir() || skipped[d.Name()] != "" {
			continue
		}
		if !have["voice/"+d.Name()] {
			t.Errorf("voice package %s has no measurement entry", d.Name())
		}
	}
	for _, name := range []string{
		"comp", "deesser", "delay", "drive_soft", "reverb", "ampcab", "chorus", "convolution", "tape",
		"pro_eq", "pro_limiter", "pro_transient", "pro_width", "pro_reverb", "pro_normalize",
	} {
		if !have["fx/"+name] {
			t.Errorf("effect %s has no measurement entry", name)
		}
	}
	// A new directory under kernel/fx must be added to the list above.
	known := map[string]bool{"ampcab": true, "chorus": true, "convolution": true, "pro": true, "tape": true}
	fxDirs, err := os.ReadDir("../fx")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range fxDirs {
		if d.IsDir() && !known[d.Name()] {
			t.Errorf("kernel/fx/%s is new: add a measurement entry and list it here", d.Name())
		}
	}
}

// naiveSaw is a non-band-limited sawtooth: the textbook source of aliasing.
func naiveSawCase() voiceCase {
	return voiceCase{
		Name:        "graph_saw", // judged against the graph polyBLEP saw's limit
		Description: "negative control: naive sawtooth 2*phase-1 with no band limiting",
		New: func(note int) (block, error) {
			phase, step := 0.0, midiHz(max(note, 0))/sampleRate
			return monoBlock(func() float32 {
				if note < 0 {
					return 0
				}
				v := 2*phase - 1
				phase += step
				phase -= math.Floor(phase)
				return float32(v)
			}), nil
		},
	}
}

// TestNaiveSawFailsTheAliasingGate proves the gate can fail: a naive saw,
// measured by the same code and judged against the real tolerance file,
// must be reported as worse than the band-limited saw's limit.
func TestNaiveSawFailsTheAliasingGate(t *testing.T) {
	e, err := measureVoice(naiveSawCase())
	if err != nil {
		t.Fatal(err)
	}
	naive := *e.Metrics[metricAliasing].Value
	t.Logf("naive saw aliasing: %.2f dB (C7 and C8, worst)", naive)
	tol, err := loadTolerances()
	if err != nil {
		t.Fatal(err)
	}
	// The naive saw must be worse than the aliasing limit of every voice that
	// has one, so no limit is loose enough to wave a non-band-limited
	// oscillator through.
	checked := 0
	for name, metrics := range tol.Limits {
		limit, ok := metrics[metricAliasing]
		if !ok || !strings.HasPrefix(name, "voice/") {
			continue
		}
		checked++
		if naive <= limit.Limit {
			t.Errorf("naive saw (%.2f dB) passes the %s limit (%.2f dB): the gate cannot detect aliasing", naive, name, limit.Limit)
		}
	}
	if checked < 10 {
		t.Errorf("only %d voices have an aliasing limit; expected at least 10", checked)
	}
	// Run it through gate() itself, as the real test does.
	r := newReport()
	r.Entries = []*entry{e}
	failed := false
	for _, f := range gate(r, tol) {
		if strings.HasPrefix(f, "voice/graph_saw: aliasing_db =") && strings.Contains(f, "worse than its limit") {
			failed = true
		}
	}
	if !failed {
		t.Error("gate() did not report the naive saw as worse than its limit")
	}
}

// TestBandLimitedSawPassesWhereNaiveFails is the paired positive control.
func TestBandLimitedSawPassesWhereNaiveFails(t *testing.T) {
	e, err := measureVoice(voiceCasesByName(t, "graph_saw"))
	if err != nil {
		t.Fatal(err)
	}
	tol, err := loadTolerances()
	if err != nil {
		t.Fatal(err)
	}
	if v, limit := *e.Metrics[metricAliasing].Value, tol.Limits["voice/graph_saw"][metricAliasing].Limit; v > limit {
		t.Errorf("band-limited saw %.2f dB exceeds its limit %.2f dB", v, limit)
	}
}

func voiceCasesByName(t *testing.T, name string) voiceCase {
	for _, c := range voiceCases() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no voice case %s", name)
	return voiceCase{}
}
