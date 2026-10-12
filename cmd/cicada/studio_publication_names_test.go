package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/recording"
)

func TestPublishRecordedAvoidsAuthoredPresetNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, newline := range []string{"\n", "\r\n"} {
		for _, location := range []string{"entry", "part"} {
			t.Run(map[string]string{"\n": "lf", "\r\n": "crlf"}[newline]+"/"+location, func(t *testing.T) {
				preset := []byte("preset captured { instrument=acid cutoff=900Hz } // preserve this preset" + newline)
				source := []byte(strings.ReplaceAll("cicada 2\n"+studioScore, "\n", newline))
				if location == "entry" {
					source = append(source, preset...)
				}
				path := studioTestPath(t, string(source))
				partPath := filepath.Join(filepath.Dir(path), "part.cicada")
				if location == "part" {
					if err := os.WriteFile(partPath, preset, 0600); err != nil {
						t.Fatal(err)
					}
					manifest := "project recording\ncicada 2\nentry \"" + filepath.Base(path) + "\"\nsource \"part.cicada\"\n"
					if err := os.WriteFile(filepath.Join(filepath.Dir(path), "cicada.mod"), []byte(manifest), 0600); err != nil {
						t.Fatal(err)
					}
				}
				s, err := newStudio(path)
				if err != nil {
					t.Fatal(err)
				}
				defer s.shutdown()
				pack, err := recording.Build("captured", []recording.Hit{{Rate: 48000, Root: 60, Peak: .5, SourceSHA256: strings.Repeat("a", 64), PCM: []float32{0, .25, .5, .25, 0}}}, 1)
				if err != nil {
					t.Fatal(err)
				}
				response := httptest.NewRecorder()
				s.publishRecorded(response, pack, 60, studioEdit{Revision: studioRevision(source), Scene: "main"}, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("publication must succeed with a suffixed name: %d %s", response.Code, response.Body.String())
				}
				var reply map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
					t.Fatal(err)
				}
				if reply["instrument"] != "captured_2" || reply["track"] != "captured_2_track" {
					t.Fatalf("wrong allocated name: %v", reply)
				}
				if location == "part" {
					if got, err := os.ReadFile(partPath); err != nil || !bytes.Equal(got, preset) {
						t.Fatalf("foreign preset changed: %q %v", got, err)
					}
				} else if got, err := os.ReadFile(path); err != nil || !bytes.Contains(got, preset) {
					t.Fatalf("authored preset changed: %q %v", got, err)
				}
				assertM5IntentLog(t, path, "publishrecorded", "local", s.sessionID)
			})
		}
	}
}
