package project

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/notation"
)

func TestStdKeysPresetControlsMatchDirectNativeControls(t *testing.T) {
	for _, test := range []struct{ patch, defaults, overrides, direct string }{
		{"tine_ep", "pickup_distance=0.2 release=100ms sustain=on voices=2", "release=0.15 level=-9dB", "pickup_distance=0.2 sustain=on voices=2 release=0.15 level=-9dB"},
		{"reed_ep", "hammer_felt=0.7 tremolo_rate=5hz voices=2", "drive=0.3", "hammer_felt=0.7 tremolo_rate=5hz voices=2 drive=0.3"},
		{"clav", "pickup=difference string_mute=0.6 voices=2", "pickup=neck click=0.1", "pickup=neck string_mute=0.6 voices=2 click=0.1"},
		{"tonewheel_organ", "scanner=c3 percussion=third percussion_fast=fast voices=4", "percussion_soft=soft rotary_fast=fast drawbar1=8", "scanner=c3 percussion=third percussion_fast=fast voices=4 percussion_soft=soft rotary_fast=fast drawbar1=8"},
		{"fm_ep", "op3_ratio=3.7 op1_detune=12 voices=2", "op3_attack=15ms route3_1=0.8", "op3_ratio=3.7 op1_detune=12 voices=2 op3_attack=15ms route3_1=0.8"},
		{"poly_keys", "filter=state_variable cutoff=2khz detune=12 drift=3 voices=4", "pwm=0.1", "filter=state_variable cutoff=2khz detune=12 drift=3 voices=4 pwm=0.1"},
		{"string_machine", "ensemble=0.6 attack=0.08 voices=4", "release=350ms", "ensemble=0.6 attack=0.08 voices=4 release=350ms"},
	} {
		t.Run(test.patch, func(t *testing.T) {
			phrase := "\npattern melody notes { c4 . e4 . }\nscene main { part=melody }\nsong { main }\n"
			sources := []string{
				"cicada 2\npreset selected { instrument=builtin." + test.patch + " " + test.defaults + " }\ntrack part selected { " + test.overrides + " }" + phrase,
				"cicada 2\ntrack part " + test.patch + " { " + test.direct + " }" + phrase,
			}
			var cfgs [2]engine.Config
			for i, source := range sources {
				score, ds := notation.Parse([]byte(source))
				if score == nil || hasErrors(ds) {
					t.Fatalf("parse%d: %+v", i, ds)
				}
				p, ds := FromScore(score)
				if p == nil || hasErrors(ds) {
					t.Fatalf("project%d: %+v", i, ds)
				}
				cfg, err := CompileEngine(p, 48000, 128)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Track[0].Kind != engine.VoiceKeys {
					t.Fatal("preset lost native keyboard routing")
				}
				cfgs[i] = cfg
			}
			if !reflect.DeepEqual(cfgs[0].Track, cfgs[1].Track) || !reflect.DeepEqual(cfgs[0].Patterns, cfgs[1].Patterns) {
				t.Fatal("preset controls differ from direct native controls")
			}
		})
	}
}

func TestStdKeysImportedPresetsPreserveLocalDeclarationOverrides(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := fmt.Sprintf("cicada 2\nimport \"std/keys\"\ninstrument %s { voice mono { out=sine(pitch)*env(gate,90ms)*velocity } }\ntrack part keys.%s {}\npattern melody notes { c4 . e4 . }\nscene main { part=melody }\nsong { main }\n", name, name)
			libraryWrite(t, root, "main.cicada", source)
			pinLibraryFixture(t, root)
			score, ds, err := LoadScore(filepath.Join(root, "main.cicada"), nil)
			if err != nil || score == nil || hasErrors(ds) {
				t.Fatalf("local override: %v %+v", err, ds)
			}
			p, ds := FromScore(score)
			if p == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			if p.NeedsKeysEngine() || cfg.Track[0].Kind != engine.VoiceGraph {
				t.Fatal("standard preset displaced a local instrument declaration")
			}
		})
	}
}

func TestStdKeysExamplePinsMatchEmbeddedNativeLibrary(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	sources, err := ReadSources(stdKeysExamplePath("fm_ep"), nil)
	if err != nil {
		t.Fatal(err)
	}
	library := sources.Libraries["std/keys"]
	if library == nil || library.Manifest.Capabilities != 512 || LibraryCapabilities&512 == 0 {
		t.Fatal("native standard keyboard capability missing")
	}
	_, changes, err := sources.UpdateLibraries("")
	if err != nil || len(changes) != 0 {
		t.Fatalf("embedded library/example pin mismatch: %v %v", changes, err)
	}
}

func TestStdKeysNativePresetRejectsInvalidControls(t *testing.T) {
	for _, test := range []struct{ patch, params, code string }{
		{"tine_ep", "voices=3.5", "CICADA-PRESET-TYPE"},
		{"tine_ep", "voices=0", "CICADA-PRESET-RANGE"},
		{"tine_ep", "pickup_distance=0.1", "CICADA-PRESET-RANGE"},
		{"fm_ep", "op1_ratio=1hz", "CICADA-PRESET-UNIT"},
		{"tonewheel_organ", "scanner=8", "CICADA-PRESET-RANGE"},
		{"tonewheel_organ", "scanner=unknown_mode", "CICADA-PRESET-TYPE"},
		{"clav", "missing_control=0.4", "CICADA-PRESET-PARAM"},
	} {
		t.Run(test.patch+"/"+test.params, func(t *testing.T) {
			source := "cicada 2\npreset selected { instrument=builtin." + test.patch + " " + test.params + " }\ntrack part selected {}\npattern p notes { c4 . }\nscene main { part=p }\nsong { main }\n"
			score, ds := notation.Parse([]byte(source))
			if score != nil {
				_, more := FromScore(score)
				ds = append(ds, more...)
			}
			for _, d := range ds {
				if d.Code == test.code {
					return
				}
			}
			t.Fatalf("invalid control did not produce %s: %+v", test.code, ds)
		})
	}
}
