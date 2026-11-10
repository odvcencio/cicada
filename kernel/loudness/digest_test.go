package loudness

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// meterDigest is the SHA-256 of the meter's filter coefficients and of every
// reading after four seconds of stepped noise at each supported rate. amd64
// never fuses a multiply and an add, but arm64 may unless an explicit
// conversion rounds the product first; an export's loudness search depends on
// these readings, so the digest must be the same on both. CI checks it on
// arm64 in the PCM24 parity job.
const meterDigest = "67924001dd91bbfa3b63ca95be4787bdcca56b02bf5208690fc5c211c884015c"

// digestNoise returns exact float32 noise in [-0.5, 0.5) from a 32-bit
// xorshift generator: a 24-bit integer divided by a power of two, so every
// target produces the same input.
func digestNoise(state *uint32) float32 {
	x := *state
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*state = x
	return float32(int32(x)>>8) / (1 << 24)
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
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != meterDigest {
		t.Fatalf("meter digest = %s, want %s", got, meterDigest)
	}
}
