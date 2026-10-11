package edit_test

import (
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

func TestLibraryIntentsExactBytes(t *testing.T) {
	source := []byte("cicada 2\ntrack lead acid { cutoff=900Hz pan=0.2 }\npattern melody { c3 . g3 . }\nscene main { lead=melody }\nsong { main }\n")
	// The host resolves libraries and validates the candidate; these checks
	// exercise the pure text half with literal references from that resolution.
	for _, c := range []struct{ kind, effect, ref, instance, want string }{
		{"instrument", "", "synth.glassbass", "", strings.Replace(string(source), "track lead acid", "track lead synth.glassbass", 1) + "\nimport \"std/synth\"\n"},
		{"effect", "delay", "fx.delay", "", strings.Replace(string(source), "pan=0.2 }", "pan=0.2 \n  send fx.delay = 0.4\n}", 1) + "\nimport \"std/synth\"\n"},
		{"effect", "comp", "fx.comp", "", strings.Replace(string(source), "pan=0.2 }", "pan=0.2 \n  out = music\n}", 1) + "\nimport \"std/synth\"\n\nbus music { insert = fx.comp }\n"},
	} {
		in := &edits.InsertLibraryItem{Track: "lead", ImportPath: "std/synth", Reference: c.ref, ItemKind: c.kind, EffectKind: c.effect, PresetInstance: c.instance}
		r, err := edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, edits.Options{Compiler: acceptCompiler{}})
		if err != nil || r == nil || string(r.Source) != c.want || r.Label != "Library: insert "+c.ref+" on lead" {
			t.Fatalf("%s: %v %+v", c.ref, err, r)
		}
	}
	declaration := "\npreset bright {\n  instrument = acid\n  pan = 0.2\n  cutoff = 900Hz\n}\n"
	r, err := applyM5(t, source, &edits.SavePreset{Track: "lead", Name: "bright", Declaration: declaration})
	if err != nil || r == nil || string(r.Source) != string(source)+declaration || r.Label != "Preset: save bright from lead" {
		t.Fatalf("save: %v %+v", err, r)
	}
}

type acceptCompiler struct{}

func (acceptCompiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	return &edits.Plan{Revision: edits.Revision(source)}, nil
}
