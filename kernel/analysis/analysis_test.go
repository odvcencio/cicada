package analysis

import (
	"math"
	"math/rand"
	"testing"
)

func sine(freq, amp float64, rate, frames int) []float32 {
	out := make([]float32, frames)
	for i := range out {
		out[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return out
}

func bandOf(rate int, hz float64) int {
	for i := 0; i < Bands; i++ {
		if low, high := BandHz(rate, i); hz >= low && hz < high {
			return i
		}
	}
	return -1
}

func analyze(t *testing.T, rate int, left, right []float32) [Bands]float32 {
	t.Helper()
	s, err := NewSpectrum(rate)
	if err != nil {
		t.Fatal(err)
	}
	s.Process(left, right)
	var out [Bands]float32
	s.Bands(&out)
	return out
}

func TestBandHzEdges(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		low, _ := BandHz(rate, 0)
		_, high := BandHz(rate, Bands-1)
		want := math.Min(20000, 0.45*float64(rate))
		if math.Abs(low-20) > 1e-9 || math.Abs(high-want) > 1e-6 {
			t.Fatalf("rate %d: edges %.3f..%.3f Hz, want 20..%.3f", rate, low, high, want)
		}
	}
}

func TestSineReadsItsLevelInItsBand(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		x := sine(1000, math.Pow(10, -12.0/20), rate, 20000)
		bands := analyze(t, rate, x, x)
		k := bandOf(rate, 1000)
		if got := bands[k]; math.Abs(float64(got)+12) > 0.5 {
			t.Fatalf("rate %d: band %d reads %.2f dB, want -12 +-0.5", rate, k, got)
		}
		for i, v := range bands {
			if i >= k-2 && i <= k+2 {
				continue
			}
			if v >= -70 {
				t.Fatalf("rate %d: band %d reads %.1f dB, want below -70", rate, i, v)
			}
		}
	}
}

func TestFullScaleSineReadsZeroDB(t *testing.T) {
	x := sine(4000, 1, 48000, 8192)
	bands := analyze(t, 48000, x, x)
	if got := bands[bandOf(48000, 4000)]; math.Abs(float64(got)) > 0.5 {
		t.Fatalf("full-scale sine reads %.2f dB", got)
	}
}

func TestTwoTonesResolveAsTwoPeaks(t *testing.T) {
	a, b := sine(500, 0.3, 48000, 8192), sine(4000, 0.3, 48000, 8192)
	mix := make([]float32, len(a))
	for i := range mix {
		mix[i] = a[i] + b[i]
	}
	bands := analyze(t, 48000, mix, mix)
	var peaks []int
	for i := 1; i < Bands-1; i++ {
		if bands[i] > -40 && bands[i] >= bands[i-1] && bands[i] > bands[i+1] {
			peaks = append(peaks, i)
		}
	}
	if len(peaks) != 2 || peaks[0] != bandOf(48000, 500) || peaks[1] != bandOf(48000, 4000) {
		t.Fatalf("peaks %v, want bands %d and %d", peaks, bandOf(48000, 500), bandOf(48000, 4000))
	}
}

func TestSilenceFloors(t *testing.T) {
	z := make([]float32, 5000)
	for i, v := range analyze(t, 48000, z, z) {
		if v != FloorDB {
			t.Fatalf("band %d = %v, want %v", i, v, FloorDB)
		}
	}
}

func noise(seed int64, n int) []float32 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(r.Float64()*0.5 - 0.25)
	}
	return out
}

func feedCorr(t *testing.T, l, r []float32) (float64, bool) {
	t.Helper()
	c, err := NewCorrelation(48000)
	if err != nil {
		t.Fatal(err)
	}
	c.Process(l, r)
	return c.Value()
}

func TestCorrelation(t *testing.T) {
	n := noise(1, 48000)
	inv := make([]float32, len(n))
	for i := range n {
		inv[i] = -n[i]
	}
	if r, ok := feedCorr(t, n, n); !ok || math.Abs(r-1) > 1e-6 {
		t.Fatalf("identical: r=%v ok=%v", r, ok)
	}
	if r, ok := feedCorr(t, n, inv); !ok || math.Abs(r+1) > 1e-6 {
		t.Fatalf("inverted: r=%v ok=%v", r, ok)
	}
	if r, ok := feedCorr(t, noise(1, 48000*3), noise(2, 48000*3)); !ok || math.Abs(r) >= 0.1 {
		t.Fatalf("independent: r=%v ok=%v", r, ok)
	}
	z := make([]float32, 48000)
	if _, ok := feedCorr(t, z, z); ok {
		t.Fatal("silence reported ok")
	}
	// One silent channel is not ok either.
	if _, ok := feedCorr(t, n, z); ok {
		t.Fatal("one silent channel reported ok")
	}
}

func TestCorrelationResetAndRecovery(t *testing.T) {
	c, _ := NewCorrelation(48000)
	n := noise(3, 48000)
	c.Process(n, n)
	c.Reset()
	if _, ok := c.Value(); ok {
		t.Fatal("reset did not clear the window")
	}
}

func TestNoAllocationsAfterConstruction(t *testing.T) {
	s, _ := NewSpectrum(48000)
	c, _ := NewCorrelation(48000)
	l, r := noise(4, 256), noise(5, 256)
	var out [Bands]float32
	if got := testing.AllocsPerRun(50, func() { s.Process(l, r); c.Process(l, r) }); got != 0 {
		t.Fatalf("Process allocated %.1f", got)
	}
	if got := testing.AllocsPerRun(20, func() { s.Bands(&out) }); got != 0 {
		t.Fatalf("Bands allocated %.1f", got)
	}
	if got := testing.AllocsPerRun(50, func() { c.Value() }); got != 0 {
		t.Fatalf("Value allocated %.1f", got)
	}
}

func TestDeterministicBits(t *testing.T) {
	l, r := noise(6, 20000), noise(7, 20000)
	run := func() ([Bands]float32, float64) {
		b := analyze(t, 48000, l, r)
		v, _ := feedCorr(t, l, r)
		return b, v
	}
	b1, v1 := run()
	b2, v2 := run()
	for i := range b1 {
		if math.Float32bits(b1[i]) != math.Float32bits(b2[i]) {
			t.Fatalf("band %d differs", i)
		}
	}
	if math.Float64bits(v1) != math.Float64bits(v2) {
		t.Fatal("correlation differs")
	}
}

func TestFrames(t *testing.T) {
	l, r := noise(8, 48000*2+100), noise(9, 48000*2+100)
	a, b := Frames(l, r, 48000, 30), Frames(l, r, 48000, 30)
	if len(a) != 60 {
		t.Fatalf("got %d frames, want 60 for 2 s at 30 fps", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("frame %d differs between runs", i)
		}
	}
	if !a[59].HasCorrelation || math.Abs(a[59].Correlation) > 0.2 {
		t.Fatalf("last frame correlation %v ok=%v", a[59].Correlation, a[59].HasCorrelation)
	}
	if Frames(l, r, 48000, 0) != nil || Frames(l, r[:10], 48000, 30) != nil {
		t.Fatal("invalid input should return nil")
	}
}
