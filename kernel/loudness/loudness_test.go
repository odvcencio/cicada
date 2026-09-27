package loudness

import (
	"math"
	"strconv"
	"testing"
)

const (
	// Official English PDF sources: Tech 3341 Table 1 and Tech 3342 Table 1.
	ebu3341 = "https://tech.ebu.ch/docs/tech/tech3341.pdf"
	ebu3342 = "https://tech.ebu.ch/files/live/sites/tech/files/shared/tech/tech3342.pdf"
	pi      = math.Pi
)

func TestEBUTech3341LoudnessCases(t *testing.T) {
	// EBU Tech 3341 v4, Table 1, case #1: stereo 1 kHz, -23 dBFS,
	// 20 seconds; M, S, and I are -23.0 +/- 0.1 LUFS and 0.0 +/- 0.1 LU.
	t.Run("case_1", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		feedSineDB(t, m, 48_000, 20, 1000, -23, 0, false)
		got := m.Metrics()
		assertEBU(t, "Tech 3341 case #1 M", got.MaxMomentaryLUFS, -23, 0.1)
		assertEBU(t, "Tech 3341 case #1 S", got.MaxShortTermLUFS, -23, 0.1)
		assertEBU(t, "Tech 3341 case #1 I", got.IntegratedLUFS, -23, 0.1)
		assertEBU(t, "Tech 3341 case #1 M relative LU", got.MaxMomentaryLUFS+23, 0, 0.1)
		assertEBU(t, "Tech 3341 case #1 S relative LU", got.MaxShortTermLUFS+23, 0, 0.1)
		assertEBU(t, "Tech 3341 case #1 I relative LU", got.IntegratedLUFS+23, 0, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #2: case #1 at -33 dBFS;
	// M, S, and I are -33.0 +/- 0.1 LUFS and -10.0 +/- 0.1 LU.
	t.Run("case_2", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		feedSineDB(t, m, 48_000, 20, 1000, -33, 0, false)
		got := m.Metrics()
		assertEBU(t, "Tech 3341 case #2 M", got.MaxMomentaryLUFS, -33, 0.1)
		assertEBU(t, "Tech 3341 case #2 S", got.MaxShortTermLUFS, -33, 0.1)
		assertEBU(t, "Tech 3341 case #2 I", got.IntegratedLUFS, -33, 0.1)
		assertEBU(t, "Tech 3341 case #2 M relative LU", got.MaxMomentaryLUFS+23, -10, 0.1)
		assertEBU(t, "Tech 3341 case #2 S relative LU", got.MaxShortTermLUFS+23, -10, 0.1)
		assertEBU(t, "Tech 3341 case #2 I relative LU", got.IntegratedLUFS+23, -10, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #3: 10 s at -36, 60 s at -23,
	// 10 s at -36 dBFS; I = -23.0 +/- 0.1 LUFS / 0.0 +/- 0.1 LU.
	t.Run("case_3", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		feedSineDB(t, m, 48_000, 10, 1000, -36, 0, false)
		feedSineDB(t, m, 48_000, 60, 1000, -23, 0, false)
		feedSineDB(t, m, 48_000, 10, 1000, -36, 0, false)
		got := m.Metrics().IntegratedLUFS
		assertEBU(t, "Tech 3341 case #3 I", got, -23, 0.1)
		assertEBU(t, "Tech 3341 case #3 I relative LU", got+23, 0, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #4: 10 s at -72, 10 s at -36,
	// 60 s at -23, 10 s at -36, and 10 s at -72 dBFS; I = -23.0 +/- 0.1 LUFS / 0.0 +/- 0.1 LU.
	t.Run("case_4", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		feedSineDB(t, m, 48_000, 10, 1000, -72, 0, false)
		feedSineDB(t, m, 48_000, 10, 1000, -36, 0, false)
		feedSineDB(t, m, 48_000, 60, 1000, -23, 0, false)
		feedSineDB(t, m, 48_000, 10, 1000, -36, 0, false)
		feedSineDB(t, m, 48_000, 10, 1000, -72, 0, false)
		got := m.Metrics().IntegratedLUFS
		assertEBU(t, "Tech 3341 case #4 I", got, -23, 0.1)
		assertEBU(t, "Tech 3341 case #4 I relative LU", got+23, 0, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #5: 20 s at -26, 20.1 s at -20,
	// 20 s at -26 dBFS; I = -23.0 +/- 0.1 LUFS.
	t.Run("case_5", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		feedSineDB(t, m, 48_000, 20, 1000, -26, 0, false)
		feedSineDB(t, m, 48_000, 20.1, 1000, -20, 0, false)
		feedSineDB(t, m, 48_000, 20, 1000, -26, 0, false)
		got := m.Metrics().IntegratedLUFS
		assertEBU(t, "Tech 3341 case #5 I", got, -23, 0.1)
		assertEBU(t, "Tech 3341 case #5 I relative LU", got+23, 0, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #6: five-channel 1 kHz tones
	// (-28 dBFS L/R, -24 C, -30 Ls/Rs), I = -23.0 +/- 0.1 LUFS.
	// The stereo API receives the exact K-weighted-energy equivalent: all
	// five channels have the same frequency and phase, so their weighted
	// mean-square sum equals that of the derived equal-level stereo pair.
	t.Run("case_6_stereo_energy_equivalent", func(t *testing.T) {
		amplitude := math.Sqrt((2*math.Pow(dbAmplitude(-28), 2) + math.Pow(dbAmplitude(-24), 2) + 2*1.41*math.Pow(dbAmplitude(-30), 2)) / 2)
		m := newTestMeter(t, 48_000)
		feedSineFrames(t, m, 48_000, 20*48_000, 1000, amplitude, 0, false)
		got := m.Metrics().IntegratedLUFS
		assertEBU(t, "Tech 3341 case #6 I", got, -23, 0.1)
		assertEBU(t, "Tech 3341 case #6 I relative LU", got+23, 0, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #7 needs authentic programme 1.
	t.Run("case_7_skipped_recorded_programme", func(t *testing.T) {
		t.Skip("requires EBU authentic programme 1 recording; not a synthetic signal")
	})
	// EBU Tech 3341 v4, Table 1, case #8 needs authentic programme 2.
	t.Run("case_8_skipped_recorded_programme", func(t *testing.T) {
		t.Skip("requires EBU authentic programme 2 recording; not a synthetic signal")
	})

	// EBU Tech 3341 v4, Table 1, case #9: repeat 1.34 s at -20 and
	// 1.66 s at -30 dBFS five times; S = -23.0 +/- 0.1 LUFS after 3 s.
	t.Run("case_9", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		for repeat := 0; repeat < 5; repeat++ {
			feedSineDB(t, m, 48_000, 1.34, 1000, -20, 0, false)
			feedSineDB(t, m, 48_000, 1.66, 1000, -30, 0, false)
			if repeat == 0 {
				assertEBU(t, "Tech 3341 case #9 S after 3 s", m.Metrics().ShortTermLUFS, -23, 0.1)
			}
		}
		got := m.Metrics().ShortTermLUFS
		assertEBU(t, "Tech 3341 case #9 S", got, -23, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #10: file-based meter, 20
	// leading-silence/3 s tone/1 s trailing-silence segments. Each Max S
	// is -23.0 +/- 0.1 LUFS.
	t.Run("case_10", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			m := newTestMeter(t, 48_000)
			feedSilenceFrames(t, m, i*7200)
			feedSineDB(t, m, 48_000, 3, 1000, -23, 0, false)
			feedSilenceFrames(t, m, 48_000)
			assertEBU(t, "Tech 3341 case #10 segment Max S", m.Metrics().MaxShortTermLUFS, -23, 0.1)
		}
	})

	// EBU Tech 3341 v4, Table 1, case #11: live meter, successive 3 s
	// tones at -38 through -19 dBFS; Max S follows those 20 levels.
	t.Run("case_11", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		for i := 0; i < 20; i++ {
			feedSilenceFrames(t, m, i*7200)
			feedSineDB(t, m, 48_000, 3, 1000, float64(-38+i), 0, false)
			feedSilenceFrames(t, m, (3*48_000)-i*7200)
			assertEBU(t, "Tech 3341 case #11 successive Max S", m.Metrics().MaxShortTermLUFS, float64(-38+i), 0.1)
		}
	})

	// EBU Tech 3341 v4, Table 1, case #12: repeat 180 ms at -20 and
	// 220 ms at -30 dBFS; M = -23.0 +/- 0.1 LUFS after 1 s and remains steady.
	t.Run("case_12", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		for range 2 {
			feedSineDB(t, m, 48_000, 0.18, 1000, -20, 0, false)
			feedSineDB(t, m, 48_000, 0.22, 1000, -30, 0, false)
		}
		feedSineDB(t, m, 48_000, 0.18, 1000, -20, 0, false)
		feedSineDB(t, m, 48_000, 0.02, 1000, -30, 0, false)
		assertEBU(t, "Tech 3341 case #12 M after 1 s", m.Metrics().MomentaryLUFS, -23, 0.1)
		feedSineDB(t, m, 48_000, 0.20, 1000, -30, 0, false)
		for range 24 {
			feedSineDB(t, m, 48_000, 0.18, 1000, -20, 0, false)
			feedSineDB(t, m, 48_000, 0.22, 1000, -30, 0, false)
		}
		assertEBU(t, "Tech 3341 case #12 steady M", m.Metrics().MomentaryLUFS, -23, 0.1)
	})

	// EBU Tech 3341 v4, Table 1, case #13: file-based meter, 20
	// silence/400 ms tone/1 s silence segments; each Max M is -23.0 +/- 0.1.
	t.Run("case_13", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			m := newTestMeter(t, 48_000)
			feedSilenceFrames(t, m, 960*i)
			feedSineDB(t, m, 48_000, 0.4, 1000, -23, 0, false)
			feedSilenceFrames(t, m, 48_000)
			assertEBU(t, "Tech 3341 case #13 segment Max M", m.Metrics().MaxMomentaryLUFS, -23, 0.1)
		}
	})

	// EBU Tech 3341 v4, Table 1, case #14: live meter, successive
	// 400 ms tones at -38 through -19 dBFS with the specified silence gaps.
	t.Run("case_14", func(t *testing.T) {
		m := newTestMeter(t, 48_000)
		for i := 0; i < 20; i++ {
			feedSilenceFrames(t, m, 960*i)
			feedSineDB(t, m, 48_000, 0.4, 1000, float64(-38+i), 0, false)
			feedSilenceFrames(t, m, 19_200-960*i)
			assertEBU(t, "Tech 3341 case #14 successive Max M", m.Metrics().MaxMomentaryLUFS, float64(-38+i), 0.1)
		}
	})
}

func TestEBUTech3341TruePeakCases(t *testing.T) {
	cases := []struct {
		caseNo int
		freq   float64
		amp    float64
		phase  float64
		want   float64
		fade   bool
	}{
		{15, 12_000, 0.50, 0, -6, true},          // EBU Tech 3341 case #15
		{16, 12_000, 0.50, pi / 4, -6, false},    // EBU Tech 3341 case #16
		{17, 8_000, 0.50, pi / 3, -6, false},     // EBU Tech 3341 case #17
		{18, 6_000, 0.50, 3 * pi / 8, -6, false}, // EBU Tech 3341 case #18
		{19, 12_000, 1.41, pi / 4, 3, false},     // EBU Tech 3341 case #19
	}
	for _, tc := range cases {
		t.Run(itoa(tc.caseNo), func(t *testing.T) {
			m := newTestMeter(t, 48_000)
			feedFrequency(t, m, 48_000, 48_000, tc.freq, tc.amp, tc.phase, tc.fade)
			if err := m.Finish(); err != nil {
				t.Fatal(err)
			}
			got := m.Metrics().TruePeakDBTP
			assertEBURange(t, "Tech 3341 true-peak case #"+itoa(tc.caseNo), got, tc.want, -0.4, 0.2)
		})
	}

	// EBU Tech 3341 v4, Table 1, cases #20-23: synthesize at 4*fs,
	// replace one period with a phase-matched fs/4 sine, low-pass filter,
	// then decimate with offsets 0, 1, 2, and 3 samples at 4*fs.
	for offset := 0; offset < 4; offset++ {
		caseNo := 20 + offset
		t.Run(itoa(caseNo), func(t *testing.T) {
			left, right := synthesizeDownsampledPeakCase(48_000, offset)
			m := newTestMeter(t, 48_000)
			if err := m.ProcessBlock(left, right); err != nil {
				t.Fatal(err)
			}
			if err := m.Finish(); err != nil {
				t.Fatal(err)
			}
			got := m.Metrics().TruePeakDBTP
			assertEBURange(t, "Tech 3341 true-peak case #"+itoa(caseNo), got, 0, -0.4, 0.2)
		})
	}
}

func TestEBUTech3342LoudnessRangeCases(t *testing.T) {
	cases := []struct {
		caseNo int
		levels []float64
		want   float64
	}{
		{1, []float64{-20, -30}, 10},
		{2, []float64{-20, -15}, 5},
		{3, []float64{-40, -20}, 20},
		{4, []float64{-50, -35, -20, -35, -50}, 15},
	}
	for _, tc := range cases {
		t.Run(itoa(tc.caseNo), func(t *testing.T) {
			m := newTestMeter(t, 48_000)
			for _, db := range tc.levels {
				feedSineDB(t, m, 48_000, 20, 1000, db, 0, false)
			}
			assertEBU(t, "Tech 3342 case #"+itoa(tc.caseNo)+" LRA", m.Metrics().LoudnessRange, tc.want, 1)
		})
	}
	// EBU Tech 3342 v4, Table 1, cases #5 and #6 require authentic
	// narrow- and wide-range programme recordings.
	t.Run("case_5_skipped_recorded_programme", func(t *testing.T) {
		t.Skip("requires EBU authentic programme 1 recording; not a synthetic signal")
	})
	t.Run("case_6_skipped_recorded_programme", func(t *testing.T) {
		t.Skip("requires EBU authentic programme 2 recording; not a synthetic signal")
	})
}

func TestNewSupportsRequiredRates(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		m, err := New(rate)
		if err != nil {
			t.Fatalf("New(%d): %v", rate, err)
		}
		feedSineFrames(t, m, rate, rate, 1000, dbAmplitude(-23), 0, false)
		assertEBU(t, "1 s stereo calibration at "+itoa(rate), m.Metrics().MomentaryLUFS, -23, 0.1)
	}
	if _, err := New(32_000); err != ErrSampleRate {
		t.Fatalf("New(32000) error = %v, want %v", err, ErrSampleRate)
	}
}

func TestProcessSampleDoesNotAllocate(t *testing.T) {
	m := newTestMeter(t, 48_000)
	var left, right [1024]float32
	allocs := testing.AllocsPerRun(100, func() {
		m.Reset()
		if err := m.ProcessBlock(left[:], right[:]); err != nil {
			t.Fatal(err)
		}
		_ = m.Metrics()
		if err := m.Finish(); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("processing, snapshot, or Finish allocated %.2f objects after New", allocs)
	}
}

func TestResetAndInputValidation(t *testing.T) {
	m := newTestMeter(t, 48_000)
	if err := m.ProcessBlock([]float32{0}, nil); err != ErrChannelCount {
		t.Fatalf("mismatched channels: got %v", err)
	}
	if err := m.ProcessBlock([]float32{float32(math.NaN())}, []float32{0}); err != ErrNonFinite {
		t.Fatalf("non-finite input: got %v", err)
	}
	m.Reset()
	if got := m.Metrics().Frames; got != 0 {
		t.Fatalf("Reset frame count = %d", got)
	}
	if !m.ProcessSample(0.1, 0.1) {
		t.Fatal("meter did not accept input after Reset")
	}
}

func newTestMeter(t *testing.T, rate int) *Meter {
	t.Helper()
	m, err := New(rate)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func feedSineDB(t *testing.T, m *Meter, rate int, seconds, frequency, db, phase float64, fade bool) {
	t.Helper()
	feedSineFrames(t, m, rate, int(math.Round(seconds*float64(rate))), frequency, dbAmplitude(db), phase, fade)
}

func feedSineFrames(t *testing.T, m *Meter, rate, frames int, frequency, amplitude, phase float64, fade bool) {
	t.Helper()
	var left, right [4096]float32
	for start := 0; start < frames; {
		count := min(len(left), frames-start)
		for i := 0; i < count; i++ {
			frame := start + i
			value := amplitude * math.Sin(2*pi*frequency*float64(frame)/float64(rate)+phase)
			if fade {
				fadeFrames := rate / 100
				if frame < fadeFrames {
					value *= float64(frame) / float64(fadeFrames)
				}
				if frames-frame <= fadeFrames {
					value *= float64(frames-frame-1) / float64(fadeFrames)
				}
			}
			left[i], right[i] = float32(value), float32(value)
		}
		if err := m.ProcessBlock(left[:count], right[:count]); err != nil {
			t.Fatal(err)
		}
		start += count
	}
}

func feedSilenceFrames(t *testing.T, m *Meter, frames int) {
	t.Helper()
	var zero [4096]float32
	for frames > 0 {
		count := min(frames, len(zero))
		if err := m.ProcessBlock(zero[:count], zero[:count]); err != nil {
			t.Fatal(err)
		}
		frames -= count
	}
}

func feedFrequency(t *testing.T, m *Meter, rate, frames int, frequency, amplitude, phase float64, fade bool) {
	t.Helper()
	var left, right [4096]float32
	for start := 0; start < frames; {
		count := min(len(left), frames-start)
		for i := 0; i < count; i++ {
			frame := start + i
			value := amplitude * math.Sin(2*pi*frequency*float64(frame)/float64(rate)+phase)
			if fade {
				fadeFrames := rate / 100
				if frame < fadeFrames {
					value *= float64(frame) / float64(fadeFrames)
				}
				if frames-frame <= fadeFrames {
					value *= float64(frames-frame-1) / float64(fadeFrames)
				}
			}
			left[i], right[i] = float32(value), float32(value)
		}
		if err := m.ProcessBlock(left[:count], right[:count]); err != nil {
			t.Fatal(err)
		}
		start += count
	}
}

func synthesizeDownsampledPeakCase(rate, offset int) ([]float32, []float32) {
	frames := rate / 2
	highRate := rate * 4
	burstStart := highRate / 4
	high := make([]float64, highRate/2)
	for i := range high {
		time := float64(i) / float64(highRate)
		phase := -pi / 6
		value := 0.5 * math.Sin(2*pi*float64(rate/6)*time+phase)
		if i >= burstStart && i < burstStart+16 {
			value = math.Sin(phase + 2*pi*float64(rate/4)*float64(i-burstStart)/float64(highRate))
		}
		fadeFrames := highRate / 100
		if i < fadeFrames {
			value *= float64(i) / float64(fadeFrames)
		}
		if len(high)-i <= fadeFrames {
			value *= float64(len(high)-i-1) / float64(fadeFrames)
		}
		high[i] = value
	}
	const taps = 97
	const middle = taps / 2
	var lowpass [taps]float64
	sum := 0.0
	for tap := range lowpass {
		x := float64(tap - middle)
		coefficient := 0.25
		if x != 0 {
			coefficient *= math.Sin(0.25*pi*x) / (0.25 * pi * x)
		}
		window := 0.42 - 0.5*math.Cos(2*pi*float64(tap)/float64(taps-1)) + 0.08*math.Cos(4*pi*float64(tap)/float64(taps-1))
		lowpass[tap] = coefficient * window
		sum += lowpass[tap]
	}
	for tap := range lowpass {
		lowpass[tap] /= sum
	}
	left, right := make([]float32, frames), make([]float32, frames)
	for frame := 0; frame < frames; frame++ {
		center := frame*4 + offset
		value := 0.0
		for tap, coefficient := range lowpass {
			sample := center + tap - middle
			if sample >= 0 && sample < len(high) {
				value += high[sample] * coefficient
			}
		}
		left[frame], right[frame] = float32(value), float32(value)
	}
	return left, right
}

func dbAmplitude(db float64) float64 { return math.Pow(10, db/20) }

func assertEBU(t *testing.T, name string, got, want, tolerance float64) {
	t.Helper()
	pass := math.Abs(got-want) <= tolerance
	status := "PASS"
	if !pass {
		status = "FAIL"
	}
	t.Logf("%s | expected %.2f +/- %.2f | measured %.4f | %s", name, want, tolerance, got, status)
	if !pass {
		t.Fatalf("%s measured %.4f, expected %.2f +/- %.2f", name, got, want, tolerance)
	}
}

func assertEBURange(t *testing.T, name string, got, want, lowerTolerance, upperTolerance float64) {
	t.Helper()
	pass := got >= want+lowerTolerance && got <= want+upperTolerance
	status := "PASS"
	if !pass {
		status = "FAIL"
	}
	t.Logf("%s | expected %.2f +%.2f/%.2f | measured %.4f | %s", name, want, upperTolerance, lowerTolerance, got, status)
	if !pass {
		t.Fatalf("%s measured %.4f, expected %.2f +%.2f/%.2f", name, got, want, upperTolerance, lowerTolerance)
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
