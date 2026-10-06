package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func multifileServer(t *testing.T) (*server, string, string) {
	t.Helper()
	root := t.TempDir()
	main := filepath.Join(root, "main.cicada")
	part := filepath.Join(root, "parts.cicada")
	for path, text := range map[string]string{
		filepath.Join(root, "cicada.mod"): "project score\ncicada 2\nentry \"main.cicada\"\nsource \"parts.cicada\"\n",
		main:                              "scene verse { bass = pulse bass.cutoff = 900Hz }\nsong { verse }\n",
		part:                              "track bass acid {}\npattern pulse { 1 . 5 . }\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(main)
	return &server{out: &bytes.Buffer{}, documents: map[string][]byte{fileURI(main): data}, versions: map[string]*int{}}, fileURI(main), fileURI(part)
}

func TestProjectDefinitionAndRenameAcrossFiles(t *testing.T) {
	s, main, part := multifileServer(t)
	for _, word := range []string{"pulse", "bass.cutoff"} {
		source := s.documents[main]
		at := utf16Position(source, bytes.Index(source, []byte(word)))
		encoded, _ := json.Marshal(s.projectDefinition(main, at))
		if !bytes.Contains(encoded, []byte(part)) {
			t.Fatalf("%s definition: %s", word, encoded)
		}
	}
	at := utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte("pulse")))
	encoded, _ := json.Marshal(s.projectRename(main, at, "hook"))
	if !bytes.Contains(encoded, []byte(part)) || bytes.Count(encoded, []byte(`"newText":"hook"`)) != 2 {
		t.Fatalf("rename: %s", encoded)
	}
	// Definitions use all unsaved buffers, including a peer file.
	s.documents[part] = []byte("// unsaved source\n\ntrack bass acid {}\npattern pulse { 5 . 1 . }\n")
	encoded, _ = json.Marshal(s.projectDefinition(main, at))
	if !bytes.Contains(encoded, []byte(`"line":3`)) {
		t.Fatalf("unsaved peer location: %s", encoded)
	}
	at = utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte("bass =")))
	encoded, _ = json.Marshal(s.projectRename(main, at, "low"))
	if bytes.Count(encoded, []byte(`"newText":"low"`)) != 3 {
		t.Fatalf("track and parameter path rename: %s", encoded)
	}
}

func TestProjectPublishRoutesDiagnosticsAndClearsPeers(t *testing.T) {
	s, main, part := multifileServer(t)
	s.documents[part] = []byte("track bass acid {}\npattern pulse { 1 . }\npattern pulse { 5 . }\n")
	if err := s.publish(main); err != nil {
		t.Fatal(err)
	}
	output := s.out.(*bytes.Buffer)
	if !bytes.Contains(output.Bytes(), []byte(part)) || !bytes.Contains(output.Bytes(), []byte("relatedInformation")) {
		t.Fatalf("duplicate locations: %s", output.Bytes())
	}
	output.Reset()
	s.documents[part] = []byte("track bass acid {}\npattern pulse { 1 . }\n")
	if err := s.publish(part); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"diagnostics":[]`)) || bytes.Contains(output.Bytes(), []byte("CICADA-REFERENCE")) {
		t.Fatalf("clearing package diagnostics: %s", output.Bytes())
	}
}

func TestProjectNoteHoverUsesEntryKey(t *testing.T) {
	s, main, part := multifileServer(t)
	s.documents[main] = append([]byte("key e minor\n"), s.documents[main]...)
	path, _ := scorePathFromURI(part)
	s.documents[part], _ = os.ReadFile(path)
	at := utf16Position(s.documents[part], bytes.Index(s.documents[part], []byte("1 .")))
	encoded, _ := json.Marshal(s.projectHover(part, at))
	if !bytes.Contains(encoded, []byte("E")) || !bytes.Contains(encoded, []byte("E minor")) {
		t.Fatalf("project key hover: %s", encoded)
	}
	s.documents[main] = bytes.Replace(s.documents[main], []byte("key e minor"), []byte("key d major"), 1)
	encoded, _ = json.Marshal(s.projectHover(part, at))
	if !bytes.Contains(encoded, []byte("D major")) {
		t.Fatalf("unsaved key hover: %s", encoded)
	}
}

func TestProjectRenameFromParameterPath(t *testing.T) {
	s, main, part := multifileServer(t)
	at := utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte("bass.cutoff"))+len("bass."))
	encoded, _ := json.Marshal(s.projectRename(main, at, "low"))
	if !bytes.Contains(encoded, []byte(part)) || bytes.Count(encoded, []byte(`"newText":"low"`)) != 3 {
		t.Fatalf("path rename: %s", encoded)
	}
}

func TestProjectLastCloseClearsPeerDiagnostics(t *testing.T) {
	s, main, part := multifileServer(t)
	delete(s.documents, main)
	s.documents[part] = []byte("track other acid {}\npattern pulse { 1 . }\n")
	if err := s.publish(part); err != nil {
		t.Fatal(err)
	}
	out := s.out.(*bytes.Buffer)
	if !bytes.Contains(out.Bytes(), []byte("unknown track bass")) {
		t.Fatalf("missing peer error: %s", out.Bytes())
	}
	out.Reset()
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]string{"uri": part}})
	if err := s.handle(request{Method: "textDocument/didClose", Params: params}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(main)) || bytes.Contains(out.Bytes(), []byte("CICADA-REFERENCE")) {
		t.Fatalf("stale peer diagnostics: %s", out.Bytes())
	}
}
