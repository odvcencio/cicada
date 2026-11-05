package demobrowser

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/patternmachine"
)

func TestMachineStagesEditablePatternsAndUndo(t *testing.T) {
	for _, track := range []string{"bass", "drums"} {
		t.Run(track, func(t *testing.T) {
			s, err := NewSession()
			if err != nil {
				t.Fatal(err)
			}
			before := s.Snapshot()
			recipe := patternmachine.Recipe{Version: 1, Seed: 34891, Bars: 2, Density: 80, Motion: 70}
			idea, err := GenerateMachine(before.Source, track, "garden", recipe)
			if err != nil {
				t.Fatal(err)
			}
			again, err := GenerateMachine(before.Source, track, "garden", recipe)
			if err != nil || !bytes.Equal(idea.Source, again.Source) {
				t.Fatalf("generation must be deterministic: %v", err)
			}
			if !bytes.Equal(s.Snapshot().Source, before.Source) || bytes.Equal(idea.Source, before.Source) {
				t.Fatal("preview must stage an edit without changing the session")
			}
			if track == "drums" && (idea.Kind != "drums" || len(idea.Lanes) != 3) {
				t.Fatalf("named drum kit lanes: %+v", idea.Lanes)
			}
			if err := s.Apply(before.Revision, idea.Source); err != nil {
				t.Fatal(err)
			}
			if err := s.Apply(before.Revision, idea.Source); err == nil {
				t.Fatal("stale revision accepted")
			}
			if err := s.Undo(s.Snapshot().Revision); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(s.Snapshot().Source, before.Source) {
				t.Fatal("one undo should restore the score")
			}
		})
	}
}

func TestMachineRejectsInvalidSelection(t *testing.T) {
	r := patternmachine.Recipe{Version: 1, Bars: 1, Density: 60, Motion: 60}
	for _, selection := range [][2]string{{"missing", "garden"}, {"bass", "missing"}} {
		if _, err := GenerateMachine([]byte(ensemble), selection[0], selection[1], r); err == nil {
			t.Fatalf("accepted %v", selection)
		}
	}
	r.Bars = 3
	if _, err := GenerateMachine([]byte(ensemble), "bass", "garden", r); err == nil {
		t.Fatal("accepted invalid length")
	}
}
