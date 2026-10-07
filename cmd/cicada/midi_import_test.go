package main

import (
	"m31labs.dev/cicada/smf"
	"os"
	"path/filepath"
	"testing"
)

func TestMIDIImportWritesValidScoreAndPreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "idea.mid")
	output := filepath.Join(dir, "idea.cicada")
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := smf.Encode(smf.File{PPQ: 960, TempoMicros: 500000, Tracks: []smf.TrackChunk{{Notes: []smf.Note{{Tick: 0, Dur: 176, Note: 60, Vel: 64}, {Tick: 320, Dur: 176, Note: 64, Vel: 100}}}}}, f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := importMIDICommand([]string{input, "-o", output}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProject(output); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := importMIDICommand([]string{input, "-o", output}); err == nil {
		t.Fatal("existing score replaced")
	}
	after, _ := os.ReadFile(output)
	if string(before) != string(after) {
		t.Fatal("existing score changed")
	}
}
