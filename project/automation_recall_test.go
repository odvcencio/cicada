package project

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestAutomationRecallRoundTripsAndReappliesHeldValue(t *testing.T) {
	source := arrangementAutomationScore + "automate bass.level { @1.1.1 -6dB @5.1.1 -6dB smooth recall @3.1.1 }\n"
	score, ds := notation.Parse([]byte(source))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	controls, err := CompileAutomation(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(controls) != 2 || controls[0].Tick != 0 || controls[1].Tick != 7680 || math.Float32frombits(controls[1].Arg0) != -6 {
		t.Fatalf("missing held-value recall: %+v", controls)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ToSource(decoded)
	if err != nil || !strings.Contains(string(out), "recall @3.1.1") {
		t.Fatal(out, err)
	}
	restoredScore, ds := notation.Parse([]byte(out))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	restored, ds := FromScore(restoredScore)
	if hasErrors(ds) || !SemanticEqual(p, restored) {
		t.Fatal("recall lost during source/JSON round trip", ds)
	}
	for tick := int64(0); tick <= 8*3840; tick += 4 {
		a, ok := AutomationValue(p, p.Automation[0], tick)
		b, ready := AutomationValue(restored, restored.Automation[0], tick)
		if a != b || ok != ready {
			t.Fatal("recall changed curve shape", tick)
		}
	}
}

func TestAutomationRecallsValidatePositionAndUniqueness(t *testing.T) {
	for _, text := range []string{"recall @1.1.1", "recall @10.1.1", "recall @3.1.1 recall @3.1.1"} {
		score, ds := notation.Parse([]byte(arrangementAutomationScore + "automate bass.level { @2.1.1 -6dB " + text + " }\n"))
		if !hasErrors(ds) {
			_, ds = FromScore(score)
		}
		if !hasErrors(ds) {
			t.Fatal("accepted invalid recall", text)
		}
	}
	p := automationProject(t)
	p.Automation[0].Recalls = []int64{1}
	if err := ValidateProject(p); err == nil {
		t.Fatal("accepted off-grid JSON recall")
	}
	p.Automation[0].Recalls = make([]int64, 1025)
	if err := ValidateProject(p); err == nil {
		t.Fatal("accepted oversized recall list")
	}
}

func TestAutomationRecallLeavesMovingCurveUnchanged(t *testing.T) {
	p := automationProject(t)
	before, err := CompileAutomation(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Automation[0].Recalls = []int64{3840, 7680, 11520}
	after, err := CompileAutomation(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+3 {
		t.Fatalf("recalls replaced curve controls: %d -> %d", len(before), len(after))
	}
}
