package project

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func automationProject(t *testing.T) *Project {
	t.Helper()
	source, err := os.ReadFile("../examples/continuous-automation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	return p
}

func TestAutomationControlSpaceAndRoundTrip(t *testing.T) {
	p := automationProject(t)
	lane := p.Automation[0]
	value, ok := AutomationValue(p, lane, 7680)
	if !ok || math.Abs(value-math.Sqrt(400*2400)) > .00001 {
		t.Fatalf("log midpoint = %g", value)
	}
	linear := lane
	linear.Points = append([]AutomationPoint(nil), lane.Points...)
	linear.Points[1].Shape = "linear"
	other, _ := AutomationValue(p, linear, 7680)
	if other != value {
		t.Fatal("exponential alias differs")
	}
	controls, err := CompileAutomation(p)
	if err != nil {
		t.Fatal(err)
	}
	// The four-tick grid would give about 7700 controls; the exponential
	// cutoff moves under a resolution step per grid tick, so it is thinned.
	if len(controls) < 3500 || len(controls) > 4500 {
		t.Fatalf("control count %d", len(controls))
	}
	for i := 1; i < len(controls); i++ {
		if controls[i].Tick < controls[i-1].Tick {
			t.Fatal("controls unordered")
		}
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	restored, ds := FromScore(score)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	a, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Format != FormatID2 || len(decoded.Automation) != 2 {
		t.Fatal("automation lost its version-2 wire form")
	}
	decodedSource, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decodedSource), "automate bass.cutoff") {
		t.Fatal("decoded automation lost source")
	}
	b, err := CanonicalJSON(restored)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("automation round trip changed semantics")
	}
}

func TestAutomationDiagnostics(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{"@0.1.1 400Hz", "CICADA-POSITION"},
		{"@1.5.1 400Hz", "CICADA-POSITION"},
		{"@1.1.1 400ms", "CICADA-UNIT"},
		{"@1.1.1 400Hz linear_period", "CICADA-UNSUPPORTED"},
		{"@2.1.1 400Hz @1.1.1 500Hz", "CICADA-POSITION"},
		{"@1.1.1 400Hz @1.1.1 500Hz", "CICADA-POSITION"},
		{"@1.1.1 400Hz curve 9", "CICADA-PARAM"},
	} {
		score, ds := notation.Parse([]byte("track bass acid {} pattern a { 1 } automate bass.cutoff { " + tc.body + " } scene main { bass=a } song { main*4 }"))
		if !hasErrors(ds) {
			_, ds = FromScore(score)
		}
		found := false
		for _, d := range ds {
			found = found || d.Code == tc.code
		}
		if !found {
			t.Errorf("%s: want %s; got %v", tc.body, tc.code, ds)
		}
	}
	p := automationProject(t)
	p.Automation[0].Path = "missing.cutoff"
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
}

func TestNumericAutomationWithSynchronizedTimeAlternatives(t *testing.T) {
	source := []byte("track bass acid {} pattern a { 1 } fx dub delay { time=200ms } automate dub.time { @1.1.1 200ms @2.1.1 800ms } scene main { bass=a } song { main*2 }")
	score, ds := notation.Parse(source)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	value, ok := AutomationValue(p, p.Automation[0], 1920)
	if !ok || math.Abs(value-400) > 0.00001 {
		t.Fatalf("time midpoint %g", value)
	}
	if _, err := CompileAutomation(p); err != nil {
		t.Fatal(err)
	}
}

func automationLaneProject(t *testing.T, bars int, lanes string) *Project {
	t.Helper()
	source := "cicada 2\ntempo 112\ntrack keys acid {}\npattern a { 1 }\n" + lanes + "scene main { keys = a }\nsong { main*" + strconv.Itoa(bars) + " }\n"
	score, ds := notation.Parse([]byte(source))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	return p
}

func TestLongSlowAutomationLaneCompiles(t *testing.T) {
	p := automationLaneProject(t, 72, "automate keys.pan { @1.1.1 0 @9.1.1 -0.35 smooth @71.1.1 0 smooth }\n")
	controls, err := CompileAutomation(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(controls) == 0 || len(controls) > 5000 {
		t.Fatalf("slow lane prepared %d controls", len(controls))
	}
	first, last := controls[0], controls[len(controls)-1]
	if first.Tick != 0 || last.Tick != 70*3840 || math.Float32frombits(last.Arg0) != 0 {
		t.Fatalf("endpoints not kept: first tick %d, last tick %d value %g", first.Tick, last.Tick, math.Float32frombits(last.Arg0))
	}
}

func TestAutomationLimitStillReportsRealOverflow(t *testing.T) {
	var body strings.Builder
	body.WriteString("automate keys.pan {\n")
	for bar := 0; bar < 100; bar++ {
		value := "-1"
		if bar%2 == 1 {
			value = "1"
		}
		body.WriteString("  @" + strconv.Itoa(bar+1) + ".1.1 " + value + " linear\n")
	}
	body.WriteString("}\n")
	p := automationLaneProject(t, 100, body.String())
	if _, err := CompileAutomation(p); err == nil || !strings.Contains(err.Error(), "CICADA-LIMIT") {
		t.Fatalf("want CICADA-LIMIT, got %v", err)
	}
}

func TestSlowAutomationStaysWithinOneResolutionStep(t *testing.T) {
	for _, lane := range []string{
		"automate keys.pan { @1.1.1 0 @9.1.1 -0.35 smooth @68.1.1 0 smooth }\n",
		"automate keys.cutoff { @1.1.1 400Hz @40.1.1 2400Hz smooth @68.1.1 500Hz curve 1 }\n",
	} {
		p := automationLaneProject(t, 70, lane)
		controls, err := CompileAutomation(p)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := ResolveParameterPath(p, p.Automation[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		d := resolved.Descriptor
		measure := func(v float64) float64 { return v }
		step := (d.Max - d.Min) / 4096
		if d.Curve == "log" {
			measure = math.Log
			step = (math.Log(d.Max) - math.Log(d.Min)) / 4096
		}
		held, next := 0.0, 0
		end := p.Automation[0].Points[len(p.Automation[0].Points)-1].Tick
		for tick := int64(0); tick <= end; tick += 4 {
			for next < len(controls) && controls[next].Tick <= tick {
				held = float64(math.Float32frombits(controls[next].Arg0))
				next++
			}
			exact, _ := AutomationValue(p, p.Automation[0], tick)
			if diff := math.Abs(measure(held) - measure(exact)); diff > step*1.01 {
				t.Fatalf("%s tick %d: held %g exact %g differ by %g > step %g", d.Path, tick, held, exact, diff, step)
			}
		}
	}
}
