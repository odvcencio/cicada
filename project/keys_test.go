package project

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func keysScore(t *testing.T, source string) *Project {
	t.Helper()
	s, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatalf("parse: %v", ds)
		}
	}
	p, ds := FromScore(s)
	if p == nil {
		t.Fatalf("project: %v", ds)
	}
	return p
}

func TestAllKeysPatchesCompileChords(t *testing.T) {
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			p := keysScore(t, fmt.Sprintf("cicada 2\ntrack keys %s { voices=4 }\npattern p notes { [c4 e4 g4 b4] . c5 . }\nscene main { keys=p }\nsong { main }\n", name))
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil || !p.NeedsKeysEngine() || cfg.Track[0].Kind != engine.VoiceKeys || cfg.Track[0].Keys.Controls[127] != 4 {
				t.Fatalf("keys score compile: %v", err)
			}
			if _, err := engine.New(cfg); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKeysVoiceLimitsCountAgainstProjectBudget(t *testing.T) {
	var source strings.Builder
	source.WriteString("cicada 2\n")
	for n := 0; n < 8; n++ {
		fmt.Fprintf(&source, "track k%d tine_ep { voices=4 }\n", n)
	}
	source.WriteString("pattern p notes { c4 }\nscene main {\n")
	for n := 0; n < 8; n++ {
		fmt.Fprintf(&source, "k%d=p\n", n)
	}
	source.WriteString("}\nsong { main }\n")
	p := keysScore(t, source.String())
	if err := ValidateProject(p); err != nil {
		t.Fatalf("exact 32 voices rejected: %v", err)
	}
	v := p.Tracks[0].Params["voices"]
	n := 5.0
	v.Number = &n
	p.Tracks[0].Params["voices"] = v
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "32 allocated voices") {
		t.Fatalf("33 voices accepted: %v", err)
	}
}

func TestKeysUnitsAndEnums(t *testing.T) {
	p := keysScore(t, "cicada 2\ntrack e tine_ep { release=20ms decay=12s tremolo_rate=5hz sustain=on }\ntrack o tonewheel_organ { voices=4 scanner=c3 percussion=third percussion_fast=fast percussion_soft=soft rotary_fast=fast }\npattern p notes { c4 . }\nscene main { e=p o=p }\nsong { main }\n")
	e, err := KeysSpecFromValues(p.Tracks[0].Kind, p.Tracks[0].Params)
	if err != nil || e.Controls[5] != .02 || e.Controls[4] != 12 || e.Controls[0] != 1 {
		t.Fatalf("EP units: %+v %v", e, err)
	}
	o, err := KeysSpecFromValues(p.Tracks[1].Kind, p.Tracks[1].Params)
	if err != nil || o.Controls[13] != 6 || o.Controls[10] != 2 || o.Controls[20] != 1 {
		t.Fatalf("organ enums: %+v %v", o, err)
	}
}

func TestKeysAuthoredInstrumentsOverrideBuiltInSources(t *testing.T) {
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			// Authored graphs accept c0; modeled keyboards start at MIDI 21.
			p := keysScore(t, fmt.Sprintf("cicada 2\ninstrument %s { voice poly { out = sine(pitch) * env(gate, 20ms) } }\ntrack part %s {}\npattern p notes { [c0 e0] }\nscene main { part=p }\nsong { main }\n", name, name))
			if p.NeedsKeysEngine() {
				t.Fatal("authored instrument was mistaken for a built-in keyboard")
			}
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil || cfg.Track[0].Kind != engine.VoiceGraph || cfg.Track[0].Polyphony != 4 || cfg.Track[0].Keys != nil {
				t.Fatalf("authored graph source changed: kind=%d polyphony=%d keys=%v err=%v", cfg.Track[0].Kind, cfg.Track[0].Polyphony, cfg.Track[0].Keys, err)
			}
		})
	}
}

func TestKeysDeclaredSamplersOverrideBuiltInSources(t *testing.T) {
	_, source, _ := assetFixture(t)
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(string(source), "sampler hit {", "sampler "+name+" {", 1)
			text = strings.Replace(text, "track chops hit {", "track chops "+name+" {", 1)
			p := keysScore(t, "cicada 2\n"+text)
			if p.NeedsKeysEngine() || !p.NeedsSampleEngine() {
				t.Fatal("declared sampler was mistaken for a built-in keyboard")
			}
			if p.Tracks[0].Kind != name || len(p.Samplers) != 1 || p.Samplers[0].Name != name || p.Samplers[0].Asset != "vocal" || p.Samplers[0].RootMIDI != 48 || p.Samplers[0].Mode != "oneshot" || p.Samplers[0].Voices != 8 {
				t.Fatal("declared sample source or its settings changed")
			}
			// The kernel-only compiler must retain the existing sample-host
			// requirement instead of selecting a modeled keyboard fallback.
			if _, err := CompileEngine(p, 48000, 128); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
				t.Fatalf("sampler bypassed its sample-capable host: %v", err)
			}
		})
	}
}

func TestKeysExpressionRejectsModeledSourcesAndPreservesAuthoredOverrides(t *testing.T) {
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			for _, row := range []string{"bend: 0ct 25ct", "vibrato: 0ct 4ct", "pressure: 0 0.7", "timbre: . 0.8"} {
				source := fmt.Sprintf("track part %s {}\npattern take notes { c4 - %s }\nscene main { part=take }\nsong { main }\n", name, row)
				score, diagnostics := notation.Parse([]byte(source))
				if hasErrors(diagnostics) {
					t.Fatalf("valid expression syntax rejected: %+v", diagnostics)
				}
				_, diagnostics = Check(score)
				if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-UNSUPPORTED" || diagnostics[0].Position.Line != 2 {
					t.Fatalf("modeled keys expression diagnostic: %+v", diagnostics)
				}
				if _, err := CompilePattern(score, score.Patterns[0], score.Tracks[0]); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
					t.Fatalf("modeled keys expression compiled: %v", err)
				}
				graph := fmt.Sprintf("instrument %s { voice mono { out = sine(pitch) * env(gate, 100ms) } }\n", name)
				p := keysScore(t, graph+source)
				cfg, err := CompileEngine(p, 48000, 128)
				if err != nil || p.NeedsKeysEngine() || cfg.Track[0].Kind != engine.VoiceGraph || cfg.Patterns[0].Slots[0].Expression == nil {
					t.Fatalf("authored graph expression was mistaken for modeled keys: %v", err)
				}
			}
			p := keysScore(t, fmt.Sprintf("track part %s {} pattern take notes { c4 - } scene main { part=take } song { main }", name))
			p.Patterns[0].Expression = []NoteExpression{{Timbre: .5}, {PitchCents: 25, Timbre: .5}}
			if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
				t.Fatalf("modeled keys semantic expression accepted: %v", err)
			}
			encoded, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeJSON(encoded); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
				t.Fatalf("modeled keys JSON expression accepted: %v", err)
			}
		})
	}
}

func TestKeysNumericDetuneControlsUseCents(t *testing.T) {
	for _, test := range []struct {
		kind, params string
		indices      []int
		values       []float32
	}{
		{"fm_ep", "op1_detune=12 op6_detune=-7", []int{4, 64}, []float32{12, -7}},
		{"poly_keys", "detune=9 drift=2", []int{7, 8}, []float32{9, 2}},
	} {
		t.Run(test.kind, func(t *testing.T) {
			p := keysScore(t, fmt.Sprintf("cicada 2\ntrack part %s { %s }\npattern p notes { c4 . }\nscene main { part=p }\nsong { main }\n", test.kind, test.params))
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			for i, index := range test.indices {
				if got := cfg.Track[0].Keys.Controls[index]; got != test.values[i] {
					t.Fatalf("control %d=%g want %g", index, got, test.values[i])
				}
			}
		})
	}
}
