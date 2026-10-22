package edit_test

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestSelectTakeIntentUsesTheProjectSnapshot(t *testing.T) {
	source := []byte("cicada 2\ntrack vox audio {}\nscene main { vox=off bass=pulse }\nsong { main }\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: source}, {Path: "part.cicada", Source: []byte("track bass acid {}\npattern pulse acid { 1 . 5 . }\n")}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	in := &edits.SelectTake{ID: "take-project", Track: "vox", Scene: "main", Rate: 48000, Channels: 1, Asset: edits.TakeAsset{Name: "take-project", Path: "take.wav", SHA256: strings.Repeat("a", 64), Frames: 48, RateHz: 48000, Channels: 1}}
	for _, projectParser := range []bool{false, true} {
		options := edits.Options{Path: "main.cicada", Edition: 2, Sources: files, Compiler: compiler}
		if projectParser {
			options.ParseProject = compiler.parse
		}
		result, err := edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, options)
		if err != nil || !bytes.Contains(result.Source, []byte("vox=take-project-clip")) {
			t.Fatalf("take intent parsed only the entry file: parser=%v error=%v", projectParser, err)
		}
		for _, file := range result.Files {
			if file.Path == "part.cicada" && !bytes.Equal(file.Before, file.After) {
				t.Fatal("take selection changed the unrelated source file")
			}
		}
	}
	files[1].Source = []byte("pattern incomplete {\n")
	if _, err := edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, edits.Options{Path: "main.cicada", Edition: 2, Sources: files, ParseProject: compiler.parse, Compiler: compiler}); err == nil {
		t.Fatal("take selection bypassed project validation")
	}
}

func TestSelectTakeExactBytesAndErrors(t *testing.T) {
	source := "cicada 2\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { vox = off bass = pulse }\nsong { main }\n"
	in := edits.SelectTake{ID: "take-fixed", Track: "vox", Scene: "main", Rate: 48000, Channels: 1, StartFrame: 4, Asset: edits.TakeAsset{Name: "take-fixed", Path: "take.wav", SHA256: strings.Repeat("a", 64), Frames: 10, RateHz: 48000, Channels: 1}}
	suffix := "\nasset take-fixed \"take.wav\" {\n  sha256 = \"" + strings.Repeat("a", 64) + "\"\n  format = wav\n  frames = 10\n  rate = 48000Hz\n  channels = 1\n  source = recorded\n}\n\nclip take-fixed-clip take-fixed {\n  start = 4frames\n  end = 10frames\n}\n"
	for _, newline := range []string{"\n", "\r\n"} {
		before := strings.ReplaceAll(source, "\n", newline)
		want := strings.Replace(before, "vox = off", "vox = take-fixed-clip", 1) + suffix
		got, err := edits.SelectTakeSource([]byte(before), in)
		if err != nil || string(got) != want {
			t.Fatalf("selection: %v\n%s\nwant:%s", err, got, want)
		}
		replay, err := edits.SelectTakeSource(got, in)
		if err != nil || string(replay) != want {
			t.Fatalf("replay: %v\n%s", err, replay)
		}
		collision := strings.Replace(want, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
		if _, err := edits.SelectTakeSource([]byte(collision), in); err == nil || err.Error() != "take asset name collision" {
			t.Fatalf("collision: %v", err)
		}
	}
	for _, c := range []struct {
		track, scene string
		start        int64
		want         string
	}{
		{"bass", "main", 4, "take target must be an existing audio track"},
		{"vox", "missing", 4, "take scene no longer exists"},
		{"vox", "main", 10, "take contains only preroll; retained for recovery"},
	} {
		copy := in
		copy.Track, copy.Scene, copy.StartFrame = c.track, c.scene, c.start
		if _, err := edits.SelectTakeSource([]byte(source), copy); err == nil || err.Error() != c.want {
			t.Fatalf("error: %v want %s", err, c.want)
		}
	}
}
