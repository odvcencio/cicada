package edit_test

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestRecordedDestinationsAccumulateAcrossOwnedFiles(t *testing.T) {
	for _, timing := range []string{"grid", "played"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(timing+fmt.Sprintf("/%q", newline), func(t *testing.T) {
				entry := []byte(strings.ReplaceAll("cicada 2\ntempo 120\nkey d minor\n// entry is not a pattern buffer\nscene main { lead=line keys=answer }\nsong { main*4 }\n", "\n", newline))
				parts := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "lead.cicada", Source: []byte(strings.ReplaceAll("instrument glass { voice mono { out=sine(pitch)*env(gate,200ms)*(0.1+pressure*0.1+timbre*0.1) } }\ntrack lead glass {}\n// lead sentinel\npattern line notes steps=8 { . . . . . . . . } // retain lead comment\n", "\n", newline))}, {Path: "keys.cicada", Source: []byte(strings.ReplaceAll("track keys piano {}\n// keys sentinel\npattern answer notes steps=8 { . . . . . . . . } // retain keys comment\n", "\n", newline))}}
				before := map[string][]byte{}
				for _, f := range parts {
					before[f.Path] = bytes.Clone(f.Source)
				}
				compiler := multiFileCompiler{path: "main.cicada", files: parts}
				takes := []edit.Recording{{Track: "lead", Pattern: "line", Notes: []edit.TakeNote{{Tick: 3857, EndTick: 4200, Note: 60, Velocity: 103, Expressions: []edit.TakeExpression{{Tick: 3857, PitchCents: 75, Pressure: .6, Timbre: .4}}}, {Tick: 4817, EndTick: 5160, Note: 67, Velocity: 87}}}, {Track: "keys", Pattern: "answer", Notes: []edit.TakeNote{{Tick: 4099, EndTick: 4340, Note: 64, Velocity: 95}, {Tick: 5059, EndTick: 5300, Note: 69, Velocity: 78}}}}
				result, err := edit.Apply(entry, edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.RecordTake{Timing: timing, Recordings: takes}}}, edit.Options{Path: compiler.path, Edition: 2, Sources: parts, Compiler: compiler, ParseProject: compiler.parse})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(result.Source, entry) || len(result.Files) != 2 {
					t.Fatal("recording changed the entry or lost a destination", result)
				}
				for _, f := range result.Files {
					if !bytes.Equal(f.Before, before[f.Path]) || !bytes.Contains(f.After, []byte("retain ")) {
						t.Fatal("lost original buffer or comments", f.Path)
					}
					if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(f.After, []byte(newline), nil), []byte("\n")) {
						t.Fatal("lost CRLF", f.Path)
					}
				}
				for _, f := range parts {
					if !bytes.Equal(f.Source, before[f.Path]) {
						t.Fatal("mutated caller source", f.Path)
					}
				}
				p := result.Plan.Patterns
				if timing == "played" {
					if p[0].Performance == nil || p[1].Performance == nil || p[0].Performance.Notes[0].Tick != 17 || p[1].Performance.Notes[0].Tick != 259 || p[0].Performance.Notes[0].Expressions[0].Params.PitchCents != 75 {
						t.Fatal("played recording lost shared origin or member values", p)
					}
				} else {
					if p[0].Performance != nil || p[0].Data[0].Note != 60 || p[0].Data[4].Note != 67 || p[0].Expression[0].PitchCents != 75 || p[1].Data[1].Note != 64 || p[1].Data[5].Note != 69 {
						t.Fatal("grid recording lost global compilation, expression or destinations", p)
					}
				}
				bad := append([]edit.Recording(nil), takes...)
				bad[1].Notes = []edit.TakeNote{{Note: 128, Velocity: 96, EndTick: 240}}
				refused, err := edit.Apply(entry, edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.RecordTake{Timing: timing, Recordings: bad}}}, edit.Options{Path: compiler.path, Edition: 2, Sources: parts, Compiler: compiler, ParseProject: compiler.parse})
				if err == nil || refused != nil {
					t.Fatal("invalid second destination produced a partial recording", err)
				}
				for _, f := range parts {
					if !reflect.DeepEqual(f.Source, before[f.Path]) {
						t.Fatal("refused recording mutated a caller buffer", f.Path)
					}
				}
			})
		}
	}
}

func TestRecordedDrumGridUsesItsOwnedBuffer(t *testing.T) {
	entry := []byte("cicada 2\ntrack drums drums {}\nscene main { drums=beat }\nsong { main }\n")
	part := []byte("// drum owner\r\npattern beat drums steps=4 { bd: .... sd: .... } // keep lanes\r\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "drums.cicada", Source: part}}
	c := multiFileCompiler{path: "main.cicada", files: files}
	r, err := edit.Apply(entry, edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.RecordTake{Recordings: []edit.Recording{{Track: "drums", Pattern: "beat", Notes: []edit.TakeNote{{Note: 36, Velocity: 96, EndTick: 120}, {Tick: 480, EndTick: 600, Note: 38, Velocity: 80}}}}}}}, edit.Options{Path: c.path, Edition: 2, Sources: files, Compiler: c, ParseProject: c.parse})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r.Source, entry) || len(r.Files) != 1 || r.Files[0].Path != "drums.cicada" || !bytes.Equal(r.Files[0].Before, part) || !bytes.Contains(r.Files[0].After, []byte("// keep lanes\r\n")) {
		t.Fatal("drum recording lost its owner or comments", r)
	}
	p := r.Plan.Patterns[0]
	if p.Lanes["bd"][0] == nil || p.Lanes["sd"][2] == nil {
		t.Fatal("cross-file drum grid lost a lane", p)
	}
}

func TestRecordedLibrarySourcesRequireLocalVariations(t *testing.T) {
	entry := []byte("cicada 2\ntrack keys piano {}\nscene main { keys=motif.phrase }\nsong { main }\n")
	part := []byte("pattern phrase notes steps=4 { . . . . }\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "motif.cicada", Library: "motif", Source: part}}
	c := multiFileCompiler{path: "main.cicada", files: files}
	for _, timing := range []string{"grid", "played"} {
		r, err := edit.Apply(entry, edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.RecordTake{Timing: timing, Recordings: []edit.Recording{{Track: "keys", Pattern: "motif.phrase", Notes: []edit.TakeNote{{Note: 60, Velocity: 96, EndTick: 120}}}}}}}, edit.Options{Path: c.path, Edition: 2, Sources: files, Compiler: c, ParseProject: c.parse})
		if err == nil || !strings.Contains(err.Error(), "library pattern") || r != nil || !bytes.Equal(files[0].Source, entry) || !bytes.Equal(files[1].Source, part) {
			t.Fatal("recording did not protect an imported library", timing, err)
		}
	}
}
