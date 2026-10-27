package notation

import (
	"strings"
	"testing"
)

func modalSustainScore(track, scene string) []byte {
	return []byte("cicada 2\ntrack vibes model_vibraphone { " + track + " }\npattern line notes { c4 . . . }\nscene main {\n  vibes = line\n  " + scene + "\n}\nsong { main }\n")
}

func TestModalTrackTakesTheSustainPedal(t *testing.T) {
	for _, setting := range []string{"sustain = on", "sustain = off", "sustain = 0", "sustain = 1", "sustain = 0.5", "sustain = 50%", "octave = 3 sustain = on"} {
		if _, diagnostics := Parse(modalSustainScore(setting, "")); len(diagnostics) != 0 {
			t.Errorf("%s: %+v", setting, diagnostics)
		}
	}
	for _, setting := range []string{"sustain = 2", "sustain = -1", "sustain = 1hz", "sustain = 3dB", "sustain = loud", "sustain = \"on\""} {
		_, diagnostics := Parse(modalSustainScore(setting, ""))
		if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-PARAM" || diagnostics[0].Position.Line != 2 || !strings.Contains(diagnostics[0].Message, "modal sustain must be on, off or a unitless value from 0 to 1") {
			t.Errorf("%s: %+v", setting, diagnostics)
		}
	}
	// The pedal is the one setting a modal track gained.
	for _, setting := range []string{"cutoff = 200Hz", "decay = 1s", "release = 1s"} {
		_, diagnostics := Parse(modalSustainScore(setting, ""))
		if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-PARAM" || !strings.Contains(diagnostics[0].Message, "unknown parameter") {
			t.Errorf("%s: %+v", setting, diagnostics)
		}
	}
}

func TestSceneCanSetTheModalPedal(t *testing.T) {
	if _, diagnostics := Parse(modalSustainScore("", "vibes.sustain = 1")); len(diagnostics) != 0 {
		t.Fatalf("%+v", diagnostics)
	}
	_, diagnostics := Parse(modalSustainScore("", "vibes.sustain = 2"))
	if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-UNIT" || !strings.Contains(diagnostics[0].Message, "vibes.sustain 2 is above the maximum 1") {
		t.Fatalf("%+v", diagnostics)
	}
	score, _ := Parse(modalSustainScore("", ""))
	for _, kind := range []string{"model_vibraphone", "model_marimba", "model_steelpan"} {
		if got := ParameterVoiceKind(score, kind); got != "modal" {
			t.Errorf("%s is %q", kind, got)
		}
	}
	if got := ParameterVoiceKind(score, "acid"); got != "acid" {
		t.Errorf("acid is %q", got)
	}
}
