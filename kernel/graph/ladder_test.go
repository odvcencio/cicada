package graph

import (
	"math"
	"testing"
)

func ladderCoef(cutoff, rate float64) float32 {
	g := float32(math.Tan(math.Pi * cutoff / rate))
	return g / (1 + g)
}

// An infinite input (a divide by a denormal, a multiply overflow) must
// saturate like any other loud sample. Before the input was bounded it turned
// the feedback estimate into Inf - Inf and left NaN in every stage, which
// faults the voice and silences the note.
func TestLadderSaturatesOnInfiniteInput(t *testing.T) {
	for _, feedback := range []float32{0, 1.6, 3.2} {
		for _, x := range []float32{float32(math.Inf(1)), float32(math.Inf(-1)), math.MaxFloat32, -math.MaxFloat32} {
			var state [4]float32
			coef := ladderCoef(1000, 48000)
			var y float32
			for i := 0; i < 64; i++ {
				y = ladder(&state, coef, feedback, x)
			}
			if f := float64(y); math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1 {
				t.Fatalf("feedback %v, input %v: output %v", feedback, x, y)
			}
			for stage, s := range state {
				if f := float64(s); math.IsNaN(f) || math.IsInf(f, 0) {
					t.Fatalf("feedback %v, input %v: stage %d state %v", feedback, x, stage, s)
				}
			}
			// The filter still passes signal afterwards.
			for i := 0; i < 4096; i++ {
				y = ladder(&state, coef, feedback, 0.25)
			}
			want := 0.25 / (1 + float64(feedback)) // DC gain of the loop while tanh is near linear
			if math.Abs(float64(y)-want) > 0.02 {
				t.Fatalf("feedback %v after input %v: settles at %v, want about %.3f", feedback, x, y, want)
			}
		}
	}
}

// Bounding the input changes nothing a finite render can reach: every input at
// or beyond the limit already saturates to exactly +-1 inside the ladder.
func TestLadderInputLimitIsPastSaturation(t *testing.T) {
	for _, feedback := range []float32{0, 3.2} {
		for _, cutoff := range []float64{20, 1000, 0.45 * 48000} {
			coef := ladderCoef(cutoff, 48000)
			for _, s := range []float32{-2, 0, 2} { // stage states stay within about +-1
				limit, beyond := [4]float32{s, s, s, s}, [4]float32{s, s, s, s}
				a := ladder(&limit, coef, feedback, ladderInputLimit)
				b := ladder(&beyond, coef, feedback, 1e30)
				if a != b || limit != beyond {
					t.Fatalf("feedback %v cutoff %v state %v: input at the limit gives %v %v, beyond it %v %v", feedback, cutoff, s, a, limit, b, beyond)
				}
			}
		}
	}
}
