package piano

import (
	"math"
	"testing"
)

func TestNeverExcitedPianoPreservesPedalAndFirstStrike(t *testing.T) {
	a, err := New(48000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New(48000)
	for _, gap := range []int{0, 13417} {
		a.Reset()
		b.Reset()
		b.excited = true // Force the full zero soundboard/resonator bank.
		for frame := 0; frame < gap+12000; frame++ {
			if frame == 17 {
				a.SetSustain(.73)
				b.SetSustain(.73)
			}
			if frame == 2277 {
				a.SetSustain(.21)
				b.SetSustain(.21)
			}
			if frame == gap {
				a.NoteOn(60, 103)
				b.NoteOn(60, 103)
			}
			if frame == gap+4911 {
				a.NoteOff(60)
				b.NoteOff(60)
			}
			aL, aR := a.NextStereo()
			bL, bR := b.NextStereo()
			if math.Float32bits(aL) != math.Float32bits(bL) || math.Float32bits(aR) != math.Float32bits(bR) || a.pedal != b.pedal {
				t.Fatalf("gap=%d frame=%d output/pedal differs", gap, frame)
			}
		}
	}
	a.Reset()
	if allocs := testing.AllocsPerRun(100, func() {
		for range 128 {
			a.NextStereo()
		}
	}); allocs != 0 {
		t.Fatalf("idle render allocations=%g", allocs)
	}
}

func BenchmarkNeverExcitedPiano(b *testing.B) {
	for _, full := range []bool{false, true} {
		name := "silent"
		if full {
			name = "full"
		}
		b.Run(name, func(b *testing.B) {
			p, _ := New(48000)
			p.excited = full
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				p.NextStereo()
			}
		})
	}
}
