package acid

import (
	"math"
	"testing"
)

// The filter is checked before voice clipping and before the master limiter.
func TestFilterStabilityGrid(t *testing.T) {
	for _, rate := range []float64{44_100, 48_000, 96_000} {
		for _, model := range []FilterModel{Diode, Ladder} {
			for _, savage := range []float64{0, 1} {
				for cutoffIndex := 0; cutoffIndex < 64; cutoffIndex++ {
					cutoff := 20 * math.Pow(400, float64(cutoffIndex)/63)
					g := calibratedFilterG(math.Tan(math.Pi*cutoff/(2*rate)), model)
					G := g / (1 + g)
					for resonanceIndex := 0; resonanceIndex < 64; resonanceIndex++ {
						resonance := float64(resonanceIndex) / 63
						var state filterState
						k := 17 * resonance * (1 + .25*savage)
						if model == Ladder {
							k = 4 * resonance * (1 + .25*savage)
						}
						for sample := 0; sample < 256; sample++ {
							input := 1.0
							if sample&1 != 0 {
								input = -1
							}
							var output float64
							if model == Diode {
								output = state.processDiode(input, G, g, k, savage)
							} else {
								output = state.processLadder(input, G, g, k, savage)
							}
							if math.IsNaN(output) || math.IsInf(output, 0) || math.Abs(output) > 4 {
								t.Fatalf("unstable filter: rate=%g model=%d savage=%g cutoff=%g resonance=%g sample=%d output=%g", rate, model, savage, cutoff, resonance, sample, output)
							}
						}
					}
				}
			}
		}
	}
}

func TestLadderLinearResponse(t *testing.T) {
	for _, rate := range []float64{44_100, 48_000, 96_000} {
		oversampledRate := 2 * rate
		for _, cutoff := range []float64{100, 600, 8000} {
			g := math.Tan(math.Pi * cutoff / oversampledRate)
			G := g / (1 + g)
			for _, frequency := range []float64{cutoff / 2, cutoff, cutoff * 1.5} {
				var state filterState
				var outputSin, outputCos, inputSin, inputCos float64
				var sinSquared, cosSquared, sinCos float64
				const warmup, measured = 16384, 16384
				for sample := 0; sample < warmup+measured; sample++ {
					phase := 2 * math.Pi * frequency * float64(sample) / oversampledRate
					sine, cosine := math.Sin(phase), math.Cos(phase)
					input := .01 * sine
					output := state.processLadder(input, G, g, 0, 0)
					if sample >= warmup {
						outputSin += output * sine
						outputCos += output * cosine
						inputSin += input * sine
						inputCos += input * cosine
						sinSquared += sine * sine
						cosSquared += cosine * cosine
						sinCos += sine * cosine
					}
				}
				determinant := sinSquared*cosSquared - sinCos*sinCos
				outputA := (outputSin*cosSquared - outputCos*sinCos) / determinant
				outputB := (outputCos*sinSquared - outputSin*sinCos) / determinant
				inputA := (inputSin*cosSquared - inputCos*sinCos) / determinant
				inputB := (inputCos*sinSquared - inputSin*sinCos) / determinant
				measuredGain := math.Hypot(outputA, outputB) / math.Hypot(inputA, inputB)
				warpedRatio := math.Tan(math.Pi*frequency/oversampledRate) / g
				wantGain := math.Pow(1/math.Sqrt(1+warpedRatio*warpedRatio), 4)
				errorDB := 20 * math.Log10(measuredGain/wantGain)
				if math.Abs(errorDB) > .1 {
					t.Fatalf("ladder response %.3f dB from bilinear four-pole reference: rate=%g cutoff=%g frequency=%g", errorDB, rate, cutoff, frequency)
				}
			}
		}
	}
}

func TestFilterSelfOscillation(t *testing.T) {
	for _, rate := range []float64{44_100, 48_000, 96_000} {
		g := math.Tan(math.Pi * 1000 / (2 * rate))
		G := g / (1 + g)
		for _, model := range []FilterModel{Diode, Ladder} {
			threshold := 17.0
			if model == Ladder {
				threshold = 4
			}
			for _, factor := range []float64{.95, 1} {
				state := filterState{state: [4]float64{.1, .1, .1, .1}, last: .1}
				var power float64
				count := int(2 * rate)
				for sample := 0; sample < count; sample++ {
					var output float64
					if model == Diode {
						output = state.processDiode(0, G, g, threshold*factor, 0)
					} else {
						output = state.processLadder(0, G, g, threshold*factor, 0)
					}
					if sample >= count*3/4 {
						power += output * output
					}
				}
				rms := math.Sqrt(power / float64(count/4))
				if factor == 1 && rms < .001 {
					t.Fatalf("filter did not self-oscillate: rate=%g model=%d rms=%g", rate, model, rms)
				}
				if factor < 1 && rms > .001 {
					t.Fatalf("filter oscillated below threshold: rate=%g model=%d rms=%g", rate, model, rms)
				}
			}
		}
	}
}

func TestDecision0002AcidFilterCutoffGate(t *testing.T) {
	for _, sampleRate := range []float64{44_100, 48_000, 96_000} {
		for _, requestedCutoff := range []float64{100, 600, 2_000, 8_000} {
			for _, filter := range []struct {
				name  string
				model FilterModel
			}{{"Ladder", Ladder}, {"Diode", Diode}} {
				measured := measureFilterCutoff(filter.model, sampleRate, requestedCutoff)
				errorPercent := math.Abs(measured/requestedCutoff-1) * 100
				if sampleRate == 48_000 && requestedCutoff == 2_000 {
					t.Logf("%s 2 kHz cutoff: %.2f Hz (%.3f%% error)", filter.name, measured, errorPercent)
				}
				if errorPercent > 2 {
					t.Errorf("%s at %.0f Hz sample rate measures %.2f Hz (%.1f%% from %.0f Hz)", filter.name, sampleRate, measured, errorPercent, requestedCutoff)
				}
			}
		}
	}
}

func measureFilterCutoff(model FilterModel, sampleRate, requestedCutoff float64) float64 {
	low, high := 20.0, sampleRate/2
	for iteration := 0; iteration < 20; iteration++ {
		frequency := (low + high) / 2
		if filterResponseDB(model, sampleRate, requestedCutoff, frequency) > -3 {
			low = frequency
		} else {
			high = frequency
		}
	}
	return (low + high) / 2
}

func filterResponseDB(model FilterModel, sampleRate, cutoff, frequency float64) float64 {
	oversampledRate := 2 * sampleRate
	baseG := math.Tan(math.Pi * cutoff / oversampledRate)
	voice := Voice{params: Params{Filter: model}}
	if model == Ladder {
		voice.filterBlend = 1
	}
	var outputSin, outputCos float64
	var sinSquared, cosSquared, sinCos float64
	const amplitude, warmup, measured = 0.001, 8192, 16384
	for sample := 0; sample < warmup+measured; sample++ {
		phase := 2 * math.Pi * frequency * float64(sample) / oversampledRate
		input := amplitude * math.Sin(phase)
		output := voice.filter(input, baseG)
		if sample >= warmup {
			sine, cosine := math.Sin(phase), math.Cos(phase)
			outputSin += output * sine
			outputCos += output * cosine
			sinSquared += sine * sine
			cosSquared += cosine * cosine
			sinCos += sine * cosine
		}
	}
	determinant := sinSquared*cosSquared - sinCos*sinCos
	outputA := (outputSin*cosSquared - outputCos*sinCos) / determinant
	outputB := (outputCos*sinSquared - outputSin*sinCos) / determinant
	return 20 * math.Log10(math.Hypot(outputA, outputB)/amplitude)
}
