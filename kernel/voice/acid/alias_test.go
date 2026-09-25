package acid

import (
	"math"
	"math/cmplx"
	"testing"
)

// A frequency close to 2 kHz is deliberately used instead of exactly 2 kHz:
// at 48 kHz, every alias of a 2 kHz harmonic lands on another harmonic bin.
func TestSawAliasing(t *testing.T) {
	const sampleRate, frequency = 48_000.0, 2_003.0
	naiveDB := worstNonharmonicDB(frequency, sampleRate, func(phase, _ float64) float64 {
		return 2*phase - 1
	})
	if naiveDB <= -60 {
		t.Fatalf("naive saw did not expose aliasing: %.2f dB", naiveDB)
	}
	bank := bankForSampleRate(int(sampleRate))
	sawDB := worstNonharmonicDB(frequency, sampleRate, func(phase, _ float64) float64 {
		return bank.saw(phase, math.Log2(frequency))
	})
	t.Logf("naive saw %.2f dB, Cicada saw %.2f dB relative to fundamental", naiveDB, sawDB)
	if sawDB > -60 {
		t.Fatalf("saw alias energy %.2f dB exceeds -60 dB gate", sawDB)
	}
	for _, width := range []float64{0.23, 0.5} {
		pulseDB := worstNonharmonicDB(frequency, sampleRate, func(phase, _ float64) float64 {
			return bank.pulse(phase, math.Log2(frequency), width)
		})
		t.Logf("pulse width %.2f: %.2f dB relative to fundamental", width, pulseDB)
		if pulseDB > -60 {
			t.Fatalf("pulse width %.2f alias energy %.2f dB exceeds -60 dB gate", width, pulseDB)
		}
	}
	for _, rate := range []float64{44_100, 96_000} {
		otherBank := bankForSampleRate(int(rate))
		otherDB := worstNonharmonicDB(frequency, rate, func(phase, _ float64) float64 {
			return otherBank.saw(phase, math.Log2(frequency))
		})
		t.Logf("%.0f Hz saw: %.2f dB relative to fundamental", rate, otherDB)
		if otherDB > -60 {
			t.Fatalf("%.0f Hz saw alias energy %.2f dB exceeds -60 dB gate", rate, otherDB)
		}
	}
	for _, noteFrequency := range []float64{40.7, 100.3, 440.7, 8_003} {
		otherDB := worstNonharmonicDB(noteFrequency, sampleRate, func(phase, _ float64) float64 {
			return bank.saw(phase, math.Log2(noteFrequency))
		})
		t.Logf("%.1f Hz saw: %.2f dB relative to fundamental", noteFrequency, otherDB)
		if otherDB > -60 {
			t.Fatalf("%.1f Hz saw alias energy %.2f dB exceeds -60 dB gate", noteFrequency, otherDB)
		}
	}
}

func TestSawTableSwitchIsContinuous(t *testing.T) {
	bank := bankForSampleRate(48_000)
	for bin := 1; bin < oscillatorBins-1; bin++ {
		boundary := oscillatorMinPitchLog + float64(bin)/12
		for _, phase := range []float64{0.13, 0.37, 0.71} {
			before := bank.saw(phase, boundary-1e-9)
			after := bank.saw(phase, boundary+1e-9)
			if math.Abs(after-before) > 1e-5 {
				t.Fatalf("table switch at bin %d phase %.2f jumps by %.8f", bin, phase, after-before)
			}
		}
	}
}

func worstNonharmonicDB(frequency, sampleRate float64, oscillator func(float64, float64) float64) float64 {
	const samples = 1 << 16
	const excludedBins = 8
	const a0, a1, a2, a3 = 0.35875, 0.48829, 0.14128, 0.01168
	fftInput := make([]complex128, samples)
	phase, delta := 0.0, frequency/sampleRate
	for i := range fftInput {
		angle := 2 * math.Pi * float64(i) / float64(samples-1)
		window := a0 - a1*math.Cos(angle) + a2*math.Cos(2*angle) - a3*math.Cos(3*angle)
		fftInput[i] = complex(oscillator(phase, delta)*window, 0)
		phase = fraction(phase + delta)
	}
	aliasFFT(fftInput)
	fundamentalBin := frequency * samples / sampleRate
	var fundamental, alias float64
	for bin := 8; bin <= samples/2; bin++ {
		magnitude := cmplx.Abs(fftInput[bin])
		if math.Abs(float64(bin)-fundamentalBin) <= excludedBins && magnitude > fundamental {
			fundamental = magnitude
		}
		harmonic := false
		for order := 1; float64(order)*frequency < sampleRate/2; order++ {
			if math.Abs(float64(bin)-float64(order)*fundamentalBin) <= excludedBins {
				harmonic = true
				break
			}
		}
		if !harmonic && magnitude > alias {
			alias = magnitude
		}
	}
	if fundamental == 0 {
		return math.Inf(1)
	}
	return 20 * math.Log10(alias/fundamental)
}

func aliasFFT(data []complex128) {
	n := len(data)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}
	for span := 2; span <= n; span <<= 1 {
		angle := -2 * math.Pi / float64(span)
		root := cmplx.Rect(1, angle)
		for base := 0; base < n; base += span {
			rotation := complex(1, 0)
			for i := 0; i < span/2; i++ {
				even, odd := data[base+i], data[base+i+span/2]*rotation
				data[base+i], data[base+i+span/2] = even+odd, even-odd
				rotation *= root
			}
		}
	}
}
