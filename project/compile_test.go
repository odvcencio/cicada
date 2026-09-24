package project

import (
	"os"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func firstScore(t *testing.T) *notation.Score {
	t.Helper()
	src, err := os.ReadFile("../examples/first-acid.cicada")
	if err != nil {
		t.Fatal(err)
	}
	s, ds := notation.Parse(src)
	if len(ds) != 0 {
		t.Fatalf("score diagnostics: %+v", ds)
	}
	return s
}

func TestCompileFirstAcid(t *testing.T) {
	s := firstScore(t)
	compiled, err := CompilePattern(s, s.Patterns[0], s.Tracks[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 || compiled[0].Pattern.SwingPermille != 80 || compiled[0].Pattern.GatePercent != 55 {
		t.Fatalf("wrong compiled pattern: %+v", compiled)
	}
	want := []uint8{45, 45, 52, 45, 43}
	for i, cell := range []int{0, 2, 3, 5, 6} {
		step, err := seq.UnpackStep(compiled[0].Pattern.Steps[cell])
		if err != nil || step.Note != want[i] {
			t.Fatalf("cell %d: got %+v, %v; want note %d", cell, step, err, want[i])
		}
	}
	step, _ := seq.UnpackStep(compiled[0].Pattern.Steps[2])
	if !step.Slide {
		t.Fatal("slide flag lost")
	}
	step, _ = seq.UnpackStep(compiled[0].Pattern.Steps[0])
	if !step.Accent {
		t.Fatal("accent flag lost")
	}
}

func TestCompileDrumLanes(t *testing.T) {
	s := firstScore(t)
	compiled, err := CompilePattern(s, s.Patterns[3], s.Tracks[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 4 || compiled[0].Lane != "bd" {
		t.Fatalf("wrong drum lanes: %+v", compiled)
	}
	step, err := seq.UnpackStep(compiled[0].Pattern.Steps[0])
	if err != nil || step.Note != 36 || step.Velocity != 127 || !step.Accent {
		t.Fatalf("wrong kick: %+v, %v", step, err)
	}
}

func TestPhraseTransposition(t *testing.T) {
	s := firstScore(t)
	compiled, err := CompilePattern(s, s.Patterns[1], s.Tracks[0])
	if err != nil {
		t.Fatal(err)
	}
	first, _ := seq.UnpackStep(compiled[0].Pattern.Steps[0])
	second, _ := seq.UnpackStep(compiled[0].Pattern.Steps[8])
	if first.Note != 45 || second.Note != 57 {
		t.Fatalf("phrase transpose got %d and %d, want 45 and 57", first.Note, second.Note)
	}
}

func TestCustomInstrumentNotePattern(t *testing.T) {
	s := firstScore(t)
	compiled, err := CompilePattern(s, s.Patterns[2], s.Tracks[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 || compiled[0].Kind != "notes" {
		t.Fatalf("custom note pattern failed: %+v", compiled)
	}
	step, _ := seq.UnpackStep(compiled[0].Pattern.Steps[0])
	if step.Note != 52 {
		t.Fatalf("first lead note = %d, want E3 (52)", step.Note)
	}
}

func TestUnspecifiedPentatonicDegreeIsRejected(t *testing.T) {
	s := firstScore(t)
	s.Scale = "pent"
	_, err := CompilePattern(s, s.Patterns[0], s.Tracks[0])
	if err == nil {
		t.Fatal("expected undefined degree mapping for pent scale")
	}
}
