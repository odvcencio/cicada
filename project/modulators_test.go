package project

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const modulatorScore = `cicada 2
tempo 120
fx hall reverb {}
fx echo delay {}
track pad acid {}
track drums acid {}
pattern p { 1 . 3 . }
`

func compileModulators(t *testing.T, body string) (*Project, []notation.Diagnostic) {
	t.Helper()
	defer notation.OpenModulatorGate()()
	score, ds := notation.Parse([]byte(modulatorScore + body + "scene s { pad = p }\nsong { s*8 }\n"))
	if score == nil || hasErrors(ds) {
		return nil, ds
	}
	return FromScore(score)
}

func firstCode(ds []notation.Diagnostic) string {
	for _, d := range ds {
		if d.Severity == "error" {
			return d.Code + ": " + d.Message
		}
	}
	return ""
}

func TestModulatorsCompileToSemanticProject(t *testing.T) {
	p, ds := compileModulators(t, `
lfo drift { wave = triangle rate = 8bars phase = 0.25 }
lfo breath { rate = 0.07Hz loop = continue }
sway wander { rate = 2bars octaves = 2 }
envelope duck { attack = 5ms release = 120ms source = drums }
contour swell { period = 1/2. @0 -1 @1/4 1 smooth @1 -1 }
mod drift -> pad.pan depth = 0.15 add
mod breath -> pad.level depth = 1.5dB
mod wander -> pad.level depth = 0.5 multiply
mod duck -> pad.level depth = -6dB
`)
	if p == nil {
		t.Fatalf("compile failed: %v", ds)
	}
	if p.Format != FormatID2 || len(p.Modulators) != 5 || len(p.Routes) != 4 {
		t.Fatalf("project = %s %d %d", p.Format, len(p.Modulators), len(p.Routes))
	}
	drift := p.Modulators[0]
	if drift.RateTicks != 30720 || drift.Wave != "triangle" || drift.Phase != 0.25 || drift.Loop != "wrap" {
		t.Fatalf("drift = %+v", drift)
	}
	if b := p.Modulators[1]; b.RateMicroHz != 70000 || b.Loop != "continue" || b.Wave != "sine" {
		t.Fatalf("breath = %+v", b)
	}
	if c := p.Modulators[4]; c.RateTicks != 1440*2 || len(c.Points) != 3 || c.Points[1].At != 0.25 || c.Points[1].Shape != "smooth" || c.Points[0].Shape != "linear" {
		t.Fatalf("swell = %+v", c)
	}
	// Declaration order is the application order.
	var order []string
	for _, r := range p.Routes {
		order = append(order, r.Source+":"+r.Mode)
	}
	if got := strings.Join(order, " "); got != "drift:add breath:add wander:multiply duck:add" {
		t.Fatalf("route order = %s", got)
	}
	if err := ValidateProject(p); err != nil {
		t.Fatal(err)
	}
}

func TestModulatorsRoundTripThroughJSONAndSource(t *testing.T) {
	p, ds := compileModulators(t, `
lfo drift { rate = 8bars }
sway wander { rate = 3/8 octaves = 3 loop = continue }
envelope duck { source = drums }
contour swell { period = 2bars @0 -1 @0.25 0.5 exponential @1 -1 }
mod drift -> pad.pan depth = 0.15
mod wander -> pad.level depth = 2dB add
mod duck -> pad.level depth = 0.25 multiply
mod swell -> pad.pan depth = 0.3 add
`)
	if p == nil {
		t.Fatal(ds)
	}
	first, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := CanonicalJSON(decoded)
	if err != nil || !bytes.Equal(first, again) {
		t.Fatalf("JSON is not stable: %v\n%s\n%s", err, first, again)
	}
	defer notation.OpenModulatorGate()()
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	score, sds := notation.Parse(source)
	if score == nil || hasErrors(sds) {
		t.Fatalf("printed source does not parse: %v\n%s", sds, source)
	}
	q, qds := FromScore(score)
	if q == nil {
		t.Fatalf("%v\n%s", qds, source)
	}
	second, err := CanonicalJSON(q)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("source round trip changed the project: %v\n%s\n---\n%s\n--- source\n%s", err, first, second, source)
	}
}

func TestRouteDiagnostics(t *testing.T) {
	head := "lfo slow { rate = 8bars }\nlfo fast { rate = 1/64 }\nlfo quick { rate = 25Hz }\nsway deep { rate = 1/16 octaves = 4 }\nenvelope duck { source = drums }\n"
	cases := []struct{ name, route, want string }{
		{"legacy reverb", "mod slow -> hall.predelay depth = 20ms", "CICADA-ROUTE: hall.predelay is a legacy reverb"},
		{"legacy reverb row", "mod slow -> hall.size depth = 0.1", "CICADA-ROUTE: hall.size is a legacy reverb"},
		{"legacy delay", "mod slow -> echo.feedback depth = 0.1", "CICADA-ROUTE: echo.feedback is a legacy delay"},
		{"not a track", "mod slow -> master.level depth = 1dB", ""},
		{"envelope cycle", "mod duck -> drums.level depth = -3dB", "CICADA-ROUTE: envelope duck follows drums"},
		{"synced above 20 Hz", "mod fast -> pad.pan depth = 0.1", "CICADA-RATE"},
		{"free above 20 Hz", "mod quick -> pad.pan depth = 0.1", "CICADA-RATE"},
		{"sway octave above 20 Hz", "mod deep -> pad.pan depth = 0.1", "CICADA-RATE"},
		{"dB target needs dB", "mod slow -> pad.level depth = 0.5", "CICADA-UNIT"},
		{"pan target takes plain numbers", "mod slow -> pad.pan depth = 3dB", "CICADA-UNIT"},
		{"time unit on a pan", "mod slow -> pad.pan depth = 20ms", "CICADA-UNIT"},
		{"multiply is unitless", "mod slow -> pad.level depth = 1dB multiply", "CICADA-UNIT"},
		{"depth wider than range", "mod slow -> pad.pan depth = 5", "CICADA-PARAM"},
		{"unknown target", "mod slow -> ghost.pan depth = 0.1", "CICADA-REFERENCE"},
		{"unknown setting", "mod slow -> pad.nothing depth = 0.1", "CICADA-PARAM"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, ds := compileModulators(t, head+c.route+"\n")
			got := firstCode(ds)
			if c.want == "" {
				if p != nil || got == "" {
					t.Fatalf("accepted: %v", ds)
				}
				return
			}
			if p != nil || !strings.HasPrefix(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Tempo changes raise a synced source's rate: 1/16 at 120 BPM is 8 Hz and fits,
// the same source in a 300 BPM scene is 20 Hz and still fits, one tick shorter does not.
func TestRateCapUsesFastestTempo(t *testing.T) {
	p, ds := compileModulators(t, "lfo a { rate = 1/16 }\nmod a -> pad.pan depth = 0.1\n")
	if p == nil {
		t.Fatal(ds)
	}
	if p.Modulators[0].exceedsRate(p) {
		t.Fatal("8 Hz was refused")
	}
	p.TempoMilli = 300_000
	if p.Modulators[0].exceedsRate(p) {
		t.Fatal("20 Hz exactly was refused")
	}
	p.Modulators[0].RateTicks--
	if !p.Modulators[0].exceedsRate(p) {
		t.Fatal("above 20 Hz was accepted")
	}
}

func TestImportedModulatorsAreValidated(t *testing.T) {
	p, ds := compileModulators(t, "lfo a { rate = 1/4 }\nmod a -> pad.pan depth = 0.1\n")
	if p == nil {
		t.Fatal(ds)
	}
	mutate := func(f func(q *Project)) error {
		data, _ := CanonicalJSON(p)
		q, err := DecodeJSON(data)
		if err != nil {
			return err
		}
		f(q)
		return ValidateProject(q)
	}
	for name, f := range map[string]func(q *Project){
		"route to a legacy reverb": func(q *Project) { q.Routes[0].Target = "hall.size" },
		"unknown source":           func(q *Project) { q.Routes[0].Source = "ghost" },
		"both rates":               func(q *Project) { q.Modulators[0].RateMicroHz = 1_000_000 },
		"duplicate route":          func(q *Project) { q.Routes = append(q.Routes, q.Routes[0]) },
		"too many routes": func(q *Project) {
			for len(q.Routes) <= 64 {
				q.Routes = append(q.Routes, q.Routes[0])
			}
		},
	} {
		if err := mutate(f); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestImportedContourRequiresSyncedPeriod(t *testing.T) {
	p, ds := compileModulators(t, "contour c { period = 1bar @0 0 @1 1 }\n")
	if p == nil {
		t.Fatal(ds)
	}
	p.Modulators[0].RateTicks = 0
	p.Modulators[0].RateMicroHz = 1_000_000
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "CICADA-PARAM") {
		t.Fatalf("free-rate contour accepted: %v", err)
	}
}

func TestImportedModulatorsCannotSilentlyReachEngine(t *testing.T) {
	p, ds := compileModulators(t, "lfo a { rate = 1/4 }\nmod a -> pad.pan depth = 0.1\n")
	if p == nil {
		t.Fatal(ds)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	for name, compile := range map[string]func() error{
		"playback": func() error { _, err := CompileEngine(decoded, 48000, 128); return err },
		"assets":   func() error { _, err := CompileEngineWithAssets(decoded, 48000, 128, nil); return err },
		"prepared": func() error { _, err := CompileEnginePrepared(decoded, 48000, 128, nil); return err },
		"notes":    func() error { _, err := CompileNoteSchedule(decoded); return err },
	} {
		if err := compile(); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
			t.Errorf("%s silently ignored modulators: %v", name, err)
		}
	}
}

func TestScoresWithoutModulatorsAreUnchanged(t *testing.T) {
	score, ds := notation.Parse([]byte(modulatorScore + "scene s { pad = p }\nsong { s }\n"))
	if score == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	p, _ := FromScore(score)
	data, _ := CanonicalJSON(p)
	if bytes.Contains(data, []byte("modulators")) || bytes.Contains(data, []byte("routes")) {
		t.Fatalf("empty modulator arrays leaked into JSON: %s", data)
	}
}

// loop = continue fits the nearest whole number of cycles, at least one, and
// warns when that moves the rate by more than 10 percent.
func TestLoopContinueFitAndWarning(t *testing.T) {
	p, ds := compileModulators(t, "lfo slow { rate = 0.01Hz loop = continue }\nlfo near { rate = 0.2Hz loop = continue }\nlfo wrap { rate = 0.01Hz }\nmod slow -> pad.pan depth = 0.1\n")
	if p == nil {
		t.Fatal(ds)
	}
	// The song is 8 bars at 120 BPM: 16 seconds.
	cycles, authored, fitted, ok := LoopFit(p, "slow")
	if !ok || cycles != 1 || math.Abs(authored-0.01) > 1e-12 || math.Abs(fitted-1.0/16) > 1e-9 {
		t.Fatalf("slow: %d %g %g %v", cycles, authored, fitted, ok)
	}
	if cycles, _, fitted, _ := LoopFit(p, "near"); cycles != 3 || math.Abs(fitted-3.0/16) > 1e-9 {
		t.Fatalf("near: %d cycles, %g Hz", cycles, fitted)
	}
	if _, _, _, ok := LoopFit(p, "wrap"); ok {
		t.Fatal("a wrap source has no fit")
	}
	var warned []string
	for _, d := range ds {
		if d.Severity == "warning" && d.Code == "CICADA-RATE" {
			warned = append(warned, d.Message)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "lfo slow") || !strings.Contains(warned[0], "0.0625 Hz") || !strings.Contains(warned[0], "0.01 Hz") {
		t.Fatalf("warnings = %q", warned)
	}
}
