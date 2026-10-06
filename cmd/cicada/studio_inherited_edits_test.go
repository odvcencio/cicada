package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inheritedEditStudio(t *testing.T, source, newline string) (string, http.Handler, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	before := []byte(strings.ReplaceAll(source, "\n", newline))
	for name, data := range map[string][]byte{"main.cicada": before, "cicada.mod": []byte("project edits\ncicada 2\n")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, handler, before
}

func checkInheritedEdit(t *testing.T, path string, handler http.Handler, before []byte, header, newline string) []byte {
	t.Helper()
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(updated, before) || !bytes.HasPrefix(updated, []byte(strings.ReplaceAll(header+"// authored header\n", "\n", newline))) || !bytes.Contains(updated, []byte("track input audio {} // retained audio track"+newline)) {
		t.Fatalf("edit lost authored source or made no change:\n%s", updated)
	}
	if header == "" && bytes.Contains(updated, []byte("cicada 2")) {
		t.Fatal("edit persisted a temporary edition header")
	}
	if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(updated, []byte(newline), nil), []byte("\n")) {
		t.Fatal("edit changed CRLF conventions")
	}
	if _, err := compileStudioSource(path, updated); err != nil {
		t.Fatal(err)
	}
	if undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(updated)}); undo.Code != http.StatusOK {
		t.Fatalf("Undo: %d %s", undo.Code, undo.Body.String())
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, before) {
		t.Fatalf("Undo did not restore exact source: %v", err)
	}
	return updated
}

func TestPatternEditsKeepInheritedAudioEditionSource(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, header := range []string{"", "cicada 2\n"} {
			for _, action := range []string{"step", "pitch", "toggle", "duplicate", "range", "resize"} {
				t.Run(fmt.Sprintf("%q/%q/%s", newline, header, action), func(t *testing.T) {
					source := header + "// authored header\n" + strings.Replace(studioPatternScore, "track bass acid {}", "track bass acid {}\ntrack input audio {} // retained audio track", 1)
					source = strings.Replace(source, "scene main {", "scene main { input=off", 1)
					path, handler, before := inheritedEditStudio(t, source, newline)
					pitch := 72
					edit := studioEdit{Revision: studioRevision(before), Action: action, Pattern: "p", Step: 1, Pitch: &pitch, NewName: "variation", Length: 16,
						NoteEdit: &studioStepEdit{Mode: "note", Pitch: pitch, Ratchet: 1, Chance: 100},
						Range:    &studioPatternRange{Operation: "clear", First: 0, Last: 1},
					}
					response := studioCall(t, handler, "/api/pattern", edit)
					if response.Code != http.StatusOK {
						t.Fatalf("inherited edition %s: %d %s", action, response.Code, response.Body.String())
					}
					updated := checkInheritedEdit(t, path, handler, before, header, newline)
					if !bytes.Contains(updated, []byte("phrase hook { 1^ . 5~*2?70 - }"+newline)) {
						t.Fatal("edit changed the shared phrase")
					}
				})
			}
		}
	}
}

func TestProjectSettingsKeepInheritedAudioEditionSource(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		path, handler, before := inheritedEditStudio(t, "// authored header\nkey db minor\ntrack bass acid {}\ntrack input audio {} // retained audio track\npattern p { 1 . c#3 . }\nscene main { bass=p input=off }\nsong { main }\n", newline)
		settings := &studioProjectSettings{Title: "Updated", TempoMilli: 127125, Root: "c#", Scale: "minor"}
		response := studioCall(t, handler, "/api/project", studioEdit{Revision: studioRevision(before), Metadata: settings})
		if response.Code != http.StatusOK {
			t.Fatalf("inherited edition settings: %d %s", response.Code, response.Body.String())
		}
		updated := checkInheritedEdit(t, path, handler, before, "", newline)
		p, err := compileStudioSource(path, updated)
		if err != nil {
			t.Fatal(err)
		}
		if p.Title != settings.Title || p.TempoMilli != settings.TempoMilli || !bytes.Contains(updated, []byte("key db minor"+newline)) || !bytes.Contains(updated, []byte("pattern p { 1 . c#3 . }"+newline)) {
			t.Fatalf("settings changed incorrectly:\n%s", updated)
		}
	}
}
