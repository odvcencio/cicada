package fx

import (
	"math"
	"reflect"
	"testing"
)

func TestCompressorFinitePreservesIEEEClassification(t *testing.T) {
	// Cover both signs, every exponent and significand boundaries, including
	// signed zeros, subnormals, maximal finite values, infinity and NaN payloads.
	for _, sign := range []uint64{0, 1 << 63} {
		for exponent := uint64(0); exponent < 2048; exponent++ {
			for _, significand := range []uint64{0, 1, 2, 1 << 51, (1 << 52) - 1} {
				bits := sign | exponent<<52 | significand
				value := math.Float64frombits(bits)
				if got, want := compressorFinite(value), !math.IsNaN(value) && !math.IsInf(value, 0); got != want {
					t.Fatalf("classification changed for %016x: got %t want %t", bits, got, want)
				}
			}
		}
	}
	bits := uint64(0x7af91cbe35d04628)
	for range 100000 {
		bits = bits*6364136223846793005 + 1442695040888963407
		value := math.Float64frombits(bits)
		if compressorFinite(value) != (!math.IsNaN(value) && !math.IsInf(value, 0)) {
			t.Fatalf("classification changed for sampled value %016x", bits)
		}
	}
}

func TestCompressorInvalidInputsPreserveDetectorAndReleaseState(t *testing.T) {
	for _, detector := range []CompDetector{PeakDetector, RMSDetector} {
		for _, invalid := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
			for input := 0; input < 4; input++ {
				c, err := NewCompressor(48000)
				if err != nil {
					t.Fatal(err)
				}
				p := c.Params()
				p.Detect = detector
				if err := c.SetParams(p); err != nil {
					t.Fatal(err)
				}
				c.Reset()
				for frame := 0; frame < 1231; frame++ {
					x := float32(.37 * math.Sin(float64(frame)*.31))
					c.ProcessSidechain(.4, -.23, x, -x)
				}
				before := *c
				before.rms = append([]float64(nil), c.rms...)
				values := [4]float32{.4, -.23, .61, -.35}
				values[input] = invalid
				left, right := c.ProcessSidechain(values[0], values[1], values[2], values[3])
				if left != 0 || right != 0 || !c.Fault() {
					t.Fatal("invalid audio/detector input did not fault silently")
				}
				before.fault = true
				if !reflect.DeepEqual(*c, before) {
					t.Fatalf("detector=%d input=%d: invalid input advanced detector, automation or release state", detector, input)
				}
			}
		}
	}
}

func TestCompressorFiniteInputsRetainSubnormalPCMAndAllocateNothing(t *testing.T) {
	c, err := NewCompressor(48000)
	if err != nil {
		t.Fatal(err)
	}
	p := c.Params()
	p.Ratio, p.Threshold, p.MakeupAuto, p.MakeupDB, p.Mix = 1, 0, false, 0, 1
	if err := c.SetParams(p); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	for _, bits := range []uint32{0, 1, 2, 0x007fffff, 0x00800000, 0x3f000000, 0x7f7fffff} {
		for _, sign := range []uint32{0, 1 << 31} {
			value := math.Float32frombits(bits | sign)
			left, right := c.Process(value, value)
			if c.Fault() || left != value || right != value {
				t.Fatalf("finite PCM was rejected or changed: %08x got=(%g,%g)", bits|sign, left, right)
			}
		}
	}
	if allocs := testing.AllocsPerRun(1000, func() { c.Process(.4, -.23); c.ProcessSidechain(.4, -.23, .61, -.35) }); allocs != 0 {
		t.Fatalf("compressor callback allocations=%g", allocs)
	}
}
