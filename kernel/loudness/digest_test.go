package loudness

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// meterDigest is the SHA-256 of the meter's filter coefficients, of every
// reading after four seconds of stepped noise, and of the true peak of each
// channel through a rising level, at each supported rate. amd64 never fuses a
// multiply and an add, but arm64 may unless an explicit conversion rounds the
// product first; an export's loudness search depends on these readings, so for
// these inputs the digest must be the same on both. CI checks it on arm64 in
// the PCM24 parity job.
const meterDigest = "0d0c4ae17e48e037d7168a7bac4b101b69c862645880bffa0d2733d7de2fea24"

// digestNoise returns exact float32 noise in [-0.5, 0.5) from a 32-bit
// xorshift generator: a 24-bit integer divided by a power of two, so every
// target produces the same input.
func digestNoise(state *uint32) float32 { return digestNoiseAt(state, 8) }

// digestNoiseAt is digestNoise with 2^(8-shift) of its amplitude, for shift
// from 8 to 31; the samples stay exact.
func digestNoiseAt(state *uint32, shift int) float32 {
	x := *state
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*state = x
	return float32(int32(x)>>shift) / (1 << 24)
}

// The table holds the amd64 values of the Blackman formula. On a target whose
// math.Cos differs in the last bit the formula moves by at most a few units in
// the last place.
func TestTruePeakWindowIsBlackman(t *testing.T) {
	for tap, want := range truePeakWindow {
		position := float64(tap) / float64(truePeakTaps-1)
		formula := 0.42 - float64(0.5*math.Cos(2*math.Pi*position)) + float64(0.08*math.Cos(4*math.Pi*position))
		if math.Abs(formula-want) > 1e-15 {
			t.Fatalf("tap %d: table %v, formula %v", tap, want, formula)
		}
	}
}

func TestMeterDigestIsIdenticalOnEveryTarget(t *testing.T) {
	sum := sha256.New()
	var word [8]byte
	put := func(values ...float64) {
		for _, v := range values {
			binary.LittleEndian.PutUint64(word[:], math.Float64bits(v))
			sum.Write(word[:])
		}
	}
	for _, rate := range []int{44_100, 48_000, 96_000} {
		m, err := New(rate)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []biquad{m.filterShelfL, m.filterRLBL} {
			put(f.b0, f.b1, f.b2, f.a1, f.a2)
		}
		for phase := range m.truePeakPhases {
			put(m.truePeakCoeff[phase][:]...)
		}
		state := uint32(rate)
		for i := 0; i < 4*rate; i++ {
			// The level steps by powers of two every half second, which keeps
			// the samples exact and gives the gates and the range work to do.
			scale := float32(1) / float32(int(1)<<(i/(rate/2)%4))
			if !m.ProcessSample(digestNoise(&state)*scale, digestNoise(&state)*scale) {
				t.Fatal("meter rejected a finite sample")
			}
		}
		if err := m.Finish(); err != nil {
			t.Fatal(err)
		}
		r := m.Metrics()
		put(r.SamplePeak, r.TruePeak, r.MomentaryLUFS, r.ShortTermLUFS, r.IntegratedLUFS, r.LoudnessRange, r.MaxMomentaryLUFS, r.MaxShortTermLUFS, r.RMS)
		t.Logf("rate %d: integrated %016x (%.17g LUFS) true peak %016x range %016x rms %016x", rate, math.Float64bits(r.IntegratedLUFS), r.IntegratedLUFS, math.Float64bits(r.TruePeak), math.Float64bits(r.LoudnessRange), math.Float64bits(r.RMS))

		// The true peak is the larger of the two channels, so the run above
		// can leave one channel's interpolation unpinned. Here one channel is
		// 18 dB louder than the other and sets every reading; its level
		// doubles every quarter second, so each step's reading comes from a
		// new interpolated peak.
		for _, leftLouder := range []bool{true, false} {
			m, err := New(rate)
			if err != nil {
				t.Fatal(err)
			}
			state := uint32(rate) + 7
			step := rate / 4
			for i := 0; i < 16*step; i++ {
				shift := 23 - i/step
				loud, quiet := digestNoiseAt(&state, shift), digestNoiseAt(&state, shift+3)
				left, right := loud, quiet
				if !leftLouder {
					left, right = quiet, loud
				}
				if !m.ProcessSample(left, right) {
					t.Fatal("meter rejected a finite sample")
				}
				if (i+1)%step == 0 {
					put(m.Metrics().TruePeak)
				}
			}
		}
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != meterDigest {
		t.Fatalf("meter digest = %s, want %s", got, meterDigest)
	}
}
