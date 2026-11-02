package edit_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const recordScore = "title \"Studio\"\ntrack bass acid {}\ntrack drums drums {}\npattern pulse acid steps=4 { 1 . 5 . }\npattern beat drums steps=4 { bd: x... sd: .... }\nscene main { bass=pulse drums=beat }\nsong { main }\n"

type m5Compiler struct{ edition int }

func (c m5Compiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	var score *notation.Score
	var ds []notation.Diagnostic
	if c.edition == 0 {
		score, ds = notation.Parse(source)
	} else {
		score, ds = notation.ParseEdition(source, c.edition)
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, errors.New(d.Message)
		}
	}
	score, ds = notation.ResolvePresets(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, errors.New(d.Message)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, errors.New(d.Message)
		}
	}
	return project.EditPlan(p, source), nil
}
func applyM5(t *testing.T, source []byte, in edits.Intent) (*edits.Result, error) {
	t.Helper()
	return edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, edits.Options{Compiler: m5Compiler{}})
}
func recordTakeSource(t *testing.T, source []byte, track, pattern string, notes []edits.TakeNote) ([]byte, error) {
	t.Helper()
	r, err := applyM5(t, source, &edits.RecordTake{Recordings: []edits.Recording{{Track: track, Pattern: pattern, Notes: notes}}})
	if err != nil {
		return nil, err
	}
	return r.Source, nil
}

func TestAddPresetExactBytes(t *testing.T) {
	before := "cicada 1\ntitle \"Keep my title\"\n// authored track\ntrack bass acid { cutoff = 723.25Hz } // preserve this\npattern p acid { 1 . 5 . }\n// scene annotation\nscene main { bass=p }\nsong { main*2 }\n"
	patch, _ := instrument.FindPatch("warm-pad")
	decl, err := patch.Source("my-pad")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(before, "scene main", decl+"\ntrack keys my-pad {}\n\nscene main", 1)
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(edits.Revision([]byte(newline))[:8], func(t *testing.T) {
			source := []byte(strings.ReplaceAll(before, "\n", newline))
			got, err := applyM5(t, source, &edits.AddPreset{Preset: "warm-pad", Instrument: "my-pad", Track: "keys"})
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Source) != strings.ReplaceAll(want, "\n", newline) || got.Label != "Add "+patch.Name+" instrument and track" {
				t.Fatalf("preset bytes/label: %s %s", got.Source, got.Label)
			}
		})
	}
}

func TestRecordTakeExactBytesAndAtomicBatch(t *testing.T) {
	notes := []edits.TakeNote{{Tick: 0, EndTick: 180, Note: 60, Velocity: 90}, {Tick: 120, EndTick: 240, Note: 64, Velocity: 100}}
	for _, newline := range []string{"\n", "\r\n"} {
		source := []byte(strings.ReplaceAll(recordScore, "\n", newline))
		r, err := applyM5(t, source, &edits.RecordTake{Recordings: []edits.Recording{{Track: "bass", Pattern: "pulse", Notes: notes}, {Track: "drums", Pattern: "beat", Notes: []edits.TakeNote{{Tick: 120, EndTick: 120, Note: 36, Velocity: 80}, {Tick: 120, EndTick: 120, Note: 38, Velocity: 127}}}}})
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(string(source), "{ 1 . 5 . }", "{ c4~ e4 5 . }", 1)
		want = strings.Replace(want, "bd: x... sd: ....", "bd: xx6.. sd: .X..", 1)
		if string(r.Source) != want || r.Label != "Take committed · 4 notes across 2 patterns" {
			t.Fatalf("take bytes/label: %s %s", r.Source, r.Label)
		}
		if _, err := applyM5(t, source, &edits.RecordTake{Recordings: []edits.Recording{{Track: "bass", Pattern: "pulse", Notes: notes}, {Track: "missing", Pattern: "beat", Notes: notes}}}); err == nil {
			t.Fatal("invalid batch accepted")
		}
		if !bytes.Equal(source, []byte(strings.ReplaceAll(recordScore, "\n", newline))) {
			t.Fatal("input mutated")
		}
	}
}

func TestRecordTakeUsesScoreOnsetDivision(t *testing.T) {
	base := "cicada 2\ntrack bass acid {}\nLIVE\npattern pulse acid steps=4 { . . . . }\nscene main { bass=pulse }\nsong { main }\n"
	for _, test := range []struct{ name, setting, want string }{
		{"default sixteenth", "live {}", "{ . c4 . . }"},
		{"configured eighth", "live { record = 1/8 }", "{ . . c4 . }"},
		{"triplet division", "live { record = 1/8T }", "{ . c4 . . }"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Replace(base, "LIVE", test.setting, 1))
			notes := []edits.TakeNote{{Tick: 260, EndTick: 350, Note: 60, Velocity: 90}}
			updated, err := recordTakeSource(t, source, "bass", "pulse", notes)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(updated), test.want) {
				t.Fatalf("got %s, want %s", updated, test.want)
			}
			if notes[0].Tick != 260 || notes[0].EndTick != 350 {
				t.Fatal("mutated retained take")
			}
		})
	}
}

func TestRecordTakeQuantizationRetainsOnConflictAndPlayedTiming(t *testing.T) {
	source := []byte("cicada 2\ntrack bass acid {}\nlive { record = 1/8 }\npattern pulse acid steps=4 { . . . . }\nscene main { bass=pulse }\nsong { main }\n")
	notes := []edits.TakeNote{{Tick: 100, EndTick: 130, Note: 60, Velocity: 90}, {Tick: 200, EndTick: 230, Note: 64, Velocity: 90}}
	before := append([]byte(nil), source...)
	if _, err := recordTakeSource(t, source, "bass", "pulse", notes); err == nil || !strings.Contains(err.Error(), "multiple notes") {
		t.Fatalf("expected collision after quantization: %v", err)
	}
	if !bytes.Equal(source, before) || notes[0].Tick != 100 || notes[1].Tick != 200 {
		t.Fatal("quantization conflict changed the source or retained take")
	}
	r, err := applyM5(t, source, &edits.RecordTake{Timing: "played", Recordings: []edits.Recording{{Track: "bass", Pattern: "pulse", Notes: notes}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"note 60 at=100ticks length=30ticks", "note 64 at=200ticks length=30ticks"} {
		if !strings.Contains(string(r.Source), want) {
			t.Fatalf("played timing lost %q: %s", want, r.Source)
		}
	}
}

func TestRecordTakeInheritedEdition(t *testing.T) {
	source := []byte("track part tine_ep { voices=2 }\npattern take notes steps=4 { . . . . }\nscene main { part=take }\nsong { main }\n")
	in := &edits.RecordTake{Recordings: []edits.Recording{{Track: "part", Pattern: "take", Notes: []edits.TakeNote{{Note: 60, Velocity: 90, EndTick: 60}}}}}
	r, err := edits.Apply(source, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, edits.Options{Edition: 2, Compiler: m5Compiler{edition: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Source) != strings.Replace(string(source), ". . . .", "c4 . . .", 1) {
		t.Fatalf("inherited bytes: %s", r.Source)
	}
}

func TestM5IntentEnvelopeRoundTrip(t *testing.T) {
	in := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{
		&edits.AddPreset{Preset: "warm-pad", Instrument: "pad", Track: "keys"},
		&edits.InsertLibraryItem{Track: "keys", ImportPath: "std/fx", Reference: "fx.delay", ItemKind: "effect", EffectKind: "delay", PresetInstance: "library-test"},
		&edits.SavePreset{Track: "keys", Name: "bright", Declaration: "preset bright {}"},
		&edits.RecordTake{Recordings: []edits.Recording{{Track: "bass", Pattern: "pulse", Notes: []edits.TakeNote{{Tick: 1, EndTick: 120, Note: 60, Velocity: 90, NoteID: 2, Expressions: []edits.TakeExpression{{Tick: 1, PitchCents: 20}}}}}}},
		&edits.PublishRecorded{Name: "captured", Declaration: "sampler captured {}", Scene: "main", Level: "-6dB", Note: "c4"},
		&edits.SelectTake{ID: "take-test", Track: "vox", Scene: "main", Rate: 48000, Channels: 1, Asset: edits.TakeAsset{Name: "take-test", Frames: 10}, StartFrame: 4},
	}}
	wire, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out edits.Envelope
	if err = json.Unmarshal(wire, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip lost intent fields: %s", wire)
	}
	for _, key := range []string{`"kind":"insertlibraryitem"`, `"itemkind":"effect"`, `"effectkind":"delay"`, `"endtick":120`, `"pitchcents":20`} {
		if !strings.Contains(string(wire), key) {
			t.Fatalf("missing public key %s: %s", key, wire)
		}
	}
}
