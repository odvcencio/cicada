package sample

import (
	"math"
	"testing"
)

// genericInterpolate keeps the frame-mapping path as an independent oracle.
// It includes zero padding, crossfade rounding, and periodic loop mapping.
func genericInterpolate(v *Voice) (float64, float64) {
	bank := v.bank
	base := int(v.phase)
	position := (v.phase - float64(base)) * phases
	index := int(position)
	fraction := position - float64(index)
	row := bank.coeff[index*bank.taps : (index+1)*bank.taps]
	next := bank.coeff[(index+1)*bank.taps : (index+2)*bank.taps]
	first := base - bank.taps/2 + 1
	var left, right float64
	for i, a := range row {
		c := float64(a) + float64((float64(next[i])-float64(a))*fraction)
		l, r := genericFrame(v, first+i)
		left += float64(float64(l) * c)
		right += float64(float64(r) * c)
	}
	return left, right
}

// Keep the original per-frame mapping and blend arithmetic independent from
// all optimized branches, so the oracle cannot silently acquire a fast path.
func genericFrame(v *Voice, index int) (float32, float32) {
	r := &v.region
	if r.Loop && (index >= r.LoopEnd || (v.looped && index < r.LoopStart+r.Crossfade)) {
		length := r.LoopEnd - r.LoopStart - r.Crossfade
		index = (index - r.LoopStart - r.Crossfade) % length
		if index < 0 {
			index += length
		}
		index += r.LoopStart + r.Crossfade
	}
	if index < r.Start || index >= r.End {
		return 0, 0
	}
	left := r.Left[index]
	right := left
	if len(r.Right) != 0 {
		right = r.Right[index]
	}
	if r.Crossfade > 0 && index >= r.LoopEnd-r.Crossfade && index < r.LoopEnd {
		head := r.LoopStart + index - (r.LoopEnd - r.Crossfade)
		blend := float64(index-(r.LoopEnd-r.Crossfade)) / float64(r.Crossfade)
		left = float32(float64(left)*(1-blend) + float64(r.Left[head])*blend)
		h := r.Left[head]
		if len(r.Right) != 0 {
			h = r.Right[head]
		}
		right = float32(float64(right)*(1-blend) + float64(h)*blend)
	}
	return left, right
}

func TestCrossfadedLoopSRCMatchesGenericMapping(t *testing.T) {
	var left, right [2049]float32
	seed := uint32(0x47ace109)
	for i := range left {
		seed = seed*1664525 + 1013904223
		left[i] = float32(int32(seed>>8)-(1<<23)) / (1 << 24)
		seed = seed*1664525 + 1013904223
		right[i] = float32(int32(seed>>8)-(1<<23)) / (1 << 24)
	}
	comparisons := 0
	for _, stereo := range []bool{false, true} {
		for _, fade := range []int{0, 1, 31, 256, 700, 771} {
			region := Region{Left: left[:], SampleRate: 48000, RootKey: 60, Start: 9, End: 2043, Loop: true, LoopStart: 257, LoopEnd: 1800, Crossfade: fade}
			if stereo {
				region.Right = right[:]
			}
			for _, ratio := range []float64{.125, .25, .75, 1, math.Exp2(1. / 12), 1.125, 1.25, 1.5, 2, 3, 4, 8} {
				v, err := New(48000, region)
				if err != nil {
					t.Fatal(err)
				}
				v.ratio, v.bank = ratio, bankFor(ratio)
				for _, looped := range []bool{false, true} {
					v.looped = looped
					// Every source position includes intro, both loop limits, and
					// every FIR-window transition at the crossfade boundaries.
					for base := 0; base <= len(left); base++ {
						for _, fraction := range []float64{0, .25, .375, .99999} {
							v.phase = float64(base) + fraction
							l, r := v.interpolate()
							wantL, wantR := genericInterpolate(v)
							if math.Float64bits(l) != math.Float64bits(wantL) || math.Float64bits(r) != math.Float64bits(wantR) {
								t.Fatalf("stereo=%v fade=%d ratio=%g looped=%v phase=%g optimized=(%016x,%016x) generic=(%016x,%016x)", stereo, fade, ratio, looped, v.phase, math.Float64bits(l), math.Float64bits(r), math.Float64bits(wantL), math.Float64bits(wantR))
							}
							comparisons++
						}
					}
				}
			}
		}
	}
	t.Logf("crossfaded loop FIR comparisons=%d all_float64_bits_identical=true", comparisons)
}

func TestCrossfadedLoopRenderAllocations(t *testing.T) {
	pcm := make([]float32, 4096)
	for i := range pcm {
		pcm[i] = float32((i*17)%101-50) / 100
	}
	r := Region{Left: pcm, Right: pcm, SampleRate: 48000, RootKey: 60, End: len(pcm), Loop: true, LoopStart: 300, LoopEnd: 4000, Crossfade: 512}
	v, err := New(48000, r)
	if err != nil {
		t.Fatal(err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		v.Reset()
		if err := v.NoteOn(61, 102); err != nil {
			t.Fatal(err)
		}
		for range 8192 {
			v.NextStereo()
		}
		v.NoteOff()
		for range 128 {
			v.NextStereo()
		}
	}); allocations != 0 {
		t.Fatalf("crossfaded loop trigger/render allocations=%g", allocations)
	}
}
