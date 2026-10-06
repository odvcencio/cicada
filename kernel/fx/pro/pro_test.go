package pro

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/fx"
)

type stereoProcessor interface {
	Process(float32, float32) (float32, float32)
	Reset()
}

func mustChain(t *testing.T, p Params) *Chain {
	t.Helper()
	c, err := New(48000, p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sineGain(process stereoProcessor, rate int, frequency float64) float64 {
	inPower, outPower := 0.0, 0.0
	for i := 0; i < rate; i++ {
		x := float32(.1 * math.Sin(2*math.Pi*frequency*float64(i)/float64(rate)))
		y, _ := process.Process(x, x)
		if i >= rate/2 {
			inPower += float64(x) * float64(x)
			outPower += float64(y) * float64(y)
		}
	}
	return 10 * math.Log10(outPower/inPower)
}

func TestEQDesignFrequencyResponse(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for _, frequency := range []float64{40, 1000, 8000, .4 * float64(rate)} {
			for _, gain := range []float64{-18, -6, 6, 18} {
				eq, err := NewEQ(rate, [4]Band{{Enabled: true, Type: Peak, FrequencyHz: frequency, Q: 1.3, GainDB: gain}})
				if err != nil {
					t.Fatal(err)
				}
				measured := sineGain(eq, rate, frequency)
				if math.Abs(measured-gain) > .02 {
					t.Fatalf("rate=%d frequency=%g requested=%g measured=%g dB", rate, frequency, gain, measured)
				}
			}
		}
	}
	for _, filter := range []EQType{LowShelf, HighShelf, Highpass, Lowpass} {
		band := Band{Enabled: true, Type: filter, FrequencyHz: 1000, Q: 1 / math.Sqrt2, GainDB: 12}
		eq, err := NewEQ(48000, [4]Band{band})
		if err != nil {
			t.Fatal(err)
		}
		expected := 6.0
		if filter >= Highpass {
			expected = -3.01029995664
		}
		if measured := sineGain(eq, 48000, 1000); math.Abs(measured-expected) > .02 {
			t.Fatalf("filter=%d expected=%g measured=%g dB", filter, expected, measured)
		}
	}
}

func TestEQExtremeControlsDecayAndReset(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for _, filter := range []EQType{Peak, LowShelf, HighShelf, Highpass, Lowpass} {
			for _, f := range []float64{10, .45 * float64(rate)} {
				for _, q := range []float64{.1, 20} {
					eq, err := NewEQ(rate, [4]Band{{Enabled: true, Type: filter, FrequencyHz: f, Q: q, GainDB: 24}})
					if err != nil {
						t.Fatal(err)
					}
					first, _ := eq.Process(1, 0)
					for i := 0; i < rate*2; i++ {
						l, r := eq.Process(0, 0)
						if !finite(float64(l)) || r != 0 {
							t.Fatalf("unstable or leaked channels: %g, %g", l, r)
						}
					}
					eq.Reset()
					if got, _ := eq.Process(1, 0); got != first {
						t.Fatal("reset failed to reproduce impulse response")
					}
				}
			}
		}
	}
}

func TestChainTransparentBypassAndFaultReset(t *testing.T) {
	for _, p := range []Params{{}, DefaultParams()} {
		c := mustChain(t, p)
		if c.LatencyFrames() != 0 {
			t.Fatal("bypass introduced latency")
		}
		for i := 0; i < 2000; i++ {
			l, r := float32(math.Sin(float64(i))), float32(math.Cos(float64(i)))
			a, b := c.Process(l, r)
			if a != l || b != r {
				t.Fatalf("bypass changed input: %g %g -> %g %g", l, r, a, b)
			}
		}
		l, r := c.Process(float32(math.NaN()), 0)
		if l != 0 || r != 0 || !c.Fault() {
			t.Fatal("nonfinite input did not latch a silent fault")
		}
		c.Reset()
		l, r = c.Process(.2, -.3)
		if l != .2 || r != -.3 || c.Fault() {
			t.Fatal("reset failed to clear fault")
		}
	}
}

func TestLinkedCompressorStaticCurve(t *testing.T) {
	p := DefaultParams()
	p.EnableCompressor = true
	p.Compressor = fx.CompParams{Threshold: -18, Ratio: 4, AttackMs: .1, ReleaseMs: 100, Mix: 1}
	c := mustChain(t, p)
	var l, r float32
	for i := 0; i < 48000; i++ {
		l, r = c.Process(.5, .05)
	}
	expected := -18 + (20*math.Log10(.5)+18)/4
	got := 20 * math.Log10(float64(l))
	if math.Abs(got-expected) > .02 || math.Abs(float64(l/r)-10) > 1e-5 {
		t.Fatalf("compressor static gain=%g expected=%g stereo ratio=%g", got, expected, l/r)
	}
	if c.LatencyFrames() != 0 {
		t.Fatal("compressor introduced latency")
	}
}

func TestTransientAttackSustainAndStereoLink(t *testing.T) {
	p := DefaultTransientParams()
	p.AttackDB, p.SustainDB = 6, -6
	s, err := NewTransient(48000, p)
	if err != nil {
		t.Fatal(err)
	}
	attack, tail := 0.0, 0.0
	for i := 0; i < 48000; i++ {
		l, r := s.Process(.2, .02)
		gain := float64(l) / .2
		if i < 1000 {
			attack = math.Max(attack, gain)
		}
		if i == 47999 {
			tail = gain
		}
		if math.Abs(float64(l/r)-10) > 2e-6 {
			t.Fatal("transient gain moved the stereo image")
		}
	}
	if attack < 1.7 || math.Abs(tail-dbGain(-6)) > .002 {
		t.Fatalf("attack gain=%g tail gain=%g", attack, tail)
	}
	t.Logf("transient attack %.3f dB; sustained %.3f dB", 20*math.Log10(attack), 20*math.Log10(tail))
	s.Reset()
	for i := 0; i <= s.LatencyFrames(); i++ {
		x := float32(0)
		if i == 0 {
			x = .2
		}
		l, r := s.Process(x, x*.1)
		if i < s.LatencyFrames() && (l != 0 || r != 0) {
			t.Fatal("transient impulse escaped lookahead")
		}
		if i == s.LatencyFrames() && l < .32 {
			t.Fatalf("transient did not boost leading impact: %g", l)
		}
	}
}

func TestWidthMidAndBassResponse(t *testing.T) {
	for _, amount := range []float64{0, .5, 1, 2} {
		w, err := NewWidth(48000, WidthParams{Amount: amount})
		if err != nil {
			t.Fatal(err)
		}
		l, r := w.Process(.3, -.1)
		if math.Abs(float64(l+r)-.2) > 3e-8 || math.Abs(float64(l-r)-.4*amount) > 4e-8 {
			t.Fatalf("mid/side incorrect at width=%g: %g %g", amount, l, r)
		}
		for i := 0; i < 100; i++ {
			l, r = w.Process(.2, .2)
			if l != .2 || r != .2 {
				t.Fatal("mono changed by width")
			}
		}
	}
	w, err := NewWidth(48000, WidthParams{Amount: 1, BassMonoHz: 120})
	if err != nil {
		t.Fatal(err)
	}
	inPower, outPower := 0.0, 0.0
	for i := 0; i < 48000; i++ {
		x := float32(.2 * math.Sin(2*math.Pi*30*float64(i)/48000))
		l, r := w.Process(x, -x)
		if i > 24000 {
			inPower += float64(x) * float64(x)
			outPower += float64(l) * float64(l)
		}
		if l != -r {
			t.Fatal("side filtering leaked into mid")
		}
	}
	if db := 10 * math.Log10(outPower/inPower); db > -23 {
		t.Fatalf("bass side rejection only %g dB", db)
	}
}

func TestReverbEarlyFieldAndStableTail(t *testing.T) {
	p := DefaultReverbParams()
	p.Mix, p.EarlyMix, p.Width = 1, 1, 1
	r, err := NewReverb(48000, p)
	if err != nil {
		t.Fatal(err)
	}
	leftFirst, rightFirst, leftCount, rightCount := -1, -1, 0, 0
	for i := 0; i < 4800; i++ {
		input := float32(0)
		if i == 0 {
			input = 1
		}
		l, rr := r.Process(input, input)
		if l != 0 {
			leftCount++
			if leftFirst == -1 {
				leftFirst = i
			}
		}
		if rr != 0 {
			rightCount++
			if rightFirst == -1 {
				rightFirst = i
			}
		}
	}
	if leftFirst != 206 || rightFirst != 283 || leftCount != 6 || rightCount != 6 {
		t.Fatalf("early field timings/counts: %d %d %d %d", leftFirst, rightFirst, leftCount, rightCount)
	}
	p = DefaultReverbParams()
	p.Mix, p.Tail.DecaySec = 1, 1
	r, err = NewReverb(48000, p)
	if err != nil {
		t.Fatal(err)
	}
	var energies [8]float64
	var tailDC float64
	for i := 0; i < 48000*8; i++ {
		input := float32(0)
		if i == 0 {
			input = 1
		}
		l, rr := r.Process(input, 0)
		if !finite(float64(l)) || !finite(float64(rr)) || r.Fault() {
			t.Fatal("reverb tail became unstable")
		}
		energies[i/48000] += float64(l)*float64(l) + float64(rr)*float64(rr)
		if i >= 48000*7 {
			tailDC += float64(l) + float64(rr)
		}
	}
	for i := 1; i < len(energies); i++ {
		if energies[i] > energies[i-1] {
			t.Fatalf("reverb energy failed to decay: %v", energies)
		}
	}
	tailDB := 10 * math.Log10(energies[7]/96000)
	if tailDB > -120 || math.Abs(tailDC/96000) > 1e-8 {
		t.Fatalf("tail RMS=%g dBFS DC=%g", tailDB, tailDC/96000)
	}
	t.Logf("reverb final-second RMS %.2f dBFS; DC %.3g", tailDB, tailDC/96000)
}

func TestRenderAllocationsSilenceAndReset(t *testing.T) {
	p := DefaultParams()
	p.EQ[0] = Band{Enabled: true, Type: Peak, FrequencyHz: 1200, Q: 1, GainDB: 3}
	p.EnableCompressor, p.Transient.Enabled, p.Width.Enabled, p.Reverb.Enabled, p.EnableLimiter = true, true, true, true, true
	p.Transient.AttackDB, p.Transient.SustainDB = 3, -2
	p.Width.Amount, p.Width.BassMonoHz = 1.2, 100
	c := mustChain(t, p)
	for i := 0; i < 10000; i++ {
		l, r := c.Process(0, 0)
		if l != 0 || r != 0 {
			t.Fatal("reset-silence noise floor is nonzero")
		}
	}
	if allocs := testing.AllocsPerRun(10, func() {
		c.Reset()
		for i := 0; i < 128; i++ {
			c.Process(.1, -.2)
		}
	}); allocs != 0 {
		t.Fatalf("render/reset allocated %g times", allocs)
	}
	c.Reset()
	var before [1024][2]float32
	for i := range before {
		l, r := c.Process(float32(math.Sin(float64(i))), float32(math.Cos(float64(i))))
		before[i] = [2]float32{l, r}
	}
	c.Reset()
	for i := range before {
		l, r := c.Process(float32(math.Sin(float64(i))), float32(math.Cos(float64(i))))
		if before[i] != [2]float32{l, r} {
			t.Fatal("reset failed to reproduce the mix chain")
		}
	}
}

func TestParameterValidation(t *testing.T) {
	if _, err := New(32000, Params{}); err == nil {
		t.Fatal("accepted unsupported rate")
	}
	for _, p := range []Params{
		{EQ: [4]Band{{Enabled: true, FrequencyHz: 1000, Q: 0}}},
		{EQ: [4]Band{{Enabled: true, FrequencyHz: 24000, Q: 1}}},
		{Transient: TransientParams{Enabled: true}},
		{Width: WidthParams{Enabled: true, Amount: math.NaN()}},
		{Width: WidthParams{Enabled: true, Amount: 1, BassMonoHz: 1}},
		{Reverb: ReverbParams{Enabled: true}},
		{EnableCompressor: true},
		{EnableLimiter: true},
	} {
		if _, err := New(48000, p); err == nil {
			t.Fatalf("accepted invalid controls: %+v", p)
		}
	}
}
