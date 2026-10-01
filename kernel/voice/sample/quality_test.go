package sample

import (
	"fmt"
	"math"
	"testing"
)

// All frequencies are cycles per source frame. The declared passband ends
// at .38/bankRatio, transition at .5/bankRatio, and cutoff is .45/bankRatio.
// In-band tests fit the output fundamental by least squares, then measure
// total residual RMS (including harmonics, images and float32 quantization).
// Stopband tests use a 33-tone sweep and measure total leaked output RMS,
// relative to a full-scale-equivalent in-band tone. This bounds each alias
// more conservatively than selecting one FFT bin. No startup/end transient
// is included: every measurement discards 1024 rendered frames.
func TestSampleSRCAliasAndDistortionTargets(t *testing.T) {
	cases := []struct {
		ratio       float64
		source, out int
	}{
		{.25, 48000, 48000}, {.5, 48000, 48000}, {.75, 48000, 48000},
		{1, 48000, 48000}, {1.25, 48000, 48000}, {1.5, 48000, 48000},
		{2, 48000, 48000}, {3, 48000, 48000}, {4, 48000, 48000},
		{44100.0 / 48000, 44100, 48000}, {48000.0 / 44100, 48000, 44100},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%.9f_%d_to_%d", tc.ratio, tc.source, tc.out)
		t.Run(name, func(t *testing.T) {
			// Use the public pitch path, including fine tune and sample rates.
			probe, _ := renderQualityTone(t, tc.ratio, tc.source, tc.out, .01, 1)
			bank := probe.bank
			worstNoise, ripple := math.Inf(-1), 0.0
			for _, frequency := range []float64{.02, .11, .23, .38} {
				v, data := renderQualityTone(t, tc.ratio, tc.source, tc.out, frequency/bank.ratio, 16384)
				amplitude, noiseDB := toneResidual(data, frequency/bank.ratio*v.Ratio())
				worstNoise = max(worstNoise, noiseDB)
				ripple = max(ripple, math.Abs(20*math.Log10(amplitude/.5)))
			}
			// Below unity, total in-band residual also bounds all unwanted
			// interpolation images. Unity introduces no SRC alias at all.
			aliasDB := worstNoise
			if probe.Ratio() == 1 {
				aliasDB = math.Inf(-1)
			} else if probe.Ratio() > 1 {
				start := .5 / probe.Ratio()
				for tone := 0; tone <= 32; tone++ {
					frequency := start + (.4999-start)*float64(tone)/32
					_, data := renderQualityTone(t, tc.ratio, tc.source, tc.out, frequency, 4096)
					var power float64
					for _, x := range data {
						power += float64(x) * float64(x)
					}
					aliasDB = max(aliasDB, 10*math.Log10(power/float64(len(data))/.125))
				}
			}
			status := "PASS"
			if aliasDB > -80 || worstNoise > -90 || ripple > .05 {
				status = "MISS"
			}
			fmt.Printf("METRIC SRC ratio=%.9f rates=%d->%d taps=%d bank=%.3f alias_rejection_db=%.2f thdn_db=%.2f ripple_db=%.5f status=%s\n",
				probe.Ratio(), tc.source, tc.out, probe.KernelTaps(), bank.ratio, -aliasDB, worstNoise, ripple, status)
			// Every listed conversion is qualified. Keep explicit gates so a
			// future regression fails, rather than selecting passing results.
			if aliasDB > -80 {
				t.Errorf("alias %.2f dB exceeds -80 dB with %d taps", aliasDB, probe.KernelTaps())
			}
			if worstNoise > -90 {
				t.Errorf("THD+N %.2f dB exceeds -90 dB with %d taps", worstNoise, probe.KernelTaps())
			}
			if ripple > .05 {
				t.Errorf("passband ripple %.5f dB exceeds 0.05 dB", ripple)
			}
		})
	}
}

func renderQualityTone(t *testing.T, ratio float64, sourceRate, outputRate int, frequency float64, frames int) (*Voice, []float32) {
	t.Helper()
	const guard = 1024
	length := int(math.Ceil(float64(frames+guard)*ratio)) + 2048
	r := Region{Left: make([]float32, length), SampleRate: sourceRate, RootKey: 60, End: length}
	for i := range r.Left {
		r.Left[i] = float32(.5 * math.Sin(2*math.Pi*frequency*float64(i)+.31))
	}
	v, err := New(outputRate, r)
	if err != nil {
		t.Fatal(err)
	}
	if ratio != float64(sourceRate)/float64(outputRate) {
		if err := v.SetParams(Params{Gain: 1, FineTuneCents: 1200 * math.Log2(ratio*float64(outputRate)/float64(sourceRate))}); err != nil {
			t.Fatal(err)
		}
	}
	trigger(t, v, 60, 127)
	for i := 0; i < guard; i++ {
		v.NextStereo()
	}
	data := make([]float32, frames)
	for i := range data {
		data[i], _ = v.NextStereo()
	}
	return v, data
}

func toneResidual(data []float32, frequency float64) (amplitude, noiseDB float64) {
	var ss, cc, sc, ys, yc float64
	for i, x := range data {
		s, c := math.Sincos(2 * math.Pi * frequency * float64(i))
		ss += s * s
		cc += c * c
		sc += s * c
		ys += float64(x) * s
		yc += float64(x) * c
	}
	determinant := ss*cc - sc*sc
	a, b := (ys*cc-yc*sc)/determinant, (yc*ss-ys*sc)/determinant
	var residual, signal float64
	for i, x := range data {
		s, c := math.Sincos(2 * math.Pi * frequency * float64(i))
		fit := a*s + b*c
		difference := float64(x) - fit
		residual += difference * difference
		signal += fit * fit
	}
	return math.Hypot(a, b), 10 * math.Log10(residual/signal)
}

func TestSampleQualityMeasurementDetectsLinearAliasing(t *testing.T) {
	const frames = 16384
	data := make([]float32, frames)
	// Linear interpolation of a .38-cycle source tone at ratio .75 has
	// strong images. The measurement must reject it by a wide margin.
	for i := range data {
		phase := float64(i) * .75
		base := math.Floor(phase)
		fraction := phase - base
		data[i] = float32((1-fraction)*math.Sin(2*math.Pi*.38*base) + fraction*math.Sin(2*math.Pi*.38*(base+1)))
	}
	_, noiseDB := toneResidual(data, .38*.75)
	if noiseDB < -30 {
		t.Fatalf("measurement failed to detect linear images: %.2f dB", noiseDB)
	}
}
