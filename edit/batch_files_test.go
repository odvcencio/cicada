package edit

import (
	"bytes"
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestContextAccumulatesAuxiliaryFiles(t *testing.T) {
	entry := []byte("title \"Entry\"\ntrack bass acid {}\npattern pulse acid steps=4 { c4 . . . }\nscene home { bass=pulse }\nsong { home }\n")
	original := []byte("scene main { bass=pulse }\n")
	first := []byte("scene first { bass=pulse }\n")
	last := []byte("scene last { bass=pulse }\n")
	ctx := &Context{Options: Options{Compiler: &fakeCompiler{}, Path: "main.cicada", Edition: 2,
		Sources: []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "part.cicada", Source: original}}}, Source: entry}
	ctx.AddFile(File{Path: "part.cicada", Before: original, After: first})
	ctx.AddFile(File{Path: "other.cicada", Before: nil, After: []byte("// new\n")})
	ctx.AddFile(File{Path: "part.cicada", Before: first, After: last})
	want := []File{{Path: "part.cicada", Before: original, After: last}, {Path: "other.cicada", After: []byte("// new\n")}}
	if !reflect.DeepEqual(ctx.files, want) {
		t.Fatalf("staged files must retain original Before and cumulative After: %+v", ctx.files)
	}
	for _, candidate := range []*Context{ctx, sourceContext(ctx, bytes.Replace(entry, []byte("Entry"), []byte("Working"), 1))} {
		score, ds, err := candidate.ParseProject()
		if err != nil || hasErrors(ds) || len(score.Scenes) != 2 || score.Scenes[1].Name != "last" {
			t.Fatalf("project parsing must see the staged source: %v %+v %+v", err, ds, score)
		}
	}
	// Hosts may supply just the other buffers; the working entry still belongs
	// to the cumulative project view.
	ctx.Options.Sources = ctx.Options.Sources[1:]
	score, ds, err := ctx.ParseProject()
	if err != nil || hasErrors(ds) || len(score.Scenes) != 2 || score.Title != "Entry" {
		t.Fatalf("auxiliary-only Sources lost the working entry: %v %+v", err, ds)
	}
	// AddFile owns its snapshots; later caller edits cannot change the candidate.
	original[0], last[0] = 'X', 'X'
	if !bytes.HasPrefix(ctx.files[0].Before, []byte("scene")) || !bytes.HasPrefix(ctx.files[0].After, []byte("scene")) {
		t.Fatal("staged bytes alias caller buffers")
	}
}

type stagedCompiler struct {
	files map[string][]byte
	calls int
}

func (c *stagedCompiler) Compile(source []byte, files map[string][]byte) (*Plan, error) {
	c.files, c.calls = files, c.calls+1
	return &Plan{Revision: Revision(source)}, nil
}

func TestCurrentPlanAndHostParserUseStagedFiles(t *testing.T) {
	compiler := &stagedCompiler{}
	ctx := &Context{Source: []byte("entry"), Options: Options{Path: "main.cicada", Compiler: compiler}}
	if _, err := ctx.CurrentPlan(); err != nil {
		t.Fatal(err)
	}
	ctx.AddFile(File{Path: "part.cicada", Before: []byte("old"), After: []byte("first")})
	ctx.AddFile(File{Path: "part.cicada", Before: []byte("first"), After: []byte("last")})
	ctx.Options.ParseProject = func(source []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
		if string(source) != "working" || len(files) != 1 || string(files["part.cicada"]) != "last" {
			t.Fatalf("host parser saw stale working bytes: %q %+v", source, files)
		}
		// Host-owned maps cannot mutate the staged candidate.
		files["part.cicada"][0] = 'X'
		return &notation.Score{}, nil, nil
	}
	working := sourceContext(ctx, []byte("working"))
	if _, _, err := working.ParseProject(); err != nil {
		t.Fatal(err)
	}
	if _, err := ctx.CurrentPlan(); err != nil {
		t.Fatal(err)
	}
	if compiler.calls != 2 || len(compiler.files) != 1 || string(compiler.files["part.cicada"]) != "last" {
		t.Fatalf("current plan did not refresh the cumulative inputs: calls=%d files=%+v", compiler.calls, compiler.files)
	}
	if _, err := working.CurrentPlan(); err != nil {
		t.Fatal(err)
	}
	if compiler.calls != 3 || string(compiler.files["part.cicada"]) != "last" {
		t.Fatal("intermediate source context lost staged files")
	}
}
