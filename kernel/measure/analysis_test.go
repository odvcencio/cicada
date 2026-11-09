package measure

import (
	"errors"
	"math"
	"math/cmplx"
	"sort"
	"time"
)

const (
	sampleRate = 48_000
	// dbFloor stands in for minus infinity, which JSON cannot hold.
	dbFloor = -200.0

	aliasSkip      = 12_000 // 0.25 s
	aliasSize      = 1 << 15
	aliasExcluded  = 8 // bins on each side of an expected partial
	aliasLowCutHz  = 20.0
	thdFrequency   = 1000.0
	thdPeriods     = 400
	thdFrames      = thdPeriods * sampleRate / 1000 // 19200, whole periods
	blockFrames    = 128
	cpuRuns        = 9
	cpuBlocksPerRn = 200
)

// Levels are peak amplitudes for the THD+N sine: -18, -6 and 0 dBFS.
var thdLevels = []struct {
	Label     string
	Amplitude float64
}{{"-18dBFS", math.Pow(10, -18.0/20)}, {"-6dBFS", math.Pow(10, -6.0/20)}, {"0dBFS", 1}}

func clampDB(db float64) float64 {
	if math.IsNaN(db) || db < dbFloor {
		return dbFloor
	}
	if db > 200 {
		return 200
	}
	return db
}

func powerDB(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 200
	}
	if numerator <= 0 {
		return dbFloor
	}
	return clampDB(10 * math.Log10(numerator/denominator))
}

// fft is an in-place iterative radix-2 transform. It follows the helper in
// kernel/voice/acid/alias_test.go, which is unexported in a test file.
func fft(data []complex128) {
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
		root := cmplx.Rect(1, -2*math.Pi/float64(span))
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

func blackmanHarris(n int) []float64 {
	const a0, a1, a2, a3 = 0.35875, 0.48829, 0.14128, 0.01168
	w := make([]float64, n)
	for i := range w {
		angle := 2 * math.Pi * float64(i) / float64(n-1)
		w[i] = a0 - a1*math.Cos(angle) + a2*math.Cos(2*angle) - a3*math.Cos(3*angle)
	}
	return w
}

var aliasWindow = blackmanHarris(aliasSize)

func midiHz(note int) float64 { return 440 * math.Exp2(float64(note-69)/12) }

// partialSet returns the expected partial frequencies for one note, given
// the measured fundamental. A nil set means integer multiples of f0.
type partialSet func(note int, f0 float64) []float64

func harmonics(divisor float64) partialSet {
	return func(_ int, f0 float64) []float64 {
		base := f0 / divisor
		var out []float64
		for k := 1; float64(k)*base < sampleRate/2; k++ {
			out = append(out, float64(k)*base)
		}
		return out
	}
}

type aliasMeasurement struct {
	DB          float64
	F0          float64
	Partials    int
	Fundamental float64 // dB below the loudest bin, to show the pitch was found
}

// aliasing analyzes aliasSize samples of a steady note.
func aliasing(x []float64, note int, partials partialSet) (aliasMeasurement, error) {
	if len(x) != aliasSize {
		return aliasMeasurement{}, errors.New("aliasing needs exactly aliasSize samples")
	}
	if partials == nil {
		partials = harmonics(1)
	}
	spectrum := make([]complex128, aliasSize)
	for i, s := range x {
		spectrum[i] = complex(s*aliasWindow[i], 0)
	}
	fft(spectrum)
	half := aliasSize / 2
	power := make([]float64, half+1)
	loudest := 0.0
	for k := range power {
		a := cmplx.Abs(spectrum[k])
		power[k] = a * a
		loudest = math.Max(loudest, power[k])
	}
	if loudest == 0 {
		return aliasMeasurement{}, errors.New("voice rendered silence")
	}
	binHz := float64(sampleRate) / aliasSize
	nominal := midiHz(note)
	lo, hi := int(nominal*.94/binHz), int(nominal*1.06/binHz)+1
	peak := lo
	for k := lo; k <= hi && k < half; k++ {
		if power[k] > power[peak] {
			peak = k
		}
	}
	// Parabolic interpolation on log power places f0 within a fraction of a bin.
	f0 := float64(peak) * binHz
	if peak > 0 && peak < half && power[peak-1] > 0 && power[peak+1] > 0 && power[peak] > 0 {
		a, b, c := math.Log(power[peak-1]), math.Log(power[peak]), math.Log(power[peak+1])
		if d := a - 2*b + c; d != 0 {
			f0 = (float64(peak) + .5*(a-c)/d) * binHz
		}
	}
	if power[peak] < loudest*1e-6 {
		return aliasMeasurement{}, errors.New("fundamental not found near the nominal pitch")
	}
	marked := make([]bool, half+1)
	set := partials(note, f0)
	for _, f := range set {
		center := f / binHz
		for k := int(math.Ceil(center - aliasExcluded)); float64(k) <= center+aliasExcluded; k++ {
			if k >= 0 && k <= half {
				marked[k] = true
			}
		}
	}
	var in, out float64
	for k := int(math.Ceil(aliasLowCutHz / binHz)); k <= half; k++ {
		if marked[k] {
			in += power[k]
		} else {
			out += power[k]
		}
	}
	return aliasMeasurement{
		DB: powerDB(out, in), F0: f0, Partials: len(set),
		Fundamental: powerDB(power[peak], loudest),
	}, nil
}

// fitTone fits a*sin+b*cos at an exact frequency in Hz by least squares and
// returns the fitted amplitude and the residual power over the fitted power.
// The residual includes DC, harmonics and noise.
func fitTone(x []float64, hz float64) (amplitude, residualToSignal float64) {
	var ss, cc, sc, ys, yc float64
	w := 2 * math.Pi * hz / sampleRate
	for i, v := range x {
		s, c := math.Sincos(w * float64(i))
		ss += s * s
		cc += c * c
		sc += s * c
		ys += v * s
		yc += v * c
	}
	det := ss*cc - sc*sc
	a, b := (ys*cc-yc*sc)/det, (yc*ss-ys*sc)/det
	var residual, signal float64
	for i, v := range x {
		s, c := math.Sincos(w * float64(i))
		fit := a*s + b*c
		d := v - fit
		residual += d * d
		signal += fit * fit
	}
	return math.Hypot(a, b), residual / math.Max(signal, 1e-300)
}

func thdnDB(x []float64) float64 {
	_, ratio := fitTone(x, thdFrequency)
	if ratio <= 0 {
		return dbFloor
	}
	return clampDB(10 * math.Log10(ratio))
}

// rmsDBFS returns the combined RMS of both channels in dB re 1.0.
func rmsDBFS(l, r []float32) float64 {
	var sum float64
	for i := range l {
		sum += float64(l[i])*float64(l[i]) + float64(r[i])*float64(r[i])
	}
	if sum == 0 {
		return dbFloor
	}
	return clampDB(10 * math.Log10(sum/float64(2*len(l))))
}

func medianNS(run func() time.Duration) float64 {
	samples := make([]float64, cpuRuns)
	for i := range samples {
		samples[i] = float64(run().Nanoseconds()) / cpuBlocksPerRn
	}
	sort.Float64s(samples)
	return samples[cpuRuns/2]
}
