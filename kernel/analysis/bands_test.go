package analysis

import (
	"math"
	"testing"
)

// totalDB adds the bands' power and returns it in dB.
func totalDB(bands [Bands]float32) float64 {
	var p float64
	for _, v := range bands {
		p += math.Pow(10, float64(v)/10)
	}
	return 10 * math.Log10(p)
}

func bandCentre(rate, i int) float64 {
	low, high := BandHz(rate, i)
	return math.Sqrt(low * high)
}

// The bands split the spectrum's power between them, so a sine's bands add up
// to its level wherever it sits, including where bands are narrower than one
// FFT bin.
func TestBandsAddUpToTheSignalPower(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		for _, hz := range []float64{50, 80, 200, 300, 1000, 10000} {
			x := sine(hz, 1, rate, 2*windowSize(rate))
			if got := totalDB(analyze(t, rate, x, x)); math.Abs(got) > 0.1 {
				t.Errorf("rate %d, %.0f Hz full-scale sine: bands add up to %.2f dB, want 0 +-0.1", rate, hz, got)
			}
		}
	}
}

// A sine in the middle of a band reads its level once the band is wide enough
// to hold the four bins a tone occupies.
func TestSineInTheMiddleOfAWideBandReadsItsLevel(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		for i := 0; i < Bands; i++ {
			if low, _ := BandHz(rate, i); low < 350 {
				continue
			}
			x := sine(bandCentre(rate, i), 1, rate, 2*windowSize(rate))
			if got := analyze(t, rate, x, x)[i]; math.Abs(float64(got)) > 0.15 {
				t.Errorf("rate %d, band %d (%.0f Hz): reads %.2f dB, want 0 +-0.15", rate, i, bandCentre(rate, i), got)
			}
		}
	}
}

// Below about 250 Hz the bands are narrower than a tone's four bins, so a low
// tone spreads over its neighbours. This pins how far: the tone's own band
// reads well below its level while the bands still add up to it.
func TestLowToneSpreadsOverNeighbouringBands(t *testing.T) {
	for _, c := range []struct {
		hz, want float64
	}{{50, -5.56}, {200, -0.77}} {
		x := sine(c.hz, 1, 48000, 8192)
		bands := analyze(t, 48000, x, x)
		if got := float64(bands[bandOf(48000, c.hz)]); math.Abs(got-c.want) > 0.3 {
			t.Errorf("%.0f Hz full-scale sine reads %.2f dB in its band, want %.2f +-0.3", c.hz, got, c.want)
		}
		if got := totalDB(bands); math.Abs(got) > 0.1 {
			t.Errorf("%.0f Hz full-scale sine: bands add up to %.2f dB, want 0 +-0.1", c.hz, got)
		}
	}
}

// White noise has the same power in every hertz, so each band's power divided
// by its width must be the same from the 2 Hz wide bands at the bottom to the
// 2 kHz wide bands at the top. Reading one whole bin for a band narrower than
// a bin would lift the lowest bands by up to 7 dB.
func TestSteadyNoiseReadsEvenlyAcrossBands(t *testing.T) {
	const (
		rate   = 48000
		frames = 200
	)
	s, err := NewSpectrum(rate)
	if err != nil {
		t.Fatal(err)
	}
	x := noise(7, frames*windowSize(rate))
	var sum [Bands]float64
	for f := 0; f < frames; f++ {
		block := x[f*windowSize(rate) : (f+1)*windowSize(rate)]
		s.Process(block, block)
		var out [Bands]float32
		s.Bands(&out)
		for i, v := range out {
			sum[i] += math.Pow(10, float64(v)/10)
		}
	}
	density := func(i int) float64 {
		low, high := BandHz(rate, i)
		return 10 * math.Log10(sum[i]/frames/(high-low))
	}
	var ref float64
	for i := 40; i < 56; i++ {
		ref += density(i) / 16
	}
	for i := 0; i < Bands; i++ {
		if d := density(i) - ref; math.Abs(d) > 1.5 {
			t.Errorf("band %d: noise density is %.2f dB from the 1.6 to 8 kHz bands, want within 1.5", i, d)
		}
	}
}

// Finite samples must never produce a level that JSON cannot carry.
func TestHugeSamplesStayFinite(t *testing.T) {
	x := make([]float32, 8192)
	for i := range x {
		x[i] = math.MaxFloat32
	}
	for i, v := range analyze(t, 48000, x, x) {
		if f := float64(v); math.IsNaN(f) || math.IsInf(f, 0) {
			t.Fatalf("band %d = %v", i, v)
		}
	}
}
