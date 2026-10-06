package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func libraryServer(t *testing.T) (*server, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	main := filepath.Join(root, "main.cicada")
	library := filepath.Join(root, "lib/demo/tone/tone.cicada")
	for filename, text := range map[string]string{
		filepath.Join(root, "cicada.mod"): "project score\ncicada 2\nentry \"main.cicada\"\n",
		main:                              "import \"demo/tone\"\ntrack lead tone.glass {}\nscene verse { lead = tone.melody }\nsong { verse }\n",
		filepath.Join(root, "lib/demo/tone/cicada.mod"): "library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n",
		library: "instrument glass { voice mono { out = sine(pitch) } }\nphrase _hidden { 1 . }\npattern melody { use _hidden }\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := project.ReadSources(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0644); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(main)
	return &server{out: &bytes.Buffer{}, documents: map[string][]byte{fileURI(main): data}, versions: map[string]*int{}}, fileURI(main), fileURI(library)
}

func TestLibraryDefinitionsAndCompletions(t *testing.T) {
	s, main, library := libraryServer(t)
	for _, word := range []string{"tone.glass", "tone.melody"} {
		at := utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte(word))+len("tone."))
		encoded, _ := json.Marshal(s.projectDefinition(main, at))
		if !bytes.Contains(encoded, []byte(library)) {
			t.Fatalf("%s definition: %s", word, encoded)
		}
	}
	at := utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte("tone.glass"))+len("tone."))
	encoded, _ := json.Marshal(s.projectCompletion(main, at))
	if !bytes.Contains(encoded, []byte("tone.glass")) || !bytes.Contains(encoded, []byte("tone.melody")) || bytes.Contains(encoded, []byte("_hidden")) {
		t.Fatalf("qualified completion: %s", encoded)
	}
	if s.projectRename(main, at, "other") != nil {
		t.Fatal("imported reference was renamed")
	}
	// An unsaved library buffer retains real source locations and emits a hash
	// diagnostic at its importing score instead of silently adopting new bytes.
	s.documents[library] = []byte("// unsaved library\n\ninstrument glass { voice mono { out = sine(pitch) } }\npattern melody { 1 . }\n")
	encoded, _ = json.Marshal(s.projectDefinition(main, at))
	if !bytes.Contains(encoded, []byte(`"line":2`)) {
		t.Fatalf("unsaved library definition: %s", encoded)
	}
	_, _, ds := s.parseProjectDocument(main)
	found := false
	for _, d := range ds {
		found = found || d.Code == "CICADA-LIB-HASH"
	}
	if !found {
		t.Fatalf("unsaved hash diagnostic: %+v", ds)
	}
}

func TestImportPathCompletion(t *testing.T) {
	s, main, _ := libraryServer(t)
	s.documents[main] = append(s.documents[main], []byte("import \"demo/to")...)
	at := utf16Position(s.documents[main], len(s.documents[main]))
	encoded, _ := json.Marshal(s.projectCompletion(main, at))
	if !bytes.Contains(encoded, []byte("demo/tone")) || !bytes.Contains(encoded, []byte("textEdit")) {
		t.Fatalf("path completion: %s", encoded)
	}
}

func TestLibraryPrivateDefinitionStaysInItsLibrary(t *testing.T) {
	s, _, library := libraryServer(t)
	data, err := os.ReadFile(mustScorePath(t, library))
	if err != nil {
		t.Fatal(err)
	}
	s.documents[library] = data
	at := utf16Position(data, bytes.LastIndex(data, []byte("_hidden")))
	encoded, _ := json.Marshal(s.projectDefinition(library, at))
	if !bytes.Contains(encoded, []byte(library)) || !bytes.Contains(encoded, []byte(`"line":1`)) {
		t.Fatalf("private definition: %s", encoded)
	}
}
func mustScorePath(t *testing.T, uri string) string {
	t.Helper()
	filename, ok := scorePathFromURI(uri)
	if !ok {
		t.Fatal("invalid score URI")
	}
	return filename
}

func TestLibraryImportCompletionUsesProjectLibraryRoot(t *testing.T) {
	s, _, library := libraryServer(t)
	data, err := os.ReadFile(mustScorePath(t, library))
	if err != nil {
		t.Fatal(err)
	}
	s.documents[library] = append(data, []byte("\nimport \"demo/to")...)
	at := utf16Position(s.documents[library], len(s.documents[library]))
	encoded, _ := json.Marshal(s.projectCompletion(library, at))
	if !bytes.Contains(encoded, []byte("demo/tone")) {
		t.Fatalf("library import paths: %s", encoded)
	}
}

func TestStandardLibraryDefinitionsHaveReadableSources(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s, main, _ := libraryServer(t)
	for _, test := range []struct{ library, binding, reference string }{
		{"std/synth", "track lead synth.glassbass {}", "synth.glassbass"},
		{"std/drums", "track lead drums.steel {}", "drums.steel"},
		{"std/presets", "track lead presets.acid-round {}", "presets.acid-round"},
		{"std/fx", "track lead acid { send fx.delay=0.4 }", "fx.delay"},
	} {
		t.Run(test.library, func(t *testing.T) {
			source := []byte("import \"" + test.library + "\"\n" + test.binding + "\npattern melody { 1 . }\nscene main { lead=melody }\nsong { main }\n")
			s.documents[main] = source
			at := utf16Position(source, bytes.Index(source, []byte(test.reference)))
			target := s.projectDefinition(main, at)
			if target == nil {
				t.Fatal("missing std definition")
			}
			uri := target.(map[string]any)["uri"].(string)
			filename, ok := scorePathFromURI(uri)
			if !ok {
				t.Fatalf("invalid URI: %s", uri)
			}
			data, err := os.ReadFile(filename)
			if err != nil || !bytes.Contains(data, []byte(strings.Split(test.reference, ".")[1])) {
				t.Fatalf("unreadable std definition: %v %s", err, data)
			}
			// A damaged cache entry must be repaired from embedded bytes.
			if err := os.WriteFile(filename, []byte("damaged cache"), 0600); err != nil {
				t.Fatal(err)
			}
			if again := s.projectDefinition(main, at); again == nil {
				t.Fatal("cache repair lost definition")
			}
			repaired, err := os.ReadFile(filename)
			if err != nil || !bytes.Equal(repaired, data) {
				t.Fatalf("cache repair: %v", err)
			}
		})
	}
}

func TestStandardLibraryDefinitionDidOpenUsesLibraryContext(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s, main, _ := libraryServer(t)
	source := []byte("import \"std/synth\"\ntrack bass synth.glassbass {}\npattern riff { 1 . 5 . }\nscene main { bass = riff }\nsong { main }\n")
	path := mustScorePath(t, main)
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	s.documents[main] = source
	sources, err := project.ReadSources(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sources.Root, "cicada.sum"), sum, 0600); err != nil {
		t.Fatal(err)
	}
	at := utf16Position(source, bytes.Index(source, []byte("synth.glassbass")))
	target := s.projectDefinition(main, at).(map[string]any)
	uri := target["uri"].(string)
	data, err := os.ReadFile(mustScorePath(t, uri))
	if err != nil {
		t.Fatal(err)
	}
	// The cached definition retains context even after its score is closed.
	delete(s.documents, main)
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri, "text": string(data)}})
	if err := s.handle(request{Method: "textDocument/didOpen", Params: params}); err != nil {
		t.Fatal(err)
	}
	_, score, ds := s.parseProjectDocument(uri)
	if score == nil || hasErrors(ds) {
		t.Fatalf("opened std definition: %+v", ds)
	}
	if compiled, ds := project.FromScore(score); compiled == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	if bytes.Contains(s.out.(*bytes.Buffer).Bytes(), []byte("CICADA-LIMIT")) {
		t.Fatal("published false score limits")
	}
}
