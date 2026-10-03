package drum

import (
	"fmt"
	"math"
	"testing"
)

func TestBassDrumDecaysMatchPerSampleFormula(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		kit, err := New(rate, 4242)
		if err != nil {
			t.Fatal(err)
		}
		// A mapped bass drum must use its recipe's coefficients, too.
		if err := kit.SetRecipe(SD, BD); err != nil {
			t.Fatal(err)
		}
		for sample := 0; sample < 16000; sample++ {
			for _, lane := range []Lane{BD, SD} {
				p := kit.Params(lane)
				if sample%1024 == 0 {
					p.Decay = .08 + float64(sample%700)/500
					p.SweepTime = .01 + float64(sample%60)/1000
					if err := kit.SetParams(lane, p); err != nil {
						t.Fatal(err)
					}
				}
				if sample%1024 == 512 {
					p.Decay = 1.5
					if err := kit.SetParamsTarget(lane, p, .01); err != nil {
						t.Fatal(err)
					}
				}
				if sample%193 == 0 {
					kit.Hit(lane, uint8(64+sample%64), sample%2 == 0)
				}
			}
			if sample%3001 == 3000 {
				kit.Reset()
			}
			before := kit.lanes
			kit.NextStereo()
			for _, lane := range []Lane{BD, SD} {
				v, old := &kit.lanes[lane], &before[lane]
				for i, pair := range [][2]*state{{&old.current, &v.current}, {&old.old, &v.old}} {
					if !pair[0].active || (i == 1 && old.fadeRemaining == 0) {
						continue
					}
					want := [3]float64{
						pair[0].sweep * math.Exp(-1/(v.params.SweepTime*kit.rate)),
						pair[0].amp * math.Exp(-1/(v.params.Decay*kit.rate)),
						pair[0].click * math.Exp(-1/(.002*kit.rate)),
					}
					got := [3]float64{pair[1].sweep, pair[1].amp, pair[1].click}
					for coefficient := range want {
						if math.Float64bits(got[coefficient]) != math.Float64bits(want[coefficient]) {
							t.Fatalf("rate=%d lane=%s sample=%d state=%d decay=%d: bits differ", rate, Names[lane], sample, i, coefficient)
						}
					}
				}
			}
		}
	}
}

func TestBassDrumParameterChangesDoNotAllocate(t *testing.T) {
	kit, err := New(48000, 4242)
	if err != nil {
		t.Fatal(err)
	}
	p := kit.Params(BD)
	allocs := testing.AllocsPerRun(100, func() {
		p.Decay = .2
		if err := kit.SetParams(BD, p); err != nil {
			panic(err)
		}
		kit.Hit(BD, 100, false)
		p.Decay = .8
		if err := kit.SetParamsTarget(BD, p, .01); err != nil {
			panic(err)
		}
		for range 128 {
			kit.NextStereo()
		}
	})
	if allocs != 0 {
		t.Fatalf("bass drum control changes allocated %g objects", allocs)
	}
}

var drumBenchmarkOutput float32

func BenchmarkDrumKit(b *testing.B) {
	for lane := Lane(0); lane < LaneCount; lane++ {
		b.Run(fmt.Sprintf("%s/48000", Names[lane]), func(b *testing.B) {
			kit, err := New(48000, 4242)
			if err != nil {
				b.Fatal(err)
			}
			for other := Lane(0); other < LaneCount; other++ {
				if other != lane {
					if err := kit.Disable(other); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i&127 == 0 {
					kit.Hit(lane, 100, false)
				}
				left, right := kit.NextStereo()
				drumBenchmarkOutput = left + right
			}
		})
	}
}
