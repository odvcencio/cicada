package edit_test

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
)

const scalePerformanceScore = `cicada 2
title "Shape the phrase"
tempo 120
instrument glass { voice poly { out=sine(pitch)*env(gate,300ms) } }
track keys glass {}
// before the phrase
pattern phrase performance length=3840ticks slot=3 transpose=12 seed=99 {
 // keep its articulation
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=1ticks bend=10ct pressure=0.1 timbre=0.6;
  gesture at=2ticks bend=20ct pressure=0.2 timbre=0.7;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
  gesture at=3839ticks bend=-50ct pressure=0.3 timbre=0.4;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch {
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7 vibrato=12ct rate=7Hz;
  gesture at=1ticks bend=50ct pressure=0.3 timbre=0.8;
  gesture at=2ticks bend=75ct pressure=0.4 timbre=0.9;
  gesture at=703ticks bend=100ct pressure=0.5 timbre=0.6;
 }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
} // outside the phrase
arrange { place first keys phrase { at=@1.1.1 length=2bars } }
`

func TestScalePerformanceMovesAbsoluteNotesAndBothGestureLayersTogether(t *testing.T) {
	for _, check := range []struct {
		n, d                int
		length, start, end  int64
		expressions, shared []int64
		firstBend           float32
	}{
		{2, 1, 7680, 34, 1440, []int64{0, 2, 4, 1406}, []int64{0, 2, 4, 1920, 7678}, 25},
		{1, 2, 1920, 9, 360, []int64{0, 1, 351}, []int64{0, 1, 480, 1919}, 50},
		{2, 3, 2560, 11, 480, []int64{0, 1, 2, 469}, []int64{0, 1, 640, 2559}, 25},
		{3, 2, 5760, 26, 1080, []int64{0, 1, 3, 1054}, []int64{0, 2, 3, 1440, 5759}, 25},
	} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%d/%d/%q", check.n, check.d, newline), func(t *testing.T) {
				source := []byte(strings.ReplaceAll(scalePerformanceScore, "\n", newline))
				original := bytes.Clone(source)
				result, err := applyM5(t, source, &edit.ScalePerformance{Entity: "pattern:phrase", Numerator: check.n, Denominator: check.d})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(source, original) {
					t.Fatal("time scale mutated the input")
				}
				p := result.Plan.Patterns[0].Performance
				note := p.Notes[0]
				if p.LengthTicks != check.length || note.Tick != check.start || note.EndTick != check.end || note.Note != 60 || note.Velocity != 103 || note.Group != 1 || p.Notes[1].Note != 64 || p.Notes[1].Velocity != 87 || p.Notes[1].Group != 1 {
					t.Fatal("changed notes or timing", p)
				}
				var expressions, shared []int64
				for _, point := range note.Expressions {
					expressions = append(expressions, point.Tick)
				}
				for _, point := range p.Groups[0].Points {
					shared = append(shared, point.Tick)
				}
				if !reflect.DeepEqual(expressions, check.expressions) || !reflect.DeepEqual(shared, check.shared) || note.Expressions[0].Params.PitchCents != check.firstBend || p.Groups[0].Points[len(shared)-1].Value.PitchCents != -50 {
					t.Fatal("gesture layers lost alignment, tails or later values", expressions, shared)
				}
				for _, keep := range []string{"tempo 120", "slot=3", "transpose=12", "seed=99", "// before the phrase", "// keep its articulation", "// outside the phrase", "length=2bars"} {
					if !bytes.Contains(result.Source, []byte(keep)) {
						t.Fatal("lost surrounding source or settings", keep)
					}
				}
				if check.firstBend == 25 && (note.Expressions[0].Params.VibratoRateHz != 7 || note.Expressions[0].Params.VibratoDepthCents != 12) {
					t.Fatal("time scaling changed vibrato parameters")
				}
				if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(result.Source, []byte(newline), nil), []byte("\n")) {
					t.Fatal("time scaling changed line endings")
				}
			})
		}
	}
}

func TestScalePerformanceRefusesInvalidScalesAndOverlappingCompressedNotes(t *testing.T) {
	for _, check := range []struct {
		name, source string
		n, d         int
	}{
		{"zero numerator", scalePerformanceScore, 0, 1},
		{"zero denominator", scalePerformanceScore, 1, 0},
		{"large numerator", scalePerformanceScore, 17, 1},
		{"large denominator", scalePerformanceScore, 1, 17},
		{"short loop", "track k piano {}\npattern p performance length=30ticks { note 60 at=0ticks length=1ticks velocity=96; }\n", 1, 2},
		{"large loop", "track k piano {}\npattern p performance length=1099511627776ticks { note 60 at=0ticks length=1ticks velocity=96; }\n", 2, 1},
		{"colliding gates", "track k piano {}\npattern p performance length=60ticks { note 60 at=1ticks length=1ticks velocity=96; note 60 at=2ticks length=1ticks velocity=96; }\n", 1, 2},
	} {
		t.Run(check.name, func(t *testing.T) {
			id := "p"
			if check.source == scalePerformanceScore {
				id = "phrase"
			}
			source := check.source
			if !strings.HasPrefix(source, "cicada 2\n") {
				source = "cicada 2\n" + source
			}
			if id == "p" {
				source += "arrange { place first k p { at=@1.1.1 length=1bar } }\n"
			}
			if _, err := (m5Compiler{}).Compile([]byte(source), nil); err != nil {
				t.Fatal("refusal fixture must compile before scaling", err)
			}
			want := "time scale needs"
			switch check.name {
			case "short loop", "large loop":
				want = "scaled loop must"
			case "colliding gates":
				want = "cannot retain the notes"
			}
			result, err := applyM5(t, []byte(source), &edit.ScalePerformance{Entity: edit.EntityID("pattern:" + id), Numerator: check.n, Denominator: check.d})
			if err == nil || result != nil || !strings.Contains(err.Error(), want) {
				t.Fatal("invalid time scale produced a candidate", err)
			}
		})
	}
}
