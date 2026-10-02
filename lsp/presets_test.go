package lsp

import (
	"bytes"
	"encoding/json"
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
