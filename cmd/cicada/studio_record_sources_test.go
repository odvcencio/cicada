package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func studioProjectReceipt(t *testing.T, path string) string {
	t.Helper()
	version, err := project.ReadScoreRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func recordingSourcesStudio(t *testing.T, newline string) (*studio, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{}
	texts := map[string]string{
		"main.cicada": "cicada 2\ntitle \"Keep the complete take\"\ntempo 120\nseed 42\n// entry stays intact\nscene main { lead=line keys=answer }\nsong { main*2 }\n",
		"lead.cicada": "instrument glass { voice mono { out=sine(pitch)*env(gate,200ms)*(0.1+pressure*0.1+timbre*0.1) } }\ntrack lead glass { level=-24dB }\n// lead definition stays here\npattern line notes steps=16 { . . . . . . . . . . . . . . . . } // keep lead comment\n",
		"keys.cicada": "track keys piano { level=-24dB }\n// keys definition stays here\npattern answer notes steps=16 { . . . . . . . . . . . . . . . . } // keep keys comment\n",
		"cicada.mod":  "project recording-sources\ncicada 2\nentry \"main.cicada\"\nsource \"lead.cicada\"\nsource \"keys.cicada\"\n",
	}
	for name, text := range texts {
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

func recordingSourcesTake(timing string) []studioTakeRecording {
	takes := []studioTakeRecording{
		{Track: "lead", Pattern: "line", Notes: []studioTakeNote{{Tick: 3857, EndTick: 4200, Note: 60, Velocity: 103, Expressions: []studioTakeExpression{{Tick: 3857, PitchCents: 75, Pressure: .6, Timbre: .4}}}, {Tick: 4817, EndTick: 5160, Note: 67, Velocity: 87}}},
		{Track: "keys", Pattern: "answer", Notes: []studioTakeNote{{Tick: 4099, EndTick: 4340, Note: 64, Velocity: 95}, {Tick: 5059, EndTick: 5300, Note: 69, Velocity: 78}}},
	}
	if timing == "played" {
		takes[0].Groups = []studioTakeGroup{{Kind: "channel", Points: []studioTakeExpression{{Tick: 3840, Timbre: .5}, {Tick: 4800, PitchCents: 150, Pressure: .7, Timbre: .6}, {Tick: 7200, PitchCents: -50, Pressure: .3, Timbre: .4}}}}
		for i := range takes[0].Notes {
			takes[0].Notes[i].Group = 1
		}
	}
	return takes
}

func TestRecordedMultiSourceTakeHasOneExactUndoAndDurableHandoff(t *testing.T) {
	isolateState(t)
	for _, timing := range []string{"grid", "played"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(timing+map[string]string{"\n": "/LF", "\r\n": "/CRLF"}[newline], func(t *testing.T) {
				s, before := recordingSourcesStudio(t, newline)
				request := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: studioProjectReceipt(t, s.path), Timing: timing, Recordings: recordingSourcesTake(timing)}
				missing := request
				missing.SourceRevision = ""
				if r := studioCall(t, s.routes(), "/api/record", missing); r.Code != 409 {
					t.Fatal("missing capture/review version accepted", r.Code)
				}
				if r := studioCall(t, s.routes(), "/api/record", request); r.Code != 200 {
					t.Fatal(r.Code, r.Body.String())
				}
				after := map[string][]byte{}
				for path, original := range before {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					after[path] = data
					owned := strings.HasSuffix(path, "lead.cicada") || strings.HasSuffix(path, "keys.cicada")
					if owned == bytes.Equal(data, original) {
						t.Fatal("wrong recording source scope", path)
					}
					if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(data, []byte(newline), nil), []byte("\n")) {
						t.Fatal("lost CRLF", path)
					}
				}
				p, err := compileStudioSource(s.path, after[s.path])
				if err != nil {
					t.Fatal(err)
				}
				var line, answer project.Pattern
				for _, pattern := range p.Patterns {
					if pattern.ID == "line" {
						line = pattern
					}
					if pattern.ID == "answer" {
						answer = pattern
					}
				}
				if timing == "played" {
					lead, keys := line.Performance, answer.Performance
					if lead == nil || keys == nil || lead.Notes[0].Tick != 17 || keys.Notes[0].Tick != 259 || lead.Notes[0].Velocity != 103 || lead.Groups[0].Points[2].Tick != 3360 || lead.Notes[0].EndTick != 360 {
						t.Fatal("recording lost origin, dynamics, gates or shared tail", lead, keys)
					}
				} else {
					if line.Data[0].Note != 60 || line.Data[4].Note != 67 || answer.Data[1].Note != 64 || line.Expression[0].PitchCents != 75 {
						t.Fatal("grid changed its quantization or expression")
					}
				}
				if len(studioHistoryEdits(t, s.routes())) != 1 || s.history.pendingTakeRevision == "" {
					t.Fatal("take split history or missed its playback handoff")
				}
				if r := studioCall(t, s.routes(), "/api/record", request); r.Code != 409 {
					t.Fatal("unchanged entry allowed a stale peer take", r.Code)
				}
				for _, step := range []struct {
					action string
					files  map[string][]byte
				}{{"undo", before}, {"redo", after}} {
					if r := studioCall(t, s.routes(), "/api/"+step.action, studioEdit{Revision: studioRevision(after[s.path])}); r.Code != 200 {
						t.Fatal(step.action, r.Code, r.Body.String())
					}
					for path, want := range step.files {
						if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
							t.Fatal("history lost exact project bytes", step.action, path)
						}
					}
				}
				if err := s.shutdown(); err != nil {
					t.Fatal(err)
				}
				reopened, err := compileStudioSource(s.path, after[s.path])
				if err != nil || !reflect.DeepEqual(reopened.Patterns, p.Patterns) {
					t.Fatal("reopened take changed", err)
				}
			})
		}
	}
}

func TestRecordedMultiSourceTakeRefusesConflictsAndInvalidSecondDestination(t *testing.T) {
	isolateState(t)
	for _, timing := range []string{"grid", "played"} {
		for _, when := range []string{"before", "during", "invalid-second"} {
			t.Run(timing+"/"+when, func(t *testing.T) {
				s, before := recordingSourcesStudio(t, "\r\n")
				request := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: studioProjectReceipt(t, s.path), Timing: timing, Recordings: recordingSourcesTake(timing)}
				path := filepath.Join(filepath.Dir(s.path), "keys.cicada")
				outside := append(bytes.Clone(before[path]), []byte("// outside keys save\r\n")...)
				write := func() {
					if err := os.WriteFile(path, outside, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if when == "before" {
					write()
				} else if when == "during" {
					s.afterStage = write
				} else {
					request.Recordings[1].Notes[0].Note = 128
				}
				r := studioCall(t, s.routes(), "/api/record", request)
				want := 409
				if when == "invalid-second" {
					want = 422
				} else {
					before[path] = outside
				}
				if r.Code != want {
					t.Fatal("recording failure not atomic", r.Code, r.Body.String())
				}
				for path, want := range before {
					if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
						t.Fatal("refused take lost a source", path)
					}
				}
				if len(s.history.edits) != 0 || s.history.pendingTakeRevision != "" {
					t.Fatal("refused take entered history or armed playback")
				}
			})
		}
	}
}
