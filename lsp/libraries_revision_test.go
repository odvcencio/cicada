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

func TestIndependentScoreImportsSupportLocalRenameAndHover(t *testing.T) {
	for _, manifest := range []bool{true, false} {
		t.Run(fmt.Sprint(manifest), func(t *testing.T) {
			s, main, library := libraryServer(t)
			root := filepath.Dir(mustScorePath(t, main))
			if manifest {
				if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project score\ncicada 2\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(filepath.Join(root, "cicada.mod")); err != nil {
					t.Fatal(err)
				}
				s.documents[main] = append([]byte("cicada 2\n"), s.documents[main]...)
			}
			s.documents[main] = bytes.Replace(s.documents[main], []byte("lead = tone.melody"), []byte("lead = tone.melody lead.level=-3dB"), 1)
			at := utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte("lead")))
			edits := s.projectRename(main, at, "renamed")
			data, err := json.Marshal(edits)
			if err != nil || edits == nil || !bytes.Contains(data, []byte("renamed")) || bytes.Contains(data, []byte(library)) {
				t.Errorf("local rename bypassed import context or edited a library: %v %s", err, data)
			}
			at = utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte("lead.level"))+len("lead."))
			info := s.projectHover(main, at)
			data, err = json.Marshal(info)
			if err != nil || info == nil || !bytes.Contains(data, []byte("dB")) {
				t.Errorf("parameter hover lost imported score context: %v %s", err, data)
			}
		})
	}
}

func TestIndependentScoreImportsOfferNotationFix(t *testing.T) {
	for _, manifest := range []bool{true, false} {
		t.Run(fmt.Sprint(manifest), func(t *testing.T) {
			s, main, library := libraryServer(t)
			root := filepath.Dir(mustScorePath(t, main))
			if manifest {
				if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project score\ncicada 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(filepath.Join(root, "cicada.mod")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "lib/demo/tone/cicada.mod"), []byte("library demo/tone\ncicada 1\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			data := []byte("import \"demo/tone\"\nfx delay { feedback=0.2 }\ntrack lead tone.glass { send_a=0.4 }\nscene verse { lead=tone.melody }\nsong { verse }\n")
			if err := os.WriteFile(mustScorePath(t, main), data, 0600); err != nil {
				t.Fatal(err)
			}
			s.documents[main] = data
			sources, err := project.ReadSources(mustScorePath(t, main), nil)
			if err != nil {
				t.Fatal(err)
			}
			sum, _, err := sources.UpdateLibraries("")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0600); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(mustScorePath(t, library))
			if err != nil {
				t.Fatal(err)
			}
			s.canEditDocuments, s.canCreateFiles = true, true
			params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": main}, "context": map[string]any{"only": []string{"quickfix"}}})
			if err := s.handle(request{ID: json.RawMessage("1"), Method: "textDocument/codeAction", Params: params}); err != nil {
				t.Fatal(err)
			}
			response := s.out.(*bytes.Buffer).Bytes()
			if !bytes.Contains(response, []byte("Apply Cicada notation fixes")) || !bytes.Contains(response, []byte("documentChanges")) || bytes.Contains(response, []byte(library)) {
				t.Fatalf("import-aware notation fix missing or edits library: %s", response)
			}
			// A dirty imported buffer must not be adopted by a project fix.
			s.documents[library] = append(bytes.Clone(original), []byte("// changed after pinning\n")...)
			s.out.(*bytes.Buffer).Reset()
			if err := s.handle(request{ID: json.RawMessage("2"), Method: "textDocument/codeAction", Params: params}); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(s.out.(*bytes.Buffer).Bytes(), []byte("Apply Cicada notation fixes")) {
				t.Fatal("notation fix accepted a changed library pin")
			}
			got, err := os.ReadFile(mustScorePath(t, library))
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("code action changed library bytes: %v", err)
			}
			got, err = os.ReadFile(filepath.Join(root, "cicada.sum"))
			if err != nil || !bytes.Equal(got, sum) {
				t.Fatalf("code action changed library pins: %v", err)
			}
		})
	}
}

func TestIndependentNotationFixPreservesImportedReturnAlias(t *testing.T) {
	s, main, library := libraryServer(t)
	root := filepath.Dir(mustScorePath(t, main))
	data := []byte("import \"demo/tone\"\ntrack lead tone.glass { send_a=0.4 }\nscene verse { lead=tone.melody }\nsong { verse }\n")
	for path, content := range map[string][]byte{
		filepath.Join(root, "cicada.mod"):               []byte("project score\ncicada 1\n"),
		filepath.Join(root, "lib/demo/tone/cicada.mod"): []byte("library demo/tone\ncicada 1\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n"),
		mustScorePath(t, main):                          data,
	} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := os.ReadFile(mustScorePath(t, library))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mustScorePath(t, library), append(lib, []byte("fx echo delay {}\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := project.ReadSources(mustScorePath(t, main), nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0600); err != nil {
		t.Fatal(err)
	}
	s.documents[main] = data
	s.canEditDocuments = true
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": main}, "context": map[string]any{"only": []string{"quickfix"}}})
	if err := s.handle(request{ID: json.RawMessage("1"), Method: "textDocument/codeAction", Params: params}); err != nil {
		t.Fatal(err)
	}
	response := s.out.(*bytes.Buffer).Bytes()
	if !bytes.Contains(response, []byte("send tone.echo")) || bytes.Contains(response, []byte("send demo.tone.echo")) || bytes.Contains(response, []byte(library)) {
		t.Fatalf("imported-return fix missing, illegal or edits library: %s", response)
	}
}
