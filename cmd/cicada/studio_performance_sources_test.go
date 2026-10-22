package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

type performanceSourceCase struct {
	action string
	set    func(*studioEdit)
	want   func(*seq.Performance)
}

func performanceSourceCases() []performanceSourceCase {
	return []performanceSourceCase{
		{"performed-note", func(e *studioEdit) {
			e.PerformanceNote = &edit.SetPerformedNote{Entity: "pattern:wrong", Index: 1, Tick: 37, EndTick: 1337, Pitch: 67, Velocity: 95}
		}, func(p *seq.Performance) { n := &p.Notes[1]; n.Tick, n.EndTick, n.Note, n.Velocity = 37, 1337, 67, 95 }},
		{"performance-length", func(e *studioEdit) { e.PerformanceLength = 7680 }, func(p *seq.Performance) { p.LengthTicks = 7680 }},
		{"performed-gesture", func(e *studioEdit) {
			e.PerformanceExpression = &edit.SetPerformedExpression{NoteIndex: 0, Index: 0, Tick: 0, PitchCents: 75, Pressure: .6, Timbre: .4, VibratoDepthCents: 14, VibratoRateHz: 6}
		}, func(p *seq.Performance) {
			p.Notes[0].Expressions[0].Params = seq.Expression{Set: true, PitchCents: 75, Pressure: .6, Timbre: .4, VibratoDepthCents: 14, VibratoRateHz: 6}
		}},
		{"performance-group", func(e *studioEdit) { e.PerformanceGroup = &edit.SetPerformanceGroup{Index: 0, Name: "swell"} }, func(p *seq.Performance) { p.Groups[0].Name = "swell" }},
		{"performance-binding", func(e *studioEdit) { e.PerformanceBinding = &edit.BindPerformedGroup{NoteIndex: 1, Group: 0} }, func(p *seq.Performance) { p.Notes[1].Group = 0 }},
		{"shared-gesture", func(e *studioEdit) {
			e.SharedExpression = &edit.SetSharedExpression{GroupIndex: 0, Index: 1, Tick: 1200, PitchCents: 150, Pressure: .7, Timbre: .6}
		}, func(p *seq.Performance) {
			p.Groups[0].Points[1] = seq.GroupPoint{Tick: 1200, Value: expression.Controls{PitchCents: 150, Pressure: .7, Timbre: .6}}
		}},
	}
}

func TestStudioPerformanceSourceEditsHaveExactProjectHistory(t *testing.T) {
	isolateState(t)
	for _, layout := range []string{"entry", "peer"} {
		for _, newline := range []string{"\n", "\r\n"} {
			for _, c := range performanceSourceCases() {
				t.Run(layout+"/"+c.action+map[string]string{"\n": "/LF", "\r\n": "/CRLF"}[newline], func(t *testing.T) {
					s, before, id := placementVariationStudio(t, layout, newline)
					receipt, err := project.ReadScoreRevision(s.path)
					if err != nil {
						t.Fatal(err)
					}
					original, err := compileStudioSource(s.path, before[s.path])
					if err != nil {
						t.Fatal(err)
					}
					want := original.Patterns[0].Performance.Kernel()
					c.want(want)
					req := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: receipt, Action: c.action, Pattern: id}
					c.set(&req)
					missing := req
					missing.SourceRevision = ""
					if r := studioCall(t, s.routes(), "/api/pattern", missing); r.Code != 409 {
						t.Fatal("missing project receipt accepted", r.Code, r.Body.String())
					}
					if r := studioCall(t, s.routes(), "/api/pattern", req); r.Code != 200 {
						t.Fatal(r.Code, r.Body.String())
					}
					owner := s.path
					if layout == "peer" {
						owner = filepath.Join(filepath.Dir(s.path), "definitions.cicada")
					}
					after := map[string][]byte{}
					for name, b := range before {
						after[name], err = os.ReadFile(name)
						if err != nil || name != owner && !bytes.Equal(after[name], b) {
							t.Fatal("changed another source", name, err)
						}
						if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(after[name], []byte(newline), nil), []byte("\n")) {
							t.Fatal("lost line endings", name)
						}
					}
					if bytes.Equal(after[owner], before[owner]) {
						t.Fatal("owning source unchanged")
					}
					compiled, err := compileStudioSource(s.path, after[s.path])
					if err != nil || !reflect.DeepEqual(compiled.Patterns[0].Performance.Kernel(), want) {
						t.Fatal("edit changed unrelated musical fields", err)
					}
					if !reflect.DeepEqual(compiled.Arrange, original.Arrange) || len(studioHistoryEdits(t, s.routes())) != 1 {
						t.Fatal("edit changed arrangement or split history")
					}
					if r := studioCall(t, s.routes(), "/api/pattern", req); r.Code != 409 {
						t.Fatal("stale edit accepted", r.Code)
					}
					for _, restore := range []struct {
						action string
						from   []byte
						files  map[string][]byte
					}{{"undo", after[s.path], before}, {"redo", before[s.path], after}} {
						if r := studioCall(t, s.routes(), "/api/"+restore.action, studioEdit{Revision: studioRevision(restore.from)}); r.Code != 200 {
							t.Fatal(restore.action, r.Code, r.Body.String())
						}
						for name, b := range restore.files {
							if got, _ := os.ReadFile(name); !bytes.Equal(got, b) {
								t.Fatal("history lost exact source bytes", restore.action, name)
							}
						}
					}
					if err := s.shutdown(); err != nil {
						t.Fatal(err)
					}
					reopened, err := compileStudioSource(s.path, after[s.path])
					if err != nil || !reflect.DeepEqual(reopened.Patterns, compiled.Patterns) {
						t.Fatal("reopened edit changed", err)
					}
				})
			}
		}
	}
}

func TestStudioPerformanceSourceEditsRefuseOutsideSavesAndLibraries(t *testing.T) {
	isolateState(t)
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, c := range performanceSourceCases() {
		for _, when := range []string{"before", "during", "library"} {
			t.Run(c.action+"/"+when, func(t *testing.T) {
				layout := "peer"
				if when == "library" {
					layout = "library"
				}
				s, before, id := placementVariationStudio(t, layout, "\r\n")
				receipt, _ := project.ReadScoreRevision(s.path)
				owner := filepath.Join(filepath.Dir(s.path), "definitions.cicada")
				outside := append(bytes.Clone(before[owner]), []byte("// outside save\r\n")...)
				write := func() {
					if err := os.WriteFile(owner, outside, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if when == "before" {
					write()
				} else if when == "during" {
					s.afterStage = write
				}
				req := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: receipt, Action: c.action, Pattern: id}
				c.set(&req)
				r := studioCall(t, s.routes(), "/api/pattern", req)
				if when == "library" {
					if r.Code != 422 || !strings.Contains(r.Body.String(), "copy library pattern") {
						t.Fatal("library write not refused", r.Code, r.Body.String())
					}
				} else {
					if r.Code != 409 {
						t.Fatal("outside save not refused", r.Code, r.Body.String())
					}
					if strings.Contains(r.Body.String(), "render check") || !strings.Contains(r.Body.String(), "Refresh Studio before saving") {
						t.Fatal("performed source conflict exposed implementation details", r.Body.String())
					}
					before[owner] = outside
				}
				for name, b := range before {
					if got, _ := os.ReadFile(name); !bytes.Equal(got, b) {
						t.Fatal("refusal lost source bytes", name)
					}
				}
				if len(s.history.edits) != 0 {
					t.Fatal("refused edit entered history")
				}
			})
		}
	}
}

func TestStudioRawPerformanceIntentRequiresProjectReceiptForCommit(t *testing.T) {
	isolateState(t)
	s, before, id := placementVariationStudio(t, "peer", "\n")
	receipt, _ := project.ReadScoreRevision(s.path)
	env := edit.Envelope{Version: 1, Revision: studioRevision(before[s.path]), Intents: []edit.Intent{&edit.SetPerformedNote{Entity: edit.EntityID("pattern:" + id), Index: 1, Tick: 19, EndTick: 1320, Pitch: 67, Velocity: 87}}}
	call := func(header string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(env)
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/intents", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Cicada-Source-Revision", header)
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		return w
	}
	if r := call(""); r.Code != 409 {
		t.Fatal("raw batch bypassed full receipt", r.Code, r.Body.String())
	}
	env.DryRun = true
	if r := call(""); r.Code != 200 {
		t.Fatal("read-only preview refused", r.Code, r.Body.String())
	}
	for name, b := range before {
		if got, _ := os.ReadFile(name); !bytes.Equal(got, b) {
			t.Fatal("dry run wrote source", name)
		}
	}
	env.DryRun = false
	if r := call(receipt); r.Code != 200 {
		t.Fatal("raw receipt commit failed", r.Code, r.Body.String())
	}
	owner := filepath.Join(filepath.Dir(s.path), "definitions.cicada")
	b, _ := os.ReadFile(owner)
	if !bytes.Contains(b, []byte("note 67 at=19ticks")) {
		t.Fatal("raw intent missed owned source")
	}
	if r := call(receipt); r.Code != 409 {
		t.Fatal("raw stale receipt accepted", r.Code)
	}
}
