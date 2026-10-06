package project

import (
	"math"
	"os"
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
	if len(controls) < 7000 || len(controls) > 8000 {
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
	a, _ := CanonicalJSON(p)
	b, _ := CanonicalJSON(restored)
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
