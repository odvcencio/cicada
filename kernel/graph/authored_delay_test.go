package graph_test

import (
	"math"
	"os"
	"strconv"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

func authoredDelayPrograms(tb testing.TB) []graph.Program {
	tb.Helper()
	source, err := os.ReadFile("../../examples/pluck.cicada")
	if err != nil {
		tb.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		tb.Fatalf("parse: %+v", ds)
	}
	var programs []graph.Program
	for _, definition := range score.Instruments {
		p, ds := instrument.Compile(definition)
		if p == nil || len(ds) != 0 {
			tb.Fatalf("compile: %+v", ds)
		}
		lowered, err := instrument.Lower(p, nil)
		if err != nil {
			tb.Fatal(err)
		}
		programs = append(programs, lowered)
	}
	return programs
}

func TestAuthoredPluckTuning(t *testing.T) {
	p := authoredDelayPrograms(t)[0]
	for _, rate := range []int{44_100, 48_000} {
		for _, note := range []uint8{33, 45, 57, 69, 81} {
			v, err := graph.NewVoice(p, rate)
			if err != nil {
				t.Fatal(err)
			}
			v.NoteOn(note, 127, false)
			for range rate / 10 {
				v.Next()
			}
			window := make([]float64, rate/2)
			for i := range window {
				window[i] = float64(v.Next()) * (.5 - .5*math.Cos(2*math.Pi*float64(i)/float64(len(window)-1)))
			}
			want := 440 * math.Exp2(float64(int(note)-69)/12)
			// Maximize the Hann-windowed fundamental's spectrum locally. The
			// search uses actual rendered noise excitation, not filter coefficients.
			lo, hi := want*.99, want*1.01
			for range 64 {
				left, right := lo+(hi-lo)/3, hi-(hi-lo)/3
				if spectralPower(window, left, rate) < spectralPower(window, right, rate) {
					lo = left
				} else {
					hi = right
				}
			}
			measured := (lo + hi) / 2
			cents := 1200 * math.Log2(measured/want)
			t.Logf("METRIC: pluck tuning | rate=%d Hz pitch=%.0f Hz measured=%.9f Hz cents=%+.6f", rate, want, measured, cents)
			if math.Abs(cents) > 1 {
				t.Fatalf("pluck tuning error %g cents", cents)
			}
		}
	}
}

func spectralPower(window []float64, frequency float64, rate int) float64 {
	step := 2 * math.Pi * frequency / float64(rate)
	c, s := math.Cos(step), math.Sin(step)
	x, y, real, imaginary := 1.0, 0.0, 0.0, 0.0
	for _, value := range window {
		real += value * x
		imaginary += value * y
		x, y = x*c-y*s, x*s+y*c
	}
	return real*real + imaginary*imaginary
}

var sampleSink float32

func BenchmarkAuthoredDelayVoice(b *testing.B) {
	programs := authoredDelayPrograms(b)
	for i, name := range []string{"pluck", "slapback"} {
		for _, rate := range []int{44_100, 48_000} {
			b.Run(name+"/"+strconv.Itoa(rate), func(b *testing.B) {
				v, err := graph.NewVoice(programs[i], rate)
				if err != nil {
					b.Fatal(err)
				}
				v.NoteOn(69, 127, false)
				b.ReportAllocs()
				b.ResetTimer()
				for j := 0; j < b.N; j++ {
					if j%4096 == 0 {
						v.NoteOn(69, 127, false)
					}
					sampleSink = v.Next()
				}
			})
		}
	}
}
