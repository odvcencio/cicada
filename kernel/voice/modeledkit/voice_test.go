package modeledkit

import (
	"math"
	"testing"
)

func newTestVoice(t *testing.T, profile Profile, rate int) *Voice {
	t.Helper()
	v, err := NewVoice(profile, rate, 4242)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func metrics(v *Voice, frames int) (rms, brightness, peak, dc float64) {
	energy, difference, sum, previous := 0.0, 0.0, 0.0, 0.0
	for i := 0; i < frames; i++ {
		x := float64(v.Next())
		energy += x * x
		difference += (x - previous) * (x - previous)
		sum += x
		peak = math.Max(peak, math.Abs(x))
		previous = x
	}
	rms = math.Sqrt(energy / float64(frames))
	// First-difference energy is a frequency-weighted spectral moment:
	// a sinusoid contributes 4*sin(pi*f/rate)^2 times its energy.
	if energy > 0 {
		brightness = math.Sqrt(difference / energy)
	}
	dc = sum / float64(frames)
	return
}

func TestProfileNamesAndValidation(t *testing.T) {
	if ProfileCount != 15 {
		t.Fatal("kit must expose 15 pieces and articulations")
	}
	for profile := Profile(0); profile < ProfileCount; profile++ {
		parsed, ok := ParseProfile(Names[profile])
		if !ok || parsed != profile {
			t.Fatalf("profile %d did not round-trip", profile)
		}
		if IsHat(profile) != (profile >= HatClosed && profile <= HatOpen) {
			t.Fatalf("profile %s has wrong choke family", Names[profile])
		}
	}
	if _, ok := ParseProfile("recorded_drum"); ok {
		t.Fatal("unknown profile accepted")
	}
	if _, err := NewVoice(ProfileCount, 48_000, 1); err == nil {
		t.Fatal("invalid profile accepted")
	}
	if _, err := NewVoice(Kick, 22_050, 1); err == nil {
		t.Fatal("unsupported rate accepted")
	}
	for _, p := range []Params{
		{.49, 1, .35, .015}, {2.01, 1, .35, .015},
		{1, .24, .35, .015}, {1, 2.01, .35, .015},
		{1, 1, -.01, .015}, {1, 1, 1.01, .015},
		{1, 1, .35, -.01}, {1, 1, .35, .11},
		{math.NaN(), 1, .35, .015}, {1, math.Inf(1), .35, .015},
	} {
		v := newTestVoice(t, Kick, 48_000)
		if v.SetParams(p) == nil || v.Params() != DefaultParams() {
			t.Fatal("invalid parameters accepted or partially applied")
		}
	}
	for _, p := range []Params{{.5, .25, 0, 0}, {2, 2, 1, .1}, DefaultParams()} {
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVelocityLevelAndBrightness(t *testing.T) {
	for profile := Profile(0); profile < ProfileCount; profile++ {
		t.Run(Names[profile], func(t *testing.T) {
			v := newTestVoice(t, profile, 48_000)
			previousRMS, previousBrightness := 0.0, 0.0
			for _, velocity := range []uint8{24, 48, 80, 110, 127} {
				v.Reset()
				v.Hit(velocity)
				rms, brightness, peak, _ := metrics(v, 4800)
				t.Logf("METRIC modeledkit profile=%s velocity=%d rms_dbfs=%.3f brightness=%.6f peak_dbfs=%.3f", Names[profile], velocity, 20*math.Log10(rms), brightness, 20*math.Log10(peak))
				if rms <= previousRMS || brightness <= previousBrightness {
					t.Errorf("velocity %d: RMS %.6f after %.6f; brightness %.5f after %.5f", velocity, rms, previousRMS, brightness, previousBrightness)
				}
				if peak > 1.2 {
					t.Errorf("velocity %d peak %.5f exceeds reserved headroom", velocity, peak)
				}
				previousRMS, previousBrightness = rms, brightness
			}
		})
	}
}

func TestFourStrikeVariationsAndReset(t *testing.T) {
	for profile := Profile(0); profile < ProfileCount; profile++ {
		v := newTestVoice(t, profile, 48_000)
		var strikes [5][128]float32
		for i := range strikes {
			v.Choke()
			for frame := 0; frame < 240; frame++ {
				v.Next()
			}
			v.Hit(100)
			for frame := range strikes[i] {
				strikes[i][frame] = v.Next()
			}
		}
		for i := 0; i < 4; i++ {
			for j := 0; j < i; j++ {
				if strikes[i] == strikes[j] {
					t.Fatalf("%s variations %d and %d are identical", Names[profile], i, j)
				}
			}
		}
		if strikes[0] != strikes[4] {
			t.Fatalf("%s variation sequence is not four strikes", Names[profile])
		}
		v.Reset()
		if v.Active() {
			t.Fatal("reset retained ringing state")
		}
		for frame := 0; frame < 128; frame++ {
			if v.Next() != 0 {
				t.Fatal("reset retained noise")
			}
		}
		v.Hit(0)
		if v.Active() || v.Next() != 0 {
			t.Fatal("velocity zero excited the voice")
		}
		v.Hit(100)
		for frame := range strikes[0] {
			if v.Next() != strikes[0][frame] {
				t.Fatalf("%s reset did not reproduce strike at %d", Names[profile], frame)
			}
		}
	}
}

func TestVelocityZeroPreservesRingingTail(t *testing.T) {
	for profile := Profile(0); profile < ProfileCount; profile++ {
		v := newTestVoice(t, profile, 48_000)
		v.Hit(100)
		for frame := 0; frame < 128; frame++ {
			v.Next()
		}
		control := *v
		v.Hit(0)
		if *v != control {
			t.Fatalf("%s velocity zero changed ringing state", Names[profile])
		}
		for frame := 0; frame < 128; frame++ {
			if v.Next() != control.Next() {
				t.Fatalf("%s velocity zero changed tail at frame %d", Names[profile], frame)
			}
		}
	}
}

func TestAllRatesFiniteDecayAndNoiseFloor(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for profile := Profile(0); profile < ProfileCount; profile++ {
			v := newTestVoice(t, profile, rate)
			v.Hit(127)
			peak, sum := 0.0, 0.0
			frames := int(3*max(recipes[profile].t60, recipes[profile].noiseT60)*float64(rate)) + rate/10
			for frame := 0; frame < frames; frame++ {
				x := float64(v.Next())
				if math.IsNaN(x) || math.IsInf(x, 0) {
					t.Fatalf("%s at %d Hz produced nonfinite audio", Names[profile], rate)
				}
				peak = math.Max(peak, math.Abs(x))
				sum += x
			}
			dc := math.Abs(sum / float64(frames))
			t.Logf("METRIC modeledkit profile=%s rate=%d peak_dbfs=%.3f dc_dbfs=%.3f silence_after_s=%.2f", Names[profile], rate, 20*math.Log10(peak), 20*math.Log10(dc), float64(frames)/float64(rate))
			if peak < .02 || peak > 1.2 || dc > .001 {
				t.Errorf("%s at %d Hz: peak %.5f; DC %.7f", Names[profile], rate, peak, dc)
			}
			if v.Active() || v.Next() != 0 {
				t.Errorf("%s at %d Hz retained a tail after %.1f seconds", Names[profile], rate, float64(frames)/float64(rate))
			}
		}
	}
}

func TestChokeAndRetriggerAreBounded(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for profile := Profile(0); profile < ProfileCount; profile++ {
			v := newTestVoice(t, profile, rate)
			v.Hit(110)
			for i := 0; i < rate/50; i++ {
				v.Next()
			}
			v.Hit(127)
			if v.fadeLeft != rate/1000 || !v.old.active {
				t.Fatal("retrigger did not preserve a bounded predecessor")
			}
			for i := 0; i < rate/1000; i++ {
				v.Next()
			}
			if v.old.active || v.fadeLeft != 0 {
				t.Fatal("retrigger tail exceeded 1 ms")
			}
			v.Choke()
			for i := 0; i < rate/200; i++ {
				v.Choke()
				v.Next()
			}
			if v.Active() || v.Next() != 0 {
				t.Fatalf("%s choke exceeded 5 ms at %d Hz", Names[profile], rate)
			}
		}
	}
}

func TestRenderAndStrikeDoNotAllocate(t *testing.T) {
	for profile := Profile(0); profile < ProfileCount; profile++ {
		v := newTestVoice(t, profile, 48_000)
		allocations := testing.AllocsPerRun(100, func() {
			v.Hit(100)
			for i := 0; i < 128; i++ {
				v.Next()
			}
			v.Choke()
		})
		if allocations != 0 {
			t.Fatalf("%s allocated %.2f times per strike/block/choke", Names[profile], allocations)
		}
	}
}

// Fourier peak search measures the rendered settled membrane rather than
// inspecting oscillator coefficients. Resolution is 0.25% of expected pitch.
func renderedPitch(v *Voice, expected float64) float64 {
	v.Hit(127)
	for i := 0; i < 9600; i++ {
		v.Next()
	}
	var samples [8192]float64
	for i := range samples {
		samples[i] = float64(v.Next()) * (.5 - .5*math.Cos(2*math.Pi*float64(i)/float64(len(samples)-1)))
	}
	bestFrequency, bestEnergy := 0.0, 0.0
	for step := -80; step <= 80; step++ {
		frequency := expected * (1 + .0025*float64(step))
		sin, cos := math.Sincos(2 * math.Pi * frequency / 48_000)
		x, y, real, imaginary := 1.0, 0.0, 0.0, 0.0
		for _, sample := range samples {
			real += x * sample
			imaginary += y * sample
			x, y = cos*x-sin*y, sin*x+cos*y
		}
		energy := real*real + imaginary*imaginary
		if energy > bestEnergy {
			bestEnergy, bestFrequency = energy, frequency
		}
	}
	return bestFrequency
}

func TestTuneDecayAndStrikePosition(t *testing.T) {
	for _, profile := range []Profile{Kick, TomLow, TomMid, TomHigh} {
		v := newTestVoice(t, profile, 48_000)
		p := DefaultParams()
		p.Tune, p.Humanize = 1.4, 0
		if err := v.SetParams(p); err != nil {
			t.Fatal(err)
		}
		expected := recipes[profile].frequency * p.Tune
		frequency := renderedPitch(v, expected)
		t.Logf("METRIC modeledkit profile=%s tune=%.2f rendered_tuning_cents=%.3f", Names[profile], p.Tune, 1200*math.Log2(frequency/expected))
		if math.Abs(1200*math.Log2(frequency/expected)) > 5 {
			t.Errorf("%s tuned fundamental: got %.3f Hz, want %.3f Hz", Names[profile], frequency, expected)
		}
	}
	for _, profile := range []Profile{TomLow, Snare, RideBow} {
		short := newTestVoice(t, profile, 48_000)
		long := newTestVoice(t, profile, 48_000)
		p := DefaultParams()
		p.Decay = .5
		if err := short.SetParams(p); err != nil {
			t.Fatal(err)
		}
		p.Decay = 1.5
		if err := long.SetParams(p); err != nil {
			t.Fatal(err)
		}
		short.Hit(100)
		long.Hit(100)
		for i := 0; i < 24_000; i++ {
			short.Next()
			long.Next()
		}
		shortRMS, _, _, _ := metrics(short, 4800)
		longRMS, _, _, _ := metrics(long, 4800)
		if longRMS < 3*shortRMS {
			t.Errorf("%s decay did not lengthen tail: short %g long %g", Names[profile], shortRMS, longRMS)
		}
	}
	center := newTestVoice(t, TomLow, 48_000)
	edge := newTestVoice(t, TomLow, 48_000)
	p := DefaultParams()
	p.Position = 0
	if err := center.SetParams(p); err != nil {
		t.Fatal(err)
	}
	p.Position = 1
	if err := edge.SetParams(p); err != nil {
		t.Fatal(err)
	}
	center.Hit(127)
	edge.Hit(127)
	_, centerBrightness, _, _ := metrics(center, 4800)
	_, edgeBrightness, _, _ := metrics(edge, 4800)
	if edgeBrightness <= centerBrightness {
		t.Errorf("edge strike did not increase upper membrane energy: center %g edge %g", centerBrightness, edgeBrightness)
	}
}

func BenchmarkVoice(b *testing.B) {
	for profile := Profile(0); profile < ProfileCount; profile++ {
		b.Run(Names[profile], func(b *testing.B) {
			v, err := NewVoice(profile, 48_000, 4242)
			if err != nil {
				b.Fatal(err)
			}
			v.Hit(110)
			for frame := 0; frame < 128; frame++ {
				v.Next()
			}
			// Keep the bank and filtered noise active throughout the timing
			// run. Otherwise short hats turn into a silence benchmark.
			v.current.remaining = int(^uint(0) >> 1)
			v.current.noisePole = 1
			for i := 0; i < v.current.count; i++ {
				m := &v.current.modes[i]
				radius := math.Hypot(m.targetC, m.targetS)
				m.c, m.s = m.targetC/radius, m.targetS/radius
				m.targetC, m.targetS = m.c, m.s
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for frame := 0; frame < 128; frame++ {
					v.Next()
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*128), "ns/sample")
		})
	}
}

func BenchmarkHit(b *testing.B) {
	for _, profile := range []Profile{Kick, Snare, Crash} {
		b.Run(Names[profile], func(b *testing.B) {
			v, err := NewVoice(profile, 48_000, 4242)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v.Hit(110)
			}
		})
	}
}

func TestExtremeControlsKeepPolesStableAndInBand(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for profile := Profile(0); profile < ProfileCount; profile++ {
			for _, p := range []Params{{.5, .25, 0, 0}, {2, 2, 1, .1}} {
				v := newTestVoice(t, profile, rate)
				if err := v.SetParams(p); err != nil {
					t.Fatal(err)
				}
				for variation := 0; variation < 4; variation++ {
					v.Hit(127)
					for i := 0; i < v.current.count; i++ {
						m := &v.current.modes[i]
						for _, pole := range [][2]float64{{m.c, m.s}, {m.targetC, m.targetS}} {
							if radius := math.Hypot(pole[0], pole[1]); radius <= 0 || radius >= 1 {
								t.Fatalf("%s at %d Hz has unstable radius %g", Names[profile], rate, radius)
							}
							if frequency := math.Atan2(pole[1], pole[0]) * float64(rate) / (2 * math.Pi); frequency >= .45*float64(rate) {
								t.Fatalf("%s at %d Hz includes an out-of-band mode at %.2f Hz", Names[profile], rate, frequency)
							}
						}
					}
					for frame := 0; frame < 4096; frame++ {
						x := float64(v.Next())
						if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 3 {
							t.Fatalf("%s extreme controls produced invalid audio at %d Hz", Names[profile], rate)
						}
					}
				}
			}
		}
	}
}

func TestRetainedModalTailAgainst96kReference(t *testing.T) {
	// Compare the linear post-contact resonances at two time steps. This is
	// an oscillator alias/noise measurement, not a qualification of filtered
	// random excitation, contact transients, or acoustic realism.
	for profile := Profile(0); profile < ProfileCount; profile++ {
		for _, tune := range []float64{1, 2} {
			v := newTestVoice(t, profile, 48_000)
			p := DefaultParams()
			p.Tune, p.Humanize = tune, 0
			if err := v.SetParams(p); err != nil {
				t.Fatal(err)
			}
			v.Hit(127)
			for frame := 0; frame < 128; frame++ {
				v.Next()
			}
			v.current.noiseAmp, v.current.bodyCoupling = 0, 0
			v.current.filter = bandFilter{}
			v.current.age = v.current.contact
			for i := 0; i < v.current.count; i++ {
				m := &v.current.modes[i]
				m.c, m.s = m.targetC, m.targetS
			}
			ref := newTestVoice(t, profile, 96_000)
			ref.current = v.current
			ref.current.remaining *= 2
			for i := 0; i < ref.current.count; i++ {
				m := &ref.current.modes[i]
				radius := math.Hypot(m.c, m.s)
				m.c = math.Sqrt((radius + m.c) / 2)
				m.s /= 2 * m.c
				m.targetC, m.targetS = m.c, m.s
			}
			signal, residual := 0.0, 0.0
			for frame := 0; frame < 8192; frame++ {
				ref.Next()
				want, got := float64(ref.Next()), float64(v.Next())
				signal += want * want
				residual += (got - want) * (got - want)
			}
			db := 10 * math.Log10(residual/signal)
			t.Logf("METRIC modeledkit profile=%s tune=%.2f retained_modal_96k_residual_db=%.3f", Names[profile], tune, db)
			if db > -110 {
				t.Errorf("%s retained modal tail residual %.3f dB exceeds -110 dB", Names[profile], db)
			}
		}
	}
}
