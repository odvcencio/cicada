package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
