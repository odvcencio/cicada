package edit_test

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/patternmachine"
)

func TestGeneratedPatternIsScopedAndEditable(t *testing.T) {
	for _, fixture := range []struct{ track, kind string }{{"track keys tine_ep {}", "notes"}, {"track keys acid {}", "acid"}, {"track keys drums {}", "drums"}} {
		before := []byte("cicada 2\nkey f# minor\n// keep this comment\n" + fixture.track + "\npattern original notes { c4 . }\nscene main { keys=off }\nsong { main }\n")
		recipe := patternmachine.Recipe{Version: 1, Seed: 34891, Bars: 4, Root: 6, Scale: "minor", Density: 88, Motion: 60}
		intent := &edit.CreatePattern{Name: "idea", Track: "keys", Steps: 64, Starter: "machine", Machine: &recipe}
		result, err := edit.Apply(before, edit.Envelope{Version: 1, Revision: edit.Revision(before), Intents: []edit.Intent{intent}}, edit.Options{Compiler: gridCompiler{}})
		if err != nil {
			t.Fatal(fixture.kind, err)
		}
		if !bytes.HasPrefix(result.Source, before) || result.Plan.Patterns[1].Kind != fixture.kind || result.Plan.Patterns[1].Steps != 64 || result.Plan.Scenes[0].Bindings["keys"] != "off" {
			t.Fatal("generated edit rewrote original project")
		}
		if !bytes.Contains(result.Source, []byte("step = 1/16")) {
			t.Fatal("sixteenth timing missing")
		}
		lanes := []string(nil)
		if fixture.kind == "drums" {
			lanes = []string{"bd", "sd", "ch", "oh", "rs"}
		}
		generated, err := patternmachine.Generate(recipe, fixture.kind, lanes)
		if err != nil {
			t.Fatal(err)
		}
		if fixture.kind != "drums" {
			for i, want := range generated.Notes {
				got := result.Plan.Patterns[1].Data[i]
				if want.Velocity == 0 && got != nil || want.Velocity != 0 && (got == nil || int(got.Note) != want.Pitch || int(got.Velocity) != want.Velocity) {
					t.Fatalf("preview differs from saved step %d: %+v / %+v", i, want, got)
				}
			}
		} else {
			for _, lane := range lanes {
				got := result.Plan.Patterns[1].Lanes[lane]
				if len(got) != 64 {
					t.Fatalf("saved lane %s has %d steps", lane, len(got))
				}
				for i, velocity := range generated.Lanes[lane] {
					if (velocity == 0) != (got[i] == nil) {
						t.Fatalf("preview differs from saved %s step %d", lane, i)
					}
				}
			}
		}
		intent.Steps = 16
		if out, err := edit.Apply(before, edit.Envelope{Version: 1, Intents: []edit.Intent{intent}}, edit.Options{Compiler: gridCompiler{}}); err == nil || out != nil {
			t.Fatal("mismatched recipe accepted")
		}
	}
}
