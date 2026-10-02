package project

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestExperimentalGuitarRoundTripAndRegistry(t *testing.T) {
	source, err := os.ReadFile("../examples/expressive-guitar.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, ds := notation.Parse(rewritten)
	if reparsed == nil || hasErrors(ds) {
		t.Fatalf("%+v\n%s", ds, rewritten)
	}
	recompiled, ds := FromScore(reparsed)
	if recompiled == nil || hasErrors(ds) || !SemanticEqual(p, recompiled) {
		t.Fatalf("guitar source/JSON/source changed: %+v", ds)
	}
	for _, name := range []string{"bend", "vibrato", "brightness", "damping", "pickup", "drive"} {
		resolved, err := ResolveParameterPath(p, "lead."+name)
		if err != nil {
			t.Fatal(err)
		}
		d := resolved.Descriptor
		if d.Type != "number" || d.SmoothingMS != 8 || !d.Live || !d.Automatable || d.Unit == "" {
			t.Fatalf("incomplete guitar metadata: %+v", d)
		}
		if _, err := ParamAddressByName(p, "lead."+name); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(data, []byte(`"experimental"`)) {
		t.Fatal("JSON lost experimental opt-in")
	}
	delete(decoded.Tracks[0].Params, "experimental")
	if _, err := CanonicalJSON(decoded); err == nil {
		t.Fatal("JSON accepted guitar without opt-in")
	}
	for _, rate := range []int{44100, 48000} {
		cfg, err := CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Track[0].Kind != engine.VoiceGuitar || !cfg.Track[0].Experimental {
			t.Fatal("guitar was not lowered to the opted-in voice")
		}
		if _, err := engine.New(cfg); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResolveParameterPath(p, "lead.cutoff"); err == nil {
		t.Fatal("guitar inherited acid controls")
	}
	if kernel.Params[kernel.ParamGuitarBend].ID != 131 {
		t.Fatal("guitar moved existing parameter IDs")
	}
}

func TestExperimentalGuitarDiagnostics(t *testing.T) {
	const base = "cicada 2\ntrack lead guitar { experimental = on\nCONTROL\n}\npattern p { 1 . }\nscene main { lead = p }\nsong { main }\n"
	for _, tc := range []struct{ name, control, code string }{
		{"wrong units", "brightness = 900Hz", "CICADA-UNIT"},
		{"range", "vibrato = 101", "CICADA-PARAM"},
		{"pickup range", "pickup = 0.5", "CICADA-PARAM"},
		{"fractional octave", "octave = 2.5", "CICADA-PARAM"},
		{"unknown", "pressure = 0.4", "CICADA-PARAM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			score, ds := notation.Parse([]byte(strings.Replace(base, "CONTROL", tc.control, 1)))
			if !hasErrors(ds) {
				_, more := Check(score)
				ds = append(ds, more...)
			}
			for _, d := range ds {
				if d.Code == tc.code && d.Position.Line == 3 {
					return
				}
			}
			t.Fatalf("wanted %s at line 3, got %+v", tc.code, ds)
		})
	}
	for _, source := range []string{
		strings.Replace(base, "experimental = on", "experimental = off", 1),
		strings.Replace(base, "experimental = on", "", 1),
		strings.Replace(base, "cicada 2", "cicada 1", 1),
	} {
		source = strings.Replace(source, "CONTROL", "", 1)
		_, ds := notation.Parse([]byte(source))
		if !hasErrors(ds) {
			t.Fatalf("missing edition/opt-in rejection: %s", source)
		}
	}
}
