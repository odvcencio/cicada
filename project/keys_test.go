package project

import (
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
