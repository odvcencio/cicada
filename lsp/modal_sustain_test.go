package lsp

import (
	"bytes"
	"testing"
)

// A modeled percussion track completes its pedal and the mixer controls, and
// none from another voice.
func TestModalPathCompletionOffersTheSustainPedal(t *testing.T) {
	for _, profile := range []string{"model_vibraphone", "model_marimba", "model_conga"} {
		source := []byte("track vibes " + profile + " {}\nscene main { vibes.\n}\n")
		items := parameterPathCompletion(source, utf16Position(source, bytes.Index(source, []byte("vibes."))+len("vibes.")))
		labels := map[string]bool{}
		for _, item := range items {
			labels[item["label"].(string)] = true
		}
		for _, want := range []string{"sustain", "level", "pan"} {
			if !labels[want] {
				t.Errorf("%s: completion missing %q: %v", profile, want, labels)
			}
		}
		for _, avoid := range []string{"tremolo", "gain", "drive", "output", "cutoff"} {
			if labels[avoid] {
				t.Errorf("%s: completion offers %q from another voice", profile, avoid)
			}
		}
	}
}
