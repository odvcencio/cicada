package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

func TestStudioPerformancePitchEditsItsOwnedSourceWithAtomicHistory(t *testing.T) {
	isolateState(t)
	for _, layout := range []string{"entry", "peer"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(layout+map[string]string{"\n": "/LF", "\r\n": "/CRLF"}[newline], func(t *testing.T) {
				s, before, parent := placementVariationStudio(t, layout, newline)
				receipt, err := project.ReadScoreRevision(s.path)
				if err != nil {
					t.Fatal(err)
				}
				request := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: receipt, Pattern: parent, Action: "performance-pitch", PerformancePitch: &edit.ShapePerformancePitch{Entity: "pattern:another", Operation: "reflect", Pivot: 64}}
				missing := request
				missing.SourceRevision = ""
				if r := studioCall(t, s.routes(), "/api/pattern", missing); r.Code != http.StatusConflict {
					t.Fatal("pitch shaping accepted a missing project receipt", r.Code, r.Body.String())
				}
				if r := studioCall(t, s.routes(), "/api/pattern", request); r.Code != http.StatusOK {
					t.Fatal(r.Code, r.Body.String())
				}
				after := map[string][]byte{}
				owner := s.path
				if layout == "peer" {
					owner = filepath.Join(filepath.Dir(s.path), "definitions.cicada")
				}
				for name, original := range before {
					after[name], err = os.ReadFile(name)
					if err != nil || name != owner && !bytes.Equal(after[name], original) {
						t.Fatal("pitch shaping changed another source", name, err)
					}
					if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(after[name], []byte(newline), nil), []byte("\n")) {
						t.Fatal("changed line endings", name)
					}
				}
				compiled, err := compileStudioSource(s.path, after[s.path])
				if err != nil {
					t.Fatal(err)
				}
				performance := compiled.Patterns[0].Performance
				if performance.LengthTicks != 3840 || performance.Notes[0].Tick != 17 || performance.Notes[0].EndTick != 720 || performance.Notes[0].Note != 68 || performance.Notes[1].Note != 64 || performance.Notes[0].Group != 1 || performance.Groups[0].Points[1].Tick != 960 {
					t.Fatal("pitch shaping lost performed notes or shared gestures", performance)
				}
				if len(studioHistoryEdits(t, s.routes())) != 1 || compiled.Arrange.Placements[0].LengthTicks != 3840 || compiled.Arrange.Placements[1].AtTick != 3840 {
					t.Fatal("pitch shaping changed block timing or split history")
				}
				preview, _, err := patternPreviewProject(s.path, soundPreviewRequest{Pattern: parent, Track: "keys", Revision: studioRevision(after[s.path])})
				if err != nil || preview.Arrange == nil || preview.Arrange.Placements[0].LengthTicks != 3840 {
					t.Fatal("part audition truncated the scaled loop", err)
				}
				if r := studioCall(t, s.routes(), "/api/pattern", request); r.Code != http.StatusConflict {
					t.Fatal("stale pitch shaping repeated on a peer file", r.Code)
				}
				for _, step := range []struct {
					action string
					entry  []byte
					want   map[string][]byte
				}{{"undo", after[s.path], before}, {"redo", before[s.path], after}} {
					if r := studioCall(t, s.routes(), "/api/"+step.action, studioEdit{Revision: studioRevision(step.entry)}); r.Code != 200 {
						t.Fatal(step.action, r.Code, r.Body.String())
					}
					for name, expected := range step.want {
						if actual, err := os.ReadFile(name); err != nil || !bytes.Equal(actual, expected) {
							t.Fatal(step.action, "did not restore source exactly", name, err)
						}
					}
				}
				if err := s.shutdown(); err != nil {
					t.Fatal(err)
				}
				reopened, err := compileStudioSource(s.path, after[s.path])
				if err != nil || !reflect.DeepEqual(reopened.Patterns, compiled.Patterns) {
					t.Fatal("reopened performance changed", err)
				}
			})
		}
	}
}

func TestStudioPerformancePitchRejectsExternalEditsAndLibraries(t *testing.T) {
	isolateState(t)
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, when := range []string{"before", "during", "library"} {
		t.Run(when, func(t *testing.T) {
			layout := "peer"
			if when == "library" {
				layout = "library"
			}
			s, before, parent := placementVariationStudio(t, layout, "\r\n")
			receipt, _ := project.ReadScoreRevision(s.path)
			owner := filepath.Join(filepath.Dir(s.path), "definitions.cicada")
			external := append(bytes.Clone(before[owner]), []byte("// an external save\r\n")...)
			write := func() {
				if err := os.WriteFile(owner, external, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if when == "before" {
				write()
			} else if when == "during" {
				s.afterStage = write
			}
			r := studioCall(t, s.routes(), "/api/pattern", studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: receipt, Action: "performance-pitch", Pattern: parent, PerformancePitch: &edit.ShapePerformancePitch{Operation: "transpose", Semitones: 12}})
			if when == "library" {
				if r.Code != 422 || !strings.Contains(r.Body.String(), "copy library pattern") {
					t.Fatal("library pitch shaping was not refused", r.Code, r.Body.String())
				}
			} else {
				if r.Code != 409 {
					t.Fatal("pitch shaping lost a concurrent save", r.Code, r.Body.String())
				}
				if actual, _ := os.ReadFile(owner); !bytes.Equal(actual, external) {
					t.Fatal("external save overwritten")
				}
				before[owner] = external
			}
			for name, expected := range before {
				if actual, err := os.ReadFile(name); err != nil || !bytes.Equal(actual, expected) {
					t.Fatal("refused pitch shaping changed source", name, err)
				}
			}
			if len(s.history.edits) != 0 {
				t.Fatal("failed pitch shaping entered history")
			}
		})
	}
}
