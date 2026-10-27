package ampcab

import (
	"fmt"
	"m31labs.dev/cicada/kernel/dsp/halfband"
	"math"
	"testing"
)

func TestBypassLatencyStereoAndReset(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		v := DefaultParams()
		v.Bypass = true
		p, err := New(rate, v)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < Latency+8; i++ {
			x := float32(0)
			if i == 0 {
				x = .5
			}
			l, r := p.Process(x, 0)
			if r != 0 {
				t.Fatal("channels coupled")
			}
			if (i == Latency && l != .5) || (i != Latency && l != 0) {
				t.Fatalf("rate %d frame %d bypass %g", rate, i, l)
			}
		}
		p.Reset()
		l, r := p.Process(0, 0)
		if l != 0 || r != 0 {
			t.Fatal("reset left history")
		}
	}
}
func TestFiniteTailAndNoAllocations(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		p, err := New(rate, DefaultParams())
		if err != nil {
			t.Fatal(err)
		}
		if n := testing.AllocsPerRun(10, func() {
			for i := 0; i < 128; i++ {
				p.Process(.1, -.2)
			}
		}); n != 0 {
			t.Fatalf("allocations %g", n)
		}
		for i := 0; i < rate; i++ {
			l, r := p.Process(0, 0)
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) || math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				t.Fatal("non-finite output")
			}
			if i == rate-1 && (math.Abs(float64(l)) > 1e-8 || math.Abs(float64(r)) > 1e-8) {
				t.Fatalf("tail not drained %g %g", l, r)
			}
		}
	}
}
func TestFourTimesOversamplingReducesAlias(t *testing.T) {
	p, err := New(48000, Params{Drive: 1, Tone: 1, Cabinet: 2})
	if err != nil {
		t.Fatal(err)
	}
	// 7 kHz fifth harmonic aliases to 13 kHz at 48 kHz.
	const n = 48000
	var osRe, osIm, rawRe, rawIm float64
	for i := 0; i < n+4096; i++ {
		x := .6 * math.Sin(2*math.Pi*7000*float64(i)/48000)
		y, _ := p.Process(float32(x), 0)
		raw := math.Tanh(13*x) / math.Tanh(13)
		if i < 4096 {
			continue
		}
		a := 2 * math.Pi * 13000 * float64(i) / 48000
		osRe += float64(y) * math.Cos(a)
		osIm += float64(y) * math.Sin(a)
		rawRe += raw * math.Cos(a)
		rawIm += raw * math.Sin(a)
	}
	ratio := math.Hypot(osRe, osIm) / math.Hypot(rawRe, rawIm)
	if ratio > .2 {
		t.Fatalf("13 kHz alias ratio %g", ratio)
	}
	t.Logf("13 kHz alias attenuation vs unfiltered nonlinear reference %.1f dB", 20*math.Log10(ratio))
}

func TestOversamplingLatency(t *testing.T) {
	u1, u2, d2, d1 := halfband.New(), halfband.New(), halfband.New(), halfband.New()
	var sum, moment, phase float64
	for i := 0; i < 256; i++ {
		x := 0.0
		if i == 0 {
			x = 1
		}
		a, b := u1.Upsample(x)
		a0, a1 := u2.Upsample(a)
		b0, b1 := u2.Upsample(b)
		a0, a1, b0, b1, phase = phase, a0, a1, b0, b1
		y := d1.Downsample(d2.Downsample(a0, a1), d2.Downsample(b0, b1))
		sum += y
		moment += float64(i) * y
	}
	delay := moment / sum
	t.Logf("4x FIR group delay %.3f frames, reported %d", delay, Latency)
	if math.Abs(delay-Latency) > .00001 {
		t.Fatalf("latency misreported: %g", delay)
	}
}

func TestControlChangesAreSmoothed(t *testing.T) {
	p, err := New(48000, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	reference, err := New(48000, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	frame := 0
	advance := func() (float32, float32) {
		x := float32(.12 * math.Sin(2*math.Pi*440*float64(frame)/48000))
		frame++
		a, _ := p.Process(x, x)
		b, _ := reference.Process(x, x)
		return a, b
	}
	for i := 0; i < 6000; i++ {
		advance()
	}
	changes := []Params{{Drive: 1, Tone: 1, Output: 12, Cabinet: 2}, {Drive: 0, Tone: 0, Output: -24, Cabinet: 1}, {Drive: .5, Tone: .5, Output: 0, Cabinet: 0, Bypass: true}}
	for _, v := range changes {
		if err := p.SetParams(v); err != nil {
			t.Fatal(err)
		}
		a, b := advance()
		if math.Abs(float64(a-b)) > .002 {
			t.Fatalf("immediate control discontinuity %.6f", a-b)
		}
		if err := reference.SetParams(v); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 6000; i++ {
			advance()
		}
	}
}
func TestOversampledNonlinearityAlias(t *testing.T) {
	u1, u2, d2, d1 := halfband.New(), halfband.New(), halfband.New(), halfband.New()
	var ar, ai, br, bi float64
	for i := 0; i < 52096; i++ {
		x := .6 * math.Sin(2*math.Pi*7000*float64(i)/48000)
		a, b := u1.Upsample(x)
		a0, a1 := u2.Upsample(a)
		b0, b1 := u2.Upsample(b)
		shape := func(v float64) float64 { return math.Tanh(13*v) / math.Tanh(13) }
		y := d1.Downsample(d2.Downsample(shape(a0), shape(a1)), d2.Downsample(shape(b0), shape(b1)))
		raw := shape(x)
		if i < 4096 {
			continue
		}
		phase := 2 * math.Pi * 13000 * float64(i) / 48000
		ar += y * math.Cos(phase)
		ai += y * math.Sin(phase)
		br += raw * math.Cos(phase)
		bi += raw * math.Sin(phase)
	}
	db := 20 * math.Log10(math.Hypot(ar, ai)/math.Hypot(br, bi))
	t.Logf("4x nonlinear stage alias at 13 kHz vs 1x: %.1f dB", db)
	if db > -20 {
		t.Fatalf("insufficient alias suppression %.1f dB", db)
	}
}

func BenchmarkAmpCab128Stereo(b *testing.B) {
	p, err := New(48000, DefaultParams())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 128; j++ {
			p.Process(.1, -.1)
		}
	}
}

func TestRateConsistentTone(t *testing.T) {
	var reference float64
	for _, rate := range []int{44100, 48000, 96000} {
		p, err := New(rate, DefaultParams())
		if err != nil {
			t.Fatal(err)
		}
		var power float64
		for i := 0; i < rate/2; i++ {
			x := float32(.05 * math.Sin(2*math.Pi*1000*float64(i)/float64(rate)))
			y, _ := p.Process(x, x)
			if i >= rate/4 {
				power += float64(y) * float64(y)
			}
		}
		rms := math.Sqrt(power / float64(rate/4))
		if reference == 0 {
			reference = rms
		}
		delta := 20 * math.Log10(rms/reference)
		t.Logf("rate %d 1kHz output rms %.6f, relative %.2f dB", rate, rms, delta)
		if math.Abs(delta) > 2 {
			t.Fatalf("rate response differs %.2f dB", delta)
		}
	}
}

func BenchmarkAmpCabSession(b *testing.B) {
	for _, voices := range []int{1, 16} {
		for _, frames := range []int{64, 128} {
			b.Run(fmt.Sprintf("stereo%d_frames%d", voices, frames), func(b *testing.B) {
				ps := make([]*Processor, voices)
				for i := range ps {
					var err error
					ps[i], err = New(48000, DefaultParams())
					if err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					for j := 0; j < frames; j++ {
						for _, p := range ps {
							p.Process(.1, -.1)
						}
					}
				}
			})
		}
	}
}

func TestDCInputIsRejected(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		p, err := New(rate, DefaultParams())
		if err != nil {
			t.Fatal(err)
		}
		var y float32
		for i := 0; i < rate; i++ {
			y, _ = p.Process(.2, .2)
		}
		if math.Abs(float64(y)) > 1e-5 {
			t.Fatalf("rate %d DC residual %g", rate, y)
		}
	}
}

func TestImmediateControlsPreserveHistory(t *testing.T) {
	a, err := New(48000, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New(48000, DefaultParams())
	for i := 0; i < 128; i++ {
		x := float32(math.Sin(float64(i)*.17) * .2)
		a.Process(x, x)
		b.Process(x, x)
	}
	if err := a.SetParamsImmediate(a.Params()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		al, ar := a.Process(0, 0)
		bl, br := b.Process(0, 0)
		if al != bl || ar != br {
			t.Fatalf("history changed at %d", i)
		}
	}
}
