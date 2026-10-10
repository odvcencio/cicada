package edit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type multiFileCompiler struct {
	path  string
	files []notation.SourceFile
}

func (c multiFileCompiler) sources(source []byte, overrides map[string][]byte) []notation.SourceFile {
	files := append([]notation.SourceFile(nil), c.files...)
	for i := range files {
		if files[i].Path == c.path {
			files[i].Source = source
		} else if after, ok := overrides[files[i].Path]; ok {
			files[i].Source = after
		}
	}
	return files
}

func (c multiFileCompiler) parse(source []byte) (*notation.Score, []notation.Diagnostic, error) {
	score, ds := notation.ParseFiles(c.sources(source, nil), 2)
	return score, ds, nil
}

func (c multiFileCompiler) Compile(source []byte, overrides map[string][]byte) (*edits.Plan, error) {
	score, ds := notation.ParseFiles(c.sources(source, overrides), 2)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	score, ds = notation.ResolvePresets(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	return project.EditPlan(p, source, c.sources(source, overrides)...), nil
}

func TestEveryIntentRespectsMultiFileOwnership(t *testing.T) {
	patch, ok := instrument.FindPatch("warm-pad")
	if !ok {
		t.Fatal("warm-pad missing")
	}
	voice, err := patch.Source("voice")
	if err != nil {
		t.Fatal(err)
	}
	scene := "scene main { lead=melody keys=keys-line drums=beat vox=off lead.cutoff=700Hz }"
	part := "title \"Foreign\"\ntempo 120\nkey a minor\n" + voice + "\n" +
		"preset bright { instrument=acid cutoff=900Hz }\nfx grit drive {}\n" +
		"track lead acid {}\ntrack keys voice {}\ntrack drums drums {}\ntrack vox audio {}\n" +
		"asset wave \"recorded.wav\" { sha256=\"" + strings.Repeat("a", 64) + "\" format=wav frames=480 rate=48000Hz channels=1 }\nclip hit wave { start=0frames end=480frames }\n" +
		"pattern melody acid steps=4 { c4^ . . . }\npattern beat drums steps=4 { bd: x... }\n" +
		"pattern keys-line notes steps=4 { c4 . . . }\n" +
		"pattern empty acid steps=4 { . . . .\n bend: 0ct 0ct 0ct 0ct\n vibrato: 0ct 0ct 0ct 0ct\n pressure: 0 0 0 0\n timbre: 0.5 0.5 0.5 0.5\n}\n" +
		"scene recording { lead=empty }\n" + scene + "\nscene other { lead=melody }\nsong { main*2 other recording }\n"
	// Every foreign line/column lands in valid unrelated entry-file comments,
	// so compilation cannot mask a writer that silently uses those offsets.
	entry := "cicada 2\n" + strings.Repeat("// entry sentinel "+strings.Repeat("x", 180)+"\n", strings.Count(part, "\n")+5)
	declaration := "sampler captured { asset=wave root=c4 mode=oneshot voices=4 }"
	fixtures := map[string]edits.Intent{
		"togglestep":         &edits.ToggleStep{Entity: "step:melody/0", Shared: "definition"},
		"setpitch":           &edits.SetPitch{Entity: "step:melody/0", Pitch: 62, Shared: "definition"},
		"togglemodifier":     &edits.ToggleModifier{Entity: "step:melody/0", Modifier: "accent", Shared: "definition"},
		"cyclestep":          &edits.CycleStep{Entity: "step:melody/0", Field: "chance", Shared: "definition"},
		"setdrumvelocity":    &edits.SetDrumVelocity{Entity: "step:beat/bd/0", Velocity: 80, Shared: "definition"},
		"setpatternsettings": &edits.SetPatternSettings{Entity: "pattern:melody", Swing100: 5400, Gate: 50},
		"setstep":            &edits.SetStep{Entity: "step:melody/0", Mode: "note", Pitch: 62, Ratchet: 1, Chance: 100, Shared: "pattern"},
		"duplicatepattern":   &edits.DuplicatePattern{Entity: "pattern:melody", Name: "copy"},
		"setrange":           &edits.SetRange{Entity: "pattern:melody", Operation: "clear", First: 0, Last: 0, Shared: "pattern"},
		"resizepattern":      &edits.ResizePattern{Entity: "pattern:melody", Length: 8, Shared: "pattern"},
		"bindscene":          &edits.BindScene{Scene: "main", Track: "lead", Pattern: "empty"},
		"movesongentry":      &edits.MoveSongEntry{Entity: "song:0", Target: 1},
		"setsongbars":        &edits.SetSongBars{Entity: "song:0", Bars: 4},
		"appendsongentry":    &edits.AppendSongEntry{Scene: "other", Bars: 2},
		"duplicatesongentry": &edits.DuplicateSongEntry{Entity: "song:0"},
		"deletesongentry":    &edits.DeleteSongEntry{Entity: "song:0"},
		"setsongscene":       &edits.SetSongScene{Entity: "song:0", Scene: "other"},
		"setscenesetting":    &edits.SetSceneSetting{Entity: "setting:main/lead.cutoff", Value: json.RawMessage(`900`)},
		"removescenesetting": &edits.RemoveSceneSetting{Entity: "setting:main/lead.cutoff"},
		"setclipsettings":    &edits.SetClipSettings{Entity: "clip:hit", Start: 10, End: 400, GainDB: -2},
		"addaudiotrack":      &edits.AddAudioTrack{Name: "vox"},
		"setparam":           &edits.SetParam{Entity: "param:lead.level", Value: json.RawMessage(`-3`)},
		"addeffect":          &edits.AddEffect{Name: "grit", EffectKind: "drive"},
		"addpreset":          &edits.AddPreset{Preset: "warm-pad", Instrument: "voice", Track: "new-keys"},
		"insertlibraryitem":  &edits.InsertLibraryItem{Track: "lead", ImportPath: "part.cicada", Reference: "voice", ItemKind: "instrument"},
		"savepreset":         &edits.SavePreset{Track: "lead", Name: "bright", Declaration: "\npreset bright { instrument=acid cutoff=900Hz }\n"},
		"recordtake":         &edits.RecordTake{Recordings: []edits.Recording{{Track: "lead", Pattern: "empty", Notes: []edits.TakeNote{{Note: 60, Velocity: 90, EndTick: 120, Expressions: []edits.TakeExpression{{PitchCents: 50, Timbre: .5}}}}}}},
		"selecttake":         &edits.SelectTake{ID: "selected", Track: "vox", Scene: "main", Rate: 48000, Channels: 1, Asset: edits.TakeAsset{Name: "selected", Path: "selected.wav", SHA256: strings.Repeat("b", 64), Frames: 480, RateHz: 48000, Channels: 1}},
		"publishrecorded":    &edits.PublishRecorded{Name: "captured", Declaration: declaration, Scene: "main", ScenePath: "part.cicada", Level: "-6dB", Note: "c4"},
		"setprojectsettings": &edits.SetProjectSettings{Title: "Edited", TempoMilli: 130000, Root: "a", Scale: "minor"},
		"replacedeclaration": &edits.ReplaceDeclaration{Declaration: "pattern", Name: "melody", Text: "pattern melody acid steps=4 { d4 . . . }"},
		"replacetext":        &edits.ReplaceText{},
	}
	var kinds []string
	for _, kind := range edits.Kinds() {
		// These two constructors are registered only by the registry and
		// invalid-candidate unit tests, not by the production edit package.
		if kind != "probe" && kind != "break" {
			kinds = append(kinds, kind)
		}
	}
	if len(fixtures) != len(kinds) {
		t.Fatalf("%d fixture kinds for %d registered intents", len(fixtures), len(kinds))
	}
	for _, kind := range kinds {
		intent, ok := fixtures[kind]
		if !ok || intent.Kind() != kind {
			t.Fatalf("missing multi-file fixture for %s", kind)
		}
		for _, newline := range []string{"\n", "\r\n"} {
			for _, injected := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%q/host=%v", kind, newline, injected), func(t *testing.T) {
					main := []byte(strings.ReplaceAll(entry, "\n", newline))
					other := []byte(strings.ReplaceAll(part, "\n", newline))
					files := []notation.SourceFile{{Path: "main.cicada", Source: main}, {Path: "part.cicada", Source: other}}
					compiler := multiFileCompiler{path: "main.cicada", files: files}
					input := main
					in := intent
					var want []byte
					if kind == "replacetext" {
						// Whole-buffer saves select their file through Options.Path.
						compiler.path, input = "part.cicada", other
						want = bytes.Replace(other, []byte("Foreign"), []byte("Edited"), 1)
						in = &edits.ReplaceText{Source: string(want)}
					}
					opts := edits.Options{Compiler: compiler, Path: compiler.path, Edition: 2, Sources: files}
					if injected {
						opts.ParseProject = compiler.parse
					}
					if _, err := compiler.Compile(input, nil); err != nil {
						t.Fatalf("invalid generated fixture: %v", err)
					}
					before := bytes.Clone(input)
					filesBefore := make([]notation.SourceFile, len(files))
					for i, file := range files {
						filesBefore[i] = file
						filesBefore[i].Source = bytes.Clone(file.Source)
					}
					result, err := edits.Apply(input, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, opts)
					if !bytes.Equal(input, before) || !reflect.DeepEqual(files, filesBefore) {
						t.Fatal("intent mutated a supplied source buffer")
					}
					switch kind {
					case "publishrecorded":
						wantPart := strings.Replace(string(other), scene, strings.TrimSuffix(scene, "}")+newline+"  captured_track = captured_taps"+newline+"}", 1)
						if err != nil || result == nil || !bytes.HasPrefix(result.Source, main) || len(result.Files) != 1 || result.Files[0].Path != "part.cicada" || !bytes.Equal(result.Files[0].Before, other) || string(result.Files[0].After) != wantPart {
							t.Fatalf("publication did not edit the owning scene file: candidate=%v error=%v", result != nil, err)
						}
						bound := false
						for _, track := range result.Plan.Tracks {
							for _, slot := range track.Slots {
								bound = bound || track.ID == "captured_track" && slot != nil && *slot == "captured_taps"
							}
						}
						if !bound {
							t.Fatal("published track has no compiled scene binding")
						}
					case "replacetext":
						if err != nil || result == nil || !bytes.Equal(result.Source, want) || len(result.Files) != 0 {
							t.Fatalf("buffer save did not edit the selected owning file: candidate=%v error=%v", result != nil, err)
						}
					default:
						if err == nil || result != nil {
							t.Fatalf("foreign target must be refused without a candidate: candidate=%v error=%v", result != nil, err)
						}
					}
				})
			}
		}
	}
}
