package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryDeclarationRenameDoesNotChangeProjectNames(t *testing.T) {
	s, main, library := libraryServer(t)
	source := bytes.Replace(s.documents[main], []byte("tone.glass"), []byte("glass"), 1)
	source = append([]byte("instrument glass { voice mono { out=sine(pitch) } }\n"), source...)
	s.documents[main] = source
	data, err := os.ReadFile(mustScorePath(t, library))
	if err != nil {
		t.Fatal(err)
	}
	s.documents[library] = data
	at := utf16Position(data, bytes.Index(data, []byte("glass")))
	if edits := s.projectRename(library, at, "renamed"); edits != nil {
		t.Fatalf("library rename edited project: %+v", edits)
	}
}

func TestProjectRenameRejectsImportAliasCollision(t *testing.T) {
	s, main, _ := libraryServer(t)
	// Exercise both the single-source and explicit multi-source manifest routes.
	for _, multifile := range []bool{false, true} {
		if multifile {
			root := filepath.Dir(mustScorePath(t, main))
			if err := os.WriteFile(filepath.Join(root, "parts.cicada"), []byte("// peer\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project score\ncicada 2\nentry \"main.cicada\"\nsource \"parts.cicada\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		at := utf16Position(s.documents[main], bytes.Index(s.documents[main], []byte("lead")))
		if edits := s.projectRename(main, at, "tone"); edits != nil {
			t.Fatalf("rename accepted alias collision: %+v", edits)
		}
	}
}
