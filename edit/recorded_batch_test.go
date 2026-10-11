package edit_test

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestPublishRecordedBatchAccumulatesSecondaryScene(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(map[string]string{"\n": "lf", "\r\n": "crlf"}[newline], func(t *testing.T) {
			entry := []byte(strings.ReplaceAll("cicada 2\ntrack bass acid {}\npattern pulse acid steps=4 { c4 . . . }\nsong { main }\nasset wave \"recorded.wav\" { sha256=\""+strings.Repeat("a", 64)+"\" format=wav frames=480 rate=48000Hz channels=1 }\n", "\n", newline))
			part := []byte("// preserve scene file" + newline + "scene main { bass=pulse }" + newline)
			files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "part.cicada", Source: part}}
			compiler := multiFileCompiler{path: "main.cicada", files: files}
			var intents []edits.Intent
			for range 2 {
				intents = append(intents, &edits.PublishRecorded{Name: "captured", Declaration: "sampler captured { asset=wave root=c4 mode=oneshot voices=4 }", Scene: "main", ScenePath: "part.cicada", Level: "-6dB", Note: "c4"})
			}
			result, err := edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: intents}, edits.Options{Compiler: compiler, Path: compiler.path, Edition: 2, Sources: files})
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(string(part), "bass=pulse }", "bass=pulse "+newline+"  captured_track = captured_taps"+newline+newline+"  captured_2_track = captured_2_taps"+newline+"}", 1)
			if len(result.Files) != 1 || result.Files[0].Path != "part.cicada" || !bytes.Equal(result.Files[0].Before, part) || string(result.Files[0].After) != want {
				t.Fatalf("expected one cumulative scene patch: %+v", result.Files)
			}
			for _, name := range []string{"captured", "captured_2"} {
				if len(result.Plan.Scenes) != 1 || result.Plan.Scenes[0].Bindings[name+"_track"] != name+"_taps" {
					t.Fatalf("compiled candidate lost %s: %+v", name, result.Plan.Scenes)
				}
			}
		})
	}
}
