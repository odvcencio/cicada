package edit_test

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestLearnMidiPreservesOtherProjectFilesAndComments(t *testing.T) {
	entry := []byte("cicada 2\ntrack bass acid {}\npattern phrase { 1 }\nscene main { bass = phrase }\nsong { main }\n")
	part := []byte("midi {\n  port keys\n  keys ch 2 cc 74 -> bass.pan // keep comment\n}\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "part.cicada", Source: part}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	opts := edits.Options{Path: "main.cicada", Edition: 2, Sources: files, Compiler: compiler, ParseProject: compiler.parse}
	apply := func(intent edits.Intent) (*edits.Result, error) {
		return edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}, opts)
	}
	result, err := apply(&edits.SetMIDIMapping{Port: "keys", Message: "cc", Channel: 2, Number: 74, Target: "bass.cutoff"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Source, entry) || len(result.Files) != 1 || !strings.Contains(string(result.Files[0].After), "keys ch 2 cc 74 -> bass.cutoff // keep comment") {
		t.Fatalf("mapping replacement: %+v", result)
	}
	result, err = apply(&edits.SetMIDIMapping{Port: "keys", Message: "note", Channel: 1, Number: 60, Target: "launch main"})
	if err != nil || len(result.Files) != 1 || !strings.Contains(string(result.Files[0].After), "keys ch 1 note 60 -> launch main") {
		t.Fatalf("mapping insert: %+v %v", result, err)
	}
	if _, err := apply(&edits.SetMIDIMapping{Port: "keys", Message: "cc", Channel: 2, Number: 74, Target: "bass.cutoff\ntrack hack acid {}"}); err == nil {
		t.Fatal("accepted injected source")
	}
}
