package edit_test

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestSceneEditsAccumulateInTheirOwningFile(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		entry := []byte("cicada 2" + newline + "track bass acid {}" + newline + "song { main other }" + newline)
		part := []byte(strings.ReplaceAll("// preserve arrangement\npattern pulse acid { 1 . 5 . }\npattern variation acid { 3 . 7 . }\nscene main { bass=pulse }\nscene other { bass=variation }\n", "\n", newline))
		files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "parts.cicada", Source: part}}
		compiler := multiFileCompiler{path: "main.cicada", files: files}
		result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{
			&edits.BindScene{Scene: "main", Track: "bass", Pattern: "variation"},
			&edits.BindScene{Scene: "other", Track: "bass", Pattern: "keep"},
			&edits.CreatePattern{Name: "idea", Track: "bass", Steps: 8, Scene: "other", Starter: "pulse"},
		}}, edits.Options{Path: compiler.path, Sources: files, Compiler: compiler, Edition: 2, ParseProject: compiler.parse})
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Replace(part, []byte("bass=pulse"), []byte("bass=variation"), 1)
		want = bytes.Replace(want, []byte("scene other { bass=variation }"), []byte("scene other { bass=idea }"), 1)
		if len(result.Files) != 1 || !bytes.Equal(result.Files[0].Before, part) || !bytes.Equal(result.Files[0].After, want) {
			t.Fatal("batch did not retain the original scene snapshot and cumulative edits")
		}
		if !bytes.HasPrefix(result.Source, entry) || !bytes.Contains(result.Source, []byte("pattern idea acid steps=8")) || result.Plan.Scenes[0].Bindings["bass"] != "variation" || result.Plan.Scenes[1].Bindings["bass"] != "idea" {
			t.Fatal("new part or scene binding was lost from the compiled project")
		}
	}
}

func TestSceneEditsRequireAnOwnedSourceSnapshot(t *testing.T) {
	entry := []byte("cicada 2\ntrack bass acid {}\nsong { main }\n")
	part := []byte("pattern pulse acid { 1 . 5 . }\nscene main { bass=pulse }\n")
	compiler := multiFileCompiler{path: "main.cicada", files: []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "library.cicada", Source: part}}}
	for _, in := range []edits.Intent{&edits.BindScene{Scene: "main", Track: "bass", Pattern: "off"}, &edits.CreatePattern{Name: "idea", Track: "bass", Steps: 8, Scene: "main"}} {
		result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{in}}, edits.Options{Path: "main.cicada", Compiler: compiler, Sources: compiler.files[:1], ParseProject: compiler.parse})
		if err == nil || result != nil {
			t.Fatal("an unowned declaration yielded a source mutation")
		}
	}
}
