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
	"m31labs.dev/cicada/project"
)

type studioOwnedGridCase struct {
	name, owner, pattern, route string
	body                        studioEdit
	raw                         edit.Intent
}

func studioOwnedGridCases() []studioOwnedGridCase {
	pitch := 62
	return []studioOwnedGridCase{
		{name: "toggle", owner: "lead.cicada", pattern: "line", body: studioEdit{Action: "toggle", Step: 1}},
		{name: "pitch", owner: "lead.cicada", pattern: "line", body: studioEdit{Action: "pitch", Pitch: &pitch}},
		{name: "accent", owner: "lead.cicada", pattern: "line", route: "/api/toggle", body: studioEdit{Modifier: "accent"}},
		{name: "chance", owner: "lead.cicada", pattern: "line", route: "/api/toggle", body: studioEdit{Modifier: "chance"}},
		{name: "velocity", owner: "drums.cicada", pattern: "beat", raw: &edit.SetDrumVelocity{Entity: "step:beat/bd/0", Velocity: 80, Shared: "pattern"}},
		{name: "step", owner: "lead.cicada", pattern: "line", body: studioEdit{Action: "step", Step: 1, NoteEdit: &studioStepEdit{Mode: "note", Pitch: 65, Ratchet: 2, Chance: 75}}},
		{name: "settings", owner: "lead.cicada", pattern: "line", body: studioEdit{Action: "settings", Settings: &studioPatternSettings{Swing100: 5500, Gate: 65, Transpose: 12}}},
		{name: "range", owner: "lead.cicada", pattern: "line", body: studioEdit{Action: "range", Range: &studioPatternRange{Operation: "transpose", First: 0, Last: 3, Amount: 2}}},
		{name: "resize", owner: "lead.cicada", pattern: "line", body: studioEdit{Action: "resize", Length: 12}},
		{name: "chord", owner: "keys.cicada", pattern: "harmony", body: studioEdit{Action: "chord", Chord: &edit.SetChord{Notes: []int{60, 64, 67}, Hold: 2}}},
	}
}

func gridSourcesStudio(t *testing.T, newline string) (*studio, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{}
	for name, text := range map[string]string{
		"main.cicada":  "cicada 2\ntitle \"Edit the recorded idea\"\ntempo 120\nseed 42\ntrack lead acid {}\ntrack keys piano {}\ntrack drums drums {}\nscene main { lead=line keys=harmony drums=beat }\nsong { main*2 }\n",
		"lead.cicada":  "// lead remains here\npattern line acid steps=8 { c4 . . . e4 . . . } // keep lead\n",
		"keys.cicada":  "// keys remain here\npattern harmony notes steps=8 { c4 - . . g4 . . . } // keep keys\n",
		"drums.cicada": "// drums remain here\npattern beat drums steps=8 { bd: x....... sd: ..x..... } // keep drums\n",
		"cicada.mod":   "project grid-sources\ncicada 2\nentry \"main.cicada\"\nsource \"lead.cicada\"\nsource \"keys.cicada\"\nsource \"drums.cicada\"\n",
	} {
		path := filepath.Join(dir, name)
		files[path] = []byte(strings.ReplaceAll(text, "\n", newline))
		if err := os.WriteFile(path, files[path], 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(filepath.Join(dir, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	return s, files
}

func gridSourceRequest(t *testing.T, s *studio, c studioOwnedGridCase, receipt string) *httptest.ResponseRecorder {
	t.Helper()
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if c.raw != nil {
		body, _ := json.Marshal(edit.Envelope{Version: 1, Revision: studioRevision(source), Intents: []edit.Intent{c.raw}})
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/intents", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Cicada-Source-Revision", receipt)
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		return w
	}
	body := c.body
	body.Revision, body.SourceRevision, body.Pattern = studioRevision(source), receipt, c.pattern
	route := c.route
	if route == "" {
		route = "/api/pattern"
	}
	return studioCall(t, s.routes(), route, body)
}

// Legacy batch contracts opt into the project receipt explicitly. Tests of
// missing/stale receipts use their own requests, never this fixture helper.
func studioIntentWithProjectReceipt(t *testing.T, handler http.Handler, path string, env edit.Envelope) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/intents", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Cicada-Source-Revision", studioProjectReceipt(t, path))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestRawGridIntentDryRunNeedsNoReceiptAndWritesNothing(t *testing.T) {
	isolateState(t)
	s, before := gridSourcesStudio(t, "\r\n")
	env := edit.Envelope{Version: 1, Revision: studioRevision(before[s.path]), DryRun: true, Intents: []edit.Intent{&edit.SetPitch{Entity: "step:line/0", Pitch: 62, Shared: "pattern"}}}
	r := studioCall(t, s.routes(), "/api/intents", env)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "lead.cicada") {
		t.Fatal("source-aware dry run refused or hid its peer diff", r.Code, r.Body.String())
	}
	for path, want := range before {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
			t.Fatal("dry run wrote source", path, err)
		}
	}
	if len(studioHistoryEdits(t, s.routes())) != 0 {
		t.Fatal("dry run wrote history")
	}
}

func TestStudioOwnedGridEditsHaveExactProjectHistoryAndReopen(t *testing.T) {
	isolateState(t)
	for _, newline := range []string{"\n", "\r\n"} {
		for _, c := range studioOwnedGridCases() {
			t.Run(c.name+map[string]string{"\n": "/LF", "\r\n": "/CRLF"}[newline], func(t *testing.T) {
				s, before := gridSourcesStudio(t, newline)
				receipt := studioProjectReceipt(t, s.path)
				if r := gridSourceRequest(t, s, c, ""); r.Code != 409 {
					t.Fatal("missing source receipt accepted", r.Code, r.Body.String())
				}
				if r := gridSourceRequest(t, s, c, receipt); r.Code != 200 {
					t.Fatal(r.Code, r.Body.String())
				} else if r.Header().Get("X-Cicada-Write-Source-Revision") != studioProjectReceipt(t, s.path) {
					t.Fatal("grid acknowledgment did not identify its committed project", r.Header())
				}
				after := map[string][]byte{}
				owner := filepath.Join(filepath.Dir(s.path), c.owner)
				for path, original := range before {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					after[path] = data
					if path == owner {
						if bytes.Equal(data, original) || !bytes.Contains(data, []byte("// keep ")) {
							t.Fatal("destination did not retain its source", path)
						}
					} else if !bytes.Equal(data, original) {
						t.Fatal("changed another source", path)
					}
					if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(data, []byte(newline), nil), []byte("\n")) {
						t.Fatal("CRLF lost", path)
					}
				}
				compiled, err := compileStudioSource(s.path, after[s.path])
				if err != nil {
					t.Fatal(err)
				}
				if len(studioHistoryEdits(t, s.routes())) != 1 {
					t.Fatal("grid edit split project history")
				}
				if r := gridSourceRequest(t, s, c, receipt); r.Code != 409 {
					t.Fatal("entry-identical stale project repeated a grid edit", r.Code)
				}
				for _, step := range []struct {
					action string
					want   map[string][]byte
				}{{"undo", before}, {"redo", after}} {
					if r := studioCall(t, s.routes(), "/api/"+step.action, studioEdit{Revision: studioRevision(before[s.path])}); r.Code != 200 {
						t.Fatal(step.action, r.Code, r.Body.String())
					}
					for path, want := range step.want {
						if actual, err := os.ReadFile(path); err != nil || !bytes.Equal(actual, want) {
							t.Fatal("project history lost exact source", step.action, path, err)
						}
					}
				}
				if err := s.shutdown(); err != nil {
					t.Fatal(err)
				}
				reopened, err := compileStudioSource(s.path, after[s.path])
				if err != nil || !reflect.DeepEqual(reopened.Patterns, compiled.Patterns) {
					t.Fatal("reopen changed musical material", err)
				}
			})
		}
	}
}

func TestStudioOwnedGridEditsRefuseOutsideSourceSaves(t *testing.T) {
	isolateState(t)
	for _, when := range []string{"before", "during"} {
		for _, c := range studioOwnedGridCases() {
			t.Run(c.name+"/"+when, func(t *testing.T) {
				s, before := gridSourcesStudio(t, "\r\n")
				receipt := studioProjectReceipt(t, s.path)
				peer := "keys.cicada"
				if c.owner == peer {
					peer = "lead.cicada"
				}
				path := filepath.Join(filepath.Dir(s.path), peer)
				outside := append(bytes.Clone(before[path]), []byte("// another editor saved this unrelated part\r\n")...)
				write := func() {
					if err := os.WriteFile(path, outside, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if when == "before" {
					write()
				} else {
					s.afterStage = write
				}
				if r := gridSourceRequest(t, s, c, receipt); r.Code != 409 {
					t.Fatal("outside save was not refused", r.Code, r.Body.String())
				}
				before[path] = outside
				for path, want := range before {
					if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
						t.Fatal("refusal lost a project source", path, err)
					}
				}
				if len(studioHistoryEdits(t, s.routes())) != 0 {
					t.Fatal("refusal wrote history")
				}
			})
		}
	}
}

func TestGridNoopRefusesASourceSaveDuringValidation(t *testing.T) {
	isolateState(t)
	s, before := gridSourcesStudio(t, "\r\n")
	receipt := studioProjectReceipt(t, s.path)
	path := filepath.Join(filepath.Dir(s.path), "keys.cicada")
	outside := append(bytes.Clone(before[path]), []byte("// saved during no-op validation\r\n")...)
	previous := studioCompile
	t.Cleanup(func() { studioCompile = previous })
	changed := false
	studioCompile = func(pathname string, source []byte, files map[string][]byte) (*project.Project, error) {
		p, err := previous(pathname, source, files)
		if err == nil && !changed {
			changed = true
			if err := os.WriteFile(path, outside, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return p, err
	}
	c := studioOwnedGridCase{pattern: "line", body: studioEdit{Action: "step", NoteEdit: &studioStepEdit{Mode: "note", Pitch: 60, Ratchet: 1, Chance: 100}}}
	r := gridSourceRequest(t, s, c, receipt)
	if !changed || r.Code != 409 || r.Header().Get("X-Cicada-Write-Source-Revision") != "" {
		t.Fatal("no-op acknowledged an unseen source save", changed, r.Code, r.Body.String())
	}
	before[path] = outside
	for name, want := range before {
		if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, want) {
			t.Fatal("no-op conflict changed source", name, err)
		}
	}
	if len(studioHistoryEdits(t, s.routes())) != 0 {
		t.Fatal("no-op conflict wrote history")
	}
}
