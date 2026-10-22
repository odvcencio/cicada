package edit_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestRecordingIntoUnusedLibraryPatternKeepsSongAndSlots(t *testing.T) {
	for _, timing := range []string{"grid", "played"} {
		t.Run(timing, func(t *testing.T) {
			source := []byte("cicada 2\ntrack keys piano {}\npattern song-part notes { c4 . . . }\npattern fresh notes steps=16 { . . . . . . . . . . . . . . . . } // keep my library idea\nscene main { keys=song-part }\nsong { main*4 }\n")
			before := bytes.Clone(source)
			result, err := applyM5(t, source, &edit.RecordTake{Timing: timing, Recordings: []edit.Recording{{Track: "keys", Pattern: "fresh", Notes: []edit.TakeNote{{Tick: 3857, EndTick: 4200, Note: 60, Velocity: 90}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(source, before) || !bytes.Contains(result.Source, []byte("scene main { keys=song-part }\nsong { main*4 }")) || !bytes.Contains(result.Source, []byte("// keep my library idea")) {
				t.Fatal("recording changed the song or source ownership")
			}
			if strings.Contains(string(result.Source), "slot=") {
				t.Fatal("recording allocated an implicit launch slot")
			}
			for _, slot := range result.Plan.Tracks[0].Slots {
				if slot != nil && *slot == "fresh" {
					t.Fatal("library recording acquired a song binding")
				}
			}
			found := false
			for _, p := range result.Plan.Patterns {
				if p.ID == "fresh" {
					found = timing == "played" && p.Performance != nil || timing == "grid" && p.Data[0] != nil
				}
			}
			if !found {
				t.Fatal("recorded material was not retained")
			}
		})
	}
}

func TestUnusedRecordingRechecksCapturedSoundCompatibilityAtomically(t *testing.T) {
	for _, tc := range []struct {
		name, track string
		notes       []edit.TakeNote
	}{
		{"keyboard range", "piano", []edit.TakeNote{{Note: 120, Velocity: 90, EndTick: 240}}},
		{"keyboard expression", "piano", []edit.TakeNote{{Note: 60, Velocity: 90, EndTick: 240, Expressions: []edit.TakeExpression{{PitchCents: 50}}}}},
		{"drum performance", "drums", []edit.TakeNote{{Note: 36, Velocity: 90, EndTick: 240}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, pattern := "notes", ". . . ."
			if tc.track == "drums" {
				kind, pattern = "drums", "bd: ...."
			}
			source := []byte("cicada 2\ntrack target " + tc.track + " {}\npattern fresh " + kind + " { " + pattern + " }\nscene main {}\nsong { main*4 }\n")
			before := bytes.Clone(source)
			result, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{{Track: "target", Pattern: "fresh", Notes: tc.notes}}})
			if err == nil || result != nil || !bytes.Equal(source, before) {
				t.Fatalf("unsupported unused recording was applied: %v %+v", err, result)
			}
		})
	}
	for _, timing := range []string{"grid", "played"} {
		source := []byte("cicada 2\ntrack first piano {}\ntrack second piano {}\npattern owned notes { c4 . }\nscene main { second=owned }\nsong { main }\n")
		result, err := applyM5(t, source, &edit.RecordTake{Timing: timing, Recordings: []edit.Recording{{Track: "first", Pattern: "owned", Notes: []edit.TakeNote{{Note: 60, Velocity: 90, EndTick: 240}}}}})
		if err == nil || result != nil {
			t.Fatal("recording silently took another track's pattern", timing)
		}
	}
}

func TestUnusedDrumGridRecordingNeedsNoSceneAssignment(t *testing.T) {
	source := []byte("cicada 2\ntrack drums drums {}\npattern fresh drums steps=4 { bd: .... sd: .... }\nscene main {}\nsong { main }\n")
	result, err := applyM5(t, source, &edit.RecordTake{Recordings: []edit.Recording{{Track: "drums", Pattern: "fresh", Notes: []edit.TakeNote{{Note: 36, Velocity: 96, EndTick: 120}, {Tick: 480, EndTick: 600, Note: 38, Velocity: 80}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.Source, []byte("scene main {}")) || result.Plan.Patterns[0].Lanes["bd"][0] == nil || result.Plan.Patterns[0].Lanes["sd"][2] == nil {
		t.Fatal("unused drum material or scene changed")
	}
}

func TestLibraryRecordingDoesNotNeedAFreeLaunchSlot(t *testing.T) {
	var source strings.Builder
	source.WriteString("cicada 2\ntrack keys piano {}\n")
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&source, "pattern p%d notes { c4 . }\nscene s%d { keys=p%d }\n", i, i, i)
	}
	source.WriteString("pattern fresh notes { . . }\nsong { s0 }\n")
	result, err := applyM5(t, []byte(source.String()), &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{{Track: "keys", Pattern: "fresh", Notes: []edit.TakeNote{{Note: 64, Velocity: 100, EndTick: 480}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range result.Plan.Tracks[0].Slots {
		if slot == nil || *slot == "fresh" {
			t.Fatal("existing sixteen launch slots changed")
		}
	}
}

func TestUnusedLibraryRecordingStagesItsOwnedProjectSource(t *testing.T) {
	entry := []byte("cicada 2\n// entry stays exactly the same\n")
	part := []byte("// keep library file\r\ntrack keys piano {}\r\npattern fresh notes steps=4 { . . . . } // keep this comment\r\nscene main {}\r\nsong { main*4 }\r\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "parts.cicada", Source: part}}
	for _, timing := range []string{"grid", "played"} {
		for _, owner := range []bool{false, true} {
			path, input := "main.cicada", entry
			if owner {
				path, input = "parts.cicada", part
			}
			before := bytes.Clone(input)
			compiler := multiFileCompiler{path: path, files: files}
			result, err := edit.Apply(input, edit.Envelope{Version: edit.EnvelopeVersion, Intents: []edit.Intent{&edit.RecordTake{Timing: timing, Recordings: []edit.Recording{{Track: "keys", Pattern: "fresh", Notes: []edit.TakeNote{{Note: 60, Velocity: 90, EndTick: 240}}}}}}}, edit.Options{Path: path, Sources: files, Compiler: compiler, ParseProject: compiler.parse, Edition: 2})
			if !bytes.Equal(input, before) {
				t.Fatal("recording mutated an input buffer")
			}
			if err != nil {
				t.Fatal(timing, err)
			}
			changed := result.Source
			if !owner {
				if !bytes.Equal(result.Source, entry) || len(result.Files) != 1 || result.Files[0].Path != "parts.cicada" || !bytes.Equal(result.Files[0].Before, part) {
					t.Fatal("recording lost its owning file", timing)
				}
				changed = result.Files[0].After
			} else if len(result.Files) != 0 {
				t.Fatal("owner recording staged unrelated files", timing)
			}
			if !bytes.Contains(changed, []byte("// keep this comment")) || !bytes.Equal(files[0].Source, entry) {
				t.Fatal("recording lost comments or mutated supplied source", timing)
			}
			if bytes.Contains(bytes.ReplaceAll(changed, []byte("\r\n"), nil), []byte("\n")) {
				t.Fatal("recording lost CRLF source formatting")
			}
		}
	}
}
