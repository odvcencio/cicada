package acid

import (
	"math"
	"testing"
)

func TestNormalDiodeFastPathPreservesOutputAndStateBits(t *testing.T) {
	for _, G := range []float64{.05, .25, .7} {
		for _, k := range []float64{0, 9.35, 17} {
			g := G / (1 - G)
			invDen3 := 1 / (1 - G*G/2)
			a3 := (G / 2) * invDen3
			invDen2 := 1 / (1 - G*a3/2)
			a2 := (G / 2) * invDen2
			c := G * a3 * a2
			inv := 1 / (1 + g)
			invFirstDen := 1 / (1 - G*a2/2 + G*k*c/2)
			invRefinedDen := 1 / (1 - G*a2/2)
			var normal, generic filterState
			for i := 0; i < 4096; i++ {
				input := math.Sin(float64(i) * .017)
				got := normal.processDiodeShapedNormal(input, G, k, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen)
				want := generic.processDiodeShaped(input, G, k, 0, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen)
				if math.Float64bits(got) != math.Float64bits(want) {
					t.Fatalf("G %g k %g sample %d output changed bits", G, k, i)
				}
				for stage := range normal.state {
					if math.Float64bits(normal.state[stage]) != math.Float64bits(generic.state[stage]) {
						t.Fatalf("G %g k %g sample %d stage %d changed bits", G, k, i, stage)
					}
				}
			}
		}
	}
}
