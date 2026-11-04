package edit_test

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestModulationIntentsPreserveOwnedSourceAndValidate(t *testing.T) {
	entry := []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern pulse { 1 . }\nscene main { bass = pulse }\nsong { main }\n")
	part := []byte("lfo drift { rate = 1/4 }\n// keep this comment\nmod drift -> bass.pan depth = 0.2 // keep this too\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "part.cicada", Source: part}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	opts := edits.Options{Path: "main.cicada", Edition: 2, Sources: files, Compiler: compiler, ParseProject: compiler.parse}
	apply := func(intent edits.Intent) (*edits.Result, error) {
		return edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}, opts)
	}
	result, err := apply(&edits.ModulationRoute{Operation: "set", Source: "drift", Target: "bass.pan", Mode: "add", Depth: "0.4"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Source, entry) || len(result.Files) != 1 || result.Files[0].Path != "part.cicada" || !strings.Contains(string(result.Files[0].After), "depth = 0.4 // keep this too") || !strings.Contains(string(result.Files[0].After), "// keep this comment") {
		t.Fatalf("wrong owned-source edit: %+v", result)
	}
	result, err = apply(&edits.ModulationRoute{Operation: "remove", Source: "drift", Target: "bass.pan", Mode: "add"})
	if err != nil || len(result.Files) != 1 || strings.Contains(string(result.Files[0].After), "mod drift") || !strings.Contains(string(result.Files[0].After), "// keep this comment") {
		t.Fatalf("remove: %+v, %v", result, err)
	}
	if _, err := apply(&edits.ModulationRoute{Operation: "set", Source: "drift", Target: "bass.pan", Mode: "add", Depth: "5Hz"}); err == nil {
		t.Fatal("accepted depth with the wrong unit")
	}
	if _, err := apply(&edits.ModulationRoute{Operation: "add", Source: "drift", Target: "bass.pan", Mode: "add", Depth: "0.2"}); err == nil {
		t.Fatal("accepted duplicate route")
	}
	if _, err := apply(&edits.ModulationRoute{Operation: "add", Source: "drift", Target: "bass.pan", Mode: "add", Depth: "0.2\ntrack injected acid {}"}); err == nil {
		t.Fatal("accepted an injected declaration")
	}
}

func TestAddModulatorAndRouteCompileTogether(t *testing.T) {
	entry := []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern pulse { 1 . }\nscene main { bass = pulse }\nsong { main }\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	opts := edits.Options{Path: "main.cicada", Edition: 2, Sources: files, Compiler: compiler, ParseProject: compiler.parse}
	result, err := edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{
		&edits.AddModulator{SourceKind: "lfo", Name: "drift", Rate: "1/4", Wave: "triangle"},
		&edits.ModulationRoute{Operation: "add", Source: "drift", Target: "bass.cutoff", Mode: "add", Depth: "500Hz"},
	}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Source), "lfo drift { wave = triangle rate = 1/4 }") || !strings.Contains(string(result.Source), "mod drift -> bass.cutoff depth = 500Hz add") {
		t.Fatalf("missing source or route: %s", result.Source)
	}
}

func TestEditModulatorPreservesSourceAndValidatesPoints(t *testing.T) {
	entry := []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern pulse { 1 . }\nscene main { bass = pulse }\nsong { main }\n")
	part := []byte("lfo drift { rate = 1/4 // keep rate comment\n}\ncontour shape { period = 1bar @0 0 @1 0 }\nmod drift -> bass.pan depth = 0.2\n")
	apply := func(intent edits.Intent) error {
		files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "part.cicada", Source: part}}
		compiler := multiFileCompiler{path: "main.cicada", files: files}
		opts := edits.Options{Path: "main.cicada", Edition: 2, Sources: files, Compiler: compiler, ParseProject: compiler.parse}
		result, err := edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}, opts)
		if err != nil {
			return err
		}
		if !bytes.Equal(result.Source, entry) || len(result.Files) != 1 || result.Files[0].Path != "part.cicada" {
			t.Fatalf("wrong source owner: %+v", result)
		}
		part = result.Files[0].After
		return nil
	}
	for _, intent := range []edits.Intent{
		&edits.EditModulator{Operation: "set", Source: "drift", Field: "rate", Value: "1/2"},
		&edits.EditModulator{Operation: "set", Source: "drift", Field: "wave", Value: "triangle"},
		&edits.EditModulator{Operation: "point-add", Source: "shape", At: "1/2", Value: "0.75", Shape: "smooth"},
		&edits.EditModulator{Operation: "point-set", Source: "shape", Index: 1, At: "1/4", Value: "-0.5", Shape: "step"},
	} {
		if err := apply(intent); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"rate = 1/2 // keep rate comment", "wave = triangle", "@1/4 -0.5 step"} {
		if !strings.Contains(string(part), text) {
			t.Fatalf("missing %q: %s", text, part)
		}
	}
	for _, intent := range []edits.Intent{
		&edits.EditModulator{Operation: "set", Source: "drift", Field: "rate", Value: "1/4 }\ntrack injected acid {}"},
		&edits.EditModulator{Operation: "set", Source: "drift", Field: "rate", Value: "1/4 wave=square"},
		&edits.EditModulator{Operation: "point-add", Source: "shape", At: "1/4", Value: "0", Shape: "linear"},
		&edits.EditModulator{Operation: "point-set", Source: "shape", Index: 0, At: "1/2", Value: "0"},
		&edits.EditModulator{Operation: "remove", Source: "drift"},
	} {
		before := bytes.Clone(part)
		if err := apply(intent); err == nil || !bytes.Equal(before, part) {
			t.Fatalf("invalid edit accepted: %+v (%v)", intent, err)
		}
	}
	if err := apply(&edits.EditModulator{Operation: "point-remove", Source: "shape", Index: 1}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(part), "@1/4") {
		t.Fatalf("point not removed: %s", part)
	}
	if err := apply(&edits.EditModulator{Operation: "remove", Source: "shape"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(part), "contour shape") {
		t.Fatalf("source not removed: %s", part)
	}
}
