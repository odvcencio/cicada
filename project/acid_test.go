package project

import (
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestAcidParameterUnitsAndRanges(t *testing.T) {
	score := firstScore(t)
	params, err := CompileAcidParams(score.Tracks[0])
	if err != nil {
		t.Fatal(err)
	}
	if params.Cutoff != 620 || params.Decay != .38 || params.Resonance != .62 {
		t.Fatalf("wrong acid parameters: %+v", params)
	}
	project, diagnostics := FromScore(score)
	if project == nil || len(diagnostics) != 0 {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	bad := 620.0
	project.Tracks[0].Params["cutoff"] = Value{Unit: "ms", Number: &bad}
	if err := ValidateProject(project); err == nil {
		t.Fatal("accepted JSON acid cutoff with milliseconds")
	}

	source := []byte(`cicada 1
tempo 120
key a minor
track bass acid { cutoff = 700ms }
pattern p acid steps=1 { 1 }
scene main { bass=p }
song { main }
`)
	score, diagnostics = notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	_, diagnostics = Check(score)
	if len(diagnostics) == 0 || diagnostics[0].Code != "CICADA-PARAM" {
		t.Fatalf("expected acid unit error, got %+v", diagnostics)
	}
}

func TestTrackGateBecomesPatternDefault(t *testing.T) {
	source := []byte(`cicada 1
tempo 120
key a minor
track bass acid { gate = 70 }
pattern p acid steps=1 { 1 }
scene main { bass=p }
song { main }
`)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	compiled, err := CompilePattern(score, score.Patterns[0], score.Tracks[0])
	if err != nil {
		t.Fatal(err)
	}
	if compiled[0].Pattern.GatePercent != 70 {
		t.Fatalf("gate default %d", compiled[0].Pattern.GatePercent)
	}
}
