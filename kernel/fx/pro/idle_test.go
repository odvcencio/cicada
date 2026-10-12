package pro

import (
	"math"
	"testing"
)

func TestSilentEQPreservesExactStateCleanupAndRetrigger(t *testing.T) {
	bands := [4]Band{{Enabled: true, Type: Highpass, FrequencyHz: 83, Q: .707}, {Enabled: true, Type: Peak, FrequencyHz: 1700, Q: 2.7, GainDB: 9}}
	a, err := NewEQ(48000, bands)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewEQ(48000, bands)
	if !a.Silent() {
		t.Fatal("new EQ should have a zero filter state")
	}
	for frame := 0; frame < 20000; frame++ {
		var l, r float32
		if frame == 371 || frame == 17001 {
			l, r = .7, -.3
		}
		aL, aR := a.Process(l, r)
		bL, bR := float64(l), float64(r)
		for i := range b.filters {
			if b.active[i] {
				bL = b.filters[i].process(bL, 0)
				bR = b.filters[i].process(bR, 1)
			}
		}
		if math.Float32bits(aL) != math.Float32bits(float32(bL)) || math.Float32bits(aR) != math.Float32bits(float32(bR)) {
			t.Fatalf("EQ frame %d differs from full kernel", frame)
		}
		if frame == 371 && a.Silent() {
			t.Fatal("excited filter reported silent")
		}
	}
	a.Reset()
	if allocs := testing.AllocsPerRun(100, func() {
		for range 128 {
			a.Process(0, 0)
		}
	}); allocs != 0 {
		t.Fatalf("silent EQ allocations=%g", allocs)
	}
}
