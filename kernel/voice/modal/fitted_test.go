package modal

import (
	"math"
	"testing"
)

func fittedFixture() Model {
	return Model{Count: 3, Modes: [MaxModes]Mode{{1, .6, .6}, {2.71, .3, .3}, {4.95, .1, .18}}, NoiseMix: .05}
}

func TestFittedVoiceMeasuredPolesAndNoAllocations(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		v, err := NewFittedVoice(fittedFixture(), rate)
		if err != nil {
			t.Fatal(err)
		}
		for note := 0; note <= 127; note++ {
			v.Reset()
			v.NoteOn(uint8(note), 127, false)
			s := &v.strikes[v.latest]
			for i := 0; i < s.count; i++ {
				m := s.modes[i]
				want := 440 * math.Exp2(float64(note-69)/12) * v.model.Modes[i].Ratio
				if math.Abs(m.freq-want) > 1e-8 || math.Abs(-math.Log(1000)/(math.Log(m.radius)*float64(rate))-v.model.Modes[i].T60) > 1e-8 {
					t.Fatal("measured poles changed", note, i)
				}
			}
			for i := 0; i < 256; i++ {
				if x := float64(v.Next()); math.IsNaN(x) || math.IsInf(x, 0) {
					t.Fatal("nonfinite voice", note)
				}
			}
		}
		if allocs := testing.AllocsPerRun(100, func() {
			v.Reset()
			v.NoteOn(60, 80, false)
			for i := 0; i < 128; i++ {
				v.Next()
			}
			v.NoteOn(72, 100, true)
			v.NoteOff()
		}); allocs != 0 {
			t.Fatal("modal render allocations", allocs)
		}
		v.ResetVariation(2)
		v.NoteOn(60, 80, false)
		first := v.Next()
		v.ResetVariation(2)
		v.NoteOn(60, 80, false)
		if first != v.Next() {
			t.Fatal("nonrepeatable variation")
		}
		v.Reset()
		v.NoteOn(60, 0, false)
		if v.ActiveVoices() != 0 {
			t.Fatal("zero velocity strike")
		}
	}
}

func TestFittedVoiceRejectsInvalidModel(t *testing.T) {
	for _, m := range []Model{{}, {Count: 11}, {Count: 1, Modes: [MaxModes]Mode{{1, 1, 0}}}, {Count: 1, Modes: [MaxModes]Mode{{math.NaN(), 1, .2}}}, {Count: 1, Modes: [MaxModes]Mode{{1, 1, .2}}, NoiseMix: math.NaN()}} {
		if _, err := NewFittedVoice(m, 48000); err == nil {
			t.Fatal("invalid model accepted", m)
		}
	}
}
