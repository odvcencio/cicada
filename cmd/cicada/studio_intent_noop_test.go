package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/project"
)

type noopFile struct {
	data    string
	mode    fs.FileMode
	modTime time.Time
}

func noopFiles(t *testing.T, roots ...string) map[string]noopFile {
	t.Helper()
	files := map[string]noopFile{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err == nil {
				files[path] = noopFile{string(data), info.Mode(), info.ModTime()}
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// These expectations preserve the old handlers' complete no-op responses,
// including the mixer receipt. Grid toggles and declaration creation have no
// successful unchanged operation; their refusals also must leave no effects.
func TestIntentRoutesKeepNoopResponsesAndEffects(t *testing.T) {
	library := t.TempDir()
	t.Setenv("CICADA_LIBRARY", library)
	voice := string(instrumentParameterFixture(t))
	voiceSet := strings.Replace(voice, "track keys my-pad {}", "track keys my-pad { brightness = 0.5Hz }", 1)
	clip, _ := m4ClipSource()
	recorded := strings.Replace(studioScore, "{ 1 . 5 . }", "{ c4 . 5 . }", 1)
	projectSource := "tempo 130\nkey a minor\n" + studioScore
	patternSettings := strings.Replace(studioScore, "steps=4 { 1", "steps=4 swing=50% gate=50% transpose=0 { 1", 1)
	for _, c := range []struct {
		name, route, source string
		body                studioEdit
		prime               bool
		error               string
	}{
		{"instrument set", "/api/instrument", voiceSet, studioEdit{Action: "set-parameter", Track: "keys", NewName: "brightness", Value: json.RawMessage(`0.5`)}, false, ""},
		{"instrument reset absent", "/api/instrument", voice, studioEdit{Action: "reset-parameter", Track: "keys", NewName: "brightness"}, false, ""},
		{"instrument duplicate", "/api/instrument", presetEditScore, studioEdit{Action: "add-preset", Pattern: "warm-pad", NewName: "pad", Track: "bass"}, false, "track bass already exists"},
		{"mixer set", "/api/mixer", studioMixerScore, studioEdit{Path: "bass.level", Value: json.RawMessage(`-6`)}, false, ""},
		{"grid modifier refusal", "/api/toggle", studioScore, studioEdit{Pattern: "pulse", Step: 1, Modifier: "accent"}, false, "step 2 needs a note before setting accent"},
		{"pattern settings", "/api/pattern", patternSettings, studioEdit{Action: "settings", Pattern: "pulse", Settings: &studioPatternSettings{Swing100: 5000, Gate: 50}}, false, ""},
		{"pattern step", "/api/pattern", recorded, studioEdit{Action: "step", Pattern: "pulse", NoteEdit: &studioStepEdit{Mode: "note", Pitch: 60, Ratchet: 1, Chance: 100}}, false, ""},
		{"pattern rest", "/api/pattern", studioScore, studioEdit{Action: "step", Pattern: "pulse", Step: 1, NoteEdit: &studioStepEdit{Mode: "rest", Ratchet: 1, Chance: 100}}, false, ""},
		{"pattern range", "/api/pattern", studioScore, studioEdit{Action: "range", Pattern: "pulse", Range: &studioPatternRange{Operation: "clear", First: 1, Last: 1}}, false, ""},
		{"pattern resize", "/api/pattern", studioScore, studioEdit{Action: "resize", Pattern: "pulse", Length: 4}, false, ""},
		{"pattern bind", "/api/pattern", studioScore, studioEdit{Action: "bind", Pattern: "pulse", Track: "bass", Scene: "main"}, false, ""},
		{"song move", "/api/song", studioSongScore, studioEdit{Action: "move", Index: 1, Target: 1}, false, ""},
		{"song bars", "/api/song", studioSongScore, studioEdit{Action: "bars", Index: 1, Bars: 1}, false, ""},
		{"song scene", "/api/song", studioSongScore, studioEdit{Action: "scene", Scene: "dusk"}, false, ""},
		{"automation set", "/api/automation", strings.Replace(automationScore, "900.0Hz", "900Hz", 1), studioEdit{Action: "automation-set", Scene: "main", Path: "bass.cutoff", Value: json.RawMessage(`900`)}, false, ""},
		{"automation remove absent", "/api/automation", automationScore, studioEdit{Action: "automation-remove", Scene: "hold", Path: "bass.pan"}, false, "scene hold has no point for bass.pan"},
		{"project settings", "/api/project", projectSource, studioEdit{Metadata: &studioProjectSettings{Title: "Studio", TempoMilli: 130000, Root: "a", Scale: "minor"}}, false, ""},
		{"clip settings", "/api/clip", clip, studioEdit{Action: "clip-settings", Pattern: "hit", ClipSettings: &studioClipSettings{Start: 480, End: 4800, GainDB: -2}}, false, ""},
		{"clip bind", "/api/clip", clip, studioEdit{Action: "clip-bind", Pattern: "hit", Track: "vox", Scene: "main"}, false, ""},
		{"clip duplicate track", "/api/clip", clip, studioEdit{Action: "audio-track", NewName: "vox"}, false, "track vox already exists"},
		{"source replace", "/api/source", studioScore, studioEdit{Source: studioScore}, false, ""},
		{"file replace", "/api/files", studioScore, studioEdit{File: "score.cicada", Source: studioScore}, false, ""},
		{"file part replace", "/api/files", "pattern riff { c3 . }\n", studioEdit{File: "part.cicada", Source: "pattern riff { c3 . }\n"}, false, ""},
		{"library insert instrument", "/api/library/insert", studioLibraryScore, studioEdit{Path: "std/synth", Item: "glassbass", Track: "lead"}, true, ""},
		{"library insert effect", "/api/library/insert", studioLibraryScore, studioEdit{Path: "std/fx", Item: "delay", Track: "lead"}, true, ""},
		{"library insert preset", "/api/library/insert", studioLibraryScore, studioEdit{Path: "std/presets", Item: "acid-bite", Track: "lead"}, true, ""},
		{"library save duplicate", "/api/library/save", studioLibraryScore, studioEdit{Track: "lead", Name: "bright"}, true, "preset name already exists"},
		{"record notes", "/api/record", recorded, studioEdit{Track: "bass", Pattern: "pulse", Take: []studioTakeNote{{Note: 60, Velocity: 90, EndTick: 60}}}, false, ""},
		{"record drums", "/api/record", studioScore, studioEdit{Track: "drums", Pattern: "beat", Take: []studioTakeNote{{Note: 36, Velocity: 127, EndTick: 60}}}, true, ""},
	} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(c.name+"/"+map[string]string{"\n": "lf", "\r\n": "crlf"}[newline], func(t *testing.T) {
				fixture := strings.ReplaceAll(c.source, "\n", newline)
				s, path := m4Session(t, m4RouteCase{source: fixture, part: c.name == "file part replace"})
				body := c.body
				body.Source = strings.ReplaceAll(body.Source, "\n", newline)
				body.Revision = studioRevision([]byte(fixture))
				if c.prime {
					response := studioCall(t, s.routes(), c.route, body)
					if response.Code != http.StatusOK {
						t.Fatalf("setup: %d %s", response.Code, response.Body.String())
					}
				}
				current, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				body.Revision = studioRevision(current)
				if c.name == "file part replace" {
					// Initialize the per-file session before comparing its history.
					response := studioCall(t, s.routes(), c.route, body)
					if response.Code != http.StatusOK {
						t.Fatalf("file session setup: %d %s", response.Code, response.Body.String())
					}
				}
				target := s
				if path != s.path {
					target = s.fileSessions[path]
				}
				history := target.history.historySnapshot()
				pending := target.history.pendingTakeRevision
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				roots := []string{filepath.Dir(path), os.Getenv("XDG_CONFIG_HOME"), library}
				files := noopFiles(t, roots...)
				wantStatus := http.StatusOK
				want := map[string]any{"revision": body.Revision, "valid": true, "source": string(current), "unchanged": true, "playingRevision": body.Revision}
				if c.error != "" {
					wantStatus = http.StatusUnprocessableEntity
					want = map[string]any{"error": c.error}
				} else if c.route == "/api/mixer" {
					line := strings.Count(string(current[:bytes.Index(current, []byte("level = -6dB"))]), "\n") + 1
					want["path"], want["value"], want["previous"] = "bass.level", "-6 dB", "-6 dB"
					want["changedRange"] = mixerLineRange{Start: line, End: line}
				}
				wantJSON, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				response := studioCall(t, s.routes(), c.route, body)
				if response.Code != wantStatus || !bytes.Equal(bytes.TrimSpace(response.Body.Bytes()), wantJSON) {
					t.Fatalf("legacy no-op response\n got: %d %s\nwant: %d %s", response.Code, response.Body.String(), wantStatus, wantJSON)
				}
				afterInfo, err := os.Stat(path)
				if err != nil || !os.SameFile(info, afterInfo) {
					t.Fatalf("no-op exchanged the source: %v", err)
				}
				if !reflect.DeepEqual(files, noopFiles(t, roots...)) {
					t.Fatal("no-op wrote a score, pins, edit log, recovery file, snapshot, or journal")
				}
				if !reflect.DeepEqual(history, target.history.historySnapshot()) || pending != target.history.pendingTakeRevision {
					t.Fatal("no-op changed history or armed the recording commit hook")
				}
				if target.takes != nil || target.recordedModeled != "" || target.recordedSampled != "" || target.recordingPreview != "" {
					t.Fatal("no-op changed take or recording state")
				}
			})
		}
	}
}

func TestRecordRouteRestoresCommitHookAfterConflict(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, path, s := studioTestStudio(t)
	defer s.shutdown()
	s.history.expectRecordedTake("previous-recording")
	dir, err := studioRecoveryDir(path)
	if err != nil {
		t.Fatal(err)
	}
	external := []byte(studioScore + "// concurrent edit\n")
	raced := make(chan error, 1)
	original := studioCompile
	defer func() { studioCompile = original }()
	armed := false
	studioCompile = func(filename string, source []byte, overrides map[string][]byte) (*project.Project, error) {
		p, err := original(filename, source, overrides)
		if err == nil && !armed && !bytes.Equal(source, []byte(studioScore)) {
			armed = true
			// Hold the history lock so the route cannot arm its beforeSwap
			// hook until an external writer has changed the staged revision.
			s.history.mu.Lock()
			go func() {
				defer s.history.mu.Unlock()
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					stages, err := filepath.Glob(filepath.Join(dir, studioRecoveryPattern(path)))
					if err != nil {
						raced <- err
						return
					}
					if len(stages) > 0 {
						raced <- os.WriteFile(path, external, 0600)
						return
					}
					time.Sleep(time.Millisecond)
				}
				raced <- fmt.Errorf("recording never staged a source write")
			}()
		}
		return p, err
	}
	response := studioCall(t, s.routes(), "/api/record", studioEdit{Revision: studioRevision([]byte(studioScore)), Track: "bass", Pattern: "pulse", Take: []studioTakeNote{{Note: 60, Velocity: 90, EndTick: 60}}})
	select {
	case err := <-raced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recording did not reach the source exchange")
	}
	if response.Code != http.StatusConflict {
		t.Fatalf("late conflict: %d %s", response.Code, response.Body.String())
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, external) {
		t.Fatalf("conflict lost the external edit: %v", err)
	}
	if s.history.pendingTakeRevision != "previous-recording" {
		t.Fatalf("failed commit left a recording side effect: %q", s.history.pendingTakeRevision)
	}
	if edits := s.history.historySnapshot().Edits; len(edits) != 0 {
		t.Fatalf("failed commit added history: %+v", edits)
	}
}
