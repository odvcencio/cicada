package modalfit

import (
	"math"
	"strings"
	"testing"
)

func TestFitKnownInharmonicModes(t *testing.T) {
	for _, rate := range []int{16000, 44100, 48000, 96000} {
		frequencies := []float64{440, 1193, 2179}
		decays := []float64{.6, .3, .18}
		weights := []float64{.7, .5, .3}
		pcm := make([]float32, rate/2)
		for i := range pcm {
			time := float64(i) / float64(rate)
			for mode, freq := range frequencies {
				pcm[i] += float32(weights[mode] * math.Exp(-math.Log(1000)*time/decays[mode]) * math.Sin(2*math.Pi*freq*time))
			}
		}
		model, err := Fit(pcm, rate, strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		if len(model.Modes) != 3 {
			t.Fatalf("rate %d: %d modes: %+v", rate, len(model.Modes), model.Modes)
		}
		for i, mode := range model.Modes {
			if math.Abs(1200*math.Log2(mode.FrequencyHz/frequencies[i])) > 10 {
				t.Errorf("rate %d mode %d: frequency %f want %f", rate, i, mode.FrequencyHz, frequencies[i])
			}
			if math.Abs(mode.T60/decays[i]-1) > .15 || mode.DecayConfidence < .9 {
				t.Errorf("rate %d mode %d: T60 %f want %f, R² %f", rate, i, mode.T60, decays[i], mode.DecayConfidence)
			}
		}
		t.Logf("rate %d: modes %+v, excitation noise %.4f", rate, model.Modes, model.NoiseMix)
	}
}

func TestFitRejectsInvalidHits(t *testing.T) {
	if _, err := Fit(make([]float32, 4800), 48000, strings.Repeat("a", 64)); err == nil {
		t.Fatal("silence accepted")
	}
	if _, err := Fit([]float32{1}, 48000, strings.Repeat("a", 64)); err == nil {
		t.Fatal("short hit accepted")
	}
	pcm := make([]float32, 4800)
	pcm[0] = float32(math.NaN())
	if _, err := Fit(pcm, 48000, strings.Repeat("a", 64)); err == nil {
		t.Fatal("NaN accepted")
	}
	if _, err := Fit(pcm, 48000, "missing"); err == nil {
		t.Fatal("missing source checksum accepted")
	}
}
