package pro

import (
	"math"
	"testing"
)

// oraclePeak uses 32 phases and a 96-tap Hann reconstruction, independently
// lengthened/windowed relative to the live eight-phase 64-tap Blackman FIR.
// It includes zero-extended boundaries and is checked against an analytic tone.
func oraclePeak(left, right []float32) float64 {
	const taps, half, phases = 96, 48, 32
	var coeff [phases - 1][taps]float64
	for phase := range coeff {
		fraction, sum := float64(phase+1)/phases, 0.0
		for tap := range coeff[phase] {
			x := float64(tap-(half-1)) - fraction
			h := math.Sin(math.Pi*x) / (math.Pi * x)
			h *= .5 - .5*math.Cos(2*math.Pi*float64(tap)/(taps-1))
			coeff[phase][tap] = h
			sum += h
		}
		for tap := range coeff[phase] {
			coeff[phase][tap] /= sum
		}
	}
	peak := 0.0
	for center := -half; center < len(left)+half; center++ {
		if center >= 0 && center < len(left) {
			peak = math.Max(peak, math.Max(math.Abs(float64(left[center])), math.Abs(float64(right[center]))))
		}
		for phase := range coeff {
			l, r := 0.0, 0.0
			for tap, h := range coeff[phase] {
				i := center + tap - (half - 1)
				if i >= 0 && i < len(left) {
					l += h * float64(left[i])
					r += h * float64(right[i])
				}
			}
			peak = math.Max(peak, math.Max(math.Abs(l), math.Abs(r)))
		}
	}
	return peak
}

func renderLimiter(t *testing.T, rate int, p LimiterParams, left, right []float32) ([]float32, []float32) {
	t.Helper()
	l, err := NewLimiter(rate, p)
	if err != nil {
		t.Fatal(err)
	}
	outL, outR := make([]float32, len(left)+l.LatencyFrames()), make([]float32, len(left)+l.LatencyFrames())
	for i := range outL {
		a, b := float32(0), float32(0)
		if i < len(left) {
			a, b = left[i], right[i]
		}
		outL[i], outR[i] = l.Process(a, b)
	}
	if l.Fault() {
		t.Fatal("limiter faulted on finite test signal")
	}
	return outL, outR
}

func TestLimiterLatencyAndUnity(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		limiter, err := NewLimiter(rate, DefaultLimiterParams())
		if err != nil {
			t.Fatal(err)
		}
		expected := int(math.Ceil(.003*float64(rate))) + limiterTaps
		if limiter.LatencyFrames() != expected {
			t.Fatalf("rate=%d latency=%d expected=%d", rate, limiter.LatencyFrames(), expected)
		}
		for i := 0; i < expected+32; i++ {
			in := float32(0)
			if i == 0 {
				in = .1
			}
			l, r := limiter.Process(in, -in)
			want := float32(0)
			if i == expected {
				want = .1
			}
			if l != want || r != -want {
				t.Fatalf("delay/unity incorrect at %d: %g %g expected=%g", i, l, r, want)
			}
		}
	}
}

func TestLimiterTruePeakQuarterRate(t *testing.T) {
	const n = 8192
	left, right := make([]float32, n), make([]float32, n)
	for i := range left {
		// The samples reach only 0.884, while the analytic sinusoid reaches
		// 1.25. A sample-only limiter would miss this overload entirely.
		x := 1.25 * math.Sin(math.Pi*.5*float64(i)+math.Pi*.25)
		if i < 128 {
			x *= float64(i) / 128
		} else if i > n-129 {
			x *= float64(n-i-1) / 128
		}
		left[i], right[i] = float32(x), float32(x*.5)
	}
	inputPeak := oraclePeak(left, right)
	if math.Abs(inputPeak-1.25) > .001 {
		t.Fatalf("independent reconstruction missed analytic peak: %g", inputPeak)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		l, r := renderLimiter(t, rate, DefaultLimiterParams(), left, right)
		peak := oraclePeak(l, r)
		if peak > dbGain(-1) {
			t.Fatalf("rate=%d limited true peak=%g dBTP", rate, 20*math.Log10(peak))
		}
		for i := range l {
			if l[i] != r[i]*2 {
				t.Fatal("limiter moved linked stereo image")
			}
		}
		t.Logf("rate=%d analytic input %.3f dBTP; dense output %.3f dBTP", rate, 20*math.Log10(inputPeak), 20*math.Log10(peak))
	}
}

func TestLimiterDenseOracleTransientFixtures(t *testing.T) {
	const n = 6144
	for _, fixture := range []string{"impulses", "burst", "noise", "near-nyquist", "clipped"} {
		t.Run(fixture, func(t *testing.T) {
			left, right := make([]float32, n), make([]float32, n)
			state := uint32(123456789)
			for i := range left {
				x := 0.0
				switch fixture {
				case "impulses":
					if i%557 == 0 {
						x = 4
					}
				case "burst":
					if i%1000 > 320 && i%1000 < 450 {
						x = 3 * math.Sin(2*math.Pi*.2937*float64(i))
					}
				case "noise":
					state = state*1664525 + 1013904223
					x = 2.5 * (2*float64(state)/math.MaxUint32 - 1)
					if i%1000 < 400 {
						x *= .05
					}
				case "near-nyquist":
					x = 2 * math.Sin(2*math.Pi*.4893*float64(i)+.733)
				case "clipped":
					x = math.Max(-1.2, math.Min(1.2, 3*math.Sin(2*math.Pi*.0777*float64(i))))
				}
				left[i], right[i] = float32(x), float32(-x*.3)
			}
			p := DefaultLimiterParams()
			// The shortest permitted lookahead is the most demanding attack.
			p.LookaheadMs, p.ReleaseMs = 1, 20
			l, r := renderLimiter(t, 48000, p, left, right)
			peak := oraclePeak(l, r)
			if peak > dbGain(-1) {
				t.Fatalf("dense output peak=%g dBTP", 20*math.Log10(peak))
			}
			t.Logf("dense output %.3f dBTP", 20*math.Log10(peak))
		})
	}
}

func TestLimiterDenseOracleNoiseSeeds(t *testing.T) {
	const n = 3072
	worst := math.Inf(-1)
	for seed := uint32(1); seed <= 128; seed++ {
		left, right := make([]float32, n), make([]float32, n)
		state := seed * 2654435761
		for i := range left {
			state = state*1664525 + 1013904223
			a := 2*float64(state)/math.MaxUint32 - 1
			state = state*1664525 + 1013904223
			b := 2*float64(state)/math.MaxUint32 - 1
			level := 2.0
			if i%641 < 211 {
				level = .01
			}
			left[i], right[i] = float32(a*level), float32(b*level)
		}
		p := DefaultLimiterParams()
		p.LookaheadMs, p.ReleaseMs = 1, 20
		rate := []int{44100, 48000, 96000}[seed%3]
		l, r := renderLimiter(t, rate, p, left, right)
		peakDB := 20 * math.Log10(oraclePeak(l, r))
		worst = math.Max(worst, peakDB)
		if peakDB > -1 {
			t.Fatalf("seed=%d rate=%d dense peak %.4f dBTP", seed, rate, peakDB)
		}
	}
	t.Logf("128 seeded stereo burst/noise programmes: worst %.4f dBTP", worst)
}

func TestNormalizeTargetAndPeakConstraint(t *testing.T) {
	const n = 48000
	for _, target := range []float64{-20, -5} {
		left, right := make([]float32, n), make([]float32, n)
		for i := range left {
			x := float32(.1 * math.Sin(2*math.Pi*1000*float64(i)/48000))
			left[i], right[i] = x, x
		}
		if target == -5 {
			// A short impact raises crest factor without enough energy to
			// materially raise programme loudness.
			left[n/2], right[n/2] = .7, .7
		}
		report, err := NormalizeStereo(48000, left, right, target, -1)
		if err != nil {
			t.Fatal(err)
		}
		if target == -20 && (!report.Reachable || report.PeakLimited || math.Abs(report.After.IntegratedLUFS-target) > .01) {
			t.Fatalf("reachable target failed: %+v", report)
		}
		if target == -5 && (report.Reachable || !report.PeakLimited) {
			t.Fatalf("unreachable target was hidden: %+v", report)
		}
		peak := oraclePeak(left, right)
		if peak > dbGain(-1) || report.After.TruePeakDBTP > -1 {
			t.Fatalf("normalization exceeded peak ceiling: dense=%g reported=%g dBTP", 20*math.Log10(peak), report.After.TruePeakDBTP)
		}
		t.Logf("target %.1f LUFS; achieved %.3f LUFS; gain %.3f dB; true peak %.3f dBTP; reachable=%t", target,
			report.After.IntegratedLUFS, report.AppliedGainDB, 20*math.Log10(peak), report.Reachable)
	}
	left, right := make([]float32, n), make([]float32, n)
	report, err := NormalizeStereo(48000, left, right, -14, -1)
	if err != nil || report.Reachable || !math.IsInf(report.After.IntegratedLUFS, -1) {
		t.Fatalf("silence normalization failed: %+v err=%v", report, err)
	}
	left[10] = float32(math.NaN())
	if _, err := NormalizeStereo(48000, left, right, -14, -1); err == nil {
		t.Fatal("normalization accepted nonfinite programme")
	}
}

func TestNormalizeShortProgrammeDoesNotInferLUFS(t *testing.T) {
	const n = 4800
	left, right := make([]float32, n), make([]float32, n)
	for i := range left {
		x := float32(.1 * math.Sin(2*math.Pi*1000*float64(i)/48000))
		left[i], right[i] = x, x
	}
	want := append([]float32(nil), left...)
	report, err := NormalizeStereo(48000, left, right, -14, -1)
	if err != nil {
		t.Fatal(err)
	}
	if report.Reachable || report.AppliedGainDB != 0 || !math.IsInf(report.After.IntegratedLUFS, -1) {
		t.Fatalf("a programme shorter than a complete 400 ms block was normalized: %+v", report)
	}
	for i := range left {
		if left[i] != want[i] || right[i] != want[i] {
			t.Fatal("short programme was changed without valid integrated loudness")
		}
	}
}
