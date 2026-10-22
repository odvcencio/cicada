package edit_test

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
)

func TestShapePerformancePitchPreservesTimingAndBothGestureLayers(t *testing.T) {
	for _, check := range []struct {
		intent  edit.ShapePerformancePitch
		pitches [2]uint8
	}{
		{edit.ShapePerformancePitch{Operation: "transpose", Semitones: 12}, [2]uint8{72, 76}},
		{edit.ShapePerformancePitch{Operation: "transpose", Semitones: -7}, [2]uint8{53, 57}},
		{edit.ShapePerformancePitch{Operation: "reflect", Pivot: 60}, [2]uint8{60, 56}},
		{edit.ShapePerformancePitch{Operation: "reflect", Pivot: 64}, [2]uint8{68, 64}},
	} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%s/%d/%d/%q", check.intent.Operation, check.intent.Semitones, check.intent.Pivot, newline), func(t *testing.T) {
				source := []byte(strings.ReplaceAll(scalePerformanceScore, "\n", newline))
				original := bytes.Clone(source)
				before, err := (m5Compiler{}).Compile(source, nil)
				if err != nil {
					t.Fatal(err)
				}
				in := check.intent
				in.Entity = "pattern:phrase"
				result, err := applyM5(t, source, &in)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(source, original) {
					t.Fatal("pitch shaping mutated its input")
				}
				got := result.Plan.Patterns[0].Performance
				for i, pitch := range check.pitches {
					if got.Notes[i].Note != pitch {
						t.Fatal("wrong shaped pitch", i, got.Notes[i].Note, pitch)
					}
				}
				// Restore only the pitches, then compare every timing, expression,
				// membership, vibrato and dynamic field against the original.
				for i := range got.Notes {
					got.Notes[i].Note = before.Patterns[0].Performance.Notes[i].Note
				}
				if !reflect.DeepEqual(got, before.Patterns[0].Performance) {
					t.Fatal("pitch shaping changed articulation", got)
				}
				for _, keep := range []string{"tempo 120", "slot=3", "transpose=12", "seed=99", "// before the phrase", "// keep its articulation", "// outside the phrase", "length=2bars"} {
					if !bytes.Contains(result.Source, []byte(keep)) {
						t.Fatal("lost surrounding source or settings", keep)
					}
				}
				if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(result.Source, []byte(newline), nil), []byte("\n")) {
					t.Fatal("changed line endings")
				}
				inverse := in
				inverse.Semitones = -in.Semitones
				restored, err := applyM5(t, result.Source, &inverse)
				if err != nil || !reflect.DeepEqual(restored.Plan.Patterns[0].Performance, before.Patterns[0].Performance) || restored.Plan.Patterns[0].Transpose != before.Patterns[0].Transpose {
					t.Fatal("inverse did not restore musical material", err)
				}
			})
		}
	}
}

func TestShapePerformancePitchRefusesOutOfRangeAndAmbiguousOperations(t *testing.T) {
	for _, in := range []edit.ShapePerformancePitch{
		{Operation: "transpose", Semitones: 0}, {Operation: "transpose", Semitones: 49}, {Operation: "transpose", Semitones: -49},
		{Operation: "transpose", Semitones: 48}, // a high-note fixture below checks retained +12 too
		{Operation: "transpose", Semitones: 1, Pivot: 60},
		{Operation: "reflect", Pivot: -1}, {Operation: "reflect", Pivot: 128},
		{Operation: "reflect", Pivot: 0}, {Operation: "reflect", Pivot: 127},
		{Operation: "reflect", Pivot: 60, Semitones: 1}, {Operation: "reverse"},
	} {
		t.Run(fmt.Sprintf("%s/%d/%d", in.Operation, in.Semitones, in.Pivot), func(t *testing.T) {
			in.Entity = "pattern:phrase"
			source := []byte(scalePerformanceScore)
			if in.Operation == "transpose" && in.Semitones == 48 {
				source = bytes.Replace(source, []byte("note 64 at="), []byte("note 68 at="), 1)
			}
			original := bytes.Clone(source)
			result, err := applyM5(t, source, &in)
			if err == nil || result != nil || !bytes.Equal(source, original) {
				t.Fatal("invalid pitch shaping produced a candidate", err)
			}
		})
	}
}

func TestUnusedPitchShapingChecksSoundingLimitsWithoutClamping(t *testing.T) {
	for _, check := range []struct {
		name                            string
		transpose, first, second, shift int
		want                            bool
	}{
		{"lowest sounding pitch", -12, 60, 64, -48, true},
		{"below sounding range", -12, 59, 64, -48, false},
		{"highest sounding pitch", 12, 60, 67, 48, true},
		{"above sounding range", 12, 60, 68, 48, false},
	} {
		t.Run(check.name, func(t *testing.T) {
			source := fmt.Sprintf("cicada 2\ntrack keys piano {}\npattern phrase performance length=3840ticks transpose=%d { note %d at=0ticks length=240ticks velocity=96; note %d at=960ticks length=240ticks velocity=87; }\npattern backing notes { c4 . . . }\nscene main { keys=backing }\nsong { main }\n", check.transpose, check.first, check.second)
			if _, err := (m5Compiler{}).Compile([]byte(source), nil); err != nil {
				t.Fatal("initial unused phrase must compile", err)
			}
			result, err := applyM5(t, []byte(source), &edit.ShapePerformancePitch{Entity: "pattern:phrase", Operation: "transpose", Semitones: check.shift})
			if !check.want {
				if err == nil || result != nil || !strings.Contains(err.Error(), "outside MIDI") {
					t.Fatal("unused phrase bypassed sounding limits", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := result.Plan.Patterns[0]
			if int(p.Performance.Notes[0].Note) != check.first+check.shift || int(p.Performance.Notes[1].Note) != check.second+check.shift || int(p.Transpose) != check.transpose {
				t.Fatal("boundary pitches were clamped or settings changed", p)
			}
		})
	}
}
