package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectSettingsPreserveEditionCommentsAndPitchSpelling(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		before := []byte(strings.ReplaceAll("cicada 2\n// authored header\nkey db minor\ntempo // tempo comment\n138.0\ntrack bass acid {}\npattern p { 1 . c#3 . }\nscene main { bass=p }\nsong { main }\n", "\n", newline))
		updated, err := projectSettingsSource(before, &studioProjectSettings{Title: "東京 \"Studio\"", TempoMilli: 127125, Root: "c#", Scale: "minor"})
		if err != nil {
			t.Fatal(err)
		}
		p := patternProject(t, updated)
		if p.Title != "東京 \"Studio\"" || p.TempoMilli != 127125 || !bytes.HasPrefix(updated, []byte("cicada 2"+newline)) || !bytes.Contains(updated, []byte("key db minor")) || !bytes.Contains(updated, []byte("tempo // tempo comment"+newline+"127.125")) {
			t.Fatalf("project header changed incorrectly:\n%s", updated)
		}
		if !bytes.Contains(updated, []byte("pattern p { 1 . c#3 . }")) {
			t.Fatal("metadata rewrote musical source")
		}
		if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(updated, []byte(newline), nil), []byte("\n")) {
			t.Fatal("metadata changed line endings")
		}
	}
}

func TestProjectSettingsAreRevisionCheckedAndRejectInvalidRetuning(t *testing.T) {
	source := []byte("key a minor\ntrack bass acid {}\npattern p { 2 6 }\nscene main { bass=p }\nsong { main }\n")
	path := filepath.Join(t.TempDir(), "settings.cicada")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := &studioProjectSettings{Title: "Test", TempoMilli: 120000, Root: "c", Scale: "pent"}
	response := studioCall(t, handler, "/api/project", studioEdit{Revision: studioRevision(source), Metadata: settings})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid scale retuning: %d %s", response.Code, response.Body.String())
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(source, unchanged) {
		t.Fatal("failed retuning changed the score")
	}
	settings.Scale = "dorian"
	response = studioCall(t, handler, "/api/project", studioEdit{Revision: "old", Metadata: settings})
	if response.Code != http.StatusConflict {
		t.Fatal("stale project settings accepted")
	}
	response = studioCall(t, handler, "/api/project", studioEdit{Revision: studioRevision(source), Metadata: settings})
	if response.Code != 200 {
		t.Fatalf("settings: %d %s", response.Code, response.Body.String())
	}
}
