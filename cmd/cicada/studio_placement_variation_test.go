package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

const placementVariationDefinitions = `instrument glass { voice poly { out=sine(pitch)*env(gate,300ms)*(0.1+pressure*0.05+timbre*0.05) } }
pattern parent performance length=3840ticks slot=7 {
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch { gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7; }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
}
`

func placementVariationStudio(t *testing.T, layout, newline string) (*studio, map[string][]byte, string) {
	t.Helper()
	dir := t.TempDir()
	entry := "cicada 2\ntitle \"One phrase variation\"\ntempo 120\nseed 42\n"
	parent, sound := "parent", "glass"
	files := map[string][]byte{}
	if layout == "library" {
		library := filepath.Join(dir, "lib", "motif")
		if err := project.NewLibrary("motif", library); err != nil {
			t.Fatal(err)
		}
		files[filepath.Join(library, "main.cicada")] = []byte(placementVariationDefinitions)
		entry += "import \"motif\"\n"
		parent, sound = "motif.parent", "motif.glass"
	} else if layout == "entry" {
		entry += placementVariationDefinitions
	} else {
		files[filepath.Join(dir, "definitions.cicada")] = []byte(placementVariationDefinitions)
	}
	entry += "track keys " + sound + " { level=-24dB }\n"
	arrangement := "// Keep this section note.\narrange {\n  place first keys " + parent + " { at=@1.1.1 length=1bar } // keep the original\n  place return keys " + parent + " { at=@2.1.1 length=1bar } // vary this use\n}\n"
	if layout == "entry" {
		entry += arrangement
	} else {
		files[filepath.Join(dir, "arrangement.cicada")] = []byte(arrangement)
		manifest := "project placement-variation\ncicada 2\nentry \"main.cicada\"\nsource \"arrangement.cicada\"\n"
		if layout == "peer" {
			manifest += "source \"definitions.cicada\"\n"
		}
		files[filepath.Join(dir, "cicada.mod")] = []byte(manifest)
	}
	path := filepath.Join(dir, "main.cicada")
	files[path] = []byte(entry)
	for name, data := range files {
		files[name] = bytes.ReplaceAll(data, []byte("\n"), []byte(newline))
		if err := os.WriteFile(name, files[name], 0600); err != nil {
			t.Fatal(err)
		}
	}
	if layout == "library" {
		sources, err := project.ReadSources(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		pins, _, err := sources.UpdateLibraries("")
		if err != nil {
			t.Fatal(err)
		}
		pinPath := filepath.Join(dir, "cicada.sum")
		if err := os.WriteFile(pinPath, pins, 0600); err != nil {
			t.Fatal(err)
		}
		files[pinPath] = pins
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	return s, files, parent
}

func TestPlacementVariationCopiesAndRebindsOneUseWithAtomicHistory(t *testing.T) {
	isolateState(t)
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, layout := range []string{"entry", "peer", "library"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(layout+map[string]string{"\n": "/LF", "\r\n": "/CRLF"}[newline], func(t *testing.T) {
				s, before, parent := placementVariationStudio(t, layout, newline)
				seen, err := project.ReadScoreRevision(s.path)
				if err != nil {
					t.Fatal(err)
				}
				initial, err := compileStudioSource(s.path, before[s.path])
				if err != nil {
					t.Fatal(err)
				}
				request := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: seen, Action: "vary", Name: "return", NewName: "independent"}
				r := studioCall(t, s.routes(), "/api/arrange", request)
				if r.Code != 200 {
					t.Fatal(r.Code, r.Body.String())
				}
				after := map[string][]byte{}
				for name, original := range before {
					after[name], err = os.ReadFile(name)
					if err != nil {
						t.Fatal(err)
					}
					if name != s.path && filepath.Base(name) != "arrangement.cicada" && !bytes.Equal(after[name], original) {
						t.Fatal("variation rewrote definitions, library or manifest", name)
					}
					if newline == "\r\n" && filepath.Ext(name) != ".sum" && bytes.Contains(bytes.ReplaceAll(after[name], []byte(newline), nil), []byte("\n")) {
						t.Fatal("variation changed line endings", name)
					}
				}
				compiled, err := compileStudioSource(s.path, after[s.path])
				if err != nil {
					t.Fatal(err)
				}
				if compiled.Arrange.Placements[0].Content != parent || compiled.Arrange.Placements[1].Content != "independent" || compiled.Arrange.Placements[1].AtTick != 3840 || compiled.Arrange.Placements[1].LengthTicks != 3840 {
					t.Fatal("variation changed another placement or its timing")
				}
				want, copied := initial.Patterns[0], compiled.Patterns[len(compiled.Patterns)-1]
				want.ID = copied.ID
				if !reflect.DeepEqual(want, copied) {
					t.Fatal("variation changed the recorded notes or gestures")
				}
				if len(studioHistoryEdits(t, s.routes())) != 1 {
					t.Fatal("variation was not one history step")
				}
				for _, step := range []struct {
					route string
					entry []byte
					want  map[string][]byte
				}{{"undo", after[s.path], before}, {"redo", before[s.path], after}} {
					if r := studioCall(t, s.routes(), "/api/"+step.route, studioEdit{Revision: studioRevision(step.entry)}); r.Code != 200 {
						t.Fatal(step.route, r.Code, r.Body.String())
					}
					for name, expected := range step.want {
						if data, err := os.ReadFile(name); err != nil || !bytes.Equal(data, expected) {
							t.Fatal(step.route, "did not restore exact source bytes", name, err)
						}
					}
				}
				if err := s.shutdown(); err != nil {
					t.Fatal(err)
				}
				reopened, err := newStudio(s.path)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.shutdown()
				if _, err := compileStudioSource(reopened.path, after[s.path]); err != nil {
					t.Fatal("reopened variation did not compile", err)
				}
			})
		}
	}
}

func TestPlacementVariationRejectsUnseenAndMidCommitSourceChanges(t *testing.T) {
	isolateState(t)
	for _, mid := range []bool{false, true} {
		t.Run(map[bool]string{false: "unseen", true: "mid-commit"}[mid], func(t *testing.T) {
			s, before, _ := placementVariationStudio(t, "peer", "\r\n")
			seen, _ := project.ReadScoreRevision(s.path)
			path := filepath.Join(filepath.Dir(s.path), "arrangement.cicada")
			external := append(bytes.Clone(before[path]), []byte("// another editor saved this\r\n")...)
			write := func() {
				if err := os.WriteFile(path, external, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mid {
				s.afterStage = write
			} else {
				write()
			}
			r := studioCall(t, s.routes(), "/api/arrange", studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: seen, Action: "vary", Name: "return", NewName: "independent"})
			if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "changed") {
				t.Fatal("variation overwrote a competing save", r.Code, r.Body.String())
			}
			if data, _ := os.ReadFile(path); !bytes.Equal(data, external) {
				t.Fatal("external save was lost")
			}
			if data, _ := os.ReadFile(s.path); !bytes.Equal(data, before[s.path]) {
				t.Fatal("failed variation left a copied pattern")
			}
			if len(s.history.edits) != 0 {
				t.Fatal("failed variation entered history")
			}
		})
	}
}

type variationRenderMismatch struct{ secondary bool }

func (c variationRenderMismatch) Hash(source []byte) (string, error) {
	if c.secondary {
		return "same", nil
	}
	if bytes.Contains(source, []byte("pattern independent")) || bytes.Contains(source, []byte("keys independent")) {
		return "changed", nil
	}
	return "original", nil
}

func (variationRenderMismatch) ForSource(string, map[string][]byte) (edits.RenderCheck, error) {
	return variationRenderMismatch{}, nil
}

func TestPlacementVariationRenderRefusalsLeaveNoPartialCopy(t *testing.T) {
	isolateState(t)
	for _, layout := range []string{"entry", "peer"} {
		t.Run(layout, func(t *testing.T) {
			s, before, _ := placementVariationStudio(t, layout, "\n")
			s.renderCheck = variationRenderMismatch{secondary: layout == "peer"}
			seen, _ := project.ReadScoreRevision(s.path)
			r := studioCall(t, s.routes(), "/api/arrange", studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: seen, Action: "vary", Name: "return", NewName: "independent"})
			if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "changes the rendered song") {
				t.Fatal("mismatched sound was not refused", r.Code, r.Body.String())
			}
			for name, expected := range before {
				if data, err := os.ReadFile(name); err != nil || !bytes.Equal(data, expected) {
					t.Fatal("refusal left partial source changes", name, err)
				}
			}
			if len(s.history.edits) != 0 {
				t.Fatal("refused sound entered history")
			}
		})
	}
}
