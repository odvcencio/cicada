package render

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestLegacySceneOffRendersAfterSourceRoundTrip(t *testing.T) {
	const source = "cicada 1\ntrack bass acid {}\npattern riff acid steps=4 { 1 . 5 . }\nscene main { bass=riff }\nscene quiet { bass=off }\nsong { main quiet }\n"
	for _, name := range []string{"riff", "stop"} {
		t.Run(name, func(t *testing.T) {
			score, diagnostics := notation.Parse([]byte(strings.ReplaceAll(source, "riff", name)))
			if len(diagnostics) != 0 {
				t.Fatalf("parse: %+v", diagnostics)
			}
			p, diagnostics := project.FromScore(score)
			if p == nil || len(diagnostics) != 0 {
				t.Fatalf("project: %+v", diagnostics)
			}
			formatted, err := project.ToSource(p)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, diagnostics := notation.Parse(formatted)
			if len(diagnostics) != 0 {
				t.Fatalf("round trip: %+v", diagnostics)
			}
			var before, after bytes.Buffer
			opts := Options{SampleRate: 48000, Bits: 24}
			if _, err := WAV(score, opts, &before); err != nil {
				t.Fatal(err)
			}
			if _, err := WAV(roundTrip, opts, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("source round trip changed legacy scene audio")
			}
		})
	}
}
