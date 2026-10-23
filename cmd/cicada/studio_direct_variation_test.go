package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

const directVariationExtra = `
phrase hook { [1 3 5]^ - . . }
pattern idea notes slot=7 { step=1/8t swing=54% gate=70% transpose=5 seed=99 use hook }
pattern beat drums slot=5 { bd: X.x.; sd: ..x.; }
`

func directVariationStudio(t *testing.T, layout, newline string) (*studio, map[string][]byte, string) {
	t.Helper()
	dir := t.TempDir()
	definitions := strings.Replace(placementVariationDefinitions, "instrument glass {", "instrument glass { octave=3", 1) + directVariationExtra
	entry := "cicada 2\ntitle \"Ideas from a library\"\ntempo 120\nseed 42\nkey c minor\n"
	prefix := ""
	files := map[string]string{}
	if layout == "entry" {
		entry += definitions
	} else if layout == "peer" {
		files["definitions.cicada"] = definitions
		files["cicada.mod"] = "project direct-variation\ncicada 2\nentry \"main.cicada\"\nsource \"definitions.cicada\"\n"
	} else {
		if err := project.NewLibrary("motif", filepath.Join(dir, "lib", "motif")); err != nil {
			t.Fatal(err)
		}
		prefix = "motif."
		files["lib/motif/main.cicada"] = definitions
		entry += "import \"motif\"\n"
	}
	entry += "track keys " + prefix + "glass { level=-24dB }\ntrack upper " + prefix + "glass { octave=5 level=-18dB }\ntrack drums drums { level=-24dB }\npattern foundation notes { c3 . g3 . }\nscene main { keys=foundation upper=off drums=off }\nsong { main*4 }\n"
	files["main.cicada"] = entry
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(data, "\n", newline)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "main.cicada")
	if layout == "library" {
		sources, err := project.ReadSources(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		pins, _, err := sources.UpdateLibraries("")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cicada.sum"), pins, 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string][]byte{}
	if err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(name)
		before[name] = data
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	return s, before, prefix
}

func TestDirectVariationCopiesUnusedMusicalMaterialWithOneUndo(t *testing.T) {
	isolateState(t)
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, layout := range []string{"entry", "peer", "library"} {
		for _, newline := range []string{"\n", "\r\n"} {
			for _, material := range []string{"idea", "parent", "beat"} {
				t.Run(layout+"/"+material+map[string]string{"\n": "/LF", "\r\n": "/CRLF"}[newline], func(t *testing.T) {
					s, before, prefix := directVariationStudio(t, layout, newline)
					track := "upper"
					if material == "beat" {
						track = "drums"
					}
					parent := prefix + material
					receipt, _ := project.ReadScoreRevision(s.path)
					request := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: receipt, Action: "duplicate", Pattern: parent, Track: track, NewName: "local-idea"}
					originalPreview, _, err := patternPreviewProject(s.path, soundPreviewRequest{Pattern: parent, Track: track, Revision: request.Revision})
					if err != nil {
						t.Fatal(err)
					}
					if r := studioCall(t, s.routes(), "/api/pattern", request); r.Code != http.StatusOK {
						t.Fatal(r.Code, r.Body.String())
					}
					updated, _ := os.ReadFile(s.path)
					if !bytes.HasPrefix(updated, before[s.path]) || !bytes.Contains(updated, []byte("pattern local-idea")) {
						t.Fatal("variation rewrote the original entry declarations")
					}
					for name, original := range before {
						if actual, err := os.ReadFile(name); err != nil || name != s.path && !bytes.Equal(actual, original) {
							t.Fatal("variation changed its source or library pins", name, err)
						}
					}
					if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(updated, []byte(newline), nil), []byte("\n")) {
						t.Fatal("variation changed entry line endings")
					}
					copyPreview, _, err := patternPreviewProject(s.path, soundPreviewRequest{Pattern: "local-idea", Track: track, Revision: studioRevision(updated)})
					if err != nil {
						t.Fatal(err)
					}
					var want, got project.Pattern
					for _, p := range originalPreview.Patterns {
						if p.ID == parent {
							want = p
						}
					}
					for _, p := range copyPreview.Patterns {
						if p.ID == "local-idea" {
							got = p
						}
					}
					want.ID = got.ID
					if got.ID == "" || !reflect.DeepEqual(want, got) {
						t.Fatal("variation changed its chosen register, timing or gestures", want, got)
					}
					if len(studioHistoryEdits(t, s.routes())) != 1 {
						t.Fatal("variation split history")
					}
					if r := studioCall(t, s.routes(), "/api/pattern", request); r.Code != 409 {
						t.Fatal("stale duplicate was applied twice", r.Code)
					}
					for _, step := range []struct {
						action string
						entry  []byte
						want   []byte
					}{{"undo", updated, before[s.path]}, {"redo", before[s.path], updated}} {
						if r := studioCall(t, s.routes(), "/api/"+step.action, studioEdit{Revision: studioRevision(step.entry)}); r.Code != 200 {
							t.Fatal(step.action, r.Code, r.Body.String())
						}
						if actual, _ := os.ReadFile(s.path); !bytes.Equal(actual, step.want) {
							t.Fatal(step.action, "did not restore exact source")
						}
					}
				})
			}
		}
	}
}

func TestDirectVariationRefusesConflictsNamesAndChangedSound(t *testing.T) {
	isolateState(t)
	for _, mode := range []string{"unseen", "mid-commit", "sound", "reserved", "peer-name", "unknown-track", "incompatible-track"} {
		t.Run(mode, func(t *testing.T) {
			s, before, _ := directVariationStudio(t, "peer", "\r\n")
			receipt, _ := project.ReadScoreRevision(s.path)
			request := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: receipt, Action: "duplicate", Pattern: "idea", Track: "upper", NewName: "independent"}
			owner := filepath.Join(filepath.Dir(s.path), "definitions.cicada")
			external := append(bytes.Clone(before[owner]), []byte("// an external save\r\n")...)
			write := func() {
				if err := os.WriteFile(owner, external, 0600); err != nil {
					t.Fatal(err)
				}
			}
			status := 422
			switch mode {
			case "unseen":
				write()
				status = 409
			case "mid-commit":
				s.afterStage = write
				status = 409
			case "sound":
				s.renderCheck = variationRenderMismatch{}
			case "reserved":
				request.NewName = "off"
			case "peer-name":
				request.NewName = "parent"
			case "unknown-track":
				request.Track = "missing"
			case "incompatible-track":
				request.Track = "drums"
			}
			if r := studioCall(t, s.routes(), "/api/pattern", request); r.Code != status {
				t.Fatal("variation refusal", mode, r.Code, r.Body.String())
			}
			if mode == "unseen" || mode == "mid-commit" {
				before[owner] = external
			}
			for name, expected := range before {
				if actual, err := os.ReadFile(name); err != nil || !bytes.Equal(actual, expected) {
					t.Fatal("failed variation changed source", name, err)
				}
			}
			if len(s.history.edits) != 0 {
				t.Fatal("failed variation entered history")
			}
		})
	}
}

func TestPlacementVariationKeepsTheSelectedTracksRegister(t *testing.T) {
	isolateState(t)
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	s, before, prefix := directVariationStudio(t, "library", "\n")
	source := strings.Split(string(before[s.path]), "scene main")[0] + "arrange {\n place low keys foundation { at=@1.1.1 length=1bar }\n place high upper " + prefix + "idea { at=@2.1.1 length=1bar }\n}\n"
	if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.shutdown()
	receipt, _ := project.ReadScoreRevision(s.path)
	if r := studioCall(t, reopened.routes(), "/api/arrange", studioEdit{Revision: studioRevision([]byte(source)), SourceRevision: receipt, Action: "vary", Name: "high", NewName: "answer"}); r.Code != 200 {
		t.Fatal("variation changed the high track's sound", r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, after)
	if err != nil {
		t.Fatal(err)
	}
	var copy project.Pattern
	for _, pattern := range p.Patterns {
		if pattern.ID == "answer" {
			copy = pattern
		}
	}
	if copy.Data[0].Note != 72 || p.Arrange.Placements[0].Content != "foundation" || p.Arrange.Placements[1].Content != "answer" {
		t.Fatal("copy used the representative low track instead of the selected high track")
	}
}
