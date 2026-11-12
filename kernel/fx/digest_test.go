package fx

import (
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// The digests below pin each processor's output bits for an exact test
// programme, and where a processor keeps float64 state, that state after every
// frame: the float32 output rounds away most last-bit differences, the state
// does not. amd64 never fuses a multiply and an add; arm64 does unless an
// explicit conversion rounds the product first. CI checks the same digests on
// arm64 in the PCM24 parity job, so a fusable expression in a processor fails
// there.

type stereoProcessor interface {
	Process(left, right float32) (float32, float32)
}

// runProgram feeds frames of the test programme into p and adds every output
// sample, and the values state returns if it is set, to d. At the half-way
// frame it calls change, if set, so parameter smoothing is part of the digest.
func runProgram(t *testing.T, d *bitdigest.Digest, p stereoProcessor, frames int, seed uint32, change func() error, state func() []float64) {
	t.Helper()
	left, right := bitdigest.NewNoise(seed), bitdigest.NewNoise(seed^0x5bd1e995)
	for i := 0; i < frames; i++ {
		if i == frames/2 && change != nil {
			if err := change(); err != nil {
				t.Fatal(err)
			}
		}
		l, r := bitdigest.Program(left, right, i)
		d.Float32(p.Process(l, r))
		if state != nil {
			d.Float64(state()...)
		}
	}
}

func checkDigest(t *testing.T, name string, d *bitdigest.Digest, want string) {
	t.Helper()
	if got := d.Hex(); got != want {
		t.Fatalf("%s digest = %s, want %s", name, got, want)
	}
}

// compressorDigest: moved on 2026-11-10 (pull request #327) when the
// compressor's attack, release, sidechain and smoothing coefficients (Exp) and
// knee floor (Pow) moved from the math library to kernel/dsp/detmath.
const compressorDigest = "86cf1d19159f4ceac9a9041e80b6bb3fd31168a2bbdac79c5eabede35aed45cb"

func TestCompressorDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []CompParams{
		DefaultCompParams(),
		{Detect: RMSDetector, Threshold: -30, Ratio: 8, Knee: 12, AttackMs: 1, ReleaseMs: 300, MakeupAuto: true, Mix: .7},
		{Detect: PeakDetector, Threshold: -12, Ratio: 2.5, Knee: 0, AttackMs: 30, ReleaseMs: 40, MakeupDB: 3.5, Mix: 1},
	}
	for i, p := range settings {
		for _, sidechain := range []bool{false, true} {
			c, err := NewCompressor([]int{44_100, 48_000, 96_000}[i])
			if err != nil {
				t.Fatal(err)
			}
			if err := c.SetParams(p); err != nil {
				t.Fatal(err)
			}
			next := settings[(i+1)%len(settings)]
			side := bitdigest.NewNoise(uint32(i) + 77)
			left, right := bitdigest.NewNoise(uint32(i)+1), bitdigest.NewNoise(uint32(i)+2)
			for frame := 0; frame < 40_000; frame++ {
				if frame == 20_000 {
					if err := c.SetParams(next); err != nil {
						t.Fatal(err)
					}
				}
				l, r := bitdigest.Program(left, right, frame)
				if sidechain {
					s := side.Next(frame / 4096 % 6)
					d.Float32(c.ProcessSidechain(l, r, s, -s))
				} else {
					d.Float32(c.Process(l, r))
				}
				if frame%512 == 0 {
					d.Float64(c.GainReductionDB())
				}
			}
		}
	}
	checkDigest(t, "compressor", d, compressorDigest)
}

// deesserDigest: moved on 2026-11-10 (pull request #327) when the de-esser's
// filter coefficient (Tan) and its four envelope coefficients (Exp) moved from
// the math library to kernel/dsp/detmath.
const deesserDigest = "6e11dd45beb49351be3e30d6ea780c18ecb040d02f272e920f4d08ea1ac5e3b8"

func TestDeesserDigest(t *testing.T) {
	d := bitdigest.New()
	for i, p := range []DeesserParams{
		DefaultDeesserParams(),
		{FrequencyHz: 4500, ThresholdDB: -36, Ratio: 10, RangeDB: 18, AttackMs: .3, ReleaseMs: 250, Mix: .6},
	} {
		x, err := NewDeesser([]int{48_000, 44_100}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		runProgram(t, d, x, 40_000, uint32(i)+11, nil, func() []float64 { return []float64{x.z1[0], x.z1[1], x.envelope, x.reduction} })
	}
	checkDigest(t, "de-esser", d, deesserDigest)
}

// delayDigest: moved on 2026-11-10 (pull request #327) when the delay's
// smoothing and damping coefficients (Exp) moved from the math library to
// kernel/dsp/detmath.
const delayDigest = "548cbe3e46afa40ec9efc2c7dd37eff075b8d5a1d6b518e7a29b2a380daf8ceb"

func TestDelayDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []DelayParams{
		DefaultDelayParams(),
		{Division: FreeDelay, TimeMs: 37.5, Feedback: .8, DampHz: 2_500, PingPong: true, Width: .6, Mix: .45},
		{Division: EighthDotted, Feedback: .55, DampHz: 11_000, Width: 1, Mix: .8},
	}
	for i, p := range settings {
		x, err := NewDelay([]int{48_000, 44_100, 96_000}[i], 123_456)
		if err != nil {
			t.Fatal(err)
		}
		if err := x.SetParams(p); err != nil {
			t.Fatal(err)
		}
		next := settings[(i+1)%len(settings)]
		runProgram(t, d, x, 40_000, uint32(i)+21, func() error { return x.SetParams(next) }, func() []float64 {
			last := x.buffer[(x.position+len(x.buffer)-1)%len(x.buffer)]
			return []float64{x.feedbackState.left, x.feedbackState.right, last.left, last.right}
		})
	}
	checkDigest(t, "delay", d, delayDigest)
}

// driveDigest: the fold shape runs on the second tier (SinPiNear, 2026-11-11)
// and its gain tables on the exact SinPi; neither move changed these bits,
// which the programme's fold setting covers.
const driveDigest = "267a31bf2928cce345c1e9b8645288576285467f8139b6c376a4aeae4334a575"

func TestDriveDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []DriveParams{
		{Shape: Soft, GainDB: 9, ToneHz: 9_000, Mix: 1},
		{Shape: Hard, GainDB: 24, ToneHz: 4_000, Mix: .5},
		{Shape: Fold, GainDB: 15, ToneHz: 16_000, Mix: .8},
	}
	for i, p := range settings {
		x, err := NewDrive([]int{48_000, 44_100, 96_000}[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := x.SetParams(p); err != nil {
			t.Fatal(err)
		}
		next := settings[(i+1)%len(settings)]
		runProgram(t, d, x, 30_000, uint32(i)+31, func() error { return x.SetParams(next) }, nil)
	}
	checkDigest(t, "drive", d, driveDigest)
}

// reverbDigest: moved on 2026-11-10 (pull request #327) when the reverb's
// line gains (Pow), damping, high-pass and smoothing coefficients (Exp) and
// modulation (Sin) moved from the math library to kernel/dsp/detmath. The
// modulation's move to the second tier (SinPiNear, 2026-11-11) left these
// bits unchanged.
const reverbDigest = "5ef9d6acb97a40cef3c5a5758d85c825787bfcc02de82043aab562e70d78f575"

func TestReverbDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []ReverbParams{
		DefaultReverbParams(),
		{Size: .6, DecaySec: .8, DampHz: 3_000, HighpassHz: 300, PredelayMs: 25, Mix: .35},
		{Size: 1.4, DecaySec: 9, DampHz: 14_000, HighpassHz: 60, PredelayMs: 120, Mix: .9},
	}
	for i, p := range settings {
		x, err := NewReverb([]int{48_000, 44_100, 96_000}[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := x.SetParams(p); err != nil {
			t.Fatal(err)
		}
		next := settings[(i+1)%len(settings)]
		runProgram(t, d, x, 40_000, uint32(i)+41, func() error { return x.SetParams(next) }, nil)
	}
	checkDigest(t, "reverb", d, reverbDigest)
}
