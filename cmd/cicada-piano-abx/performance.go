package main

import (
	"math"
	"sort"
)

func performances() []fixture {
	frame := func(seconds float64) int { return int(math.Round(seconds * outputRate)) }
	makeClip := func(id, prompt string, duration float64) fixture {
		return fixture{ID: id, Prompt: prompt, Frames: frame(duration)}
	}
	note := func(f *fixture, at, duration float64, n, v uint8) {
		f.Events = append(f.Events, event{Frame: frame(at), Kind: "on", Note: n, Velocity: v}, event{Frame: frame(at + duration), Kind: "off", Note: n})
	}
	var result []fixture
	for _, spec := range []struct {
		id, prompt string
		notes      []uint8
	}{
		{"01-low", "Low register attacks and damping", []uint8{24, 36, 43}},
		{"02-middle", "Middle register attacks and damping", []uint8{48, 60, 72}},
		{"03-high", "High register attacks and damping", []uint8{84, 96, 105}},
	} {
		f := makeClip(spec.id, spec.prompt, 8)
		for i, n := range spec.notes {
			note(&f, .25+float64(i)*1.8, 1, n, 88)
		}
		result = append(result, f)
	}
	f := makeClip("04-dynamics", "One pitch at four MIDI velocities", 8)
	for i, v := range []uint8{24, 56, 88, 120} {
		note(&f, .25+float64(i)*1.25, .75, 60, v)
	}
	result = append(result, f)
	f = makeClip("05-repeated", "Repeated notes and short articulated chords", 7)
	for i := 0; i < 8; i++ {
		note(&f, .25+float64(i)*.22, .14, 60, uint8(56+(i%4)*16))
	}
	for i, n := range []uint8{48, 55, 60, 64, 67} {
		note(&f, 2.7, .5, n, uint8(72+i*4))
	}
	result = append(result, f)
	for _, pedal := range []bool{false, true} {
		id, prompt := "06-chords-dry", "Chords with pedal released"
		if pedal {
			id, prompt = "07-chords-pedal", "The same chords with pedal held and released"
		}
		f = makeClip(id, prompt, 9)
		if pedal {
			f.Events = append(f.Events, event{Frame: frame(.1), Kind: "pedal", Pedal: 1}, event{Frame: frame(5.5), Kind: "pedal", Pedal: 0})
		}
		for c, notes := range [][]uint8{{48, 55, 60, 64}, {50, 57, 62, 65}} {
			for k, n := range notes {
				note(&f, .3+float64(c)*2.2+float64(k)*.015, .8, n, uint8(72+k*8))
			}
		}
		result = append(result, f)
	}
	for i := range result {
		sort.SliceStable(result[i].Events, func(a, b int) bool { return result[i].Events[a].Frame < result[i].Events[b].Frame })
	}
	return result
}
