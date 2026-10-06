package guitar

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
)

func TestGuitarSlideRepluckAndReset(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		v, err := New(rate, DefaultParams())
		if err != nil {
			t.Fatal(err)
		}
		v.NoteOn(40, 100, false, false)
		for range rate {
			v.Next()
		}
		// A live slide changes pitch but retains the decayed string excitation.
		v.NoteOn(47, 100, false, true)
		var slidePower float64
		for range 2048 {
			x := float64(v.Next())
			slidePower += x * x
		}
		v.NoteOn(47, 100, false, false)
		var pluckPower float64
		for range 2048 {
			x := float64(v.Next())
			pluckPower += x * x
		}
		if pluckPower <= slidePower*2 {
			t.Fatalf("repluck power %g, slide %g", pluckPower, slidePower)
		}
		v.Reset()
		for range 256 {
			if v.Next() != 0 {
				t.Fatal("reset left a sounding string or amp tail")
			}
		}
		v.NoteOn(40, 100, false, false)
		var first [1024]float32
		for i := range first {
			first[i] = v.Next()
		}
		v.Reset()
		v.NoteOn(40, 100, false, false)
		for i, want := range first {
			if got := v.Next(); got != want {
				t.Fatalf("reset is not deterministic at %d", i)
			}
		}
		v.NoteOff()
		// A slide after a release must excite a new string.
		v.NoteOn(40, 100, false, true)
		var power float64
		for range 2048 {
			x := float64(v.Next())
			power += x * x
		}
		if power == 0 {
			t.Fatal("slide from a released gate did not pluck")
		}
	}
}

func TestGuitarControlsAllocateNothing(t *testing.T) {
	v, err := New(48000, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		v.Reset()
		v.NoteOn(40, 100, false, false)
		for id := kernel.ParamGuitarBend; id <= kernel.ParamGuitarDrive; id++ {
			if err := v.SetParam(id, float64(kernel.Params[id].Max)); err != nil {
				panic(err)
			}
		}
		for range 128 {
			if x := float64(v.Next()); math.IsNaN(x) || math.IsInf(x, 0) {
				panic("nonfinite guitar")
			}
		}
		v.NoteOn(45, 80, false, true)
		v.NoteOff()
	})
	if allocs != 0 {
		t.Fatalf("guitar callback allocations/run = %g", allocs)
	}
	t.Logf("METRIC: guitar controls allocations/run | %g", allocs)
}

func BenchmarkGuitarNext(b *testing.B) {
	v, err := New(48000, Params{Brightness: .7, Pickup: .22, Vibrato: 25, Drive: .7, Damping: .15})
	if err != nil {
		b.Fatal(err)
	}
	v.NoteOn(40, 100, false, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%48000 == 0 {
			v.NoteOn(40, 100, false, false)
		}
		v.Next()
	}
}
