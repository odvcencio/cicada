package phrase

import "testing"

func TestRhythmMasksAndOnsetGate(t *testing.T) {
	for index, mask := range rhythmMasks {
		if len(mask) != 16 {
			t.Fatalf("mask %d has %d cells", index+1, len(mask))
		}
	}
	for _, steps := range []int{8, 16, 32, 64} {
		for _, density := range []uint8{0, 1, 38, 64} {
			for _, restDownbeat := range []bool{false, true} {
				for seed := uint64(0); seed < 100; seed++ {
					s := newStream(seed)
					onsets := generateRhythm(&s, steps, density, restDownbeat)
					count := 0
					for _, active := range onsets {
						if active {
							count++
						}
					}
					if len(onsets) != steps || count < 1 || count > steps || !restDownbeat && !onsets[0] {
						t.Fatalf("invalid rhythm: steps=%d density=%d seed=%d downbeat=%t count=%d", steps, density, seed, restDownbeat, count)
					}
					if delta := count - onsetTarget(steps, int(density)); delta < -1 || delta > 1 {
						t.Fatalf("onset target drift: steps=%d density=%d seed=%d count=%d", steps, density, seed, count)
					}
				}
			}
		}
	}
}
