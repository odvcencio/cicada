package project

import (
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const parameterPathScore = "fx delay { feedback = 0.2 }\ntrack bass acid { send_a = 0.2 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"

func TestResolveParameterPathOwnerAndKernelAddress(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(parameterPathScore))
	if score == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("parse base score: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("compile base project: %+v", diagnostics)
	}
	for _, test := range []struct {
		path, kind, owner, id string
		track                 uint8
	}{
		{"bass.send.delay", "track", "bass", "mix.send_a", 0},
		{"delay.feedback", "effect", "delay", "fx.delay.feedback", 0xff},
		{"tempo", "global", "tempo", "global.tempo", 0xff},
	} {
		resolved, err := ResolveParameterPath(p, test.path)
		if err != nil {
			t.Fatalf("resolve %s: %v", test.path, err)
		}
		if resolved.Path != test.path || resolved.OwnerKind != test.kind || resolved.Owner != test.owner || resolved.Descriptor.ID != test.id || resolved.ID == 0 || resolved.Track != test.track {
			t.Errorf("resolved %s = %+v", test.path, resolved)
		}
	}
	for path, code := range map[string]string{
		"ghost.cutoff": "CICADA-REFERENCE",
		"bass.unknown": "CICADA-PARAM",
		"master.level": "CICADA-UNSUPPORTED",
	} {
		_, err := ResolveParameterPath(p, path)
		var pathErr *PathError
		if !errors.As(err, &pathErr) || pathErr.Code != code {
			t.Errorf("resolve %s error = %v, want %s", path, err, code)
		}
	}
}

func TestResolveParameterPathForCustomVoiceAndKitTracks(t *testing.T) {
	source := "instrument kick { voice mono { out = sine(pitch) * env(gate, 120ms); } }\nkit steel { bd = kick; ch = builtin.ch; }\ntrack keys kick {}\ntrack drums steel {}\npattern beat drums steps=1 { bd: x; }\nscene main { drums=beat keys.pan=0.25 drums.bd_tune=50Hz }\nsong { main }\n"
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("parse custom voice/kit score: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("compile custom voice/kit score: %+v", diagnostics)
	}
	for _, path := range []string{"keys.pan", "drums.bd_tune"} {
		if _, err := ResolveParameterPath(p, path); err != nil {
			t.Errorf("resolve %s: %v", path, err)
		}
	}
}

func TestScenePathValidationCodesAndAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name, source, path, code string
	}{
		{"unknown owner", parameterPathScore + "scene extra { ghost.cutoff=900Hz }\n", "ghost.cutoff", "CICADA-REFERENCE"},
		{"unknown setting", parameterPathScore + "scene extra { bass.mystery=1 }\n", "bass.mystery", "CICADA-PARAM"},
		{"bad unit", parameterPathScore + "scene extra { bass.cutoff=3dB }\n", "bass.cutoff", "CICADA-UNIT"},
		{"unknown toggle value", parameterPathScore + "scene extra { delay.pingpong=yes }\n", "delay.pingpong", "CICADA-UNIT"},
		{"invalid toggle spelling", parameterPathScore + "scene extra { delay.pingpong=maybe }\n", "delay.pingpong", "CICADA-UNIT"},
		{"nonlive", "fx drive { shape = soft }\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene main { bass=riff drive.shape=hard }\nsong { main }\n", "drive.shape", "CICADA-UNSUPPORTED"},
		{"ambiguous owner", "fx delay { feedback = 0.2 }\ntrack delay acid {}\npattern riff acid steps=1 { 1 }\nscene main { delay=riff delay.feedback=0.3 }\nsong { main }\n", "delay.feedback", "CICADA-REFERENCE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := notation.Parse([]byte(test.source))
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == test.code && (test.name != "ambiguous owner" || strings.Contains(diagnostic.Message, "hint:")) {
					return
				}
			}
			t.Fatalf("path %s did not report %s: %+v", test.path, test.code, diagnostics)
		})
	}
}

// Each keyboard family resolves its own live controls plus the ones all
// keyboards share. A path from another family fails with CICADA-PARAM in both
// the scene check and the project resolver.
func TestKeyboardFamiliesResolveTheirOwnControls(t *testing.T) {
	families := []struct {
		patch string
		own   []string
	}{
		{"tine_ep", []string{"tremolo", "tremolo_rate", "auto_pan", "gain", "drive"}},
		{"reed_tremolo", []string{"tremolo", "tremolo_rate", "auto_pan", "gain", "drive"}},
		{"clav", []string{"gain", "drive"}},
		{"tonewheel_organ", []string{"gain", "drive", "rotary_mix", "rotary_fast", "drawbar1", "drawbar5", "drawbar9"}},
		{"fm_ep", []string{"gain"}},
		{"poly_keys", []string{"drive", "output"}},
		{"string_machine", []string{"output"}},
	}
	shared := []string{"sustain", "level", "pan"}
	all := map[string]bool{}
	for _, family := range families {
		for _, name := range family.own {
			all[name] = true
		}
	}
	for _, family := range families {
		source := "cicada 2\ntrack keys " + family.patch + " {}\npattern p notes { c4 }\nscene main { keys=p }\nsong { main }\n"
		p := keysScore(t, source)
		own := map[string]bool{}
		for _, name := range family.own {
			own[name] = true
		}
		for name := range all {
			path := "keys." + name
			_, err := ResolveParameterPath(p, path)
			var pathErr *PathError
			switch {
			case own[name] && err != nil:
				t.Errorf("%s: %s should resolve: %v", family.patch, path, err)
			case !own[name] && (!errors.As(err, &pathErr) || pathErr.Code != "CICADA-PARAM"):
				t.Errorf("%s: %s error = %v, want CICADA-PARAM", family.patch, path, err)
			}
			// The scene check in the notation package gives the same answer.
			_, diagnostics := notation.Parse([]byte(strings.Replace(source, "scene main { keys=p }", "scene main { keys=p "+path+"="+validKeysSettingValue(name)+" }", 1)))
			found := false
			for _, diagnostic := range diagnostics {
				found = found || diagnostic.Code == "CICADA-PARAM"
			}
			if own[name] == found {
				t.Errorf("%s: scene setting %s: CICADA-PARAM reported=%v, want %v: %+v", family.patch, path, found, !own[name], diagnostics)
			}
		}
		for _, name := range shared {
			if _, err := ResolveParameterPath(p, "keys."+name); err != nil {
				t.Errorf("%s: shared control %s should resolve: %v", family.patch, name, err)
			}
		}
	}
}

func validKeysSettingValue(name string) string {
	switch {
	case strings.HasPrefix(name, "drawbar"):
		return "4"
	case name == "tremolo_rate":
		return "5hz"
	case name == "output":
		return "-6db"
	case name == "rotary_fast":
		return "fast"
	}
	return "0.5"
}

func TestKeyboardControlsAreAutomatableExceptWholeNumbers(t *testing.T) {
	p := keysScore(t, "cicada 2\ntrack keys tonewheel_organ {}\npattern p notes { c4 }\nscene main { keys=p }\nsong { main }\n")
	for _, name := range []string{"gain", "drive", "rotary_mix"} {
		if resolved, err := ResolveParameterPath(p, "keys."+name); err != nil || !resolved.Descriptor.Automatable {
			t.Errorf("keys.%s should be automatable: %v", name, err)
		}
	}
	for _, name := range []string{"rotary_fast", "drawbar1", "drawbar9"} {
		resolved, err := ResolveParameterPath(p, "keys."+name)
		if err != nil || resolved.Descriptor.Automatable || !resolved.Descriptor.Live {
			t.Errorf("keys.%s should be a live scene setting that automation cannot move: %v", name, err)
		}
	}
	score, diagnostics := notation.Parse([]byte("cicada 2\ntrack keys tonewheel_organ {}\npattern p notes { c4 }\nautomate keys.drawbar1 { @1.1.1 2 @2.1.1 6 }\nscene main { keys=p }\nsong { main*2 }\n"))
	if !hasProjectErrors(diagnostics) {
		_, diagnostics = FromScore(score)
	}
	if !hasProjectErrors(diagnostics) {
		t.Fatal("automating a whole-number control was accepted")
	}
}

// A declaration named like a keyboard patch replaces the patch, so keyboard
// controls are unknown settings in the scene check and in the project resolver
// (and the language server's completion agrees: see lsp).
func TestOverridingDeclarationRemovesKeyboardControls(t *testing.T) {
	const declaration = "instrument tine_ep { voice mono { out = sine(pitch) * env(gate, 120ms); } }\n"
	const body = "track keys tine_ep {}\npattern p notes { c4 }\nscene main { keys=p SETTING }\nsong { main }\n"
	score, diagnostics := notation.Parse([]byte(declaration + strings.Replace(body, "SETTING", "keys.pan=0.25", 1)))
	if score == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("compile: %+v", diagnostics)
	}
	if _, err := ResolveParameterPath(p, "keys.pan"); err != nil {
		t.Errorf("mixer control should resolve: %v", err)
	}
	for _, path := range []string{"keys.tremolo", "keys.sustain"} {
		var pathErr *PathError
		if _, err := ResolveParameterPath(p, path); !errors.As(err, &pathErr) || pathErr.Code != "CICADA-PARAM" {
			t.Errorf("%s resolved for an overridden patch: %v", path, err)
		}
	}
	_, diagnostics = notation.Parse([]byte(declaration + strings.Replace(body, "SETTING", "keys.tremolo=0.5", 1)))
	found := false
	for _, d := range diagnostics {
		found = found || d.Code == "CICADA-PARAM"
	}
	if !found {
		t.Errorf("notation accepted keys.tremolo on an overridden patch: %+v", diagnostics)
	}
}
