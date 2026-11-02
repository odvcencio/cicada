package mix

import (
	"math"
	"testing"
)

func TestNewTrackTableMatchesTheLaw(t *testing.T) {
	worst := 0.0
	for gain := -60.0; gain <= 6; gain += 0.37 {
		for pan := -1.0; pan <= 1; pan += 0.05 {
			want := NewTrack(gain, pan, false)
			got := NewTrackTable(float32(gain), float32(pan))
			for _, pair := range [][2]float32{{got.Left, want.Left}, {got.Right, want.Right}} {
				if pair[1] == 0 {
					continue
				}
				d := math.Abs(float64(pair[0])-float64(pair[1])) / math.Max(float64(pair[1]), 1e-3*math.Pow(10, gain/20))
				worst = math.Max(worst, d)
			}
		}
	}
	if worst > 2e-6 {
		t.Fatalf("worst relative error %g", worst)
	}
}

func TestNewTrackTableAllocatesNothing(t *testing.T) {
	var sink Track
	if n := testing.AllocsPerRun(100, func() { sink = NewTrackTable(-6.5, .3) }); n != 0 {
		t.Fatalf("%v allocations", n)
	}
	_ = sink
}
