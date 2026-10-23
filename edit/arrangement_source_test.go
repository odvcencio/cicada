package edit_test

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestArrangementEditsAccumulateInTheirOwningFile(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		entry := []byte("cicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\n")
		part := []byte(strings.ReplaceAll("// preserve section comments\narrange {\n  place one bass pulse { at = @1.1.1 length = 4bars } // retain\n  place two bass pulse { at = @5.1.1 length = 4bars }\n  marker chorus { at = @5.1.1 }\n}\n", "\n", newline))
		files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "arrangement.cicada", Source: part}}
		compiler := multiFileCompiler{path: "main.cicada", files: files}
		result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{
			&edits.UpdatePlacement{Entity: "placement:one", Settings: &edits.PlacementSettings{At: "@2.1.1", Length: "3bars"}},
			&edits.CopyPlacement{Entity: "placement:one", Name: "copy", Settings: &edits.PlacementSettings{At: "@9.1.1"}},
			&edits.RemovePlacement{Entity: "placement:two"},
			&edits.AddPlacement{Name: "ending", Settings: &edits.PlacementSettings{At: "@13.1.1", Length: "1bar", Track: "bass", Content: "pulse"}},
			&edits.SetMarker{Entity: "marker:chorus", Settings: &edits.PlacementSettings{At: "@3.1.1"}},
			&edits.AddMarker{Name: "finish", Settings: &edits.PlacementSettings{At: "@14.1.1"}},
			&edits.RemoveMarker{Entity: "marker:chorus"},
		}}, edits.Options{Path: compiler.path, Edition: 2, Sources: files, Compiler: compiler, ParseProject: compiler.parse})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(result.Source, entry) || len(result.Files) != 1 || result.Files[0].Path != "arrangement.cicada" || !bytes.Equal(result.Files[0].Before, part) {
			t.Fatal("entry or original arrangement snapshot changed")
		}
		after := string(result.Files[0].After)
		for _, want := range []string{"// preserve section comments", "place one bass pulse { at = @2.1.1 length = 3bars } // retain", "place copy bass pulse { at = @9.1.1 length = 3bars }", "place ending bass pulse { at = @13.1.1 length = 1bar }", "marker finish { at = @14.1.1 }"} {
			if !strings.Contains(after, want) {
				t.Fatalf("missing %q: %s", want, after)
			}
		}
		if strings.Contains(after, "place two") || strings.Contains(after, "marker chorus") {
			t.Fatal("removed regions survived")
		}
		if newline == "\r\n" && strings.Contains(strings.ReplaceAll(after, newline, ""), "\n") {
			t.Fatal("line endings changed")
		}
		if len(result.Plan.Placements) != 3 {
			t.Fatal("batch did not compile all placement edits")
		}
	}
}

func TestArrangementEditsRequireOwnedSnapshot(t *testing.T) {
	entry := []byte("cicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\n")
	part := []byte("arrange { place one bass pulse { at = @1.1.1 length = 4bars } }\n")
	compiler := multiFileCompiler{path: "main.cicada", files: []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "foreign.cicada", Source: part}}}
	for _, in := range []edits.Intent{&edits.UpdatePlacement{Entity: "placement:one", Settings: &edits.PlacementSettings{At: "@2.1.1"}}, &edits.AddMarker{Name: "end", Settings: &edits.PlacementSettings{At: "@9.1.1"}}} {
		result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{in}}, edits.Options{Path: compiler.path, Edition: 2, Compiler: compiler, Sources: compiler.files[:1], ParseProject: compiler.parse})
		if err == nil || result != nil {
			t.Fatal("unowned arrangement yielded a candidate")
		}
	}
}
