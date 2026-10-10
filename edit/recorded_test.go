package edit_test

import (
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
