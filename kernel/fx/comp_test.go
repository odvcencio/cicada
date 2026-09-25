package fx

import (
	"math"
	"testing"
)

func TestCompressorBoundsAndGainComputer(t *testing.T) {
	if _, err := NewCompressor(22_050); err == nil {
		t.Fatal("accepted unsupported sample rate")
	}
	c, err := NewCompressor(48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CompParams){
		func(p *CompParams) { p.Detect = 2 },
		func(p *CompParams) { p.Threshold = -41 },
		func(p *CompParams) { p.Ratio = 21 },
		func(p *CompParams) { p.Knee = 13 },
		func(p *CompParams) { p.AttackMs = .09 },
		func(p *CompParams) { p.ReleaseMs = 9 },
		func(p *CompParams) { p.Mix = math.NaN() },
	} {
		p := DefaultCompParams()
		mutate(&p)
		if err := c.SetParams(p); err == nil {
			t.Fatalf("accepted invalid compressor params %+v", p)
		}
	}
	p := DefaultCompParams()
	p.Threshold, p.Ratio, p.Knee = -12, 4, 0
	p.AttackMs, p.ReleaseMs, p.MakeupAuto, p.MakeupDB = .1, 10, false, 0
	if err := c.SetParams(p); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	input := float32(math.Pow(10, -6.0/20)) // 6 dB above threshold
	var output float32
	for frame := 0; frame < 4_800; frame++ {
		output, _ = c.Process(input, input)
	}
	reduction := 20 * math.Log10(float64(input/output))
	if math.Abs(reduction-4.5) > .2 {
		t.Fatalf("steady compressor reduction %.3f dB, want 4.5 ±0.2 dB", reduction)
	}
	if c.reductionDB(math.Pow(10, -15.0/20)) != 0 {
		t.Fatal("hard knee reduced audio below threshold")
	}
	p.Knee = 12
	c.SetParams(p)
	if got := c.reductionDB(math.Pow(10, -12.0/20)); math.Abs(got-1.125) > 1e-9 {
		t.Fatalf("soft knee midpoint reduction %g, want 1.125 dB", got)
	}
}

func TestCompressorAttackReleaseAndSidechain(t *testing.T) {
	c, _ := NewCompressor(48_000)
	p := DefaultCompParams()
	p.Threshold, p.Ratio, p.Knee = -12, 4, 0
	p.AttackMs, p.ReleaseMs, p.MakeupAuto = 10, 100, false
	c.SetParams(p)
	c.Reset()
	input := float32(math.Pow(10, -6.0/20))
	for frame := 0; frame < 480; frame++ {
		c.Process(input, input)
	}
	if c.reduction < 2.5 || c.reduction > 3.1 {
		t.Fatalf("10 ms attack reached %.3f dB, want ~63%% of 4.5 dB", c.reduction)
	}
	for frame := 0; frame < 4_800; frame++ {
		c.Process(input, input)
	}
	for frame := 0; frame < 4_800; frame++ {
		c.Process(0, 0)
	}
	if c.reduction < 1.5 || c.reduction > 1.9 {
		t.Fatalf("100 ms release retained %.3f dB, want ~37%% of 4.5 dB", c.reduction)
	}
	c.Reset()
	var output float32
	for frame := 0; frame < 4_800; frame++ {
		output, _ = c.ProcessSidechain(input, input, 0, 0)
	}
	if math.Abs(float64(output-input)) > 1e-6 {
		t.Fatal("silent sidechain reduced audio")
	}
	for frame := 0; frame < 4_800; frame++ {
		side := input
		if frame/120%2 != 0 {
			side = -input
		}
		output, _ = c.ProcessSidechain(input, input, side, -side)
	}
	if output >= input*.9 {
		t.Fatalf("active sidechain did not duck audio: %g versus %g", output, input)
	}
	c.Reset()
	for frame := 0; frame < 48_000; frame++ {
		output, _ = c.ProcessSidechain(input, input, input, input)
	}
	if math.Abs(float64(output-input)) > 1e-3 {
		t.Fatalf("80 Hz sidechain highpass retained DC: %g versus %g", output, input)
	}
}

func TestCompressorRMSAndAllocationFree(t *testing.T) {
	c, _ := NewCompressor(48_000)
	p := DefaultCompParams()
	p.Detect, p.Threshold, p.Knee, p.MakeupAuto = RMSDetector, -18, 0, false
	if err := c.SetParams(p); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	if count := testing.AllocsPerRun(1_000, func() { c.Process(.4, -.4) }); count != 0 {
		t.Fatalf("compressor process allocated %g times", count)
	}
	if count := testing.AllocsPerRun(1_000, func() { c.SetParams(p) }); count != 0 {
		t.Fatalf("compressor parameter update allocated %g times", count)
	}
	for frame := 0; frame < 50_000; frame++ {
		left, right := c.Process(1, -1)
		if !finite(float64(left)) || !finite(float64(right)) {
			t.Fatalf("nonfinite compressor at frame %d", frame)
		}
	}
	if math.Abs(c.rmsSum/float64(len(c.rms))-1) > 1e-9 {
		t.Fatalf("RMS detector did not settle at unit energy: %g", c.rmsSum/float64(len(c.rms)))
	}
	c.Process(float32(math.NaN()), 0)
	if !c.Fault() {
		t.Fatal("nonfinite input did not fault")
	}
	c.Reset()
	if c.Fault() {
		t.Fatal("reset retained fault")
	}
}

func BenchmarkCompressorStereoSample(b *testing.B) {
	c, _ := NewCompressor(48_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Process(.4, -.4)
	}
}

func BenchmarkCompressorStereoSampleDynamic(b *testing.B) {
	c, _ := NewCompressor(48_000)
	var wave [1024]float32
	for i := range wave {
		wave[i] = float32(.4 * math.Sin(2*math.Pi*float64(i)/1024))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		input := wave[i&1023]
		c.Process(input, -input)
	}
}
