package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestPresetTargetAndParameterCompletion(t *testing.T) {
	for _, test := range []struct{ source, have, absent string }{
		{"instrument tone { param bite=0.4 voice mono { out=sine(pitch)*bite } }\npreset bright {\n instrument = t", "tone", "bite"},
		{"preset bright {\n instrument = acid\n cu", "cutoff", "bd_tune"},
		{"instrument tone { param bite=0.4 voice mono { out=sine(pitch)*bite } }\npreset bright {\n instrument = tone\n bi", "bite", "cutoff"},
		{"preset bright {\n instrument = builtin.bd\n de", "decay", "sd_decay"},
		{"preset bright {\n instrument = builtin.drive\n ga", "gain", "cutoff"},
	} {
		src := []byte(test.source)
		items, ok := presetCompletion(nil, src, utf16Position(src, len(src)))
		data, _ := json.Marshal(items)
		if !ok || !bytes.Contains(data, []byte(`"label":"`+test.have+`"`)) || bytes.Contains(data, []byte(`"label":"`+test.absent+`"`)) {
			t.Fatalf("completion for %s: %s", src, data)
		}
	}
}

func TestPresetImportedParameterCompletion(t *testing.T) {
	s, uri, _ := libraryServer(t)
	path := mustScorePath(t, uri)
	library := filepath.Join(filepath.Dir(path), "lib/demo/tone/tone.cicada")
	content := []byte("instrument glass { param bite=0.4 voice mono { out=sine(pitch)*bite } }\npreset soft { instrument=glass bite=0.3 }\n")
	if err := os.WriteFile(library, content, 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte("import \"demo/tone\"\npreset bright {\n instrument = tone.glass\n bi")
	s.documents[uri] = source
	// Completion reads source declarations even before an incomplete score can compile.
	files, err := project.ReadSources(path, map[string][]byte{path: source})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := presetCompletion(files, source, utf16Position(source, len(source)))
	data, _ := json.Marshal(items)
	if !ok || !bytes.Contains(data, []byte(`"label":"bite"`)) {
		t.Fatalf("imported params: %s", data)
	}
}

func TestEffectPresetDefinitionAndRename(t *testing.T) {
	source := []byte("cicada 2\nfx prototype delay { feedback=0.2 }\npreset wet { instrument=prototype feedback=0.3 }\nfx echo wet {}\ntrack lead acid { send echo=0.4 }\npattern melody { 1 . }\nscene main { lead=melody }\nsong { main }\n")
	for _, test := range []struct {
		word, newName  string
		definitionLine int
	}{{"prototype feedback", "base", 1}, {"wet {}", "long", 2}} {
		at := utf16Position(source, bytes.Index(source, []byte(test.word)))
		target, _ := json.Marshal(definition("file:///score.cicada", source, at))
		if !bytes.Contains(target, []byte(fmt.Sprintf(`"line":%d`, test.definitionLine))) {
			t.Fatalf("%s definition: %s", test.word, target)
		}
		edits, _ := json.Marshal(rename("file:///score.cicada", source, at, test.newName))
		if bytes.Count(edits, []byte(`"newText":"`+test.newName+`"`)) != 2 {
			t.Fatalf("%s rename: %s", test.word, edits)
		}
	}
}

func TestEffectPresetNavigationAcrossSourceFiles(t *testing.T) {
	s, main, part := multifileServer(t)
	s.documents[main] = []byte("preset wet { instrument=prototype feedback=0.3 }\nfx echo wet {}\ntrack bass acid { send echo=0.4 }\nscene verse { bass=pulse }\nsong { verse }\n")
	s.documents[part] = []byte("fx prototype delay { feedback=0.2 }\npattern pulse { 1 . }\n")
	for _, test := range []struct{ word, newName, uri string }{{"prototype feedback", "base", part}, {"wet {}", "long", main}} {
		at := utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte(test.word)))
		target, _ := json.Marshal(s.projectDefinition(main, at))
		if !bytes.Contains(target, []byte(test.uri)) {
			t.Fatalf("cross-file definition: %s", target)
		}
		edits, _ := json.Marshal(s.projectRename(main, at, test.newName))
		if bytes.Count(edits, []byte(`"newText":"`+test.newName+`"`)) != 2 {
			t.Fatalf("cross-file rename: %s", edits)
		}
	}
}
