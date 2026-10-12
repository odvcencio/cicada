package edit_test

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestPublishRecordedTextAndAuxiliaryScene(t *testing.T) {
	source := []byte(recordScore)
	declaration := "sampler captured { pack=\"assets/manifest.json\" sha256=\"" + strings.Repeat("a", 64) + "\" root=c4 mode=oneshot voices=4 }"
	in := &edits.PublishRecorded{Name: "captured", Declaration: declaration, Scene: "main", Level: "-6dB", Note: "c4"}
	r, err := edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, edits.Options{Compiler: acceptCompiler{}})
	want := "cicada 2\n" + strings.Replace(recordScore, "bass=pulse drums=beat }", "bass=pulse drums=beat \n  captured_track = captured_taps\n}", 1) + "\n" + declaration + "\ntrack captured_track captured { level = -6dB }\npattern captured_taps { c4 . . . c4 . . . }\n"
	if err != nil || r == nil || string(r.Source) != want || r.Label != "Recorded instrument added · captured" {
		t.Fatalf("publish: %v %+v", err, r)
	}
	before := []byte("scene main { bass=pulse }\r\n")
	in.ScenePath = "scenes.cicada"
	r, err = edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, edits.Options{Path: "main.cicada", Sources: []notation.SourceFile{{Path: "scenes.cicada", Source: before}}, Compiler: acceptCompiler{}})
	if err != nil || r == nil || len(r.Files) != 1 || r.Files[0].Path != "scenes.cicada" || string(r.Files[0].Before) != string(before) || string(r.Files[0].After) != "scene main { bass=pulse \r\n  captured_track = captured_taps\r\n}\r\n" {
		t.Fatalf("scene file: %v %+v", err, r)
	}
}

func TestPublishRecordedPreservesPresetAndUsesSuffixedName(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(map[string]string{"\n": "lf", "\r\n": "crlf"}[newline], func(t *testing.T) {
			preset := "preset captured { instrument=acid cutoff=900Hz } // keep preset\n"
			asset := "asset wave \"recorded.wav\" { sha256=\"" + strings.Repeat("a", 64) + "\" format=wav frames=480 rate=48000Hz channels=1 }\n"
			source := []byte(strings.ReplaceAll("cicada 2\n"+preset+asset+recordScore, "\n", newline))
			before := bytes.Clone(source)
			compiler := m5Compiler{}
			if _, err := compiler.Compile(source, nil); err != nil {
				t.Fatalf("invalid starting score: %v", err)
			}
			declaration := "sampler captured { asset=wave root=c4 mode=oneshot voices=4 }"
			result, err := applyM5(t, source, &edits.PublishRecorded{Name: "captured", Declaration: declaration, Scene: "main", Level: "-6dB", Note: "c4"})
			if err != nil || result == nil {
				t.Fatalf("publication with an occupied preset name must succeed: %v", err)
			}
			if result.Response["instrument"] != "captured_2" || result.Response["track"] != "captured_2_track" || result.Label != "Recorded instrument added · captured_2" {
				t.Fatalf("wrong allocated name: %v %q", result.Response, result.Label)
			}
			want := strings.Replace(string(source), "bass=pulse drums=beat }", "bass=pulse drums=beat "+newline+"  captured_2_track = captured_2_taps"+newline+"}", 1) +
				"\n" + strings.Replace(declaration, "sampler captured {", "sampler captured_2 {", 1) + "\ntrack captured_2_track captured_2 { level = -6dB }\npattern captured_2_taps { c4 . . . c4 . . . }\n"
			if string(result.Source) != want || !bytes.Equal(source, before) || len(result.Files) != 0 {
				t.Fatal("publication changed bytes outside its declarations and scene binding")
			}
			score, ds := notation.Parse(result.Source)
			if len(score.Presets) != 1 || score.Presets[0].Name != "captured" || len(score.Samplers) != 1 || score.Samplers[0].Name != "captured_2" {
				t.Fatalf("preset or sampler changed: %v", ds)
			}
		})
	}
}
