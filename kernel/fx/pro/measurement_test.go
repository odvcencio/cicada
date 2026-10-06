package pro

import (
	"math"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/fx"
)

func harmonicAmplitude(samples []float32, harmonic int) float64 {
	real, imaginary := 0.0, 0.0
	for i, sample := range samples {
		phase := 2 * math.Pi * 1000 * float64(harmonic*i) / 48000
		real += float64(sample) * math.Cos(phase)
		imaginary -= float64(sample) * math.Sin(phase)
	}
	return 2 * math.Hypot(real, imaginary) / float64(len(samples))
}

func measureTHD(processor stereoProcessor, inputAmplitude float64) float64 {
	const settle, n = 48000, 4800
	var samples [n]float32
	for i := 0; i < settle+n; i++ {
		x := float32(inputAmplitude * math.Sin(2*math.Pi*1000*float64(i)/48000))
		y, _ := processor.Process(x, x)
		if i >= settle {
			samples[i-settle] = y
		}
	}
	fundamental := harmonicAmplitude(samples[:], 1)
	power := 0.0
	for harmonic := 2; harmonic <= 10; harmonic++ {
		amplitude := harmonicAmplitude(samples[:], harmonic)
		power += amplitude * amplitude
	}
	return math.Sqrt(power) / fundamental
}

func TestMeasuredTHD(t *testing.T) {
	eq, err := NewEQ(48000, [4]Band{{Enabled: true, Type: Peak, FrequencyHz: 1000, Q: 1, GainDB: 6}})
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWidth(48000, WidthParams{Amount: 1.5, BassMonoHz: 100})
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultTransientParams()
	p.AttackDB, p.SustainDB = 4, -2
	transient, err := NewTransient(48000, p)
	if err != nil {
		t.Fatal(err)
	}
	compressor, err := fx.NewCompressor(48000)
	if err != nil {
		t.Fatal(err)
	}
	cp := fx.DefaultCompParams()
	cp.MakeupAuto, cp.AttackMs, cp.ReleaseMs = false, 20, 200
	if err := compressor.SetParams(cp); err != nil {
		t.Fatal(err)
	}
	compressor.Reset()
	limiter, err := NewLimiter(48000, DefaultLimiterParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		p     stereoProcessor
		level float64
		limit float64
	}{
		{"eq", eq, .1, 1e-6},
		{"width", w, .1, 1e-6},
		{"compressor", compressor, .5, .001},
		{"transient", transient, .1, .001},
		{"limiter", limiter, 1.5, .001},
	} {
		thd := measureTHD(test.p, test.level)
		if thd > test.limit {
			t.Fatalf("%s THD=%g limit=%g", test.name, thd, test.limit)
		}
		t.Logf("%s steady 1 kHz THD %.6f%% (%.2f dB)", test.name, 100*thd, 20*math.Log10(thd))
	}
}

func TestPresetNames(t *testing.T) {
	for _, name := range PresetNames {
		p, ok := Preset(name)
		if !ok {
			t.Fatalf("preset %q missing", name)
		}
		if _, err := New(48000, p); err != nil {
			t.Fatalf("preset %q invalid: %v", name, err)
		}
	}
	if _, ok := Preset("unknown"); ok {
		t.Fatal("accepted unknown preset")
	}
}

func TestDynamicHighRateResidualMeasurements(t *testing.T) {
	// Compare 48 kHz processing with a latency-aligned 96 kHz reference,
	// downsampled through a 129-tap lowpass. The residual includes detector
	// discretization and aliasing; it is not a complete anti-alias certificate.
	// Fit a single gain so transparent static gain differences do not dominate.
	const settle, n, half = 48000, 4800, 64
	var filter [2*half + 1]float64
	sum := 0.0
	for i := range filter {
		k := float64(i - half)
		h := .48
		if k != 0 {
			h = math.Sin(math.Pi*.48*k) / (math.Pi * k)
		}
		position := float64(i) / float64(len(filter)-1)
		h *= .42 - .5*math.Cos(2*math.Pi*position) + .08*math.Cos(4*math.Pi*position)
		filter[i] = h
		sum += h
	}
	for i := range filter {
		filter[i] /= sum
	}
	for _, test := range []struct {
		name  string
		level float64
	}{{"compressor", .5}, {"transient", .1}, {"limiter", 1.5}} {
		params, _ := Preset(test.name)
		base, err := New(48000, params)
		if err != nil {
			t.Fatal(err)
		}
		high, err := New(96000, params)
		if err != nil {
			t.Fatal(err)
		}
		baseAudio := make([]float32, settle+n+base.LatencyFrames()+half)
		highAudio := make([]float32, (settle+n)*2+high.LatencyFrames()+half)
		for i := range baseAudio {
			x := float32(test.level * math.Sin(2*math.Pi*18000*float64(i)/48000))
			baseAudio[i], _ = base.Process(x, x)
		}
		for i := range highAudio {
			x := float32(test.level * math.Sin(2*math.Pi*18000*float64(i)/96000))
			highAudio[i], _ = high.Process(x, x)
		}
		var reference, residual [n]float32
		cross, referenceEnergy, baseEnergy := 0.0, 0.0, 0.0
		for i := range reference {
			center := (settle+i)*2 + high.LatencyFrames()
			y := 0.0
			for tap, coefficient := range filter {
				y += coefficient * float64(highAudio[center+tap-half])
			}
			reference[i] = float32(y)
			x := float64(baseAudio[settle+i+base.LatencyFrames()])
			cross += x * y
			referenceEnergy += y * y
			baseEnergy += x * x
		}
		gain, residualEnergy := cross/referenceEnergy, 0.0
		for i := range residual {
			error := float64(baseAudio[settle+i+base.LatencyFrames()]) - gain*float64(reference[i])
			residual[i] = float32(error)
			residualEnergy += error * error
		}
		residualDB := 10 * math.Log10(residualEnergy/baseEnergy)
		// The 18 kHz tone's third harmonic folds to 6 kHz at the base rate.
		folded := harmonicAmplitude(residual[:], 6)
		foldedDB := 20 * math.Log10(folded/math.Sqrt(2*baseEnergy/n))
		if !finite(residualDB) || !finite(foldedDB) {
			t.Fatalf("%s nonfinite residual measurement", test.name)
		}
		t.Logf("%s 18 kHz residual vs96k reference %.2f dB; folded6k %.2f dB; fitted gain %.5f", test.name, residualDB, foldedDB, gain)
	}
}

func BenchmarkProcessors(b *testing.B) {
	for _, name := range PresetNames {
		b.Run(name, func(b *testing.B) {
			p, _ := Preset(name)
			c, err := New(48000, p)
			if err != nil {
				b.Fatal(err)
			}
			var block [128][2]float32
			for i := range block {
				x := float32(.5 * math.Sin(2*math.Pi*1000*float64(i)/48000))
				block[i] = [2]float32{x, x * .7}
			}
			b.ReportAllocs()
			b.ResetTimer()
			start := time.Now()
			for n := 0; n < b.N; n++ {
				for _, pair := range block {
					c.Process(pair[0], pair[1])
				}
			}
			b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N*128), "ns/frame")
		})
	}
}
