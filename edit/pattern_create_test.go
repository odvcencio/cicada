package edit_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/instrument"
)

func TestCreatePatternForNewInstrumentAndSceneControls(t *testing.T) {
	patch, _ := instrument.FindPatch("warm-pad")
	voice, _ := patch.Source("warm")
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(newline, func(t *testing.T) {
			text := "cicada 2\n// My sounds\n" + voice + "track keys warm {}\npattern existing notes { c4 . }\n" +
				"// Quiet introduction\nscene intro { keys=off }\n" +
				"scene main {}\nsong { intro main*4 }\n// Keep this ending comment\n"
			before := []byte(strings.ReplaceAll(text, "\n", newline))
			result, err := edit.Apply(before, edit.Envelope{Version: 1, Revision: edit.Revision(before), Intents: []edit.Intent{
				&edit.CreatePattern{Name: "glow", Track: "keys", Steps: 16, Scene: "main"},
			}}, edit.Options{Compiler: gridCompiler{}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Plan.Patterns) != 2 || result.Plan.Patterns[1].ID != "glow" || result.Plan.Patterns[1].Kind != "notes" || len(result.Plan.Patterns[1].Data) != 16 {
				t.Fatalf("new instrument needs an empty notes grid: %+v", result.Plan.Patterns)
			}
			for _, note := range result.Plan.Patterns[1].Data {
				if note != nil {
					t.Fatal("creating an empty grid inserted music")
				}
			}
			if result.Plan.Scenes[0].Bindings["keys"] != "off" || result.Plan.Scenes[1].Bindings["keys"] != "glow" {
				t.Fatal("creation lost the scene assignment or introduction")
			}
			originalWithBinding := strings.Replace(string(before), "scene main {}", "scene main {"+newline+"  keys = glow"+newline+"}", 1)
			if !bytes.HasPrefix(result.Source, []byte(originalWithBinding)) {
				t.Fatal("creation rewrote existing music, comments, or line endings")
			}
			// Newly created grids immediately accept normal edits and scene
			// stop/carry controls through the same public intent path.
			result, err = edit.Apply(result.Source, edit.Envelope{Version: 1, Intents: []edit.Intent{
				&edit.SetPitch{Entity: "step:glow/0", Pitch: 57},
				&edit.SetStep{Entity: "step:glow/1", Mode: "tie", Ratchet: 1, Chance: 100, Shared: "pattern"},
				&edit.BindScene{Scene: "intro", Track: "keys", Pattern: "keep"},
				&edit.BindScene{Scene: "main", Track: "keys", Pattern: "off"},
			}}, edit.Options{Compiler: gridCompiler{}})
			if err != nil || result.Plan.Patterns[1].Data[0].Note != 57 || !result.Plan.Patterns[1].Data[1].Tie || result.Plan.Scenes[0].Bindings["keys"] != "" || result.Plan.Scenes[1].Bindings["keys"] != "off" || !bytes.Contains(result.Source, []byte("keys=keep")) {
				t.Fatalf("new pattern is not immediately editable: %v", err)
			}
		})
	}
}

func TestCreatePatternInfersTrackKind(t *testing.T) {
	for _, test := range []struct {
		name, declarations, kind string
		lanes                    []string
	}{
		{"bass", "track target acid {}", "acid", nil},
		{"keys", "track target tine_ep {}", "notes", nil},
		{"drums", "track target drums {}", "drums", []string{"bd", "sd", "ch"}},
		{"kit", "kit taps { bd = builtin.bd sd = builtin.sd }\ntrack target taps {}", "drums", []string{"bd", "sd"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := []byte("cicada 2\n" + test.declarations + "\npattern existing notes { c4 . }\nscene main { target=off }\nsong { main }\n")
			result, err := edit.Apply(before, edit.Envelope{Version: 1, Intents: []edit.Intent{
				&edit.CreatePattern{Name: "idea", Track: "target", Steps: 8},
			}}, edit.Options{Compiler: gridCompiler{}})
			if err != nil {
				t.Fatal(err)
			}
			p := result.Plan.Patterns[1]
			if p.Kind != test.kind || p.Steps != 8 || !bytes.HasPrefix(result.Source, before) {
				t.Fatalf("wrong kind or destructive source insertion: %+v", p)
			}
			if result.Plan.Scenes[0].Bindings["target"] != "off" {
				t.Fatal("library-only creation changed the arrangement")
			}
			for _, lane := range test.lanes {
				if len(p.Lanes[lane]) != 8 {
					t.Fatalf("missing playable drum lane %s", lane)
				}
			}
		})
	}
}

func TestCreatePatternRefusesInvalidRequestsAtomically(t *testing.T) {
	before := []byte("cicada 2\ntrack keys tine_ep {}\ntrack vox audio {}\npattern existing notes { c4 . }\nscene main { keys=existing }\nsong { main }\n")
	if _, err := (gridCompiler{}).Compile(before, nil); err != nil {
		t.Fatal(err)
	}
	for _, test := range []edit.CreatePattern{
		{Name: "existing", Track: "keys", Steps: 16},
		{Name: "../escape", Track: "keys", Steps: 16},
		{Name: "off", Track: "keys", Steps: 16},
		{Name: "idea", Track: "keys", Steps: 0},
		{Name: "idea", Track: "keys", Steps: 65},
		{Name: "idea", Track: "missing", Steps: 16},
		{Name: "idea", Track: "vox", Steps: 16},
		{Name: "idea", Track: "keys", Steps: 16, Scene: "missing"},
	} {
		original := bytes.Clone(before)
		result, err := edit.Apply(before, edit.Envelope{Version: 1, Intents: []edit.Intent{&test}}, edit.Options{Compiler: gridCompiler{}})
		if err == nil || result != nil || !bytes.Equal(before, original) {
			t.Fatalf("invalid creation was not atomic: %+v, %v", test, err)
		}
	}
	_, err := edit.Apply(before, edit.Envelope{Version: 1, Revision: "stale", Intents: []edit.Intent{
		&edit.CreatePattern{Name: "idea", Track: "keys", Steps: 16, Scene: "main"},
	}}, edit.Options{Compiler: gridCompiler{}})
	var conflict *edit.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("stale creation was not refused: %v", err)
	}
}

func TestCreateFirstNotePatternAlongsideAudioClips(t *testing.T) {
	before := []byte("cicada 2\ntrack vox audio {}\ntrack keys tine_ep {}\n" +
		"asset wave \"silence.wav\" { sha256=\"" + strings.Repeat("a", 64) + "\" format=wav frames=480 rate=48000Hz channels=1 }\n" +
		"clip bed wave { start=0frames end=480frames }\nscene main { vox=bed keys=off }\nsong { main }\n")
	result, err := edit.Apply(before, edit.Envelope{Version: 1, Intents: []edit.Intent{
		&edit.CreatePattern{Name: "first", Track: "keys", Steps: 16, Scene: "main"},
	}}, edit.Options{Compiler: gridCompiler{}})
	if err != nil || len(result.Plan.Patterns) != 1 || result.Plan.Patterns[0].Kind != "notes" {
		t.Fatalf("could not create the first note pattern in an audio project: %v", err)
	}
	if result.Plan.Scenes[0].Bindings["vox"] != "bed" || result.Plan.Scenes[0].Bindings["keys"] != "first" {
		t.Fatal("first note pattern changed the existing audio binding")
	}
}
