package edit_test

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestRecordedPerformanceKeepsChordsTimingVelocitiesAndComments(t *testing.T) {
	before := "title \"Keep this\"\ntrack keys piano {}\npattern take notes slot=3 transpose=12 { . // my phrase\n . . . } // outside\nscene main { keys=take }\nsong { main*8 }\n"
	for _, newline := range []string{"\n", "\r\n"} {
		source := []byte(strings.ReplaceAll(before, "\n", newline))
		result, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{{Track: "keys", Pattern: "take", Notes: []edit.TakeNote{
			{Tick: 3840 + 17, EndTick: 3840 + 720, Note: 60, Velocity: 82},
			{Tick: 3840 + 19, EndTick: 3840 + 1220, Note: 64, Velocity: 105},
		}}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, keep := range []string{"title \"Keep this\"", "// my phrase", "// outside", "slot=3", "note 60 at=17ticks length=703ticks velocity=82", "note 64 at=19ticks length=1201ticks velocity=105"} {
			if !bytes.Contains(result.Source, []byte(keep)) {
				t.Fatalf("performance lost %q\n%s", keep, result.Source)
			}
		}
		if bytes.Contains(result.Source, []byte("transpose=12")) {
			t.Fatal("captured MIDI pitches acquired an old pattern transpose")
		}
		if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(result.Source, []byte("\r\n"), nil), []byte("\n")) {
			t.Fatal("changed CRLF formatting")
		}
		score, ds := notation.Parse(result.Source)
		p, ds := project.FromScore(score)
		if p == nil {
			t.Fatal(ds)
		}
		performance := p.Patterns[0].Performance
		if performance == nil || performance.LengthTicks != 3840 || performance.Notes[1].Tick != 19 || performance.Notes[1].EndTick != 1220 || p.Tracks[0].Slots[3] == nil {
			t.Fatal("compiled performance changed", performance)
		}
	}
}

func TestRecordedPerformanceUsesOneOriginForEveryDestination(t *testing.T) {
	source := []byte("track one piano {}\ntrack two piano {}\npattern a { . . }\npattern b { . . }\nscene main { one=a two=b }\nsong { main*8 }\n")
	result, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{
		{Track: "one", Pattern: "a", Notes: []edit.TakeNote{{Tick: 3857, EndTick: 4200, Note: 60, Velocity: 90}}},
		{Track: "two", Pattern: "b", Notes: []edit.TakeNote{{Tick: 8001, EndTick: 8499, Note: 67, Velocity: 110}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"length=7680ticks", "note 60 at=17ticks length=343ticks velocity=90", "note 67 at=4161ticks length=498ticks velocity=110"} {
		if !bytes.Contains(result.Source, []byte(want)) {
			t.Fatalf("unaligned destinations: missing %s\n%s", want, result.Source)
		}
	}
}

func TestBuiltInPianoCanKeepTheStepGridRecordingChoice(t *testing.T) {
	source := []byte("track keys piano {}\npattern take notes { . . . . }\nscene main { keys=take }\nsong { main }\n")
	result, err := applyM5(t, source, &edit.RecordTake{Timing: "grid", Recordings: []edit.Recording{{Track: "keys", Pattern: "take", Notes: []edit.TakeNote{{Tick: 17, EndTick: 720, Note: 60, Velocity: 82}}}}})
	if err != nil {
		t.Fatal(err)
	}
	pattern := result.Plan.Patterns[0]
	if pattern.Performance != nil || pattern.Data[0] == nil || pattern.Data[0].Note != 60 {
		t.Fatal("piano grid choice did not create a grid note")
	}
}

func TestRecordedPerformanceRefusesUnsupportedOrCollidingDataAtomically(t *testing.T) {
	source := []byte("track keys piano {}\npattern take { . . }\nscene main { keys=take }\nsong { main }\n")
	for _, notes := range [][]edit.TakeNote{
		{{Tick: 0, EndTick: 120, Note: 60, Velocity: 90}, {Tick: 50, EndTick: 180, Note: 60, Velocity: 100}},
		{{Tick: 0, EndTick: 120, Note: 60, Velocity: 90, Expressions: []edit.TakeExpression{{PitchCents: 12}}}},
		{{Tick: 0, EndTick: 120, Note: 120, Velocity: 90}},
	} {
		if _, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{{Track: "keys", Pattern: "take", Notes: notes}}}); err == nil {
			t.Fatal("unrepresentable performance was accepted", notes)
		}
	}
}

func TestRecordedGesturesStayWithTheirNoteThroughTypedEdits(t *testing.T) {
	source := []byte("track lead acid {}\npattern take { . . . . }\nscene main { lead=take }\nsong { main*4 }\n")
	result, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{{Track: "lead", Pattern: "take", Notes: []edit.TakeNote{{Tick: 3857, EndTick: 4557, Note: 60, Velocity: 90, Expressions: []edit.TakeExpression{
		{Tick: 3857, PitchCents: 25, Pressure: .2, Timbre: .5},
		{Tick: 3977, PitchCents: 30, Pressure: .4, Timbre: .7},
		{Tick: 3977, PitchCents: 30, Pressure: .8, Timbre: .7},
	}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	p := result.Plan.Patterns[0].Performance
	if p == nil || p.Notes[0].Tick != 17 || len(p.Notes[0].Expressions) != 2 || p.Notes[0].Expressions[1].Tick != 120 || p.Notes[0].Expressions[1].Params.Pressure != .8 {
		t.Fatal("capture flattened or misplaced gestures", p)
	}
	edited, err := applyM5(t, result.Source, &edit.SetPerformedExpression{Entity: "pattern:take", NoteIndex: 0, Index: 1, Tick: 66, PitchCents: -12.5, Pressure: .6, Timbre: .9, VibratoDepthCents: 8, VibratoRateHz: 7})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := applyM5(t, edited.Source, &edit.SetPerformedNote{Entity: "pattern:take", Index: 0, Tick: 117, EndTick: 817, Pitch: 62, Velocity: 99})
	if err != nil {
		t.Fatal(err)
	}
	point := moved.Plan.Patterns[0].Performance.Notes[0].Expressions[1]
	if point.Tick != 66 || point.Params.PitchCents != -12.5 || point.Params.Pressure != .6 || point.Params.Timbre != .9 || point.Params.VibratoRateHz != 7 {
		t.Fatal("moving a note changed its gesture", point)
	}
	if _, err := applyM5(t, moved.Source, &edit.SetPerformedNote{Entity: "pattern:take", Index: 0, Tick: 117, EndTick: 118, Pitch: 62, Velocity: 99}); err == nil {
		t.Fatal("shortening a note discarded a gesture")
	}
}
