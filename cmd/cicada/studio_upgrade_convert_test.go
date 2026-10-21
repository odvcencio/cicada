package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

const batchManifest1 = "project batch\ncicada 1\n"

func upgradeBatch(intents ...edits.Intent) []edits.Intent {
	upgrade := &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`), ConfirmUpgrade: true}
	return append([]edits.Intent{upgrade}, intents...)
}

func writeEditionOneProject(t *testing.T) (path, manifest string) {
	t.Helper()
	dir := t.TempDir()
	path, manifest = filepath.Join(dir, "main.cicada"), filepath.Join(dir, "cicada.mod")
	if err := os.WriteFile(path, []byte(convertSongScore), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(batchManifest1), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, manifest
}

// A confirmed edition upgrade stages a new cicada.mod. The render check must
// render that staged state, not the edition-1 manifest still on disk.
func TestUpgradeThenConvertAndSplitInOneEnvelope(t *testing.T) {
	isolateState(t)
	for _, c := range []struct {
		name    string
		intents []edits.Intent
		want    []string
	}{
		{"convert", upgradeBatch(&edits.ConvertSongToArrange{}), []string{"place main-1-bass bass pulse {", "length = 4bars"}},
		{"convert and split", upgradeBatch(&edits.ConvertSongToArrange{}, &edits.SplitClip{Entity: "placement:main-1-bass", AtTick: 7680}), []string{"place main-1-bass bass pulse {", "place main-1-bass-2 bass pulse {", "at = @3.1.1"}},
	} {
		check := func(t *testing.T, source, manifest []byte) {
			t.Helper()
			if string(manifest) != "project batch\ncicada 2\n" || bytes.Contains(source, []byte("song {")) {
				t.Fatalf("manifest %q source:\n%s", manifest, source)
			}
			for _, want := range c.want {
				if !strings.Contains(string(source), want) {
					t.Fatalf("missing %q in:\n%s", want, source)
				}
			}
		}
		t.Run("apply/"+c.name, func(t *testing.T) {
			path, manifest := writeEditionOneProject(t)
			file := filepath.Join(filepath.Dir(path), "intents.json")
			writeApplyEnvelope(t, file, edits.Envelope{Version: 1, Intents: c.intents})
			if err := applyCommandWithHooks([]string{path, file, "--write"}, io.Discard, nil); err != nil {
				t.Fatal(err)
			}
			source, _ := os.ReadFile(path)
			upgraded, _ := os.ReadFile(manifest)
			check(t, source, upgraded)
		})
		t.Run("studio/"+c.name, func(t *testing.T) {
			path, manifest := writeEditionOneProject(t)
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.shutdown() })
			handler := s.domainRoutes()
			env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(convertSongScore)), Author: "tester", Intents: c.intents}
			if r := studioCall(t, handler, "/api/intents", env); r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			source, _ := os.ReadFile(path)
			upgraded, _ := os.ReadFile(manifest)
			check(t, source, upgraded)
			if history := studioHistoryEdits(t, handler); len(history) != 1 {
				t.Fatalf("history entries: %d", len(history))
			}
			if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(source)}); r.Code != 200 {
				t.Fatalf("undo: %d %s", r.Code, r.Body.String())
			}
			restored, _ := os.ReadFile(path)
			restoredManifest, _ := os.ReadFile(manifest)
			if string(restored) != convertSongScore || string(restoredManifest) != batchManifest1 {
				t.Fatalf("one undo did not restore both files:\n%s\n%s", restored, restoredManifest)
			}
		})
		t.Run("apply-service/"+c.name, func(t *testing.T) {
			path, manifest := writeEditionOneProject(t)
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.shutdown() })
			opts, err := s.editOptions([]byte(convertSongScore), nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := edits.Apply([]byte(convertSongScore), edits.Envelope{Version: 1, Intents: c.intents}, opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Files) != 1 || result.Files[0].Path != manifest {
				t.Fatalf("files: %+v", result.Files)
			}
			check(t, result.Source, result.Files[0].After)
			if disk, _ := os.ReadFile(manifest); string(disk) != batchManifest1 {
				t.Fatal("Apply wrote the manifest")
			}
		})
	}
}
