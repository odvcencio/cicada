package render

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestCustomInstrumentOctaveRenderAndCompatibility(t *testing.T) {
	const arrangement = "\npattern riff notes steps=4 { 1 . 5 . }\nscene main { lead=riff }\nsong { main }\n"
	const legacy = "cicada 1\ninstrument tone { param octave: hz = 440hz; voice mono { out = sine(octave); } }\ntrack lead tone {}"
	const override = "cicada 1\ninstrument tone { voice mono { out = sine(pitch); } }\ntrack lead tone { octave=3 }"
	render := func(source string) (engine.Config, []byte) {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(source))
		if len(diagnostics) != 0 {
			t.Fatalf("parse: %+v", diagnostics)
		}
		p, diagnostics := project.FromScore(score)
		if p == nil || len(diagnostics) != 0 {
			t.Fatalf("project: %+v", diagnostics)
		}
		cfg, err := project.CompileEngine(p, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := engine.New(cfg); err != nil {
			t.Fatal(err)
		}
		canonical, err := project.CanonicalJSON(p)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := project.DecodeJSON(canonical)
		if err != nil || !project.SemanticEqual(p, decoded) {
			t.Fatalf("JSON round trip: %v", err)
		}
		formatted, err := project.ToSource(decoded)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, diagnostics := notation.Parse(formatted)
		if len(diagnostics) != 0 {
			t.Fatalf("source round trip: %+v", diagnostics)
		}
		var output, after bytes.Buffer
		opts := Options{SampleRate: 48000, Bits: 24}
		report, err := WAV(score, opts, &output)
		if err != nil || report.Peak <= 0 {
			t.Fatalf("render: %v; peak=%v", err, report.Peak)
		}
		if _, err := WAV(roundTrip, opts, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output.Bytes(), after.Bytes()) {
			t.Fatal("source round trip changed instrument audio")
		}
		return cfg, output.Bytes()
	}
	legacyOverride := strings.Replace(legacy, "track lead tone {}", "track lead tone { octave=660hz }", 1)
	for _, test := range []struct{ name, source, control string }{
		{"register", override, strings.ReplaceAll(strings.Replace(override, "instrument tone {", "instrument tone { octave=3", 1), "track lead tone { octave=3 }", "track lead tone {}")},
		{"legacy parameter", legacy, strings.ReplaceAll(legacy, "octave", "frequency")},
		{"legacy parameter override", legacyOverride, strings.ReplaceAll(legacyOverride, "octave", "frequency")},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, audio := render(test.source + arrangement)
			wantCfg, wantAudio := render(test.control + arrangement)
			if !reflect.DeepEqual(cfg, wantCfg) || !bytes.Equal(audio, wantAudio) {
				t.Fatal("octave name changed native configuration or offline audio")
			}
		})
	}
}
