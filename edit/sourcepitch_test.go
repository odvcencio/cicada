package edit

import "testing"

func TestSourcePitchSpellsMIDIExtremes(t *testing.T) {
	for pitch, want := range map[int]string{0: "c0,", 48: "c3", 61: "c#4", 127: "g6'''"} {
		if got := sourcePitch(pitch); got != want {
			t.Errorf("MIDI %d: got %q, want %q", pitch, got, want)
		}
	}
}
