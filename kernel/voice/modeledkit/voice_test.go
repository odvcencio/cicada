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
	if ProfileCount != 17 {
		t.Fatal("kit must expose 17 pieces and articulations")
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
			hold := 0.0
			if profile == BrushSweep {
				hold = sweepHold
			}
			frames := int((3*max(recipes[profile].t60, recipes[profile].noiseT60)+hold)*float64(rate)) + rate/10
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
	for _, profile := range []Profile{Kick, Snare, Crash, CrossStick, HatClosed, BrushTap, BrushSweep} {
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

func TestProfileNumbersMatchKernelImages(t *testing.T) {
	// A kernel image stores the profile number: the first fifteen pieces keep
	// theirs and the brush pieces take the next two.
	for want, name := range []string{
		"kick", "snare", "rimshot", "cross_stick", "tom_low", "tom_mid", "tom_high",
		"hat_closed", "hat_pedal", "hat_half_open", "hat_open", "ride_bow", "ride_bell", "crash", "splash",
		"brush_tap", "brush_sweep",
	} {
		if got, ok := ParseProfile(name); !ok || int(got) != want {
			t.Fatalf("%s is profile %d, want %d", name, got, want)
		}
	}
	for _, profile := range []Profile{BrushTap, BrushSweep} {
		if IsHat(profile) || isMetal(profile) {
			t.Fatalf("%s must not take the hat choke or the plate model", Names[profile])
		}
	}
}

func TestBrushPiecesAreAudibleBoundedAndDeterministic(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, profile := range []Profile{BrushTap, BrushSweep} {
			for _, velocity := range []uint8{40, 100, 127} {
				play := func(seed uint32) []float32 {
					v, err := NewVoice(profile, rate, seed)
					if err != nil {
						t.Fatal(err)
					}
					v.Hit(velocity)
					out := make([]float32, 2*rate)
					for i := range out {
						out[i] = v.Next()
					}
					return out
				}
				first, again, other := play(4242), play(4242), play(4243)
				peak, energy, sum := 0.0, 0.0, 0.0
				same := true
				for i, sample := range first {
					x := float64(sample)
					if math.IsNaN(x) || math.IsInf(x, 0) {
						t.Fatalf("%s at %d Hz produced nonfinite audio", Names[profile], rate)
					}
					peak = math.Max(peak, math.Abs(x))
					energy += x * x
					sum += x
					same = same && sample == other[i]
				}
				t.Logf("METRIC modeledkit profile=%s rate=%d velocity=%d peak_dbfs=%.3f rms_2s_dbfs=%.3f dc_dbfs=%.3f", Names[profile], rate, velocity, 20*math.Log10(peak), 10*math.Log10(energy/float64(len(first))), 20*math.Log10(math.Abs(sum)/float64(len(first))))
				if peak < .005 || peak > 1.2 {
					t.Errorf("%s at %d Hz velocity %d: peak %.5f is silent or over the reserved headroom", Names[profile], rate, velocity, peak)
				}
				if math.Abs(sum)/float64(len(first)) > .001 {
					t.Errorf("%s at %d Hz velocity %d: DC %.7f", Names[profile], rate, velocity, sum/float64(len(first)))
				}
				for i := range first {
					if first[i] != again[i] {
						t.Fatalf("%s at %d Hz velocity %d: the same seed changed at frame %d", Names[profile], rate, velocity, i)
					}
				}
				if same {
					t.Errorf("%s at %d Hz velocity %d: the seed did not change the noise", Names[profile], rate, velocity)
				}
			}
		}
	}
}

// lastAudible plays a sweep and returns its last frame above -60 dBFS. A gate
// of zero is never closed; otherwise Choke closes it at that many seconds.
func lastAudible(t *testing.T, rate int, params Params, gate float64) int {
	t.Helper()
	v := newTestVoice(t, BrushSweep, rate)
	if err := v.SetParams(params); err != nil {
		t.Fatal(err)
	}
	v.Hit(100)
	last, shut := 0, int(gate*float64(rate))
	for i := 0; i < 8*rate; i++ {
		if gate > 0 && i == shut {
			v.Choke()
		}
		if math.Abs(float64(v.Next())) > 1e-3 {
			last = i
		}
	}
	return last
}

func TestBrushSweepLengthFollowsItsGate(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		var lengths []int
		for _, gate := range []float64{.25, .6, 1.2} {
			last := lastAudible(t, rate, DefaultParams(), gate)
			lengths = append(lengths, last)
			t.Logf("METRIC modeledkit profile=brush_sweep rate=%d gate_s=%.2f last_audible_s=%.4f", rate, gate, float64(last)/float64(rate))
			// The gate closes through Choke, so the swish stops within its 5 ms fade.
			if open := int(gate * float64(rate)); last < open || last > open+rate/200 {
				t.Errorf("%d Hz: gate of %.2f s ended the sweep at frame %d, want %d to %d", rate, gate, last, open, open+rate/200)
			}
		}
		if got, want := lengths[2]-lengths[1], int(.6*float64(rate)); got < want-rate/200 || got > want+rate/200 {
			t.Errorf("%d Hz: a gate 0.6 s longer made the sweep %d frames longer, want %d", rate, got, want)
		}
		// A gate held past the hold does not stretch the sweep: it fades on its own.
		hold := int(sweepHold * float64(rate))
		release := int(3 * recipes[BrushSweep].noiseT60 * float64(rate))
		if last := lastAudible(t, rate, DefaultParams(), 3); last < hold || last > hold+release {
			t.Errorf("%d Hz: a gate held for 3 s ended the sweep at frame %d, want %d to %d", rate, last, hold, hold+release)
		}
	}
}

func TestBrushSweepHoldFollowsDecayAndRestartsOnRetrigger(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, decay := range []float64{.25, .5, 1, 2} {
			p := DefaultParams()
			p.Decay = decay
			last := lastAudible(t, rate, p, 0)
			hold := int(sweepHold * decay * float64(rate))
			release := int(3 * recipes[BrushSweep].noiseT60 * decay * float64(rate))
			t.Logf("METRIC modeledkit profile=brush_sweep rate=%d decay=%.2f ungated_last_audible_s=%.4f", rate, decay, float64(last)/float64(rate))
			if last < hold || last > hold+release {
				t.Errorf("%d Hz decay %.2f: the sweep ended at frame %d, want %d to %d", rate, decay, last, hold, hold+release)
			}
		}
		v := newTestVoice(t, BrushSweep, rate)
		v.Hit(100)
		for i := 0; i < rate/2; i++ {
			v.Next()
		}
		v.Hit(100)
		last := rate / 2
		for i := 0; i < 8*rate; i++ {
			if math.Abs(float64(v.Next())) > 1e-3 {
				last = rate/2 + i
			}
		}
		hold := int((.5 + sweepHold) * float64(rate))
		release := int(3 * recipes[BrushSweep].noiseT60 * float64(rate))
		if last < hold || last > hold+release {
			t.Errorf("%d Hz: a retriggered sweep ended at frame %d, want %d to %d", rate, last, hold, hold+release)
		}
	}
}

func TestBrushSweepHoldsLevelAndBrushTapDecays(t *testing.T) {
	const rate = 48_000
	windows := func(profile Profile, window int, count int) []float64 {
		v := newTestVoice(t, profile, rate)
		v.Hit(100)
		out := make([]float64, count)
		for w := range out {
			energy := 0.0
			for i := 0; i < window; i++ {
				x := float64(v.Next())
				energy += x * x
			}
			out[w] = math.Sqrt(energy / float64(window))
		}
		return out
	}
	// Three half-second windows inside the hold, each about one stroke long.
	held := windows(BrushSweep, rate/2, 3)
	t.Logf("METRIC modeledkit profile=brush_sweep half_second_rms_dbfs=%.3f,%.3f,%.3f", 20*math.Log10(held[0]), 20*math.Log10(held[1]), 20*math.Log10(held[2]))
	if ratio := math.Max(held[0], math.Max(held[1], held[2])) / math.Min(held[0], math.Min(held[1], held[2])); ratio > 1.26 {
		t.Errorf("a held sweep changed level by %.2f dB between half-second windows", 20*math.Log10(ratio))
	}
	tap := windows(BrushTap, rate/10, 3)
	t.Logf("METRIC modeledkit profile=brush_tap tenth_second_rms_dbfs=%.3f,%.3f,%.3f", 20*math.Log10(tap[0]), 20*math.Log10(tap[1]), 20*math.Log10(tap[2]))
	if tap[1] > tap[0]/4 || tap[2] > tap[1]/4 {
		t.Errorf("a brush tap did not die away: %g, %g, %g", tap[0], tap[1], tap[2])
	}
}

func TestBrushPiecesKeepTheirLevelAcrossRates(t *testing.T) {
	// The brushes are mostly noise, so each trims its noise to a constant
	// density. Without that, the same strike is 3 dB quieter at 96 kHz.
	for _, profile := range []Profile{BrushTap, BrushSweep} {
		level := func(rate int) float64 {
			v := newTestVoice(t, profile, rate)
			v.Hit(127)
			rms, _, _, _ := metrics(v, rate/2)
			return 20 * math.Log10(rms)
		}
		reference := level(48_000)
		for _, rate := range []int{44_100, 96_000} {
			got := level(rate)
			t.Logf("METRIC modeledkit profile=%s rate=%d half_second_rms_dbfs=%.3f rms_vs_48k_db=%.3f", Names[profile], rate, got, got-reference)
			if math.Abs(got-reference) > 1.5 {
				t.Errorf("%s at %d Hz is %.2f dB from its 48 kHz level", Names[profile], rate, got-reference)
			}
		}
	}
}

func TestOnlyTheBrushSweepHolds(t *testing.T) {
	for profile := Profile(0); profile < ProfileCount; profile++ {
		v := newTestVoice(t, profile, 48_000)
		v.Hit(127)
		held := v.current.hold != 0 || v.current.swell != 0
		if held != (profile == BrushSweep) {
			t.Errorf("%s: hold %d, swell %g", Names[profile], v.current.hold, v.current.swell)
		}
	}
}

func TestBrushControlsReachBothPieces(t *testing.T) {
	// Tune scales the head resonance of a tap, as it does for a tom.
	tap := newTestVoice(t, BrushTap, 48_000)
	p := DefaultParams()
	p.Tune, p.Humanize = 1.4, 0
	if err := tap.SetParams(p); err != nil {
		t.Fatal(err)
	}
	expected := recipes[BrushTap].frequency * p.Tune
	frequency := renderedPitch(tap, expected)
	t.Logf("METRIC modeledkit profile=brush_tap tune=%.2f rendered_tuning_cents=%.3f", p.Tune, 1200*math.Log2(frequency/expected))
	if math.Abs(1200*math.Log2(frequency/expected)) > 5 {
		t.Errorf("brush tap tuned fundamental: got %.3f Hz, want %.3f Hz", frequency, expected)
	}
	// Decay lengthens a tap's tail.
	short, long := newTestVoice(t, BrushTap, 48_000), newTestVoice(t, BrushTap, 48_000)
	p = DefaultParams()
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
	for i := 0; i < 4800; i++ {
		short.Next()
		long.Next()
	}
	shortRMS, _, _, _ := metrics(short, 4800)
	longRMS, _, _, _ := metrics(long, 4800)
	if longRMS < 3*shortRMS {
		t.Errorf("brush tap decay did not lengthen the tail: short %g long %g", shortRMS, longRMS)
	}
	// Tune and strike position move a sweep's bristle band: higher is brighter.
	brightness := func(profile Profile, edit func(*Params)) float64 {
		v := newTestVoice(t, profile, 48_000)
		p := DefaultParams()
		edit(&p)
		if err := v.SetParams(p); err != nil {
			t.Fatal(err)
		}
		v.Hit(100)
		_, b, _, _ := metrics(v, 24_000)
		return b
	}
	for _, control := range []struct {
		name      string
		low, high func(*Params)
	}{
		{"tune", func(p *Params) { p.Tune = .6 }, func(p *Params) { p.Tune = 1.8 }},
		{"position", func(p *Params) { p.Position = 0 }, func(p *Params) { p.Position = 1 }},
	} {
		low, high := brightness(BrushSweep, control.low), brightness(BrushSweep, control.high)
		t.Logf("METRIC modeledkit profile=brush_sweep control=%s brightness_low=%.5f brightness_high=%.5f", control.name, low, high)
		if high <= low*1.2 {
			t.Errorf("brush sweep %s did not brighten the band: %.5f then %.5f", control.name, low, high)
		}
	}
	// Strike position also brightens a tap through its upper head modes.
	if low, high := brightness(BrushTap, func(p *Params) { p.Position = 0 }), brightness(BrushTap, func(p *Params) { p.Position = 1 }); high <= low {
		t.Errorf("brush tap edge strike was not brighter than the center: %.5f then %.5f", low, high)
	}
}

// BenchmarkStrikeSecond renders the second after each strike, so a short piece
// is charged for its own life and the idle samples after it end.
func BenchmarkStrikeSecond(b *testing.B) {
	for _, profile := range []Profile{CrossStick, HatClosed, BrushTap, BrushSweep} {
		b.Run(Names[profile], func(b *testing.B) {
			v, err := NewVoice(profile, 48_000, 4242)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v.Hit(110)
				for frame := 0; frame < 48_000; frame++ {
					v.Next()
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*48_000), "ns/sample")
		})
	}
}

// windowRMS renders n frames and returns their RMS.
func windowRMS(v *Voice, n int) float64 {
	energy := 0.0
	for i := 0; i < n; i++ {
		x := float64(v.Next())
		energy += x * x
	}
	return math.Sqrt(energy / float64(n))
}

func TestChainedSweepsJoinWithoutADip(t *testing.T) {
	// A score chains sweeps to make one longer than its hold. A sweep that
	// replaces a sweep must take over at the level the old one has reached: its
	// own swell opens at zero over 40 to 62 ms while the old one fades in 1 ms,
	// which left the swish 12 to 17 dB down for the first 10 to 15 ms.
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, velocity := range []uint8{70, 100, 127} {
			// The joins fall inside the hold and just inside its end.
			for _, gap := range []float64{.5, 1.2, 1.55} {
				v := newTestVoice(t, BrushSweep, rate)
				v.Hit(velocity)
				join := int(gap * float64(rate))
				for i := 0; i < join-rate/5; i++ {
					v.Next()
				}
				held := windowRMS(v, rate/5)
				v.Hit(velocity)
				worst, best := 0.0, 0.0
				for w := 0; w < 12; w++ {
					db := 20 * math.Log10(windowRMS(v, rate/200)/held)
					worst, best = math.Min(worst, db), math.Max(best, db)
				}
				t.Logf("METRIC modeledkit profile=brush_sweep rate=%d velocity=%d join_s=%.2f worst_5ms_db=%.2f best_5ms_db=%.2f", rate, velocity, gap, worst, best)
				// The stroke swings the level by 3.4 dB on its own, and the old
				// sweep's last millisecond adds to the new one's first.
				if worst < -5 || best > 8 {
					t.Errorf("%d Hz velocity %d, join at %.2f s: the first 60 ms of the new sweep ran from %.1f to %+.1f dB of the old level", rate, velocity, gap, worst, best)
				}
			}
		}
	}
	// A sweep that starts after the old one has died away still swells in.
	v := newTestVoice(t, BrushSweep, 48_000)
	v.Hit(100)
	for i := 0; i < 5*48_000; i++ {
		v.Next()
	}
	if v.current.active {
		t.Fatal("the first sweep never ended")
	}
	v.Hit(100)
	if v.current.age != 0 {
		t.Errorf("a sweep after silence began %d frames into its swell", v.current.age)
	}
}

func TestBrushSweepLevelFollowsItsStroke(t *testing.T) {
	// A brush circling the head swells and falls about twice a second, 22
	// percent either way. 100 ms windows inside the hold give a range of 36 to
	// 47 percent of the mean; the same noise without the stroke gives 4 to 9.
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, velocity := range []uint8{60, 127} {
			spread := func(stroke bool) float64 {
				v := newTestVoice(t, BrushSweep, rate)
				v.Hit(velocity)
				if !stroke {
					v.current.swell = 0
				}
				windowRMS(v, rate*3/10) // past the swell-in
				lo, hi, sum := math.Inf(1), 0.0, 0.0
				const windows = 12
				for w := 0; w < windows; w++ {
					level := windowRMS(v, rate/10)
					lo, hi, sum = math.Min(lo, level), math.Max(hi, level), sum+level
				}
				return (hi - lo) / (sum / windows)
			}
			with, without := spread(true), spread(false)
			t.Logf("METRIC modeledkit profile=brush_sweep rate=%d velocity=%d window_range_with_stroke=%.3f without=%.3f", rate, velocity, with, without)
			if with < .25 || with < 3*without {
				t.Errorf("%d Hz velocity %d: held sweep windows vary by %.3f of the mean, %.3f without the stroke", rate, velocity, with, without)
			}
		}
	}
	// The level moves at the stroke rate, 1.9 Hz within the 8 percent spread of
	// the four strike variations. Take the strongest of the frequencies from 0.5
	// to 5 Hz in 20 ms windows over the 3.2 s hold of Decay 2.
	const rate = 48_000
	v := newTestVoice(t, BrushSweep, rate)
	p := DefaultParams()
	p.Decay = 2
	if err := v.SetParams(p); err != nil {
		t.Fatal(err)
	}
	v.Hit(100)
	windowRMS(v, rate*3/10)
	var series []float64
	for w := 0; w < 140; w++ {
		series = append(series, windowRMS(v, rate/50))
	}
	mean := 0.0
	for _, x := range series {
		mean += x / float64(len(series))
	}
	strongest, at := 0.0, 0.0
	for f := .5; f <= 5; f += .05 {
		var re, im float64
		for i, x := range series {
			phase := 2 * math.Pi * f * float64(i) * .02
			re += (x - mean) * math.Cos(phase)
			im += (x - mean) * math.Sin(phase)
		}
		if power := re*re + im*im; power > strongest {
			strongest, at = power, f
		}
	}
	t.Logf("METRIC modeledkit profile=brush_sweep stroke_hz=%.2f", at)
	if at < 1.6 || at > 2.3 {
		t.Errorf("the held sweep's level moves at %.2f Hz, want about 1.9", at)
	}
}

func TestBrushTapWireResponseAddsToTheHeadNoise(t *testing.T) {
	// The tap drives its snare wires from the head's displacement, so the wire
	// term adds energy that grows with velocity. Without it, 0.2 s of a tap
	// holds 0.3 to 5 percent less energy.
	energy := func(rate int, velocity uint8, wire bool) float64 {
		v := newTestVoice(t, BrushTap, rate)
		v.Hit(velocity)
		if !wire {
			v.current.bodyCoupling = 0
		}
		rms := windowRMS(v, rate/5)
		return rms * rms
	}
	for _, rate := range []int{44_100, 48_000, 96_000} {
		soft := energy(rate, 40, true) / energy(rate, 40, false)
		loud := energy(rate, 127, true) / energy(rate, 127, false)
		t.Logf("METRIC modeledkit profile=brush_tap rate=%d wire_energy_ratio_velocity40=%.4f velocity127=%.4f", rate, soft, loud)
		if loud < 1.03 || loud < soft+.02 {
			t.Errorf("%d Hz: the wire term adds %.1f%% of energy at velocity 127 and %.1f%% at 40", rate, 100*(loud-1), 100*(soft-1))
		}
	}
}
